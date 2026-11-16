package sarekpdf

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/khanshahan/sarek_pdf/internal/content"
	"github.com/khanshahan/sarek_pdf/internal/pdf"
	"github.com/khanshahan/sarek_pdf/model"
)

// Highlight describes highlight annotations to add to a page.
type Highlight struct {
	Page   int          // 1-based
	Rects  []model.Rect // display coordinates (as returned by Search)
	Color  model.Color  // default yellow
	Note   string       // optional pop-up text
	Author string       // optional
}

// HighlightMatches converts search matches into highlights.
func HighlightMatches(ms []Match, c model.Color) []Highlight {
	out := make([]Highlight, 0, len(ms))
	for _, m := range ms {
		out = append(out, Highlight{Page: m.Page, Rects: m.Rects, Color: c})
	}
	return out
}

// WriteHighlighted writes a copy of the PDF with highlight annotations
// added. The original bytes are preserved and the annotations are
// appended as an incremental update, so signatures over earlier
// revisions stay intact. Damaged files (rebuilt cross-reference table)
// are rewritten instead.
func (d *Document) WriteHighlighted(w io.Writer, hs []Highlight) error {
	byPage := map[int][]Highlight{}
	for _, h := range hs {
		if h.Page < 1 || h.Page > len(d.pages) {
			return fmt.Errorf("sarekpdf: highlight page %d out of range", h.Page)
		}
		if len(h.Rects) == 0 {
			continue
		}
		if h.Color == (model.Color{}) {
			h.Color = model.Color{R: 255, G: 235, B: 59}
		}
		byPage[h.Page] = append(byPage[h.Page], h)
	}
	if d.r.Repaired() {
		all := make([]int, len(d.pages))
		for i := range all {
			all[i] = i + 1
		}
		return d.rewrite(w, all, byPage)
	}
	data := d.r.Data()
	if _, err := w.Write(data); err != nil {
		return err
	}
	base := int64(len(data))
	fw := pdf.NewFileWriter(w, base)
	if len(data) > 0 && data[len(data)-1] != '\n' && data[len(data)-1] != '\r' {
		fw.Raw([]byte("\n"))
	}
	next := d.r.MaxObjectNumber()
	alloc := func() pdf.Ref { r := pdf.Ref{Num: next}; next++; return r }
	encrypted := d.r.Encrypted()

	pageNums := make([]int, 0, len(byPage))
	for n := range byPage {
		pageNums = append(pageNums, n)
	}
	sort.Ints(pageNums)
	for _, n := range pageNums {
		pg := d.pages[n-1]
		var newRefs pdf.Array
		for _, h := range byPage[n] {
			annot, ap := d.highlightAnnot(pg, h, encrypted)
			if ap != nil {
				apRef := alloc()
				fw.WriteObject(apRef, ap)
				annot["AP"] = pdf.Dict{"N": apRef}
			}
			ref := alloc()
			annot["P"] = pg.Ref
			fw.WriteObject(ref, annot)
			newRefs = append(newRefs, ref)
		}
		// Attach to the page: rewrite the /Annots array object when it is
		// indirect, otherwise rewrite the page dictionary.
		if aref, ok := pg.Dict["Annots"].(pdf.Ref); ok {
			arr := append(append(pdf.Array{}, d.r.Array(aref)...), newRefs...)
			fw.WriteObject(aref, arr)
			continue
		}
		if encrypted && containsString(pg.Dict) {
			return errors.New("sarekpdf: cannot update this encrypted page in place")
		}
		pd := pdf.Dict{}
		for k, v := range pg.Dict {
			pd[k] = v
		}
		pd["Annots"] = append(append(pdf.Array{}, d.r.Array(pg.Dict["Annots"])...), newRefs...)
		fw.WriteObject(pg.Ref, pd)
	}
	trailer := pdf.Dict{"Size": pdf.Integer(next), "Prev": pdf.Integer(d.r.StartXref())}
	for _, k := range []pdf.Name{"Root", "Info", "ID", "Encrypt"} {
		if v, ok := d.r.Trailer[k]; ok {
			trailer[k] = v
		}
	}
	if d.r.UsesXrefStream() {
		x := alloc()
		trailer["Size"] = pdf.Integer(next)
		fw.WriteXrefStream(x.Num, trailer)
	} else {
		fw.WriteXrefTable(trailer, false)
	}
	return fw.Flush()
}

func containsString(o pdf.Object) bool {
	switch v := o.(type) {
	case pdf.String:
		return true
	case pdf.Array:
		for _, e := range v {
			if containsString(e) {
				return true
			}
		}
	case pdf.Dict:
		for _, e := range v {
			if containsString(e) {
				return true
			}
		}
	}
	return false
}

