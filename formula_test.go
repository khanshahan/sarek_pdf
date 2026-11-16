package sarekpdf

import (
	"context"
	"strings"
	"testing"

	"github.com/khanshahan/sarek_pdf/internal/testpdf"
	"github.com/khanshahan/sarek_pdf/model"
)

// formulaPDF typesets, glyph by glyph as TeX would:
//
//	page 1: x = (−b ± √(b² − 4ac)) / 2a      (1)
//	page 2: S = Σ_{i=1}^{n} i²               (2)   with stacked limits
//	page 3: inline math and a chemical formula in running text
func formulaPDF() []byte {
	T := func(font string, size, x, y float64, s string) string { return testpdf.Text(font, size, x, y, s) }
	G := testpdf.Glyph
	b := &testpdf.Builder{Compress: true}

	p1 := testpdf.Paragraph("F3", 11, 72, 700, 14, "The roots of a quadratic equation are given by") +
		T("F5", 12, 100, 500, "x") + T("F3", 12, 110, 500, "=") +
		testpdf.Rule(128, 232, 503.75, 0.5) +
		G("F6", 12, 130, 509, "00") + T("F5", 12, 139, 509, "b") + G("F6", 12, 147, 509, "06") +
		// TeX positions the CMSY radical by its top edge, level with the overbar.
		G("F6", 12, 158, 518.6, "70") + testpdf.Rule(165, 231, 518.6, 0.4) +
		T("F5", 12, 167, 509, "b") + T("F3", 8, 173, 513, "2") +
		G("F6", 12, 182, 509, "00") + T("F3", 12, 191, 509, "4") + T("F5", 12, 197, 509, "a") + T("F5", 12, 203, 509, "c") +
		T("F3", 12, 170, 491, "2") + T("F5", 12, 176, 491, "a") +
		T("F3", 12, 480, 500, "(1)") +
		testpdf.Paragraph("F3", 11, 72, 440, 14, "where a, b and c are the coefficients.")
	b.AddPage(p1)

	p2 := T("F5", 12, 100, 400, "S") + T("F3", 12, 110, 400, "=") +
		// TeX places the CMEX operator by its top edge: ink hangs below the origin.
		G("F7", 18, 122, 413, "58") +
		T("F5", 8, 122, 383, "i") + T("F3", 8, 125, 383, "=1") + T("F5", 8, 125, 418, "n") +
		T("F5", 12, 140, 400, "i") + T("F3", 8, 145, 405, "2") +
		T("F3", 12, 480, 400, "(2)")
	b.AddPage(p2)

	p3 := T("F3", 11, 72, 700, "The area of a circle is") + G("F5", 11, 180, 700, "19") + T("F5", 11, 186, 700, "r") +
		T("F3", 7, 191, 704, "2") + T("F3", 11, 197, 700, "and water is H") + T("F3", 7, 261.5, 697, "2") + T("F3", 11, 265.5, 700, "O.")
	b.AddPage(p3)

	// Inline fraction and inline radical inside a paragraph (TeX style:
	// script-size numerator/denominator, CMSY radical hanging from its top).
	p4 := T("F3", 11, 72, 714, "Before the formula there is a full line of ordinary prose text here.") +
		T("F3", 11, 72, 700, "The scores are scaled by") +
		T("F3", 7, 191, 703.5, "1") + testpdf.Rule(189, 198, 701.5, 0.4) + T("F5", 7, 191, 694, "d") +
		T("F3", 11, 202, 700, "and then divided by") +
		G("F6", 11, 296, 709, "70") + testpdf.Rule(303, 310, 709, 0.4) + T("F5", 11, 304, 700, "n") +
		T("F3", 11, 313, 700, "for stability of the training.") +
		T("F3", 11, 72, 686, "After the formula the paragraph continues with more ordinary words.")
	b.AddPage(p4)
	return b.Bytes()
}

func formulasOf(p *model.Page) []*model.Formula {
	var out []*model.Formula
	for _, b := range p.Blocks {
		if f, ok := b.(*model.Formula); ok {
			out = append(out, f)
		}
	}
	return out
}

func squash(s string) string { return strings.Join(strings.Fields(s), "") }

