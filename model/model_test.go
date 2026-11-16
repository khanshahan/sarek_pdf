package model

import (
	"strings"
	"testing"
)

func TestMergedCellTextAppearsOnce(t *testing.T) {
	cell := func(r, c, rs, cs int, text string) *Cell {
		return &Cell{Row: r, Col: c, RowSpan: rs, ColSpan: cs,
			Paragraphs: []*Paragraph{{Runs: []Run{{Text: text}}}}}
	}
	tbl := &Table{ColX: []float64{0, 1, 2}, RowY: []float64{0, 1, 2, 3},
		Cells: []*Cell{cell(0, 0, 3, 1, "merged"), cell(0, 1, 1, 1, "a"), cell(1, 1, 1, 1, "b"), cell(2, 1, 1, 1, "c")}}
	p := &Page{Blocks: []Block{tbl}}
	if n := strings.Count(p.Text(), "merged"); n != 1 {
		t.Errorf("merged cell text appears %d times:\n%s", n, p.Text())
	}
	if g := tbl.Grid(); g[2][0] != "merged" {
		t.Errorf("Grid must repeat merged text, got %v", g)
	}
}
