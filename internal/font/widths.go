package font

import "strings"

// Approximate advance widths (1/1000 em) for ASCII 32..126 of the common
// standard fonts, used only when a PDF omits /Widths (standard-14 fonts).
// Index 0 holds the default for non-ASCII codes. Values affect layout
// geometry only, never extracted text.
var (
	helvWidths  [128]float64
	timesWidths [128]float64
)

func init() {
	helv := []float64{278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278,
		556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556,
		1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778,
		667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556,
		333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556,
		556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584}
	times := []float64{250, 333, 408, 500, 500, 833, 778, 180, 333, 333, 500, 564, 250, 333, 250, 278,
		500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 278, 278, 564, 564, 564, 444,
		921, 722, 667, 667, 722, 611, 556, 722, 722, 333, 389, 722, 611, 889, 722, 722,
		556, 722, 667, 556, 611, 722, 722, 944, 722, 722, 611, 333, 278, 333, 469, 500,
		333, 444, 500, 444, 500, 444, 333, 500, 500, 278, 278, 500, 278, 778, 500, 500,
		500, 500, 333, 389, 278, 500, 500, 722, 500, 500, 444, 480, 200, 480, 541}
	copy(helvWidths[32:], helv)
	copy(timesWidths[32:], times)
	helvWidths[0], timesWidths[0] = 556, 500
}

var courierWidths = func() (w [128]float64) {
	for i := range w {
		w[i] = 600
	}
	return
}()

func standardWidths(name string) *[128]float64 {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "courier") || strings.Contains(n, "mono"):
		return &courierWidths
	case strings.Contains(n, "times"):
		return &timesWidths
	case strings.Contains(n, "helvetica") || strings.Contains(n, "arial"):
		return &helvWidths
	}
	return nil
}
