package sarekpdf

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/khanshahan/sarek_pdf/internal/testpdf"
	"github.com/khanshahan/sarek_pdf/model"
)

// columnsPDF lays out n columns of body text, optionally under a
// full-width title, with column lines deliberately not aligned across
// columns (different leading per column) and a paragraph break.
func columnsPDF(n int, title bool, widths []float64) []byte {
	return columnsPDFOpt(n, title, widths, false)
}

// prose words used to fill column lines to (roughly) full width.
var prose = strings.Fields("the committee reviewed the annual figures and agreed that further analysis of regional performance would be required before any final decision could be taken on the proposed budget")

func columnsPDFOpt(n int, title bool, widths []float64, aligned bool) []byte {
	b := &testpdf.Builder{Compress: true}
	var c string
	top := 740.0
	if title {
		c += testpdf.Text("F2", 18, 72, 750, "A Title That Spans Every Column Of This Page")
		top = 710
	}
	x := 54.0
	for col := 0; col < n; col++ {
		leading := 12.0 + float64(col)
		if aligned {
			leading = 12
		}
		y := top
		for l := 0; l < 30; l++ {
			if l == 15 {
				y -= 10 // paragraph break
			}
			line := fmt.Sprintf("C%d L%02d text", col+1, l)
			if aligned { // fill the column width with prose (≤4.6pt per char at 9pt Times keeps lines inside the column)
				for k := 0; ; k++ {
					next := line + " " + prose[(l*7+k)%len(prose)]
					if float64(len(next))*4.6 > widths[col] {
						break
					}
					line = next
				}
			}
			c += testpdf.Text("F3", 9, x, y, line)
			y -= leading
		}
		x += widths[col] + 18
	}
	b.AddPage(c)
	return b.Bytes()
}

func checkColumnOrder(t *testing.T, data []byte, n int, title bool) {
	t.Helper()
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := doc.Text(context.Background())
	pos := 0
	if title {
		if !strings.HasPrefix(text, "A Title That Spans") {
			t.Fatalf("title not first:\n%s", text[:min(200, len(text))])
		}
	}
	for col := 1; col <= n; col++ {
		for l := 0; l < 30; l++ {
			tok := fmt.Sprintf("C%d L%02d text", col, l)
			i := strings.Index(text[pos:], tok)
			if i < 0 {
				t.Fatalf("%d columns: %q out of order or missing\n%s", n, tok, text)
			}
			pos += i + len(tok)
		}
	}
}

func TestColumns(t *testing.T) {
	cases := []struct {
		n      int
		title  bool
		widths []float64
	}{
		{2, false, []float64{240, 240}},
		{2, true, []float64{240, 240}},
		{3, false, []float64{150, 150, 150}},
		{3, true, []float64{150, 150, 150}},
		{3, true, []float64{100, 200, 150}}, // unequal widths
	}
	for _, c := range cases {
		for _, aligned := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dcols_title=%v_%v_aligned=%v", c.n, c.title, c.widths, aligned), func(t *testing.T) {
				data := columnsPDFOpt(c.n, c.title, c.widths, aligned)
				checkColumnOrder(t, data, c.n, c.title)
				doc, _ := OpenBytes(data)
				res, _ := doc.Extract(context.Background())
				for _, b := range res.Pages[0].Blocks {
					if _, ok := b.(*model.Table); ok {
						t.Errorf("prose columns misdetected as a table")
					}
				}
			})
		}
	}
}
