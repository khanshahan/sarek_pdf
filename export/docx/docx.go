// Package docx writes a model.Document as an Office Open XML (.docx)
// file using only the standard library.
//
// Mapping:
//   - paragraphs → w:p with run formatting (font, size, bold, italic,
//     colour) and Heading1–6 / ListParagraph styles
//   - tables     → w:tbl with gridSpan / vMerge for merged cells; tables
//     continued across pages are written once, merged
//   - images     → inline pictures (JPEG/PNG)
//   - drawings   → inline SVG pictures with a PNG fallback
//   - pages      → page breaks; page size from the first page
//   - formulas   → native Word equations (OMML); scripts → vertAlign
//   - OCR text   → "OCR Text" paragraph style (orange left bar) and a Word
//     comment on each OCR page stating engine and mean confidence
package docx

import (
	"archive/zip"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/khanshahan/sarek_pdf/export/xmlexport"
	"github.com/khanshahan/sarek_pdf/model"
)

// Options controls DOCX output.
type Options struct {
	// SkipHeadersFooters omits paragraphs detected as running page
	// headers/footers.
	SkipHeadersFooters bool
	// NoPageBreaks joins pages without page breaks.
	NoPageBreaks bool
	// NoDrawings omits vector drawings.
	NoDrawings bool
	// NoImages omits raster images.
	NoImages bool
	// KeepOCRImages keeps page images whose text was recognised by OCR
	// (by default they are omitted because their text is already present).
	KeepOCRImages bool
}

const emuPerPt = 12700

type media struct {
	name, rid, ctype string
	data             []byte
}

type comment struct {
	id   int
	text string
}

type writer struct {
	o        Options
	comments []comment
	// pending is the OCR notice to attach to the next OCR paragraph.
	pending string
	sb      strings.Builder
	media   []media
	nextID  int
	maxW    float64 // max content width in points
}

