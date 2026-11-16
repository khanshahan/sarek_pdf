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
