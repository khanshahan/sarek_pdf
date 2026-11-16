// Command capi builds sarek_pdf as a C shared library for Python, .NET
// and other FFI hosts:
//
//	go build -buildmode=c-shared -o libsarekpdf.so    ./capi   (Linux)
//	go build -buildmode=c-shared -o libsarekpdf.dylib ./capi   (macOS)
//	go build -buildmode=c-shared -o sarekpdf.dll      ./capi   (Windows)
//
// ABI (all functions are thread-safe; strings are UTF-8, NUL-terminated):
//
//	int  sarek_open_file (const char* path, const char* options_json, uint64_t* handle, char** err);
//	int  sarek_open_bytes(const void* data, size_t len, const char* options_json, uint64_t* handle, char** err);
//	int  sarek_open_url  (const char* url,  const char* options_json, uint64_t* handle, char** err);
//	int  sarek_call(uint64_t handle, const char* op, const char* args_json, void** out, size_t* out_len, char** err);
//	void sarek_close(uint64_t handle);
//	void sarek_free(void* p);
//	const char* sarek_version(void);
//
// Functions return 0 on success. On failure *err receives a message that
// must be released with sarek_free. Output buffers must also be released
// with sarek_free.
//
// Open options (JSON): {"password","workers","tables","drawings","images",
// "llm": {"url","model","concurrency"},
// "ocr": {"models": "<dir>", "lang": "english", "mode": "auto|always|never",
//
//	"formulas": "<formula model dir>"}}
//
// OCR requires a library built with -tags onnx.
//
// Operations for sarek_call (args are JSON objects):
//
//	info        {}                                  → JSON
//	text        {"pages"}                           → UTF-8 text
//	xml         {"pages","geometry","embed_images"} → XML
//	docx        {"pages"}                           → DOCX bytes
//	markdown    {"pages","embed_images"}            → Markdown (images as data URIs when embed_images)
//	paragraphs  {"pages"}                           → JSON (with "source": "text-layer"|"ocr", "confidence")
//	headings    {"pages"}                           → JSON
//	formulas    {"pages"}                           → JSON (latex, mathml, text, number, bbox)
//	search      {"query","regex","case_sensitive","whole_word","pages","max_results"} → JSON
//	highlight   {"query",… as search, "color":"FFEB3B"} → PDF bytes
//	extract_pages {"pages"}                         → PDF bytes
package main

