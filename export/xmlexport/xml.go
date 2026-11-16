// Package xmlexport writes a model.Document as XML.
//
// Schema overview (namespace urn:sarek-pdf:1):
//
//	<document version pages>
//	  <metadata><title/>…</metadata>
//	  <outline><item title page>…</item></outline>
//	  <page number width height>
//	    <paragraph role level align x y w h>
//	      <text>…</text>            flowing (possibly corrected) text
//	      <original>…</original>    only when corrected
//	      <line x y w h><span font size bold italic color x y w h>…</span></line>
//	    </paragraph>
//	    <table rows cols ruled x y w h>
//	      <cell row col rowspan colspan x y w h fill><paragraph/>…</cell>
//	    </table>
//	    <drawing x y w h><path d stroke fill stroke-width fill-rule/><label/></drawing>
//	    <image x y w h width height format ocr-source>base64?</image>
//	    <ocr engine reason lines confidence/>   only on pages that were OCR'd
//
// Text recognised by OCR carries source="ocr" and confidence="0.000–1.000"
// on its paragraph and span elements; the page carries ocr="true".
//
//	  </page>
//	</document>
//
// All coordinates are points from the page's top-left corner.
package xmlexport

import (
	"bufio"
	"encoding/base64"
	"io"
	"strconv"
	"strings"

	"github.com/khanshahan/sarek_pdf/model"
)

// Options controls the XML output.
type Options struct {
	// Geometry includes line/span elements with coordinates (default true
	// via DefaultOptions).
	Geometry bool
	// EmbedImages includes base64 image data.
	EmbedImages bool
	// Paths includes vector path data for drawings.
	Paths bool
}

// DefaultOptions returns the recommended options.
func DefaultOptions() Options { return Options{Geometry: true, Paths: true} }

type writer struct {
	w   *bufio.Writer
	o   Options
	err error
}

