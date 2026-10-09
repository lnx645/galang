# Build, test, and release for GaLang.
#
# Targets:
#   make build       - Build for the current platform
#   make release     - Build release binaries (Linux, Windows, ARM64)
#   make release-zip - Create zip archives for distribution
#   make ext         - Compile the official extensions (ext/*) cross-platform
#   make pack-ext    - Pack each extension into dist/<name>.zip
#   make test        - Run all tests
#   make bench       - Run benchmarks
#   make ref         - Run the PHP comparison
#   make clean       - Remove build artifacts

GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
# -X injects the version into the cli.Version symbol (main.version does NOT
# exist — the linker silently ignores -X for unknown symbols).
LDFLAGS := -s -w -X galang/internal/delivery/cli.Version=$(patsubst v%,%,$(VERSION))
BUILD_DIR := bin
DIST_DIR := dist

# Platform targets — release binaries. macOS (darwin) is deliberately NOT
# here: cross-building CGO for darwin from Linux is impossible, so
# gar-darwin-* are built by GitHub Actions (release.yml) and uploaded as
# release assets.
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	windows/amd64 \
	windows/arm64

.PHONY: all build release release-zip ext pack-ext test bench ref clean install

all: build

# Build for the current platform
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/gar ./cmd/gar

# Install into $GOPATH/bin or /usr/local/bin
install: build
	cp $(BUILD_DIR)/gar $(shell go env GOPATH)/bin/gar 2>/dev/null || sudo cp $(BUILD_DIR)/gar /usr/local/bin/gar

# Build all release platform binaries (see PLATFORMS).
#
# CGO per platform: linux/amd64 (host, gcc) and windows/amd64 (needs
# gcc-mingw-w64-x86-64) are built WITH CGO so GNE (dlopen/LoadLibrary)
# and SQLite are active. Other platforms cross-build with CGO=0 → the
# binaries still run, but `use` for native extensions and databases
# produce a "requires CGO" error (documented in docs/{id,en}).
# Darwin CGO=1 binaries are built by release.yml on the macOS runner.
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

# Create zip archives for distribution.
# Each zip contains the platform binary + gne.h (the GNE ABI header for
# extension authors — deliberately attached to every zip for easy access).
release-zip: release
	@cp include/gne.h $(DIST_DIR)/
	@cd $(DIST_DIR) && \
	for f in gar-*; do \
		case $$f in *.zip) continue ;; esac; \
		base=$${f%.*}; \
		zip -q $${base}.zip $$f gne.h; \
		echo "Created: $$base.zip"; \
	done

# ---- Official extensions (ext/) ----
#
# Binaries per platform are stored in ext/<name>/build/<GOOS-GOARCH>/ and
# packed by `gar gne pack` (the pack-ext target). macOS is NOT built here
# — cross-CGO for darwin is impossible from Linux. The .dylib files are
# built by release.yml on the macOS runner and placed into build/ via the
# ext-darwin-builds.zip asset download before pack-ext runs.
EXTS      ?= redis smtp uuid jwt httpclient
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
	    if [ "$$name" = uuid ] && [ "$$goos" = windows ]; then \
	      libs="$$libs -lbcrypt"; \
	    fi; \
	    out=ext/$$name/build/$$goos-$$goarch; \
	    mkdir -p $$out; \
	    $$cc -shared -fPIC -Wall -Wextra -I include -o $$out/$$name$$ext \
	      ext/$$name/$$name.c $$libs; \
	    echo "Extension $$name → $$out/$$name$$ext"; \
	  done; \
	done

# Pack each extension into a release zip.
#
# Depends on `ext` so the binaries are ALWAYS rebuilt from the current
# source before packing — packing stale build/ artifacts (compiled
# against an older gne.h) would ship extensions whose compiled-in
# GNE_ABI disagrees with the manifest's gne_abi, causing an
# "ABI mismatch" error at load time.
#
# Darwin binaries are NOT built here (cross-CGO for darwin is
# impossible from Linux); they come from release.yml's
# ext-darwin-builds.zip asset, which must be extracted into
# ext/<name>/build/darwin-*/ before running this target.
pack-ext: build ext
	@set -e; for name in $(EXTS); do \
	  $(BUILD_DIR)/gar gne pack ext/$$name -o $(DIST_DIR)/$$name.zip; \
	done

# Run all package tests
test:
	$(GO) test ./...

# Benchmarks
bench:
	$(GO) test -run=XXX -bench=. -benchmem ./internal/usecase/interp/

# PHP comparison
ref:
	./bench/compare.sh

# Run the example application
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

# Everything
check: fmt vet test

# Development: auto-reload (requires entr)
dev:
	entr -r make build run <<< examples/bahasa.ga

# Release notes template
notes:
	@echo "## GaLang $(VERSION)"
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