# sarek_pdf architecture

This document describes how sarek_pdf is structured, how a page flows through the
pipeline, and the public interfaces each package exposes. The binding rules that
shape these choices are in [CONSTITUTION.md](../CONSTITUTION.md).

- [1. Layers and packages](#1-layers-and-packages)
- [2. Page extraction pipeline](#2-page-extraction-pipeline)
- [3. Public interfaces](#3-public-interfaces)
- [4. Pluggable modules (OCR and language model)](#4-pluggable-modules-ocr-and-language-model)
- [5. Concurrency model](#5-concurrency-model)
- [6. Cross-language interface (C ABI)](#6-cross-language-interface-c-abi)

---

## 1. Layers and packages

Dependencies point downwards only. Everything under `internal/` is an implementation
detail; applications use the root package `sarekpdf`, `model`, the exporters and the
pluggable modules.

```mermaid
flowchart TB
    subgraph Hosts["Host applications"]
        GOAPP["Go application"]
        PY["Python<br/>bindings/python (ctypes)"]
        NET[".NET<br/>bindings/dotnet (P/Invoke)"]
        CLI["cmd/sarekpdf (CLI)"]
    end

    CAPI["capi<br/>C ABI: libsarekpdf.so / .dylib / .dll"]

    subgraph Public["Public Go API"]
        ROOT["sarekpdf (root)<br/>Open* · Document · Extract · Search<br/>Headings · Outline · WriteHighlighted · ExtractPages"]
        MODEL["model<br/>Document · Page · Block · Paragraph<br/>Table · Drawing · Image · Span · Source"]
        DOCX["export/docx"]
        XML["export/xmlexport"]
    end

    subgraph Plug["Pluggable modules (interfaces)"]
        OCRI["ocr<br/>Engine interface · Mode"]
        PADDLE["ocr/paddle<br/>PaddleOCR on ONNX Runtime<br/>(build tag onnx)"]
        CORR["correct<br/>Corrector · Verify · Rules"]
        LLM["correct/llm<br/>Engine interface · Corrector"]
        OAI["correct/llm/openai<br/>OpenAI-compatible HTTP"]
        LCPP["correct/llm/llamacpp<br/>in-process llama.cpp<br/>(build tag llamacpp)"]
    end

    subgraph Internal["internal/ (implementation)"]
        LAYOUT["layout<br/>fragments · lines · XY-cut columns<br/>paragraphs · headings · tables<br/>drawings · bidi · OCR lines"]
        CONTENT["content<br/>content-stream interpreter<br/>glyphs · paths · image placements"]
        FONT["font<br/>encodings · CMaps · ToUnicode<br/>TrueType cmap · widths"]
        IMAGES["images<br/>JPEG/PNG export · CCITT G3/G4<br/>rasterise for OCR"]
        TEXTNORM["textnorm<br/>ligatures · CJK/Arabic<br/>compatibility forms"]
        PDF["pdf<br/>lexer · parser · xref/objstm · repair<br/>filters · RC4/AES · writer"]
    end

    GOAPP --> ROOT
    CLI --> ROOT
    PY --> CAPI
    NET --> CAPI
    CAPI --> ROOT
    CAPI --> DOCX
    CAPI --> XML

    ROOT --> MODEL
    DOCX --> MODEL
    XML --> MODEL
    ROOT --> OCRI
    ROOT --> CORR
    PADDLE -. implements .-> OCRI
    LLM -. implements .-> CORR
    OAI -. implements .-> LLM
    LCPP -. implements .-> LLM

    ROOT --> LAYOUT
    ROOT --> CONTENT
    ROOT --> IMAGES
    LAYOUT --> MODEL
    LAYOUT --> TEXTNORM
    CONTENT --> FONT
    CONTENT --> PDF
    FONT --> PDF
    IMAGES --> PDF
```

| Package | Responsibility |
|---|---|
| `internal/pdf` | Zero-copy lexer and parser, cross-reference tables and streams, object streams, hybrid files, automatic repair of broken files, stream filters (Flate, LZW, ASCII85, ASCIIHex, RunLength, predictors), standard security handler (RC4, AES-128, AES-256 R5/R6), object writer for incremental updates and page extraction. |
| `internal/font` | Simple and composite fonts: standard encodings, `Differences`, ToUnicode and CID CMaps, Unicode recovery from embedded TrueType/OpenType `cmap` tables, glyph widths. |
| `internal/content` | Executes content streams (text, paths, images, form XObjects, inline images) into positioned glyphs, vector paths and image placements in display coordinates. |
| `internal/layout` | Turns glyphs into words, lines and paragraphs; recursive XY-cut reading order (any number of columns); headings, lists, alignment; ruled, rule-delimited and borderless tables; vector drawings; right-to-left reordering; layout of OCR lines. |
| `internal/images` | Image export (JPEG pass-through, PNG), CCITT G3/G4 decoder, rasterisation of page images for OCR. |
| `internal/textnorm` | Deterministic normalisation tables (generated from the Unicode Character Database). |
| `sarekpdf` (root) | Public API: opening, parallel extraction, page cache, search, headings, outline, highlighting, page extraction, OCR and correction orchestration. |
| `model` | Plain-data result types shared by all exporters and bindings. |
| `export/docx`, `export/xmlexport` | Writers for DOCX and XML. |
| `ocr`, `ocr/paddle` | OCR module interface and the PaddleOCR/ONNX engine. |
| `correct`, `correct/llm/...` | Correction interface, zero-hallucination guard and language-model engines. |
| `capi`, `bindings/*`, `cmd/sarekpdf` | C ABI, Python and .NET bindings, command-line tool. |

---

## 2. Page extraction pipeline

`Document.Extract` distributes pages over a bounded worker pool. Each page runs the
pipeline below independently; results are cached per page.

```mermaid
sequenceDiagram
    autonumber
    participant App as Application
    participant Doc as sarekpdf.Document
    participant Pool as Worker pool
    participant CI as content.Interpreter
    participant OCR as ocr.Engine (optional)
    participant LA as layout.Analyze
    participant COR as correct.Corrector (optional)

    App->>Doc: Extract(ctx, pages...)
    Doc->>Pool: one job per page (WithWorkers)
    loop every page, in parallel
        Pool->>CI: RunPage(page)
        CI-->>Pool: glyphs, paths, image placements
        alt no usable text layer and OCR configured
            Pool->>OCR: Recognize(page image)
            OCR-->>Pool: text lines + confidence
            Note over Pool: lines mapped to page coordinates,<br/>marked Source = OCR
        else scanned page without OCR engine
            Note over Pool: Page.NoTextLayer = true
        end
        Pool->>LA: Analyze(glyphs, paths, images, OCR lines)
        LA-->>Pool: blocks in reading order
        opt corrector configured
            Pool->>COR: Correct(paragraph texts)
            COR-->>Pool: proposals
            Note over Pool: correct.Verify accepts only local repairs,<br/>original kept in Paragraph.Original
        end
    end
    Pool-->>Doc: pages
    Doc->>Doc: mark running headers and footers
    Doc-->>App: *model.Document
```

Layout steps inside `layout.Analyze`:

```mermaid
flowchart LR
    G["glyphs"] --> F["fragments<br/>(words and runs on a baseline)"]
    O["OCR lines"] --> F
    P["paths"] --> T["ruled and rule-delimited tables"]
    F --> T
    P --> D["drawings<br/>(clustered paths + labels)"]
    F --> D
    T --> X["XY-cut reading order<br/>columns, bands, borderless tables"]
    D --> X
    I["images"] --> X
    F --> X
    X --> L["lines → bidi reorder"]
    L --> PA["paragraphs, headings, lists, alignment"]
    PA --> B["ordered blocks"]
```

---

## 3. Public interfaces

### 3.1 Root package `sarekpdf`

```mermaid
classDiagram
    direction LR
    class Document {
        +NumPages() int
        +Version() string
        +Encrypted() bool
        +PageSize(n int) (w, h float64, err)
        +Metadata() model.Metadata
        +Outline() []model.OutlineItem
        +Page(ctx, n int) (*model.Page, error)
        +Extract(ctx, pages ...int) (*model.Document, error)
        +Text(ctx, pages ...int) (string, error)
        +Paragraphs(ctx, pages ...int) ([]ParagraphRef, error)
        +Headings(ctx, pages ...int) ([]Heading, error)
        +Search(ctx, query string, SearchOptions) ([]Match, error)
        +WriteHighlighted(w io.Writer, []Highlight) error
        +ExtractPages(w io.Writer, pages []int) error
        +Close() error
    }
    class Opening {
        <<functions>>
        +Open(path, ...Option) (*Document, error)
        +OpenBytes(data, ...Option) (*Document, error)
        +OpenReader(io.Reader, ...Option) (*Document, error)
        +OpenReaderAt(io.ReaderAt, size, ...Option) (*Document, error)
        +OpenURL(ctx, url, ...Option) (*Document, error)
        +ParsePageRanges(spec string, n int) ([]int, error)
        +HighlightMatches([]Match, model.Color) []Highlight
    }
    class Option {
        <<func(*config)>>
        WithPassword(string)
        WithWorkers(int)
        WithTables(bool) / WithDrawings(bool)
        WithImages(bool) / WithImageData(bool)
        WithPageCache(bool) / WithNormalization(bool)
        WithMaxSize(int64) / WithHTTPClient(*http.Client)
        WithCIDToUnicode(collection, cmap)
        WithOCR(ocr.Engine, ocr.Mode)
        WithOCRMinConfidence(float64)
        WithCorrector(correct.Corrector)
        WithCorrectTables(bool)
    }
    class SearchOptions {
        +CaseSensitive bool
        +WholeWord bool
        +Regexp bool
        +Pages []int
        +MaxResults int
    }
    class Match {
        +Page int
        +Text string
        +Rects []model.Rect
        +Source model.Source
    }
    class Highlight {
        +Page int
        +Rects []model.Rect
        +Color model.Color
        +Note string
        +Author string
    }
    class Heading {
        +Level int
        +Text string
        +Page int
        +BBox model.Rect
    }
    class ParagraphRef {
        +Page int
        +Paragraph *model.Paragraph
    }
    Opening ..> Document : creates
    Opening ..> Option : accepts
    Document ..> SearchOptions
    Document ..> Match : returns
    Document ..> Highlight : accepts
    Document ..> Heading : returns
    Document ..> ParagraphRef : returns
```

### 3.2 Result model (`model`)

```mermaid
classDiagram
    direction TB
    class Document {
        +Version string
        +Metadata Metadata
        +Outline []OutlineItem
        +Pages []*Page
        +Text() string
        +TextWithNotices() string
    }
    class Page {
        +Number int
        +Width, Height float64
        +Blocks []Block
        +OCR *OCRInfo
        +NoTextLayer bool
        +Text() string
        +Paragraphs(withTables bool) []*Paragraph
        +OCRNotice() string
    }
    class Block {
        <<interface>>
        +Bounds() Rect
    }
    class Paragraph {
        +BBox Rect
        +Role Role
        +Level int
        +Align Align
        +RTL bool
        +Lines []Line
        +Runs []Run
        +Original string
        +Text() string
        +Source() Source
        +Confidence() float64
    }
    class Table {
        +BBox Rect
        +Ruled bool
        +ColX, RowY []float64
        +Cells []*Cell
        +NumRows() int
        +NumCols() int
        +Grid() [][]string
    }
    class Cell {
        +Row, Col, RowSpan, ColSpan int
        +Paragraphs []*Paragraph
        +Fill *Color
    }
    class Drawing {
        +BBox Rect
        +Paths []Path
        +Labels []Line
    }
    class Image {
        +BBox Rect
        +Width, Height int
        +Format string
        +Data []byte
        +OCRSource bool
    }
    class Line {
        +BBox Rect
        +Baseline float64
        +Spans []Span
    }
    class Span {
        +Text string
        +BBox Rect
        +Style Style
        +Edges []float64
        +Invisible bool
        +Source Source
        +Confidence float64
    }
    class Source {
        <<enum>>
        SourceTextLayer
        SourceOCR
    }
    class OCRInfo {
        +Engine string
        +Reason string
        +Lines int
        +Confidence float64
        +Errors []string
    }
    Document "1" *-- "n" Page
    Page "1" *-- "n" Block
    Page o-- OCRInfo
    Block <|.. Paragraph
    Block <|.. Table
    Block <|.. Drawing
    Block <|.. Image
    Table "1" *-- "n" Cell
    Cell "1" *-- "n" Paragraph
    Paragraph "1" *-- "n" Line
    Drawing "1" *-- "n" Line : labels
    Line "1" *-- "n" Span
    Span --> Source
```

### 3.3 Exporters

```mermaid
classDiagram
    class docx {
        <<package export/docx>>
        +Write(w io.Writer, doc *model.Document, o Options) error
        +SVG(d *model.Drawing) string
    }
    class docxOptions["docx.Options"] {
        +SkipHeadersFooters bool
        +NoPageBreaks bool
        +NoDrawings bool
        +NoImages bool
        +KeepOCRImages bool
    }
    class xmlexport {
        <<package export/xmlexport>>
        +Write(w io.Writer, doc *model.Document, o Options) error
        +DefaultOptions() Options
        +PathData(p *model.Path, ox, oy float64) string
    }
    class xmlOptions["xmlexport.Options"] {
        +Geometry bool
        +EmbedImages bool
        +Paths bool
    }
    docx ..> docxOptions
    xmlexport ..> xmlOptions
```

---

## 4. Pluggable modules (OCR and language model)

Both modules are interfaces. The core never depends on a concrete engine, so an
engine or model can be replaced without touching the library.

```mermaid
classDiagram
    direction LR
    class OCREngine["ocr.Engine"] {
        <<interface>>
        +Recognize(ctx, img image.Image) ([]ocr.Line, error)
        +Name() string
    }
    class OCRLine["ocr.Line"] {
        +Text string
        +Quad [4]image.Point
        +Confidence float64
    }
    class OCRMode["ocr.Mode"] {
        <<enum>>
        Auto
        Always
        Never
    }
    class PaddleEngine["paddle.Engine"] {
        +New(paddle.Options) (*Engine, error)
        +FromDir(dir, lang string) Options
        +Close()
    }
    OCREngine <|.. PaddleEngine
    OCREngine ..> OCRLine

    class Corrector["correct.Corrector"] {
        <<interface>>
        +Correct(ctx, paragraphs []string) ([]string, error)
    }
    class Verify["correct.Verify"] {
        <<func>>
        Verify(orig, corrected string) ([]int, bool)
    }
    class Rules["correct.Rules"] {
        +Apply(s string) string
    }
    class LLMEngine["llm.Engine"] {
        <<interface>>
        +Complete(ctx, system, user string, maxTokens int) (string, error)
    }
    class LLMCorrector["llm.Corrector"] {
        +Engine llm.Engine
        +Concurrency int
        +MinWords int
        +MaxChars int
    }
    class OpenAI["openai.Engine"] {
        +New(baseURL, model string) *Engine
    }
    class LlamaCpp["llamacpp.Engine"] {
        +New(llamacpp.Options) (*Engine, error)
        +Close()
    }
    Corrector <|.. Rules
    Corrector <|.. LLMCorrector
    LLMCorrector --> LLMEngine
    LLMCorrector ..> Verify : every proposal
    LLMEngine <|.. OpenAI
    LLMEngine <|.. LlamaCpp
```

Guarantees enforced by the library, independent of the engine:

| Module | Guarantee |
|---|---|
| OCR | Runs only when configured (`WithOCR`). In `Auto` mode only on pages without a usable text layer. Every OCR span has `Source = SourceOCR` and a confidence; lines below `WithOCRMinConfidence` are dropped and reported; `Page.OCR` records engine, reason and mean confidence; exporters label OCR text. |
| Language model | Off by default. A proposal is accepted only if `correct.Verify` proves it is a local extraction repair (merge/split words, case, diacritics, hyphens, ligatures, `rn↔m`, `cl↔d`, `vv↔w`, `I↔l`; identical digits; no added, removed or reordered words). The original text is kept in `Paragraph.Original`. |

---

## 5. Concurrency model

```mermaid
flowchart LR
    subgraph Shared["Shared per Document (thread-safe)"]
        R["pdf.Reader<br/>sharded object cache (64 RW-locked shards)<br/>object-stream cache"]
        FL["font.Loader<br/>font cache"]
        FC["content.FormCache<br/>decoded form XObjects (bounded)"]
        PC["page cache<br/>per-page mutex"]
    end
    subgraph Workers["Worker pool (WithWorkers, default GOMAXPROCS)"]
        W1["worker 1<br/>interpreter + layout"]
        W2["worker 2"]
        WN["worker n"]
    end
    GP["sync.Pool<br/>glyph buffers"]
    W1 --> R & FL & FC & PC & GP
    W2 --> R & FL & FC & PC & GP
    WN --> R & FL & FC & PC & GP
```

- A `*Document` may be used by any number of goroutines at once; parsed objects are
  immutable after loading.
- Per-page state (interpreter, layout) is goroutine-local; glyph buffers are recycled
  through a `sync.Pool`.
- OCR and language-model engines must be safe for concurrent use; the provided ones are.
- All long operations take a `context.Context` and stop on cancellation.

---

## 6. Cross-language interface (C ABI)

```mermaid
sequenceDiagram
    participant Host as Python / .NET
    participant ABI as libsarekpdf (capi)
    participant Go as sarekpdf
    Host->>ABI: sarek_open_file(path, options_json)
    ABI->>Go: Open(path, options...)
    ABI-->>Host: handle (uint64)
    Host->>ABI: sarek_call(handle, "search", args_json)
    ABI->>Go: doc.Search(...)
    ABI-->>Host: JSON bytes (free with sarek_free)
    Host->>ABI: sarek_call(handle, "docx", args_json)
    ABI-->>Host: DOCX bytes
    Host->>ABI: sarek_close(handle)
```

| C function | Purpose |
|---|---|
| `sarek_open_file`, `sarek_open_bytes`, `sarek_open_url` | Open a document; return a handle. Options JSON: `password`, `workers`, `tables`, `drawings`, `images`, `ocr {models, lang, mode}`, `llm {url, model, concurrency}`. |
| `sarek_call(handle, op, args_json, &out, &len, &err)` | Operations: `info`, `text`, `xml`, `docx`, `paragraphs`, `headings`, `search`, `highlight`, `extract_pages`. |
| `sarek_close`, `sarek_free`, `sarek_version` | Release a handle, free returned buffers, report version. |

All functions are thread-safe, convert panics into errors, and copy input buffers so
the caller may free them immediately.
