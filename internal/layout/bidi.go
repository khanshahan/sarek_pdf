package layout

import (
	"unicode"
	"unicode/utf8"

	"github.com/khanshahan/sarek_pdf/model"
)

// PDF content places right-to-left text (Hebrew, Arabic, …) in visual
// order. reorderRTL converts a visually ordered line whose dominant
// direction is RTL into logical order: the character sequence is reversed,
// left-to-right runs (Latin, digits) are restored to reading order and
// paired brackets are mirrored. Per-character geometry is preserved, so
// search and highlighting keep working.

type bidiClass uint8

const (
	bidiN bidiClass = iota // neutral
	bidiL                  // left-to-right strong (letters, digits)
	bidiR                  // right-to-left strong
)

func classify(r rune) bidiClass {
	switch {
	case r >= 0x0590 && r <= 0x08FF, r >= 0xFB1D && r <= 0xFDFF, r >= 0xFE70 && r <= 0xFEFF,
		r >= 0x10800 && r <= 0x10FFF, r >= 0x1E800 && r <= 0x1EFFF:
		return bidiR
	case unicode.IsLetter(r) || unicode.IsDigit(r):
		return bidiL
	}
	return bidiN
}

var mirror = map[rune]rune{'(': ')', ')': '(', '[': ']', ']': '[', '{': '}', '}': '{',
	'<': '>', '>': '<', '«': '»', '»': '«', '‹': '›', '›': '‹'}

type glyphCell struct {
	r      rune
	x0, x1 float64
	span   int
}

// isRTLLine reports whether RTL characters dominate the line.
func isRTLLine(l *model.Line) bool {
	var nr, nl int
	for _, s := range l.Spans {
		for _, r := range s.Text {
			switch classify(r) {
			case bidiR:
				nr++
			case bidiL:
				nl++
			}
		}
	}
	return nr > 0 && nr >= nl
}

func reorderRTL(l *model.Line) {
	base, any := bidiL, false
	var nr, nl int
	for _, sp := range l.Spans {
		for _, r := range sp.Text {
			switch classify(r) {
			case bidiR:
				nr++
				any = true
			case bidiL:
				nl++
			}
		}
	}
	if !any {
		return
	}
	if nr >= nl {
		base = bidiR
	}
	var cells []glyphCell
	for si, s := range l.Spans {
		i := 0
		for _, r := range s.Text {
			x0, x1 := s.BBox.X0, s.BBox.X1
			if i+1 < len(s.Edges) {
				x0, x1 = s.Edges[i], s.Edges[i+1]
			}
			cells = append(cells, glyphCell{r, x0, x1, si})
			i++
		}
	}
	n := len(cells)
	cls := make([]bidiClass, n)
	for i, c := range cells {
		cls[i] = classify(c.r)
	}
	// Resolve neutrals: they take the direction of their strong neighbours
	// when both agree, otherwise the base direction.
	res := make([]bidiClass, n)
	for i := range cells {
		if cls[i] != bidiN {
			res[i] = cls[i]
			continue
		}
		left, right := base, base
		for k := i - 1; k >= 0; k-- {
			if cls[k] != bidiN {
				left = cls[k]
				break
			}
		}
		for k := i + 1; k < n; k++ {
			if cls[k] != bidiN {
				right = cls[k]
				break
			}
		}
		if left == right {
			res[i] = left
		} else {
			res[i] = base
		}
	}
	// RTL base: reverse everything, then restore each L run.
	// LTR base: keep order, reverse each R run.
	order := make([]int, n)
	inner := bidiR
	for i := range order {
		order[i] = i
	}
	if base == bidiR {
		for i := range order {
			order[i] = n - 1 - i
		}
		inner = bidiL
	}
	for i := 0; i < n; {
		if res[order[i]] != inner {
			i++
			continue
		}
		j := i
		for j < n && res[order[j]] == inner {
			j++
		}
		for a, b := i, j-1; a < b; a, b = a+1, b-1 {
			order[a], order[b] = order[b], order[a]
		}
		i = j
	}
	// Rebuild spans: split on style or direction change.
	var spans []model.Span
	var prevSpan = -1
	var prevDir bidiClass
	for _, idx := range order {
		c := cells[idx]
		r := c.r
		if res[idx] == bidiR {
			if m, ok := mirror[r]; ok {
				r = m
			}
		}
		src := l.Spans[c.span]
		if len(spans) == 0 || c.span != prevSpan || res[idx] != prevDir {
			spans = append(spans, model.Span{Style: src.Style, Invisible: src.Invisible,
				BBox: model.Rect{X0: min(c.x0, c.x1), Y0: src.BBox.Y0, X1: max(c.x0, c.x1), Y1: src.BBox.Y1}})
			sp := &spans[len(spans)-1]
			if res[idx] == bidiR {
				sp.Edges = []float64{c.x1, c.x0}
			} else {
				sp.Edges = []float64{c.x0, c.x1}
			}
			sp.Text = string(r)
		} else {
			sp := &spans[len(spans)-1]
			sp.Text += string(r)
			if res[idx] == bidiR {
				sp.Edges = append(sp.Edges, c.x0)
			} else {
				sp.Edges = append(sp.Edges, c.x1)
			}
			sp.BBox.X0, sp.BBox.X1 = min(sp.BBox.X0, c.x0, c.x1), max(sp.BBox.X1, c.x0, c.x1)
		}
		prevSpan, prevDir = c.span, res[idx]
	}
	for i := range spans {
		if utf8.RuneCountInString(spans[i].Text)+1 != len(spans[i].Edges) {
			spans[i].Edges = nil // defensive: never emit inconsistent geometry
		}
	}
	l.Spans = spans // not merged: spans of opposite direction must stay apart
}
