package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/khanshahan/sarek_pdf/correct/llm"
	"github.com/khanshahan/sarek_pdf/correct/llm/openai"
)

// fakeServer answers like an OpenAI-compatible endpoint: it repairs a
// split word, and "hallucinates" by appending text for other inputs.
func fakeServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages    []struct{ Role, Content string } `json:"messages"`
			Temperature float64                          `json:"temperature"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Temperature != 0 {
			t.Errorf("bad request: %v temp=%v", err, req.Temperature)
		}
		in := req.Messages[len(req.Messages)-1].Content
		out := strings.ReplaceAll(in, "exam ple", "example")
		if out == in {
			out = in + " which was approved by the board"
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": out}}}})
	}))
}

func TestCorrectorAppliesOnlyVerifiedChanges(t *testing.T) {
	srv := fakeServer(t)
	defer srv.Close()
	c := &llm.Corrector{Engine: openai.New(srv.URL, "test"), Concurrency: 4}
	in := []string{
		"This exam ple shows a split word",
		"The budget was rejected last year",
		"ok",
	}
	out, err := c.Correct(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out[0] != "This example shows a split word" {
		t.Errorf("repair not applied: %q", out[0])
	}
	if out[1] != in[1] {
		t.Errorf("hallucination accepted: %q", out[1])
	}
	if out[2] != "ok" {
		t.Errorf("short text changed: %q", out[2])
	}
}
