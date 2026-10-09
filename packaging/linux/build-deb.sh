#!/usr/bin/env bash
# build-deb.sh <versi> <arsitektur> <binari-gar> [dir-keluaran]
#
# Susun paket .deb Debian/Ubuntu dari binari gar. Arsitektur memakai
# nama Debian (amd64 / arm64). Contoh:
#   ./packaging/linux/build-deb.sh 0.6.1 amd64 gar-linux-amd64 dist
# Hasil: garurda_<versi>_<arsitektur>.deb berisi /usr/bin/gar.
set -euo pipefail

VER=${1:?versi wajib, mis. 0.6.1}
ARCH=${2:?arsitektur wajib (amd64/arm64)}
BIN=${3:?binari gar wajib}
OUT=${4:-.}

case "$ARCH" in
  amd64|arm64) ;;
  *) echo "arsitektur tak dikenal: $ARCH (pakai amd64 atau arm64)" >&2
     exit 1 ;;
esac
if [ ! -f "$BIN" ]; then
  echo "binari tidak ada: $BIN" >&2
  exit 1
fi

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
# mktemp memberi mode 700 pada root stage — tanpa ini, mode ikut
# terekam sebagai "drwx------ ./" di dalam paket.
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
Description: Interpreter bahasa Galang (gar)
 Garurda adalah pustaka dan interpreter bahasa Galang: skrip satu
 berkas, runtime web, pemasang ekstensi native (GNE), serta ekstensi
 resmi redis dan smtp.
EOF

mkdir -p "$OUT"
dpkg-deb --build --root-owner-group "$STAGE" "$OUT/$PKG.deb"
echo "Dibuat: $OUT/$PKG.deb"
