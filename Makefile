GO ?= go
UNAME := $(shell uname -s)
ifeq ($(UNAME),Darwin)
LIB := build/libsarekpdf.dylib
else ifeq ($(OS),Windows_NT)
LIB := build/sarekpdf.dll
else
LIB := build/libsarekpdf.so
endif

.PHONY: all test fuzz bench lib cli clean

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

clean:
	rm -rf build
