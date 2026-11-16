package sarekpdf

import (
	"bytes"
	"context"
	"image"
	"math"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/khanshahan/sarek_pdf/export/docx"
	"github.com/khanshahan/sarek_pdf/export/xmlexport"
	"github.com/khanshahan/sarek_pdf/internal/testpdf"
	"github.com/khanshahan/sarek_pdf/model"
	"github.com/khanshahan/sarek_pdf/ocr"
)

// fakeOCR returns fixed lines in pixel coordinates of a 1000×1300 scan.
type fakeOCR struct{ calls atomic.Int32 }

func (f *fakeOCR) Name() string { return "fake" }

func (f *fakeOCR) Recognize(_ context.Context, img image.Image) ([]ocr.Line, error) {
	f.calls.Add(1)
	if img.Bounds().Dx() != 1000 || img.Bounds().Dy() != 1300 {
		return nil, nil
	}
	q := func(x0, y0, x1, y1 int) [4]image.Point {
		return [4]image.Point{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
	}
	return []ocr.Line{
		{Text: "Scanned Contract", Quad: q(100, 100, 600, 160), Confidence: 0.98},
		{Text: "The tenant shall pay the rent", Quad: q(100, 250, 900, 280), Confidence: 0.95},
		{Text: "on the first day of each month.", Quad: q(100, 290, 880, 320), Confidence: 0.93},
		{Text: "zx#q illegible", Quad: q(100, 400, 500, 430), Confidence: 0.2}, // dropped
	}, nil
}

func scannedPDF() []byte {
	gray := bytes.Repeat([]byte{255}, 1000*1300)
	b := &testpdf.Builder{Compress: true}
	b.AddImagePage("q 612 0 0 792 0 0 cm /Im1 Do Q\n", testpdf.Image{W: 1000, H: 1300, Gray: gray})
	b.AddPage(testpdf.Text("F1", 12, 72, 700, "A normal text page"))
	return b.Bytes()
}

func TestOCRFallback(t *testing.T) {
	eng := &fakeOCR{}
	doc, err := OpenBytes(scannedPDF(), WithOCR(eng, ocr.Auto))
	if err != nil {
		t.Fatal(err)
	}
	res, err := doc.Extract(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if eng.calls.Load() != 1 {
		t.Errorf("engine called %d times, want 1 (text page must not be OCR'd)", eng.calls.Load())
	}
	p1, p2 := res.Pages[0], res.Pages[1]
	if p1.OCR == nil || p1.OCR.Reason != "no-text-layer" || p1.OCR.Lines != 3 || p1.OCR.Engine != "fake" {
		t.Fatalf("page 1 OCR info = %+v", p1.OCR)
	}
	if p2.OCR != nil {
		t.Errorf("page 2 must not be OCR'd")
	}
	if !strings.Contains(p1.OCR.Errors[0], "1 line(s) below confidence") {
		t.Errorf("low-confidence line not reported: %v", p1.OCR.Errors)
	}
	text := p1.Text()
	if !strings.Contains(text, "Scanned Contract") || !strings.Contains(text, "The tenant shall pay the rent on the first day of each month.") {
		t.Errorf("OCR text = %q", text)
	}
	if strings.Contains(text, "illegible") {
		t.Error("low-confidence line must be dropped")
	}
	var paras int
	for _, b := range p1.Blocks {
		switch v := b.(type) {
		case *model.Paragraph:
			paras++
			if v.Source() != model.SourceOCR || v.Confidence() < 0.9 {
				t.Errorf("paragraph %q source=%v conf=%v", v.Text(), v.Source(), v.Confidence())
			}
			if v.Text() == "Scanned Contract" {
				// Pixel box (100,100)-(600,160) of 1000×1300 on 612×792 pt.
				want := model.Rect{X0: 61.2, Y0: 60.92, X1: 367.2, Y1: 97.48}
				if math.Abs(v.BBox.X0-want.X0) > 0.1 || math.Abs(v.BBox.Y0-want.Y0) > 0.1 || math.Abs(v.BBox.X1-want.X1) > 0.1 || math.Abs(v.BBox.Y1-want.Y1) > 0.1 {
					t.Errorf("bbox = %+v, want %+v", v.BBox, want)
				}
			}
		case *model.Image:
			if !v.OCRSource {
				t.Error("scan image must be flagged OCRSource")
			}
		}
	}
	if paras != 2 {
		t.Errorf("paragraphs = %d, want 2", paras)
	}
	// Plain text carries a notice.
	if !strings.HasPrefix(res.TextWithNotices(), "[page 1: text recognised by OCR (fake), mean confidence 95.3%") {
		t.Errorf("notice = %q", strings.SplitN(res.TextWithNotices(), "\n", 2)[0])
	}
	// XML marks source.
	var xb bytes.Buffer
	xmlexport.Write(&xb, res, xmlexport.DefaultOptions())
	for _, want := range []string{`ocr="true"`, `<ocr engine="fake" reason="no-text-layer" lines="3"`, `source="ocr" confidence="0.950"`, `ocr-source="true"`} {
		if !strings.Contains(xb.String(), want) {
			t.Errorf("xml missing %s", want)
		}
	}
	// DOCX marks source with a style and a comment, and omits the scan image.
	var db bytes.Buffer
	docx.Write(&db, res, docx.Options{})
	z := db.String()
	if !strings.Contains(z, "word/comments.xml") {
		t.Error("docx: no comments part")
	}
	// Search reports OCR provenance.
	ms, _ := doc.Search(context.Background(), "tenant", SearchOptions{})
	if len(ms) != 1 || ms[0].Source != model.SourceOCR {
		t.Errorf("search = %+v", ms)
	}
	ms, _ = doc.Search(context.Background(), "normal text", SearchOptions{})
	if len(ms) != 1 || ms[0].Source != model.SourceTextLayer {
		t.Errorf("search text layer = %+v", ms)
	}
}

func TestOCRNeverWithoutEngineOrMode(t *testing.T) {
	doc, _ := OpenBytes(scannedPDF())
	res, _ := doc.Extract(context.Background())
	if res.Pages[0].OCR != nil || res.Pages[0].Text() != "" {
		t.Error("no OCR must run without an engine")
	}
	if !res.Pages[0].NoTextLayer || res.Pages[1].NoTextLayer {
		t.Error("scanned page must be flagged NoTextLayer")
	}
	if !strings.HasPrefix(res.TextWithNotices(), "[page 1: no usable text layer") {
		t.Errorf("notice = %q", res.TextWithNotices())
	}
	eng := &fakeOCR{}
	doc, _ = OpenBytes(scannedPDF(), WithOCR(eng, ocr.Never))
	doc.Extract(context.Background())
	if eng.calls.Load() != 0 {
		t.Error("ocr.Never must not call the engine")
	}
}

// Regression: a normal text page must never be flagged NoTextLayer (the
// flag was once computed after the glyph buffer had been recycled).
func TestTextPageNotFlagged(t *testing.T) {
	doc, _ := OpenBytes(scannedPDF())
	res, _ := doc.Extract(context.Background())
	// A page with a large image AND a full text layer.
	b := &testpdf.Builder{Compress: true}
	var text string
	for l := 0; l < 10; l++ {
		text += testpdf.Text("F1", 11, 72, 700-float64(l)*14, "This page has a real text layer over a background picture.")
	}
	b.AddImagePage("q 612 0 0 792 0 0 cm /Im1 Do Q\n"+text, testpdf.Image{W: 10, H: 10, Gray: bytes.Repeat([]byte{200}, 100)})
	mixed := b.Bytes()
	for i := 0; i < 3; i++ { // reuse pooled buffers
		doc2, _ := OpenBytes(mixed, WithPageCache(false))
		r2, _ := doc2.Extract(context.Background())
		for _, p := range r2.Pages {
			if p.NoTextLayer {
				t.Fatalf("text page %d flagged NoTextLayer", p.Number)
			}
		}
	}
	if !res.Pages[0].NoTextLayer {
		t.Fatal("scanned page must be flagged")
	}
}

// fakeFormulas recognises one display formula with an equation number
// and one low-confidence formula, in the 1000×1300 fake scan.
type fakeFormulas struct{}

func (fakeFormulas) Name() string { return "fake-math" }

func (fakeFormulas) Formulas(context.Context, image.Image) ([]ocr.Formula, error) {
	q := func(x0, y0, x1, y1 int) [4]image.Point {
		return [4]image.Point{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
	}
	num := q(900, 600, 960, 640)
	return []ocr.Formula{
		{Quad: q(300, 590, 700, 660), LaTeX: `E = m c ^ { 2 }`, Confidence: 0.95, Display: true, Number: &num},
		{Quad: q(300, 800, 700, 860), LaTeX: `\frac { a } { b }`, Confidence: 0.3, Display: true},
		{Quad: q(100, 250, 200, 280), LaTeX: `x`, Confidence: 0.99, Display: false},
	}, nil
}

// fakeOCRWithMath returns text lines, including garbage OCR of the
// formula regions, as a text recogniser would.
type fakeOCRWithMath struct{ fakeOCR }

func (f *fakeOCRWithMath) Recognize(ctx context.Context, img image.Image) ([]ocr.Line, error) {
	lines, _ := f.fakeOCR.Recognize(ctx, img)
	q := func(x0, y0, x1, y1 int) [4]image.Point {
		return [4]image.Point{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
	}
	return append(lines,
		ocr.Line{Text: "E=mc2", Quad: q(320, 600, 680, 650), Confidence: 0.8},
		ocr.Line{Text: "(1)", Quad: q(905, 605, 955, 635), Confidence: 0.9},
		ocr.Line{Text: "a b", Quad: q(320, 810, 680, 850), Confidence: 0.7},
	), nil
}

func TestFormulaOCR(t *testing.T) {
	doc, err := OpenBytes(scannedPDF(), WithOCR(&fakeOCRWithMath{}, ocr.Auto), WithFormulaOCR(fakeFormulas{}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := doc.Extract(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pg := res.Pages[0]
	fs := formulasOf(pg)
	if len(fs) != 1 {
		t.Fatalf("formulas = %d", len(fs))
	}
	f := fs[0]
	if f.Source != model.SourceOCR || f.Confidence != 0.95 || f.Number != "(1)" {
		t.Errorf("formula = %+v", f)
	}
	if f.LaTeX() != `E=mc^{2}` || !strings.Contains(f.MathML(), "<msup>") {
		t.Errorf("latex = %q mathml = %s", f.LaTeX(), f.MathML())
	}
	text := pg.Text()
	if strings.Contains(text, "E=mc2") || strings.Contains(text, "\n(1)") {
		t.Errorf("formula region text not replaced:\n%s", text)
	}
	if !strings.Contains(text, "a b") {
		t.Errorf("low-confidence formula region must keep its OCR text:\n%s", text)
	}
	if pg.OCR.Formulas != 1 || !strings.Contains(strings.Join(pg.OCR.Errors, ";"), "1 formula(s) below confidence") {
		t.Errorf("ocr info = %+v", pg.OCR)
	}
	if !strings.Contains(pg.OCR.Engine, "fake-math") {
		t.Errorf("engine = %q", pg.OCR.Engine)
	}
	var xb bytes.Buffer
	xmlexport.Write(&xb, res, xmlexport.DefaultOptions())
	if !strings.Contains(xb.String(), `source="ocr" confidence="0.950"`) {
		t.Error("xml formula not marked as OCR")
	}
}
