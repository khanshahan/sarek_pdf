# sarek_pdf

High-performance, dependency-free PDF extraction for Go, Python and .NET.

* **Text, paragraphs, headings, lists** in reading order (multi-column, RTL, CJK)
* **Tables** with structure (ruled grids incl. merged cells, borderless and rule-delimited tables)
* **Drawings** (vector paths → SVG / DOCX) and **images** (JPEG pass-through, PNG)
* **Search** (plain / regex / whole-word) with match rectangles, **highlighting** (incremental update)
* **Page extraction**, page count, metadata, outline/bookmarks
* **Export** to **DOCX** and **XML**
* PDF 1.0–2.0 structures: xref streams, object streams, hybrid files, broken-xref repair,
  encryption (RC4 40/128, AES-128, AES-256 R5/R6)
* Optional, **constrained** language-model correction — off by default, with a guard that
  rejects any change that is not a local extraction repair (zero hallucination)

> The project's binding requirements are in [CONSTITUTION.md](CONSTITUTION.md).

## Quality and speed

Measured on this repository's test corpus (macOS system PDFs, Apple M-series, 14 cores):

| Metric | Result |
|---|---|
| Word recall / precision vs. Apple PDFKit, 72 Latin-script documents | 99.2 % / 99.8 % |
| Robustness: 1,794 real-world PDFs | 0 failures |
| Throughput, 200-page synthetic document | 2,460 pages/s (1 core), 13,600 pages/s (14 cores) |

## Go

```go
import (
    sarekpdf "github.com/khanshahan/sarek_pdf"
    "github.com/khanshahan/sarek_pdf/export/docx"
)

doc, err := sarekpdf.Open("report.pdf")            // or OpenBytes, OpenReader, OpenReaderAt, OpenURL
if err != nil { return err }
defer doc.Close()

fmt.Println(doc.NumPages(), doc.Metadata().Title)

res, err := doc.Extract(ctx)                         // all pages, in parallel
for _, page := range res.Pages {
    for _, b := range page.Blocks {
        switch v := b.(type) {
        case *model.Paragraph: fmt.Println(v.Role, v.Text())
        case *model.Table:     fmt.Println(v.Grid())
        case *model.Drawing:   fmt.Println(len(v.Paths), "paths")
        case *model.Image:     fmt.Println(v.Format, len(v.Data))
        }
    }
}

hits, _ := doc.Search(ctx, "net revenue", sarekpdf.SearchOptions{})
_ = doc.WriteHighlighted(out, sarekpdf.HighlightMatches(hits, model.Color{}))
_ = doc.ExtractPages(out2, []int{1, 3})
_ = docx.Write(out3, res, docx.Options{})
```

A `*Document` is safe for concurrent use. Pages are extracted by a bounded worker pool
(`WithWorkers`) and cached (`WithPageCache(false)` for one-pass streaming).

## CLI

```
go install github.com/khanshahan/sarek_pdf/cmd/sarekpdf@latest
sarekpdf info report.pdf
sarekpdf convert -f docx -o report.docx report.pdf
sarekpdf convert -f xml  -o report.xml  https://example.com/report.pdf
sarekpdf search -q "revenue" report.pdf
sarekpdf highlight -q "revenue" -o highlighted.pdf report.pdf
sarekpdf pages -p 1-3,7 -o subset.pdf report.pdf
sarekpdf headings report.pdf
```

## Python and .NET

Build the shared library (C ABI documented in [capi/main.go](capi/main.go)):

```
make lib          # build/libsarekpdf.{so,dylib} or sarekpdf.dll
```

```python
from sarek_pdf import Document          # bindings/python, ctypes only
with Document.open("report.pdf") as doc:
    print(doc.page_count, doc.headings())
    doc.save_docx("report.docx")
```

```csharp
using var doc = SarekPdf.Document.Open("report.pdf");   // bindings/dotnet
File.WriteAllBytes("report.docx", doc.ToDocx());
```

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
  from the font's own `cmap`.
* No OCR: scanned pages without a text layer yield images only.
* The cgo llama.cpp engine is written against the 2025 llama.cpp C API and must be compiled
  against a pinned llama.cpp version.

## Development

```
make test     # go vet + go test -race ./...
make fuzz     # 60 s fuzzing of the parser/extractor
make bench
```

qpdf (Apache-2.0) is used by some tests as an external oracle; those tests skip when it is
not installed. It is never linked or shipped.
