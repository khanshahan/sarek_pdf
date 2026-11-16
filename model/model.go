// Package model defines the structured document produced by sarek_pdf.
//
// Coordinates are in PDF points (1/72 inch) with the origin at the
// top-left corner of the (rotated) page and y growing downwards.
// All values are plain data: a Document can be shared between goroutines
// for reading.
package model

import (
	"strings"
)

// Point is a 2D point.
type Point struct{ X, Y float64 }

// Rect is an axis-aligned rectangle (X0 ≤ X1, Y0 ≤ Y1).
type Rect struct{ X0, Y0, X1, Y1 float64 }

// W returns the width.
func (r Rect) W() float64 { return r.X1 - r.X0 }

// H returns the height.
func (r Rect) H() float64 { return r.Y1 - r.Y0 }

// Empty reports whether the rectangle has no area.
func (r Rect) Empty() bool { return r.X1 <= r.X0 || r.Y1 <= r.Y0 }

// Union returns the smallest rectangle containing r and o.
func (r Rect) Union(o Rect) Rect {
	if r == (Rect{}) {
		return o
	}
	if o == (Rect{}) {
		return r
	}
	return Rect{min(r.X0, o.X0), min(r.Y0, o.Y0), max(r.X1, o.X1), max(r.Y1, o.Y1)}
}

// Intersect returns the intersection (possibly empty).
func (r Rect) Intersect(o Rect) Rect {
	return Rect{max(r.X0, o.X0), max(r.Y0, o.Y0), min(r.X1, o.X1), min(r.Y1, o.Y1)}
}

// Area returns the area (0 if empty).
func (r Rect) Area() float64 {
	if r.Empty() {
		return 0
	}
	return r.W() * r.H()
}

// Contains reports whether p lies inside r.
func (r Rect) Contains(p Point) bool { return p.X >= r.X0 && p.X <= r.X1 && p.Y >= r.Y0 && p.Y <= r.Y1 }

// Center returns the centre point.
func (r Rect) Center() Point { return Point{(r.X0 + r.X1) / 2, (r.Y0 + r.Y1) / 2} }

// Color is an sRGB colour.
type Color struct{ R, G, B uint8 }

// Hex returns "RRGGBB".
func (c Color) Hex() string {
	const h = "0123456789ABCDEF"
	return string([]byte{h[c.R>>4], h[c.R&15], h[c.G>>4], h[c.G&15], h[c.B>>4], h[c.B&15]})
}

// Style describes the visual style of a run of text.
type Style struct {
	Font   string  // family name, e.g. "Arial"
	Size   float64 // points
	Bold   bool
	Italic bool
	Color  Color
}

// Span is a run of text on one line with a uniform style.
type Span struct {
	Text  string
	BBox  Rect
	Style Style
	// Edges holds the left x-coordinate of every rune of Text plus the
	// right edge of the last rune (len = runes+1). Used for precise
	// search hits and highlighting.
	Edges []float64
	// Invisible marks text drawn with render mode 3 (e.g. OCR layers).
	Invisible bool
}

// Line is a visual line of text.
type Line struct {
	BBox     Rect
	Baseline float64
	Spans    []Span
}

// Text returns the line text.
func (l *Line) Text() string {
	var sb strings.Builder
	for _, s := range l.Spans {
		sb.WriteString(s.Text)
	}
	return sb.String()
}

// Run is a styled piece of flowing paragraph text (used by exporters).
type Run struct {
	Text  string
	Style Style
}

// Role classifies a paragraph.
type Role uint8

const (
	RoleBody Role = iota
	RoleHeading
	RoleListItem
	RoleCaption
	RolePageHeader
	RolePageFooter
)

func (r Role) String() string {
	return [...]string{"body", "heading", "list-item", "caption", "page-header", "page-footer"}[r]
}

// Align is horizontal paragraph alignment.
type Align uint8

const (
	AlignLeft Align = iota
	AlignCenter
	AlignRight
	AlignJustify
)

func (a Align) String() string { return [...]string{"left", "center", "right", "justify"}[a] }

// Block is a top-level page element: *Paragraph, *Table, *Drawing or *Image.
type Block interface {
	Bounds() Rect
	isBlock()
}

// Paragraph is a block of text lines.
type Paragraph struct {
	BBox  Rect
	Role  Role
	Level int // heading level 1–6 when Role == RoleHeading
	Align Align
	Lines []Line
	// Runs is the flowing text (lines joined, hyphenation repaired).
	Runs []Run
	// RTL is true when the dominant text direction is right-to-left.
	// Text is always stored in logical (reading) order.
	RTL bool
	// Original holds the text before language-model correction; empty
	// when the paragraph was not changed.
	Original string
}

// Bounds implements Block.
func (p *Paragraph) Bounds() Rect { return p.BBox }
func (*Paragraph) isBlock()       {}

// Text returns the flowing paragraph text.
func (p *Paragraph) Text() string {
	var sb strings.Builder
	for _, r := range p.Runs {
		sb.WriteString(r.Text)
	}
	return sb.String()
}

