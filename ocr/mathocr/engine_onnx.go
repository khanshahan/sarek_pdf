//go:build onnx

package mathocr

import (
	"context"
	"errors"
	"image"
	"math"
	"path/filepath"
	"sort"

	"github.com/khanshahan/sarek_pdf/internal/ort"
	"github.com/khanshahan/sarek_pdf/ocr"
)

// Options configures the engine.
type Options struct {
	Layout    string // PP-DocLayoutV3 .onnx
	Encoder   string // Pix2Text-MFR encoder_model.onnx
	Decoder   string // Pix2Text-MFR decoder_model.onnx
	Tokenizer string // Pix2Text-MFR tokenizer.json
	Threads   int
	// MinScore is the minimum detection score (default 0.5).
	MinScore float64
	// MaxTokens bounds the LaTeX length (default 384).
	MaxTokens int
}

// FromDir expects layout.onnx, encoder_model.onnx, decoder_model.onnx and
// tokenizer.json in dir.
func FromDir(dir string) Options {
	return Options{Layout: filepath.Join(dir, "layout.onnx"), Encoder: filepath.Join(dir, "encoder_model.onnx"),
		Decoder: filepath.Join(dir, "decoder_model.onnx"), Tokenizer: filepath.Join(dir, "tokenizer.json")}
}

// Engine implements ocr.FormulaEngine. Safe for concurrent use.
type Engine struct {
	layout, enc, dec *ort.Session
	tok              *tokenizer
	o                Options
}

var _ ocr.FormulaEngine = (*Engine)(nil)

// Label indices of PP-DocLayoutV3.
const (
	labelDisplayFormula = 5
	labelFormulaNumber  = 11
	labelInlineFormula  = 15
)

// New loads the models.
func New(o Options) (*Engine, error) {
	if o.MinScore <= 0 {
		o.MinScore = 0.5
	}
	if o.MaxTokens <= 0 {
		o.MaxTokens = 384
	}
	tok, err := loadTokenizer(o.Tokenizer)
	if err != nil {
		return nil, err
	}
	e := &Engine{tok: tok, o: o}
	if e.layout, err = ort.Open(o.Layout, o.Threads); err != nil {
		return nil, err
	}
	if e.enc, err = ort.Open(o.Encoder, o.Threads); err != nil {
		e.Close()
		return nil, err
	}
	if e.dec, err = ort.Open(o.Decoder, o.Threads); err != nil {
		e.Close()
		return nil, err
	}
	return e, nil
}

// Close releases the sessions.
func (e *Engine) Close() {
	for _, s := range []*ort.Session{e.layout, e.enc, e.dec} {
		if s != nil {
			s.Close()
		}
	}
}

// Name implements ocr.FormulaEngine.
func (e *Engine) Name() string { return "pp-doclayoutv3+pix2text-mfr" }

type region struct {
	label int
	score float64
	r     image.Rectangle
}

// detect runs the layout model and returns formula and number regions.
func (e *Engine) detect(m *rgb) ([]region, error) {
	const side = 800
	in := m.tensor(image.Rect(0, 0, m.w, m.h), side, side, [3]float64{0, 0, 0}, [3]float64{1, 1, 1})
	sh, sw := float32(side)/float32(m.h), float32(side)/float32(m.w)
	out, dims, err := e.layout.Run([]ort.Tensor{
		{Name: e.layout.Inputs[0], Float32: []float32{side, side}, Shape: []int64{1, 2}},
		{Name: e.layout.Inputs[1], Float32: in, Shape: []int64{1, 3, side, side}},
		{Name: e.layout.Inputs[2], Float32: []float32{sh, sw}, Shape: []int64{1, 2}},
	}, e.layout.Outputs[0])
	if err != nil {
		return nil, err
	}
	if len(dims) != 2 || dims[1] < 6 {
		return nil, errors.New("mathocr: unexpected layout output shape")
	}
	cols := int(dims[1])
	var regs []region
	for i := 0; i+cols <= len(out); i += cols {
		row := out[i : i+cols]
		label, score := int(row[0]), float64(row[1])
		if score < e.o.MinScore || (label != labelDisplayFormula && label != labelInlineFormula && label != labelFormulaNumber) {
			continue
		}
		r := image.Rect(int(row[2]), int(row[3]), int(math.Ceil(float64(row[4]))), int(math.Ceil(float64(row[5])))).
			Intersect(image.Rect(0, 0, m.w, m.h))
		if r.Dx() < 4 || r.Dy() < 4 {
			continue
		}
		regs = append(regs, region{label: label, score: score, r: r})
	}
	return regs, nil
}

