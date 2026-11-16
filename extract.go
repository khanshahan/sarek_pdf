package sarekpdf

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/khanshahan/sarek_pdf/correct"
	"github.com/khanshahan/sarek_pdf/internal/content"
	"github.com/khanshahan/sarek_pdf/internal/images"
	"github.com/khanshahan/sarek_pdf/internal/layout"
	"github.com/khanshahan/sarek_pdf/model"
)

var glyphPool = sync.Pool{New: func() any { b := make([]content.Glyph, 0, 4096); return &b }}

// Page extracts (or returns the cached) page n, 1-based.
func (d *Document) Page(ctx context.Context, n int) (*model.Page, error) {
	if n < 1 || n > len(d.pages) {
		return nil, fmt.Errorf("sarekpdf: page %d out of range 1..%d", n, len(d.pages))
	}
	if !d.cfg.cachePages {
		return d.extractPage(ctx, n)
	}
	s := &d.slots[n-1]
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return s.page, s.err
	}
	page, err := d.extractPage(ctx, n)
	if ctx.Err() != nil {
		return nil, ctx.Err() // never cache cancellation
	}
	s.page, s.err, s.done = page, err, true
	return page, err
}

func (d *Document) extractPage(ctx context.Context, n int) (pg *model.Page, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		// Hostile input must never crash the host process.
		if r := recover(); r != nil {
			pg, err = nil, fmt.Errorf("sarekpdf: page %d: internal error: %v", n, r)
		}
	}()
	p := d.pages[n-1]
	gb := glyphPool.Get().(*[]content.Glyph)
	in := content.NewInterpreter(d.r, d.fonts, d.forms, content.Options{
		Images: d.cfg.images, Paths: d.cfg.tables || d.cfg.drawings,
	}, *gb)
	res, ierr := in.RunPage(ctx, p)
	if res == nil {
		return nil, ierr
	}
	var imgs []*model.Image
	if d.cfg.images {
		for _, ip := range res.Images {
			bb := ip.BBox
			if bb.W() < 2 || bb.H() < 2 || bb.X1 < 0 || bb.Y1 < 0 || bb.X0 > res.Width || bb.Y0 > res.Height {
				continue
			}
			imgs = append(imgs, images.Convert(d.r, ip.Stream, bb, d.cfg.imageData))
		}
	}
	lopts := layout.Options{Tables: d.cfg.tables, Drawings: d.cfg.drawings}
	var ocrInfo *model.OCRInfo
	if reason := d.needOCR(res); reason != "" {
		var used map[*content.ImagePlacement]bool
		lopts.OCR, ocrInfo, used = d.runOCR(ctx, res, reason)
		for i, ip := range res.Images {
			if used[&res.Images[i]] {
				for _, im := range imgs {
					if im.BBox == ip.BBox {
						im.OCRSource = true
					}
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	blocks := layout.Analyze(res, imgs, lopts)
	// Must run before the glyph buffer is recycled below.
	noTextLayer := (ocrInfo == nil || ocrInfo.Lines == 0) && textLayerProblem(res) != ""
	// Layout copies everything it keeps; recycle the glyph buffer.
	if cap(res.Glyphs) <= 1<<20 {
		clear(res.Glyphs)
		*gb = res.Glyphs[:0]
		glyphPool.Put(gb)
	}
	page := &model.Page{Number: n, Width: res.Width, Height: res.Height, Blocks: blocks, OCR: ocrInfo}
	page.NoTextLayer = noTextLayer
	if d.cfg.rules {
		normalizePage(page)
	}
	if d.cfg.corrector != nil {
		if err := d.correctPage(ctx, page); err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return page, nil
}

func forEachParagraph(page *model.Page, tables bool, fn func(p *model.Paragraph)) {
	for _, b := range page.Blocks {
		switch v := b.(type) {
		case *model.Paragraph:
			fn(v)
		case *model.Table:
			if tables {
				for _, c := range v.Cells {
					for _, p := range c.Paragraphs {
						fn(p)
					}
				}
			}
		}
	}
}

func normalizePage(page *model.Page) {
	var rules correct.Rules
	forEachParagraph(page, true, func(p *model.Paragraph) {
		for i := range p.Runs {
			p.Runs[i].Text = rules.Apply(p.Runs[i].Text)
		}
	})
}

// correctPage sends paragraphs to the corrector and applies only
// verified changes, re-mapping run styles word by word.
func (d *Document) correctPage(ctx context.Context, page *model.Page) error {
	var ps []*model.Paragraph
	var texts []string
	forEachParagraph(page, d.cfg.correctTables, func(p *model.Paragraph) {
		ps = append(ps, p)
		texts = append(texts, p.Text())
	})
	if len(ps) == 0 {
		return nil
	}
	out, err := d.cfg.corrector.Correct(ctx, texts)
	for i, p := range ps {
		if i >= len(out) || out[i] == texts[i] {
			continue
		}
		align, ok := correct.Verify(texts[i], out[i])
		if !ok {
			continue
		}
		p.Original = texts[i]
		p.Runs = remapRuns(p.Runs, out[i], align)
	}
	return err
}

// remapRuns rebuilds styled runs for corrected text: each corrected word
// takes the style of the original word it aligns to.
func remapRuns(runs []model.Run, corrected string, align []int) []model.Run {
	var styles []model.Style
	for _, r := range runs {
		words := strings.Fields(r.Text)
		// A run that starts mid-word continues the previous word.
		cont := len(styles) > 0 && len(r.Text) > 0 && r.Text[0] != ' '
		for k := range words {
			if k == 0 && cont {
				continue
			}
			styles = append(styles, r.Style)
		}
	}
	var out []model.Run
	for i, w := range strings.Fields(corrected) {
		st := model.Style{}
		if i < len(align) && align[i] < len(styles) {
			st = styles[align[i]]
		} else if len(styles) > 0 {
			st = styles[len(styles)-1]
		}
		text := w
		if i > 0 {
			text = " " + w
		}
		if n := len(out); n > 0 && out[n-1].Style == st {
			out[n-1].Text += text
		} else {
			out = append(out, model.Run{Text: text, Style: st})
		}
	}
	return out
}

// Extract extracts the given pages (1-based; all pages when none are
// given) in parallel. Pages that fail are returned empty and their errors
// joined into the returned error; the Document is always non-nil unless
// the context is cancelled.
func (d *Document) Extract(ctx context.Context, pages ...int) (*model.Document, error) {
	if len(pages) == 0 {
		pages = make([]int, len(d.pages))
		for i := range pages {
			pages[i] = i + 1
		}
	}
	out := &model.Document{Version: d.Version(), Metadata: d.Metadata(), Outline: d.Outline(),
		Pages: make([]*model.Page, len(pages))}
	errs := make([]error, len(pages))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < min(d.cfg.workers, len(pages)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				p, err := d.Page(ctx, pages[i])
				if err != nil {
					errs[i] = err
					w, h, _ := d.PageSize(pages[i])
					p = &model.Page{Number: pages[i], Width: w, Height: h}
				}
				out.Pages[i] = p
			}
		}()
	}
feed:
	for i := range pages {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	markHeadersFooters(out.Pages)
	return out, errors.Join(errs...)
}

var digitsRe = regexp.MustCompile(`\d+`)

// markHeadersFooters tags paragraphs repeated near the top or bottom of
// most pages (page numbers, running titles). Text is never removed.
func markHeadersFooters(pages []*model.Page) {
	if len(pages) < 3 {
		return
	}
	key := func(p *model.Paragraph) string { return digitsRe.ReplaceAllString(strings.TrimSpace(p.Text()), "#") }
	top, bottom := map[string]int{}, map[string]int{}
	zone := func(pg *model.Page, p *model.Paragraph) int {
		switch {
		case p.BBox.Y1 < 0.1*pg.Height:
			return -1
		case p.BBox.Y0 > 0.9*pg.Height:
			return 1
		}
		return 0
	}
	for _, pg := range pages {
		seenT, seenB := map[string]bool{}, map[string]bool{}
		for _, b := range pg.Blocks {
			p, ok := b.(*model.Paragraph)
			if !ok || len(p.Lines) > 2 {
				continue
			}
			k := key(p)
			switch zone(pg, p) {
			case -1:
				if !seenT[k] {
					top[k]++
					seenT[k] = true
				}
			case 1:
				if !seenB[k] {
					bottom[k]++
					seenB[k] = true
				}
			}
		}
	}
	need := max(3, len(pages)/2)
	for _, pg := range pages {
		for _, b := range pg.Blocks {
			p, ok := b.(*model.Paragraph)
			if !ok || len(p.Lines) > 2 {
				continue
			}
			k := key(p)
			switch zone(pg, p) {
			case -1:
				if top[k] >= need {
					p.Role, p.Level = model.RolePageHeader, 0
				}
			case 1:
				if bottom[k] >= need {
					p.Role, p.Level = model.RolePageFooter, 0
				}
			}
		}
	}
}

// Text returns the full text of the given pages (all when none given).
func (d *Document) Text(ctx context.Context, pages ...int) (string, error) {
	doc, err := d.Extract(ctx, pages...)
	if doc == nil {
		return "", err
	}
	return doc.Text(), err
}

// ParagraphRef is a paragraph together with its page number.
type ParagraphRef struct {
	Page      int
	Paragraph *model.Paragraph
}

// Paragraphs returns all body paragraphs (including headings and list
// items; excluding table cells) in reading order.
func (d *Document) Paragraphs(ctx context.Context, pages ...int) ([]ParagraphRef, error) {
	doc, err := d.Extract(ctx, pages...)
	if doc == nil {
		return nil, err
	}
	var out []ParagraphRef
	for _, pg := range doc.Pages {
		for _, p := range pg.Paragraphs(false) {
			out = append(out, ParagraphRef{Page: pg.Number, Paragraph: p})
		}
	}
	return out, err
}

// Heading is a detected heading.
type Heading struct {
	Level int
	Text  string
	Page  int
	BBox  model.Rect
}

// Headings returns headings detected from the page layout (font size and
// weight). For author-defined bookmarks use Outline.
func (d *Document) Headings(ctx context.Context, pages ...int) ([]Heading, error) {
	doc, err := d.Extract(ctx, pages...)
	if doc == nil {
		return nil, err
	}
	var out []Heading
	for _, pg := range doc.Pages {
		for _, p := range pg.Paragraphs(false) {
			if p.Role == model.RoleHeading {
				out = append(out, Heading{Level: p.Level, Text: p.Text(), Page: pg.Number, BBox: p.BBox})
			}
		}
	}
	return out, err
}
