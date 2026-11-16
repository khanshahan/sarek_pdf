package mathocr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestByteLevelDecode(t *testing.T) {
	// "Ġ" is the GPT-2 mapping of the space byte.
	tk := &tokenizer{vocab: []string{"<s>", "\\frac", "{", "a", "}", "Ġ+", "Ġb"}, special: map[int]bool{0: true}, byteOf: byteDecoder()}
	if got := tk.decode([]int{0, 1, 2, 3, 4, 2, 6, 4, 5, 6}); got != `\frac{a}{ b} + b` {
		t.Errorf("decode = %q", got)
	}
	if !repeating(append(make([]int, 10), []int{5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6, 5, 6}...)) {
		t.Error("loop not detected")
	}
	if repeating([]int{1, 2, 3}) {
		t.Error("short sequence flagged")
	}
}

func TestLoadTokenizer(t *testing.T) {
	dir := os.Getenv("SAREK_MFR_DIR")
	if dir == "" {
		t.Skip("SAREK_MFR_DIR not set")
	}
	tk, err := loadTokenizer(filepath.Join(dir, "tokenizer.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tk.vocab) < 1000 || !tk.special[0] || !tk.special[2] {
		t.Errorf("vocab=%d special=%v", len(tk.vocab), tk.special)
	}
}
