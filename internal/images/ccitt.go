package images

import (
	"errors"
	"strings"
)

// CCITT Group 3 / Group 4 fax decoding (ITU-T T.4 / T.6), written from
// the recommendations' code tables.

type ccittParams struct {
	K          int
	Columns    int
	Rows       int
	BlackIs1   bool
	ByteAlign  bool
	EndOfBlock bool
}

// Run-length code tables: "bits:run" pairs.
const whiteCodes = `00110101:0 000111:1 0111:2 1000:3 1011:4 1100:5 1110:6 1111:7 10011:8 10100:9
00111:10 01000:11 001000:12 000011:13 110100:14 110101:15 101010:16 101011:17 0100111:18
0001100:19 0001000:20 0010111:21 0000011:22 0000100:23 0101000:24 0101011:25 0010011:26
0100100:27 0011000:28 00000010:29 00000011:30 00011010:31 00011011:32 00010010:33 00010011:34
00010100:35 00010101:36 00010110:37 00010111:38 00101000:39 00101001:40 00101010:41 00101011:42
00101100:43 00101101:44 00000100:45 00000101:46 00001010:47 00001011:48 01010010:49 01010011:50
01010100:51 01010101:52 00100100:53 00100101:54 01011000:55 01011001:56 01011010:57 01011011:58
01001010:59 01001011:60 00110010:61 00110011:62 00110100:63
11011:64 10010:128 010111:192 0110111:256 00110110:320 00110111:384 01100100:448 01100101:512
01101000:576 01100111:640 011001100:704 011001101:768 011010010:832 011010011:896 011010100:960
011010101:1024 011010110:1088 011010111:1152 011011000:1216 011011001:1280 011011010:1344
011011011:1408 010011000:1472 010011001:1536 010011010:1600 011000:1664 010011011:1728`

const blackCodes = `0000110111:0 010:1 11:2 10:3 011:4 0011:5 0010:6 00011:7 000101:8 000100:9
0000100:10 0000101:11 0000111:12 00000100:13 00000111:14 000011000:15 0000010111:16 0000011000:17
0000001000:18 00001100111:19 00001101000:20 00001101100:21 00000110111:22 00000101000:23
00000010111:24 00000011000:25 000011001010:26 000011001011:27 000011001100:28 000011001101:29
000001101000:30 000001101001:31 000001101010:32 000001101011:33 000011010010:34 000011010011:35
000011010100:36 000011010101:37 000011010110:38 000011010111:39 000001101100:40 000001101101:41
000011011010:42 000011011011:43 000001010100:44 000001010101:45 000001010110:46 000001010111:47
000001100100:48 000001100101:49 000001010010:50 000001010011:51 000000100100:52 000000110111:53
000000111000:54 000000100111:55 000000101000:56 000001011000:57 000001011001:58 000000101011:59
000000101100:60 000001011010:61 000001100110:62 000001100111:63
0000001111:64 000011001000:128 000011001001:192 000001011011:256 000000110011:320 000000110100:384
000000110101:448 0000001101100:512 0000001101101:576 0000001001010:640 0000001001011:704
0000001001100:768 0000001001101:832 0000001110010:896 0000001110011:960 0000001110100:1024
0000001110101:1088 0000001110110:1152 0000001110111:1216 0000001010010:1280 0000001010011:1344
0000001010100:1408 0000001010101:1472 0000001011010:1536 0000001011011:1600 0000001100100:1664
0000001100101:1728`

// Extended make-up codes shared by both colours.
const extCodes = `00000001000:1792 00000001100:1856 00000001101:1920 000000010010:1984 000000010011:2048
000000010100:2112 000000010101:2176 000000010110:2240 000000010111:2304 000000011100:2368
000000011101:2432 000000011110:2496 000000011111:2560`

type codeKey struct {
	n    uint8
	bits uint16
}

func buildTable(src ...string) map[codeKey]int {
	m := map[codeKey]int{}
	for _, s := range src {
		for _, f := range strings.Fields(s) {
			i := strings.IndexByte(f, ':')
			var bits uint16
			for _, c := range f[:i] {
				bits = bits<<1 | uint16(c-'0')
			}
			v := 0
			for _, c := range f[i+1:] {
				v = v*10 + int(c-'0')
			}
			m[codeKey{uint8(i), bits}] = v
		}
	}
	return m
}

var (
	whiteTable = buildTable(whiteCodes, extCodes)
	blackTable = buildTable(blackCodes, extCodes)
)

type bitReader struct {
	data []byte
	pos  int // bit position
}

func (b *bitReader) bit() (uint16, bool) {
	if b.pos >= len(b.data)*8 {
		return 0, false
	}
	v := uint16(b.data[b.pos>>3]>>(7-b.pos&7)) & 1
	b.pos++
	return v, true
}

func (b *bitReader) peek(n int) (uint16, bool) {
	var v uint16
	for i := 0; i < n; i++ {
		p := b.pos + i
		if p >= len(b.data)*8 {
			return 0, false
		}
		v = v<<1 | uint16(b.data[p>>3]>>(7-p&7))&1
	}
	return v, true
}

func (b *bitReader) align() { b.pos = (b.pos + 7) &^ 7 }

var errCCITT = errors.New("ccitt: invalid code")

