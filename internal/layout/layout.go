package layout

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/khanshahan/sarek_pdf/internal/content"
	"github.com/khanshahan/sarek_pdf/model"
)

// Options controls layout analysis.
type Options struct {
	Tables   bool // detect tables
	Drawings bool // group vector paths into drawings
	// OCR holds text lines recognised from page images. They are laid out
	// together with text-layer text and marked as model.SourceOCR.
	OCR []OCRLine
}

// OCRLine is a recognised line in display coordinates.
type OCRLine struct {
	Text       string
	BBox       model.Rect
	Confidence float64
}

// ocrFragments converts OCR lines into fragments, skipping lines that
// overlap text already present in the text layer.
func ocrFragments(lines []OCRLine, existing []*frag) []*frag {
	// Detector boxes jitter in height; snap sizes near the median height
	// to the median so body text has one consistent size.
	hs := make([]float64, 0, len(lines))
	for _, l := range lines {
		hs = append(hs, l.BBox.H())
	}
	sort.Float64s(hs)
	medH := 0.0
	if len(hs) > 0 {
		medH = hs[len(hs)/2]
	}
	var out []*frag
	for _, l := range lines {
		text := strings.TrimSpace(l.Text)
		if text == "" || l.BBox.Empty() {
			continue
		}
		dup := false
		for _, f := range existing {
			if f.bbox.Intersect(l.BBox).Area() > 0.5*min(f.bbox.Area(), l.BBox.Area()) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		h := l.BBox.H()
		if medH > 0 && math.Abs(h-medH) <= 0.25*medH {
			h = medH
		}
		size := math.Round(h/1.2*2) / 2 // box height ≈ ascent+descent ≈ 1.2 em
		n := utf8.RuneCountInString(text)
		edges := make([]float64, n+1)
		for i := range edges {
			edges[i] = l.BBox.X0 + l.BBox.W()*float64(i)/float64(n)
		}
		f := &frag{bbox: l.BBox, baseline: l.BBox.Y1 - 0.2*h, size: size, lastX1: l.BBox.X1}
		f.spans = []model.Span{{Text: text, BBox: l.BBox, Edges: edges,
			Style: model.Style{Size: size}, Source: model.SourceOCR, Confidence: l.Confidence}}
		out = append(out, f)
	}
	return out
}

// item is an element participating in reading-order segmentation.
type item struct {
	bbox  model.Rect
	frag  *frag
	block model.Block
}

// Analyze converts interpreted page content into ordered blocks.
// images are already-converted image blocks.
func Analyze(res *content.Result, images []*model.Image, opts Options) []model.Block {
	page := model.Rect{X1: res.Width, Y1: res.Height}
	frags := buildFragments(res.Glyphs, page)
	if len(opts.OCR) > 0 {
		frags = append(frags, ocrFragments(opts.OCR, frags)...)
	}
	body := bodySize(frags)

	var blocks []model.Block
	usedPaths := map[int]bool{}

	if opts.Tables && len(res.Paths) > 0 {
		cands := findRuledTables(res.Paths, page)
		sort.Slice(cands, func(i, j int) bool { return cands[i].table.BBox.Area() > cands[j].table.BBox.Area() })
		var accepted []*model.Table
		for _, c := range cands {
			overlaps := false
			for _, a := range accepted {
				if a.BBox.Intersect(c.table.BBox).Area() > 0.1*c.table.BBox.Area() {
					overlaps = true
				}
			}
			if overlaps {
				continue
			}
			// Reject frames: a 1×N or N×1 "grid" containing a lot of prose.
			fillTable(c.table, frags, body)
			if !plausibleTable(c.table) {
				for _, f := range frags {
					if f.used && c.table.BBox.Contains(f.bbox.Center()) {
						f.used = false
					}
				}
				continue
			}
			cellFills(c.table, res.Paths)
			accepted = append(accepted, c.table)
			blocks = append(blocks, c.table)
			for p := range c.paths {
				usedPaths[p] = true
			}
		}
		if hr := openTables(res.Paths, usedPaths, frags, body); len(hr) > 0 {
			blocks = append(blocks, hr...)
		}
	}

	if opts.Drawings {
		blocks = append(blocks, buildDrawings(res.Paths, usedPaths, frags, page)...)
	}
	for _, im := range images {
		blocks = append(blocks, im)
	}

	// Background blocks (covering text) are emitted first and kept out of
	// the reading-order segmentation.
	var items []*item
	var background []model.Block
	for _, b := range blocks {
		if coversText(b.Bounds(), frags) {
			background = append(background, b)
			continue
		}
		items = append(items, &item{bbox: b.Bounds(), block: b})
	}
	for _, f := range frags {
		if !f.used && len(f.spans) > 0 {
			items = append(items, &item{bbox: f.bbox, frag: f})
		}
	}
	var out []model.Block
	out = append(out, background...)
	seg := &segmenter{body: body, tables: opts.Tables}
	for _, leaf := range seg.cut(items, 0) {
		out = append(out, seg.leafBlocks(leaf)...)
	}
	return out
}

func plausibleTable(t *model.Table) bool {
	nonEmpty, words := 0, 0
	for _, c := range t.Cells {
		if len(c.Paragraphs) > 0 {
			nonEmpty++
			for _, p := range c.Paragraphs {
				words += len(strings.Fields(p.Text()))
			}
		}
	}
	if t.NumCols() == 1 {
		// A single column has no tabular structure: it is a frame or a box
		// with separator lines around text, which reads better as paragraphs.
		return false
	}
	if t.NumRows() == 1 {
		// A box split into a single row: accept only if it holds several
		// short cells (not a framed paragraph).
		return nonEmpty >= 2 && words <= nonEmpty*25
	}
	return true
}

func coversText(r model.Rect, frags []*frag) bool {
	n := 0
	for _, f := range frags {
		if !f.used && r.Contains(f.bbox.Center()) {
			n++
			if n >= 3 {
				return true
			}
		}
	}
	return false
}

// bodySize is the character-weighted mode of font sizes on the page.
func bodySize(frags []*frag) float64 {
	counts := map[float64]int{}
	for _, f := range frags {
		for _, s := range f.spans {
			counts[math.Round(s.Style.Size)] += utf8.RuneCountInString(s.Text)
		}
	}
	best, bestN := 0.0, 0
	for sz, n := range counts {
		if n > bestN || (n == bestN && sz < best) {
			best, bestN = sz, n
		}
	}
	return best
}

// openTables finds tables delimited by full-width horizontal rules only
// (no vertical rules), e.g. "booktabs" style, with columns derived from
// text alignment.
func openTables(paths []model.Path, used map[int]bool, frags []*frag, body float64) []model.Block {
	var hs []rule
	for _, r := range mergeRules(extractRules(paths, model.Rect{X1: 1e6, Y1: 1e6})) {
		if r.horiz && !used[r.path] && r.hi-r.lo > 50 {
			hs = append(hs, r)
		}
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].pos < hs[j].pos })
	var out []model.Block
	for i := 0; i < len(hs); {
		j := i + 1
		for j < len(hs) && math.Abs(hs[j].lo-hs[i].lo) < 4 && math.Abs(hs[j].hi-hs[i].hi) < 4 {
			j++
		}
		if j-i >= 2 {
			region := model.Rect{X0: hs[i].lo - 1, Y0: hs[i].pos, X1: hs[i].hi + 1, Y1: hs[j-1].pos}
			var fs []*frag
			for _, f := range frags {
				if !f.used && region.Contains(f.bbox.Center()) {
					fs = append(fs, f)
				}
			}
			if cols := columnGaps(fs, region, max(body, 6)*0.8); len(cols) >= 3 {
				if t := textTable(fs, cols); t != nil && t.NumRows() >= 2 {
					t.BBox = t.BBox.Union(region)
					out = append(out, t)
					for k := i; k < j; k++ {
						used[hs[k].path] = true
					}
				}
			}
		}
		i = j
	}
	return out
}

