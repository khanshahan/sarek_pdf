package sarekpdf

import (
	"context"
	"fmt"
	"testing"

	"github.com/khanshahan/sarek_pdf/internal/testpdf"
)

// bigPDF builds an n-page document with dense text, a table and a figure
// on every page.
func bigPDF(n int) []byte {
	b := &testpdf.Builder{Compress: true}
	for i := 0; i < n; i++ {
		var c string
		for l := 0; l < 40; l++ {
			c += testpdf.Text("F1", 10, 72, 740-float64(l)*12,
				fmt.Sprintf("Line %d of page %d: the quick brown fox jumps over the lazy dog again and again.", l, i))
		}
		c += testpdf.Grid([]float64{72, 222, 372}, []float64{250, 230, 210}, [][]string{{"a", "b"}, {"c", "d"}})
		c += testpdf.Circle(300, 120, 40)
		b.AddPage(c)
	}
	return b.Bytes()
}

func benchExtract(b *testing.B, workers int) {
	data := bigPDF(200)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doc, err := OpenBytes(data, WithWorkers(workers), WithPageCache(false))
		if err != nil {
			b.Fatal(err)
		}
		if _, err := doc.Extract(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(200*b.N)/b.Elapsed().Seconds(), "pages/s")
}

func BenchmarkExtract1Worker(b *testing.B)    { benchExtract(b, 1) }
func BenchmarkExtractAllWorkers(b *testing.B) { benchExtract(b, 0) }
