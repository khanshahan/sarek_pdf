//go:build llamacpp

// Package llamacpp is an in-process llm.Engine backed by llama.cpp
// (MIT license) through cgo. It is compiled only with the build tag
// "llamacpp" so the core library stays pure Go:
//
//	CGO_CFLAGS="-I/path/to/llama.cpp/include -I/path/to/llama.cpp/ggml/include" \
//	CGO_LDFLAGS="-L/path/to/llama.cpp/build/bin -lllama -lggml -lggml-base" \
//	go build -tags llamacpp ./...
//
// Written against the llama.cpp C API as of 2025 (llama_model_load_from_file,
// llama_init_from_model, llama_memory_clear). Pin your llama.cpp version.
package llamacpp

/*
#cgo LDFLAGS: -lllama -lggml -lggml-base
#include <stdlib.h>
#include <string.h>
#include "llama.h"
*/
import "C"

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"unsafe"
)

// Options configures the engine.
type Options struct {
	ModelPath   string // GGUF file
	ContextSize int    // tokens per context (default 4096)
	Contexts    int    // parallel contexts (default 1)
	Threads     int    // CPU threads per context (default NumCPU/Contexts)
	GPULayers   int    // layers to offload (default 0)
}

// Engine runs a GGUF model in-process. Safe for concurrent use: calls are
// distributed over a pool of llama contexts.
type Engine struct {
	model *C.struct_llama_model
	vocab *C.struct_llama_vocab
	tmpl  *C.char
	pool  chan *C.struct_llama_context
	all   []*C.struct_llama_context
	nctx  int
	once  sync.Once
}

var backendOnce sync.Once

// New loads a model.
func New(o Options) (*Engine, error) {
	backendOnce.Do(func() { C.llama_backend_init() })
	if o.ContextSize <= 0 {
		o.ContextSize = 4096
	}
	if o.Contexts <= 0 {
		o.Contexts = 1
	}
	if o.Threads <= 0 {
		o.Threads = max(1, runtime.NumCPU()/o.Contexts)
	}
	path := C.CString(o.ModelPath)
	defer C.free(unsafe.Pointer(path))
	mp := C.llama_model_default_params()
	mp.n_gpu_layers = C.int32_t(o.GPULayers)
	model := C.llama_model_load_from_file(path, mp)
	if model == nil {
		return nil, errors.New("llamacpp: failed to load model " + o.ModelPath)
	}
	e := &Engine{model: model, vocab: C.llama_model_get_vocab(model), nctx: o.ContextSize,
		pool: make(chan *C.struct_llama_context, o.Contexts)}
	e.tmpl = C.llama_model_chat_template(model, nil)
	for i := 0; i < o.Contexts; i++ {
		cp := C.llama_context_default_params()
		cp.n_ctx = C.uint32_t(o.ContextSize)
		cp.n_batch = C.uint32_t(o.ContextSize)
		cp.n_threads = C.int32_t(o.Threads)
		cp.n_threads_batch = C.int32_t(o.Threads)
		ctx := C.llama_init_from_model(model, cp)
		if ctx == nil {
			e.Close()
			return nil, errors.New("llamacpp: failed to create context")
		}
		e.all = append(e.all, ctx)
		e.pool <- ctx
	}
	return e, nil
}

// Close releases all resources.
func (e *Engine) Close() {
	e.once.Do(func() {
		for _, c := range e.all {
			C.llama_free(c)
		}
		if e.model != nil {
			C.llama_model_free(e.model)
		}
	})
}

func (e *Engine) applyTemplate(system, user string) (string, error) {
	roles := [2]*C.char{C.CString("system"), C.CString("user")}
	contents := [2]*C.char{C.CString(system), C.CString(user)}
	defer func() {
		for i := 0; i < 2; i++ {
			C.free(unsafe.Pointer(roles[i]))
			C.free(unsafe.Pointer(contents[i]))
		}
	}()
	msgs := (*[2]C.struct_llama_chat_message)(C.malloc(C.size_t(2 * unsafe.Sizeof(C.struct_llama_chat_message{}))))
	defer C.free(unsafe.Pointer(msgs))
	for i := 0; i < 2; i++ {
		msgs[i].role = roles[i]
		msgs[i].content = contents[i]
	}
	size := C.int32_t(len(system) + len(user) + 1024)
	for attempt := 0; attempt < 2; attempt++ {
		buf := (*C.char)(C.malloc(C.size_t(size)))
		n := C.llama_chat_apply_template(e.tmpl, &msgs[0], 2, C.bool(true), buf, size)
		if n < 0 {
			C.free(unsafe.Pointer(buf))
			return "", errors.New("llamacpp: chat template not supported by model")
		}
		if n <= size {
			s := C.GoStringN(buf, n)
			C.free(unsafe.Pointer(buf))
			return s, nil
		}
		C.free(unsafe.Pointer(buf))
		size = n + 1
	}
	return "", errors.New("llamacpp: template buffer error")
}

// Complete implements llm.Engine with greedy decoding.
func (e *Engine) Complete(ctx context.Context, system, user string, maxTokens int) (string, error) {
	prompt, err := e.applyTemplate(system, user)
	if err != nil {
		return "", err
	}
	var lc *C.struct_llama_context
	select {
	case lc = <-e.pool:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { e.pool <- lc }()
	C.llama_memory_clear(C.llama_get_memory(lc), C.bool(true))

	cp := C.CString(prompt)
	defer C.free(unsafe.Pointer(cp))
	maxTok := C.int32_t(e.nctx)
	toks := (*C.llama_token)(C.malloc(C.size_t(maxTok) * C.size_t(unsafe.Sizeof(C.llama_token(0)))))
	defer C.free(unsafe.Pointer(toks))
	n := C.llama_tokenize(e.vocab, cp, C.int32_t(len(prompt)), toks, maxTok, C.bool(true), C.bool(true))
	if n < 0 || int(n)+maxTokens > e.nctx {
		return "", errors.New("llamacpp: prompt too long for context")
	}
	if C.llama_decode(lc, C.llama_batch_get_one(toks, n)) != 0 {
		return "", errors.New("llamacpp: decode failed")
	}
	smpl := C.llama_sampler_chain_init(C.llama_sampler_chain_default_params())
	defer C.llama_sampler_free(smpl)
	C.llama_sampler_chain_add(smpl, C.llama_sampler_init_greedy())

	one := (*C.llama_token)(C.malloc(C.size_t(unsafe.Sizeof(C.llama_token(0)))))
	defer C.free(unsafe.Pointer(one))
	piece := (*C.char)(C.malloc(256))
	defer C.free(unsafe.Pointer(piece))
	out := make([]byte, 0, len(user)+64)
	for i := 0; i < maxTokens; i++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		tok := C.llama_sampler_sample(smpl, lc, -1)
		if C.llama_vocab_is_eog(e.vocab, tok) {
			break
		}
		pn := C.llama_token_to_piece(e.vocab, tok, piece, 256, 0, C.bool(false))
		if pn > 0 {
			out = append(out, C.GoBytes(unsafe.Pointer(piece), pn)...)
		}
		*one = tok
		if C.llama_decode(lc, C.llama_batch_get_one(one, 1)) != 0 {
			return "", errors.New("llamacpp: decode failed")
		}
	}
	return string(out), nil
}
