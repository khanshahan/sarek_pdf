package layout

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/khanshahan/sarek_pdf/internal/textnorm"
	"github.com/khanshahan/sarek_pdf/model"
)

// buildLines clusters fragments into visual lines (top to bottom, left to
// right within a line).
func buildLines(frags []*frag) []model.Line {
	if len(frags) == 0 {
		return nil
	}
	fs := make([]*frag, 0, len(frags))
	for _, f := range frags {
		if len(f.spans) > 0 {
			fs = append(fs, f)
		}
	}
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].baseline < fs[j].baseline })
	type group struct {
		frags        []*frag
		y0, y1, base float64
		size         float64
	}
	var groups []*group
	for _, f := range fs {
		var best *group
		bestOv := 0.0
		for k := len(groups) - 1; k >= 0 && k >= len(groups)-4; k-- {
			g := groups[k]
			ov := min(g.y1, f.bbox.Y1) - max(g.y0, f.bbox.Y0)
			h := min(g.y1-g.y0, f.bbox.H())
			if h > 0 && ov/h >= 0.5 && math.Abs(f.baseline-g.base) < 0.6*max(g.size, f.size) && ov/h > bestOv {
				best, bestOv = g, ov/h
			}
		}
		if best == nil {
			groups = append(groups, &group{frags: []*frag{f}, y0: f.bbox.Y0, y1: f.bbox.Y1, base: f.baseline, size: f.size})
			continue
		}
		best.frags = append(best.frags, f)
		best.y0, best.y1 = min(best.y0, f.bbox.Y0), max(best.y1, f.bbox.Y1)
		if f.size > best.size {
			best.size, best.base = f.size, f.baseline
		}
	}
	lines := make([]model.Line, 0, len(groups))
	for _, g := range groups {
		sort.SliceStable(g.frags, func(i, j int) bool { return g.frags[i].bbox.X0 < g.frags[j].bbox.X0 })
		var ln model.Line
		ln.Baseline = g.base
		var prev *frag
		for _, f := range g.frags {
			if prev != nil {
				// Duplicate fragment (same text drawn twice, slightly offset).
				ov := min(prev.bbox.X1, f.bbox.X1) - max(prev.bbox.X0, f.bbox.X0)
				if ov > 0.7*f.bbox.W() && prev.text() == f.text() {
					continue
				}
				gap := f.bbox.X0 - prev.bbox.X1
				if gap > 0.1*g.size && !endsWithSpace(ln) {
					st := f.spans[0].Style
					ln.Spans = append(ln.Spans, model.Span{Text: " ", Style: st,
						Source: f.spans[0].Source, Confidence: f.spans[0].Confidence,
						BBox:  model.Rect{X0: prev.bbox.X1, Y0: f.bbox.Y0, X1: f.bbox.X0, Y1: f.bbox.Y1},
						Edges: []float64{prev.bbox.X1, f.bbox.X0}})
				}
			}
			ln.Spans = append(ln.Spans, f.spans...)
			ln.BBox = ln.BBox.Union(f.bbox)
			prev = f
		}
		ln.Spans = mergeSpans(ln.Spans)
		reorderRTL(&ln)
		lines = append(lines, ln)
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].BBox.Y0 < lines[j].BBox.Y0 })
	return lines
}

func endsWithSpace(l model.Line) bool {
	if n := len(l.Spans); n > 0 {
		t := l.Spans[n-1].Text
		return strings.HasSuffix(t, " ")
	}
	return true
}

// mergeSpans joins adjacent spans with identical style.
func mergeSpans(sp []model.Span) []model.Span {
	if len(sp) < 2 {
		return sp
	}
	out := sp[:1]
	for _, s := range sp[1:] {
		last := &out[len(out)-1]
		if sameStyle(last.Style, s.Style) && last.Invisible == s.Invisible && last.Source == s.Source && last.Confidence == s.Confidence {
			last.Text += s.Text
			last.Edges = append(last.Edges[:len(last.Edges)-1], s.Edges...)
			last.BBox = last.BBox.Union(s.BBox)
			continue
		}
		out = append(out, s)
	}
	return out
}

