package layout

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/khanshahan/sarek_pdf/internal/content"
	"github.com/khanshahan/sarek_pdf/model"
)

// Formula recognition for born-digital PDFs.
//
// A formula's structure is recovered from geometry alone: glyph positions,
// sizes and baselines, plus the thin horizontal rules drawn as fraction
// bars and radical bars. Display formulas become model.Formula blocks;
// inline math stays in the paragraph text with Span.Math set.

// mathFontPrefixes identifies mathematics fonts by (subset-stripped) name.
var mathFontPrefixes = []string{
	"cmmi", "cmsy", "cmex", "cmbsy", "cmmib", "msam", "msbm", "eufm", "eusm", "rsfs", "esint",
	"lmmathitalic", "lmmathsymbols", "lmmathextension", "stixmath", "stixtwomath", "cambriamath",
	"cambria math", "latinmodernmath", "xitsmath", "texgyre", "asanamath", "mathematicalpi",
	"mt extra", "mtextra", "euclid", "symbol", "txsy", "txmi", "txex", "pxsy", "pxmi", "pxex",
	"ntxmi", "ntxsy", "ntxexx", "zxmi", "rtxmi", "mathjax", "ncm", "fourier-math",
}

func isMathFontName(name string) bool {
	n := strings.ToLower(name)
	for _, p := range mathFontPrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return strings.Contains(n, "math") && !strings.Contains(n, "mathilde")
}

// isMathRune reports characters that only occur in mathematics.
func isMathRune(r rune) bool {
	switch {
	case r >= 0x2200 && r <= 0x22FF, // mathematical operators
		r >= 0x2A00 && r <= 0x2AFF, // supplemental operators
		r >= 0x27C0 && r <= 0x27EF, r >= 0x2980 && r <= 0x29FF,
		r >= 0x1D400 && r <= 0x1D7FF, // mathematical alphanumerics
		r >= 0x2190 && r <= 0x21FF:   // arrows
		return true
	}
	switch r {
	case '±', '×', '÷', '∂', '∞', '√', '∑', '∏', '∫', '≤', '≥', '≠', '≈', '−', '′', '″', 'ℝ', 'ℕ', 'ℤ', 'ℚ', 'ℂ', 'ℓ', 'ℏ':
		return true
	}
	return false
}

// isMathGlyph reports whether a glyph belongs to mathematics: set in a
// math font, or a math-only symbol.
func isMathGlyph(g *content.Glyph, text string) bool {
	if g.Font != nil && isMathFontName(g.Font.Name) {
		return true
	}
	for _, r := range text {
		if isMathRune(r) {
			return true
		}
	}
	return false
}

// fragMathStats counts math characters and ordinary letters of a fragment.
func fragMathStats(f *frag) (mathChars, plainLetters int) {
	for _, sp := range f.spans {
		for _, r := range sp.Text {
			switch {
			case sp.Math && !unicode.IsSpace(r):
				mathChars++
			case isMathRune(r):
				mathChars++
			case unicode.IsLetter(r):
				plainLetters++
			}
		}
	}
	return
}

// bar is a thin horizontal rule that may be a fraction or radical bar.
type bar struct {
	x0, x1, y float64
	path      int
}

func formulaBars(paths []model.Path, used map[int]bool, page model.Rect) []bar {
	var out []bar
	for i, p := range paths {
		if used[i] {
			continue
		}
		bb := p.BBox
		if bb.H() > 1.6 || bb.W() < 2 || bb.W() > 0.6*page.W() {
			continue
		}
		_, isRect := asRect(p)
		filledRule := isRect && p.Fill
		strokedLine := p.Stroke && len(p.Segments) <= 3 // m, l (and an optional close)
		if filledRule || strokedLine {
			out = append(out, bar{x0: bb.X0, x1: bb.X1, y: (bb.Y0 + bb.Y1) / 2, path: i})
		}
	}
	return out
}

var eqNumberRe = regexp.MustCompile(`^\(\s*[0-9IVXivx]+(\.[0-9]+)*[a-z]?\s*\)$`)

// fitem is a fragment or a bar taking part in formula detection.
type fitem struct {
	f        *frag
	b        *bar
	bb       model.Rect
	m, l     int // math characters, ordinary letters
	seed     bool
	prose    bool
	sentence bool // a run of ordinary words: never part of a formula
	assigned bool
}

func (it *fitem) size() float64 {
	if it.f != nil {
		return it.f.size
	}
	return 8
}

