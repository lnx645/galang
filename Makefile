# Build, test, dan release untuk Garurda.
#
# Target:
#   make build       - Build untuk platform saat ini
#   make release     - Build binari rilis (Linux, Windows, ARM64)
#   make release-zip - Buat archive zip untuk distribusi
#   make ext         - Kompilasi ekstensi resmi (ext/*) lintas platform
#   make pack-ext    - Kemas ekstensi jadi dist/redis.zip & dist/smtp.zip
#   make test        - Jalankan semua test
#   make bench       - Jalankan benchmark
#   make ref         - Jalankan perbandingan vs PHP
#   make clean       - Hapus artifacts build

GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)
BUILD_DIR := build
DIST_DIR := dist

# Platform targets — binari rilis. macOS (darwin) sengaja TIDAK ada di
# sini: cross-build CGO ke darwin mustahil dari Linux, sehingga
# gar-darwin-* dibangun GitHub Actions (release.yml) dan diunggah
# sebagai aset rilis.
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	windows/amd64 \
	windows/arm64

.PHONY: all build release release-zip ext pack-ext test bench ref clean install

all: build

# Build untuk platform saat ini
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/gar ./cmd/gar

# Install ke $GOPATH/bin atau /usr/local/bin
install: build
	cp $(BUILD_DIR)/gar $(shell go env GOPATH)/bin/gar 2>/dev/null || sudo cp $(BUILD_DIR)/gar /usr/local/bin/gar

# Build untuk semua platform binari rilis (lihat PLATFORMS).
#
# CGO per platform: linux/amd64 (host, gcc) dan windows/amd64 (butuh
# gcc-mingw-w64-x86-64) dibangun DENGAN CGO sehingga GNE (dlopen/
# LoadLibrary) dan SQLite aktif. Platform lain cross-build CGO=0 →
# binari tetap jalan, tetapi `use` ekstensi native dan database
# menghasilkan galat "butuh CGO" (terdokumentasi di docs/{id,en}).
# Binari darwin CGO=1 dibangun oleh release.yml di runner macOS.
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
		if [ "$$goos" = "windows" ]; then ext=.exe; else ext=; fi; \
		if [ -n "$$cc" ]; then \
			GOOS=$$goos GOARCH=$$goarch CGO_ENABLED=$$cgo CC="$$cc" \
				$(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/gar-$$goos-$$goarch$$ext ./cmd/gar; \
		else \
			GOOS=$$goos GOARCH=$$goarch CGO_ENABLED=$$cgo \
				$(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/gar-$$goos-$$goarch$$ext ./cmd/gar; \
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

# ---- Ekstensi resmi (ext/) ----
#
# Binari per platform disimpan di ext/<nama>/build/<GOOS-GOARCH>/ lalu
# dikemas oleh `gar gne pack` (target pack-ext). macOS TIDAK dibangun di
# sini — cross-CGO ke darwin mustahil dari Linux. .dylib dibangun oleh
# release.yml di runner macOS dan ditaruh ke build/ lewat unduhan
# aset ext-darwin-builds.zip sebelum pack-ext dijalankan.
EXTS      ?= redis smtp
EXTPLAT   ?= linux/amd64 linux/arm64 windows/amd64
AARCH64CC ?= aarch64-linux-gnu-gcc

ext:
	@set -e; for name in $(EXTS); do \
	  for platform in $(EXTPLAT); do \
	    goos=$${platform%/*}; goarch=$${platform#*/}; \
	    cc=gcc; libs=; ext=.so; \
	    case "$$goos-$$goarch" in \
	      linux-arm64) cc=$(AARCH64CC) ;; \
	      windows-amd64) cc=$(MINGW); libs="-lws2_32"; ext=.dll ;; \
	    esac; \
	    out=ext/$$name/build/$$goos-$$goarch; \
	    mkdir -p $$out; \
	    $$cc -shared -fPIC -Wall -Wextra -I include -o $$out/$$name$$ext \
	      ext/$$name/$$name.c $$libs; \
	    echo "Ekstensi $$name → $$out/$$name$$ext"; \
	  done; \
	done

# Kemas tiap ekstensi jadi zip rilis. Butuh minimal satu binari di
# build/ (linux/windows dari make ext; darwin dari hasil CI).
pack-ext: build
	@set -e; for name in $(EXTS); do \
	  $(BUILD_DIR)/gar gne pack ext/$$name -o $(DIST_DIR)/$$name.zip; \
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
	rm -rf ext/*/build
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