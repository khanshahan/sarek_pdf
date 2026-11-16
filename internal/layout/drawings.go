package layout

import (
	"sort"
	"strings"

	"github.com/khanshahan/sarek_pdf/model"
)

const (
	maxDrawingPaths = 50_000
	clusterTol      = 4.0
)

// buildDrawings groups unused vector paths into figures. Simple
// decorations (frames, underlines, highlight boxes around text) are
// dropped; figures claim the text labels lying inside them.
func buildDrawings(paths []model.Path, used map[int]bool, frags []*frag, page model.Rect) []model.Block {
	idx := make([]int, 0, len(paths))
	pageArea := page.Area()
	for i, p := range paths {
		if used[i] {
			continue
		}
		if p.BBox.Area() > 0.85*pageArea {
			continue // page background
		}
		if p.Fill && !p.Stroke && isWhite(p.FillColor) {
			continue // white masks / backgrounds
		}
		if p.BBox.W() < 0.5 && p.BBox.H() < 0.5 {
			continue
		}
		idx = append(idx, i)
	}
	if len(idx) == 0 {
		return nil
	}
	var groups [][]int
	if len(idx) > maxDrawingPaths {
		groups = [][]int{idx}
	} else {
		groups = clusterPaths(paths, idx)
	}
	var out []model.Block
	for _, g := range groups {
		d := &model.Drawing{}
		curves := false
		for _, i := range g {
			p := paths[i]
			d.Paths = append(d.Paths, p)
			d.BBox = d.BBox.Union(p.BBox)
			for _, s := range p.Segments {
				if s.Op == model.CurveTo {
					curves = true
				}
			}
		}
		bb := d.BBox
		if bb.Area() < 25 && max(bb.W(), bb.H()) < 20 {
			continue // bullets, tiny marks
		}
		// Text inside the drawing's bounds.
		var inside []*frag
		textArea := 0.0
		for _, f := range frags {
			if !f.used && bb.Contains(f.bbox.Center()) {
				inside = append(inside, f)
				textArea += f.bbox.Area()
			}
		}
		simple := !curves && len(g) <= 4
		if simple && len(inside) > 0 {
			continue // box / underline / highlight around text
		}
		if bb.H() < 3 && len(inside) == 0 && nearText(bb, frags) {
			continue // underline or strike-through
		}
		if len(inside) > 0 && (textArea > 0.35*bb.Area() || proseInside(inside)) {
			continue // a decorative frame around prose: keep text as paragraphs
		}
		for _, f := range inside {
			f.used = true
		}
		d.Labels = buildLines(inside)
		out = append(out, d)
	}
	return out
}

func proseInside(fs []*frag) bool {
	words := 0
	for _, f := range fs {
		words += len(strings.Fields(f.text()))
	}
	return words > 60
}

func nearText(bb model.Rect, frags []*frag) bool {
	for _, f := range frags {
		if bb.X0 < f.bbox.X1 && bb.X1 > f.bbox.X0 && bb.Y0 >= f.bbox.Y0-2 && bb.Y0 <= f.bbox.Y1+3 {
			return true
		}
	}
	return false
}

// clusterPaths unions paths whose (expanded) boxes overlap, using a
// sweep over x to stay near O(n log n) for typical pages.
func clusterPaths(paths []model.Path, idx []int) [][]int {
	sort.Slice(idx, func(a, b int) bool { return paths[idx[a]].BBox.X0 < paths[idx[b]].BBox.X0 })
	parent := make(map[int]int, len(idx))
	for _, i := range idx {
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
	active := make([]int, 0, 64)
	for _, i := range idx {
		bi := paths[i].BBox
		keep := active[:0]
		for _, j := range active {
			bj := paths[j].BBox
			if bj.X1+clusterTol < bi.X0 {
				continue // can no longer overlap anything
			}
			keep = append(keep, j)
			if bj.Y0 <= bi.Y1+clusterTol && bj.Y1 >= bi.Y0-clusterTol {
				parent[find(i)] = find(j)
			}
		}
		active = append(keep, i)
	}
	groups := map[int][]int{}
	for _, i := range idx {
		r := find(i)
		groups[r] = append(groups[r], i)
	}
	out := make([][]int, 0, len(groups))
	for _, g := range groups {
		sort.Ints(g) // keep painting order
		out = append(out, g)
	}
	sort.Slice(out, func(a, b int) bool { return out[a][0] < out[b][0] })
	return out
}
