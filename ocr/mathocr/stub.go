//go:build !onnx

package mathocr

import (
	"context"
	"errors"
	"image"
	"path/filepath"

	"github.com/khanshahan/sarek_pdf/ocr"
)

// ErrNotCompiled is returned in builds without the "onnx" tag.
var ErrNotCompiled = errors.New("mathocr: formula engine not compiled in; rebuild with -tags onnx (requires ONNX Runtime)")

// Options configures the engine (see engine_onnx.go).
type Options struct {
	Layout, Encoder, Decoder, Tokenizer string
	Threads                             int
	MinScore                            float64
	MaxTokens                           int
}

// FromDir expects layout.onnx, encoder_model.onnx, decoder_model.onnx and
// tokenizer.json in dir.
func FromDir(dir string) Options {
	return Options{Layout: filepath.Join(dir, "layout.onnx"), Encoder: filepath.Join(dir, "encoder_model.onnx"),
		Decoder: filepath.Join(dir, "decoder_model.onnx"), Tokenizer: filepath.Join(dir, "tokenizer.json")}
}

// Engine is unavailable in this build.
type Engine struct{}

var _ ocr.FormulaEngine = (*Engine)(nil)

// New always fails in builds without the "onnx" tag.
func New(Options) (*Engine, error) { return nil, ErrNotCompiled }

// Name implements ocr.FormulaEngine.
func (*Engine) Name() string { return "pp-doclayoutv3+pix2text-mfr" }

// Formulas implements ocr.FormulaEngine.
func (*Engine) Formulas(context.Context, image.Image) ([]ocr.Formula, error) {
	return nil, ErrNotCompiled
}

// Close is a no-op.
func (*Engine) Close() {}