// recognize transcribes one formula image into LaTeX.
func (e *Engine) recognize(ctx context.Context, m *rgb, r image.Rectangle) (string, float64, error) {
	const side = 384
	pad := max(2, r.Dy()/10)
	r = image.Rect(r.Min.X-pad, r.Min.Y-pad, r.Max.X+pad, r.Max.Y+pad).Intersect(image.Rect(0, 0, m.w, m.h))
	px := m.tensor(r, side, side, [3]float64{0.5, 0.5, 0.5}, [3]float64{0.5, 0.5, 0.5})
	hidden, hd, err := e.enc.Run([]ort.Tensor{{Name: e.enc.Inputs[0], Float32: px, Shape: []int64{1, 3, side, side}}}, e.enc.Outputs[0])
	if err != nil {
		return "", 0, err
	}
	ids := []int64{1} // decoder start token
	var out []int
	conf := 0.0
	for step := 0; step < e.o.MaxTokens; step++ {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		logits, ld, err := e.dec.Run([]ort.Tensor{
			{Name: e.dec.Inputs[0], Int64: ids, Shape: []int64{1, int64(len(ids))}},
			{Name: e.dec.Inputs[1], Float32: hidden, Shape: hd},
		}, e.dec.Outputs[0])
		if err != nil {
			return "", 0, err
		}
		v := int(ld[len(ld)-1])
		last := logits[len(logits)-v:]
		best, bv := 0, last[0]
		for k, x := range last {
			if x > bv {
				best, bv = k, x
			}
		}
		// Probability of the chosen token (softmax).
		sum := 0.0
		for _, x := range last {
			sum += math.Exp(float64(x - bv))
		}
		p := 1 / sum
		if best == 2 { // end of sequence
			break
		}
		conf += p
		out = append(out, best)
		ids = append(ids, int64(best))
		if repeating(out) {
			return "", 0, nil // stuck in a loop: no reliable result
		}
	}
	if len(out) == 0 {
		return "", 0, nil
	}
	return e.tok.decode(out), conf / float64(len(out)), nil
}

func quad(r image.Rectangle) [4]image.Point {
	return [4]image.Point{r.Min, {r.Max.X, r.Min.Y}, r.Max, {r.Min.X, r.Max.Y}}
}

// Formulas implements ocr.FormulaEngine.
func (e *Engine) Formulas(ctx context.Context, img image.Image) ([]ocr.Formula, error) {
	m := toRGB(img)
	if m.w < 16 || m.h < 16 {
		return nil, nil
	}
	regs, err := e.detect(m)
	if err != nil {
		return nil, err
	}
	sort.Slice(regs, func(i, j int) bool { return regs[i].r.Min.Y < regs[j].r.Min.Y })
	var out []ocr.Formula
	for _, rg := range regs {
		if rg.label == labelFormulaNumber {
			continue
		}
		latex, conf, err := e.recognize(ctx, m, rg.r)
		if err != nil {
			return nil, err
		}
		if latex == "" {
			continue
		}
		f := ocr.Formula{Quad: quad(rg.r), LaTeX: latex, Confidence: conf * rg.score, Display: rg.label == labelDisplayFormula}
		// Equation number: a number region on the same row.
		for _, n := range regs {
			if n.label != labelFormulaNumber || !f.Display {
				continue
			}
			ov := min(n.r.Max.Y, rg.r.Max.Y) - max(n.r.Min.Y, rg.r.Min.Y)
			if ov > n.r.Dy()/2 {
				q := quad(n.r)
				f.Number = &q
				break
			}
		}
		out = append(out, f)
	}
	return out, nil
}