// invert returns the inverse of an affine matrix.
func invert(m content.Matrix) content.Matrix {
	det := m[0]*m[3] - m[1]*m[2]
	if det == 0 {
		return content.Identity
	}
	a, b, c, d := m[3]/det, -m[1]/det, -m[2]/det, m[0]/det
	return content.Matrix{a, b, c, d, -(m[4]*a + m[5]*c), -(m[4]*b + m[5]*d)}
}

func num(f float64) pdf.Object { return pdf.Real(float64(int64(f*1000)) / 1000) }

// highlightAnnot builds the annotation dictionary and (unless the file is
// encrypted) an appearance stream.
func (d *Document) highlightAnnot(pg pdf.Page, h Highlight, encrypted bool) (pdf.Dict, *pdf.Stream) {
	m, _, _ := content.PageMatrix(pg.CropBox, pg.Rotate)
	inv := invert(m)
	var quads pdf.Array
	var bb model.Rect
	var ap bytes.Buffer
	col := func(c uint8) string { return strconv.FormatFloat(float64(c)/255, 'f', 3, 64) }
	ap.WriteString("/G0 gs " + col(h.Color.R) + " " + col(h.Color.G) + " " + col(h.Color.B) + " rg\n")
	for _, r := range h.Rects {
		tl, tr := inv.Apply(r.X0, r.Y0), inv.Apply(r.X1, r.Y0)
		bl, br := inv.Apply(r.X0, r.Y1), inv.Apply(r.X1, r.Y1)
		for _, p := range []model.Point{tl, tr, bl, br} {
			quads = append(quads, num(p.X), num(p.Y))
			bb = bb.Union(model.Rect{X0: p.X, Y0: p.Y, X1: p.X + 0.001, Y1: p.Y + 0.001})
		}
		fmt.Fprintf(&ap, "%.3f %.3f m %.3f %.3f l %.3f %.3f l %.3f %.3f l h f\n", tl.X, tl.Y, tr.X, tr.Y, br.X, br.Y, bl.X, bl.Y)
	}
	rect := pdf.Array{num(bb.X0), num(bb.Y0), num(bb.X1), num(bb.Y1)}
	annot := pdf.Dict{
		"Type": pdf.Name("Annot"), "Subtype": pdf.Name("Highlight"), "Rect": rect,
		"QuadPoints": quads, "F": pdf.Integer(4),
		"C": pdf.Array{num(float64(h.Color.R) / 255), num(float64(h.Color.G) / 255), num(float64(h.Color.B) / 255)},
	}
	if encrypted {
		return annot, nil // strings/streams would require encryption
	}
	annot["M"] = pdf.String(time.Now().UTC().Format("D:20060102150405Z"))
	if h.Note != "" {
		annot["Contents"] = pdf.TextStringObject(h.Note)
	}
	if h.Author != "" {
		annot["T"] = pdf.TextStringObject(h.Author)
	}
	apStream := &pdf.Stream{Dict: pdf.Dict{
		"Type": pdf.Name("XObject"), "Subtype": pdf.Name("Form"), "BBox": rect,
		"Resources": pdf.Dict{"ExtGState": pdf.Dict{"G0": pdf.Dict{
			"Type": pdf.Name("ExtGState"), "BM": pdf.Name("Multiply"), "CA": pdf.Real(1), "ca": pdf.Real(1)}}},
	}, Raw: ap.Bytes()}
	return annot, apStream
}

// ExtractPages writes a new PDF containing only the given pages
// (1-based, in the given order). The output is not encrypted.
func (d *Document) ExtractPages(w io.Writer, pages []int) error {
	if len(pages) == 0 {
		return errors.New("sarekpdf: no pages selected")
	}
	for _, n := range pages {
		if n < 1 || n > len(d.pages) {
			return fmt.Errorf("sarekpdf: page %d out of range 1..%d", n, len(d.pages))
		}
	}
	return d.rewrite(w, pages, nil)
}

type copier struct {
	r       *pdf.Reader
	mapping map[pdf.Ref]pdf.Ref
	pages   map[pdf.Ref]pdf.Ref
	queue   []pdf.Ref
	next    uint32
}

func (c *copier) alloc() pdf.Ref { r := pdf.Ref{Num: c.next}; c.next++; return r }

func (c *copier) mapRef(old pdf.Ref) pdf.Object {
	if nr, ok := c.pages[old]; ok {
		return nr
	}
	if nr, ok := c.mapping[old]; ok {
		return nr
	}
	if dd := c.r.Dict(old); dd != nil {
		if t := c.r.Name(dd["Type"]); t == "Page" || t == "Pages" || t == "Catalog" {
			return nil // never pull in unselected pages
		}
	}
	nr := c.alloc()
	c.mapping[old] = nr
	c.queue = append(c.queue, old)
	return nr
}