// Write writes doc as DOCX to w.
func Write(w io.Writer, doc *model.Document, o Options) error {
	x := &writer{o: o, nextID: 1}
	pw, ph := 612.0, 792.0
	if len(doc.Pages) > 0 && doc.Pages[0].Width > 0 {
		pw, ph = doc.Pages[0].Width, doc.Pages[0].Height
	}
	margin := pageMargins(doc)
	x.maxW = pw - margin[1] - margin[3]

	x.sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture" xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"><w:body>`)
	for i, p := range doc.Pages {
		if i > 0 && !o.NoPageBreaks {
			x.sb.WriteString(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
		}
		x.pending = p.OCRNotice()
		for _, b := range p.Blocks {
			x.block(b)
		}
	}
	orient := ""
	if pw > ph {
		orient = ` w:orient="landscape"`
	}
	fmt.Fprintf(&x.sb, `<w:sectPr><w:pgSz w:w="%d" w:h="%d"%s/><w:pgMar w:top="%d" w:right="%d" w:bottom="%d" w:left="%d" w:header="360" w:footer="360" w:gutter="0"/></w:sectPr></w:body></w:document>`,
		tw(pw), tw(ph), orient, tw(margin[0]), tw(margin[1]), tw(margin[2]), tw(margin[3]))

	zw := zip.NewWriter(w)
	files := []struct{ name, body string }{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", rootRels},
		{"docProps/core.xml", coreXML(doc.Metadata)},
		{"docProps/app.xml", appXML},
		{"word/styles.xml", stylesXML},
		{"word/settings.xml", settingsXML},
		{"word/_rels/document.xml.rels", x.docRels()},
		{"word/document.xml", x.sb.String()},
	}
	ct := contentTypes
	if len(x.comments) > 0 {
		files = append(files, struct{ name, body string }{"word/comments.xml", x.commentsXML()})
		ct = strings.Replace(ct, "</Types>", `<Override PartName="/word/comments.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml"/></Types>`, 1)
	}
	files[0].body = ct
	for _, f := range files {
		fw, err := zw.Create(f.name)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(fw, f.body); err != nil {
			return err
		}
	}
	for _, m := range x.media {
		// Media is already compressed: store without deflate.
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: "word/media/" + m.name, Method: zip.Store})
		if err != nil {
			return err
		}
		if _, err := fw.Write(m.data); err != nil {
			return err
		}
	}
	return zw.Close()
}

// pageMargins estimates [top right bottom left] margins from content.
func pageMargins(doc *model.Document) [4]float64 {
	m := [4]float64{72, 72, 72, 72}
	if len(doc.Pages) == 0 {
		return m
	}
	p := doc.Pages[0]
	var bb model.Rect
	for _, b := range p.Blocks {
		bb = bb.Union(b.Bounds())
	}
	if bb.Empty() {
		return m
	}
	clamp := func(v float64) float64 { return math.Max(36, math.Min(108, v)) }
	return [4]float64{clamp(bb.Y0), clamp(p.Width - bb.X1), clamp(p.Height - bb.Y1), clamp(bb.X0)}
}

func tw(pt float64) int { return int(math.Round(pt * 20)) }

func esc(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '<':
			sb.WriteString("&lt;")
		case r == '>':
			sb.WriteString("&gt;")
		case r == '&':
			sb.WriteString("&amp;")
		case r == '"':
			sb.WriteString("&quot;")
		case r == '\t' || r == '\n' || r == '\r':
			sb.WriteRune(r)
		case r < 0x20 || (r >= 0xD800 && r <= 0xDFFF) || r == 0xFFFE || r == 0xFFFF:
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func (x *writer) block(b model.Block) {
	switch v := b.(type) {
	case *model.Paragraph:
		if x.o.SkipHeadersFooters && (v.Role == model.RolePageHeader || v.Role == model.RolePageFooter) {
			return
		}
		x.paragraph(v)
	case *model.Table:
		// A table continued from the previous page was already written,
		// merged, with its first part.
		if v.Prev == nil {
			x.table(v.Merged())
		}
	case *model.Image:
		if !x.o.NoImages && (!v.OCRSource || x.o.KeepOCRImages) {
			x.image(v)
		}
	case *model.Drawing:
		if !x.o.NoDrawings {
			x.drawing(v)
		}
	case *model.Formula:
		x.formula(v)
	}
}

// formula writes a display formula as a centred native Word equation,
// followed by its equation number.
func (x *writer) formula(f *model.Formula) {
	style := ""
	if f.Source == model.SourceOCR {
		style = `<w:pStyle w:val="OCRText"/>`
	}
	x.sb.WriteString(`<w:p><w:pPr>` + style + `<w:jc w:val="center"/></w:pPr><m:oMath>`)
	x.sb.WriteString(omml(f.Root))
	x.sb.WriteString(`</m:oMath>`)
	if f.Number != "" {
		x.sb.WriteString(`<w:r><w:t xml:space="preserve">    ` + esc(f.Number) + `</w:t></w:r>`)
	}
	x.sb.WriteString(`</w:p>`)
}

func (x *writer) paragraph(p *model.Paragraph) {
	isOCR := p.Source() == model.SourceOCR
	x.sb.WriteString("<w:p><w:pPr>")
	switch p.Role {
	case model.RoleHeading:
		lvl := min(max(p.Level, 1), 6)
		fmt.Fprintf(&x.sb, `<w:pStyle w:val="Heading%d"/>`, lvl)
	case model.RoleListItem:
		x.sb.WriteString(`<w:pStyle w:val="ListParagraph"/>`)
	case model.RolePageHeader, model.RolePageFooter:
		x.sb.WriteString(`<w:pStyle w:val="HeaderFooterText"/>`)
	default:
		if isOCR {
			x.sb.WriteString(`<w:pStyle w:val="OCRText"/>`)
		}
	}
	if p.RTL {
		x.sb.WriteString(`<w:bidi/>`)
	}
	switch p.Align {
	case model.AlignCenter:
		x.sb.WriteString(`<w:jc w:val="center"/>`)
	case model.AlignRight:
		x.sb.WriteString(`<w:jc w:val="right"/>`)
	case model.AlignJustify:
		x.sb.WriteString(`<w:jc w:val="both"/>`)
	}
	x.sb.WriteString("</w:pPr>")
	cid := -1
	if isOCR && x.pending != "" {
		cid = len(x.comments)
		x.comments = append(x.comments, comment{id: cid, text: x.pending})
		x.pending = ""
		fmt.Fprintf(&x.sb, `<w:commentRangeStart w:id="%d"/>`, cid)
	}
	for _, r := range p.Runs {
		x.run(r)
	}
	if cid >= 0 {
		fmt.Fprintf(&x.sb, `<w:commentRangeEnd w:id="%d"/><w:r><w:rPr><w:rStyle w:val="CommentReference"/></w:rPr><w:commentReference w:id="%d"/></w:r>`, cid, cid)
	}
	x.sb.WriteString("</w:p>")
}

func (x *writer) commentsXML() string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:comments xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	for _, c := range x.comments {
		fmt.Fprintf(&sb, `<w:comment w:id="%d" w:author="sarek_pdf" w:initials="OCR"><w:p><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p></w:comment>`, c.id, esc(c.text))
	}
	sb.WriteString("</w:comments>")
	return sb.String()
}

func (x *writer) run(r model.Run) {
	if r.Text == "" {
		return
	}
	if r.Formula != nil {
		x.sb.WriteString("<m:oMath>" + omml(r.Formula) + "</m:oMath>")
		return
	}
	x.sb.WriteString("<w:r><w:rPr>")
	if f := r.Style.Font; f != "" {
		fe := esc(f)
		fmt.Fprintf(&x.sb, `<w:rFonts w:ascii="%s" w:hAnsi="%s" w:cs="%s"/>`, fe, fe, fe)
	}
	if r.Style.Bold {
		x.sb.WriteString("<w:b/><w:bCs/>")
	}
	if r.Style.Italic {
		x.sb.WriteString("<w:i/><w:iCs/>")
	}
	if hasRTL(r.Text) {
		x.sb.WriteString("<w:rtl/>")
	}
	switch r.Script {
	case model.ScriptSuper:
		x.sb.WriteString(`<w:vertAlign w:val="superscript"/>`)
	case model.ScriptSub:
		x.sb.WriteString(`<w:vertAlign w:val="subscript"/>`)
	}
	if c := r.Style.Color; c != (model.Color{}) {
		fmt.Fprintf(&x.sb, `<w:color w:val="%s"/>`, c.Hex())
	}
	if s := r.Style.Size; s > 0 {
		hp := int(math.Round(s * 2))
		fmt.Fprintf(&x.sb, `<w:sz w:val="%d"/><w:szCs w:val="%d"/>`, hp, hp)
	}
	x.sb.WriteString(`</w:rPr><w:t xml:space="preserve">`)
	x.sb.WriteString(esc(r.Text))
	x.sb.WriteString("</w:t></w:r>")
}

func hasRTL(s string) bool {
	for _, r := range s {
		if r >= 0x0590 && r <= 0x08FF || r >= 0xFB1D && r <= 0xFEFF {
			return true
		}
	}
	return false
}

func (x *writer) table(t *model.Table) {
	nr, nc := t.NumRows(), t.NumCols()
	if nr == 0 || nc == 0 {
		return
	}
	scale := 1.0
	if w := t.BBox.W(); w > x.maxW && w > 0 {
		scale = x.maxW / w
	}
	x.sb.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="0" w:type="auto"/><w:tblLayout w:type="fixed"/>`)
	if t.Ruled {
		x.sb.WriteString(`<w:tblBorders><w:top w:val="single" w:sz="4" w:space="0" w:color="000000"/><w:left w:val="single" w:sz="4" w:space="0" w:color="000000"/><w:bottom w:val="single" w:sz="4" w:space="0" w:color="000000"/><w:right w:val="single" w:sz="4" w:space="0" w:color="000000"/><w:insideH w:val="single" w:sz="4" w:space="0" w:color="000000"/><w:insideV w:val="single" w:sz="4" w:space="0" w:color="000000"/></w:tblBorders>`)
	}
	x.sb.WriteString(`<w:tblCellMar><w:left w:w="57" w:type="dxa"/><w:right w:w="57" w:type="dxa"/></w:tblCellMar></w:tblPr><w:tblGrid>`)
	colW := make([]int, nc)
	for c := 0; c < nc; c++ {
		colW[c] = max(tw((t.ColX[c+1]-t.ColX[c])*scale), 200)
		fmt.Fprintf(&x.sb, `<w:gridCol w:w="%d"/>`, colW[c])
	}
	x.sb.WriteString("</w:tblGrid>")
	cover := make([][]*model.Cell, nr)
	for r := range cover {
		cover[r] = make([]*model.Cell, nc)
	}
	for _, c := range t.Cells {
		for r := c.Row; r < c.Row+c.RowSpan && r < nr; r++ {
			for k := c.Col; k < c.Col+c.ColSpan && k < nc; k++ {
				cover[r][k] = c
			}
		}
	}
	for r := 0; r < nr; r++ {
		x.sb.WriteString("<w:tr>")
		for c := 0; c < nc; {
			cell := cover[r][c]
			span := 1
			if cell != nil && cell.Col == c {
				span = min(cell.ColSpan, nc-c)
			}
			width := 0
			for k := c; k < c+span; k++ {
				width += colW[k]
			}
			fmt.Fprintf(&x.sb, `<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/>`, width)
			if span > 1 {
				fmt.Fprintf(&x.sb, `<w:gridSpan w:val="%d"/>`, span)
			}
			origin := cell != nil && cell.Row == r && cell.Col == c
			if cell != nil && cell.RowSpan > 1 && cell.Col == c {
				if origin {
					x.sb.WriteString(`<w:vMerge w:val="restart"/>`)
				} else {
					x.sb.WriteString(`<w:vMerge/>`)
				}
			}
			if cell != nil && cell.Fill != nil {
				fmt.Fprintf(&x.sb, `<w:shd w:val="clear" w:color="auto" w:fill="%s"/>`, cell.Fill.Hex())
			}
			x.sb.WriteString("</w:tcPr>")
			if origin && len(cell.Paragraphs) > 0 {
				for _, p := range cell.Paragraphs {
					x.paragraph(p)
				}
			} else {
				x.sb.WriteString("<w:p/>")
			}
			x.sb.WriteString("</w:tc>")
			c += span
		}
		x.sb.WriteString("</w:tr>")
	}
	x.sb.WriteString("</w:tbl><w:p/>")
}