func newUnion(n int) (find func(int) int, union func(a, b int)) {
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union = func(a, b int) { parent[find(a)] = find(b) }
	return
}

func sameRow(a, b model.Rect) bool {
	ov := min(a.Y1, b.Y1) - max(a.Y0, b.Y0)
	return ov >= 0.5*min(a.H(), b.H())
}

// barTouches reports whether an item sits directly above or below a bar,
// or is the radical sign whose overbar the bar is.
func barTouches(br, other *fitem) bool {
	if other.f != nil && strings.ContainsRune(other.f.text(), '√') {
		s := other.size()
		if br.bb.X0 >= other.bb.X1-0.6*s && br.bb.X0 <= other.bb.X1+0.6*s && math.Abs(br.bb.Y0-other.bb.Y0) <= 0.6*s {
			return true
		}
	}
	ov := min(br.bb.X1, other.bb.X1) - max(br.bb.X0, other.bb.X0)
	gap := math.Max(other.bb.Y0-br.bb.Y1, br.bb.Y0-other.bb.Y1)
	return ov > 0.3*min(other.bb.W(), br.bb.W()) && gap < 1.2*other.size()
}

// detectFormulas finds display formulas among the unused fragments and
// returns them as blocks; their fragments and bars are marked used.
//
//  1. Core groups: fragments containing math and the bars directly above or
//     below them, joined when they touch within formula spacing.
//  2. Each group is completed with the other fragments on its rows
//     (e.g. "Attention(Q, K, V) = softmax(" set in a text font).
//  3. Rows not connected by a fraction bar become separate formulas.
//  4. A formula must be displayed: centred or indented and narrower than
//     the surrounding prose column, with no prose on its rows.
func detectFormulas(frags []*frag, glyphs []content.Glyph, paths []model.Path, used map[int]bool, page model.Rect) ([]model.Block, []*frag) {
	var items []*fitem
	for _, f := range frags {
		if f.used || f.rotated || len(f.gidx) == 0 {
			continue
		}
		m, l := fragMathStats(f)
		sentence := plainWords(f) >= 5
		items = append(items, &fitem{f: f, bb: f.bbox, m: m, l: l, seed: m > 0 && !sentence, prose: sentence || l > 2 && l > 2*m, sentence: sentence})
	}
	bars := formulaBars(paths, used, page)
	for i := range bars {
		b := &bars[i]
		items = append(items, &fitem{b: b, bb: model.Rect{X0: b.x0, Y0: b.y - 0.5, X1: b.x1, Y1: b.y + 0.5}})
	}
	if len(items) == 0 {
		return nil, nil
	}
	find, union := newUnion(len(items))
	core := func(it *fitem) bool { return it.b != nil || it.seed && !it.prose }
	for i, a := range items {
		for j := i + 1; j < len(items); j++ {
			b := items[j]
			if !core(a) || !core(b) {
				continue
			}
			switch {
			case a.b != nil && b.b != nil:
			case a.b != nil:
				if barTouches(a, b) {
					union(i, j)
				}
			case b.b != nil:
				if barTouches(b, a) {
					union(i, j)
				}
			default:
				s := max(a.size(), b.size())
				hgap := math.Max(b.bb.X0-a.bb.X1, a.bb.X0-b.bb.X1)
				vgap := math.Max(b.bb.Y0-a.bb.Y1, a.bb.Y0-b.bb.Y1)
				if hgap < 1.2*s && vgap < 0.6*s {
					union(i, j)
				}
			}
		}
	}
	groups := map[int][]*fitem{}
	for i, it := range items {
		if core(it) {
			groups[find(i)] = append(groups[find(i)], it)
		}
	}
	var out []model.Block
	var inline []*frag
	for _, g := range groups {
		hasSeed := false
		for _, it := range g {
			if it.f != nil {
				hasSeed = true
			}
		}
		if !hasSeed {
			continue
		}
		// 2. Complete the rows with adjacent fragments (also text fonts).
		completed := map[*fitem]bool{}
		for changed := true; changed; {
			changed = false
			for _, it := range items {
				if it.f == nil || it.assigned || it.sentence || containsItem(g, it) {
					continue
				}
				// Compare with the whole group: a fraction's pieces sit above
				// and below the row they belong to.
				gb := groupBox(g)
				ov := min(gb.Y1, it.bb.Y1) - max(gb.Y0, it.bb.Y0)
				if ov >= 0.5*it.bb.H() && math.Max(it.bb.X0-gb.X1, gb.X0-it.bb.X1) < 1.5*it.size() {
					g = append(g, it)
					completed[it] = true
					changed = true
				}
			}
		}
		// 3. Split into formulas per row unless joined by a fraction bar.
		for _, part := range splitRows(g) {
			if f := makeFormula(part, frags, glyphs, used); f != nil {
				out = append(out, f)
			} else if frs := inlineFormula(withoutCompleted(part, completed), items, glyphs, used); frs != nil {
				inline = append(inline, frs...)
			}
		}
	}
	return out, inline
}

