package sarekpdf

import (
	"context"
	"testing"
	"time"
)

// FuzzOpen checks that arbitrary input never panics or hangs.
// Run: go test -fuzz=FuzzOpen -fuzztime=60s .
func FuzzOpen(f *testing.F) {
	f.Add(samplePDF(false))
	f.Add(samplePDF(true))
	f.Add([]byte("%PDF-1.7\n1 0 obj<<>>endobj trailer<</Root 1 0 R>>"))
	f.Fuzz(func(t *testing.T, data []byte) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			doc, err := OpenBytes(data, WithWorkers(1))
			if err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			doc.Extract(ctx)
			doc.Search(ctx, "a", SearchOptions{})
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("input of %d bytes took longer than 3s", len(data))
		}
	})
}

// TestMutatedInputs runs a deterministic set of corruptions of the sample.
func TestMutatedInputs(t *testing.T) {
	base := samplePDF(true)
	for i := 0; i < 400; i++ {
		data := append([]byte(nil), base...)
		pos := (i * 7919) % len(data)
		switch i % 4 {
		case 0:
			data[pos] ^= 0xFF
		case 1:
			data = data[:pos]
		case 2:
			data = append(data[:pos], data[min(len(data), pos+50):]...)
		case 3:
			data[pos] = '('
		}
		doc, err := OpenBytes(data, WithWorkers(2))
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		doc.Extract(ctx)
		cancel()
	}
}
