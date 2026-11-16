package sarekpdf

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/khanshahan/sarek_pdf/internal/textnorm"
	"github.com/khanshahan/sarek_pdf/model"
)

// SearchOptions controls Search.
type SearchOptions struct {
	CaseSensitive bool
	WholeWord     bool
	Regexp        bool // treat the query as a Go regular expression (RE2)
	Pages         []int
	MaxResults    int // 0 = unlimited
}

// Match is a search hit.
type Match struct {
	Page  int
	Text  string       // matched text as it appears in the PDF
	Rects []model.Rect // one rectangle per line the match touches
	// Source is model.SourceOCR when any matched character was produced
	// by OCR rather than read from the PDF text layer.
	Source model.Source
}

// pos locates one searchable rune on a page.
type pos struct {
	line   *model.Line
	x0, x1 float64
	ocr    bool
}

// pageIndex is the searchable text of one page with rune geometry.
type pageIndex struct {
	text []rune
	pos  []pos // same length as text; line == nil for synthetic separators
}

func buildIndex(pg *model.Page) *pageIndex {
	idx := &pageIndex{}
	addLine := func(l *model.Line) {
		if len(idx.text) > 0 {
			idx.text = append(idx.text, ' ')
			idx.pos = append(idx.pos, pos{})
		}
		for _, sp := range l.Spans {
			i := 0
			for _, r := range sp.Text {
				x0, x1 := sp.BBox.X0, sp.BBox.X1
				if i+1 < len(sp.Edges) {
					x0, x1 = min(sp.Edges[i], sp.Edges[i+1]), max(sp.Edges[i], sp.Edges[i+1])
				}
				i++
				// Expand ligatures ("fi" matches "ﬁ") and compatibility forms.
				exp, ok := ligatures[r]
				if !ok {
					exp = textnorm.Expand(r)
					ok = exp != nil
				}
				if ok {
					w := (x1 - x0) / float64(len(exp))
					for k, er := range exp {
						idx.text = append(idx.text, er)
						idx.pos = append(idx.pos, pos{line: l, x0: x0 + w*float64(k), x1: x0 + w*float64(k+1), ocr: sp.Source == model.SourceOCR})
					}
					continue
				}
				if unicode.IsSpace(r) {
					r = ' '
				}
				idx.text = append(idx.text, r)
				idx.pos = append(idx.pos, pos{line: l, x0: x0, x1: x1, ocr: sp.Source == model.SourceOCR})
			}
		}
	}
	var walkPara func(p *model.Paragraph)
	walkPara = func(p *model.Paragraph) {
		for i := range p.Lines {
			addLine(&p.Lines[i])
		}
	}
	for _, b := range pg.Blocks {
		switch v := b.(type) {
		case *model.Paragraph:
			walkPara(v)
		case *model.Table:
			for _, c := range v.Cells {
				for _, p := range c.Paragraphs {
					walkPara(p)
				}
			}
		case *model.Drawing:
			for i := range v.Labels {
				addLine(&v.Labels[i])
			}
		}
	}
	return idx
}

var ligatures = map[rune][]rune{
	'ﬀ': []rune("ff"), 'ﬁ': []rune("fi"), 'ﬂ': []rune("fl"),
	'ﬃ': []rune("ffi"), 'ﬄ': []rune("ffl"), 'ﬆ': []rune("st"),
}

// Search finds query in the document text and returns page numbers and
// match rectangles (suitable for Highlight). Matches may span line breaks.
func (d *Document) Search(ctx context.Context, query string, opts SearchOptions) ([]Match, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("sarekpdf: empty search query")
	}
	expr := query
	if !opts.Regexp {
		// Whitespace in the query matches any run of whitespace.
		parts := strings.Fields(query)
		for i, p := range parts {
			parts[i] = regexp.QuoteMeta(p)
		}
		expr = strings.Join(parts, `\s+`)
	}
	if opts.WholeWord {
		expr = `\b(?:` + expr + `)\b`
	}
	if !opts.CaseSensitive {
		expr = `(?i)` + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	pages := opts.Pages
	if len(pages) == 0 {
		for i := 1; i <= d.NumPages(); i++ {
			pages = append(pages, i)
		}
	}
	results := make([][]Match, len(pages))
	errs := make([]error, len(pages))
	var wg sync.WaitGroup
	sem := make(chan struct{}, d.cfg.workers)
	for i, n := range pages {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i, n int) {
			defer func() { <-sem; wg.Done() }()
			pg, err := d.Page(ctx, n)
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = searchPage(pg, re)
		}(i, n)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []Match
	for _, r := range results {
		for _, m := range r {
			if opts.MaxResults > 0 && len(out) >= opts.MaxResults {
				return out, errors.Join(errs...)
			}
			out = append(out, m)
		}
	}
	return out, errors.Join(errs...)
}

func searchPage(pg *model.Page, re *regexp.Regexp) []Match {
	idx := buildIndex(pg)
	if len(idx.text) == 0 {
		return nil
	}
	s := string(idx.text)
	// Map byte offsets to rune indices.
	byteToRune := make([]int, len(s)+1)
	ri := 0
	for bi := range s {
		byteToRune[bi] = ri
		ri++
	}
	byteToRune[len(s)] = ri
	for bi := len(s) - 1; bi >= 0; bi-- {
		if !utf8.RuneStart(s[bi]) {
			byteToRune[bi] = byteToRune[bi+1]
		}
	}
	var out []Match
	for _, loc := range re.FindAllStringIndex(s, -1) {
		a, b := byteToRune[loc[0]], byteToRune[loc[1]]
		if a >= b {
			continue
		}
		m := Match{Page: pg.Number, Text: string(idx.text[a:b])}
		var cur *model.Line
		var rect model.Rect
		for k := a; k < b; k++ {
			p := idx.pos[k]
			if p.line == nil {
				continue
			}
			if p.ocr {
				m.Source = model.SourceOCR
			}
			if p.line != cur {
				if cur != nil {
					m.Rects = append(m.Rects, rect)
				}
				cur = p.line
				rect = model.Rect{X0: p.x0, Y0: cur.BBox.Y0, X1: p.x1, Y1: cur.BBox.Y1}
				continue
			}
			rect.X0, rect.X1 = min(rect.X0, p.x0), max(rect.X1, p.x1)
		}
		if cur != nil {
			m.Rects = append(m.Rects, rect)
		}
		out = append(out, m)
	}
	return out
}
