package paddle

import (
	"image"
	"testing"
)

func TestCTCDecode(t *testing.T) {
	dict := []string{"a", "b", " "}
	// Classes: 0 blank, 1 a, 2 b, 3 space. Sequence: a a blank a b space b.
	seq := []int{1, 1, 0, 1, 2, 3, 2}
	probs := make([]float32, len(seq)*4)
	for i, c := range seq {
		probs[i*4+c] = 0.9
	}
	text, conf := ctcDecode(probs, len(seq), 4, dict)
	if text != "aab b" || conf < 0.89 || conf > 0.91 {
		t.Errorf("ctcDecode = %q, %v", text, conf)
	}
}

func TestLoadDictSpace(t *testing.T) {
	if d := loadDict("a\nb\n", 4); len(d) != 3 || d[2] != " " {
		t.Errorf("dict with space = %q", d)
	}
	if d := loadDict("a\r\nb\r\n", 3); len(d) != 2 {
		t.Errorf("dict without space = %q", d)
	}
}

func TestDetBoxes(t *testing.T) {
	// Two separate "text kernels" on a 64×32 map, scaled 2× to the image.
	w, h := 64, 32
	prob := make([]float32, w*h)
	fill := func(x0, y0, x1, y1 int) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				prob[y*w+x] = 0.9
			}
		}
	}
	fill(4, 4, 30, 10)
	fill(36, 20, 60, 26)
	var p Params
	p.defaults()
	boxes := detBoxes(prob, w, h, 2, 2, p, 128, 64)
	if len(boxes) != 2 {
		t.Fatalf("boxes = %+v", boxes)
	}
	b := boxes[0]
	if b.x0 >= 8 || b.y0 >= 8 || b.x1 <= 60 || b.y1 <= 20 || b.y1 > 30 {
		t.Errorf("first box (unclipped, scaled) = %+v", b)
	}
}

func TestDetInputShape(t *testing.T) {
	g := image.NewGray(image.Rect(0, 0, 1700, 2200))
	data, w, h, sx, sy := detInput(toRGB(g), 1536)
	if w%32 != 0 || h%32 != 0 || max(w, h) > 1536+32 || len(data) != 3*w*h || sx <= 1 || sy <= 1 {
		t.Errorf("detInput w=%d h=%d sx=%v sy=%v", w, h, sx, sy)
	}
}
