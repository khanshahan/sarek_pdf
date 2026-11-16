package sarekpdf

import (
	"context"
	"strings"
	"testing"

	"github.com/khanshahan/sarek_pdf/model"
)

func TestExtractSample(t *testing.T) {
	for _, compress := range []bool{false, true} {
		doc, err := OpenBytes(samplePDF(compress))
		if err != nil {
			t.Fatal(err)
		}
		if doc.NumPages() != 2 {
			t.Fatalf("pages = %d", doc.NumPages())
		}
		if got := doc.Metadata().Title; got != "Sample Report" {
			t.Errorf("title = %q", got)
		}
		res, err := doc.Extract(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		p1 := res.Pages[0]
		text := p1.Text()
		for _, want := range []string{
			"Annual Report 2025",
			"The quick brown fox jumps over the lazy dog.",
			"hyphenation repair",
			"1,234,567 EUR",
			"Zß€",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("page 1 text missing %q\n---\n%s", want, text)
			}
		}
		var tables, drawings, headings int
		for _, b := range p1.Blocks {
			switch v := b.(type) {
			case *model.Table:
				tables++
				g := v.Grid()
				if v.NumRows() != 3 || v.NumCols() != 3 {
					t.Errorf("table %dx%d, want 3x3: %v", v.NumRows(), v.NumCols(), g)
				} else if g[0][0] != "Region" || g[2][1] != "200" || g[1][2] != "5%" {
					t.Errorf("table grid = %v", g)
				}
			case *model.Drawing:
				drawings++
				if len(v.Labels) != 1 || v.Labels[0].Text() != "Figure" {
					t.Errorf("drawing labels = %+v", v.Labels)
				}
			case *model.Paragraph:
				if v.Role == model.RoleHeading {
					headings++
					if v.Text() != "Annual Report 2025" || v.Level != 1 {
						t.Errorf("heading = %q level %d", v.Text(), v.Level)
					}
				}
			}
		}
		if tables != 1 || drawings != 1 || headings != 1 {
			t.Errorf("tables=%d drawings=%d headings=%d", tables, drawings, headings)
		}
		// Two-column page: left column must be read before right column.
		t2 := res.Pages[1].Text()
		li, ri := strings.Index(t2, "Left column"), strings.Index(t2, "Right column")
		if li < 0 || ri < 0 || li > ri || strings.Count(t2[:ri], "Left column") != 12 {
			t.Errorf("column order wrong:\n%s", t2)
		}
	}
}

func TestSearch(t *testing.T) {
	doc, err := OpenBytes(samplePDF(true))
	if err != nil {
		t.Fatal(err)
	}
	ms, err := doc.Search(context.Background(), "lazy DOG", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Page != 1 || len(ms[0].Rects) != 1 {
		t.Fatalf("matches = %+v", ms)
	}
	r := ms[0].Rects[0]
	if r.X0 < 72 || r.X1 > 540 || r.Y0 < 90 || r.Y1 > 110 {
		t.Errorf("rect = %+v", r)
	}
	// Match across a line break.
	ms, _ = doc.Search(context.Background(), "is long enough", SearchOptions{})
	if len(ms) != 1 || len(ms[0].Rects) != 2 {
		t.Errorf("multi-line match = %+v", ms)
	}
	ms, _ = doc.Search(context.Background(), `\d+\.\d+ percent`, SearchOptions{Regexp: true})
	if len(ms) != 1 || ms[0].Text != "12.5 percent" {
		t.Errorf("regexp match = %+v", ms)
	}
}
