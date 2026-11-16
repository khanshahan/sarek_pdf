// Package llm adapts any text-generation engine into a correct.Corrector.
//
// The engine is a replaceable module: implement Engine (a single method)
// to plug in a different runtime or model. Two engines ship with the
// library:
//
//   - package openai: any OpenAI-compatible HTTP server (llama.cpp's
//     llama-server, vLLM, LM Studio, …) — no cgo required.
//   - package llamacpp: in-process llama.cpp via cgo (build tag "llamacpp").
//
// Every proposal is checked by correct.Verify; rejected proposals leave the
// original text untouched.
package llm

import (
	"context"
	"strings"
	"sync"
	"unicode"

	"github.com/khanshahan/sarek_pdf/correct"
)

// Engine generates a completion for a chat-style prompt.
// Implementations must be safe for concurrent use.
type Engine interface {
	Complete(ctx context.Context, system, user string, maxTokens int) (string, error)
}

// SystemPrompt instructs the model to perform only local repairs.
const SystemPrompt = `You repair text that was extracted from a PDF file.
Fix ONLY extraction errors: words broken by stray spaces, words joined
without a space, wrong hyphenation, and single wrong characters (for
example "rn" read instead of "m").
Rules:
- Never add, remove, reorder or replace words.
- Never change numbers, names, units, dates or punctuation meaning.
- Never translate, rephrase, summarise or complete sentences.
- If the text is already correct, return it unchanged.
Return only the repaired text, with no explanations or quotes.`

// Corrector implements correct.Corrector on top of an Engine.
type Corrector struct {
	Engine Engine
	// Concurrency bounds parallel engine calls (default 1).
	Concurrency int
	// MinWords skips paragraphs with fewer words (default 3).
	MinWords int
	// MaxChars skips paragraphs longer than this (default 4000).
	MaxChars int
}

// Correct implements correct.Corrector.
func (c *Corrector) Correct(ctx context.Context, in []string) ([]string, error) {
	out := append([]string(nil), in...)
	conc := max(c.Concurrency, 1)
	minWords := c.MinWords
	if minWords == 0 {
		minWords = 3
	}
	maxChars := c.MaxChars
	if maxChars == 0 {
		maxChars = 4000
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i, text := range in {
		if len(strings.Fields(text)) < minWords || len(text) > maxChars || !hasLetters(text) {
			continue
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return out, ctx.Err()
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(i int, text string) {
			defer func() { <-sem; wg.Done() }()
			maxTok := len(text)/2 + 64
			res, err := c.Engine.Complete(ctx, SystemPrompt, text, maxTok)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			res = cleanResponse(res)
			if _, ok := correct.Verify(text, res); ok {
				out[i] = res
			}
		}(i, text)
	}
	wg.Wait()
	return out, firstErr
}

func hasLetters(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// cleanResponse strips wrappers models sometimes add.
func cleanResponse(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "</think>"); i >= 0 { // reasoning models
		s = strings.TrimSpace(s[i+len("</think>"):])
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"') {
		s = s[1 : len(s)-1]
	}
	return strings.Join(strings.Fields(s), " ")
}