// columnGaps returns column boundaries (outer edges included) from
// vertical whitespace channels in the fragments' x-projection.
func columnGaps(fs []*frag, region model.Rect, minGap float64) []float64 {
	if len(fs) < 4 {
		return nil
	}
	iv := make([][2]float64, 0, len(fs))
	for _, f := range fs {
		iv = append(iv, [2]float64{f.bbox.X0, f.bbox.X1})
	}
	sort.Slice(iv, func(i, j int) bool { return iv[i][0] < iv[j][0] })
	cols := []float64{min(region.X0, iv[0][0])}
	end := iv[0][1]
	for _, v := range iv[1:] {
		if v[0]-end >= minGap {
			cols = append(cols, (v[0]+end)/2)
		}
		end = max(end, v[1])
	}
	cols = append(cols, max(region.X1, end))
	return cols
}

// ---------------------------------------------------------------------------
// Reading order: recursive XY-cut

type segmenter struct {
	body   float64
	tables bool
}

type gap struct{ lo, hi float64 }

func projGaps(items []*item, horiz bool, minGap float64) []gap {
	iv := make([][2]float64, len(items))
	for i, it := range items {
		if horiz {
			iv[i] = [2]float64{it.bbox.X0, it.bbox.X1}
		} else {
			iv[i] = [2]float64{it.bbox.Y0, it.bbox.Y1}
		}
	}
	sort.Slice(iv, func(i, j int) bool { return iv[i][0] < iv[j][0] })
	var gaps []gap
	end := iv[0][1]
	for _, v := range iv[1:] {
		if v[0]-end >= minGap {
			gaps = append(gaps, gap{end, v[0]})
		}
		end = max(end, v[1])
	}
	return gaps
}

