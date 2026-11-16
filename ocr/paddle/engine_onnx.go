//go:build onnx

package paddle

/*
#cgo LDFLAGS: -lonnxruntime
#include <stdlib.h>
#include <string.h>
#include "onnxruntime_c_api.h"

static const OrtApi* g_ort = NULL;

static const char* ort_init(void) {
	if (!g_ort) {
		const OrtApiBase* base = OrtGetApiBase();
		if (!base) return "OrtGetApiBase failed";
		g_ort = base->GetApi(ORT_API_VERSION);
		if (!g_ort) return "ONNX Runtime version mismatch (library older than headers)";
	}
	return NULL;
}

// err converts a status into a malloc'ed message (NULL on success).
static char* err(OrtStatus* st) {
	if (!st) return NULL;
	const char* m = g_ort->GetErrorMessage(st);
	char* out = strdup(m ? m : "onnxruntime error");
	g_ort->ReleaseStatus(st);
	return out;
}

// ignore releases a status whose failure is not fatal.
static void ignore(OrtStatus* st) { free(err(st)); }

static char* ort_env(OrtEnv** env) {
	return err(g_ort->CreateEnv(ORT_LOGGING_LEVEL_WARNING, "sarek_pdf", env));
}

static char* ort_session(OrtEnv* env, const char* path, int threads, OrtSession** sess,
                         char** in_name, char** out_name) {
	OrtSessionOptions* so = NULL;
	char* e = err(g_ort->CreateSessionOptions(&so));
	if (e) return e;
	if (threads > 0) ignore(g_ort->SetIntraOpNumThreads(so, threads));
	ignore(g_ort->SetSessionGraphOptimizationLevel(so, ORT_ENABLE_ALL));
	e = err(g_ort->CreateSession(env, path, so, sess));
	g_ort->ReleaseSessionOptions(so);
	if (e) return e;
	OrtAllocator* alloc = NULL;
	e = err(g_ort->GetAllocatorWithDefaultOptions(&alloc));
	if (e) return e;
	char* n = NULL;
	e = err(g_ort->SessionGetInputName(*sess, 0, alloc, &n));
	if (e) return e;
	*in_name = strdup(n);
	ignore(g_ort->AllocatorFree(alloc, n));
	e = err(g_ort->SessionGetOutputName(*sess, 0, alloc, &n));
	if (e) return e;
	*out_name = strdup(n);
	ignore(g_ort->AllocatorFree(alloc, n));
	return NULL;
}

// ort_run runs a single-input/single-output float model. The output is
// copied into a malloc'ed buffer (*out) with its shape in out_shape.
static char* ort_run(OrtSession* sess, const char* in_name, const char* out_name,
                     float* data, const int64_t* shape, size_t ndim,
                     float** out, int64_t* out_shape, size_t* out_ndim) {
	OrtMemoryInfo* mi = NULL;
	char* e = err(g_ort->CreateCpuMemoryInfo(OrtArenaAllocator, OrtMemTypeDefault, &mi));
	if (e) return e;
	size_t n = 1;
	for (size_t i = 0; i < ndim; i++) n *= (size_t)shape[i];
	OrtValue* input = NULL;
	e = err(g_ort->CreateTensorWithDataAsOrtValue(mi, data, n * sizeof(float), shape, ndim,
	                                             ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT, &input));
	g_ort->ReleaseMemoryInfo(mi);
	if (e) return e;
	OrtValue* output = NULL;
	e = err(g_ort->Run(sess, NULL, &in_name, (const OrtValue* const*)&input, 1, &out_name, 1, &output));
	g_ort->ReleaseValue(input);
	if (e) return e;
	OrtTensorTypeAndShapeInfo* info = NULL;
	e = err(g_ort->GetTensorTypeAndShape(output, &info));
	if (!e) {
		size_t nd = 0;
		e = err(g_ort->GetDimensionsCount(info, &nd));
		if (nd > 8) nd = 8;
		if (!e) e = err(g_ort->GetDimensions(info, out_shape, nd));
		*out_ndim = nd;
		size_t count = 0;
		if (!e) e = err(g_ort->GetTensorShapeElementCount(info, &count));
		g_ort->ReleaseTensorTypeAndShapeInfo(info);
		float* src = NULL;
		if (!e) e = err(g_ort->GetTensorMutableData(output, (void**)&src));
		if (!e) {
			*out = (float*)malloc(count * sizeof(float));
			memcpy(*out, src, count * sizeof(float));
		}
	}
	g_ort->ReleaseValue(output);
	return e;
}

static void ort_release_session(OrtSession* s) { if (s) g_ort->ReleaseSession(s); }
static void ort_release_env(OrtEnv* e) { if (e) g_ort->ReleaseEnv(e); }
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"github.com/khanshahan/sarek_pdf/ocr"
)

// Options configures the engine.
type Options struct {
	DetModel string // detection model (.onnx), e.g. PP-OCRv5 det
	RecModel string // recognition model (.onnx) for the document language
	Dict     string // character dictionary matching RecModel
	Threads  int    // intra-op threads per session (default NumCPU)
	Params   Params
}

// Engine implements ocr.Engine. It is safe for concurrent use.
type Engine struct {
	env      *C.OrtEnv
	det, rec session
	dictText string
	params   Params

	dictOnce sync.Once
	dict     []string
	closeMu  sync.RWMutex
	closed   bool
}

type session struct {
	s       *C.OrtSession
	in, out *C.char
}

var _ ocr.Engine = (*Engine)(nil)

// New loads the models.
func New(o Options) (*Engine, error) {
	if msg := C.ort_init(); msg != nil {
		return nil, errors.New(C.GoString(msg))
	}
	dict, err := os.ReadFile(o.Dict)
	if err != nil {
		return nil, fmt.Errorf("paddle: dictionary: %w", err)
	}
	if o.Threads <= 0 {
		o.Threads = runtime.NumCPU()
	}
	o.Params.defaults()
	e := &Engine{dictText: string(dict), params: o.Params}
	if msg := C.ort_env(&e.env); msg != nil {
		return nil, cerr(msg)
	}
	if e.det, err = e.open(o.DetModel, o.Threads); err != nil {
		e.Close()
		return nil, fmt.Errorf("paddle: detection model: %w", err)
	}
	if e.rec, err = e.open(o.RecModel, o.Threads); err != nil {
		e.Close()
		return nil, fmt.Errorf("paddle: recognition model: %w", err)
	}
	return e, nil
}

func cerr(msg *C.char) error {
	defer C.free(unsafe.Pointer(msg))
	return errors.New(C.GoString(msg))
}

func (e *Engine) open(path string, threads int) (session, error) {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	var s session
	if msg := C.ort_session(e.env, cp, C.int(threads), &s.s, &s.in, &s.out); msg != nil {
		return s, cerr(msg)
	}
	return s, nil
}

// Name implements ocr.Engine.
func (e *Engine) Name() string { return "paddleocr-onnx" }

// Close releases the sessions. Recognize must not be called afterwards.
func (e *Engine) Close() {
	e.closeMu.Lock()
	defer e.closeMu.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	for _, s := range []session{e.det, e.rec} {
		C.ort_release_session(s.s)
		C.free(unsafe.Pointer(s.in))
		C.free(unsafe.Pointer(s.out))
	}
	C.ort_release_env(e.env)
}

func (s session) run(data []float32, shape []int64) ([]float32, []int64, error) {
	if len(data) == 0 {
		return nil, nil, errors.New("paddle: empty input")
	}
	var out *C.float
	var outShape [8]C.int64_t
	var nd C.size_t
	msg := C.ort_run(s.s, s.in, s.out, (*C.float)(unsafe.Pointer(&data[0])),
		(*C.int64_t)(unsafe.Pointer(&shape[0])), C.size_t(len(shape)), &out, &outShape[0], &nd)
	if msg != nil {
		return nil, nil, cerr(msg)
	}
	defer C.free(unsafe.Pointer(out))
	dims := make([]int64, nd)
	n := int64(1)
	for i := range dims {
		dims[i] = int64(outShape[i])
		n *= dims[i]
	}
	res := make([]float32, n)
	copy(res, unsafe.Slice((*float32)(unsafe.Pointer(out)), n))
	return res, dims, nil
}

// Recognize implements ocr.Engine.
func (e *Engine) Recognize(ctx context.Context, img image.Image) ([]ocr.Line, error) {
	e.closeMu.RLock()
	defer e.closeMu.RUnlock()
	if e.closed {
		return nil, errors.New("paddle: engine closed")
	}
	m := toRGB(img)
	if m.w < 8 || m.h < 8 {
		return nil, nil
	}
	data, w, h, sx, sy := detInput(m, e.params.DetMaxSide)
	prob, dims, err := e.det.run(data, []int64{1, 3, int64(h), int64(w)})
	if err != nil {
		return nil, err
	}
	if len(dims) != 4 || dims[2] != int64(h) || dims[3] != int64(w) {
		return nil, fmt.Errorf("paddle: unexpected detector output shape %v", dims)
	}
	boxes := detBoxes(prob[:w*h], w, h, sx, sy, e.params, m.w, m.h)
	var lines []ocr.Line
	for i := 0; i < len(boxes); i += e.params.RecBatchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch := boxes[i:min(i+e.params.RecBatchSize, len(boxes))]
		in, width := recInput(m, batch, e.params.RecHeight, e.params.RecMaxWidth)
		out, od, err := e.rec.run(in, []int64{int64(len(batch)), 3, int64(e.params.RecHeight), int64(width)})
		if err != nil {
			return nil, err
		}
		if len(od) != 3 {
			return nil, fmt.Errorf("paddle: unexpected recognizer output shape %v", od)
		}
		t, c := int(od[1]), int(od[2])
		e.dictOnce.Do(func() { e.dict = loadDict(e.dictText, c) })
		for k, b := range batch {
			text, conf := ctcDecode(out[k*t*c:(k+1)*t*c], t, c, e.dict)
			if text == "" {
				continue
			}
			x0, y0, x1, y1 := int(b.x0), int(b.y0), int(b.x1+0.5), int(b.y1+0.5)
			lines = append(lines, ocr.Line{Text: text, Confidence: conf,
				Quad: [4]image.Point{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}})
		}
	}
	return lines, nil
}
