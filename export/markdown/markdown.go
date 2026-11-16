// Package markdown writes a model.Document as GitHub-flavoured Markdown.
//
// Mapping:
//   - headings      → "#" … "######"
//   - paragraphs    → text with **bold** / *italic* from the PDF's font styles
//   - list items    → "- item" (bullets) or "1. item" (numbered)
//   - tables        → pipe tables; a table continued across pages is
//     written once, merged; a merged cell's text is written once
//   - formulas      → $$ LaTeX $$ (equation numbers as \tag); inline math → $…$
//   - scripts       → <sup>/<sub> outside mathematics
//   - images        → ![image](…) when an asset writer is set, or data URIs
//   - drawings      → SVG assets ![drawing](…) with their text labels as alt text
//   - OCR pages     → a visible notice before the page's content
//   - page breaks   → <!-- page N --> comments (optional)
//
// All text is escaped so that PDF content can never turn into Markdown
// formatting by accident.
package markdown

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/khanshahan/sarek_pdf/export/docx"
	"github.com/khanshahan/sarek_pdf/model"
)

// Options controls Markdown output.
type Options struct {
	// PageMarkers writes "<!-- page N -->" before each page.
	PageMarkers bool
	// SkipHeadersFooters omits running page headers and footers.
	SkipHeadersFooters bool
	// Assets, when set, receives image and drawing files; the returned
	// string is used as the link target. When nil, images are embedded as
	// data URIs if EmbedImages is true and omitted otherwise.
	Assets func(name string, data []byte) (link string, err error)
	// EmbedImages embeds images and drawings as data URIs when Assets is nil.
	EmbedImages bool
	// NoDrawings omits vector drawings.
	NoDrawings bool
	// NoImages omits raster images.
	NoImages bool
}

// DefaultOptions returns options suited to reading and LLM ingestion:
// page markers on, running headers/footers removed.
func DefaultOptions() Options {
	return Options{PageMarkers: true, SkipHeadersFooters: true}
}

type writer struct {
	w      *bufio.Writer
	o      Options
	err    error
	assets int
}

// Write writes doc as Markdown to w.
func Write(w io.Writer, doc *model.Document, o Options) error {
	x := &writer{w: bufio.NewWriterSize(w, 1<<16), o: o}
	if t := strings.TrimSpace(doc.Metadata.Title); t != "" {
		x.printf("<!-- title: %s -->\n\n", strings.ReplaceAll(t, "--", "- -"))
	}
	for _, p := range doc.Pages {
		if o.PageMarkers {
			x.printf("<!-- page %d -->\n\n", p.Number)
		}
		if n := p.OCRNotice(); n != "" {
			x.printf("> %s\n\n", escape(strings.Trim(n, "[]")))
		}
		for _, b := range p.Blocks {
			x.block(p, b)
		}
	}
	if x.err != nil {
		return x.err
	}
	return x.w.Flush()
}

func (x *writer) printf(format string, a ...any) {
	if x.err == nil {
		_, x.err = fmt.Fprintf(x.w, format, a...)
	}
}

func (x *writer) block(p *model.Page, b model.Block) {
	switch v := b.(type) {
	case *model.Paragraph:
		if x.o.SkipHeadersFooters && (v.Role == model.RolePageHeader || v.Role == model.RolePageFooter) {
			return
		}
		x.paragraph(v)
	case *model.Table:
		if v.Prev == nil { // continuation parts are written with the first part
			x.table(v.Merged())
		}
	case *model.Image:
		if !x.o.NoImages && !v.OCRSource {
			x.image(p, v)
		}
	case *model.Drawing:
		if !x.o.NoDrawings {
			x.drawing(p, v)
		}
	case *model.Formula:
		tag := ""
		if v.Number != "" {
			tag = ` \tag{` + strings.Trim(v.Number, "()") + `}`
		}
		x.printf("$$\n%s%s\n$$\n\n", v.LaTeX(), tag)
	}
}