// Write serialises doc to w.
func Write(w io.Writer, doc *model.Document, o Options) error {
	x := &writer{w: bufio.NewWriterSize(w, 1<<16), o: o}
	x.raw(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	x.open("document", "xmlns", "urn:sarek-pdf:1", "version", doc.Version, "pages", strconv.Itoa(len(doc.Pages)))
	x.metadata(doc.Metadata)
	if len(doc.Outline) > 0 {
		x.open("outline")
		x.outline(doc.Outline)
		x.close("outline")
	}
	for _, p := range doc.Pages {
		x.page(p)
	}
	x.close("document")
	if x.err != nil {
		return x.err
	}
	return x.w.Flush()
}

func (x *writer) raw(s string) {
	if x.err == nil {
		_, x.err = x.w.WriteString(s)
	}
}

func (x *writer) open(name string, attrs ...string) { x.tag(name, false, attrs...) }

func (x *writer) empty(name string, attrs ...string) { x.tag(name, true, attrs...) }

func (x *writer) tag(name string, selfClose bool, attrs ...string) {
	x.raw("<" + name)
	for i := 0; i+1 < len(attrs); i += 2 {
		if attrs[i+1] == "" {
			continue
		}
		x.raw(" " + attrs[i] + `="`)
		x.text(attrs[i+1], true)
		x.raw(`"`)
	}
	if selfClose {
		x.raw("/>")
	} else {
		x.raw(">")
	}
}

func (x *writer) close(name string) { x.raw("</" + name + ">\n") }

func (x *writer) elem(name, text string, attrs ...string) {
	x.open(name, attrs...)
	x.text(text, false)
	x.close(name)
}

// text writes escaped character data, dropping characters not allowed in
// XML 1.0.
func (x *writer) text(s string, attr bool) {
	var sb strings.Builder
	sb.Grow(len(s) + 8)
	for _, r := range s {
		switch {
		case r == '<':
			sb.WriteString("&lt;")
		case r == '>':
			sb.WriteString("&gt;")
		case r == '&':
			sb.WriteString("&amp;")
		case r == '"' && attr:
			sb.WriteString("&quot;")
		case r == '\n' && attr:
			sb.WriteString("&#10;")
		case r == '\t' || r == '\n' || r == '\r':
			sb.WriteRune(r)
		case r < 0x20 || (r >= 0xD800 && r <= 0xDFFF) || r == 0xFFFE || r == 0xFFFF:
			// not representable in XML 1.0
		default:
			sb.WriteRune(r)
		}
	}
	x.raw(sb.String())
}

func f(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func box(r model.Rect) []string {
	return []string{"x", f(r.X0), "y", f(r.Y0), "w", f(r.W()), "h", f(r.H())}
}

func (x *writer) metadata(m model.Metadata) {
	x.open("metadata")
	x.raw("\n")
	for _, kv := range [][2]string{{"title", m.Title}, {"author", m.Author}, {"subject", m.Subject},
		{"keywords", m.Keywords}, {"creator", m.Creator}, {"producer", m.Producer},
		{"created", m.CreationDate}, {"modified", m.ModDate}} {
		if kv[1] != "" {
			x.elem(kv[0], kv[1])
		}
	}
	x.close("metadata")
}

func (x *writer) outline(items []model.OutlineItem) {
	for _, it := range items {
		attrs := []string{"title", it.Title}
		if it.Page > 0 {
			attrs = append(attrs, "page", strconv.Itoa(it.Page))
		}
		if len(it.Children) == 0 {
			x.empty("item", attrs...)
			x.raw("\n")
			continue
		}
		x.open("item", attrs...)
		x.raw("\n")
		x.outline(it.Children)
		x.close("item")
	}
}

func (x *writer) page(p *model.Page) {
	attrs := []string{"number", strconv.Itoa(p.Number), "width", f(p.Width), "height", f(p.Height)}
	if p.OCR != nil {
		attrs = append(attrs, "ocr", "true")
	}
	if p.NoTextLayer {
		attrs = append(attrs, "text-layer", "missing")
	}
	x.open("page", attrs...)
	x.raw("\n")
	if o := p.OCR; o != nil {
		x.open("ocr", "engine", o.Engine, "reason", o.Reason, "lines", strconv.Itoa(o.Lines),
			"confidence", strconv.FormatFloat(o.Confidence, 'f', 3, 64))
		for _, e := range o.Errors {
			x.elem("error", e)
		}
		x.close("ocr")
	}
	for _, b := range p.Blocks {
		switch v := b.(type) {
		case *model.Paragraph:
			x.paragraph(v)
		case *model.Table:
			x.table(v)
		case *model.Drawing:
			x.drawing(v)
		case *model.Image:
			x.image(v)
		}
	}
	x.close("page")
}

func (x *writer) paragraph(p *model.Paragraph) {
	attrs := []string{"role", p.Role.String()}
	if p.Role == model.RoleHeading {
		attrs = append(attrs, "level", strconv.Itoa(p.Level))
	}
	attrs = append(attrs, "align", p.Align.String())
	if p.Source() == model.SourceOCR {
		attrs = append(attrs, "source", "ocr", "confidence", strconv.FormatFloat(p.Confidence(), 'f', 3, 64))
	}
	if p.RTL {
		attrs = append(attrs, "dir", "rtl")
	}
	attrs = append(attrs, box(p.BBox)...)
	x.open("paragraph", attrs...)
	x.elem("text", p.Text())
	if p.Original != "" {
		x.elem("original", p.Original)
	}
	if x.o.Geometry {
		for i := range p.Lines {
			x.line(&p.Lines[i], "line")
		}
	}
	x.close("paragraph")
}

func (x *writer) line(l *model.Line, tag string) {
	x.open(tag, box(l.BBox)...)
	for _, s := range l.Spans {
		attrs := []string{"font", s.Style.Font, "size", f(s.Style.Size)}
		if s.Style.Bold {
			attrs = append(attrs, "bold", "true")
		}
		if s.Style.Italic {
			attrs = append(attrs, "italic", "true")
		}
		if s.Style.Color != (model.Color{}) {
			attrs = append(attrs, "color", "#"+s.Style.Color.Hex())
		}
		if s.Invisible {
			attrs = append(attrs, "invisible", "true")
		}
		if s.Source == model.SourceOCR {
			attrs = append(attrs, "source", "ocr", "confidence", strconv.FormatFloat(s.Confidence, 'f', 3, 64))
		}
		attrs = append(attrs, box(s.BBox)...)
		x.open("span", attrs...)
		x.text(s.Text, false)
		x.raw("</span>")
	}
	x.close(tag)
}

func (x *writer) table(t *model.Table) {
	attrs := []string{"rows", strconv.Itoa(t.NumRows()), "cols", strconv.Itoa(t.NumCols()),
		"ruled", strconv.FormatBool(t.Ruled)}
	x.open("table", append(attrs, box(t.BBox)...)...)
	x.raw("\n")
	for _, c := range t.Cells {
		ca := []string{"row", strconv.Itoa(c.Row), "col", strconv.Itoa(c.Col)}
		if c.RowSpan > 1 {
			ca = append(ca, "rowspan", strconv.Itoa(c.RowSpan))
		}
		if c.ColSpan > 1 {
			ca = append(ca, "colspan", strconv.Itoa(c.ColSpan))
		}
		if c.Fill != nil {
			ca = append(ca, "fill", "#"+c.Fill.Hex())
		}
		ca = append(ca, box(c.BBox)...)
		if len(c.Paragraphs) == 0 {
			x.empty("cell", ca...)
			x.raw("\n")
			continue
		}
		x.open("cell", ca...)
		for _, p := range c.Paragraphs {
			x.paragraph(p)
		}
		x.close("cell")
	}
	x.close("table")
}

// PathData renders path segments as SVG path data relative to origin.
func PathData(p *model.Path, ox, oy float64) string {
	var sb strings.Builder
	pt := func(q model.Point) {
		sb.WriteString(f(q.X - ox))
		sb.WriteByte(' ')
		sb.WriteString(f(q.Y - oy))
	}
	for _, s := range p.Segments {
		switch s.Op {
		case model.MoveTo:
			sb.WriteString("M")
			pt(s.Pts[0])
		case model.LineTo:
			sb.WriteString("L")
			pt(s.Pts[0])
		case model.CurveTo:
			sb.WriteString("C")
			pt(s.Pts[0])
			sb.WriteByte(' ')
			pt(s.Pts[1])
			sb.WriteByte(' ')
			pt(s.Pts[2])
		case model.Close:
			sb.WriteString("Z")
		}
	}
	return sb.String()
}

func (x *writer) drawing(d *model.Drawing) {
	x.open("drawing", append([]string{"paths", strconv.Itoa(len(d.Paths))}, box(d.BBox)...)...)
	x.raw("\n")
	if x.o.Paths {
		for i := range d.Paths {
			p := &d.Paths[i]
			attrs := []string{"d", PathData(p, 0, 0)}
			if p.Stroke {
				attrs = append(attrs, "stroke", "#"+p.StrokeColor.Hex(), "stroke-width", f(p.LineWidth))
			}
			if p.Fill {
				attrs = append(attrs, "fill", "#"+p.FillColor.Hex())
				if p.EvenOdd {
					attrs = append(attrs, "fill-rule", "evenodd")
				}
			}
			x.empty("path", attrs...)
			x.raw("\n")
		}
	}
	for i := range d.Labels {
		x.line(&d.Labels[i], "label")
	}
	x.close("drawing")
}

func (x *writer) image(im *model.Image) {
	attrs := append(box(im.BBox), "width", strconv.Itoa(im.Width), "height", strconv.Itoa(im.Height), "format", im.Format)
	if im.OCRSource {
		attrs = append(attrs, "ocr-source", "true")
	}
	if !x.o.EmbedImages || len(im.Data) == 0 {
		x.empty("image", attrs...)
		x.raw("\n")
		return
	}
	x.open("image", append(attrs, "encoding", "base64")...)
	x.raw(base64.StdEncoding.EncodeToString(im.Data))
	x.close("image")
}
