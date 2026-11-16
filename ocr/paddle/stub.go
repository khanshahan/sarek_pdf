//go:build !onnx

package paddle

import (
	"context"
	"errors"
	"image"

	"github.com/khanshahan/sarek_pdf/ocr"
)

// ErrNotCompiled is returned when the engine is used in a build without
// the "onnx" build tag.
var ErrNotCompiled = errors.New("paddle: OCR engine not compiled in; rebuild with -tags onnx (requires ONNX Runtime)")

// Options configures the engine (see engine_onnx.go).
type Options struct {
	DetModel, RecModel, Dict string
	Threads                  int
	Params                   Params
}

// Engine is unavailable in this build.
type Engine struct{}

var _ ocr.Engine = (*Engine)(nil)

// New always fails in builds without the "onnx" tag.
func New(Options) (*Engine, error) { return nil, ErrNotCompiled }

// Name implements ocr.Engine.
func (*Engine) Name() string { return "paddleocr-onnx" }

// Recognize implements ocr.Engine.
func (*Engine) Recognize(context.Context, image.Image) ([]ocr.Line, error) {
	return nil, ErrNotCompiled
}

// Close is a no-op.
func (*Engine) Close() {}