/*
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"unsafe"

	sarekpdf "github.com/khanshahan/sarek_pdf"
	"github.com/khanshahan/sarek_pdf/correct/llm"
	"github.com/khanshahan/sarek_pdf/correct/llm/openai"
	"github.com/khanshahan/sarek_pdf/export/docx"
	"github.com/khanshahan/sarek_pdf/export/markdown"
	"github.com/khanshahan/sarek_pdf/export/xmlexport"
	"github.com/khanshahan/sarek_pdf/model"
	"github.com/khanshahan/sarek_pdf/ocr"
	"github.com/khanshahan/sarek_pdf/ocr/mathocr"
	"github.com/khanshahan/sarek_pdf/ocr/paddle"
)

// Version is the library version reported by sarek_version.
const Version = "0.1.0"

var (
	handles sync.Map // uint64 -> *sarekpdf.Document
	nextID  atomic.Uint64
	cVer    = C.CString(Version)
)

type openOptions struct {
	Password string `json:"password"`
	Workers  int    `json:"workers"`
	Tables   *bool  `json:"tables"`
	Drawings *bool  `json:"drawings"`
	Images   *bool  `json:"images"`
	OCR      *struct {
		Models string `json:"models"`
		Lang   string `json:"lang"`
		Mode   string `json:"mode"`
		// Formulas: directory of the formula models (layout.onnx,
		// encoder_model.onnx, decoder_model.onnx, tokenizer.json).
		Formulas string `json:"formulas"`
	} `json:"ocr"`
	LLM *struct {
		URL         string `json:"url"`
		Model       string `json:"model"`
		Concurrency int    `json:"concurrency"`
	} `json:"llm"`
}

// ocrEngines caches loaded OCR engines per model directory and language;
// engines are safe for concurrent use and expensive to load.
var (
	ocrMu      sync.Mutex
	ocrEngines = map[string]*paddle.Engine{}
)

func ocrEngine(dir, lang string) (*paddle.Engine, error) {
	ocrMu.Lock()
	defer ocrMu.Unlock()
	key := dir + "\x00" + lang
	if e := ocrEngines[key]; e != nil {
		return e, nil
	}
	e, err := paddle.New(paddle.FromDir(dir, lang))
	if err != nil {
		return nil, err
	}
	ocrEngines[key] = e
	return e, nil
}

var formulaEngines = map[string]*mathocr.Engine{}

func formulaEngine(dir string) (*mathocr.Engine, error) {
	ocrMu.Lock()
	defer ocrMu.Unlock()
	if e := formulaEngines[dir]; e != nil {
		return e, nil
	}
	e, err := mathocr.New(mathocr.FromDir(dir))
	if err != nil {
		return nil, err
	}
	formulaEngines[dir] = e
	return e, nil
}

func (o openOptions) toOptions() ([]sarekpdf.Option, error) {
	opts := []sarekpdf.Option{sarekpdf.WithPassword(o.Password), sarekpdf.WithWorkers(o.Workers)}
	if o.Tables != nil {
		opts = append(opts, sarekpdf.WithTables(*o.Tables))
	}
	if o.Drawings != nil {
		opts = append(opts, sarekpdf.WithDrawings(*o.Drawings))
	}
	if o.Images != nil {
		opts = append(opts, sarekpdf.WithImages(*o.Images))
	}
	if o.LLM != nil && o.LLM.URL != "" {
		opts = append(opts, sarekpdf.WithCorrector(&llm.Corrector{
			Engine: openai.New(o.LLM.URL, o.LLM.Model), Concurrency: max(1, o.LLM.Concurrency)}))
	}
	if o.OCR != nil {
		mode := map[string]ocr.Mode{"": ocr.Auto, "auto": ocr.Auto, "always": ocr.Always, "never": ocr.Never}[o.OCR.Mode]
		var textEng ocr.Engine
		if o.OCR.Models != "" {
			eng, err := ocrEngine(o.OCR.Models, o.OCR.Lang)
			if err != nil {
				return nil, err
			}
			textEng = eng
		}
		if textEng != nil || o.OCR.Formulas != "" {
			opts = append(opts, sarekpdf.WithOCR(textEng, mode))
		}
		if o.OCR.Formulas != "" {
			eng, err := formulaEngine(o.OCR.Formulas)
			if err != nil {
				return nil, err
			}
			opts = append(opts, sarekpdf.WithFormulaOCR(eng))
		}
	}
	return opts, nil
}

func parseOpen(cjson *C.char) ([]sarekpdf.Option, error) {
	var o openOptions
	if cjson != nil {
		if s := C.GoString(cjson); s != "" {
			if err := json.Unmarshal([]byte(s), &o); err != nil {
				return nil, fmt.Errorf("options: %w", err)
			}
		}
	}
	return o.toOptions()
}

func setErr(errOut **C.char, err error) C.int {
	if errOut != nil {
		*errOut = C.CString(err.Error())
	}
	return 1
}

func register(doc *sarekpdf.Document, handle *C.uint64_t) {
	id := nextID.Add(1)
	handles.Store(id, doc)
	*handle = C.uint64_t(id)
}

// guard converts panics into errors so a bad input can never take down
// the host process.
func guard(errOut **C.char, rc *C.int) {
	if r := recover(); r != nil {
		*rc = setErr(errOut, fmt.Errorf("internal error: %v\n%s", r, debug.Stack()))
	}
}

//export sarek_version
func sarek_version() *C.char { return cVer }

//export sarek_free
func sarek_free(p unsafe.Pointer) { C.free(p) }

//export sarek_open_file
func sarek_open_file(path *C.char, opts *C.char, handle *C.uint64_t, errOut **C.char) (rc C.int) {
	defer guard(errOut, &rc)
	o, err := parseOpen(opts)
	if err != nil {
		return setErr(errOut, err)
	}
	doc, err := sarekpdf.Open(C.GoString(path), o...)
	if err != nil {
		return setErr(errOut, err)
	}
	register(doc, handle)
	return 0
}

//export sarek_open_bytes
func sarek_open_bytes(data unsafe.Pointer, n C.size_t, opts *C.char, handle *C.uint64_t, errOut **C.char) (rc C.int) {
	defer guard(errOut, &rc)
	o, err := parseOpen(opts)
	if err != nil {
		return setErr(errOut, err)
	}
	// Copy: the caller may free its buffer after the call returns.
	buf := make([]byte, int(n))
	if n > 0 {
		copy(buf, unsafe.Slice((*byte)(data), int(n)))
	}
	doc, err := sarekpdf.OpenBytes(buf, o...)
	if err != nil {
		return setErr(errOut, err)
	}
	register(doc, handle)
	return 0
}

//export sarek_open_url
func sarek_open_url(url *C.char, opts *C.char, handle *C.uint64_t, errOut **C.char) (rc C.int) {
	defer guard(errOut, &rc)
	o, err := parseOpen(opts)
	if err != nil {
		return setErr(errOut, err)
	}
	doc, err := sarekpdf.OpenURL(context.Background(), C.GoString(url), o...)
	if err != nil {
		return setErr(errOut, err)
	}
	register(doc, handle)
	return 0
}

//export sarek_close
func sarek_close(handle C.uint64_t) {
	if v, ok := handles.LoadAndDelete(uint64(handle)); ok {
		v.(*sarekpdf.Document).Close()
	}
}

type callArgs struct {
	Pages         string `json:"pages"`
	Query         string `json:"query"`
	Regex         bool   `json:"regex"`
	CaseSensitive bool   `json:"case_sensitive"`
	WholeWord     bool   `json:"whole_word"`
	MaxResults    int    `json:"max_results"`
	Color         string `json:"color"`
	Geometry      *bool  `json:"geometry"`
	EmbedImages   bool   `json:"embed_images"`
}

//export sarek_call
func sarek_call(handle C.uint64_t, op *C.char, args *C.char, out *unsafe.Pointer, outLen *C.size_t, errOut **C.char) (rc C.int) {
	defer guard(errOut, &rc)
	v, ok := handles.Load(uint64(handle))
	if !ok {
		return setErr(errOut, errors.New("invalid document handle"))
	}
	var a callArgs
	if args != nil {
		if s := C.GoString(args); s != "" {
			if err := json.Unmarshal([]byte(s), &a); err != nil {
				return setErr(errOut, fmt.Errorf("args: %w", err))
			}
		}
	}
	res, err := call(v.(*sarekpdf.Document), C.GoString(op), a)
	if err != nil {
		return setErr(errOut, err)
	}
	p := C.malloc(C.size_t(max(len(res), 1)))
	if len(res) > 0 {
		C.memcpy(p, unsafe.Pointer(&res[0]), C.size_t(len(res)))
	}
	*out = p
	*outLen = C.size_t(len(res))
	return 0
}

type jsonRect struct{ X0, Y0, X1, Y1 float64 }

func rects(rs []model.Rect) []jsonRect {
	out := make([]jsonRect, len(rs))
	for i, r := range rs {
		out[i] = jsonRect{r.X0, r.Y0, r.X1, r.Y1}
	}
	return out
}

func call(doc *sarekpdf.Document, op string, a callArgs) ([]byte, error) {
	ctx := context.Background()
	pages, err := sarekpdf.ParsePageRanges(a.Pages, doc.NumPages())
	if err != nil {
		return nil, err
	}
	searchOpts := sarekpdf.SearchOptions{Regexp: a.Regex, CaseSensitive: a.CaseSensitive,
		WholeWord: a.WholeWord, Pages: pages, MaxResults: a.MaxResults}
	switch op {
	case "info":
		return json.Marshal(map[string]any{"pages": doc.NumPages(), "version": doc.Version(),
			"encrypted": doc.Encrypted(), "metadata": doc.Metadata(), "outline": doc.Outline()})
	case "text":
		res, err := doc.Extract(ctx, pages...)
		if res == nil {
			return nil, err
		}
		return []byte(res.TextWithNotices()), err
	case "markdown":
		res, err := doc.Extract(ctx, pages...)
		if res == nil {
			return nil, err
		}
		o := markdown.DefaultOptions()
		o.EmbedImages = a.EmbedImages
		var buf bytes.Buffer
		err = markdown.Write(&buf, res, o)
		return buf.Bytes(), err
	case "xml", "docx":
		res, err := doc.Extract(ctx, pages...)
		if res == nil {
			return nil, err
		}
		var buf bytes.Buffer
		if op == "xml" {
			o := xmlexport.DefaultOptions()
			if a.Geometry != nil {
				o.Geometry = *a.Geometry
			}
			o.EmbedImages = a.EmbedImages
			err = xmlexport.Write(&buf, res, o)
		} else {
			err = docx.Write(&buf, res, docx.Options{})
		}
		return buf.Bytes(), err
	case "paragraphs":
		ps, err := doc.Paragraphs(ctx, pages...)
		if ps == nil && err != nil {
			return nil, err
		}
		type jp struct {
			Page     int      `json:"page"`
			Role     string   `json:"role"`
			Level    int      `json:"level,omitempty"`
			Text     string   `json:"text"`
			Original string   `json:"original,omitempty"`
			BBox     jsonRect `json:"bbox"`
			Source   string   `json:"source"`
			Conf     float64  `json:"confidence"`
		}
		out := make([]jp, 0, len(ps))
		for _, p := range ps {
			b := p.Paragraph.BBox
			out = append(out, jp{p.Page, p.Paragraph.Role.String(), p.Paragraph.Level, p.Paragraph.Text(),
				p.Paragraph.Original, jsonRect{b.X0, b.Y0, b.X1, b.Y1}, p.Paragraph.Source().String(), p.Paragraph.Confidence()})
		}
		return json.Marshal(out)
	case "formulas":
		res, err := doc.Extract(ctx, pages...)
		if res == nil {
			return nil, err
		}
		type jf struct {
			Page   int      `json:"page"`
			LaTeX  string   `json:"latex"`
			MathML string   `json:"mathml"`
			Text   string   `json:"text"`
			Number string   `json:"number"`
			BBox   jsonRect `json:"bbox"`
		}
		out := []jf{}
		for _, pg := range res.Pages {
			for _, b := range pg.Blocks {
				if f, ok := b.(*model.Formula); ok {
					bb := f.BBox
					out = append(out, jf{pg.Number, f.LaTeX(), f.MathML(), f.Text(), f.Number, jsonRect{bb.X0, bb.Y0, bb.X1, bb.Y1}})
				}
			}
		}
		return json.Marshal(out)
	case "headings":
		hs, err := doc.Headings(ctx, pages...)
		if hs == nil && err != nil {
			return nil, err
		}
		type jh struct {
			Level int      `json:"level"`
			Text  string   `json:"text"`
			Page  int      `json:"page"`
			BBox  jsonRect `json:"bbox"`
		}
		out := make([]jh, 0, len(hs))
		for _, h := range hs {
			out = append(out, jh{h.Level, h.Text, h.Page, jsonRect{h.BBox.X0, h.BBox.Y0, h.BBox.X1, h.BBox.Y1}})
		}
		return json.Marshal(out)
	case "search", "highlight":
		ms, err := doc.Search(ctx, a.Query, searchOpts)
		if err != nil && ms == nil {
			return nil, err
		}
		if op == "search" {
			type jm struct {
				Page   int        `json:"page"`
				Text   string     `json:"text"`
				Rects  []jsonRect `json:"rects"`
				Source string     `json:"source"`
			}
			out := make([]jm, 0, len(ms))
			for _, m := range ms {
				out = append(out, jm{m.Page, m.Text, rects(m.Rects), m.Source.String()})
			}
			return json.Marshal(out)
		}
		var c model.Color
		if b, err := hex.DecodeString(a.Color); err == nil && len(b) == 3 {
			c = model.Color{R: b[0], G: b[1], B: b[2]}
		}
		var buf bytes.Buffer
		err = doc.WriteHighlighted(&buf, sarekpdf.HighlightMatches(ms, c))
		return buf.Bytes(), err
	case "extract_pages":
		var buf bytes.Buffer
		err := doc.ExtractPages(&buf, pages)
		return buf.Bytes(), err
	}
	return nil, fmt.Errorf("unknown operation %q", op)
}

func main() {}
