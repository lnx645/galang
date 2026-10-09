#!/usr/bin/env bash
# build-deb.sh <version> <arch> <gar-binary> [output-dir]
#
# Build a Debian/Ubuntu .deb package from a gar binary. The arch uses
# Debian names (amd64 / arm64). Example:
#   ./packaging/linux/build-deb.sh 0.6.1 amd64 gar-linux-amd64 dist
# Output: garurda_<version>_<arch>.deb containing /usr/bin/gar.
set -euo pipefail

VER=${1:?version required, e.g. 0.6.1}
ARCH=${2:?architecture required (amd64/arm64)}
BIN=${3:?gar binary required}
OUT=${4:-.}

case "$ARCH" in
  amd64|arm64) ;;
  *) echo "unknown architecture: $ARCH (use amd64 or arm64)" >&2
     exit 1 ;;
esac
if [ ! -f "$BIN" ]; then
  echo "binary not found: $BIN" >&2
  exit 1
fi

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
# mktemp creates the stage root with mode 700 — without this, that mode
# would be recorded inside the package as "drwx------ ./".
chmod 755 "$STAGE"
PKG="garurda_${VER}_${ARCH}"

mkdir -p "$STAGE/DEBIAN" "$STAGE/usr/bin" "$STAGE/usr/share/doc/garurda"
install -m 755 "$BIN" "$STAGE/usr/bin/gar"

cat > "$STAGE/DEBIAN/control" <<EOF
Package: garurda
Version: $VER
Section: interpreters
Priority: optional
Architecture: $ARCH
Maintainer: Dadan <dadanhidyt@gmail.com>
Homepage: https://github.com/lnx645/galang
Installed-Size: $(du -ks "$STAGE/usr" | cut -f1)
Description: Galang language interpreter (gar)
 Garurda is a library and interpreter for the Galang language:
 single-file scripts, a web runtime, a native extension installer
 (GNE), and the official redis and smtp extensions.
EOF

mkdir -p "$OUT"
dpkg-deb --build --root-owner-group "$STAGE" "$OUT/$PKG.deb"
echo "Created: $OUT/$PKG.deb"