func (x *writer) addMedia(ext, ctype string, data []byte) string {
	n := len(x.media) + 1
	m := media{name: fmt.Sprintf("media%d.%s", n, ext), rid: fmt.Sprintf("rIdM%d", n), ctype: ctype, data: data}
	x.media = append(x.media, m)
	return m.rid
}

// fit scales a bbox (points) to EMU, limited to the content width.
func (x *writer) fit(bb model.Rect) (int, int) {
	w, h := bb.W(), bb.H()
	if w > x.maxW && w > 0 {
		h, w = h*x.maxW/w, x.maxW
	}
	return int(w * emuPerPt), int(h * emuPerPt)
}

func (x *writer) inlinePic(cx, cy int, blip string) {
	id := x.nextID
	x.nextID++
	fmt.Fprintf(&x.sb, `<w:p><w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="%d" cy="%d"/><wp:docPr id="%d" name="Picture %d"/><wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:nvPicPr><pic:cNvPr id="%d" name="Picture %d"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill>%s<a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`,
		cx, cy, id, id, id, id, blip, cx, cy)
}

func (x *writer) image(im *model.Image) {
	var ext, ct string
	switch im.Format {
	case "jpeg":
		ext, ct = "jpeg", "image/jpeg"
	case "png":
		ext, ct = "png", "image/png"
	default:
		return // formats Word cannot display are available via the XML export
	}
	if len(im.Data) == 0 {
		return
	}
	// Images of a few pixels are solid fills stretched as backgrounds or
	// rules, not pictures; they only clutter the document.
	if im.Width <= 4 || im.Height <= 4 {
		return
	}
	rid := x.addMedia(ext, ct, im.Data)
	cx, cy := x.fit(im.BBox)
	x.inlinePic(cx, cy, fmt.Sprintf(`<a:blip r:embed="%s"/>`, rid))
}