func (x *writer) paragraph(p *model.Paragraph) {
	switch p.Role {
	case model.RoleHeading:
		lvl := min(max(p.Level, 1), 6)
		x.printf("%s %s\n\n", strings.Repeat("#", lvl), escape(oneLine(p.Text())))
		return
	case model.RoleListItem:
		x.printf("%s\n\n", listItem(p))
		return
	}
	x.printf("%s\n\n", styledRuns(p.Runs))
}

// listItem renders a list paragraph: bullet glyphs become "- ", numbers
// such as "1." or "a)" are kept as written.
func listItem(p *model.Paragraph) string {
	text := strings.TrimSpace(p.Text())
	r, size := utf8.DecodeRuneInString(text)
	if strings.ContainsRune("•◦▪▫●○■□–-*·‣⁃➢✓✔", r) {
		return "- " + escape(strings.TrimSpace(text[size:]))
	}
	// Numbered: keep the marker unescaped so it renders as written.
	if i := strings.IndexAny(text, ".)"); i > 0 && i <= 5 {
		return text[:i+1] + " " + escape(strings.TrimSpace(text[i+1:]))
	}
	return escape(text)
}

// styledRuns renders runs with **bold** / *italic* markers placed inside
// surrounding whitespace, as Markdown requires. Inline mathematics becomes
// $…$ (with its scripts); other superscripts/subscripts use <sup>/<sub>.
func styledRuns(runs []model.Run) string {
	var sb strings.Builder
	for i := 0; i < len(runs); i++ {
		r := runs[i]
		if r.Formula != nil {
			sb.WriteString(leadingSpace(r.Text) + "$" + r.Formula.LaTeX() + "$" + trailingSpace(r.Text))
			continue
		}
		if r.Math {
			// Collect the math run and the scripts that follow it.
			var tex strings.Builder
			j := i
			for ; j < len(runs) && runs[j].Formula == nil && (runs[j].Math || runs[j].Script != model.ScriptNone && j > i); j++ {
				t := strings.TrimSpace(oneLine(runs[j].Text))
				node := &model.MathNode{Kind: model.MathOperator, Text: t}
				switch runs[j].Script {
				case model.ScriptSuper:
					tex.WriteString("^{" + node.LaTeX() + "}")
				case model.ScriptSub:
					tex.WriteString("_{" + node.LaTeX() + "}")
				default:
					tex.WriteString(mathRunLaTeX(t))
				}
			}
			lead := leadingSpace(r.Text)
			trail := trailingSpace(runs[j-1].Text)
			sb.WriteString(lead + "$" + tex.String() + "$" + trail)
			i = j - 1
			continue
		}
		t := oneLine(r.Text)
		if t == "" {
			continue
		}
		core := strings.TrimSpace(t)
		if core == "" {
			sb.WriteString(t)
			continue
		}
		lead := t[:strings.Index(t, core)]
		trail := t[len(lead)+len(core):]
		mark := ""
		switch {
		case r.Style.Bold && r.Style.Italic:
			mark = "***"
		case r.Style.Bold:
			mark = "**"
		case r.Style.Italic:
			mark = "*"
		}
		open, close := "", ""
		switch r.Script {
		case model.ScriptSuper:
			open, close = "<sup>", "</sup>"
		case model.ScriptSub:
			open, close = "<sub>", "</sub>"
		}
		sb.WriteString(lead + open + mark + escape(core) + mark + close + trail)
	}
	out := strings.TrimSpace(sb.String())
	return escapeLineStart(out)
}

// mathRunLaTeX converts a run of inline math text to LaTeX, character by
// character (letters stay letters, π → \pi, ≤ → \leq, …).
func mathRunLaTeX(t string) string {
	row := &model.MathNode{Kind: model.MathRow}
	for _, r := range t {
		if r == ' ' {
			continue
		}
		k := model.MathOperator
		switch {
		case unicode.IsLetter(r):
			k = model.MathIdent
		case unicode.IsDigit(r):
			k = model.MathNumber
		}
		row.Children = append(row.Children, &model.MathNode{Kind: k, Text: string(r)})
	}
	return row.LaTeX()
}

