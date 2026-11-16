package sarekpdf

import (
	"bytes"
	"context"
	"image"
	"strings"
	"testing"

	"github.com/khanshahan/sarek_pdf/internal/testpdf"
	"github.com/khanshahan/sarek_pdf/model"
	"github.com/khanshahan/sarek_pdf/ocr"
)

// replayOCR returns detector/recogniser output recorded from the real
// PaddleOCR engine on a 1700×2200 (200 dpi) three-column scan.
type replayOCR struct{}

func (replayOCR) Name() string { return "replay" }

func (replayOCR) Recognize(context.Context, image.Image) ([]ocr.Line, error) {
	rec := []struct {
		y0, y1, x0, x1 int
		text           string
	}{
		{190, 264, 146, 967, "Quarterly Operations Review"},
		{400, 436, 146, 583, "Revenue in the third quarter"}, {397, 439, 635, 1076, "Operating costs remained"}, {398, 437, 1125, 1564, "The board approved the"},
		{445, 480, 144, 585, "rose by twelve percent"}, {443, 479, 637, 1074, "stable. Investments in"}, {442, 481, 1124, 1565, "proposed budget for the next"},
		{485, 524, 145, 586, "compared with the same period"}, {485, 520, 637, 1074, "automation reduced manual"}, {484, 523, 1125, 1565, "financial year. Hiring will"},
		{531, 566, 146, 585, "last year. The increase was"}, {527, 570, 635, 1075, "processing time, while energy"}, {528, 567, 1125, 1565, "focus on engineering and"},
		{570, 613, 145, 585, "driven mainly by stronger"}, {570, 613, 635, 1075, "prices were higher than"}, {571, 610, 1125, 1565, "customer support. A detailed"},
		{614, 653, 146, 584, "demand in the northern region"}, {613, 655, 635, 1075, "expected. Management expects"}, {614, 653, 1125, 1562, "plan will be presented at the"},
		{659, 695, 147, 582, "and by new service contracts"}, {657, 696, 634, 1074, "these prices to normalise"}, {657, 696, 1125, 1480, "annual meeting in March."},
		{700, 741, 146, 346, "signed in July."}, {700, 739, 636, 993, "during the winter months."},
	}
	out := make([]ocr.Line, len(rec))
	for i, r := range rec {
		out[i] = ocr.Line{Text: r.text, Confidence: 0.99,
			Quad: [4]image.Point{{r.x0, r.y0}, {r.x1, r.y0}, {r.x1, r.y1}, {r.x0, r.y1}}}
	}
	return out, nil
}

func TestOCRThreeColumnLayout(t *testing.T) {
	b := &testpdf.Builder{Compress: true}
	b.AddImagePage("q 612 0 0 792 0 0 cm /Im1 Do Q\n", testpdf.Image{W: 1700, H: 2200, Gray: bytes.Repeat([]byte{255}, 1700*2200)})
	doc, err := OpenBytes(b.Bytes(), WithOCR(replayOCR{}, ocr.Auto))
	if err != nil {
		t.Fatal(err)
	}
	res, err := doc.Extract(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var paras []string
	for _, blk := range res.Pages[0].Blocks {
		switch v := blk.(type) {
		case *model.Table:
			t.Errorf("prose columns misdetected as table: %v", v.Grid())
		case *model.Paragraph:
			paras = append(paras, v.Text())
		}
	}
	want := []string{
		"Quarterly Operations Review",
		"Revenue in the third quarter rose by twelve percent compared with the same period last year. The increase was driven mainly by stronger demand in the northern region and by new service contracts signed in July.",
		"Operating costs remained stable. Investments in automation reduced manual processing time, while energy prices were higher than expected. Management expects these prices to normalise during the winter months.",
		"The board approved the proposed budget for the next financial year. Hiring will focus on engineering and customer support. A detailed plan will be presented at the annual meeting in March.",
	}
	if strings.Join(paras, "\n") != strings.Join(want, "\n") {
		t.Errorf("paragraphs:\n%s", strings.Join(paras, "\n---\n"))
	}
}