// lineSize returns the dominant (character-weighted) font size of a line.
func lineSize(l *model.Line) float64 {
	best, bestN := 0.0, -1
	counts := map[float64]int{}
	for _, s := range l.Spans {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		counts[s.Style.Size] += utf8.RuneCountInString(s.Text)
	}
	for sz, n := range counts {
		if n > bestN || (n == bestN && sz > best) {
			best, bestN = sz, n
		}
	}
	if best == 0 {
		best = l.BBox.H() * 0.8
	}
	return best
}

func lineBold(l *model.Line) bool {
	for _, s := range l.Spans {
		if strings.TrimSpace(s.Text) != "" && !s.Style.Bold {
			return false
		}
	}
	return true
}

var bulletRunes = "•◦▪▫●○■□–-*·‣⁃➢✓✔"

func startsListItem(l *model.Line) bool {
	t := strings.TrimSpace(l.Text())
	if t == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(t)
	if strings.ContainsRune(bulletRunes, r) && len(t) > 1 {
		r2, _ := utf8.DecodeRuneInString(t[utf8.RuneLen(r):])
		return r2 == ' ' || r == '•' || r == '●' || r == '▪'
	}
	// "1." "1)" "a)" "iv." "(a)"
	i := 0
	if t[0] == '(' {
		i = 1
	}
	j := i
	for j < len(t) && j-i < 4 && (t[j] >= '0' && t[j] <= '9' || t[j] >= 'a' && t[j] <= 'z' && j-i < 1 || strings.IndexByte("ivx", t[j]) >= 0) {
		j++
	}
	return j > i && j+1 < len(t) && (t[j] == '.' || t[j] == ')') && t[j+1] == ' '
}

// paragraphsFromLines groups consecutive lines into paragraphs.
// region is the bounding box of the containing layout region.
func paragraphsFromLines(lines []model.Line, region model.Rect, bodySize float64) []*model.Paragraph {
	var out []*model.Paragraph
	var cur []model.Line
	flush := func() {
		if len(cur) > 0 {
			out = append(out, makeParagraph(cur, region, bodySize))
			cur = nil
		}
	}
	medGap := medianLineGap(lines)
	for i := range lines {
		l := lines[i]
		if strings.TrimSpace(l.Text()) == "" {
			continue
		}
		if len(cur) > 0 {
			prev := &cur[len(cur)-1]
			ps, cs := lineSize(prev), lineSize(&l)
			gap := l.BBox.Y0 - prev.BBox.Y1
			brk := false
			switch {
			case gap > max(0.8*cs, 1.6*medGap+0.5):
				brk = true
			case math.Abs(ps-cs) > 0.12*max(ps, cs):
				brk = true
			case lineBold(prev) != lineBold(&l) && (len(cur) == 1 || lineBold(&l)):
				brk = true // bold heading line followed by body text
			case startsListItem(&l):
				brk = true
			case l.BBox.X0-cur[0].BBox.X0 > 1.0*cs && len(cur) > 1 && prev.BBox.X1 < region.X1-2*cs:
				brk = true // indented first line after a short line
			case prev.BBox.X1 < region.X1-0.35*region.W() && len(cur) >= 1 && strings.HasSuffix(strings.TrimSpace(prev.Text()), ".") && region.W() > 20*cs:
				brk = true // previous line ended a sentence well before the margin
			case l.BBox.X0 > prev.BBox.X1 || l.BBox.X1 < prev.BBox.X0:
				brk = true // no horizontal overlap
			}
			if brk {
				flush()
			}
		}
		cur = append(cur, l)
	}
	flush()
	return out
}

