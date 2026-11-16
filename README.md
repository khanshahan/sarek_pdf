# sarek_pdf

High-performance, dependency-free PDF extraction for Go, Python and .NET.

* **Text, paragraphs, headings, lists** in reading order (1, 2, 3+ columns, RTL, CJK)
* **OCR fallback** for scanned pages (pluggable engine; OCR text is always marked as OCR)
* **Tables** with structure (ruled grids incl. merged cells, borderless and rule-delimited tables)
* **Drawings** (vector paths → SVG / DOCX) and **images** (JPEG pass-through, PNG)
* **Search** (plain / regex / whole-word) with match rectangles, **highlighting** (incremental update)
* **Page extraction**, page count, metadata, outline/bookmarks
* **Export** to **DOCX** and **XML**
* PDF 1.0–2.0 structures: xref streams, object streams, hybrid files, broken-xref repair,
  encryption (RC4 40/128, AES-128, AES-256 R5/R6)
* Optional, **constrained** language-model correction — off by default, with a guard that
  rejects any change that is not a local extraction repair (zero hallucination)

> **Architecture:** diagrams of the layers, the extraction pipeline and every public
> interface are in **[docs/architecture.md](docs/architecture.md)**.
>
> **Rules:** the project's binding requirements are in [CONSTITUTION.md](CONSTITUTION.md).

## Getting started

### Go

Requires Go 1.22+. There are no other dependencies.

```bash
go get github.com/khanshahan/sarek_pdf
```

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	sarekpdf "github.com/khanshahan/sarek_pdf"
	"github.com/khanshahan/sarek_pdf/export/docx"
	"github.com/khanshahan/sarek_pdf/model"
)

