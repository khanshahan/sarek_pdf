// Package layout turns interpreted page content (glyphs, paths, images)
// into structured blocks: paragraphs, tables, drawings and images, in
// reading order.
package layout

import (
	"math"
	"unicode/utf8"

	"github.com/khanshahan/sarek_pdf/internal/content"
	"github.com/khanshahan/sarek_pdf/model"
)

// unmapped marks glyphs whose Unicode value cannot be determined. We never
// guess characters (see CONSTITUTION.md, Article 3).
const unmapped = "�"

// frag is a horizontal run of glyphs on one baseline without large gaps.
type frag struct {
	bbox     model.Rect
	baseline float64
	size     float64 // dominant font size
	spans    []model.Span
	rotated  bool
	used     bool
	lastX1   float64
	pendSp   bool
	dir      model.Point // rotated text: unit advance direction
	lastEnd  model.Point // rotated text: end of the previous glyph
	tb       [][]byte    // span text under construction (see finalize)
}

// finalize converts the byte buffers into span strings (one allocation
// per span instead of one per glyph).
func (f *frag) finalize() {
	for i := range f.spans {
		f.spans[i].Text = string(f.tb[i])
	}
	f.tb = nil
}

func (f *frag) text() string {
	n := 0
	for _, s := range f.spans {
		n += len(s.Text)
	}
	b := make([]byte, 0, n)
	for _, s := range f.spans {
		b = append(b, s.Text...)
	}
	return string(b)
}

func styleOf(g *content.Glyph) model.Style {
	st := model.Style{Size: math.Round(g.Size*10) / 10, Color: g.Color}
	if g.Font != nil {
		st.Font, st.Bold, st.Italic = g.Font.Family, g.Font.Bold, g.Font.Italic
	}
	return st
}

func sameStyle(a, b model.Style) bool {
	return a.Font == b.Font && a.Bold == b.Bold && a.Italic == b.Italic && a.Color == b.Color &&
		math.Abs(a.Size-b.Size) < 0.6
}

// appendText adds text with per-rune edges to the last span of f, or
// starts a new span when the style changes.
func (f *frag) appendText(text string, x0, x1 float64, st model.Style, bb model.Rect, invisible bool) {
	n := utf8.RuneCountInString(text)
	if n == 0 {
		return
	}
	var sp *model.Span
	if k := len(f.spans); k > 0 && sameStyle(f.spans[k-1].Style, st) && f.spans[k-1].Invisible == invisible {
		sp = &f.spans[k-1]
		sp.BBox = sp.BBox.Union(bb)
		sp.Edges = sp.Edges[:len(sp.Edges)-1] // replace trailing right edge
		f.tb[k-1] = append(f.tb[k-1], text...)
	} else {
		f.spans = append(f.spans, model.Span{Style: st, BBox: bb, Invisible: invisible, Confidence: 1, Edges: make([]float64, 0, 32)})
		f.tb = append(f.tb, append(make([]byte, 0, 64), text...))
		sp = &f.spans[len(f.spans)-1]
	}
	step := (x1 - x0) / float64(n)
	for i := 0; i < n; i++ {
		sp.Edges = append(sp.Edges, x0+step*float64(i))
	}
	sp.Edges = append(sp.Edges, x1)
	f.bbox = f.bbox.Union(bb)
	f.lastX1 = max(f.lastX1, x1)
}

