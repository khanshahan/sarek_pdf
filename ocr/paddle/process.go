// Package paddle is an OCR engine running PaddleOCR PP-OCR models
// (Apache-2.0) exported to ONNX. Pre- and post-processing are pure Go and
// always compiled; inference uses ONNX Runtime (MIT) through cgo and is
// compiled only with the build tag "onnx" (see engine_onnx.go).
//
// Pipeline: DB text detection → per-line crops → CTC text recognition.
package paddle

import (
	"image"
	"math"
	"sort"
	"strings"
)

// Params tunes the pipeline. Zero values select the defaults.
type Params struct {
	DetMaxSide   int     // longest side fed to the detector (default 1536)
	DetThresh    float64 // pixel probability threshold (default 0.3)
	BoxThresh    float64 // minimum mean box probability (default 0.6)
	UnclipRatio  float64 // box expansion ratio (default 1.5)
	RecHeight    int     // recognizer input height (default 48)
	RecMaxWidth  int     // recognizer max input width (default 2048)
	RecBatchSize int     // lines per recognition batch (default 8)
}

func (p *Params) defaults() {
	if p.DetMaxSide <= 0 {
		p.DetMaxSide = 1536
	}
	if p.DetThresh <= 0 {
		p.DetThresh = 0.3
	}
	if p.BoxThresh <= 0 {
		p.BoxThresh = 0.6
	}
	if p.UnclipRatio <= 0 {
		p.UnclipRatio = 1.5
	}
	if p.RecHeight <= 0 {
		p.RecHeight = 48
	}
	if p.RecMaxWidth <= 0 {
		p.RecMaxWidth = 2048
	}
	if p.RecBatchSize <= 0 {
		p.RecBatchSize = 8
	}
}

// rgbImage is a dense 8-bit RGB raster.
type rgbImage struct {
	w, h int
	pix  []uint8 // RGBRGB...
}

func toRGB(img image.Image) *rgbImage {
	b := img.Bounds()
	out := &rgbImage{w: b.Dx(), h: b.Dy(), pix: make([]uint8, b.Dx()*b.Dy()*3)}
	switch src := img.(type) {
	case *image.Gray:
		for y := 0; y < out.h; y++ {
			row := src.Pix[y*src.Stride : y*src.Stride+out.w]
			for x, v := range row {
				o := (y*out.w + x) * 3
				out.pix[o], out.pix[o+1], out.pix[o+2] = v, v, v
			}
		}
		return out
	}
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bb, a := img.At(x, y).RGBA()
			if a == 0 { // transparent → white
				r, g, bb = 0xFFFF, 0xFFFF, 0xFFFF
			}
			out.pix[i], out.pix[i+1], out.pix[i+2] = uint8(r>>8), uint8(g>>8), uint8(bb>>8)
			i += 3
		}
	}
	return out
}

// sample returns the bilinearly interpolated channel value at (x, y).
func (m *rgbImage) sample(x, y float64, c int) float64 {
	x = math.Max(0, math.Min(float64(m.w-1), x))
	y = math.Max(0, math.Min(float64(m.h-1), y))
	x0, y0 := int(x), int(y)
	x1, y1 := min(x0+1, m.w-1), min(y0+1, m.h-1)
	fx, fy := x-float64(x0), y-float64(y0)
	p := func(xx, yy int) float64 { return float64(m.pix[(yy*m.w+xx)*3+c]) }
	return (p(x0, y0)*(1-fx)+p(x1, y0)*fx)*(1-fy) + (p(x0, y1)*(1-fx)+p(x1, y1)*fx)*fy
}

// detInput resizes the image for the detector (sides multiple of 32) and
// returns a CHW float tensor in BGR order normalised with ImageNet
// statistics, plus the scale factors back to the original image.
func detInput(m *rgbImage, maxSide int) (data []float32, w, h int, sx, sy float64) {
	scale := math.Min(1, float64(maxSide)/float64(max(m.w, m.h)))
	// Small scans are upscaled so text is large enough for the detector.
	if max(m.w, m.h) < 960 {
		scale = 960 / float64(max(m.w, m.h))
	}
	w = max(32, int(math.Round(float64(m.w)*scale/32))*32)
	h = max(32, int(math.Round(float64(m.h)*scale/32))*32)
	sx, sy = float64(m.w)/float64(w), float64(m.h)/float64(h)
	mean := [3]float64{0.485, 0.456, 0.406}
	std := [3]float64{0.229, 0.224, 0.225}
	data = make([]float32, 3*w*h)
	for y := 0; y < h; y++ {
		srcY := (float64(y)+0.5)*sy - 0.5
		for x := 0; x < w; x++ {
			srcX := (float64(x)+0.5)*sx - 0.5
			for c := 0; c < 3; c++ {
				v := m.sample(srcX, srcY, 2-c) / 255 // BGR
				data[c*w*h+y*w+x] = float32((v - mean[c]) / std[c])
			}
		}
	}
	return
}

