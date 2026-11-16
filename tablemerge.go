package sarekpdf

import (
	"math"

	"github.com/khanshahan/sarek_pdf/model"
)

// linkContinuedTables links a table that ends a page with a table that
// starts the next page when they are evidently one table broken by the
// page boundary: same number of columns, column boundaries aligned
// (relative to page width) and the same ruling style. A first row that
// repeats the header of the previous part is recorded in HeaderRows.
func linkContinuedTables(pages []*model.Page) {
	for i := 0; i+1 < len(pages); i++ {
		a, b := pages[i], pages[i+1]
		if a == nil || b == nil || b.Number != a.Number+1 {
			continue
		}
		t1 := edgeTable(a, false)
		t2 := edgeTable(b, true)
		if t1 == nil || t2 == nil || t1 == t2 || t2.Prev != nil || t1.Next != nil {
			continue
		}
		if !sameColumns(t1, a.Width, t2, b.Width) || t1.Ruled != t2.Ruled {
			continue
		}
		t1.Next, t2.Prev = t2, t1
		t2.HeaderRows = repeatedHeader(firstPart(t1), t2)
	}
}

func firstPart(t *model.Table) *model.Table {
	for n := 0; t.Prev != nil && n < 10000; n++ {
		t = t.Prev
	}
	return t
}

// edgeTable returns the first (start) or last (!start) content block of
// the page if it is a table, ignoring running headers/footers and page
// furniture such as small page-number paragraphs.
func edgeTable(p *model.Page, start bool) *model.Table {
	n := len(p.Blocks)
	for k := 0; k < n; k++ {
		i := n - 1 - k
		if start {
			i = k
		}
		switch v := p.Blocks[i].(type) {
		case *model.Table:
			return v
		case *model.Paragraph:
			if v.Role == model.RolePageHeader || v.Role == model.RolePageFooter || isPageFurniture(v, p) {
				continue
			}
			return nil
		case *model.Image, *model.Drawing:
			// Logos and rules in the page margins do not interrupt a table.
			bb := v.Bounds()
			if bb.Y1 < 0.12*p.Height || bb.Y0 > 0.88*p.Height {
				continue
			}
			return nil
		}
	}
	return nil
}

// isPageFurniture reports short text in the top or bottom margin
// (page numbers, document references).
func isPageFurniture(par *model.Paragraph, p *model.Page) bool {
	inMargin := par.BBox.Y1 < 0.1*p.Height || par.BBox.Y0 > 0.9*p.Height
	return inMargin && len(par.Lines) <= 2 && len([]rune(par.Text())) <= 80
}

func sameColumns(t1 *model.Table, w1 float64, t2 *model.Table, w2 float64) bool {
	if t1.NumCols() != t2.NumCols() || t1.NumCols() == 0 || w1 <= 0 || w2 <= 0 {
		return false
	}
	tol := 0.015 // 1.5 % of the page width
	for i := range t1.ColX {
		if math.Abs(t1.ColX[i]/w1-t2.ColX[i]/w2) > tol {
			return false
		}
	}
	return true
}

// repeatedHeader returns 1 when the continuation's first row repeats the
// first row of the table's first part, else 0.
func repeatedHeader(head, cont *model.Table) int {
	if head.NumRows() < 1 || cont.NumRows() < 2 {
		return 0
	}
	h, c := head.Rows(), cont.Rows()
	nonEmpty := 0
	for k := range h[0] {
		if h[0][k] != c[0][k] {
			return 0
		}
		if h[0][k] != "" {
			nonEmpty++
		}
	}
	if nonEmpty == 0 {
		return 0
	}
	return 1
}
