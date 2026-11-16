package docx

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"sort"

	"github.com/khanshahan/sarek_pdf/model"
)

// rasterize renders a drawing to PNG (used as the fallback image for
// viewers without SVG support). It supports fills (non-zero / even-odd)
// and strokes, without anti-aliasing beyond 2×2 supersampling.
func rasterize(d *model.Drawing, maxPx int) []byte {
	bb := d.BBox
	if bb.W() <= 0 || bb.H() <= 0 {
		return nil
	}
	scale := math.Min(4, float64(maxPx)/math.Max(bb.W(), bb.H()))
	ss := 2.0 // supersampling
	w := int(math.Ceil(bb.W()*scale)) + 2
	h := int(math.Ceil(bb.H()*scale)) + 2
	W, H := w*int(ss), h*int(ss)
	cover := make([]color.RGBA, W*H)
	tr := func(p model.Point) model.Point {
		return model.Point{X: (p.X - bb.X0) * scale * ss, Y: (p.Y - bb.Y0) * scale * ss}
	}
	for i := range d.Paths {
		p := &d.Paths[i]
		polys := flatten(p, tr)
		if p.Fill {
			fillPolys(cover, W, H, polys, p.EvenOdd, rgba(p.FillColor))
		}
		if p.Stroke {
			lw := math.Max(1, p.LineWidth*scale*ss)
			var quads [][]model.Point
			for _, poly := range polys {
				for k := 0; k+1 < len(poly); k++ {
					quads = append(quads, segQuad(poly[k], poly[k+1], lw))
				}
			}
			fillPolys(cover, W, H, quads, false, rgba(p.StrokeColor))
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b, a int
			for sy := 0; sy < int(ss); sy++ {
				for sx := 0; sx < int(ss); sx++ {
					c := cover[(y*int(ss)+sy)*W+x*int(ss)+sx]
					r, g, b, a = r+int(c.R), g+int(c.G), b+int(c.B), a+int(c.A)
				}
			}
			n := int(ss * ss)
			if a == 0 {
				continue
			}
			// Average premultiplied samples, then un-premultiply.
			img.Pix[y*img.Stride+x*4+0] = uint8(r * 255 / a)
			img.Pix[y*img.Stride+x*4+1] = uint8(g * 255 / a)
			img.Pix[y*img.Stride+x*4+2] = uint8(b * 255 / a)
			img.Pix[y*img.Stride+x*4+3] = uint8(a / n)
		}
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

func rgba(c model.Color) color.RGBA { return color.RGBA{c.R, c.G, c.B, 255} }

func flatten(p *model.Path, tr func(model.Point) model.Point) [][]model.Point {
	var polys [][]model.Point
	var cur []model.Point
	var start, last model.Point
	for _, s := range p.Segments {
		switch s.Op {
		case model.MoveTo:
			if len(cur) > 1 {
				polys = append(polys, cur)
			}
			start = tr(s.Pts[0])
			last = start
			cur = []model.Point{start}
		case model.LineTo:
			last = tr(s.Pts[0])
			cur = append(cur, last)
		case model.CurveTo:
			p1, p2, p3 := tr(s.Pts[0]), tr(s.Pts[1]), tr(s.Pts[2])
			const n = 12
			for k := 1; k <= n; k++ {
				t := float64(k) / n
				u := 1 - t
				cur = append(cur, model.Point{
					X: u*u*u*last.X + 3*u*u*t*p1.X + 3*u*t*t*p2.X + t*t*t*p3.X,
					Y: u*u*u*last.Y + 3*u*u*t*p1.Y + 3*u*t*t*p2.Y + t*t*t*p3.Y,
				})
			}
			last = p3
		case model.Close:
			cur = append(cur, start)
			last = start
		}
	}
	if len(cur) > 1 {
		polys = append(polys, cur)
	}
	return polys
}

func segQuad(a, b model.Point, lw float64) []model.Point {
	dx, dy := b.X-a.X, b.Y-a.Y
	l := math.Hypot(dx, dy)
	if l == 0 {
		return []model.Point{{X: a.X - lw/2, Y: a.Y - lw/2}, {X: a.X + lw/2, Y: a.Y - lw/2}, {X: a.X + lw/2, Y: a.Y + lw/2}, {X: a.X - lw/2, Y: a.Y + lw/2}}
	}
	nx, ny := -dy/l*lw/2, dx/l*lw/2
	return []model.Point{{X: a.X + nx, Y: a.Y + ny}, {X: b.X + nx, Y: b.Y + ny}, {X: b.X - nx, Y: b.Y - ny}, {X: a.X - nx, Y: a.Y - ny}, {X: a.X + nx, Y: a.Y + ny}}
}

type edge struct {
	x0, y0, x1, y1 float64
	dir            int
}

// fillPolys scan-converts polygons (each implicitly closed).
func fillPolys(buf []color.RGBA, W, H int, polys [][]model.Point, evenOdd bool, c color.RGBA) {
	var edges []edge
	for _, poly := range polys {
		n := len(poly)
		for i := 0; i < n; i++ {
			a, b := poly[i], poly[(i+1)%n]
			if a.Y == b.Y {
				continue
			}
			e := edge{a.X, a.Y, b.X, b.Y, 1}
			if a.Y > b.Y {
				e = edge{b.X, b.Y, a.X, a.Y, -1}
			}
			edges = append(edges, e)
		}
	}
	type hit struct {
		x   float64
		dir int
	}
	hits := make([]hit, 0, 16)
	for y := 0; y < H; y++ {
		sy := float64(y) + 0.5
		hits = hits[:0]
		for _, e := range edges {
			if sy < e.y0 || sy >= e.y1 {
				continue
			}
			x := e.x0 + (sy-e.y0)*(e.x1-e.x0)/(e.y1-e.y0)
			hits = append(hits, hit{x, e.dir})
		}
		if len(hits) < 2 {
			continue
		}
		sort.Slice(hits, func(i, j int) bool { return hits[i].x < hits[j].x })
		wind := 0
		for i := 0; i+1 < len(hits); i++ {
			if evenOdd {
				wind ^= 1
			} else {
				wind += hits[i].dir
			}
			if wind == 0 {
				continue
			}
			x0 := max(0, int(math.Ceil(hits[i].x-0.5)))
			x1 := min(W, int(math.Ceil(hits[i+1].x-0.5)))
			row := buf[y*W:]
			for x := x0; x < x1; x++ {
				row[x] = c
			}
		}
	}
}
