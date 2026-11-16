// Package sarekpdf is a high-performance, dependency-free PDF extraction
// library: text, paragraphs, headings, tables, drawings and images, with
// search, highlighting, page extraction and DOCX/XML export.
//
// A *Document is safe for concurrent use by multiple goroutines.
//
//	doc, err := sarekpdf.Open("report.pdf")
//	if err != nil { ... }
//	defer doc.Close()
//	res, err := doc.Extract(ctx)
//
// See CONSTITUTION.md for the project's binding requirements.
package sarekpdf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/khanshahan/sarek_pdf/correct"
	"github.com/khanshahan/sarek_pdf/internal/content"
	"github.com/khanshahan/sarek_pdf/internal/font"
	"github.com/khanshahan/sarek_pdf/internal/pdf"
	"github.com/khanshahan/sarek_pdf/model"
	"github.com/khanshahan/sarek_pdf/ocr"
)

// ErrEncrypted is returned when the password is missing or wrong.
var ErrEncrypted = pdf.ErrEncrypted

// ErrTooLarge is returned when an input exceeds the configured size limit.
var ErrTooLarge = errors.New("sarekpdf: input exceeds size limit")

// Option configures a Document.
type Option func(*config)

type config struct {
	password      string
	workers       int
	corrector     correct.Corrector
	correctTables bool
	tables        bool
	drawings      bool
	images        bool
	imageData     bool
	cachePages    bool
	maxSize       int64
	httpClient    *http.Client
	rules         bool
	cidMaps       map[string]map[uint32]rune
	ocr           ocr.Engine
	ocrMode       ocr.Mode
	ocrMinConf    float64
}

func defaults() config {
	return config{
		workers: runtime.GOMAXPROCS(0), tables: true, drawings: true, images: true,
		imageData: true, cachePages: true, maxSize: 2 << 30, rules: true, ocrMinConf: 0.5,
		httpClient: &http.Client{Timeout: 10 * time.Minute},
	}
}

// WithPassword sets the user or owner password for encrypted files.
func WithPassword(p string) Option { return func(c *config) { c.password = p } }

// WithWorkers sets the number of pages processed in parallel
// (default GOMAXPROCS).
func WithWorkers(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.workers = n
		}
	}
}

// WithCorrector enables language-model correction (off by default).
// Every proposal is verified by correct.Verify; unverifiable changes are
// discarded and the original text is kept.
func WithCorrector(cr correct.Corrector) Option { return func(c *config) { c.corrector = cr } }

// WithCorrectTables also sends table-cell text to the corrector.
func WithCorrectTables(on bool) Option { return func(c *config) { c.correctTables = on } }

// WithOCR enables OCR with the given engine (off by default). In
// ocr.Auto mode OCR runs only on pages without a usable text layer.
// Every OCR span is marked model.SourceOCR with the engine's confidence,
// and Page.OCR describes what was done.
func WithOCR(e ocr.Engine, mode ocr.Mode) Option {
	return func(c *config) { c.ocr, c.ocrMode = e, mode }
}

// WithOCRMinConfidence drops OCR lines whose confidence is below v
// (default 0.5). Dropped lines are counted in Page.OCR.Errors.
func WithOCRMinConfidence(v float64) Option { return func(c *config) { c.ocrMinConf = v } }

// WithTables enables/disables table detection (default on).
func WithTables(on bool) Option { return func(c *config) { c.tables = on } }

// WithDrawings enables/disables vector drawing extraction (default on).
func WithDrawings(on bool) Option { return func(c *config) { c.drawings = on } }

// WithImages enables/disables raster image extraction (default on).
func WithImages(on bool) Option { return func(c *config) { c.images = on } }

// WithImageData controls whether image bytes are decoded (default on).
// When off, images carry only geometry and pixel dimensions.
func WithImageData(on bool) Option { return func(c *config) { c.imageData = on } }

// WithPageCache controls memoisation of extracted pages (default on).
// Disable for one-pass streaming over very large documents.
func WithPageCache(on bool) Option { return func(c *config) { c.cachePages = on } }

// WithNormalization controls deterministic text normalisation (ligature
// expansion, removal of zero-width characters). Default on.
func WithNormalization(on bool) Option { return func(c *config) { c.rules = on } }

// WithCIDToUnicode registers a CID→Unicode table for a CID collection
// such as "Adobe-Japan1", used for CJK fonts that lack a ToUnicode map and
// an embedded TrueType cmap. cmap is a Unicode→CID CMap resource (e.g.
// Adobe-Japan1-UCS2). No such resources ship with sarek_pdf; check the
// license of any resource you supply.
func WithCIDToUnicode(collection string, cmap []byte) Option {
	return func(c *config) {
		if c.cidMaps == nil {
			c.cidMaps = map[string]map[uint32]rune{}
		}
		c.cidMaps[collection] = font.ParseCIDToUnicode(cmap)
	}
}

// WithMaxSize limits the accepted input size in bytes (default 2 GiB).
func WithMaxSize(n int64) Option { return func(c *config) { c.maxSize = n } }

// WithHTTPClient sets the client used by OpenURL.
func WithHTTPClient(h *http.Client) Option { return func(c *config) { c.httpClient = h } }

// Document is an opened PDF. It is safe for concurrent use.
type Document struct {
	r     *pdf.Reader
	pages []pdf.Page
	fonts *font.Loader
	forms *content.FormCache
	cfg   config
	slots []pageSlot
}

