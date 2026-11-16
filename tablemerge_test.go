package sarekpdf

import (
	"context"
	"fmt"
	"testing"

	"github.com/khanshahan/sarek_pdf/internal/testpdf"
	"github.com/khanshahan/sarek_pdf/model"
)

// splitTablePDF: a table starts at the bottom of page 1 and continues at
// the top of page 2 with a repeated header row. Page 3 holds a table with
// a different column layout that must not be linked.
func splitTablePDF() []byte {
	xs := []float64{72, 200, 330, 460}
	header := []string{"Item", "Quantity", "Price"}
	grid := func(top float64, rows [][]string) string {
		ys := make([]float64, len(rows)+1)
		for i := range ys {
			ys[i] = top - float64(i)*20
		}
		return testpdf.Grid(xs, ys, rows)
	}
	rows1 := [][]string{header}
	for i := 1; i <= 5; i++ {
		rows1 = append(rows1, []string{fmt.Sprintf("Part %d", i), fmt.Sprint(i * 10), fmt.Sprintf("%d.00", i*3)})
	}
	rows2 := [][]string{header}
	for i := 6; i <= 9; i++ {
		rows2 = append(rows2, []string{fmt.Sprintf("Part %d", i), fmt.Sprint(i * 10), fmt.Sprintf("%d.00", i*3)})
	}
	b := &testpdf.Builder{Compress: true}
	b.AddPage(testpdf.Paragraph("F1", 11, 72, 700, 14, "Parts list for the annual maintenance order.") +
		grid(200, rows1) + testpdf.Text("F1", 8, 300, 30, "1"))
	b.AddPage(grid(740, rows2) +
		testpdf.Paragraph("F1", 11, 72, 600, 14, "Prices exclude delivery.") + testpdf.Text("F1", 8, 300, 30, "2"))
	b.AddPage(testpdf.Grid([]float64{72, 300, 540}, []float64{740, 720, 700}, [][]string{{"Name", "Value"}, {"A", "B"}}) +
		testpdf.Text("F1", 8, 300, 30, "3"))
	return b.Bytes()
}

func tablesOf(p *model.Page) []*model.Table {
	var out []*model.Table
	for _, b := range p.Blocks {
		if t, ok := b.(*model.Table); ok {
			out = append(out, t)
		}
	}
	return out
}

func TestCrossPageTableMerge(t *testing.T) {
	doc, err := OpenBytes(splitTablePDF())
	if err != nil {
		t.Fatal(err)
	}
	res, err := doc.Extract(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t1, t2, t3 := tablesOf(res.Pages[0]), tablesOf(res.Pages[1]), tablesOf(res.Pages[2])
	if len(t1) != 1 || len(t2) != 1 || len(t3) != 1 {
		t.Fatalf("tables per page = %d %d %d", len(t1), len(t2), len(t3))
	}
	a, b, c := t1[0], t2[0], t3[0]
	if a.Next != b || b.Prev != a {
		t.Fatal("page 1 and page 2 tables not linked")
	}
	if b.Next != nil || c.Prev != nil {
		t.Error("table with a different column layout must not be linked")
	}
	if b.HeaderRows != 1 {
		t.Errorf("HeaderRows = %d, want 1", b.HeaderRows)
	}
	m := a.Merged()
	if m.NumRows() != 10 || m.NumCols() != 3 {
		t.Fatalf("merged %dx%d, want 10x3", m.NumRows(), m.NumCols())
	}
	g := m.Rows()
	if g[0][0] != "Item" || g[5][0] != "Part 5" || g[6][0] != "Part 6" || g[9][2] != "27.00" {
		t.Errorf("merged rows = %v", g)
	}
	if len(m.RowY) != 11 {
		t.Errorf("RowY len = %d", len(m.RowY))
	}
	// Parts are unchanged.
	if a.NumRows() != 6 || b.NumRows() != 5 {
		t.Errorf("parts modified: %d, %d rows", a.NumRows(), b.NumRows())
	}
	// Extracting a single page never links across the gap.
	one, _ := doc.Extract(context.Background(), 2)
	if tablesOf(one.Pages[0])[0].Prev != nil {
		t.Error("single-page extract must not carry links")
	}
}
