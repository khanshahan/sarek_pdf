package sarekpdf

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/khanshahan/sarek_pdf/internal/testpdf"
	"github.com/khanshahan/sarek_pdf/model"
)

// footerPDF has n pages with a running footer and body text.
func footerPDF(n int) []byte {
	b := &testpdf.Builder{Compress: true}
	for i := 0; i < n; i++ {
		c := testpdf.Text("F1", 8, 72, 30, fmt.Sprintf("Annual report - page %d", i+1))
		for l := 0; l < 15; l++ {
			c += testpdf.Text("F1", 10, 72, 700-float64(l)*14, fmt.Sprintf("Page %d line %d with some body text.", i+1, l))
		}
		b.AddPage(c)
	}
	return b.Bytes()
}

func pageSignature(p *model.Page) string {
	s := p.Text() + "|"
	for _, b := range p.Blocks {
		switch v := b.(type) {
		case *model.Paragraph:
			s += v.Role.String() + ";"
		case *model.Table:
			s += fmt.Sprintf("table(%v,%v,%d,%d);", v.Prev != nil, v.Next != nil, v.HeaderRows, v.Merged().NumRows())
		}
	}
	return s
}

func TestStreamMatchesExtract(t *testing.T) {
	for name, data := range map[string][]byte{
		"sample": samplePDF(true), "split-table": splitTablePDF(), "footers-40": footerPDF(40),
		"formulas": formulaPDF(), "columns": columnsPDF(3, true, []float64{150, 150, 150}),
	} {
		for _, workers := range []int{1, 4} {
			doc, err := OpenBytes(data, WithWorkers(workers))
			if err != nil {
				t.Fatal(err)
			}
			want, err := doc.Extract(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var got []*model.Page
			if err := doc.Stream(context.Background(), nil, func(p *model.Page) error {
				got = append(got, p)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want.Pages) {
				t.Fatalf("%s: %d pages streamed, want %d", name, len(got), len(want.Pages))
			}
			for i := range got {
				if got[i].Number != i+1 {
					t.Fatalf("%s: out of order: page %d at %d", name, got[i].Number, i)
				}
				if a, b := pageSignature(got[i]), pageSignature(want.Pages[i]); a != b {
					t.Errorf("%s workers=%d page %d differs:\nstream:  %.200s\nextract: %.200s", name, workers, i+1, a, b)
				}
			}
		}
	}
}

func TestStreamStopAndCancel(t *testing.T) {
	doc, _ := OpenBytes(footerPDF(60), WithWorkers(4))
	stop := errors.New("stop")
	n := 0
	err := doc.Stream(context.Background(), nil, func(p *model.Page) error {
		n++
		if n == 5 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || n != 5 {
		t.Errorf("stop: err=%v n=%d", err, n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := doc.Stream(ctx, nil, func(*model.Page) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel: err=%v", err)
	}
}

// Text-only mode keeps every letter and digit of full analysis (and the
// same words where no formulas are involved), without tables, drawings,
// images or formulas.
func TestTextOnlySameText(t *testing.T) {
	for name, data := range map[string][]byte{"sample": samplePDF(true), "formulas": formulaPDF(), "split-table": splitTablePDF()} {
		full, _ := OpenBytes(data)
		fast, _ := OpenBytes(data, WithTextOnly(true))
		a, _ := full.Extract(context.Background())
		b, _ := fast.Extract(context.Background())
		chars := func(d *model.Document) map[rune]int {
			m := map[rune]int{}
			for _, p := range d.Pages {
				for _, r := range p.Text() {
					if unicode.IsLetter(r) || unicode.IsDigit(r) {
						m[r]++
					}
				}
			}
			return m
		}
		ca, cb := chars(a), chars(b)
		for r, n := range ca {
			if cb[r] < n {
				t.Errorf("%s: text-only lost character %q (%d < %d)", name, r, cb[r], n)
			}
		}
		if name != "formulas" {
			words := func(d *model.Document) map[string]int {
				m := map[string]int{}
				for _, w := range strings.FieldsFunc(d.Text(), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
					m[w]++
				}
				return m
			}
			wa, wb := words(a), words(b)
			for w, n := range wa {
				if wb[w] < n {
					t.Errorf("%s: text-only lost word %q (%d < %d)", name, w, wb[w], n)
				}
			}
		}
		for _, p := range b.Pages {
			for _, blk := range p.Blocks {
				switch blk.(type) {
				case *model.Table, *model.Drawing, *model.Formula, *model.Image:
					t.Errorf("%s: text-only produced %T", name, blk)
				}
			}
		}
	}
}
