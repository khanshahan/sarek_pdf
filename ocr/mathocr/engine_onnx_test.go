//go:build onnx

package mathocr

import (
	"context"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"
)

// TestEngineOnImage runs detection+recognition on a page image.
// SAREK_MFR_DIR: model directory; SAREK_FORMULA_IMAGES: PNG files.
func TestEngineOnImage(t *testing.T) {
	dir, imgs := os.Getenv("SAREK_MFR_DIR"), os.Getenv("SAREK_FORMULA_IMAGES")
	if dir == "" || imgs == "" {
		t.Skip("SAREK_MFR_DIR / SAREK_FORMULA_IMAGES not set")
	}
	e, err := New(FromDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for _, p := range strings.Fields(imgs) {
		f, _ := os.Open(p)
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		fs, err := e.Formulas(context.Background(), img)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d formulas in %v", p, len(fs), time.Since(start).Round(time.Millisecond))
		for _, f := range fs {
			t.Logf("  display=%v conf=%.3f number=%v box=%v  %s", f.Display, f.Confidence, f.Number != nil, f.Quad[0], f.LaTeX)
		}
	}
}