func (x *writer) drawing(d *model.Drawing) {
	if d.BBox.W() < 1 || d.BBox.H() < 1 {
		return
	}
	svg := SVG(d)
	pngData := rasterize(d, 1600)
	if pngData == nil {
		return
	}
	pngID := x.addMedia("png", "image/png", pngData)
	svgID := x.addMedia("svg", "image/svg+xml", []byte(svg))
	cx, cy := x.fit(d.BBox)
	x.inlinePic(cx, cy, fmt.Sprintf(`<a:blip r:embed="%s"><a:extLst><a:ext uri="{96DAC541-7B7A-43D3-8B79-37D633B846F1}"><asvg:svgBlip xmlns:asvg="http://schemas.microsoft.com/office/drawing/2016/SVG/main" r:embed="%s"/></a:ext></a:extLst></a:blip>`, pngID, svgID))
}

// SVG renders a drawing (paths and labels) as a standalone SVG document.
func SVG(d *model.Drawing) string {
	bb := d.BBox
	var sb strings.Builder
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" width="%spt" height="%spt" viewBox="0 0 %s %s">`, f(bb.W()), f(bb.H()), f(bb.W()), f(bb.H()))
	for i := range d.Paths {
		p := &d.Paths[i]
		fill, stroke := "none", "none"
		if p.Fill {
			fill = "#" + p.FillColor.Hex()
		}
		if p.Stroke {
			stroke = "#" + p.StrokeColor.Hex()
		}
		rule := ""
		if p.EvenOdd {
			rule = ` fill-rule="evenodd"`
		}
		fmt.Fprintf(&sb, `<path d="%s" fill="%s" stroke="%s" stroke-width="%s"%s/>`,
			xmlexport.PathData(p, bb.X0, bb.Y0), fill, stroke, f(p.LineWidth), rule)
	}
	for _, l := range d.Labels {
		for _, s := range l.Spans {
			if strings.TrimSpace(s.Text) == "" {
				continue
			}
			weight, style := "normal", "normal"
			if s.Style.Bold {
				weight = "bold"
			}
			if s.Style.Italic {
				style = "italic"
			}
			base := l.Baseline
			if base == 0 {
				base = s.BBox.Y1 - 0.2*s.Style.Size
			}
			fmt.Fprintf(&sb, `<text x="%s" y="%s" font-family="%s" font-size="%s" font-weight="%s" font-style="%s" fill="#%s">%s</text>`,
				f(s.BBox.X0-bb.X0), f(base-bb.Y0), esc(s.Style.Font), f(s.Style.Size), weight, style, s.Style.Color.Hex(), esc(s.Text))
		}
	}
	sb.WriteString("</svg>")
	return sb.String()
}

