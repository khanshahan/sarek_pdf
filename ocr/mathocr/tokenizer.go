// Package mathocr recognises formulas in images: PP-DocLayoutV3
// (Apache-2.0) finds formula regions and Pix2Text-MFR (MIT, a TrOCR
// encoder-decoder) transcribes each into LaTeX. Inference runs on ONNX
// Runtime (MIT) and is compiled only with the build tag "onnx".
package mathocr

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// tokenizer decodes byte-level BPE token ids (GPT-2 byte mapping).
type tokenizer struct {
	vocab   []string // id -> token
	special map[int]bool
	byteOf  map[rune]byte
}

func loadTokenizer(path string) (*tokenizer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tj struct {
		Model struct {
			Type  string         `json:"type"`
			Vocab map[string]int `json:"vocab"`
		} `json:"model"`
		AddedTokens []struct {
			ID      int    `json:"id"`
			Content string `json:"content"`
			Special bool   `json:"special"`
		} `json:"added_tokens"`
	}
	if err := json.Unmarshal(data, &tj); err != nil {
		return nil, fmt.Errorf("mathocr: tokenizer: %w", err)
	}
	if tj.Model.Type != "BPE" || len(tj.Model.Vocab) == 0 {
		return nil, fmt.Errorf("mathocr: unsupported tokenizer %q", tj.Model.Type)
	}
	t := &tokenizer{special: map[int]bool{}, byteOf: byteDecoder()}
	size := 0
	for _, id := range tj.Model.Vocab {
		size = max(size, id+1)
	}
	for _, a := range tj.AddedTokens {
		size = max(size, a.ID+1)
	}
	t.vocab = make([]string, size)
	for tok, id := range tj.Model.Vocab {
		t.vocab[id] = tok
	}
	for _, a := range tj.AddedTokens {
		t.vocab[a.ID] = a.Content
		if a.Special {
			t.special[a.ID] = true
		}
	}
	return t, nil
}

// byteDecoder inverts the GPT-2 byte-to-unicode table: printable bytes map
// to themselves, the others to U+0100 onwards.
func byteDecoder() map[rune]byte {
	m := map[rune]byte{}
	n := 0
	for b := 0; b < 256; b++ {
		if b >= '!' && b <= '~' || b >= 0xA1 && b <= 0xAC || b >= 0xAE && b <= 0xFF {
			m[rune(b)] = byte(b)
			continue
		}
		m[rune(256+n)] = byte(b)
		n++
	}
	return m
}

// decode turns token ids into text.
func (t *tokenizer) decode(ids []int) string {
	var b []byte
	for _, id := range ids {
		if id < 0 || id >= len(t.vocab) || t.special[id] {
			continue
		}
		for _, r := range t.vocab[id] {
			if v, ok := t.byteOf[r]; ok {
				b = append(b, v)
			} else {
				b = utf8.AppendRune(b, r)
			}
		}
	}
	return strings.TrimSpace(string(b))
}

// repeating reports a generation stuck in a loop: the last tokens are a
// short pattern repeated many times (a typical recogniser failure).
func repeating(ids []int) bool {
	const window = 48
	if len(ids) < window {
		return false
	}
	tail := ids[len(ids)-window:]
	for period := 1; period <= 8; period++ {
		ok := true
		for i := period; i < len(tail); i++ {
			if tail[i] != tail[i-period] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