func bounds(items []*item) model.Rect {
	var r model.Rect
	for _, it := range items {
		r = r.Union(it.bbox)
	}
	return r
}

func splitBy(items []*item, gaps []gap, horiz bool) [][]*item {
	groups := make([][]*item, len(gaps)+1)
	for _, it := range items {
		c := it.bbox.Center().Y
		if horiz {
			c = it.bbox.Center().X
		}
		k := sort.Search(len(gaps), func(i int) bool { return gaps[i].lo >= c })
		groups[k] = append(groups[k], it)
	}
	out := groups[:0]
	for _, g := range groups {
		if len(g) > 0 {
			out = append(out, g)
		}
	}
	return out
}

func (s *segmenter) cut(items []*item, depth int) [][]*item {
	if len(items) <= 1 || depth > 40 {
		return [][]*item{items}
	}
	med := max(s.body, 6)
	region := bounds(items)
	xg := projGaps(items, true, 0.9*med)
	yg := projGaps(items, false, 0.6*med)

	// 1. Columns that each span a large part of the region height.
	if len(xg) > 0 {
		groups := splitBy(items, xg, true)
		tall := true
		for _, g := range groups {
			if bounds(g).H() < 0.4*region.H() {
				tall = false
				break
			}
		}
		if tall && len(groups) > 1 {
			if t := s.tryTextTable(items, region, med); t != nil {
				return [][]*item{{t}}
			}
			return s.cutGroups(groups, depth)
		}
	}
	// 2. Horizontal bands. Adjacent bands that share the same column
	// gutters belong to one multi-column section (a gap at the same height
	// in every column must not interleave the columns).
	if len(yg) > 0 {
		bands := s.mergeColumnBands(splitBy(items, yg, false), 0.9*med)
		if len(bands) == 1 && len(xg) > 0 {
			return s.cutGroups(splitBy(items, xg, true), depth)
		}
		return s.cutGroups(bands, depth)
	}
	// 3. Any column split.
	if len(xg) > 0 {
		if t := s.tryTextTable(items, region, med); t != nil {
			return [][]*item{{t}}
		}
		return s.cutGroups(splitBy(items, xg, true), depth)
	}
	return [][]*item{items}
}

// mergeColumnBands joins consecutive bands whose column gutters line up.
func (s *segmenter) mergeColumnBands(bands [][]*item, minGap float64) [][]*item {
	type sig struct {
		gaps []gap
	}
	sigOf := func(b []*item) []gap {
		if len(b) < 2 {
			return nil
		}
		return projGaps(b, true, minGap)
	}
	same := func(a, b []gap) bool {
		if len(a) == 0 || len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i].hi < b[i].lo || b[i].hi < a[i].lo {
				return false // gutters do not overlap
			}
		}
		return true
	}
	var out [][]*item
	var prev []gap
	for _, b := range bands {
		g := sigOf(b)
		if n := len(out); n > 0 && same(prev, g) {
			out[n-1] = append(out[n-1], b...)
			// Keep the intersection of gutters as the merged signature.
			for i := range prev {
				prev[i].lo, prev[i].hi = max(prev[i].lo, g[i].lo), min(prev[i].hi, g[i].hi)
			}
			continue
		}
		out = append(out, b)
		prev = g
	}
	return out
}

