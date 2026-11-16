package mathocr

import (
	"image"
	"math"
)

// rgb is a dense 8-bit RGB raster.
type rgb struct {
	w, h int
	pix  []uint8
}

func toRGB(img image.Image) *rgb {
	b := img.Bounds()
	out := &rgb{w: b.Dx(), h: b.Dy(), pix: make([]uint8, b.Dx()*b.Dy()*3)}
	if g, ok := img.(*image.Gray); ok {
		for y := 0; y < out.h; y++ {
			for x := 0; x < out.w; x++ {
				v := g.Pix[y*g.Stride+x]
				o := (y*out.w + x) * 3
				out.pix[o], out.pix[o+1], out.pix[o+2] = v, v, v
			}
		}
		return out
	}
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, gg, bb, a := img.At(x, y).RGBA()
			if a == 0 {
				r, gg, bb = 0xFFFF, 0xFFFF, 0xFFFF
			}
			out.pix[i], out.pix[i+1], out.pix[i+2] = uint8(r>>8), uint8(gg>>8), uint8(bb>>8)
			i += 3
		}
	}
	return out
}

func (m *rgb) at(x, y float64, c int) float64 {
	x = math.Max(0, math.Min(float64(m.w-1), x))
	y = math.Max(0, math.Min(float64(m.h-1), y))
	x0, y0 := int(x), int(y)
	x1, y1 := min(x0+1, m.w-1), min(y0+1, m.h-1)
	fx, fy := x-float64(x0), y-float64(y0)
	p := func(xx, yy int) float64 { return float64(m.pix[(yy*m.w+xx)*3+c]) }
	return (p(x0, y0)*(1-fx)+p(x1, y0)*fx)*(1-fy) + (p(x0, y1)*(1-fx)+p(x1, y1)*fx)*fy
}

// tensor resamples the region r of m to w×h and returns a CHW float
// tensor with (v/255 - mean[c]) / std[c].
func (m *rgb) tensor(r image.Rectangle, w, h int, mean, std [3]float64) []float32 {
	out := make([]float32, 3*w*h)
	sx := float64(r.Dx()) / float64(w)
	sy := float64(r.Dy()) / float64(h)
	for y := 0; y < h; y++ {
		srcY := float64(r.Min.Y) + (float64(y)+0.5)*sy - 0.5
		for x := 0; x < w; x++ {
			srcX := float64(r.Min.X) + (float64(x)+0.5)*sx - 0.5
			for c := 0; c < 3; c++ {
				v := m.at(srcX, srcY, c) / 255
				out[c*w*h+y*w+x] = float32((v - mean[c]) / std[c])
			}
		}
	}
	return out
}
