//go:build onnx

package sarekpdf

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khanshahan/sarek_pdf/internal/testpdf"
	"github.com/khanshahan/sarek_pdf/model"
	"github.com/khanshahan/sarek_pdf/ocr"
	"github.com/khanshahan/sarek_pdf/ocr/paddle"
)

// TestPaddleOCRScan runs the real PaddleOCR engine on a rendered 3-column
// scan. Requires SAREK_OCR_MODELS (directory with detection/v5/det.onnx and
// languages/english/{rec.onnx,dict.txt}) and SAREK_SCAN (prefix of
// <prefix>.gray 1700×2200 and <prefix>.truth.txt).
func TestPaddleOCRScan(t *testing.T) {
	models, scan := os.Getenv("SAREK_OCR_MODELS"), os.Getenv("SAREK_SCAN")
	if models == "" || scan == "" {
		t.Skip("SAREK_OCR_MODELS / SAREK_SCAN not set")
	}
	eng, err := paddle.New(paddle.Options{
		DetModel: filepath.Join(models, "detection/v5/det.onnx"),
		RecModel: filepath.Join(models, "languages/english/rec.onnx"),
		Dict:     filepath.Join(models, "languages/english/dict.txt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	gray, err := os.ReadFile(scan + ".gray")
	if err != nil {
		t.Fatal(err)
	}
	truth, _ := os.ReadFile(scan + ".truth.txt")
	b := &testpdf.Builder{Compress: true}
	b.AddImagePage("q 612 0 0 792 0 0 cm /Im1 Do Q\n", testpdf.Image{W: 1700, H: 2200, Gray: gray})
	doc, err := OpenBytes(b.Bytes(), WithOCR(eng, ocr.Auto))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := doc.Extract(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pg := res.Pages[0]
	t.Logf("OCR %v: %+v", time.Since(start).Round(time.Millisecond), *pg.OCR)
	got := pg.Text()
	t.Logf("text:\n%s", got)

	// Word-level accuracy (bag of words) and reading order.
	norm := func(s string) []string {
		return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		})
	}
	want := norm(string(truth))
	have := map[string]int{}
	for _, w := range norm(got) {
		have[w]++
	}
	hit := 0
	for _, w := range want {
		if have[w] > 0 {
			have[w]--
			hit++
		}
	}
	acc := float64(hit) / float64(len(want))
	t.Logf("word recall %.1f%% (%d/%d)", acc*100, hit, len(want))
	if acc < 0.95 {
		t.Errorf("word recall %.3f below 0.95", acc)
	}
	order := []string{"Quarterly Operations Review", "Revenue in the third", "Operating costs", "The board approved"}
	pos := -1
	for _, o := range order {
		i := strings.Index(got, o)
		if i <= pos {
			t.Errorf("reading order: %q at %d (previous %d)", o, i, pos)
		}
		pos = i
	}
	for _, b := range pg.Blocks {
		if p, ok := b.(*model.Paragraph); ok && p.Source() != model.SourceOCR {
			t.Errorf("paragraph not marked OCR: %q", p.Text())
		}
	}
}
