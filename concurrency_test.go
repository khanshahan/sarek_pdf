package sarekpdf

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/khanshahan/sarek_pdf/internal/testpdf"
	"github.com/khanshahan/sarek_pdf/model"
)

// Concurrent Extract calls on one document with different page sets must
// not race (document-level passes annotate per-call copies).
func TestConcurrentExtract(t *testing.T) {
	b := &testpdf.Builder{Compress: true}
	for i := 0; i < 12; i++ {
		c := testpdf.Text("F1", 8, 72, 30, fmt.Sprintf("Confidential report - page %d", i+1))
		for l := 0; l < 20; l++ {
			c += testpdf.Text("F1", 10, 72, 700-float64(l)*14, "Body text line for concurrency testing of the extractor.")
		}
		b.AddPage(c)
	}
	doc, err := OpenBytes(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	sets := [][]int{nil, {1, 2, 3, 4}, {2, 3}, {5, 6, 7, 8, 9}, nil}
	for _, s := range sets {
		for k := 0; k < 3; k++ {
			wg.Add(1)
			go func(pages []int) {
				defer wg.Done()
				res, err := doc.Extract(context.Background(), pages...)
				if err != nil {
					t.Error(err)
					return
				}
				_ = res.Text()
				footers := 0
				for _, p := range res.Pages {
					for _, b := range p.Paragraphs(true) {
						if b.Role == model.RolePageFooter {
							footers++
						}
					}
				}
				if len(pages) == 0 && footers != 12 {
					t.Errorf("footers = %d, want 12", footers)
				}
			}(s)
		}
	}
	wg.Wait()
}
