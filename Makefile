# Build, test, dan benchmark bahasa Garurda.
#
# CATATAN: `go build` memakai -p 1 karena mesin ini hanya punya RAM terbatas;
# build paralel besar bisa kehabisan memori.

GO      ?= go
BIN     := bin/gar
PKG     := ./...
GOFLAGS := -p 1

.PHONY: all build run repl test bench ref vet fmt clean check

all: build test

build:
	$(GO) build $(GOFLAGS) -o $(BIN) ./cmd/gar

run: build
	./$(BIN) run examples/bahasa.ga

repl: build
	./$(BIN) repl

test:
	$(GO) test $(GOFLAGS) $(PKG)

bench:
	$(GO) test $(GOFLAGS) -run=XXX -bench=. -benchmem ./internal/usecase/interp/

vet:
	$(GO) vet $(GOFLAGS) $(PKG)

fmt:
	$(GO) fmt $(PKG)

# ref menjalankan pembanding Garurda vs PHP pada program identik.
ref: build
	go build $(GOFLAGS) -o bin/ref ./bench/ref.go
	./bench/compare.sh

# check adalah gerbang yang harus lolos sebelum commit.
check: vet test

clean:
	rm -rf bin
	$(GO) clean -cache -testcache 2>/dev/null || true