func (s *segmenter) cutGroups(groups [][]*item, depth int) [][]*item {
	var out [][]*item
	for _, g := range groups {
		out = append(out, s.cut(g, depth+1)...)
	}
	return out
}

// tryTextTable recognises borderless tables: several columns whose rows
// share baselines and whose lines do not fill the column width (prose
// columns do).
func (s *segmenter) tryTextTable(items []*item, region model.Rect, med float64) *item {
	if !s.tables {
		return nil
	}
	var fs []*frag
	for _, it := range items {
		if it.block != nil {
			return nil
		}
		if it.frag.rotated {
			return nil
		}
		fs = append(fs, it.frag)
	}
	cols := columnGaps(fs, region, 0.9*med)
	if len(cols) < 3 {
		return nil
	}
	lines := buildLines(fs)
	if len(lines) < 2 {
		return nil
	}
	// Row alignment: most lines must have content in ≥2 columns.
	multi := 0
	for _, l := range lines {
		used := map[int]bool{}
		for _, sp := range l.Spans {
			if strings.TrimSpace(sp.Text) == "" {
				continue
			}
			c := sort.SearchFloat64s(cols, sp.BBox.Center().X)
			used[c] = true
		}
		if len(used) >= 2 {
			multi++
		}
	}
	if float64(multi) < 0.6*float64(len(lines)) {
		return nil
	}
	// Prose test: fraction of column-lines that fill their column.
	filled, total := 0, 0
	for c := 0; c+1 < len(cols); c++ {
		var colFr []*frag
		for _, f := range fs {
			if x := f.bbox.Center().X; x >= cols[c] && x < cols[c+1] {
				colFr = append(colFr, f)
			}
		}
		// Reference width: 75th percentile, robust to titles and headers.
		ws := make([]float64, 0, len(colFr))
		for _, f := range colFr {
			ws = append(ws, f.bbox.W())
		}
		sort.Float64s(ws)
		w := 0.0
		if len(ws) > 0 {
			w = ws[len(ws)*3/4]
		}
		for _, f := range colFr {
			total++
			// Prose lines fill their column and are long; numeric table
			// cells may fill a narrow column but are short.
			if f.bbox.W() > 0.85*w && utf8.RuneCountInString(f.text()) >= 15 {
				filled++
			}
		}
	}
	if total == 0 || float64(filled) > 0.5*float64(total) {
		return nil
	}
	t := textTable(fs, cols)
	if t == nil || t.NumRows() < 2 {
		for _, f := range fs {
			f.used = false
		}
		return nil
	}
	return &item{bbox: t.BBox, block: t}
}

// leafBlocks turns a leaf region into blocks in top-to-bottom order.
func (s *segmenter) leafBlocks(items []*item) []model.Block {
	var frags []*frag
	var blocks []model.Block
	region := bounds(items)
	for _, it := range items {
		if it.block != nil {
			blocks = append(blocks, it.block)
		} else if !it.frag.used {
			frags = append(frags, it.frag)
		}
	}
	var normal, rotated []*frag
	for _, f := range frags {
		if f.rotated {
			rotated = append(rotated, f)
		} else {
			normal = append(normal, f)
		}
	}
	for _, p := range paragraphsFromLines(buildLines(normal), region, s.body) {
		blocks = append(blocks, p)
	}
	for _, f := range rotated {
		ln := model.Line{BBox: f.bbox, Spans: f.spans}
		blocks = append(blocks, &model.Paragraph{BBox: f.bbox, Lines: []model.Line{ln}, Runs: buildRuns([]model.Line{ln})})
	}
	sort.SliceStable(blocks, func(i, j int) bool {
		a, b := blocks[i].Bounds(), blocks[j].Bounds()
		if a.Y1 <= b.Y0+1 || b.Y1 <= a.Y0+1 {
			return a.Y0 < b.Y0
		}
		return a.X0 < b.X0
	})
	return blocks
}