func (c *copier) copy(o pdf.Object) pdf.Object {
	switch v := o.(type) {
	case pdf.Ref:
		return c.mapRef(v)
	case pdf.Array:
		out := make(pdf.Array, len(v))
		for i, e := range v {
			out[i] = c.copy(e)
		}
		return out
	case pdf.Dict:
		out := make(pdf.Dict, len(v))
		for k, e := range v {
			out[k] = c.copy(e)
		}
		return out
	case *pdf.Stream:
		dd := c.copy(v.Dict).(pdf.Dict)
		return &pdf.Stream{Dict: dd, Raw: v.Raw}
	}
	return o
}

// rewrite writes a complete new file with the selected pages.
func (d *Document) rewrite(w io.Writer, pages []int, highlights map[int][]Highlight) error {
	version := d.r.Version
	if version < "1.4" {
		version = "1.4"
	}
	fw := pdf.NewFileWriter(w, 0)
	fw.Raw([]byte("%PDF-" + version + "\n%\xE2\xE3\xCF\xD3\n"))
	c := &copier{r: d.r, mapping: map[pdf.Ref]pdf.Ref{}, pages: map[pdf.Ref]pdf.Ref{}, next: 1}
	catalog, root := c.alloc(), c.alloc()
	newPages := make([]pdf.Ref, len(pages))
	for i, n := range pages {
		pr := d.pages[n-1].Ref
		if _, dup := c.pages[pr]; dup || pr == (pdf.Ref{}) {
			newPages[i] = c.alloc() // duplicate selection: a distinct page object
		} else {
			newPages[i] = c.alloc()
			c.pages[pr] = newPages[i]
		}
	}
	for i, n := range pages {
		pg := d.pages[n-1]
		pd := pdf.Dict{}
		for k, v := range pg.Dict {
			switch k {
			case "Parent", "B", "StructParents", "Annots":
				continue
			}
			pd[k] = c.copy(v)
		}
		pd["Type"] = pdf.Name("Page")
		pd["Parent"] = root
		pd["Resources"] = c.copy(pg.Dict["Resources"])
		if pg.Dict["Resources"] == nil {
			pd["Resources"] = c.copy(pdf.Dict(pg.Resources))
		}
		pd["MediaBox"] = boxArray(pg.MediaBox)
		if pg.CropBox != pg.MediaBox {
			pd["CropBox"] = boxArray(pg.CropBox)
		}
		if pg.Rotate != 0 {
			pd["Rotate"] = pdf.Integer(pg.Rotate)
		}
		var annots pdf.Array
		for _, a := range d.r.Array(pg.Dict["Annots"]) {
			if na := c.copy(a); na != nil {
				annots = append(annots, na)
			}
		}
		for _, h := range highlights[n] {
			annot, ap := d.highlightAnnot(pg, h, false)
			if ap != nil {
				apRef := c.alloc()
				fw.WriteObject(apRef, ap)
				annot["AP"] = pdf.Dict{"N": apRef}
			}
			annot["P"] = newPages[i]
			ar := c.alloc()
			fw.WriteObject(ar, annot)
			annots = append(annots, ar)
		}
		if len(annots) > 0 {
			pd["Annots"] = annots
		}
		fw.WriteObject(newPages[i], pd)
	}
	// Info is copied before draining the queue so its references are written.
	var infoRef pdf.Ref
	if info := d.r.Info(); info != nil {
		infoRef = c.alloc()
		fw.WriteObject(infoRef, c.copy(info))
	}
	for len(c.queue) > 0 {
		old := c.queue[0]
		c.queue = c.queue[1:]
		o, err := d.r.Object(old)
		if err != nil {
			o = nil
		}
		fw.WriteObject(c.mapping[old], c.copy(o))
	}
	kids := make(pdf.Array, len(newPages))
	for i, r := range newPages {
		kids[i] = r
	}
	fw.WriteObject(root, pdf.Dict{"Type": pdf.Name("Pages"), "Kids": kids, "Count": pdf.Integer(len(kids))})
	fw.WriteObject(catalog, pdf.Dict{"Type": pdf.Name("Catalog"), "Pages": root})
	trailer := pdf.Dict{"Size": pdf.Integer(c.next), "Root": catalog}
	if infoRef != (pdf.Ref{}) {
		trailer["Info"] = infoRef
	}
	fw.WriteXrefTable(trailer, true)
	return fw.Flush()
}

func boxArray(b [4]float64) pdf.Array {
	return pdf.Array{num(b[0]), num(b[1]), num(b[2]), num(b[3])}
}
