package sarekpdf

import (
	"context"
	"fmt"
	"image"
	"reflect"
	"strings"

	"github.com/khanshahan/sarek_pdf/internal/content"
	"github.com/khanshahan/sarek_pdf/internal/images"
	"github.com/khanshahan/sarek_pdf/internal/layout"
	"github.com/khanshahan/sarek_pdf/model"
	"github.com/khanshahan/sarek_pdf/ocr"
)

// needOCR decides whether OCR should run on a page and returns the
// reason ("" when it should not).
func (d *Document) needOCR(res *content.Result) string {
	if isNilEngine(d.cfg.ocr) && d.cfg.formulaOCR == nil || d.cfg.ocrMode == ocr.Never || len(res.Images) == 0 {
		return ""
	}
	if d.cfg.ocrMode == ocr.Always {
		return "forced"
	}
	return textLayerProblem(res)
}

// textLayerProblem reports why a page lacks a usable text layer
// ("no-text-layer", "unmapped-text") or "" when its text layer is usable.
func textLayerProblem(res *content.Result) string {
	if len(res.Images) == 0 {
		return ""
	}
	mapped, unmapped := 0, 0
	for i := range res.Glyphs {
		g := &res.Glyphs[i]
		switch {
		case g.Space:
		case g.Text == "":
			unmapped++
		case strings.TrimSpace(g.Text) != "":
			mapped++
		}
	}
	pageArea := res.Width * res.Height
	imgArea := 0.0
	for _, ip := range res.Images {
		imgArea += ip.BBox.Intersect(model.Rect{X1: res.Width, Y1: res.Height}).Area()
	}
	switch {
	case unmapped >= 20 && unmapped > mapped:
		return "unmapped-text"
	case mapped < 50 && imgArea >= 0.3*pageArea:
		// Scanned page: a large image and (almost) no text layer. A few
		// mapped glyphs (stamps, page numbers) do not count as a text layer.
		return "no-text-layer"
	}
	return ""
}

// runOCR recognises text (and, when configured, formulas) in the page's
// images and maps the results to display coordinates.
func (d *Document) runOCR(ctx context.Context, res *content.Result, reason string) ([]layout.OCRLine, []model.Block, *model.OCRInfo, map[*content.ImagePlacement]bool) {
	var names []string
	if !isNilEngine(d.cfg.ocr) {
		names = append(names, d.cfg.ocr.Name())
	}
	if d.cfg.formulaOCR != nil {
		names = append(names, d.cfg.formulaOCR.Name())
	}
	info := &model.OCRInfo{Engine: strings.Join(names, " + "), Reason: reason}
	used := map[*content.ImagePlacement]bool{}
	pageArea := res.Width * res.Height
	var lines []layout.OCRLine
	var blocks []model.Block
	confSum := 0.0
	dropped, droppedFormulas := 0, 0
	for i := range res.Images {
		ip := &res.Images[i]
		vis := ip.BBox.Intersect(model.Rect{X1: res.Width, Y1: res.Height})
		if vis.Area() < 0.02*pageArea || vis.W() < 20 || vis.H() < 8 {
			continue // too small to contain meaningful text
		}
		img, err := images.Decode(d.r, ip.Stream)
		if err != nil {
			info.Errors = append(info.Errors, fmt.Sprintf("image at %.0f,%.0f: %v", ip.BBox.X0, ip.BBox.Y0, err))
			continue
		}
		b := img.Bounds()
		w, h := float64(b.Dx()), float64(b.Dy())
		toPage := func(q [4]image.Point) model.Rect {
			var bb model.Rect
			for k, pt := range q {
				p := ip.CTM.Apply(float64(pt.X-b.Min.X)/w, 1-float64(pt.Y-b.Min.Y)/h)
				r := model.Rect{X0: p.X, Y0: p.Y, X1: p.X, Y1: p.Y}
				if k == 0 {
					bb = r
				} else {
					bb = bb.Union(r)
				}
			}
			return bb
		}
		var imgLines []layout.OCRLine
		if !isNilEngine(d.cfg.ocr) {
			rec, err := d.cfg.ocr.Recognize(ctx, img)
			if err != nil {
				if ctx.Err() != nil {
					return nil, nil, info, used
				}
				info.Errors = append(info.Errors, fmt.Sprintf("ocr: %v", err))
			}
			for _, l := range rec {
				if l.Confidence < d.cfg.ocrMinConf {
					dropped++
					continue
				}
				imgLines = append(imgLines, layout.OCRLine{Text: l.Text, BBox: toPage(l.Quad), Confidence: l.Confidence})
			}
			used[ip] = true
		}
		if d.cfg.formulaOCR != nil {
			fs, err := d.cfg.formulaOCR.Formulas(ctx, img)
			if err != nil {
				if ctx.Err() != nil {
					return nil, nil, info, used
				}
				info.Errors = append(info.Errors, fmt.Sprintf("formula ocr: %v", err))
			}
			for _, f := range fs {
				if !f.Display {
					continue // inline math stays with the text OCR (more reliable)
				}
				if f.Confidence < d.cfg.formulaMin {
					droppedFormulas++
					continue // keep the text-OCR lines of this region
				}
				bb := toPage(f.Quad)
				var numBox model.Rect
				if f.Number != nil {
					numBox = toPage(*f.Number)
				}
				number := ""
				kept := imgLines[:0]
				for _, l := range imgLines {
					c := l.BBox.Center()
					switch {
					case f.Number != nil && numBox.Contains(c):
						number = strings.TrimSpace(l.Text)
					case bb.Contains(c) || l.BBox.Intersect(bb).Area() > 0.5*l.BBox.Area():
						// replaced by the formula
					default:
						kept = append(kept, l)
					}
				}
				imgLines = kept
				raw := model.CompactLaTeX(f.LaTeX)
				blocks = append(blocks, &model.Formula{BBox: bb.Union(numBox), Root: model.ParseLaTeX(raw), Raw: raw,
					Number: number, Source: model.SourceOCR, Confidence: f.Confidence})
				info.Formulas++
			}
			used[ip] = true
		}
		for _, l := range imgLines {
			confSum += l.Confidence
		}
		lines = append(lines, imgLines...)
	}
	info.Lines = len(lines)
	if len(lines) > 0 {
		info.Confidence = confSum / float64(len(lines))
	}
	if dropped > 0 {
		info.Errors = append(info.Errors, fmt.Sprintf("%d line(s) below confidence %.2f dropped", dropped, d.cfg.ocrMinConf))
	}
	if droppedFormulas > 0 {
		info.Errors = append(info.Errors, fmt.Sprintf("%d formula(s) below confidence %.2f not used; their text OCR is kept", droppedFormulas, d.cfg.formulaMin))
	}
	return lines, blocks, info, used
}

// isNilEngine reports a missing text-OCR engine (including a typed nil).
func isNilEngine(e ocr.Engine) bool {
	if e == nil {
		return true
	}
	v := reflect.ValueOf(e)
	return v.Kind() == reflect.Pointer && v.IsNil()
}
