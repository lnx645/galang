#!/usr/bin/env bash
# build-pkg.sh <version> <amd64-binary> <arm64-binary> [output.pkg]
#
# Build a universal2 macOS .pkg from release binaries for both
# architectures (extracted from gar-darwin-*.zip). The two binaries are
# merged with lipo and installed to /usr/local/bin/gar. Example (on a
# macOS runner):
#   ./packaging/macos/build-pkg.sh 0.6.1 gar-darwin-amd64 gar-darwin-arm64 \
#     pkg/GaLang-0.6.1-macos.pkg
set -euo pipefail

VER=${1:?version required, e.g. 0.6.1}
AMD64=${2:?amd64 binary required}
ARM64=${3:?arm64 binary required}
OUT=${4:-"GaLang-$VER-macos.pkg"}
IDENT="io.github.lnx645.galang"

for f in "$AMD64" "$ARM64"; do
  if [ ! -f "$f" ]; then
    echo "binary not found: $f" >&2
    exit 1
  fi
  chmod +x "$f"
done

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
# mktemp creates the stage root with mode 700 — normalize it to 755 so
# it is never recorded in the package payload.
chmod 755 "$STAGE"
mkdir -p "$STAGE/usr/local/bin"

# One universal binary (Intel + Apple Silicon) via the Xcode-bundled lipo.
lipo -create "$AMD64" "$ARM64" -output "$STAGE/usr/local/bin/gar"
chmod 755 "$STAGE/usr/local/bin/gar"
file "$STAGE/usr/local/bin/gar"

mkdir -p "$(dirname "$OUT")"
pkgbuild --identifier "$IDENT" --version "$VER" --install-location / \
  --root "$STAGE" "$OUT"

echo "Created: $OUT"
pkgutil --payload-files "$OUT"