func leadingSpace(s string) string  { return s[:len(s)-len(strings.TrimLeft(s, " "))] }
func trailingSpace(s string) string { return s[len(strings.TrimRight(s, " ")):] }

func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s)
}

// escape backslash-escapes characters with inline Markdown meaning.
func escape(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '\\', '`', '*', '_', '[', ']', '<', '>', '|', '#':
			sb.WriteByte('\\')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// escapeLineStart prevents a paragraph from being read as a list,
// heading, block quote or rule because of its first characters.
func escapeLineStart(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '-', '+', '=':
		return "\\" + s
	}
	// "12. text" would become an ordered list.
	i := 0
	for i < len(s) && i < 9 && unicode.IsDigit(rune(s[i])) {
		i++
	}
	if i > 0 && i+1 < len(s) && (s[i] == '.' || s[i] == ')') && s[i+1] == ' ' {
		return s[:i] + "\\" + s[i:]
	}
	return s
}

func cellText(c string) string {
	parts := strings.Split(c, "\n")
	for i, p := range parts {
		parts[i] = escape(strings.TrimSpace(oneLine(p)))
	}
	return strings.Join(parts, "<br>")
}

func (x *writer) table(t *model.Table) {
	rows := t.Rows()
	if len(rows) == 0 || len(rows[0]) == 0 {
		return
	}
	writeRow := func(r []string) {
		x.printf("|")
		for _, c := range r {
			x.printf(" %s |", cellText(c))
		}
		x.printf("\n")
	}
	writeRow(rows[0])
	x.printf("|")
	for range rows[0] {
		x.printf(" --- |")
	}
	x.printf("\n")
	for _, r := range rows[1:] {
		writeRow(r)
	}
	x.printf("\n")
}

// link stores an asset (or embeds it) and returns its link target.
func (x *writer) link(name, mime string, data []byte) (string, bool) {
	if x.o.Assets != nil {
		l, err := x.o.Assets(name, data)
		if err != nil {
			if x.err == nil {
				x.err = err
			}
			return "", false
		}
		return l, true
	}
	if x.o.EmbedImages {
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), true
	}
	return "", false
}

func (x *writer) image(p *model.Page, im *model.Image) {
	var ext, mime string
	switch im.Format {
	case "jpeg":
		ext, mime = "jpg", "image/jpeg"
	case "png":
		ext, mime = "png", "image/png"
	default:
		return
	}
	if len(im.Data) == 0 || im.Width <= 4 || im.Height <= 4 {
		return // no data, or a few-pixel fill used as a background
	}
	x.assets++
	if l, ok := x.link(fmt.Sprintf("page%d-image%d.%s", p.Number, x.assets, ext), mime, im.Data); ok {
		x.printf("![image](%s)\n\n", l)
	}
}

func (x *writer) drawing(p *model.Page, d *model.Drawing) {
	if d.BBox.W() < 1 || d.BBox.H() < 1 {
		return
	}
	var labels []string
	for i := range d.Labels {
		if t := strings.TrimSpace(d.Labels[i].Text()); t != "" {
			labels = append(labels, t)
		}
	}
	alt := "drawing"
	if len(labels) > 0 {
		alt = "drawing: " + strings.Join(labels, " ")
	}
	x.assets++
	if l, ok := x.link(fmt.Sprintf("page%d-drawing%d.svg", p.Number, x.assets), "image/svg+xml", []byte(docx.SVG(d))); ok {
		x.printf("![%s](%s)\n\n", escape(oneLine(alt)), l)
		return
	}
	// Without assets, keep the drawing's text so no content is lost.
	if len(labels) > 0 {
		x.printf("%s\n\n", escapeLineStart(escape(strings.Join(labels, " "))))
	}
}
