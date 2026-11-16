package layout

import (
	"math"
	"sort"

	"github.com/khanshahan/sarek_pdf/model"
)

const (
	snapTol  = 2.0 // points: tolerance for aligning/intersecting rules
	minRule  = 4.0 // minimum length of a rule segment
	thinRule = 3.0 // filled rectangles thinner than this are rules
	maxRules = 6000
)

type rule struct {
	horiz  bool
	pos    float64 // y for horizontal, x for vertical
	lo, hi float64 // extent along the rule
	path   int     // source path index
}

// extractRules derives horizontal/vertical rule segments from paths.
func extractRules(paths []model.Path, page model.Rect) []rule {
	var rs []rule
	pageArea := page.Area()
	for pi, p := range paths {
		if len(rs) > maxRules {
			break
		}
		if p.Fill && !p.Stroke {
			if rect, ok := asRect(p); ok {
				switch {
				case rect.H() <= thinRule && rect.W() >= minRule:
					rs = append(rs, rule{horiz: true, pos: (rect.Y0 + rect.Y1) / 2, lo: rect.X0, hi: rect.X1, path: pi})
				case rect.W() <= thinRule && rect.H() >= minRule:
					rs = append(rs, rule{pos: (rect.X0 + rect.X1) / 2, lo: rect.Y0, hi: rect.Y1, path: pi})
				case rect.Area() < 0.5*pageArea && !isWhite(p.FillColor):
					rs = append(rs, rectRules(rect, pi)...)
				}
				continue
			}
		}
		if !p.Stroke {
			continue
		}
		var cur, start model.Point
		for _, s := range p.Segments {
			switch s.Op {
			case model.MoveTo:
				cur, start = s.Pts[0], s.Pts[0]
			case model.LineTo:
				rs = appendLine(rs, cur, s.Pts[0], pi)
				cur = s.Pts[0]
			case model.Close:
				rs = appendLine(rs, cur, start, pi)
				cur = start
			case model.CurveTo:
				cur = s.Pts[2]
			}
		}
	}
	return rs
}

func isWhite(c model.Color) bool { return c.R > 245 && c.G > 245 && c.B > 245 }

func rectRules(r model.Rect, pi int) []rule {
	return []rule{
		{horiz: true, pos: r.Y0, lo: r.X0, hi: r.X1, path: pi},
		{horiz: true, pos: r.Y1, lo: r.X0, hi: r.X1, path: pi},
		{pos: r.X0, lo: r.Y0, hi: r.Y1, path: pi},
		{pos: r.X1, lo: r.Y0, hi: r.Y1, path: pi},
	}
}

func appendLine(rs []rule, a, b model.Point, pi int) []rule {
	dx, dy := math.Abs(b.X-a.X), math.Abs(b.Y-a.Y)
	switch {
	case dy < 1 && dx >= minRule:
		return append(rs, rule{horiz: true, pos: (a.Y + b.Y) / 2, lo: min(a.X, b.X), hi: max(a.X, b.X), path: pi})
	case dx < 1 && dy >= minRule:
		return append(rs, rule{pos: (a.X + b.X) / 2, lo: min(a.Y, b.Y), hi: max(a.Y, b.Y), path: pi})
	}
	return rs
}

// asRect reports whether a path is a single axis-aligned rectangle.
func asRect(p model.Path) (model.Rect, bool) {
	pts := make([]model.Point, 0, 5)
	for i, s := range p.Segments {
		switch s.Op {
		case model.MoveTo:
			if i != 0 {
				return model.Rect{}, false
			}
			pts = append(pts, s.Pts[0])
		case model.LineTo:
			pts = append(pts, s.Pts[0])
		case model.CurveTo:
			return model.Rect{}, false
		}
	}
	if len(pts) == 5 && math.Abs(pts[4].X-pts[0].X) < 0.5 && math.Abs(pts[4].Y-pts[0].Y) < 0.5 {
		pts = pts[:4]
	}
	if len(pts) != 4 {
		return model.Rect{}, false
	}
	for i := 0; i < 4; i++ {
		a, b := pts[i], pts[(i+1)%4]
		if math.Abs(a.X-b.X) > 0.5 && math.Abs(a.Y-b.Y) > 0.5 {
			return model.Rect{}, false
		}
	}
	return p.BBox, true
}

