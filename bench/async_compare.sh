#!/usr/bin/env bash
# async_compare.sh — mengukur fitur async Garurda vs JavaScript (Bun).
#
# Tiga pola identik di kedua bahasa, ditambah satu KONTROL:
#   call    — KONTROL: panggilan sinkron biasa (tanpa async) — memisahkan
#             biaya mekanisme async dari kecepatan mentah eksekusi
#   await   — loop `await f(x)` 100.001 iterasi (biaya per-await)
#   gather  — 1.000 batch x 10 promise, diselesaikan sekaligus
#             (Garurda gather vs JS Promise.all)
#   spawn   — 100.000 tugas fire-and-forget + drain
#
# Cara yang adil: tiap program dijalankan RUNS kali dan yang dicatat adalah
# waktu TERCEPAT, sama seperti bench/compare.sh terhadap PHP.
#
# Catatan jujur: Bun adalah JIT (JavaScriptCore), Garurda adalah interpreter
# tree-walking. Angka ini membandingkan biaya MEKANISME async-nya, bukan
# kecepatan mentah eksekusi.

set -u
cd "$(dirname "$0")/.."

RUNS=9
GAR="./bin/gar"
JS_BIN="bun"
PEAK="./bin/peak"

if [ ! -x "$GAR" ]; then echo "build dulu: make build" >&2; exit 1; fi
if ! command -v "$JS_BIN" >/dev/null 2>&1; then echo "bun belum terpasang" >&2; exit 1; fi
if [ ! -x "$PEAK" ]; then
  if command -v go >/dev/null 2>&1; then
    go build -o "$PEAK" ./bench/peak 2>/dev/null || true
  fi
fi

# time_ms menjalankan satu perintah RUNS kali dan mencetak waktu terbaik (ms).
time_ms() {
  local best=999999999
  local i
  for ((i = 0; i < RUNS; i++)); do
    local start end
    start=$(date +%s%N)
    "$@" >/dev/null 2>&1
    end=$(date +%s%N)
    local ms=$(( (end - start) / 1000000 ))
    (( ms < best )) && best=$ms
  done
  echo "$best"
}

printf "%-8s | %-10s | %-10s | %-14s | %-9s\n" \
  "POLA" "Garurda" "JS (Bun)" "Garurda lebih" "winner"
printf -- "----------|------------|------------|----------------|----------\n"

overall_ok=1
declare -A g_ms_of j_ms_of
for t in call await gather spawn; do
  g_ms=$(time_ms "$GAR" run "bench/async/$t.ga")
  j_ms=$(time_ms "$JS_BIN" "bench/js/$t.mjs")
  g_ms_of[$t]=$g_ms
  j_ms_of[$t]=$j_ms

  # ratio = berapa kali lebih cepat Garurda dibanding JS (>1 = Garurda menang).
  ratio=$(awk -v g="$g_ms" -v j="$j_ms" 'BEGIN{ if (g>0) printf "%.2fx", j/g; else print "n/a" }')

  if [ "$g_ms" -lt "$j_ms" ]; then verdict="Garurda"; else verdict="JS"; overall_ok=0; fi

  printf "%-8s | %-10s | %-10s | %-14s | %-9s\n" \
    "$t" "${g_ms}ms" "${j_ms}ms" "$ratio" "$verdict"
done

printf -- "----------|------------|------------|----------------|----------\n"

echo
echo "Biaya relatif terhadap KONTROL call (panggilan sinkron) di bahasa masing-masing:"
echo "  (berapa kali lebih berat pola async dibanding panggilan biasa)"
for t in await gather spawn; do
  ga=$(awk -v a="${g_ms_of[$t]}" -v b="${g_ms_of[call]}" 'BEGIN{ if (b>0) printf "%.2fx", a/b; else print "n/a" }')
  ja=$(awk -v a="${j_ms_of[$t]}" -v b="${j_ms_of[call]}" 'BEGIN{ if (b>0) printf "%.2fx", a/b; else print "n/a" }')
  printf "  %-8s Garurda %-8s   JS %-8s\n" "$t" "$ga" "$ja"
done

echo
echo "Memori puncak (RSS via getrusage, bukan sampling):"
if [ -x "$PEAK" ]; then
  gb=$("$PEAK" "$GAR" run bench/async/empty.ga 2>/dev/null | tail -1)
  jb=$("$PEAK" "$JS_BIN" bench/js/empty.mjs 2>/dev/null | tail -1)
  printf "  %-8s Garurda %8s KiB   JS %8s KiB\n" "baseline" "$gb" "$jb"
  mem_ok=1
  for t in call await gather spawn; do
    gk=$("$PEAK" "$GAR" run "bench/async/$t.ga" 2>/dev/null | tail -1)
    jk=$("$PEAK" "$JS_BIN" "bench/js/$t.mjs" 2>/dev/null | tail -1)
    if awk -v g="$gk" -v j="$jk" 'BEGIN{ exit !((g+0)>0 && (j+0)>0 && (g+0)<(j+0)) }'; then
      verdict="Garurda"
    else
      verdict="JS"; mem_ok=0
    fi
    printf "  %-8s Garurda %8s KiB   JS %8s KiB   (memori lebih kecil: %s)\n" \
      "$t" "$gk" "$jk" "$verdict"
  done
  echo
  if [ "$mem_ok" -eq 1 ]; then
    echo "MEMORI: Garurda di bawah JS di semua pola."
  else
    echo "MEMORI: JS lebih kecil di setidaknya satu pola."
  fi
else
  echo "  (alat ukur bin/peak gagal dibangun — lewati)"
fi

echo
echo "Interpreter vs JIT: JS dieksekusi Bun (JavaScriptCore, JIT penuh),"
echo "Garurda dieksekusi interpretasi murni tanpa JIT."
echo
if [ $overall_ok -eq 1 ]; then
  echo "HASIL: Garurda menang di semua pola async."
else
  echo "HASIL: JS menang di setidaknya satu pola."
fi
