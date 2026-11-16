package sarekpdf

import (
	"context"
	"errors"
	"sync"

	"github.com/khanshahan/sarek_pdf/model"
)

// streamWindow is how many pages before and after a page are consulted
// for document-level analysis while streaming (running headers/footers,
// tables continued across pages).
const streamWindow = 12

// Stream extracts the given pages (all when empty) in parallel and calls
// fn with each page in document order, keeping only a bounded window of
// pages in memory. It is the low-memory alternative to Extract for large
// documents.
//
// Document-level analysis is applied within the window: running headers
// and footers are recognised from the surrounding pages, and tables
// continued across page breaks are linked as soon as both pages are
// available (Table.Prev/Next may point to a page not yet delivered).
// Stream does not use the page cache. fn is called from a single
// goroutine; returning an error stops the stream.
func (d *Document) Stream(ctx context.Context, pages []int, fn func(*model.Page) error) error {
	if len(pages) == 0 {
		pages = make([]int, len(d.pages))
		for i := range pages {
			pages[i] = i + 1
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		i    int
		page *model.Page
		err  error
	}
	workers := max(1, min(d.cfg.workers, len(pages)))
	// Bound the pages in flight: workers busy + lookahead window + slack.
	budget := make(chan struct{}, workers+2*streamWindow+2)
	jobs := make(chan int)
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				p, err := d.extractPage(ctx, pages[i])
				if err != nil && ctx.Err() == nil {
					wpx, hpx, _ := d.PageSize(pages[i])
					p = &model.Page{Number: pages[i], Width: wpx, Height: hpx}
				}
				select {
				case results <- result{i, p, err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := range pages {
			select {
			case budget <- struct{}{}:
			case <-ctx.Done():
				return
			}
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	var (
		buf      = map[int]*model.Page{} // extracted, not yet delivered
		keysTop  = map[int]map[string]bool{}
		keysBot  = map[int]map[string]bool{}
		errs     []error
		next     = 0 // next position to deliver
		linked   = 0 // pages[0..linked] have been linked with their successor
		received = 0
	)
	ready := func(k int) bool {
		_, ok := buf[k]
		return ok
	}
	deliver := func(final bool) error {
		for next < len(pages) && ready(next) {
			// Link tables across consecutive available pages.
			for linked+1 < len(pages) && ready(linked) && ready(linked+1) {
				linkContinuedTables([]*model.Page{buf[linked], buf[linked+1]})
				linked++
			}
			// Need the lookahead window (or the end of the document).
			last := min(len(pages)-1, next+streamWindow)
			for k := next + 1; k <= last; k++ {
				if !ready(k) && !final {
					return nil
				}
			}
			lo, hi := max(0, next-streamWindow), last
			top, bottom := map[string]int{}, map[string]int{}
			n := 0
			for k := lo; k <= hi; k++ {
				if keysTop[k] == nil {
					continue
				}
				n++
				for key := range keysTop[k] {
					top[key]++
				}
				for key := range keysBot[k] {
					bottom[key]++
				}
			}
			pg := buf[next]
			if n >= 3 {
				applyHeadersFooters(pg, top, bottom, max(3, n/2))
			}
			if err := fn(pg); err != nil {
				return err
			}
			delete(buf, next)
			delete(keysTop, next-streamWindow-1)
			delete(keysBot, next-streamWindow-1)
			next++
			<-budget
		}
		return nil
	}
	for r := range results {
		received++
		if r.err != nil {
			errs = append(errs, r.err)
		}
		if r.page == nil {
			continue
		}
		buf[r.i] = r.page
		keysTop[r.i], keysBot[r.i] = marginKeys(r.page)
		if err := deliver(received == len(pages)); err != nil {
			cancel()
			for range results {
			}
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := deliver(true); err != nil {
		return err
	}
	return errors.Join(errs...)
}
