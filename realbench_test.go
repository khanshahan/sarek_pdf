package sarekpdf

import (
	"bufio"
	"context"
	"os"
	"testing"
)

// BenchmarkRealCorpus extracts every file listed in $SAREK_CORPUS
// (one path per line) on one worker. Development benchmark.
func BenchmarkRealCorpus(b *testing.B) {
	list := os.Getenv("SAREK_CORPUS")
	if list == "" {
		b.Skip("SAREK_CORPUS not set")
	}
	f, _ := os.Open(list)
	var datas [][]byte
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if d, err := os.ReadFile(sc.Text()); err == nil {
			datas = append(datas, d)
		}
	}
	f.Close()
	pages := 0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, d := range datas {
			doc, err := OpenBytes(d, WithWorkers(1), WithImages(false), WithPageCache(false))
			if err != nil {
				continue
			}
			res, _ := doc.Extract(context.Background())
			if res != nil {
				pages += len(res.Pages)
			}
		}
	}
	b.ReportMetric(float64(pages)/b.Elapsed().Seconds(), "pages/s")
}