// run reads one complete run length (make-up codes plus a terminating code).
func (b *bitReader) run(white bool) (int, error) {
	t := blackTable
	if white {
		t = whiteTable
	}
	total := 0
	for {
		var code uint16
		found := false
		for n := uint8(1); n <= 13; n++ {
			bit, ok := b.bit()
			if !ok {
				return total, errCCITT
			}
			code = code<<1 | bit
			if v, ok := t[codeKey{n, code}]; ok {
				total += v
				found = true
				if v < 64 {
					return total, nil
				}
				break
			}
		}
		if !found {
			return total, errCCITT
		}
	}
}

// 2D mode codes.
const (
	modePass = iota
	modeHoriz
	modeV0
	modeVR1
	modeVR2
	modeVR3
	modeVL1
	modeVL2
	modeVL3
	modeEOL
)

func (b *bitReader) mode() (int, error) {
	// Codes: 1 V0, 011 VR1, 010 VL1, 001 H, 0001 P, 000011 VR2, 000010 VL2,
	// 0000011 VR3, 0000010 VL3, 000000000001 EOL.
	for _, c := range []struct {
		n    int
		bits uint16
		mode int
	}{{1, 1, modeV0}, {3, 3, modeVR1}, {3, 2, modeVL1}, {3, 1, modeHoriz}, {4, 1, modePass},
		{6, 3, modeVR2}, {6, 2, modeVL2}, {7, 3, modeVR3}, {7, 2, modeVL3}, {12, 1, modeEOL}} {
		v, ok := b.peek(c.n)
		if !ok {
			return 0, errCCITT
		}
		if v == c.bits {
			b.pos += c.n
			return c.mode, nil
		}
	}
	return 0, errCCITT
}

// skipEOL consumes an EOL (with optional fill bits) if present.
func (b *bitReader) skipEOL() bool {
	start := b.pos
	zeros := 0
	for {
		v, ok := b.bit()
		if !ok {
			b.pos = start
			return false
		}
		if v == 0 {
			zeros++
			continue
		}
		if zeros >= 11 {
			return true
		}
		b.pos = start
		return false
	}
}

// decodeCCITT returns one byte per pixel: 0 = black, 255 = white.
func decodeCCITT(data []byte, p ccittParams) ([]byte, int, error) {
	w := p.Columns
	if w <= 0 {
		w = 1728
	}
	br := &bitReader{data: data}
	ref := []int{w, w}
	var out []byte
	rows := 0
	for p.Rows <= 0 || rows < p.Rows {
		if br.pos >= len(data)*8-1 {
			break
		}
		if p.ByteAlign {
			br.align()
		}
		twoD := p.K < 0
		if p.K >= 0 {
			eol := br.skipEOL()
			if eol && p.K > 0 {
				tag, ok := br.bit()
				if !ok {
					break
				}
				twoD = tag == 0
			}
		}
		var cur []int
		var err error
		if twoD {
			cur, err = decode2DRow(br, ref, w)
		} else {
			cur, err = decode1DRow(br, w)
		}
		if err != nil {
			if rows > 0 && (p.EndOfBlock || p.Rows <= 0) {
				break // EOFB or trailing garbage after the image
			}
			return out, rows, err
		}
		row := make([]byte, w)
		color := byte(255)
		x := 0
		for _, c := range cur {
			c = min(c, w)
			for ; x < c; x++ {
				row[x] = color
			}
			color = 255 - color
		}
		for ; x < w; x++ {
			row[x] = color
		}
		if p.BlackIs1 {
			for i := range row {
				row[i] = 255 - row[i]
			}
		}
		out = append(out, row...)
		rows++
		ref = append(cur, w, w)
		if rows > 1<<16 {
			break
		}
	}
	return out, rows, nil
}

func decode1DRow(br *bitReader, w int) ([]int, error) {
	var changes []int
	x := 0
	white := true
	for x < w {
		r, err := br.run(white)
		if err != nil {
			return nil, err
		}
		x += r
		changes = append(changes, x)
		white = !white
	}
	return changes, nil
}

func decode2DRow(br *bitReader, ref []int, w int) ([]int, error) {
	var changes []int
	a0 := -1
	white := true
	for a0 < w {
		// b1: first changing element on the reference line right of a0
		// whose colour is opposite to the current colour.
		i := 0
		for i < len(ref) && (ref[i] <= a0 || (i%2 == 0) != white) {
			i++
		}
		b1, b2 := w, w
		if i < len(ref) {
			b1 = ref[i]
		}
		if i+1 < len(ref) {
			b2 = ref[i+1]
		}
		m, err := br.mode()
		if err != nil {
			return nil, err
		}
		switch m {
		case modePass:
			a0 = b2
		case modeHoriz:
			r1, err := br.run(white)
			if err != nil {
				return nil, err
			}
			r2, err := br.run(!white)
			if err != nil {
				return nil, err
			}
			start := max(a0, 0)
			changes = append(changes, start+r1, start+r1+r2)
			a0 = start + r1 + r2
		case modeEOL:
			return nil, errCCITT
		default:
			d := map[int]int{modeV0: 0, modeVR1: 1, modeVR2: 2, modeVR3: 3, modeVL1: -1, modeVL2: -2, modeVL3: -3}[m]
			a1 := b1 + d
			if a1 < 0 || a1 > w {
				return nil, errCCITT
			}
			changes = append(changes, a1)
			a0 = a1
			white = !white
		}
	}
	return changes, nil
}