// Cell is a table cell. Covered grid positions (inside a span) have no Cell.
type Cell struct {
	Row, Col         int
	RowSpan, ColSpan int
	BBox             Rect
	Paragraphs       []*Paragraph
	Fill             *Color
}

// Text returns the cell text with paragraphs separated by newlines.
func (c *Cell) Text() string {
	parts := make([]string, 0, len(c.Paragraphs))
	for _, p := range c.Paragraphs {
		parts = append(parts, p.Text())
	}
	return strings.Join(parts, "\n")
}

// Table is a grid of cells.
type Table struct {
	BBox Rect
	// Ruled reports whether the grid was derived from drawn lines (true)
	// or from text alignment (false).
	Ruled bool
	// ColX holds NumCols+1 column boundaries; RowY holds NumRows+1.
	ColX  []float64
	RowY  []float64
	Cells []*Cell
}

// Bounds implements Block.
func (t *Table) Bounds() Rect { return t.BBox }
func (*Table) isBlock()       {}

// NumRows returns the number of grid rows.
func (t *Table) NumRows() int { return max(0, len(t.RowY)-1) }

// NumCols returns the number of grid columns.
func (t *Table) NumCols() int { return max(0, len(t.ColX)-1) }

// Grid returns a NumRows×NumCols matrix of cell texts (spanned cells are
// repeated across their span).
func (t *Table) Grid() [][]string {
	g := make([][]string, t.NumRows())
	for i := range g {
		g[i] = make([]string, t.NumCols())
	}
	for _, c := range t.Cells {
		txt := c.Text()
		for r := c.Row; r < c.Row+c.RowSpan && r < len(g); r++ {
			for k := c.Col; k < c.Col+c.ColSpan && k < len(g[r]); k++ {
				g[r][k] = txt
			}
		}
	}
	return g
}

// SegOp is a path segment operator.
type SegOp uint8

const (
	MoveTo SegOp = iota
	LineTo
	CurveTo // cubic Bézier: Pts[0], Pts[1] control points, Pts[2] end
	Close
)

// Segment is one path segment.
type Segment struct {
	Op  SegOp
	Pts [3]Point
}

// Path is a painted vector path.
type Path struct {
	Segments    []Segment
	Stroke      bool
	Fill        bool
	EvenOdd     bool
	StrokeColor Color
	FillColor   Color
	LineWidth   float64
	BBox        Rect
}

// Drawing is a group of vector paths forming a figure, with any text
// labels found inside it.
type Drawing struct {
	BBox   Rect
	Paths  []Path
	Labels []Line
}

// Bounds implements Block.
func (d *Drawing) Bounds() Rect { return d.BBox }
func (*Drawing) isBlock()       {}

// Image is a raster image placed on the page.
type Image struct {
	BBox   Rect
	Width  int // pixels
	Height int
	// Format is "jpeg", "png", "jp2", "jbig2", "ccitt" or "" when the
	// data was not extracted.
	Format string
	Data   []byte
}

// Bounds implements Block.
func (i *Image) Bounds() Rect { return i.BBox }
func (*Image) isBlock()       {}

// Page is one extracted page.
type Page struct {
	Number int // 1-based
	Width  float64
	Height float64
	Blocks []Block
}

// Paragraphs returns all paragraphs on the page in reading order,
// including those inside table cells when withTables is true.
func (p *Page) Paragraphs(withTables bool) []*Paragraph {
	var out []*Paragraph
	for _, b := range p.Blocks {
		switch v := b.(type) {
		case *Paragraph:
			out = append(out, v)
		case *Table:
			if withTables {
				for _, c := range v.Cells {
					out = append(out, c.Paragraphs...)
				}
			}
		}
	}
	return out
}

// Text returns the page text in reading order. Paragraphs are separated
// by blank lines; table cells by tabs and rows by newlines.
func (p *Page) Text() string {
	var sb strings.Builder
	for i, b := range p.Blocks {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		switch v := b.(type) {
		case *Paragraph:
			sb.WriteString(v.Text())
		case *Table:
			for r, row := range v.Grid() {
				if r > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(strings.Join(row, "\t"))
			}
		case *Drawing:
			for k, l := range v.Labels {
				if k > 0 {
					sb.WriteByte(' ')
				}
				sb.WriteString(l.Text())
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

// Metadata is the document information.
type Metadata struct {
	Title, Author, Subject, Keywords, Creator, Producer string
	CreationDate, ModDate                               string
}

// OutlineItem is a bookmark.
type OutlineItem struct {
	Title    string
	Page     int // 1-based, 0 if unresolved
	Children []OutlineItem
}

// Document is a fully extracted document.
type Document struct {
	Version  string
	Metadata Metadata
	Outline  []OutlineItem
	Pages    []*Page
}

// Text returns the full document text, pages separated by form feeds.
func (d *Document) Text() string {
	parts := make([]string, len(d.Pages))
	for i, p := range d.Pages {
		parts[i] = p.Text()
	}
	return strings.Join(parts, "\n\f")
}