// buildFragments groups glyphs (in content-stream order) into fragments.
func buildFragments(glyphs []content.Glyph, page model.Rect) []*frag {
	var out []*frag
	var cur *frag
	// Ring of recent glyph positions for duplicate ("fake bold") suppression.
	type key struct {
		t    string
		x, y int32
	}
	var recent [64]key
	nRecent := 0
	for i := range glyphs {
		g := &glyphs[i]
		if g.Space {
			if cur != nil {
				cur.pendSp = true
			}
			continue
		}
		bb := g.BBox()
		// Drop glyphs entirely outside the page.
		if bb.X1 < page.X0-1 || bb.X0 > page.X1+1 || bb.Y1 < page.Y0-1 || bb.Y0 > page.Y1+1 {
			continue
		}
		text := g.Text
		if text == "" {
			text = unmapped
		}
		k := key{text, int32(math.Round(g.Origin.X * 2)), int32(math.Round(g.Origin.Y * 2))}
		dup := false
		for j := 0; j < min(nRecent, len(recent)); j++ {
			if recent[j] == k {
				dup = true
				break
			}
		}
		if dup {
			continue // same glyph drawn again at the same spot
		}
		recent[nRecent%len(recent)] = k
		nRecent++
		st := styleOf(g)
		if !g.Horizontal() {
			// Rotated/vertical text: continue the run only when the glyph
			// follows on along the same writing direction.
			ul := math.Hypot(g.Up.X, g.Up.Y)
			dir := model.Point{X: -g.Up.Y / ul, Y: g.Up.X / ul}
			cont := false
			if cur != nil && cur.rotated && cur.dir.X*dir.X+cur.dir.Y*dir.Y > 0.99 {
				dx, dy := g.Origin.X-cur.lastEnd.X, g.Origin.Y-cur.lastEnd.Y
				along := dx*dir.X + dy*dir.Y
				across := math.Abs(-dx*dir.Y + dy*dir.X)
				if across < 0.3*g.Size && along > -0.3*g.Size && along < 1.0*g.Size {
					cont = true
					if along > 0.15*g.Size {
						cur.pendSp = true
					}
				}
			}
			if !cont {
				cur = &frag{rotated: true, size: g.Size, bbox: bb, dir: dir}
				out = append(out, cur)
			}
			if cur.pendSp {
				cur.appendText(" ", bb.X0, bb.X0, st, bb, g.Invisible)
				cur.pendSp = false
			}
			cur.appendText(text, bb.X0, bb.X1, st, bb, g.Invisible)
			cur.lastEnd = g.End
			continue
		}
		x0, x1 := g.Origin.X, max(g.End.X, g.Origin.X)
		if cur != nil && !cur.rotated && sameLine(cur, g, bb) {
			gap := x0 - cur.lastX1
			size := max(cur.size, g.Size)
			switch {
			case gap > 1.0*size || gap < -0.6*size:
				cur = nil // column break or jump back: new fragment
			case cur.pendSp || gap > 0.15*size:
				if gap > 0 {
					cur.appendText(" ", cur.lastX1, x0, st, model.Rect{X0: cur.lastX1, Y0: bb.Y0, X1: x0, Y1: bb.Y1}, g.Invisible)
				} else {
					cur.appendText(" ", x0, x0, st, model.Rect{X0: x0, Y0: bb.Y0, X1: x0, Y1: bb.Y1}, g.Invisible)
				}
			}
			if cur != nil {
				cur.pendSp = false
				cur.appendText(text, x0, x1, st, bb, g.Invisible)
				if g.Size > cur.size {
					cur.size = g.Size
					cur.baseline = g.Origin.Y
				}
				continue
			}
		}
		cur = &frag{baseline: g.Origin.Y, size: g.Size, bbox: bb, lastX1: x0}
		cur.appendText(text, x0, x1, st, bb, g.Invisible)
		out = append(out, cur)
	}
	for _, f := range out {
		f.finalize()
	}
	return out
}

func sameLine(f *frag, g *content.Glyph, bb model.Rect) bool {
	// Vertical overlap of at least half the smaller height.
	ov := min(f.bbox.Y1, bb.Y1) - max(f.bbox.Y0, bb.Y0)
	return ov >= 0.5*min(f.bbox.H(), bb.H()) && math.Abs(g.Origin.Y-f.baseline) < 0.6*max(f.size, g.Size)
}

func dist(a, b model.Point) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

func near(a, b model.Rect, tol float64) bool {
	return b.X0 <= a.X1+tol && b.X1 >= a.X0-tol && b.Y0 <= a.Y1+tol && b.Y1 >= a.Y0-tol
}

// splitAt splits a fragment at x (between runes). Returns the left and
// right parts; ok is false when x does not fall inside the fragment.
func (f *frag) splitAt(x float64) (*frag, *frag, bool) {
	if x <= f.bbox.X0+0.5 || x >= f.bbox.X1-0.5 || f.rotated {
		return nil, nil, false
	}
	l := &frag{baseline: f.baseline, size: f.size}
	r := &frag{baseline: f.baseline, size: f.size}
	for _, sp := range f.spans {
		runes := []rune(sp.Text)
		cut := len(runes)
		for i := range runes {
			mid := (sp.Edges[i] + sp.Edges[i+1]) / 2
			if mid > x {
				cut = i
				break
			}
		}
		if cut > 0 {
			s := sp
			s.Text = string(runes[:cut])
			s.Edges = append([]float64(nil), sp.Edges[:cut+1]...)
			s.BBox.X1 = s.Edges[cut]
			l.spans = append(l.spans, s)
			l.bbox = l.bbox.Union(s.BBox)
		}
		if cut < len(runes) {
			s := sp
			s.Text = string(runes[cut:])
			s.Edges = append([]float64(nil), sp.Edges[cut:]...)
			s.BBox.X0 = s.Edges[0]
			r.spans = append(r.spans, s)
			r.bbox = r.bbox.Union(s.BBox)
		}
	}
	trimSpaces(l)
	trimSpaces(r)
	if len(l.spans) == 0 || len(r.spans) == 0 {
		return nil, nil, false
	}
	return l, r, true
}

// trimSpaces removes leading/trailing space runes created by splitting.
func trimSpaces(f *frag) {
	for len(f.spans) > 0 {
		s := &f.spans[0]
		if len(s.Text) > 0 && s.Text[0] == ' ' {
			s.Text = s.Text[1:]
			s.Edges = s.Edges[1:]
			if s.Text == "" {
				f.spans = f.spans[1:]
				continue
			}
		}
		break
	}
	for len(f.spans) > 0 {
		s := &f.spans[len(f.spans)-1]
		if n := len(s.Text); n > 0 && s.Text[n-1] == ' ' {
			s.Text = s.Text[:n-1]
			s.Edges = s.Edges[:len(s.Edges)-1]
			if s.Text == "" {
				f.spans = f.spans[:len(f.spans)-1]
				continue
			}
		}
		break
	}
	f.bbox = model.Rect{}
	for _, s := range f.spans {
		f.bbox = f.bbox.Union(s.BBox)
	}
}