// mergeRules joins collinear overlapping rules.
func mergeRules(rs []rule) []rule {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].horiz != rs[j].horiz {
			return rs[i].horiz
		}
		if math.Abs(rs[i].pos-rs[j].pos) > snapTol/2 {
			return rs[i].pos < rs[j].pos
		}
		return rs[i].lo < rs[j].lo
	})
	var out []rule
	for _, r := range rs {
		if n := len(out); n > 0 {
			l := &out[n-1]
			if l.horiz == r.horiz && math.Abs(l.pos-r.pos) <= snapTol/2 && r.lo <= l.hi+snapTol {
				l.hi = max(l.hi, r.hi)
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

type tableCandidate struct {
	table *model.Table
	paths map[int]bool
}

// findRuledTables detects grids formed by horizontal and vertical rules.
func findRuledTables(paths []model.Path, page model.Rect) []tableCandidate {
	rs := mergeRules(extractRules(paths, page))
	if len(rs) < 4 {
		return nil
	}
	// Union-find over intersecting horizontal/vertical rules.
	parent := make([]int, len(rs))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i := range rs {
		if !rs[i].horiz {
			continue
		}
		for j := range rs {
			if rs[j].horiz {
				continue
			}
			h, v := rs[i], rs[j]
			if v.pos >= h.lo-snapTol && v.pos <= h.hi+snapTol && h.pos >= v.lo-snapTol && h.pos <= v.hi+snapTol {
				parent[find(i)] = find(j)
			}
		}
	}
	comps := map[int][]rule{}
	for i := range rs {
		comps[find(i)] = append(comps[find(i)], rs[i])
	}
	var out []tableCandidate
	for _, c := range comps {
		if t := gridFromRules(c); t != nil {
			out = append(out, *t)
		}
	}
	return out
}

func clusterPositions(v []float64) []float64 {
	sort.Float64s(v)
	var out []float64
	for _, x := range v {
		if n := len(out); n > 0 && x-out[n-1] <= snapTol {
			continue
		}
		out = append(out, x)
	}
	return out
}

func gridFromRules(rs []rule) *tableCandidate {
	var hs, vs []rule
	var xs, ys []float64
	paths := map[int]bool{}
	bb := model.Rect{}
	for _, r := range rs {
		paths[r.path] = true
		if r.horiz {
			hs = append(hs, r)
			ys = append(ys, r.pos)
			bb = bb.Union(model.Rect{X0: r.lo, Y0: r.pos, X1: r.hi, Y1: r.pos + 0.01})
		} else {
			vs = append(vs, r)
			xs = append(xs, r.pos)
			bb = bb.Union(model.Rect{X0: r.pos, Y0: r.lo, X1: r.pos + 0.01, Y1: r.hi})
		}
	}
	if len(hs) < 2 || len(vs) < 2 {
		return nil
	}
	xs = clusterPositions(append(xs, bb.X0, bb.X1))
	ys = clusterPositions(append(ys, bb.Y0, bb.Y1))
	nr, nc := len(ys)-1, len(xs)-1
	if nr < 1 || nc < 1 || nr*nc < 2 || nr > 500 || nc > 100 {
		return nil
	}
	hasV := func(x, ya, yb float64) bool {
		mid := (ya + yb) / 2
		for _, v := range vs {
			if math.Abs(v.pos-x) <= snapTol && v.lo <= mid && v.hi >= mid {
				return true
			}
		}
		return false
	}
	hasH := func(y, xa, xb float64) bool {
		mid := (xa + xb) / 2
		for _, h := range hs {
			if math.Abs(h.pos-y) <= snapTol && h.lo <= mid && h.hi >= mid {
				return true
			}
		}
		return false
	}
	covered := make([][]bool, nr)
	for i := range covered {
		covered[i] = make([]bool, nc)
	}
	t := &model.Table{Ruled: true, ColX: xs, RowY: ys, BBox: model.Rect{X0: xs[0], Y0: ys[0], X1: xs[nc], Y1: ys[nr]}}
	for r := 0; r < nr; r++ {
		for c := 0; c < nc; c++ {
			if covered[r][c] {
				continue
			}
			cs := 1
			for c+cs < nc && !covered[r][c+cs] && !hasV(xs[c+cs], ys[r], ys[r+1]) {
				cs++
			}
			rsn := 1
			for r+rsn < nr {
				open := true
				for k := c; k < c+cs; k++ {
					if hasH(ys[r+rsn], xs[k], xs[k+1]) || covered[r+rsn][k] {
						open = false
						break
					}
				}
				if !open {
					break
				}
				rsn++
			}
			for i := r; i < r+rsn; i++ {
				for k := c; k < c+cs; k++ {
					covered[i][k] = true
				}
			}
			t.Cells = append(t.Cells, &model.Cell{Row: r, Col: c, RowSpan: rsn, ColSpan: cs,
				BBox: model.Rect{X0: xs[c], Y0: ys[r], X1: xs[c+cs], Y1: ys[r+rsn]}})
		}
	}
	if len(t.Cells) < 2 {
		return nil
	}
	return &tableCandidate{table: t, paths: paths}
}

// fillTable assigns fragments to cells (splitting fragments that cross
// column borders) and builds cell paragraphs. It returns the fragments
// that were consumed.
func fillTable(t *model.Table, frags []*frag, bodySize float64) {
	inside := func(f *frag) bool {
		c := f.bbox.Center()
		return !f.used && t.BBox.Contains(c)
	}
	var mine []*frag
	for _, f := range frags {
		if inside(f) {
			mine = append(mine, f)
		}
	}
	// Split fragments at interior column boundaries.
	for _, x := range t.ColX[1 : len(t.ColX)-1] {
		var next []*frag
		for _, f := range mine {
			if l, r, ok := f.splitAt(x); ok {
				next = append(next, l, r)
			} else {
				next = append(next, f)
			}
		}
		mine = next
	}
	cellFr := make(map[*model.Cell][]*frag, len(t.Cells))
	for _, f := range mine {
		c := f.bbox.Center()
		var best *model.Cell
		for _, cell := range t.Cells {
			if cell.BBox.Contains(c) {
				best = cell
				break
			}
		}
		if best == nil {
			continue
		}
		cellFr[best] = append(cellFr[best], f)
	}
	for _, f := range frags {
		if inside(f) {
			f.used = true
		}
	}
	for _, cell := range t.Cells {
		fs := cellFr[cell]
		if len(fs) == 0 {
			continue
		}
		lines := buildLines(fs)
		cell.Paragraphs = paragraphsFromLines(lines, cell.BBox, 0)
	}
}

// cellFills records background colours from filled rectangles.
func cellFills(t *model.Table, paths []model.Path) {
	for _, p := range paths {
		if !p.Fill || isWhite(p.FillColor) {
			continue
		}
		r, ok := asRect(p)
		if !ok || r.H() <= thinRule || r.W() <= thinRule {
			continue
		}
		for _, c := range t.Cells {
			in := c.BBox.Intersect(r)
			if in.Area() > 0.8*c.BBox.Area() {
				col := p.FillColor
				c.Fill = &col
			}
		}
	}
}

// textTable builds a borderless table from fragments that align into
// columns and rows. cols are x boundaries (including outer edges).
func textTable(frags []*frag, cols []float64) *model.Table {
	lines := buildLines(frags)
	if len(lines) < 2 || len(cols) < 3 {
		return nil
	}
	// Row boundaries halfway between lines.
	rows := []float64{lines[0].BBox.Y0}
	for i := 1; i < len(lines); i++ {
		rows = append(rows, (lines[i-1].BBox.Y1+lines[i].BBox.Y0)/2)
	}
	rows = append(rows, lines[len(lines)-1].BBox.Y1)
	t := &model.Table{ColX: cols, RowY: rows}
	t.BBox = model.Rect{X0: cols[0], Y0: rows[0], X1: cols[len(cols)-1], Y1: rows[len(rows)-1]}
	for r := 0; r < len(rows)-1; r++ {
		for c := 0; c < len(cols)-1; c++ {
			t.Cells = append(t.Cells, &model.Cell{Row: r, Col: c, RowSpan: 1, ColSpan: 1,
				BBox: model.Rect{X0: cols[c], Y0: rows[r], X1: cols[c+1], Y1: rows[r+1]}})
		}
	}
	fillTable(t, frags, 0)
	mergeContinuationRows(t)
	return t
}

// mergeContinuationRows merges a row into the previous one when its first
// column is empty and the previous row's first column is not (wrapped cell
// text in borderless tables).
func mergeContinuationRows(t *model.Table) {
	nc := t.NumCols()
	grid := func() map[[2]int]*model.Cell {
		m := map[[2]int]*model.Cell{}
		for _, c := range t.Cells {
			m[[2]int{c.Row, c.Col}] = c
		}
		return m
	}
	for r := t.NumRows() - 1; r >= 1; r-- {
		g := grid()
		first := g[[2]int{r, 0}]
		prevFirst := g[[2]int{r - 1, 0}]
		if first == nil || prevFirst == nil || len(first.Paragraphs) > 0 || len(prevFirst.Paragraphs) == 0 {
			continue
		}
		// Merge row r into r-1.
		for c := 0; c < nc; c++ {
			a, b := g[[2]int{r - 1, c}], g[[2]int{r, c}]
			if a == nil || b == nil {
				continue
			}
			if len(b.Paragraphs) > 0 {
				if len(a.Paragraphs) > 0 {
					last := a.Paragraphs[len(a.Paragraphs)-1]
					for _, p := range b.Paragraphs {
						last.Lines = append(last.Lines, p.Lines...)
						last.BBox = last.BBox.Union(p.BBox)
					}
					last.Runs = buildRuns(last.Lines)
				} else {
					a.Paragraphs = b.Paragraphs
				}
			}
			a.BBox = a.BBox.Union(b.BBox)
		}
		var cells []*model.Cell
		for _, c := range t.Cells {
			if c.Row == r {
				continue
			}
			if c.Row > r {
				c.Row--
			}
			cells = append(cells, c)
		}
		t.Cells = cells
		t.RowY = append(t.RowY[:r], t.RowY[r+1:]...)
	}
}
