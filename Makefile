GO ?= go
UNAME := $(shell uname -s)
ifeq ($(UNAME),Darwin)
LIB := build/libsarekpdf.dylib
else ifeq ($(OS),Windows_NT)
LIB := build/sarekpdf.dll
else
LIB := build/libsarekpdf.so
endif

# ONNX Runtime (MIT) for the optional OCR engine: set ORT_DIR to an
# extracted onnxruntime release (with include/ and lib/).
ORT_DIR ?=
ORT_ENV = CGO_CFLAGS="-I$(ORT_DIR)/include" CGO_LDFLAGS="-L$(ORT_DIR)/lib -Wl,-rpath,$(ORT_DIR)/lib"

.PHONY: all test fuzz bench lib cli lib-ocr cli-ocr test-ocr clean

all: test lib cli

test:
	$(GO) vet ./...
	$(GO) test -race ./...

fuzz:
	$(GO) test -run=NONE -fuzz=FuzzOpen -fuzztime=60s .

bench:
	$(GO) test -run=NONE -bench=. -benchmem .

lib:
	$(GO) build -trimpath -buildmode=c-shared -o $(LIB) ./capi

cli:
	$(GO) build -trimpath -o build/sarekpdf ./cmd/sarekpdf

lib-ocr:
	$(ORT_ENV) $(GO) build -tags onnx -trimpath -buildmode=c-shared -o $(LIB) ./capi

cli-ocr:
	$(ORT_ENV) $(GO) build -tags onnx -trimpath -o build/sarekpdf ./cmd/sarekpdf

# Needs SAREK_OCR_MODELS and SAREK_SCAN (see ocr_onnx_test.go).
test-ocr:
	$(ORT_ENV) $(GO) test -tags onnx -run OCR -v .

clean:
	rm -rf build