type pageSlot struct {
	mu   sync.Mutex
	done bool
	page *model.Page
	err  error
}

// Open reads and opens a PDF file.
func Open(path string, opts ...Option) (*Document, error) {
	cfg := applyOpts(opts)
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > cfg.maxSize {
		return nil, ErrTooLarge
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return openBytes(data, cfg)
}

// OpenBytes opens a PDF held in memory. data must not be modified while
// the Document is in use.
func OpenBytes(data []byte, opts ...Option) (*Document, error) {
	cfg := applyOpts(opts)
	if int64(len(data)) > cfg.maxSize {
		return nil, ErrTooLarge
	}
	return openBytes(data, cfg)
}

// OpenReader reads all of r and opens it.
func OpenReader(r io.Reader, opts ...Option) (*Document, error) {
	cfg := applyOpts(opts)
	data, err := io.ReadAll(io.LimitReader(r, cfg.maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > cfg.maxSize {
		return nil, ErrTooLarge
	}
	return openBytes(data, cfg)
}

// OpenReaderAt reads a PDF of the given size from ra using parallel
// chunked reads (ra must allow concurrent ReadAt, as io.ReaderAt requires).
func OpenReaderAt(ra io.ReaderAt, size int64, opts ...Option) (*Document, error) {
	cfg := applyOpts(opts)
	if size > cfg.maxSize {
		return nil, ErrTooLarge
	}
	data := make([]byte, size)
	const chunk = 4 << 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	sem := make(chan struct{}, 8)
	for off := int64(0); off < size; off += chunk {
		end := min(off+chunk, size)
		wg.Add(1)
		sem <- struct{}{}
		go func(off, end int64) {
			defer func() { <-sem; wg.Done() }()
			if _, err := ra.ReadAt(data[off:end], off); err != nil && err != io.EOF {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(off, end)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return openBytes(data, cfg)
}

// OpenURL downloads a PDF over HTTP(S). When the server supports range
// requests, the file is fetched in parallel chunks.
func OpenURL(ctx context.Context, url string, opts ...Option) (*Document, error) {
	cfg := applyOpts(opts)
	hc := cfg.httpClient
	size, ranges := int64(-1), false
	if req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil); err == nil {
		if resp, err := hc.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode/100 == 2 {
				size = resp.ContentLength
				ranges = resp.Header.Get("Accept-Ranges") == "bytes"
			}
		}
	}
	if size > cfg.maxSize {
		return nil, ErrTooLarge
	}
	if ranges && size > 8<<20 {
		return OpenReaderAt(&httpReaderAt{ctx: ctx, url: url, client: hc}, size, opts...)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("sarekpdf: GET %s: %s", url, resp.Status)
	}
	return OpenReader(resp.Body, opts...)
}

type httpReaderAt struct {
	ctx    context.Context
	url    string
	client *http.Client
}

func (h *httpReaderAt) ReadAt(p []byte, off int64) (int, error) {
	req, err := http.NewRequestWithContext(h.ctx, http.MethodGet, h.url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", "bytes="+strconv.FormatInt(off, 10)+"-"+strconv.FormatInt(off+int64(len(p))-1, 10))
	resp, err := h.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("sarekpdf: range request failed: %s", resp.Status)
	}
	return io.ReadFull(resp.Body, p)
}

func applyOpts(opts []Option) config {
	c := defaults()
	for _, o := range opts {
		o(&c)
	}
	return c
}

func openBytes(data []byte, cfg config) (*Document, error) {
	r, err := pdf.NewReader(data, cfg.password)
	if err != nil {
		return nil, err
	}
	pages, err := r.Pages()
	if err != nil {
		return nil, err
	}
	fonts := font.NewLoader(r)
	fonts.CIDMaps = cfg.cidMaps
	return &Document{
		r: r, pages: pages, fonts: fonts, forms: content.NewFormCache(64 << 20),
		cfg: cfg, slots: make([]pageSlot, len(pages)),
	}, nil
}

// Close releases cached data. The Document must not be used afterwards.
func (d *Document) Close() error {
	d.slots = nil
	return nil
}

// NumPages returns the number of pages.
func (d *Document) NumPages() int { return len(d.pages) }

// Version returns the PDF version, e.g. "1.7".
func (d *Document) Version() string { return d.r.Version }

// Encrypted reports whether the file is encrypted.
func (d *Document) Encrypted() bool { return d.r.Encrypted() }

// PageSize returns the display width and height (points) of page n (1-based).
func (d *Document) PageSize(n int) (w, h float64, err error) {
	if n < 1 || n > len(d.pages) {
		return 0, 0, fmt.Errorf("sarekpdf: page %d out of range 1..%d", n, len(d.pages))
	}
	p := d.pages[n-1]
	_, w, h = content.PageMatrix(p.CropBox, p.Rotate)
	return w, h, nil
}

// Metadata returns the document information dictionary.
func (d *Document) Metadata() model.Metadata {
	info := d.r.Info()
	get := func(k pdf.Name) string { return pdf.TextString(d.r.Resolve(info[k])) }
	return model.Metadata{
		Title: get("Title"), Author: get("Author"), Subject: get("Subject"),
		Keywords: get("Keywords"), Creator: get("Creator"), Producer: get("Producer"),
		CreationDate: get("CreationDate"), ModDate: get("ModDate"),
	}
}
