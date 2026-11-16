package sarekpdf

import (
	"context"
	"fmt"
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
	if d.cfg.ocr == nil || d.cfg.ocrMode == ocr.Never || len(res.Images) == 0 {
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

// runOCR recognises text in the page's images and maps it to display
// coordinates.
func (d *Document) runOCR(ctx context.Context, res *content.Result, reason string) ([]layout.OCRLine, *model.OCRInfo, map[*content.ImagePlacement]bool) {
	info := &model.OCRInfo{Engine: d.cfg.ocr.Name(), Reason: reason}
	used := map[*content.ImagePlacement]bool{}
	pageArea := res.Width * res.Height
	var lines []layout.OCRLine
	confSum := 0.0
	dropped := 0
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
		rec, err := d.cfg.ocr.Recognize(ctx, img)
		if err != nil {
			if ctx.Err() != nil {
				return nil, info, used
			}
			info.Errors = append(info.Errors, fmt.Sprintf("ocr: %v", err))
			continue
		}
		used[ip] = true
		b := img.Bounds()
		w, h := float64(b.Dx()), float64(b.Dy())
		for _, l := range rec {
			if l.Confidence < d.cfg.ocrMinConf {
				dropped++
				continue
			}
			var bb model.Rect
			for k, q := range l.Quad {
				p := ip.CTM.Apply(float64(q.X-b.Min.X)/w, 1-float64(q.Y-b.Min.Y)/h)
				r := model.Rect{X0: p.X, Y0: p.Y, X1: p.X, Y1: p.Y}
				if k == 0 {
					bb = r
				} else {
					bb = bb.Union(r)
				}
			}
			lines = append(lines, layout.OCRLine{Text: l.Text, BBox: bb, Confidence: l.Confidence})
			confSum += l.Confidence
		}
	}
	info.Lines = len(lines)
	if len(lines) > 0 {
		info.Confidence = confSum / float64(len(lines))
	}
	if dropped > 0 {
		info.Errors = append(info.Errors, fmt.Sprintf("%d line(s) below confidence %.2f dropped", dropped, d.cfg.ocrMinConf))
	}
	return lines, info, used
}
