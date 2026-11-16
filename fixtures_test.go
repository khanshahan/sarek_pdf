package sarekpdf

import (
	"github.com/khanshahan/sarek_pdf/internal/testpdf"
)

// samplePDF builds a two-page document exercising headings, paragraphs,
// hyphenation, a ruled table, a drawing, custom encodings and columns.
func samplePDF(compress bool) []byte {
	b := &testpdf.Builder{Compress: compress, Title: "Sample Report"}
	p1 := testpdf.Text("F2", 22, 72, 720, "Annual Report 2025") +
		testpdf.Paragraph("F1", 11, 72, 690, 14,
			"The quick brown fox jumps over the lazy dog. This paragraph is long",
			"enough to wrap across several lines so that the layout engine has to",
			"join them into a single paragraph with correct spacing and hyphen-",
			"ation repair at the end of the third line.") +
		testpdf.Paragraph("F1", 11, 72, 610, 14,
			"A second paragraph follows after a blank line. It mentions revenue of",
			"1,234,567 EUR and growth of 12.5 percent.") +
		testpdf.Grid([]float64{72, 222, 372, 522}, []float64{560, 540, 520, 500}, [][]string{
			{"Region", "Revenue", "Growth"},
			{"North", "100", "5%"},
			{"South", "200", "7%"},
		}) +
		testpdf.Circle(300, 380, 60) +
		testpdf.Text("F1", 9, 285, 377, "Figure") +
		"BT /F4 12 Tf 72 250 Td <010203> Tj ET\n"
	b.AddPage(p1)
	var p2 string
	for i := 0; i < 12; i++ {
		y := 700 - float64(i)*14
		p2 += testpdf.Text("F3", 11, 72, y, "Left column text line that continues the story")
		p2 += testpdf.Text("F3", 11, 320, y, "Right column text line with other content here")
	}
	b.AddPage(p2)
	return b.Bytes()
}
