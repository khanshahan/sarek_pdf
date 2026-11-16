# sarek_pdf — Constitution

This document records the **non-negotiable requirements** of the project.
Every change (human- or AI-authored) must comply. A pull request that
violates an article must be rejected, regardless of other merits.
Amendments require an explicit decision by the project owner and must be
recorded in the *Amendment log* at the end of this file.

---

## Article 1 — Purpose

sarek_pdf is a high-performance, scalable, maintainable **Go** library for
extracting content from PDF files (PDF 1.5, 1.6, 1.7 and later, including
PDF 2.0 where feasible). It is intended for commercial use and is licensed under the MIT
License (Article 2.6).

## Article 2 — Licensing (strict)

1. **No copyleft code** of any kind (GPL, LGPL, AGPL, MPL, EPL, CDDL, SSPL,
   EUPL, CC-BY-SA, or similar) may be used, linked, vendored, copied,
   translated or adapted — in source, binary, test or tooling form shipped
   with the library.
2. **No BSD-licensed third-party code** may be added. The only accepted
   exception is the **Go standard library and toolchain** (BSD-3-Clause),
   which is unavoidable for any Go program; its attribution notice is kept
   in `THIRD_PARTY_NOTICES.md`.
3. **Zero third-party Go module dependencies** in the library. `go.mod`
   must contain no `require` lines. Any exception needs an amendment.
4. No code, tables or data files may be copied from other PDF libraries
   (pdf.js, MuPDF, Poppler, PDFium, pdfminer, pdfcpu, unipdf, etc.).
   Everything is implemented from the ISO 32000 specification.
5. Optional external runtimes (e.g. an LLM engine) must be **MIT or
   Apache-2.0**, must be **opt-in** (build tag or runtime configuration),
   and must never be required to build or use the core library.
   Model weights recommended in documentation must be MIT or Apache-2.0.
6. The project license is the standard **MIT License** (see `LICENSE`),
   unmodified. MIT contains no express patent grant. The project must
   **not** adopt any license that contains an express patent grant
   (e.g. Apache-2.0, MPL, GPLv3) for its own code, and contributions must
   be accepted under the MIT License.

## Article 3 — Accuracy and zero hallucination

1. Extracted text must come **only from the PDF**. The library never
   invents, completes, summarises or paraphrases content.
2. Language-model correction is **off by default**. When enabled it is
   **constrained**: it may only perform local repairs (broken words,
   spacing, hyphenation, character-level extraction errors). Any output
   that adds, removes or reorders words beyond those limits is
   **rejected automatically** and the original text is kept.
3. The original (uncorrected) text must always remain available alongside
   any corrected text.
4. Where information cannot be recovered reliably (e.g. fonts without a
   Unicode mapping), the library leaves it empty or marks it, rather
   than guessing.

## Article 4 — Performance and concurrency

1. Designed for **very high throughput in multi-threaded environments**.
2. An opened document is **safe for concurrent use** by many goroutines.
3. Pages are processed in parallel with a bounded worker pool; the degree
   of parallelism is configurable.
4. Hot paths are allocation-conscious (zero-copy lexing, caching of
   parsed objects and fonts, bounded memory per operation).
5. All long operations accept a `context.Context` and honour cancellation.
6. Hostile input must not cause panics, unbounded memory or infinite
   loops (depth limits, size limits, cycle detection).

## Article 5 — Functional scope

The library must support:

- Opening PDFs from a **file path**, **in-memory bytes**, any
  **`io.ReaderAt`**, and a **network location (URL)**.
- Page count, metadata, document outline.
- Extraction of **all text**, **paragraphs**, **headings**, **tables**
  (with structure: rows, columns, merged cells), **drawings** (vector
  graphics) and **images**.
- **Multi-column layouts** (2, 3 or more columns) read in correct order.
- **OCR fallback** for pages without a usable text layer, through a
  pluggable engine. OCR output must always be identifiable as OCR
  (marked in the model and in every export) together with its confidence;
  scanned pages processed without OCR must be flagged, never silently empty.
- **Formula recognition**: mathematics in the text layer rebuilt only from
  glyph geometry and drawn rules (never guessed); formulas in page images
  recognised by a pluggable engine and always marked as OCR with their
  confidence. Exported as LaTeX, MathML and native Word equations.
- **Search** (plain and regular expression) returning page numbers and
  match rectangles.
- **Highlighting** of text (writing highlight annotations as an
  incremental update; the original bytes are preserved).
- **Extracting pages** into a new PDF.
- Export to **DOCX** and **XML**.
- Encrypted documents (standard security handler, RC4 / AES-128 /
  AES-256) when the password is known.

## Article 6 — Interoperability

The library must be usable from **Go**, **Python** and **.NET**, via a
stable C ABI (`c-shared` build) with thin language bindings.

## Article 7 — Pluggable language model

1. The language model is a **replaceable module** behind a small Go
   interface. Swapping engines or models must not require changes to the
   core library.
2. The default recommendation is a **lightweight** model with a
   permissive license (MIT / Apache-2.0); accuracy is preferred over
   speed when the two conflict.

## Article 8 — Maintainability

1. Idiomatic Go: small packages with clear responsibilities, internal
   packages for implementation details, documented exported identifiers,
   wrapped errors, no global mutable state.
2. `go vet` and `go test -race ./...` must pass on every change.
3. New features come with tests; parsers come with malformed-input tests.

---

## Amendment log

| Date       | Article | Change            | Approved by   |
|------------|---------|-------------------|---------------|
| 2026-11-16 | —       | Initial version   | Project owner |
| 2026-11-16 | 5       | Multi-column reading order; OCR fallback with mandatory OCR labelling | Project owner |
| 2026-11-16 | 1, 2.6  | License changed from proprietary to MIT with patent reservation | Project owner |
| 2026-11-16 | 1, 2.6  | License changed to the standard, unmodified MIT License | Project owner |
| 2026-11-16 | 5       | Markdown export, cross-page tables, formula recognition | Project owner |
| 2026-11-16 | 5       | Formula recognition in scanned images (opt-in engine, OCR-marked) | Project owner |
