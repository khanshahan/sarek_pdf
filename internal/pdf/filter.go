package pdf

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
)

// MaxDecodedSize caps the output of a single stream decode to defend
// against decompression bombs (default 512 MiB).
var MaxDecodedSize int64 = 512 << 20

// ErrUnsupportedFilter is returned for image-only codecs that are passed
// through undecoded (DCT, JPX, JBIG2, CCITT).
var ErrUnsupportedFilter = errors.New("pdf: unsupported filter")

// imageFilters are codecs left encoded; the caller receives the data
// together with the filter name.
var imageFilters = map[Name]bool{
	"DCTDecode": true, "DCT": true, "JPXDecode": true,
	"JBIG2Decode": true, "CCITTFaxDecode": true, "CCF": true,
}

var filterAbbrev = map[Name]Name{
	"AHx": "ASCIIHexDecode", "A85": "ASCII85Decode", "LZW": "LZWDecode",
	"Fl": "FlateDecode", "RL": "RunLengthDecode", "CCF": "CCITTFaxDecode",
	"DCT": "DCTDecode",
}

// DecodeResult is the result of decoding a stream.
type DecodeResult struct {
	Data []byte
	// Remaining is the first undecoded image filter (e.g. "DCTDecode"),
	// empty if the stream was fully decoded.
	Remaining Name
	// RemainingParms are the decode params of the remaining filter.
	RemainingParms Dict
}

// DecodeStream applies all stream filters. Image codecs stop the chain.
func (r *Reader) DecodeStream(s *Stream) (DecodeResult, error) {
	filters, parms := r.filterChain(s.Dict)
	data := s.Raw
	for i, f := range filters {
		if full, ok := filterAbbrev[f]; ok {
			f = full
		}
		if imageFilters[f] {
			return DecodeResult{Data: data, Remaining: f, RemainingParms: parms[i]}, nil
		}
		var err error
		data, err = applyFilter(f, data, parms[i])
		if err != nil {
			return DecodeResult{Data: data}, fmt.Errorf("pdf: %s on %v: %w", f, s.Ref, err)
		}
	}
	return DecodeResult{Data: data}, nil
}

// Decode fully decodes a stream, failing on image codecs.
func (r *Reader) Decode(s *Stream) ([]byte, error) {
	res, err := r.DecodeStream(s)
	if err != nil {
		return res.Data, err
	}
	if res.Remaining != "" {
		return res.Data, fmt.Errorf("%w: %s", ErrUnsupportedFilter, res.Remaining)
	}
	return res.Data, nil
}

func (r *Reader) filterChain(d Dict) ([]Name, []Dict) {
	fo := r.Resolve(d["Filter"])
	if fo == nil {
		fo = r.Resolve(d["F"]) // inline image abbreviation
	}
	po := r.Resolve(d["DecodeParms"])
	if po == nil {
		po = r.Resolve(d["DP"])
	}
	var names []Name
	var parms []Dict
	switch f := fo.(type) {
	case Name:
		names = []Name{f}
		pd, _ := po.(Dict)
		if pa, ok := po.(Array); ok && len(pa) > 0 {
			pd, _ = r.Resolve(pa[0]).(Dict)
		}
		parms = []Dict{pd}
	case Array:
		pa, _ := po.(Array)
		for i, e := range f {
			n, ok := r.Resolve(e).(Name)
			if !ok {
				continue
			}
			names = append(names, n)
			var pd Dict
			if i < len(pa) {
				pd, _ = r.Resolve(pa[i]).(Dict)
			}
			parms = append(parms, pd)
		}
	}
	return names, parms
}

func applyFilter(f Name, data []byte, parms Dict) ([]byte, error) {
	switch f {
	case "FlateDecode":
		out, err := inflate(data)
		if err != nil && len(out) == 0 {
			return nil, err
		}
		return predict(out, parms)
	case "LZWDecode":
		early := 1
		if v, ok := Int(parms["EarlyChange"]); ok {
			early = v
		}
		out, err := lzwDecode(data, early == 1)
		if err != nil && len(out) == 0 {
			return nil, err
		}
		return predict(out, parms)
	case "ASCIIHexDecode":
		return asciiHex(data), nil
	case "ASCII85Decode":
		return ascii85(data)
	case "RunLengthDecode":
		return runLength(data), nil
	case "Crypt":
		return data, nil // identity crypt filter; real crypt handled at load
	}
	return data, fmt.Errorf("%w: %s", ErrUnsupportedFilter, f)
}

// inflate decodes zlib data, tolerating bad checksums, raw deflate and
// truncated streams (returns what was recovered).
func inflate(data []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return inflateRaw(data)
	}
	defer zr.Close()
	var buf bytes.Buffer
	buf.Grow(len(data) * 3)
	_, err = io.Copy(&buf, io.LimitReader(zr, MaxDecodedSize))
	if err != nil && buf.Len() > 0 {
		return buf.Bytes(), nil // checksum / truncation: keep recovered data
	}
	return buf.Bytes(), err
}

func asciiHex(data []byte) []byte {
	out := make([]byte, 0, len(data)/2)
	var hi byte
	half := false
	for _, c := range data {
		if c == '>' {
			break
		}
		v, ok := unhexDigit(c)
		if !ok {
			continue
		}
		if half {
			out = append(out, hi<<4|v)
		} else {
			hi = v
		}
		half = !half
	}
	if half {
		out = append(out, hi<<4)
	}
	return out
}