func medianLineGap(lines []model.Line) float64 {
	if len(lines) < 2 {
		return 2
	}
	gaps := make([]float64, 0, len(lines))
	for i := 1; i < len(lines); i++ {
		if g := lines[i].BBox.Y0 - lines[i-1].BBox.Y1; g > -2 {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) == 0 {
		return 2
	}
	sort.Float64s(gaps)
	return max(gaps[len(gaps)/2], 0)
}

func makeParagraph(lines []model.Line, region model.Rect, bodySize float64) *model.Paragraph {
	p := &model.Paragraph{Lines: lines}
	for _, l := range lines {
		p.BBox = p.BBox.Union(l.BBox)
	}
	p.Runs = buildRuns(lines)
	p.Align = detectAlign(lines, region)
	p.RTL = isRTLLine(&lines[0])
	size := lineSize(&lines[0])
	text := strings.TrimSpace(p.Text())
	switch {
	case startsListItem(&lines[0]):
		p.Role = model.RoleListItem
	case bodySize > 0 && len(lines) <= 4 && len(text) < 300 && size >= 1.15*bodySize:
		p.Role = model.RoleHeading
		switch r := size / bodySize; {
		case r >= 1.9:
			p.Level = 1
		case r >= 1.45:
			p.Level = 2
		default:
			p.Level = 3
		}
	case len(lines) == 1 && lineBold(&lines[0]) && len(text) < 120 && len(text) > 1 &&
		!strings.HasSuffix(text, ".") && !strings.HasSuffix(text, ",") && hasLetter(text):
		p.Role, p.Level = model.RoleHeading, 4
	}
	return p
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// buildRuns joins lines into flowing text, repairing end-of-line
// hyphenation only where unambiguous (soft hyphen, or hyphen between two
// lowercase letters).
func buildRuns(lines []model.Line) []model.Run {
	var runs []model.Run
	add := func(text string, st model.Style) {
		if text == "" {
			return
		}
		if n := len(runs); n > 0 && sameStyle(runs[n-1].Style, st) {
			runs[n-1].Text += text
			return
		}
		runs = append(runs, model.Run{Text: text, Style: st})
	}
	for i, l := range lines {
		for j, s := range l.Spans {
			t := s.Text
			if j == len(l.Spans)-1 && i < len(lines)-1 {
				t = strings.TrimRight(t, " ")
				next := strings.TrimLeft(lines[i+1].Text(), " ")
				nr, _ := utf8.DecodeRuneInString(next)
				switch {
				case strings.HasSuffix(t, "\u00AD"):
					t = strings.TrimSuffix(t, "\u00AD")
				case strings.HasSuffix(t, "-") && unicode.IsLower(nr) && prevIsLower(t):
					t = strings.TrimSuffix(t, "-")
				case endsCJK(t) && startsCJK(next):
					// CJK text has no inter-word spaces: join lines directly.
				default:
					t += " "
				}
			}
			if j == 0 && i > 0 {
				t = strings.TrimLeft(t, " ")
			}
			add(t, s.Style)
		}
	}
	if n := len(runs); n > 0 {
		runs[n-1].Text = strings.TrimRight(runs[n-1].Text, " ")
	}
	return runs
}

func endsCJK(t string) bool {
	r, _ := utf8.DecodeLastRuneInString(t)
	return textnorm.IsCJK(r)
}

func startsCJK(t string) bool {
	r, _ := utf8.DecodeRuneInString(t)
	return textnorm.IsCJK(r)
}

func prevIsLower(t string) bool {
	t = strings.TrimSuffix(t, "-")
	r, _ := utf8.DecodeLastRuneInString(t)
	return unicode.IsLower(r)
}

func detectAlign(lines []model.Line, region model.Rect) model.Align {
	if region.W() <= 0 {
		return model.AlignLeft
	}
	tol := max(2, 0.02*region.W())
	if len(lines) == 1 {
		l := lines[0].BBox
		lm, rm := l.X0-region.X0, region.X1-l.X1
		switch {
		case lm > 0.15*region.W() && math.Abs(lm-rm) < max(tol, 0.05*region.W()):
			return model.AlignCenter
		case lm > 0.4*region.W() && rm < tol:
			return model.AlignRight
		}
		return model.AlignLeft
	}
	var leftVar, rightVar, centerVar float64
	l0 := lines[0].BBox
	full := 0
	for i, l := range lines {
		b := l.BBox
		if i > 0 {
			leftVar = max(leftVar, math.Abs(b.X0-lines[1].BBox.X0))
		}
		if i < len(lines)-1 {
			rightVar = max(rightVar, math.Abs(b.X1-l0.X1))
		}
		centerVar = max(centerVar, math.Abs((b.X0+b.X1)/2-(l0.X0+l0.X1)/2))
		if region.X1-b.X1 < tol {
			full++
		}
	}
	switch {
	case leftVar < tol && rightVar < tol && len(lines) > 2:
		return model.AlignJustify
	case leftVar < tol:
		return model.AlignLeft
	case centerVar < tol:
		return model.AlignCenter
	case rightVar < tol && full >= len(lines)-1:
		return model.AlignRight
	}
	return model.AlignLeft
}
