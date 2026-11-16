//go:build onnx

package ort

import (
	"os"
	"testing"
)

// TestSignatures prints model inputs/outputs (set SAREK_ONNX_MODELS to a
// space-separated list of model files).
func TestSignatures(t *testing.T) {
	list := os.Getenv("SAREK_ONNX_MODELS")
	if list == "" {
		t.Skip()
	}
	for _, p := range splitFields(list) {
		s, err := Open(p, 1)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: inputs=%v outputs=%v", p, s.Inputs, s.Outputs)
		s.Close()
	}
}

func splitFields(s string) []string {
	var out []string
	cur := ""
	for _, r := range s + " " {
		if r == ' ' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	return out
}
