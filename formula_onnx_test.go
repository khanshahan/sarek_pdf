//go:build onnx

package sarekpdf

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/khanshahan/sarek_pdf/internal/testpdf"
	"github.com/khanshahan/sarek_pdf/ocr"
	"github.com/khanshahan/sarek_pdf/ocr/mathocr"
	"github.com/khanshahan/sarek_pdf/ocr/paddle"
)

// TestScannedFormulas runs text OCR and formula OCR on scanned pages.
// SAREK_OCR_MODELS, SAREK_MFR_DIR, and SAREK_FORMULA_SCANS as
// "path:width:height ..." of 8-bit gray rasters.
func TestScannedFormulas(t *testing.T) {
	ocrDir, mfrDir, scans := os.Getenv("SAREK_OCR_MODELS"), os.Getenv("SAREK_MFR_DIR"), os.Getenv("SAREK_FORMULA_SCANS")
	if ocrDir == "" || mfrDir == "" || scans == "" {
		t.Skip("model directories / scans not set")
	}
	textEng, err := paddle.New(paddle.FromDir(ocrDir, "english"))
	if err != nil {
		t.Fatal(err)
	}
	defer textEng.Close()
	mathEng, err := mathocr.New(mathocr.FromDir(mfrDir))
	if err != nil {
		t.Fatal(err)
	}
	defer mathEng.Close()
	b := &testpdf.Builder{Compress: true}
	for _, s := range strings.Fields(scans) {
		parts := strings.Split(s, ":")
		w, _ := strconv.Atoi(parts[1])
		h, _ := strconv.Atoi(parts[2])
		gray, err := os.ReadFile(parts[0])
		if err != nil {
			t.Fatal(err)
		}
		b.AddImagePage("q 612 0 0 792 0 0 cm /Im1 Do Q\n", testpdf.Image{W: w, H: h, Gray: gray})
	}
	doc, err := OpenBytes(b.Bytes(), WithOCR(textEng, ocr.Auto), WithFormulaOCR(mathEng))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := doc.Extract(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d pages in %v", len(res.Pages), time.Since(start).Round(time.Millisecond))
	var all []string
	for _, pg := range res.Pages {
		t.Logf("page %d: %+v", pg.Number, *pg.OCR)
		for _, f := range formulasOf(pg) {
			line := fmt.Sprintf("%s %.2f %s", f.Number, f.Confidence, f.LaTeX())
			t.Log("  ", line)
			all = append(all, line)
		}
	}
	joined := strings.Join(all, "\n")
	for _, want := range []string{`\mathrm{Attention}(Q,K,V)=\mathrm{softmax}(\frac{QK^{T}}{\sqrt{d_{k}}})V`, `\mathrm{FFN}(x)=`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing formula %s", want)
		}
	}
	text := res.TextWithNotices()
	if !strings.Contains(text, "text recognised by OCR") {
		t.Error("OCR notice missing")
	}
}
