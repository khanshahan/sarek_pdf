# Project instructions

Read and follow [CONSTITUTION.md](CONSTITUTION.md) before any change. In particular:

- No third-party Go modules (`go.mod` has no `require`), no BSD/copyleft code or data,
  nothing copied from other PDF libraries.
- Never invent text. LLM correction stays off by default and every change goes through
  `correct.Verify`.
- `*Document` must remain safe for concurrent use; run `make test` (vet + race) before committing.

Layout: `internal/pdf` (objects, xref, filters, crypto, writer) → `internal/font` →
`internal/content` (interpreter) → `internal/layout` (blocks) → root package API →
`export/{docx,xmlexport}`, `correct/...`, `capi` (C ABI), `bindings/{python,dotnet}`.