func main() {
	ctx := context.Background()

	doc, err := sarekpdf.Open("report.pdf") // or OpenBytes, OpenReader, OpenReaderAt, OpenURL
	if err != nil {
		log.Fatal(err)
	}
	defer doc.Close()
	fmt.Println("pages:", doc.NumPages())

	// Headings and paragraphs in reading order.
	headings, _ := doc.Headings(ctx)
	for _, h := range headings {
		fmt.Printf("H%d %s (page %d)\n", h.Level, h.Text, h.Page)
	}
	paras, _ := doc.Paragraphs(ctx)
	for _, p := range paras {
		fmt.Printf("p%d [%s] %s\n", p.Page, p.Paragraph.Role, p.Paragraph.Text())
	}

	// Search, then write a copy with the hits highlighted.
	hits, _ := doc.Search(ctx, "revenue", sarekpdf.SearchOptions{})
	hl, _ := os.Create("highlighted.pdf")
	defer hl.Close()
	if err := doc.WriteHighlighted(hl, sarekpdf.HighlightMatches(hits, model.Color{})); err != nil {
		log.Fatal(err)
	}

	// Full structure (paragraphs, tables, drawings, images) and DOCX export.
	res, err := doc.Extract(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, page := range res.Pages {
		for _, b := range page.Blocks {
			if t, ok := b.(*model.Table); ok {
				fmt.Println("table:", t.Grid())
			}
		}
	}
	out, _ := os.Create("report.docx")
	defer out.Close()
	if err := docx.Write(out, res, docx.Options{}); err != nil {
		log.Fatal(err)
	}
}
```

A `*Document` is safe for concurrent use. Pages are extracted by a bounded worker pool
(`WithWorkers`) and cached (`WithPageCache(false)` for one-pass streaming).

### Python

Build the native library once (needs Go 1.22+ and a C compiler). The binding itself
uses only the Python standard library:

```bash
git clone https://github.com/khanshahan/sarek_pdf && cd sarek_pdf
make lib                                   # → build/libsarekpdf.dylib (.so on Linux, .dll on Windows)
export SAREK_PDF_LIB=$PWD/build/libsarekpdf.dylib
export PYTHONPATH=$PWD/bindings/python
```

```python
from sarek_pdf import Document

with Document.open("report.pdf") as doc:          # also Document.from_bytes(data) and http(s) URLs
    print("pages:", doc.page_count)

    for h in doc.headings():
        print("  " * (h["level"] - 1) + h["text"], f"(page {h['page']})")

    for p in doc.paragraphs(pages="1-3"):
        print(p["page"], p["source"], p["text"][:80])   # source: "text-layer" or "ocr"

    for hit in doc.search("revenue"):
        print("found on page", hit["page"], hit["rects"])

    doc.highlight("revenue", "highlighted.pdf")
    doc.extract_pages("1-2", "first-two-pages.pdf")
    doc.save_docx("report.docx")
    xml = doc.to_xml()
```

A `Document` can be shared between threads.

### CLI

```bash
go install github.com/khanshahan/sarek_pdf/cmd/sarekpdf@latest
sarekpdf info report.pdf
sarekpdf convert -f docx -o report.docx report.pdf
sarekpdf convert -f xml  -o report.xml  https://example.com/report.pdf
sarekpdf search -q "revenue" report.pdf
sarekpdf highlight -q "revenue" -o highlighted.pdf report.pdf
sarekpdf pages -p 1-3,7 -o subset.pdf report.pdf
sarekpdf headings report.pdf
```

### .NET

Uses the same native library (C ABI documented in [capi/main.go](capi/main.go)):

```csharp
using var doc = SarekPdf.Document.Open("report.pdf");   // bindings/dotnet
foreach (var hit in doc.Search("revenue")) Console.WriteLine($"{hit.Page} {hit.Source}");
File.WriteAllBytes("report.docx", doc.ToDocx());
```

## Quality and speed

Measured on this repository's test corpus (Apple M-series, 14 cores):

| Metric | Result |
|---|---|
| Word recall / precision vs. Apple PDFKit, 72 Latin-script documents | 99.2 % / 99.8 % |
| Robustness: 1,794 real-world PDFs | 0 failures |
| Throughput, 200-page synthetic document | 2,460 pages/s (1 core), 13,600 pages/s (14 cores) |
| Reading order, 2- and 3-column layouts (spanning titles, aligned baselines, unequal widths) | all test layouts correct |
| OCR, 200 dpi 3-column scan (PaddleOCR v5, CPU) | 100 % word recall, 99.2 % mean confidence, ~1.4 s/page |

## OCR fallback for scanned pages

OCR is a pluggable module (`ocr.Engine`). The included engine runs PaddleOCR PP-OCRv5
models (Apache-2.0) on ONNX Runtime (MIT) and is compiled with the `onnx` build tag.

```bash
# 1. ONNX Runtime: download a release from github.com/microsoft/onnxruntime/releases
# 2. Models: huggingface.co/monkt/paddleocr-onnx (detection/v5 + languages/<lang>)
make cli-ocr ORT_DIR=/path/to/onnxruntime       # or: make lib-ocr for Python/.NET
build/sarekpdf convert -f docx -o out.docx -ocr-models /path/to/paddleocr-onnx scan.pdf
```

```go
eng, err := paddle.New(paddle.FromDir("/path/to/paddleocr-onnx", "english")) // or "latin", "eslav", …
doc, err := sarekpdf.Open("scan.pdf", sarekpdf.WithOCR(eng, ocr.Auto))
```

```python
doc = Document.open("scan.pdf", ocr={"models": "/path/to/paddleocr-onnx", "lang": "english"})
```

In `ocr.Auto` mode OCR runs only on pages without a usable text layer. **OCR output is
always labelled**:

| Output | How OCR text is marked |
|---|---|
| Go model | `Span.Source == model.SourceOCR`, `Span.Confidence`; `Paragraph.Source()`; `Page.OCR` (engine, reason, mean confidence, errors) |
| Text | `[page N: text recognised by OCR (engine), mean confidence 99.2%; not from the PDF text layer]` |
| XML | `<page ocr="true">`, `<ocr engine reason lines confidence/>`, `source="ocr" confidence="…"` on paragraphs and spans |
| DOCX | "OCR Text" paragraph style (orange bar) and a Word comment per OCR page |
| Search / JSON | `Match.Source`, `"source": "ocr"` |

Scanned pages processed **without** an OCR engine are flagged `Page.NoTextLayer` and
announced in text output, so missing text is never silent. Lines below the confidence
threshold (`WithOCRMinConfidence`, default 0.5) are dropped and reported in `Page.OCR.Errors`.

## Language-model correction (optional)

The model is a replaceable module behind `llm.Engine` (one method). Engines included:

* `correct/llm/openai` — any OpenAI-compatible server, e.g. llama.cpp's `llama-server` (MIT). Pure Go.
* `correct/llm/llamacpp` — in-process llama.cpp via cgo, build tag `llamacpp`.

```go
eng := openai.New("http://127.0.0.1:8080/v1", "qwen3-1.7b")
doc, _ := sarekpdf.Open("scan.pdf", sarekpdf.WithCorrector(&llm.Corrector{Engine: eng, Concurrency: 4}))
```

Every proposal passes `correct.Verify`: words may only be merged, split, or changed by known
extraction artefacts (case, diacritics, hyphens, ligatures, `rn↔m`, `cl↔d`, `vv↔w`, `I↔l`);
digits must be identical; no word may be added, removed or reordered. Rejected proposals keep
the original text, and `Paragraph.Original` always holds the uncorrected text.

Recommended permissively licensed models (check the license of the exact checkpoint you use):

| Model | Size | License | Notes |
|---|---|---|---|
| Qwen3-1.7B | 1.7 B | Apache-2.0 | good default; fast on CPU (Q4_K_M ≈ 1.1 GB) |
| Qwen2.5-1.5B-Instruct | 1.5 B | Apache-2.0 | (note: Qwen2.5-**3B** is *not* Apache-2.0) |
| Phi-4-mini-instruct | 3.8 B | MIT | higher accuracy, slower |
| SmolLM2-1.7B-Instruct | 1.7 B | Apache-2.0 | smallest footprint |

Gemma and Llama models carry custom licenses and are not recommended under the constitution.

## Known limitations

* CJK fonts that are bare CID-keyed CFF with **no** ToUnicode map (some Japanese/Korean PDFs)
  cannot be decoded exactly without Adobe's CID tables, which are BSD-licensed and therefore
  not shipped. Such characters are emitted as U+FFFD. Callers may supply tables at runtime with
  `WithCIDToUnicode` after checking their license. Embedded TrueType/OpenType fonts are decoded
  from the font's own `cmap`. Such pages can also be OCR'd with `ocr.Always`.
* OCR handles upright pages; rotated scans need the optional orientation model (not yet wired in).
  JPEG 2000 and JBIG2 scan images cannot be decoded for OCR yet (reported in `Page.OCR.Errors`).
* The cgo llama.cpp engine is written against the 2025 llama.cpp C API and must be compiled
  against a pinned llama.cpp version.

## Development

```bash
make test                         # go vet + go test -race ./...
make fuzz                         # 60 s fuzzing of the parser/extractor
make bench
make test-ocr ORT_DIR=…           # real OCR test; needs SAREK_OCR_MODELS and SAREK_SCAN
```

qpdf (Apache-2.0) is used by some tests as an external oracle; those tests skip when it is
not installed. It is never linked or shipped.