// inlineFormula rebuilds a fraction or radical that sits inside a line of
// prose (its pieces extend above and below the line) as one inline math
// fragment placed on that line, so the paragraph keeps its order.
//
// A numerator set on the text line itself belongs to the prose fragment;
// it is taken from there (the fragment is split around the bar). The
// returned fragments are the formula and the remaining prose pieces.
func inlineFormula(g []*fitem, items []*fitem, glyphs []content.Glyph, used map[int]bool) []*frag {
	hasBar := false
	for _, it := range g {
		if it.b != nil {
			hasBar = true
		}
		if it.assigned {
			return nil
		}
	}
	if !hasBar {
		return nil
	}
	bb := groupBox(g)
	// The prose line it belongs to: the sentence fragment overlapping the
	// group's rows most, adjacent horizontally.
	var line *fitem
	best := 0.0
	for _, it := range items {
		if it.f == nil || it.f.used || !it.sentence || containsItem(g, it) {
			continue
		}
		ov := min(bb.Y1, it.bb.Y1) - max(bb.Y0, it.bb.Y0)
		gap := math.Max(it.bb.X0-bb.X1, bb.X0-it.bb.X1)
		if ov > best && gap < 2*it.size() {
			line, best = it, ov
		}
	}
	if line == nil {
		return nil
	}
	var leaves []mathElem
	var bs []bar
	for _, it := range g {
		if it.b != nil {
			bs = append(bs, *it.b)
			continue
		}
		for _, gi := range it.f.gidx {
			if e, ok := leafElem(&glyphs[gi]); ok {
				leaves = append(leaves, e)
			}
		}
	}
	// Numerator glyphs set on the text line (inside prose or a short
	// fragment): glyphs above a bar and within its span. A fragment that
	// also holds other text is split around the bar.
	// A radical bar starts at the top right of a √ in the group; its
	// radicand lies below it. Any other bar is a fraction bar.
	isRadicalBar := func(b bar) bool {
		for _, e := range leaves {
			if e.char == '√' && b.x0 >= e.bb.X1-0.6*e.size && b.x0 <= e.bb.X1+0.4*e.size && math.Abs(b.y-e.bb.Y0) <= 0.5*e.size {
				return true
			}
		}
		return false
	}
	var rest []*frag
	var source *fitem // the line the radicand was taken from
	for _, b := range bs {
		radical := isRadicalBar(b)
		for _, it := range items {
			if it.f == nil || it.f.used || it.assigned || containsItem(g, it) || len(it.f.gidx) == 0 {
				continue
			}
			var taken []mathElem
			all := true
			for _, gi := range it.f.gidx {
				gl := &glyphs[gi]
				c := gl.BBox().Center()
				var take bool
				if radical {
					// Radicand: under the radical bar, any size.
					take = c.X >= b.x0 && c.X <= b.x1 && c.Y > b.y && c.Y < b.y+1.2*gl.Size
				} else {
					// Numerator: inline fractions use a smaller (script) size;
					// full-size text of neighbouring lines is never taken.
					small := gl.Size <= 0.85*it.f.size || gl.Size <= 0.85*line.f.size
					take = small && c.X >= b.x0 && c.X <= b.x1 && c.Y < b.y && c.Y > b.y-1.0*gl.Size-2
				}
				if take {
					if e, ok := leafElem(gl); ok {
						taken = append(taken, e)
					}
				} else if strings.TrimSpace(gl.Text) != "" {
					all = false
				}
			}
			if len(taken) == 0 {
				continue
			}
			if radical && source == nil {
				source = it
			}
			leaves = append(leaves, taken...)
			it.f.used, it.assigned = true, true
			bb = bb.Union(model.Rect{X0: b.x0, Y0: bb.Y0, X1: b.x1, Y1: bb.Y1})
			if all {
				continue
			}
			left, mid, okL := it.f.splitAt(b.x0 - 0.1)
			if !okL {
				left, mid = nil, it.f
			}
			if _, right, okR := mid.splitAt(b.x1 + 0.1); okR {
				rest = append(rest, right)
			}
			if left != nil {
				rest = append(rest, left)
			}
		}
	}
	if source != nil {
		line = source
	}
	root := parseMath(leaves, bs, 0)
	if root == nil || len(root.Children) == 0 {
		return nil
	}
	// Sentence punctuation after the formula stays text.
	trail := ""
	if k := len(root.Children); k > 1 {
		if last := root.Children[k-1]; (last.Kind == model.MathOperator || last.Kind == model.MathNumber) && (last.Text == "." || last.Text == ",") {
			trail = last.Text
			root.Children = root.Children[:k-1]
		}
	}
	text := root.Linear()
	n := utf8.RuneCountInString(text)
	r := model.Rect{X0: bb.X0, Y0: line.bb.Y0, X1: bb.X1, Y1: line.bb.Y1}
	edges := make([]float64, n+1)
	for i := range edges {
		edges[i] = r.X0 + r.W()*float64(i)/float64(max(n, 1))
	}
	st := line.f.spans[0].Style
	st.Bold, st.Italic = false, false
	f := &frag{bbox: r, baseline: line.f.baseline, size: line.f.size, lastX1: r.X1,
		spans: []model.Span{{Text: text, BBox: r, Style: st, Edges: edges, Math: true, Formula: root, Confidence: 1}}}
	if trail != "" {
		f.spans = append(f.spans, model.Span{Text: trail, BBox: model.Rect{X0: r.X1, Y0: r.Y0, X1: r.X1, Y1: r.Y1},
			Style: st, Edges: []float64{r.X1, r.X1}, Confidence: 1})
	}
	for _, it := range g {
		it.assigned = true
		if it.f != nil {
			it.f.used = true
		} else {
			used[it.b.path] = true
		}
	}
	return append(rest, f)
}

