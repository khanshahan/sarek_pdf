package images

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"testing"
)

// The fixture is a CCITT Group 4 strip written by an independent encoder
// (Apple ImageIO) from rendered text; the expected pixels are ImageIO's
// own decoding of the same file. The TIFF is BlackIsZero, so the codec's
// "white" runs carry pixel value 0 (black): in PDF terms BlackIs1 = true.
func TestCCITTGroup4(t *testing.T) {
	data, err := os.ReadFile("testdata/g4_803x211.bin")
	if err != nil {
		t.Fatal(err)
	}
	gz, err := os.ReadFile("testdata/g4_803x211.gray.gz")
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := gzip.NewReader(bytes.NewReader(gz))
	want, _ := io.ReadAll(zr)
	got, rows, err := decodeCCITT(data, ccittParams{K: -1, Columns: 803, Rows: 211, EndOfBlock: true, BlackIs1: true})
	if err != nil || rows != 211 {
		t.Fatalf("rows=%d err=%v", rows, err)
	}
	diff := 0
	for i := range want {
		if got[i] != want[i] {
			diff++
		}
	}
	if diff != 0 {
		t.Fatalf("%d of %d pixels differ", diff, len(want))
	}
}

func TestCCITT1D(t *testing.T) {
	// Hand-encoded G3 1D row of width 16: 4 white (1011), 8 black
	// (000101), 4 white (1011) → bits 1011 000101 1011 + padding.
	data := []byte{0b10110001, 0b01101100}
	got, rows, err := decodeCCITT(data, ccittParams{K: 0, Columns: 16, Rows: 1})
	if err != nil || rows != 1 {
		t.Fatalf("rows=%d err=%v", rows, err)
	}
	want := []byte{255, 255, 255, 255, 0, 0, 0, 0, 0, 0, 0, 0, 255, 255, 255, 255}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}