func TestFormulaRecognition(t *testing.T) {
	doc, err := OpenBytes(formulaPDF())
	if err != nil {
		t.Fatal(err)
	}
	res, err := doc.Extract(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f1 := formulasOf(res.Pages[0])
	if len(f1) != 1 {
		t.Fatalf("page 1 formulas = %d\n%s", len(f1), res.Pages[0].Text())
	}
	if got, want := squash(f1[0].LaTeX()), squash(`x = \frac{-b \pm \sqrt{b^{2} - 4ac}}{2a}`); got != want {
		t.Errorf("LaTeX = %s\nwant    %s", f1[0].LaTeX(), want)
	}
	if f1[0].Number != "(1)" {
		t.Errorf("number = %q", f1[0].Number)
	}
	if !strings.Contains(f1[0].MathML(), "<mfrac>") || !strings.Contains(f1[0].MathML(), "<msqrt>") {
		t.Errorf("MathML = %s", f1[0].MathML())
	}
	// Surrounding prose stays prose.
	text := res.Pages[0].Text()
	if !strings.Contains(text, "The roots of a quadratic equation are given by") || !strings.Contains(text, "where a, b and c are the coefficients.") {
		t.Errorf("prose lost:\n%s", text)
	}

	f2 := formulasOf(res.Pages[1])
	if len(f2) != 1 {
		t.Fatalf("page 2 formulas = %d\n%s", len(f2), res.Pages[1].Text())
	}
	if got, want := squash(f2[0].LaTeX()), squash(`S = \sum_{i=1}^{n} i^{2}`); got != want {
		t.Errorf("LaTeX = %s\nwant    %s", f2[0].LaTeX(), want)
	}

	// Inline math is not a display formula; scripts are detected.
	if n := len(formulasOf(res.Pages[2])); n != 0 {
		t.Errorf("page 3: %d display formulas, want 0", n)
	}
	var runs []model.Run
	for _, p := range res.Pages[2].Paragraphs(false) {
		runs = append(runs, p.Runs...)
	}
	var sawMath, sawSup, sawSub bool
	for i, r := range runs {
		if r.Math && strings.Contains(r.Text, "π") {
			sawMath = true
		}
		if r.Script == model.ScriptSuper && r.Text == "2" && i > 0 && strings.HasSuffix(strings.TrimSpace(runs[i-1].Text), "r") {
			sawSup = true
		}
		if r.Script == model.ScriptSub && r.Text == "2" && i > 0 && strings.HasSuffix(runs[i-1].Text, "H") {
			sawSub = true
		}
	}
	if !sawMath || !sawSup || !sawSub {
		t.Errorf("math=%v sup=%v sub=%v runs=%+v", sawMath, sawSup, sawSub, runs)
	}
}

func TestInlineFormulas(t *testing.T) {
	doc, _ := OpenBytes(formulaPDF())
	res, err := doc.Extract(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	pg := res.Pages[0]
	if n := len(formulasOf(pg)); n != 0 {
		t.Errorf("inline math must not become display formulas (%d)", n)
	}
	paras := pg.Paragraphs(false)
	if len(paras) != 1 {
		t.Fatalf("paragraph split by inline math: %d paragraphs\n%s", len(paras), pg.Text())
	}
	var latex []string
	for _, r := range paras[0].Runs {
		if r.Formula != nil {
			latex = append(latex, r.Formula.LaTeX())
		}
	}
	if len(latex) != 2 || squash(latex[0]) != `\frac{1}{d}` || squash(latex[1]) != `\sqrt{n}` {
		t.Errorf("inline formulas = %q", latex)
	}
	text := paras[0].Text()
	if !strings.Contains(text, "scaled by 1/d and then divided by √n for stability") {
		t.Errorf("paragraph text = %q", text)
	}
}

// Ordinary documents must not produce formulas.
func TestNoFalseFormulas(t *testing.T) {
	for _, data := range [][]byte{samplePDF(true), splitTablePDF(), columnsPDFOpt(3, true, []float64{150, 150, 150}, true)} {
		doc, _ := OpenBytes(data)
		res, _ := doc.Extract(context.Background())
		for _, p := range res.Pages {
			if fs := formulasOf(p); len(fs) > 0 {
				t.Errorf("page %d: false formula %q", p.Number, fs[0].LaTeX())
			}
		}
	}
}