func (x *writer) docRels() string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdStyles" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/><Relationship Id="rIdSettings" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings" Target="settings.xml"/>`)
	for _, m := range x.media {
		fmt.Fprintf(&sb, `<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/%s"/>`, m.rid, m.name)
	}
	if len(x.comments) > 0 {
		sb.WriteString(`<Relationship Id="rIdComments" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/comments" Target="comments.xml"/>`)
	}
	sb.WriteString("</Relationships>")
	return sb.String()
}

func coreXML(m model.Metadata) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>` +
		esc(m.Title) + `</dc:title><dc:subject>` + esc(m.Subject) + `</dc:subject><dc:creator>` + esc(m.Author) +
		`</dc:creator><cp:keywords>` + esc(m.Keywords) + `</cp:keywords></cp:coreProperties>`
}

const contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Default Extension="jpeg" ContentType="image/jpeg"/><Default Extension="svg" ContentType="image/svg+xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/><Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/><Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/></Types>`

const rootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/></Relationships>`

const appXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"><Application>sarek_pdf</Application></Properties>`

const settingsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:compat><w:compatSetting w:name="compatibilityMode" w:uri="http://schemas.microsoft.com/office/word" w:val="15"/></w:compat></w:settings>`

var stylesXML = func() string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:cs="Calibri"/><w:sz w:val="22"/><w:szCs w:val="22"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="264" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults><w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>`)
	sizes := []int{36, 30, 26, 24, 22, 22}
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&sb, `<w:style w:type="paragraph" w:styleId="Heading%d"><w:name w:val="heading %d"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:uiPriority w:val="9"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="120"/><w:outlineLvl w:val="%d"/></w:pPr><w:rPr><w:b/><w:sz w:val="%d"/><w:szCs w:val="%d"/></w:rPr></w:style>`, i, i, i-1, sizes[i-1], sizes[i-1])
	}
	sb.WriteString(`<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/><w:uiPriority w:val="34"/><w:qFormat/><w:pPr><w:ind w:left="360"/></w:pPr></w:style><w:style w:type="paragraph" w:customStyle="1" w:styleId="HeaderFooterText"><w:name w:val="Header Footer Text"/><w:basedOn w:val="Normal"/><w:rPr><w:color w:val="7F7F7F"/><w:sz w:val="16"/></w:rPr></w:style><w:style w:type="paragraph" w:customStyle="1" w:styleId="OCRText"><w:name w:val="OCR Text"/><w:basedOn w:val="Normal"/><w:qFormat/><w:pPr><w:pBdr><w:left w:val="single" w:sz="12" w:space="4" w:color="F0A30A"/></w:pBdr></w:pPr></w:style><w:style w:type="character" w:styleId="CommentReference"><w:name w:val="annotation reference"/><w:rPr><w:sz w:val="16"/></w:rPr></w:style><w:style w:type="table" w:default="1" w:styleId="TableNormal"><w:name w:val="Normal Table"/><w:tblPr><w:tblInd w:w="0" w:type="dxa"/><w:tblCellMar><w:top w:w="0" w:type="dxa"/><w:left w:w="108" w:type="dxa"/><w:bottom w:w="0" w:type="dxa"/><w:right w:w="108" w:type="dxa"/></w:tblCellMar></w:tblPr></w:style></w:styles>`)
	return sb.String()
}()
