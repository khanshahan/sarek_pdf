// Package ocr defines the pluggable OCR module used as a fallback when a
// page has no usable text layer.
//
// An Engine recognises text lines in an image. The library maps the
// results onto the page, builds paragraphs from them and marks every
// span with model.SourceOCR and the engine's confidence, so OCR output
// is never confused with text stored in the PDF (CONSTITUTION.md,
// Article 3).
//
// Engines included:
//
//   - package ocr/paddle: PaddleOCR models (Apache-2.0) on ONNX Runtime
//     (MIT) via cgo, build tag "onnx".
//
// Any other engine can be plugged in by implementing Engine.
package ocr

import (
	"context"
	"image"
)

// Line is one recognised text line in image pixel coordinates.
type Line struct {
	Text string
	// Quad holds the corners top-left, top-right, bottom-right,
	// bottom-left in pixels (x right, y down).
	Quad [4]image.Point
	// Confidence is in [0,1].
	Confidence float64
}

// Engine recognises text in images. Implementations must be safe for
// concurrent use.
type Engine interface {
	Recognize(ctx context.Context, img image.Image) ([]Line, error)
	// Name identifies the engine in output (e.g. "paddleocr-onnx").
	Name() string
}

// Mode selects when OCR runs.
type Mode uint8

const (
	// Auto runs OCR only on pages without a usable text layer: no
	// extractable text, or mostly glyphs without a Unicode mapping.
	Auto Mode = iota
	// Always runs OCR on every page that contains images; existing
	// text-layer text is kept and OCR text is added for image regions
	// that contain no text-layer text.
	Always
	// Never disables OCR even when an engine is configured.
	Never
)

// Formula is a mathematical formula recognised in an image.
type Formula struct {
	// Quad holds the corners in pixels (top-left, top-right, bottom-right,
	// bottom-left).
	Quad [4]image.Point
	// LaTeX is the recogniser's output.
	LaTeX string
	// Confidence is in [0,1].
	Confidence float64
	// Display is true for a formula set on its own line, false for a
	// formula inside a line of text.
	Display bool
	// Number, when non-nil, is the region of the formula's equation
	// number (its text comes from OCR).
	Number *[4]image.Point
}

// FormulaEngine finds and recognises formulas in images, e.g. scanned
// pages. Implementations must be safe for concurrent use. Recognition
// models can be wrong: results are always marked as OCR with their
// confidence, and the model's LaTeX is kept verbatim.
type FormulaEngine interface {
	Formulas(ctx context.Context, img image.Image) ([]Formula, error)
	Name() string
}
