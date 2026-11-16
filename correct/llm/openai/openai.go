// Package openai is an llm.Engine for any OpenAI-compatible chat
// completions endpoint, such as llama.cpp's llama-server (MIT):
//
//	llama-server -m qwen2.5-1.5b-instruct-q4_k_m.gguf --port 8080
//
// It uses only net/http from the standard library.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Engine calls POST {BaseURL}/chat/completions.
type Engine struct {
	BaseURL string // e.g. "http://127.0.0.1:8080/v1"
	Model   string
	APIKey  string // optional
	Client  *http.Client
}

// New returns an engine with a sensible HTTP client.
func New(baseURL, model string) *Engine {
	return &Engine{BaseURL: strings.TrimRight(baseURL, "/"), Model: model,
		Client: &http.Client{Timeout: 120 * time.Second}}
}

type msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Model       string  `json:"model,omitempty"`
	Messages    []msg   `json:"messages"`
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	MaxTokens   int     `json:"max_tokens"`
	Seed        int     `json:"seed"`
	Stream      bool    `json:"stream"`
}

type response struct {
	Choices []struct {
		Message msg `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete implements llm.Engine with deterministic (greedy) decoding.
func (e *Engine) Complete(ctx context.Context, system, user string, maxTokens int) (string, error) {
	body, _ := json.Marshal(request{
		Model: e.Model, Messages: []msg{{"system", system}, {"user", user}},
		Temperature: 0, TopP: 1, MaxTokens: maxTokens, Seed: 0,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}
	client := e.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("openai engine: HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(data[:min(len(data), 300)]))
	}
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return "", err
	}
	if r.Error != nil {
		return "", errors.New("openai engine: " + r.Error.Message)
	}
	if len(r.Choices) == 0 {
		return "", errors.New("openai engine: empty response")
	}
	return r.Choices[0].Message.Content, nil
}
