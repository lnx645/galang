#!/usr/bin/env bash
# build-pkg.sh <versi> <binari-amd64> <binari-arm64> [keluaran.pkg]
#
# Rakit .pkg macOS universal2 dari binari rilis dua arsitektur (hasil
# ekstrak gar-darwin-*.zip). Dua binari digabung dengan lipo lalu
# dipasang ke /usr/local/bin/gar. Contoh (di runner macOS):
#   ./packaging/macos/build-pkg.sh 0.6.1 gar-darwin-amd64 gar-darwin-arm64 \
#     pkg/Garurda-0.6.1-macos.pkg
set -euo pipefail

VER=${1:?versi wajib, mis. 0.6.1}
AMD64=${2:?binari amd64 wajib}
ARM64=${3:?binari arm64 wajib}
OUT=${4:-"Garurda-$VER-macos.pkg"}
IDENT="io.github.lnx645.garurda"

for f in "$AMD64" "$ARM64"; do
  if [ ! -f "$f" ]; then
    echo "binari tidak ada: $f" >&2
    exit 1
  fi
  chmod +x "$f"
done

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
# mktemp memberi mode 700 pada root stage — samakan dengan 755 agar
# tak ikut terekam dalam payload paket.
chmod 755 "$STAGE"
mkdir -p "$STAGE/usr/local/bin"

# Satu binari universal (Intel + Apple Silicon) — lipo bawaan Xcode.
lipo -create "$AMD64" "$ARM64" -output "$STAGE/usr/local/bin/gar"
chmod 755 "$STAGE/usr/local/bin/gar"
file "$STAGE/usr/local/bin/gar"

mkdir -p "$(dirname "$OUT")"
pkgbuild --identifier "$IDENT" --version "$VER" --install-location / \
  --root "$STAGE" "$OUT"

echo "Dibuat: $OUT"
pkgutil --payload-files "$OUT"
