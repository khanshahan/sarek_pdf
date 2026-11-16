//go:build onnx

// Package ort is a minimal binding to the ONNX Runtime C API (MIT) for
// models with several float32/int64 inputs. Compiled only with the build
// tag "onnx"; sessions are safe for concurrent Run calls.
package ort

/*
#cgo LDFLAGS: -lonnxruntime
#include <stdlib.h>
#include <string.h>
#include "onnxruntime_c_api.h"

static const OrtApi* g_api = NULL;

static const char* api_init(void) {
	if (!g_api) {
		const OrtApiBase* base = OrtGetApiBase();
		if (!base) return "OrtGetApiBase failed";
		g_api = base->GetApi(ORT_API_VERSION);
		if (!g_api) return "ONNX Runtime library older than its headers";
	}
	return NULL;
}

static char* st_msg(OrtStatus* st) {
	if (!st) return NULL;
	const char* m = g_api->GetErrorMessage(st);
	char* out = strdup(m ? m : "onnxruntime error");
	g_api->ReleaseStatus(st);
	return out;
}

static void st_ignore(OrtStatus* st) { free(st_msg(st)); }

static char* env_create(OrtEnv** env) {
	return st_msg(g_api->CreateEnv(ORT_LOGGING_LEVEL_WARNING, "sarek_pdf", env));
}

static char* session_create(OrtEnv* env, const char* path, int threads, OrtSession** s) {
	OrtSessionOptions* so = NULL;
	char* e = st_msg(g_api->CreateSessionOptions(&so));
	if (e) return e;
	if (threads > 0) st_ignore(g_api->SetIntraOpNumThreads(so, threads));
	st_ignore(g_api->SetSessionGraphOptimizationLevel(so, ORT_ENABLE_ALL));
	e = st_msg(g_api->CreateSession(env, path, so, s));
	g_api->ReleaseSessionOptions(so);
	return e;
}

// io_name returns the i-th input (is_input=1) or output name (malloc'ed).
static char* io_name(OrtSession* s, int is_input, size_t i, char** name) {
	OrtAllocator* a = NULL;
	char* e = st_msg(g_api->GetAllocatorWithDefaultOptions(&a));
	if (e) return e;
	char* n = NULL;
	e = st_msg(is_input ? g_api->SessionGetInputName(s, i, a, &n) : g_api->SessionGetOutputName(s, i, a, &n));
	if (e) return e;
	*name = strdup(n);
	st_ignore(g_api->AllocatorFree(a, n));
	return NULL;
}

static char* io_count(OrtSession* s, size_t* nin, size_t* nout) {
	char* e = st_msg(g_api->SessionGetInputCount(s, nin));
	if (e) return e;
	return st_msg(g_api->SessionGetOutputCount(s, nout));
}

// run executes the session with n inputs (type 1 = float32, 7 = int64)
// and copies output `out_index` (float32) into a malloc'ed buffer.
static char* run(OrtSession* s, size_t n, const char** names, const int* types, void** datas,
                 const int64_t* shapes, const size_t* ndims, const char* out_name,
                 float** out, int64_t* out_shape, size_t* out_ndim) {
	OrtMemoryInfo* mi = NULL;
	char* e = st_msg(g_api->CreateCpuMemoryInfo(OrtArenaAllocator, OrtMemTypeDefault, &mi));
	if (e) return e;
	OrtValue* vals[8] = {0};
	if (n > 8) n = 8;
	size_t off = 0;
	for (size_t i = 0; i < n && !e; i++) {
		size_t count = 1;
		for (size_t k = 0; k < ndims[i]; k++) count *= (size_t)shapes[off + k];
		size_t elem = types[i] == 7 ? 8 : 4;
		ONNXTensorElementDataType t = types[i] == 7 ? ONNX_TENSOR_ELEMENT_DATA_TYPE_INT64 : ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT;
		e = st_msg(g_api->CreateTensorWithDataAsOrtValue(mi, datas[i], count * elem, shapes + off, ndims[i], t, &vals[i]));
		off += ndims[i];
	}
	g_api->ReleaseMemoryInfo(mi);
	OrtValue* output = NULL;
	if (!e) e = st_msg(g_api->Run(s, NULL, names, (const OrtValue* const*)vals, n, &out_name, 1, &output));
	for (size_t i = 0; i < n; i++) if (vals[i]) g_api->ReleaseValue(vals[i]);
	if (e) return e;
	OrtTensorTypeAndShapeInfo* info = NULL;
	e = st_msg(g_api->GetTensorTypeAndShape(output, &info));
	size_t count = 0;
	if (!e) {
		size_t nd = 0;
		e = st_msg(g_api->GetDimensionsCount(info, &nd));
		if (nd > 8) nd = 8;
		if (!e) e = st_msg(g_api->GetDimensions(info, out_shape, nd));
		*out_ndim = nd;
		if (!e) e = st_msg(g_api->GetTensorShapeElementCount(info, &count));
		g_api->ReleaseTensorTypeAndShapeInfo(info);
	}
	float* src = NULL;
	if (!e) e = st_msg(g_api->GetTensorMutableData(output, (void**)&src));
	if (!e) {
		*out = (float*)malloc(count * sizeof(float) + 1);
		memcpy(*out, src, count * sizeof(float));
	}
	g_api->ReleaseValue(output);
	return e;
}

static void session_release(OrtSession* s) { if (s) g_api->ReleaseSession(s); }
static void env_release(OrtEnv* e) { if (e) g_api->ReleaseEnv(e); }
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

var (
	envOnce sync.Once
	env     *C.OrtEnv
	envErr  error
)

func initEnv() error {
	envOnce.Do(func() {
		if msg := C.api_init(); msg != nil {
			envErr = errors.New(C.GoString(msg))
			return
		}
		if msg := C.env_create(&env); msg != nil {
			envErr = cerr(msg)
		}
	})
	return envErr
}

func cerr(msg *C.char) error {
	defer C.free(unsafe.Pointer(msg))
	return errors.New(C.GoString(msg))
}

// Session is a loaded model.
type Session struct {
	s       *C.OrtSession
	Inputs  []string
	Outputs []string
	mu      sync.RWMutex
	closed  bool
}

// Open loads a model file.
func Open(path string, threads int) (*Session, error) {
	if err := initEnv(); err != nil {
		return nil, err
	}
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	s := &Session{}
	if msg := C.session_create(env, cp, C.int(threads), &s.s); msg != nil {
		return nil, fmt.Errorf("ort: %s: %w", path, cerr(msg))
	}
	var nin, nout C.size_t
	if msg := C.io_count(s.s, &nin, &nout); msg != nil {
		s.Close()
		return nil, cerr(msg)
	}
	for i := 0; i < int(nin)+int(nout); i++ {
		isIn, k := 1, i
		if i >= int(nin) {
			isIn, k = 0, i-int(nin)
		}
		var n *C.char
		if msg := C.io_name(s.s, C.int(isIn), C.size_t(k), &n); msg != nil {
			s.Close()
			return nil, cerr(msg)
		}
		if isIn == 1 {
			s.Inputs = append(s.Inputs, C.GoString(n))
		} else {
			s.Outputs = append(s.Outputs, C.GoString(n))
		}
		C.free(unsafe.Pointer(n))
	}
	return s, nil
}

// Tensor is a model input: Float32 or Int64 data with a shape.
type Tensor struct {
	Name    string
	Float32 []float32
	Int64   []int64
	Shape   []int64
}

// Run executes the model and returns the named output (float32).
func (s *Session) Run(inputs []Tensor, output string) ([]float32, []int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, nil, errors.New("ort: session closed")
	}
	n := len(inputs)
	if n == 0 || n > 8 {
		return nil, nil, errors.New("ort: 1..8 inputs supported")
	}
	// C-allocated copies: no Go pointers are stored in C memory.
	names := (*[8]*C.char)(C.malloc(C.size_t(8 * unsafe.Sizeof((*C.char)(nil)))))
	datas := (*[8]unsafe.Pointer)(C.malloc(C.size_t(8 * unsafe.Sizeof(unsafe.Pointer(nil)))))
	types := make([]C.int, n)
	ndims := make([]C.size_t, n)
	var shapes []C.int64_t
	defer func() {
		for i := 0; i < n; i++ {
			C.free(unsafe.Pointer(names[i]))
			C.free(datas[i])
		}
		C.free(unsafe.Pointer(names))
		C.free(unsafe.Pointer(datas))
	}()
	for i, in := range inputs {
		names[i] = C.CString(in.Name)
		switch {
		case in.Int64 != nil:
			types[i] = 7
			datas[i] = C.malloc(C.size_t(max(len(in.Int64), 1) * 8))
			copy(unsafe.Slice((*int64)(datas[i]), len(in.Int64)), in.Int64)
		default:
			types[i] = 1
			datas[i] = C.malloc(C.size_t(max(len(in.Float32), 1) * 4))
			copy(unsafe.Slice((*float32)(datas[i]), len(in.Float32)), in.Float32)
		}
		ndims[i] = C.size_t(len(in.Shape))
		for _, d := range in.Shape {
			shapes = append(shapes, C.int64_t(d))
		}
	}
	on := C.CString(output)
	defer C.free(unsafe.Pointer(on))
	var out *C.float
	var outShape [8]C.int64_t
	var nd C.size_t
	msg := C.run(s.s, C.size_t(n), (**C.char)(unsafe.Pointer(names)), &types[0], (*unsafe.Pointer)(unsafe.Pointer(datas)),
		&shapes[0], &ndims[0], on, &out, &outShape[0], &nd)
	if msg != nil {
		return nil, nil, cerr(msg)
	}
	defer C.free(unsafe.Pointer(out))
	dims := make([]int64, nd)
	count := int64(1)
	for i := range dims {
		dims[i] = int64(outShape[i])
		count *= dims[i]
	}
	res := make([]float32, count)
	copy(res, unsafe.Slice((*float32)(unsafe.Pointer(out)), count))
	return res, dims, nil
}

// Close releases the session.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		C.session_release(s.s)
	}
}
