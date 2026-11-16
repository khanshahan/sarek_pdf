package pdf

import (
	"errors"
	"unicode/utf16"
	"unicode/utf8"
)

// Page is a resolved leaf of the page tree with inherited attributes
// already applied.
type Page struct {
	Ref       Ref
	Dict      Dict
	Resources Dict
	MediaBox  [4]float64 // llx, lly, urx, ury (normalised)
	CropBox   [4]float64
	Rotate    int // 0, 90, 180, 270
}

// Pages walks the page tree and returns all pages in order.
func (r *Reader) Pages() ([]Page, error) {
	cat := r.Catalog()
	if cat == nil {
		return nil, errors.New("pdf: missing catalog")
	}
	root, rootRef := cat["Pages"], Ref{}
	if ref, ok := root.(Ref); ok {
		rootRef = ref
	}
	var pages []Page
	seen := map[Ref]bool{}
	var walk func(node Object, ref Ref, inh Dict, depth int)
	walk = func(node Object, ref Ref, inh Dict, depth int) {
		if depth > 64 {
			return
		}
		if ref != (Ref{}) {
			if seen[ref] {
				return
			}
			seen[ref] = true
		}
		d := r.Dict(node)
		if d == nil {
			return
		}
		next := Dict{}
		for k, v := range inh {
			next[k] = v
		}
		for _, k := range []Name{"Resources", "MediaBox", "CropBox", "Rotate"} {
			if v, ok := d[k]; ok {
				next[k] = v
			}
		}
		kids, hasKids := r.Resolve(d["Kids"]).(Array)
		typ := r.Name(d["Type"])
		if typ == "Page" || (!hasKids && typ != "Pages") {
			pages = append(pages, r.makePage(ref, d, next))
			return
		}
		for _, k := range kids {
			kr, _ := k.(Ref)
			walk(k, kr, next, depth+1)
		}
	}
	walk(root, rootRef, Dict{}, 0)
	if len(pages) == 0 {
		return nil, errors.New("pdf: document has no pages")
	}
	return pages, nil
}

func (r *Reader) makePage(ref Ref, d, inh Dict) Page {
	p := Page{Ref: ref, Dict: d, Resources: r.Dict(inh["Resources"])}
	p.MediaBox = r.box(inh["MediaBox"], [4]float64{0, 0, 612, 792})
	p.CropBox = r.box(inh["CropBox"], p.MediaBox)
	// Crop box is clipped to the media box.
	p.CropBox[0] = max(p.CropBox[0], p.MediaBox[0])
	p.CropBox[1] = max(p.CropBox[1], p.MediaBox[1])
	p.CropBox[2] = min(p.CropBox[2], p.MediaBox[2])
	p.CropBox[3] = min(p.CropBox[3], p.MediaBox[3])
	if p.CropBox[2]-p.CropBox[0] < 1 || p.CropBox[3]-p.CropBox[1] < 1 {
		p.CropBox = p.MediaBox
	}
	rot := r.Int(inh["Rotate"], 0) % 360
	if rot < 0 {
		rot += 360
	}
	p.Rotate = rot / 90 * 90
	return p
}

func (r *Reader) box(o Object, def [4]float64) [4]float64 {
	n := r.Numbers(o)
	if len(n) != 4 {
		return def
	}
	b := [4]float64{min(n[0], n[2]), min(n[1], n[3]), max(n[0], n[2]), max(n[1], n[3])}
	if b[2]-b[0] < 1 || b[3]-b[1] < 1 {
		return def
	}
	return b
}

// Contents returns the concatenated, decoded content streams of a page.
func (r *Reader) Contents(p Page) ([]byte, error) {
	switch c := r.Resolve(p.Dict["Contents"]).(type) {
	case *Stream:
		return r.Decode(c)
	case Array:
		var out []byte
		var firstErr error
		for _, e := range c {
			s := r.Stream(e)
			if s == nil {
				continue
			}
			b, err := r.Decode(s)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			out = append(out, b...)
			out = append(out, '\n') // streams may split tokens only at whitespace
		}
		return out, firstErr
	}
	return nil, nil
}

// TextString decodes a PDF text string (PDFDocEncoding, UTF-16BE/LE with
// BOM, or UTF-8 with BOM) into a Go string.
func TextString(o Object) string {
	s, ok := o.(String)
	if !ok {
		if n, ok := o.(Name); ok {
			return string(n)
		}
		return ""
	}
	b := []byte(s)
	switch {
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return decodeUTF16(b[2:], true)
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return decodeUTF16(b[2:], false)
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return string(b[3:])
	}
	out := make([]rune, 0, len(b))
	for _, c := range b {
		out = append(out, PDFDocRune(c))
	}
	return string(out)
}

func decodeUTF16(b []byte, be bool) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if be {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			u = append(u, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	return string(utf16.Decode(u))
}

// pdfDocHigh maps PDFDocEncoding 0x80–0xA0 (Annex D.2).
var pdfDocHigh = [...]rune{
	0x2022, 0x2020, 0x2021, 0x2026, 0x2014, 0x2013, 0x0192, 0x2044,
	0x2039, 0x203A, 0x2212, 0x2030, 0x201E, 0x201C, 0x201D, 0x2018,
	0x2019, 0x201A, 0x2122, 0xFB01, 0xFB02, 0x0141, 0x0152, 0x0160,
	0x0178, 0x017D, 0x0131, 0x0142, 0x0153, 0x0161, 0x017E, utf8.RuneError,
	0x20AC,
}

// PDFDocRune maps a PDFDocEncoding byte to a rune.
func PDFDocRune(c byte) rune {
	switch {
	case c >= 0x80 && c <= 0xA0:
		return pdfDocHigh[c-0x80]
	case c == 0x18:
		return 0x02D8
	case c == 0x19:
		return 0x02C7
	case c == 0x1A:
		return 0x02C6
	case c == 0x1B:
		return 0x02D9
	case c == 0x1C:
		return 0x02DD
	case c == 0x1D:
		return 0x02DB
	case c == 0x1E:
		return 0x02DA
	case c == 0x1F:
		return 0x02DC
	}
	return rune(c)
}
