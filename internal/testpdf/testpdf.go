// Package testpdf builds small PDF files for tests. Test-only.
package testpdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
)

// Builder assembles a PDF with standard fonts.
type Builder struct {
	pages    []string
	Compress bool
	Title    string
}

// AddPage adds a page with the given content stream (612×792).
func (b *Builder) AddPage(content string) { b.pages = append(b.pages, content) }

// Bytes renders the PDF.
func (b *Builder) Bytes() []byte {
	var objs []string
	add := func(s string) int { objs = append(objs, s); return len(objs) }
	add("") // 1 catalog placeholder
	add("") // 2 pages placeholder
	f1 := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	f2 := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	f3 := add("<< /Type /Font /Subtype /Type1 /BaseFont /Times-Roman /Encoding /WinAnsiEncoding >>")
	// F4: custom-encoded font with a ToUnicode CMap (codes 1..3 -> "Zß€").
	cmap := `/CIDInit /ProcSet findresource begin 12 dict begin begincmap
/CMapName /Custom def 1 begincodespacerange <00> <FF> endcodespacerange
2 beginbfchar <01> <005A> <02> <00DF> endbfchar
1 beginbfrange <03> <03> <20AC> endbfrange
endcmap CMapName currentdict /CMap defineresource pop end end`
	tu := add(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(cmap), cmap))
	f4 := add(fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /Custom /FirstChar 1 /LastChar 3 /Widths [600 600 600] /Encoding << /Differences [1 /g1 /g2 /g3] >> /ToUnicode %d 0 R >>", tu))
	res := fmt.Sprintf("<< /Font << /F1 %d 0 R /F2 %d 0 R /F3 %d 0 R /F4 %d 0 R >> >>", f1, f2, f3, f4)
	var kids []string
	for _, c := range b.pages {
		var cs string
		if b.Compress {
			var z bytes.Buffer
			zw := zlib.NewWriter(&z)
			zw.Write([]byte(c))
			zw.Close()
			cs = fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>\nstream\n%s\nendstream", z.Len(), z.String())
		} else {
			cs = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(c), c)
		}
		cn := add(cs)
		pn := add(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources %s /Contents %d 0 R >>", res, cn))
		kids = append(kids, fmt.Sprintf("%d 0 R", pn))
	}
	objs[0] = "<< /Type /Catalog /Pages 2 0 R >>"
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids))
	info := 0
	if b.Title != "" {
		info = add(fmt.Sprintf("<< /Title (%s) >>", b.Title))
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	x := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	tr := fmt.Sprintf("/Size %d /Root 1 0 R", len(objs)+1)
	if info > 0 {
		tr += fmt.Sprintf(" /Info %d 0 R", info)
	}
	fmt.Fprintf(&out, "trailer\n<< %s /ID [<0123456789ABCDEF0123456789ABCDEF> <0123456789ABCDEF0123456789ABCDEF>] >>\nstartxref\n%d\n%%%%EOF\n", tr, x)
	return out.Bytes()
}

// Text returns a content-stream snippet drawing text at (x, y).
func Text(font string, size, x, y float64, s string) string {
	return fmt.Sprintf("BT /%s %g Tf %g %g Td (%s) Tj ET\n", font, size, x, y, escape(s))
}

func escape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return r.Replace(s)
}

// Paragraph draws wrapped lines with the given leading.
func Paragraph(font string, size, x, y, leading float64, lines ...string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "BT /%s %g Tf %g TL %g %g Td\n", font, size, leading, x, y)
	for i, l := range lines {
		if i > 0 {
			sb.WriteString("T*\n")
		}
		fmt.Fprintf(&sb, "(%s) Tj\n", escape(l))
	}
	sb.WriteString("ET\n")
	return sb.String()
}

// Grid draws a ruled table with the given column x and row y positions
// (PDF coordinates) and fills cells with text (row-major).
func Grid(xs, ys []float64, cells [][]string) string {
	var sb strings.Builder
	sb.WriteString("0.5 w 0 0 0 RG\n")
	for _, y := range ys {
		fmt.Fprintf(&sb, "%g %g m %g %g l S\n", xs[0], y, xs[len(xs)-1], y)
	}
	for _, x := range xs {
		fmt.Fprintf(&sb, "%g %g m %g %g l S\n", x, ys[0], x, ys[len(ys)-1])
	}
	for r, row := range cells {
		for c, t := range row {
			sb.WriteString(Text("F1", 10, xs[c]+4, ys[r]-14, t))
		}
	}
	return sb.String()
}

// Circle draws a stroked circle with Bézier curves.
func Circle(cx, cy, r float64) string {
	k := 0.5523 * r
	return fmt.Sprintf("1 0 0 RG 2 w %g %g m %g %g %g %g %g %g c %g %g %g %g %g %g c %g %g %g %g %g %g c %g %g %g %g %g %g c S\n",
		cx+r, cy,
		cx+r, cy+k, cx+k, cy+r, cx, cy+r,
		cx-k, cy+r, cx-r, cy+k, cx-r, cy,
		cx-r, cy-k, cx-k, cy-r, cx, cy-r,
		cx+k, cy-r, cx+r, cy-k, cx+r, cy)
}