// withoutCompleted drops fragments that joined only by row completion:
// for inline math they are the surrounding prose, not part of the formula.
func withoutCompleted(g []*fitem, completed map[*fitem]bool) []*fitem {
	out := make([]*fitem, 0, len(g))
	for _, it := range g {
		if !completed[it] {
			out = append(out, it)
		}
	}
	return out
}

func containsItem(g []*fitem, it *fitem) bool {
	for _, x := range g {
		if x == it {
			return true
		}
	}
	return false
}

// splitRows separates a group into rows; fragments on the same row and
// fractions (bar plus the rows touching it) stay together.
func splitRows(g []*fitem) [][]*fitem {
	find, union := newUnion(len(g))
	for i, a := range g {
		for j := i + 1; j < len(g); j++ {
			b := g[j]
			switch {
			case a.b != nil && b.b == nil:
				if barTouches(a, b) {
					union(i, j)
				}
			case b.b != nil && a.b == nil:
				if barTouches(b, a) {
					union(i, j)
				}
			case a.f != nil && b.f != nil:
				if sameRow(a.bb, b.bb) || isLimitOf(a, b) || isLimitOf(b, a) {
					union(i, j)
				}
			}
		}
	}
	parts := map[int][]*fitem{}
	for i, it := range g {
		parts[find(i)] = append(parts[find(i)], it)
	}
	out := make([][]*fitem, 0, len(parts))
	for _, p := range parts {
		out = append(out, p)
	}
	// Merge side-by-side components that share a row band, e.g. "x ="
	// next to a fraction whose numerator and denominator straddle it.
	for merged := true; merged; {
		merged = false
		for i := 0; i < len(out) && !merged; i++ {
			for j := i + 1; j < len(out); j++ {
				a, b := groupBox(out[i]), groupBox(out[j])
				s := max(maxSize(out[i]), maxSize(out[j]))
				hgap := math.Max(b.X0-a.X1, a.X0-b.X1)
				ov := min(a.Y1, b.Y1) - max(a.Y0, b.Y0)
				if hgap < 2*s && ov >= 0.5*min(a.H(), b.H()) {
					out[i] = append(out[i], out[j]...)
					out = append(out[:j], out[j+1:]...)
					merged = true
					break
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return groupBox(out[i]).Y0 < groupBox(out[j]).Y0 })
	return out
}

// isLimitOf reports whether fragment lim is a small limit stacked directly
// above or below a large operator in fragment op.
func isLimitOf(lim, op *fitem) bool {
	if lim.f.size >= 0.85*op.f.size && op.f.size > 0 {
		return false
	}
	for _, sp := range op.f.spans {
		for _, r := range sp.Text {
			if !strings.ContainsRune(bigOps, r) {
				continue
			}
			c := (lim.bb.X0 + lim.bb.X1) / 2
			gap := math.Max(lim.bb.Y0-op.bb.Y1, op.bb.Y0-lim.bb.Y1)
			return c >= sp.BBox.X0-0.6*op.f.size && c <= sp.BBox.X1+0.6*op.f.size && gap < 1.2*op.f.size
		}
	}
	return false
}

func maxSize(g []*fitem) float64 {
	s := 0.0
	for _, it := range g {
		if it.f != nil {
			s = max(s, it.f.size)
		}
	}
	return s
}

func groupBox(g []*fitem) model.Rect {
	var bb model.Rect
	for _, it := range g {
		bb = bb.Union(it.bb)
	}
	return bb
}

// makeFormula checks that a row group is a displayed formula and parses it.
func makeFormula(g []*fitem, frags []*frag, glyphs []content.Glyph, used map[int]bool) *model.Formula {
	bb := groupBox(g)
	m, l, hasBar, hasFrag := 0, 0, false, false
	for _, it := range g {
		if it.b != nil {
			hasBar = true
			continue
		}
		if it.assigned {
			return nil
		}
		hasFrag = true
		m += it.m
		l += it.l
	}
	if !hasFrag || (!hasBar && m < 2) {
		return nil
	}
	member := map[*frag]bool{}
	size := 0.0
	for _, it := range g {
		if it.f != nil {
			member[it.f] = true
			size = max(size, it.f.size)
		}
	}
	// No other text on the formula's rows, except an equation number.
	number := ""
	var numFrag *frag
	for _, f := range frags {
		if f.used || member[f] || !sameRow(f.bbox, bb) && !sameRow(bb, f.bbox) {
			continue
		}
		ov := min(f.bbox.Y1, bb.Y1) - max(f.bbox.Y0, bb.Y0)
		if ov <= 0.5*min(f.bbox.H(), bb.H()) {
			continue
		}
		if t := strings.TrimSpace(f.text()); eqNumberRe.MatchString(t) && f.bbox.X0 > bb.X1 {
			number, numFrag = t, f
			continue
		}
		return nil
	}
	// Displayed: centred or indented within the prose column around it.
	var col model.Rect
	for _, f := range frags {
		if member[f] || f == numFrag || f.rotated {
			continue
		}
		fm, fl := fragMathStats(f)
		if fl < 10 || fl <= 2*fm {
			continue // not a prose line
		}
		near := f.bbox.Y1 > bb.Y0-4*size && f.bbox.Y0 < bb.Y1+4*size
		overlapX := f.bbox.X1 > bb.X0 && f.bbox.X0 < bb.X1
		if near && overlapX {
			col = col.Union(f.bbox)
		}
	}
	if col.Empty() {
		if l > 2*m && !hasBar {
			return nil // no context and mostly letters: treat as text
		}
	} else {
		w := col.W()
		centred := math.Abs((bb.X0+bb.X1)/2-(col.X0+col.X1)/2) < 0.12*w
		indented := bb.X0 > col.X0+0.05*w
		if bb.W() > 0.92*w || !(centred || indented) {
			return nil
		}
	}
	var leaves []mathElem
	var bs []bar
	for _, it := range g {
		if it.b != nil {
			bs = append(bs, *it.b)
			continue
		}
		for _, gi := range it.f.gidx {
			if e, ok := leafElem(&glyphs[gi]); ok {
				leaves = append(leaves, e)
			}
		}
	}
	root := parseMath(leaves, bs, 0)
	if root == nil || len(root.Children) == 0 {
		return nil
	}
	for _, it := range g {
		it.assigned = true
		if it.f != nil {
			it.f.used = true
		} else {
			used[it.b.path] = true
		}
	}
	if numFrag != nil {
		numFrag.used = true
		bb = bb.Union(numFrag.bbox)
	}
	return &model.Formula{BBox: bb, Root: root, Number: number}
}

// ---------------------------------------------------------------------------
// Parsing glyph geometry into a math tree

type mathElem struct {
	node *model.MathNode
	bb   model.Rect
	base float64 // baseline (y grows downwards)
	size float64
	char rune // for leaves: the (first) character
	bar  *bar
}

func leafElem(g *content.Glyph) (mathElem, bool) {
	text := strings.TrimSpace(g.Text)
	if text == "" || g.Space {
		return mathElem{}, false
	}
	r, _ := utf8.DecodeRuneInString(text)
	kind := model.MathOperator
	switch {
	case unicode.IsDigit(r) || r == '.' && len(text) == 1:
		kind = model.MathNumber
	case unicode.IsLetter(r):
		kind = model.MathIdent
	}
	italic := g.Font != nil && (g.Font.Italic || strings.HasPrefix(strings.ToLower(g.Font.Name), "cmmi"))
	n := &model.MathNode{Kind: kind, Text: text, Upright: kind == model.MathIdent && !italic && !isGreek(r)}
	return mathElem{node: n, bb: g.BBox(), base: g.Origin.Y, size: g.Size, char: r}, true
}

func isGreek(r rune) bool { return r >= 0x0370 && r <= 0x03FF }

// plainWords counts words of two or more ordinary (non-math) letters.
func plainWords(f *frag) int {
	n := 0
	for _, sp := range f.spans {
		if sp.Math {
			continue
		}
		run := 0
		for _, r := range sp.Text + " " {
			if unicode.IsLetter(r) && !isGreek(r) {
				run++
				continue
			}
			if run >= 2 {
				n++
			}
			run = 0
		}
	}
	return n
}

var bigOps = "∑∏∐∫∬∭∮⋃⋂⋁⋀⨁⨂"

// parseMath builds the tree for a set of glyph leaves and bars.
func parseMath(elems []mathElem, bars []bar, depth int) *model.MathNode {
	if depth > 12 {
		return &model.MathNode{Kind: model.MathRow}
	}
	// 1. Fractions: longest bars first, so outer fractions contain inner ones.
	sort.Slice(bars, func(i, j int) bool { return bars[i].x1-bars[i].x0 > bars[j].x1-bars[j].x0 })
	var restBars []bar
	for bi := 0; bi < len(bars); bi++ {
		b := bars[bi]
		num, den, rest := splitFraction(elems, b)
		if len(num) == 0 || len(den) == 0 {
			restBars = append(restBars, b)
			continue
		}
		// Bars inside the numerator/denominator belong to nested fractions.
		var nb, db, ob []bar
		for _, o := range bars[bi+1:] {
			switch {
			case o.x0 >= b.x0-0.5 && o.x1 <= b.x1+0.5 && o.y < b.y:
				nb = append(nb, o)
			case o.x0 >= b.x0-0.5 && o.x1 <= b.x1+0.5 && o.y > b.y:
				db = append(db, o)
			default:
				ob = append(ob, o)
			}
		}
		bars = append(bars[:bi+1], ob...)
		numN, denN := parseMath(num, nb, depth+1), parseMath(den, db, depth+1)
		size := 0.0
		bb := model.Rect{X0: b.x0, Y0: b.y, X1: b.x1, Y1: b.y + 0.01}
		for _, e := range append(num, den...) {
			size = max(size, e.size)
			bb = bb.Union(e.bb)
		}
		frac := mathElem{node: &model.MathNode{Kind: model.MathFrac, Children: []*model.MathNode{numN, denN}},
			bb: bb, base: b.y + 0.25*size, size: size}
		elems = append(rest, frac)
	}
	// 2. Radicals: √ followed by an overbar covering the radicand.
	elems, restBars = parseRadicals(elems, restBars, depth)
	// 3. Large operators with limits stacked above/below.
	elems = parseLimits(elems, depth)
	// 4. Horizontal sequence with superscripts and subscripts.
	return parseScripts(elems, depth)
}

// splitFraction returns the elements stacked directly above (numerator)
// and below (denominator) a bar, and the remaining elements.
func splitFraction(elems []mathElem, b bar) (num, den, rest []mathElem) {
	inX := func(e mathElem) bool {
		c := (e.bb.X0 + e.bb.X1) / 2
		return c >= b.x0-0.5 && c <= b.x1+0.5
	}
	var above, below []int
	for i, e := range elems {
		if !inX(e) {
			continue
		}
		switch {
		case e.bb.Y1 <= b.y+0.6*e.size*0.25 && (e.bb.Y0+e.bb.Y1)/2 < b.y:
			above = append(above, i)
		case e.bb.Y0 >= b.y-0.6*e.size*0.25 && (e.bb.Y0+e.bb.Y1)/2 > b.y:
			below = append(below, i)
		}
	}
	take := map[int]bool{}
	// Numerator: grow upwards from the bar while elements stay connected.
	sort.Slice(above, func(i, j int) bool { return elems[above[i]].bb.Y1 > elems[above[j]].bb.Y1 })
	top := b.y
	for _, i := range above {
		e := elems[i]
		if e.bb.Y1 >= top-0.7*e.size {
			take[i] = true
			num = append(num, e)
			top = math.Min(top, e.bb.Y0)
		}
	}
	sort.Slice(below, func(i, j int) bool { return elems[below[i]].bb.Y0 < elems[below[j]].bb.Y0 })
	bottom := b.y
	for _, i := range below {
		e := elems[i]
		if e.bb.Y0 <= bottom+0.7*e.size {
			take[i] = true
			den = append(den, e)
			bottom = math.Max(bottom, e.bb.Y1)
		}
	}
	for i, e := range elems {
		if !take[i] {
			rest = append(rest, e)
		}
	}
	return
}

func parseRadicals(elems []mathElem, bars []bar, depth int) ([]mathElem, []bar) {
	sort.Slice(elems, func(i, j int) bool { return elems[i].bb.X0 < elems[j].bb.X0 })
	for i := 0; i < len(elems); i++ {
		r := elems[i]
		if r.node == nil || r.node.Kind != model.MathOperator || r.char != '√' {
			continue
		}
		// Overbar starting at the radical sign's top right.
		bi := -1
		for k, b := range bars {
			if b.x0 >= r.bb.X1-0.6*r.size && b.x0 <= r.bb.X1+0.4*r.size && math.Abs(b.y-r.bb.Y0) <= 0.5*r.size {
				bi = k
				break
			}
		}
		var body, rest []mathElem
		var index *mathElem
		right := r.bb.X1 + 1.5*r.size
		if bi >= 0 {
			right = bars[bi].x1 + 0.5
		}
		for k, e := range elems {
			if k == i {
				continue
			}
			c := (e.bb.X0 + e.bb.X1) / 2
			cy := (e.bb.Y0 + e.bb.Y1) / 2
			switch {
			case c > r.bb.X1-0.2*r.size && c <= right && cy > r.bb.Y0-0.2*r.size && cy < r.bb.Y1+0.2*r.size &&
				(bi >= 0 || len(body) == 0):
				body = append(body, e)
			case e.bb.X1 <= r.bb.X0+0.6*r.size && e.bb.Y1 < (r.bb.Y0+r.bb.Y1)/2 && e.size < 0.8*r.size && index == nil:
				ee := e
				index = &ee
			default:
				rest = append(rest, e)
			}
		}
		if len(body) == 0 {
			continue
		}
		var node *model.MathNode
		bodyN := parseMath(body, nil, depth+1)
		bb := r.bb
		for _, e := range body {
			bb = bb.Union(e.bb)
		}
		if index != nil {
			node = &model.MathNode{Kind: model.MathRoot, Children: []*model.MathNode{bodyN, parseMath([]mathElem{*index}, nil, depth+1)}}
			bb = bb.Union(index.bb)
		} else {
			node = &model.MathNode{Kind: model.MathSqrt, Children: []*model.MathNode{bodyN}}
		}
		base := body[0].base
		for _, e := range body {
			if e.size >= body[0].size {
				base = e.base
			}
		}
		if bi >= 0 {
			bars = append(bars[:bi], bars[bi+1:]...)
		}
		elems = append(rest, mathElem{node: node, bb: bb, base: base, size: r.size})
		sort.Slice(elems, func(i, j int) bool { return elems[i].bb.X0 < elems[j].bb.X0 })
		i = -1 // restart: the list changed
	}
	return elems, bars
}

func parseLimits(elems []mathElem, depth int) []mathElem {
	for i := 0; i < len(elems); i++ {
		op := elems[i]
		if op.node == nil || op.node.Kind != model.MathOperator || !strings.ContainsRune(bigOps, op.char) {
			continue
		}
		var under, over, rest []mathElem
		for k, e := range elems {
			if k == i {
				continue
			}
			c := (e.bb.X0 + e.bb.X1) / 2
			inX := c >= op.bb.X0-0.4*op.size && c <= op.bb.X1+0.4*op.size
			switch {
			case inX && e.bb.Y1 <= op.bb.Y0+0.15*op.size && e.bb.Y1 >= op.bb.Y0-1.2*op.size:
				over = append(over, e)
			case inX && e.bb.Y0 >= op.bb.Y1-0.15*op.size && e.bb.Y0 <= op.bb.Y1+1.2*op.size:
				under = append(under, e)
			default:
				rest = append(rest, e)
			}
		}
		if len(under) == 0 && len(over) == 0 {
			continue
		}
		bb := op.bb
		for _, e := range append(under, over...) {
			bb = bb.Union(e.bb)
		}
		node := &model.MathNode{Kind: model.MathUnderOver, Children: []*model.MathNode{
			op.node, parseMath(under, nil, depth+1), parseMath(over, nil, depth+1)}}
		elems = append(rest, mathElem{node: node, bb: bb, base: op.base, size: op.size})
		i = -1
	}
	return elems
}

// parseScripts orders elements left to right and attaches raised/lowered
// smaller elements to the preceding base as superscripts/subscripts.
func parseScripts(elems []mathElem, depth int) *model.MathNode {
	row := &model.MathNode{Kind: model.MathRow}
	if len(elems) == 0 {
		return row
	}
	sort.Slice(elems, func(i, j int) bool { return elems[i].bb.X0 < elems[j].bb.X0 })
	// Reference size and baseline come from ordinary symbols; large
	// operators and fractions are taller and sit differently.
	ordinary := func(e mathElem) bool {
		k := e.node.Kind
		return k != model.MathUnderOver && k != model.MathFrac && !(k == model.MathOperator && strings.ContainsRune(bigOps, e.char))
	}
	ref := 0.0
	for _, e := range elems {
		if ordinary(e) {
			ref = max(ref, e.size)
		}
	}
	if ref == 0 {
		for _, e := range elems {
			ref = max(ref, e.size)
		}
	}
	// Main baseline: most common baseline of the full-size elements.
	counts := map[int]int{}
	for _, e := range elems {
		if ordinary(e) && e.size >= 0.9*ref {
			counts[int(math.Round(e.base))]++
		}
	}
	mainBase, best := elems[0].base, -1
	for b, n := range counts {
		if n > best {
			mainBase, best = float64(b), n
		}
	}
	class := func(e mathElem) int {
		if e.size >= 0.88*ref || !ordinary(e) {
			return 0
		}
		switch d := e.base - mainBase; {
		case d < -0.18*ref:
			return 1 // superscript
		case d > 0.1*ref:
			return -1 // subscript
		}
		return 0
	}
	var prev *model.MathNode
	for i := 0; i < len(elems); {
		c := class(elems[i])
		if c == 0 {
			n := elems[i].node
			// Merge adjacent digits into one number.
			if n.Kind == model.MathNumber && prev != nil && prev.Kind == model.MathNumber &&
				i > 0 && elems[i].bb.X0-elems[i-1].bb.X1 < 0.15*ref && class(elems[i-1]) == 0 {
				prev.Text += n.Text
				i++
				continue
			}
			// Merge adjacent upright letters into one word (sin, log, max).
			if n.Kind == model.MathIdent && n.Upright && prev != nil && prev.Kind == model.MathIdent && prev.Upright &&
				i > 0 && elems[i].bb.X0-elems[i-1].bb.X1 < 0.15*ref && class(elems[i-1]) == 0 {
				prev.Text += n.Text
				i++
				continue
			}
			cp := *n
			row.Children = append(row.Children, &cp)
			prev = row.Children[len(row.Children)-1]
			i++
			continue
		}
		// Collect the run of script elements; superscripts and subscripts
		// stacked at the same position are separated by class.
		j := i
		var sups, subs []mathElem
		for j < len(elems) && class(elems[j]) != 0 {
			if class(elems[j]) == 1 {
				sups = append(sups, elems[j])
			} else {
				subs = append(subs, elems[j])
			}
			j++
		}
		if len(subs) > 0 {
			attach(row, parseScripts(subs, depth+1), -1)
		}
		if len(sups) > 0 {
			attach(row, parseScripts(sups, depth+1), 1)
		}
		prev = nil
		i = j
	}
	return row
}

// attach adds a superscript (c=1) or subscript (c=-1) to the last node
// of the row, combining into SubSup when both are present.
func attach(row *model.MathNode, script *model.MathNode, c int) {
	if len(row.Children) == 0 {
		row.Children = append(row.Children, &model.MathNode{Kind: model.MathRow})
	}
	k := len(row.Children) - 1
	base := row.Children[k]
	switch {
	case c == 1 && base.Kind == model.MathSub:
		row.Children[k] = &model.MathNode{Kind: model.MathSubSup, Children: []*model.MathNode{base.Children[0], base.Children[1], script}}
	case c == -1 && base.Kind == model.MathSup:
		row.Children[k] = &model.MathNode{Kind: model.MathSubSup, Children: []*model.MathNode{base.Children[0], script, base.Children[1]}}
	case c == 1:
		row.Children[k] = &model.MathNode{Kind: model.MathSup, Children: []*model.MathNode{base, script}}
	default:
		row.Children[k] = &model.MathNode{Kind: model.MathSub, Children: []*model.MathNode{base, script}}
	}
}
