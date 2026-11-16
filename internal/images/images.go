// Package images converts PDF image XObjects into portable image files
// (JPEG pass-through, PNG for raw samples) using only the standard library.
package images

import (
	"bytes"
	"image"
	"image/color"
	"image/png"

	"github.com/khanshahan/sarek_pdf/internal/pdf"
	"github.com/khanshahan/sarek_pdf/model"
)

// MaxPixels bounds decoded image size (default 100 megapixels).
var MaxPixels = 100_000_000

// Convert produces a model.Image for an image stream. When decode is false
// only geometry and dimensions are filled in.
func Convert(r *pdf.Reader, s *pdf.Stream, bbox model.Rect, decode bool) *model.Image {
	d := s.Dict
	w := r.Int(d["Width"], 0)
	h := r.Int(d["Height"], 0)
	img := &model.Image{BBox: bbox, Width: w, Height: h}
	if !decode || w <= 0 || h <= 0 || w*h > MaxPixels {
		return img
	}
	res, err := r.DecodeStream(s)
	if err != nil && len(res.Data) == 0 {
		return img
	}
	switch res.Remaining {
	case "DCTDecode":
		img.Format, img.Data = "jpeg", res.Data
		return img
	case "JPXDecode":
		img.Format, img.Data = "jp2", res.Data
		return img
	case "JBIG2Decode":
		img.Format, img.Data = "jbig2", res.Data
		return img
	case "CCITTFaxDecode":
		img.Format, img.Data = "ccitt", res.Data
		return img
	}
	if data := toPNG(r, d, res.Data, w, h); data != nil {
		img.Format, img.Data = "png", data
	}
	return img
}

type colorSpace struct {
	n       int    // components
	palette []byte // Indexed: base RGB triples
	cmyk    bool
}

func resolveCS(r *pdf.Reader, o pdf.Object) colorSpace {
	switch v := r.Resolve(o).(type) {
	case pdf.Name:
		switch v {
		case "DeviceGray", "CalGray", "G":
			return colorSpace{n: 1}
		case "DeviceRGB", "CalRGB", "RGB", "Lab":
			return colorSpace{n: 3}
		case "DeviceCMYK", "CMYK":
			return colorSpace{n: 4, cmyk: true}
		}
	case pdf.Array:
		if len(v) == 0 {
			break
		}
		switch r.Name(v[0]) {
		case "ICCBased":
			if s := r.Stream(v[1]); s != nil {
				n := r.Int(s.Dict["N"], 3)
				return colorSpace{n: n, cmyk: n == 4}
			}
		case "Indexed", "I":
			if len(v) < 4 {
				break
			}
			base := resolveCS(r, v[1])
			var lookup []byte
			switch l := r.Resolve(v[3]).(type) {
			case pdf.String:
				lookup = []byte(l)
			case *pdf.Stream:
				lookup, _ = r.Decode(l)
			}
			return colorSpace{n: 1, palette: paletteRGB(lookup, base)}
		case "CalRGB", "Lab":
			return colorSpace{n: 3}
		case "CalGray":
			return colorSpace{n: 1}
		case "Separation", "DeviceN":
			n := 1
			if names := r.Array(v[1]); len(names) > 0 {
				n = len(names)
			}
			return colorSpace{n: n} // rendered as gray (tint)
		}
	}
	return colorSpace{n: 3}
}

func paletteRGB(lookup []byte, base colorSpace) []byte {
	if base.n == 3 {
		return lookup
	}
	var out []byte
	for i := 0; i+base.n <= len(lookup); i += base.n {
		switch base.n {
		case 1:
			out = append(out, lookup[i], lookup[i], lookup[i])
		case 4:
			r, g, b := cmykToRGB(lookup[i], lookup[i+1], lookup[i+2], lookup[i+3])
			out = append(out, r, g, b)
		}
	}
	return out
}

func cmykToRGB(c, m, y, k byte) (byte, byte, byte) {
	kk := 255 - int(k)
	return byte((255 - int(c)) * kk / 255), byte((255 - int(m)) * kk / 255), byte((255 - int(y)) * kk / 255)
}

func toPNG(r *pdf.Reader, d pdf.Dict, data []byte, w, h int) []byte {
	bpc := r.Int(d["BitsPerComponent"], 8)
	mask, _ := r.Resolve(d["ImageMask"]).(pdf.Boolean)
	cs := colorSpace{n: 1}
	if mask {
		bpc = 1
	} else {
		cs = resolveCS(r, d["ColorSpace"])
	}
	if bpc != 1 && bpc != 2 && bpc != 4 && bpc != 8 && bpc != 16 {
		return nil
	}
	rowBits := w * cs.n * bpc
	rowLen := (rowBits + 7) / 8
	if len(data) < rowLen*h {
		h = len(data) / max(rowLen, 1)
		if h == 0 {
			return nil
		}
	}
	decode := r.Numbers(d["Decode"])
	invert := len(decode) >= 2 && decode[0] > decode[1]
	// ImageMask with the default Decode [0 1]: 0 = painted (black), which
	// is exactly the gray value we produce, so no extra inversion.
	sample := func(row []byte, idx int) int {
		switch bpc {
		case 8:
			return int(row[idx])
		case 16:
			return int(row[idx*2])
		}
		bit := idx * bpc
		v := int(row[bit/8]>>(8-bpc-bit%8)) & (1<<bpc - 1)
		if cs.palette != nil {
			return v
		}
		return v * 255 / (1<<bpc - 1)
	}
	var out image.Image
	switch {
	case cs.palette != nil:
		pal := make(color.Palette, 0, len(cs.palette)/3)
		for i := 0; i+2 < len(cs.palette) && len(pal) < 256; i += 3 {
			pal = append(pal, color.RGBA{cs.palette[i], cs.palette[i+1], cs.palette[i+2], 255})
		}
		if len(pal) == 0 {
			return nil
		}
		im := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for y := 0; y < h; y++ {
			row := data[y*rowLen:]
			for x := 0; x < w; x++ {
				v := sample(row, x)
				if v >= len(pal) {
					v = len(pal) - 1
				}
				im.Pix[y*im.Stride+x] = uint8(v)
			}
		}
		out = im
	case cs.n == 1:
		im := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			row := data[y*rowLen:]
			for x := 0; x < w; x++ {
				v := sample(row, x)
				if invert {
					v = 255 - v
				}
				im.Pix[y*im.Stride+x] = uint8(v)
			}
		}
		out = im
	case cs.n == 3 || cs.n == 4:
		im := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			row := data[y*rowLen:]
			for x := 0; x < w; x++ {
				var c [4]byte
				for k := 0; k < cs.n; k++ {
					c[k] = byte(sample(row, x*cs.n+k))
				}
				o := y*im.Stride + x*4
				if cs.n == 4 {
					c[0], c[1], c[2] = cmykToRGB(c[0], c[1], c[2], c[3])
				}
				im.Pix[o], im.Pix[o+1], im.Pix[o+2], im.Pix[o+3] = c[0], c[1], c[2], 255
			}
		}
		out = im
	default:
		return nil
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, out); err != nil {
		return nil
	}
	return buf.Bytes()
}