// box is an axis-aligned text box in original image pixels.
type box struct {
	x0, y0, x1, y1 float64
	score          float64
}

// detBoxes extracts text boxes from the detector's probability map using
// connected components (DB post-processing with axis-aligned boxes).
func detBoxes(prob []float32, w, h int, sx, sy float64, p Params, imgW, imgH int) []box {
	mask := make([]bool, w*h)
	for i, v := range prob {
		mask[i] = float64(v) > p.DetThresh
	}
	seen := make([]bool, w*h)
	var boxes []box
	stack := make([]int, 0, 1024)
	for start := range mask {
		if !mask[start] || seen[start] {
			continue
		}
		minX, minY, maxX, maxY := w, h, -1, -1
		sum, n := 0.0, 0
		stack = append(stack[:0], start)
		seen[start] = true
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x, y := i%w, i/w
			minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
			sum += float64(prob[i])
			n++
			for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := x+d[0], y+d[1]
				if nx < 0 || ny < 0 || nx >= w || ny >= h {
					continue
				}
				j := ny*w + nx
				if mask[j] && !seen[j] {
					seen[j] = true
					stack = append(stack, j)
				}
			}
		}
		bw, bh := float64(maxX-minX+1), float64(maxY-minY+1)
		if min(bw, bh) < 3 || n < 10 {
			continue
		}
		score := sum / float64(n)
		if score < p.BoxThresh {
			continue
		}
		// Unclip: the detector predicts shrunken text kernels.
		d := bw * bh * p.UnclipRatio / (2 * (bw + bh))
		b := box{
			x0: math.Max(0, (float64(minX)-d)*sx), y0: math.Max(0, (float64(minY)-d)*sy),
			x1: math.Min(float64(imgW), (float64(maxX+1)+d)*sx), y1: math.Min(float64(imgH), (float64(maxY+1)+d)*sy),
			score: score,
		}
		boxes = append(boxes, b)
	}
	// Reading order: top-to-bottom, then left-to-right for boxes on a line.
	sort.Slice(boxes, func(i, j int) bool {
		a, b := boxes[i], boxes[j]
		if math.Abs(a.y0-b.y0) < 0.5*math.Min(a.y1-a.y0, b.y1-b.y0) {
			return a.x0 < b.x0
		}
		return a.y0 < b.y0
	})
	return boxes
}

// recInput crops and resizes boxes into one batch tensor (N×3×H×W),
// normalised to [-1, 1], BGR order, right-padded with zeros.
func recInput(m *rgbImage, boxes []box, height, maxW int) (data []float32, width int) {
	widths := make([]int, len(boxes))
	for i, b := range boxes {
		ratio := (b.x1 - b.x0) / math.Max(1, b.y1-b.y0)
		widths[i] = min(maxW, max(8, int(math.Ceil(float64(height)*ratio))))
		width = max(width, widths[i])
	}
	width = max(width, 320)
	per := 3 * height * width
	data = make([]float32, len(boxes)*per)
	for i, b := range boxes {
		bw, bh := b.x1-b.x0, b.y1-b.y0
		for y := 0; y < height; y++ {
			srcY := b.y0 + (float64(y)+0.5)*bh/float64(height) - 0.5
			for x := 0; x < widths[i]; x++ {
				srcX := b.x0 + (float64(x)+0.5)*bw/float64(widths[i]) - 0.5
				for c := 0; c < 3; c++ {
					v := m.sample(srcX, srcY, 2-c) / 255
					data[i*per+c*height*width+y*width+x] = float32((v - 0.5) / 0.5)
				}
			}
		}
	}
	return data, width
}

// ctcDecode greedily decodes one sequence of class probabilities
// (T×C, row-major). Class 0 is the CTC blank; class k maps to dict[k-1].
func ctcDecode(probs []float32, t, c int, dict []string) (string, float64) {
	var sb strings.Builder
	prev := -1
	confSum, n := 0.0, 0
	for i := 0; i < t; i++ {
		row := probs[i*c : (i+1)*c]
		best, bp := 0, row[0]
		for k := 1; k < c; k++ {
			if row[k] > bp {
				best, bp = k, row[k]
			}
		}
		if best != 0 && best != prev && best-1 < len(dict) {
			sb.WriteString(dict[best-1])
			confSum += float64(bp)
			n++
		}
		prev = best
	}
	if n == 0 {
		return "", 0
	}
	return strings.TrimSpace(sb.String()), confSum / float64(n)
}

// loadDict parses a PaddleOCR character dictionary (one entry per line).
// When the model has one more class than blank+dict, a space is appended
// (models trained with use_space_char).
func loadDict(text string, classes int) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if classes == len(lines)+2 {
		lines = append(lines, " ")
	}
	return lines
}