func ascii85(data []byte) ([]byte, error) {
	if bytes.HasPrefix(data, []byte("<~")) {
		data = data[2:]
	}
	out := make([]byte, 0, len(data)*4/5)
	var group [5]byte
	n := 0
	for _, c := range data {
		if c == '~' {
			break
		}
		if isWhite[c] {
			continue
		}
		if c == 'z' && n == 0 {
			out = append(out, 0, 0, 0, 0)
			continue
		}
		if c < '!' || c > 'u' {
			return out, errors.New("ascii85: invalid character")
		}
		group[n] = c - '!'
		n++
		if n == 5 {
			v := uint32(0)
			for _, g := range group {
				v = v*85 + uint32(g)
			}
			out = append(out, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
			n = 0
		}
	}
	if n > 1 {
		for i := n; i < 5; i++ {
			group[i] = 84
		}
		v := uint32(0)
		for _, g := range group {
			v = v*85 + uint32(g)
		}
		tmp := []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
		out = append(out, tmp[:n-1]...)
	}
	return out, nil
}

func runLength(data []byte) []byte {
	out := make([]byte, 0, len(data)*2)
	for i := 0; i < len(data); {
		l := int(data[i])
		i++
		switch {
		case l == 128:
			return out
		case l < 128:
			end := min(i+l+1, len(data))
			out = append(out, data[i:end]...)
			i = end
		default:
			if i >= len(data) {
				return out
			}
			for k := 0; k < 257-l; k++ {
				out = append(out, data[i])
			}
			i++
		}
	}
	return out
}

// lzwDecode implements the PDF LZW variant (MSB-first, 9–12 bit codes).
func lzwDecode(data []byte, early bool) ([]byte, error) {
	type entry struct {
		prefix int
		suffix byte
		length int
	}
	table := make([]entry, 4096)
	for i := 0; i < 256; i++ {
		table[i] = entry{-1, byte(i), 1}
	}
	next := 258
	width := 9
	var bitBuf uint32
	bits := 0
	pos := 0
	prev := -1
	out := make([]byte, 0, len(data)*3)
	e := 0
	if early {
		e = 1
	}
	emit := func(code int) byte {
		l := table[code].length
		start := len(out)
		out = append(out, make([]byte, l)...)
		c := code
		for i := l - 1; i >= 0; i-- {
			out[start+i] = table[c].suffix
			c = table[c].prefix
		}
		return out[start]
	}
	for {
		for bits < width && pos < len(data) {
			bitBuf = bitBuf<<8 | uint32(data[pos])
			pos++
			bits += 8
		}
		if bits < width {
			return out, nil
		}
		code := int(bitBuf>>(bits-width)) & (1<<width - 1)
		bits -= width
		switch {
		case code == 256:
			next, width, prev = 258, 9, -1
			continue
		case code == 257:
			return out, nil
		}
		if prev == -1 {
			if code > 255 {
				return out, errors.New("lzw: bad first code")
			}
			emit(code)
			prev = code
			continue
		}
		var first byte
		if code < next {
			first = emit(code)
		} else if code == next {
			// KwKwK case.
			pf := emit(prev)
			out = append(out, pf)
			first = pf
		} else {
			return out, errors.New("lzw: invalid code")
		}
		if next < 4096 {
			table[next] = entry{prev, first, table[prev].length + 1}
			next++
		}
		prev = code
		switch {
		case next+e >= 4096:
			width = 12
		case next+e >= 2048:
			width = 12
		case next+e >= 1024:
			width = 11
		case next+e >= 512:
			width = 10
		}
	}
}

// predict reverses TIFF (2) and PNG (10–15) predictors.
func predict(data []byte, parms Dict) ([]byte, error) {
	if parms == nil {
		return data, nil
	}
	pred, _ := Int(parms["Predictor"])
	if pred <= 1 {
		return data, nil
	}
	colors, bpc, cols := 1, 8, 1
	if v, ok := Int(parms["Colors"]); ok && v > 0 {
		colors = v
	}
	if v, ok := Int(parms["BitsPerComponent"]); ok && v > 0 {
		bpc = v
	}
	if v, ok := Int(parms["Columns"]); ok && v > 0 {
		cols = v
	}
	bpp := (colors*bpc + 7) / 8
	rowLen := (colors*bpc*cols + 7) / 8
	if pred == 2 {
		if bpc != 8 {
			return data, nil
		}
		for r := 0; r+rowLen <= len(data); r += rowLen {
			row := data[r : r+rowLen]
			for i := bpp; i < len(row); i++ {
				row[i] += row[i-bpp]
			}
		}
		return data, nil
	}
	out := make([]byte, 0, len(data))
	prev := make([]byte, rowLen)
	for r := 0; r < len(data); r += rowLen + 1 {
		ft := data[r]
		end := min(r+1+rowLen, len(data))
		row := make([]byte, rowLen)
		copy(row, data[r+1:end])
		switch ft {
		case 0:
		case 1:
			for i := bpp; i < rowLen; i++ {
				row[i] += row[i-bpp]
			}
		case 2:
			for i := 0; i < rowLen; i++ {
				row[i] += prev[i]
			}
		case 3:
			for i := 0; i < rowLen; i++ {
				var left byte
				if i >= bpp {
					left = row[i-bpp]
				}
				row[i] += byte((int(left) + int(prev[i])) / 2)
			}
		case 4:
			for i := 0; i < rowLen; i++ {
				var a, c byte
				if i >= bpp {
					a, c = row[i-bpp], prev[i-bpp]
				}
				row[i] += paeth(a, prev[i], c)
			}
		}
		out = append(out, row[:end-r-1]...)
		prev = row
	}
	return out, nil
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
