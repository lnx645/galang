# Build, test, dan release untuk Garurda.
#
# Target:
#   make build       - Build untuk platform saat ini
#   make release     - Build multi-platform (Linux, Windows, macOS, ARM64)
#   make release-zip - Buat archive zip untuk distribusi
#   make test        - Jalankan semua test
#   make bench       - Jalankan benchmark
#   make ref         - Jalankan perbandingan vs PHP
#   make clean       - Hapus artifacts build

GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)
BUILD_DIR := build
DIST_DIR := dist

# Platform targets
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	windows/amd64 \
	windows/arm64 \
	darwin/amd64 \
	darwin/arm64

.PHONY: all build release release-zip test bench ref clean install

all: build

# Build untuk platform saat ini
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/gar ./cmd/gar

# Install ke $GOPATH/bin atau /usr/local/bin
install: build
	cp $(BUILD_DIR)/gar $(shell go env GOPATH)/bin/gar 2>/dev/null || sudo cp $(BUILD_DIR)/gar /usr/local/bin/gar

# Build untuk semua platform
#
# CGO per platform: linux/amd64 (host, gcc) dan windows/amd64 (butuh
# gcc-mingw-w64-x86-64) dibangun DENGAN CGO sehingga GNE (dlopen/
# LoadLibrary) dan SQLite aktif. Platform lain cross-build CGO=0 →
# binari tetap jalan, tetapi `use` ekstensi native dan database
# menghasilkan galat "butuh CGO" (terdokumentasi di docs/{id,en}).
MINGW ?= x86_64-w64-mingw32-gcc

release:
	@echo "Building for platforms: $(PLATFORMS)"
	@mkdir -p $(DIST_DIR)
	@for platform in $(PLATFORMS); do \
		goos=$${platform%/*}; \
		goarch=$${platform#*/}; \
		cgo=0; \
		cc=; \
		case "$$goos/$$goarch" in \
			linux/amd64|windows/amd64) cgo=1 ;; \
		esac; \
		if [ "$$goos/$$goarch" = "windows/amd64" ]; then cc=$(MINGW); fi; \
		if [ -n "$$cc" ]; then \
			GOOS=$$goos GOARCH=$$goarch CGO_ENABLED=$$cgo CC="$$cc" \
				$(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/gar-$$goos-$$goarch$(if $(filter windows/%,$(platform)),.exe,) ./cmd/gar; \
		else \
			GOOS=$$goos GOARCH=$$goarch CGO_ENABLED=$$cgo \
				$(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/gar-$$goos-$$goarch$(if $(filter windows/%,$(platform)),.exe,) ./cmd/gar; \
		fi; \
		echo "Built: gar-$$goos-$$goarch (CGO=$$cgo)"; \
	done

# Buat archive zip untuk distribusi.
# Tiap zip berisi binari platform + gne.h (header ABI GNE untuk penulis
# ekstensi — sengaja ditempel di setiap zip agar mudah diambil).
release-zip: release
	@cp include/gne.h $(DIST_DIR)/
	@cd $(DIST_DIR) && \
	for f in gar-*; do \
		case $$f in *.zip) continue ;; esac; \
		base=$${f%.*}; \
		zip -q $${base}.zip $$f gne.h; \
		echo "Created: $$base.zip"; \
	done

# Test semua package
test:
	$(GO) test ./...

# Benchmark
bench:
	$(GO) test -run=XXX -bench=. -benchmem ./internal/usecase/interp/

# Perbandingan vs PHP
ref:
	./bench/compare.sh

# Jalankan contoh aplikasi
run:
	./bin/gar run examples/bahasa.ga

# REPL
repl:
	./bin/gar repl

# Clean artifacts
clean:
	rm -rf $(BUILD_DIR) $(DIST_DIR) bin
	$(GO) clean -cache -testcache

# Format code
fmt:
	gofmt -w .

# Vet code
vet:
	$(GO) vet ./...

# Check semua
check: fmt vet test

# Development: auto-reload (butuh entr)
dev:
	entr -r make build run <<< examples/bahasa.ga

# Release notes template
notes:
	@echo "## Garurda $(VERSION)"
	@echo ""
	@echo "### Changes"
	@echo "- "
	@echo ""
	@echo "### Benchmarks"
	@./bench/compare.sh 2>&1 | tail -14
	@echo ""
	@echo "### Downloads"
	@for f in $(DIST_DIR)/*.zip; do \
		echo "- [$$f]($$f)"; \
	done