#!/usr/bin/env bash
# compare.sh — mengukur Garurda vs PHP pada program yang identik.
#
# Cara yang adil: tiap program dijalankan RUNS kali dan yang dicatat adalah
# waktu TERCEPAT, karena itu angka yang paling stabil untuk pembanding
# single-process. Memori adalah RSS puncak proses (/proc/<pid>/status).
#
# PHP dijalankan apa adanya: opcache.enable_cli=Off dan JIT=Off, yaitu cara
# PHP CLI berjalan secara normal.

set -u
cd "$(dirname "$0")/.."

RUNS=5
GAR="./bin/gar"
PHP_BIN="php"

if [ ! -x "$GAR" ]; then echo "build dulu: make build" >&2; exit 1; fi
if ! command -v "$PHP_BIN" >/dev/null 2>&1; then echo "php-cli belum terpasang" >&2; exit 1; fi

# time_ms menjalankan satu perintah 5x dan mencetak waktu terbaik (ms).
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

# peak_rss_kb menjalankan perintah sambil mengukur RSS puncak dalam KiB.
peak_rss_kb() {
  "$@" >/dev/null 2>&1 &
  local pid=$!
  local peak=0
  while kill -0 "$pid" 2>/dev/null; do
    local cur
    cur=$(awk '/VmHWM/{print $2}' "/proc/$pid/status" 2>/dev/null)
    if [ -n "${cur:-}" ] && [ "$cur" -gt "$peak" ]; then peak=$cur; fi
    sleep 0.005
  done
  wait "$pid"
  echo "$peak"
}

printf "%-8s | %-10s | %-10s | %-9s | %-9s\n" \
  "TESTS" "Garurda" "PHP" "PHP/Ga" "winner"
printf -- "----------|------------|------------|------------|-------------\n"

overall_ok=1
for t in fib loop call; do
  g_ms=$(time_ms "$GAR" run "bench/$t.ga")
  p_ms=$(time_ms "$PHP_BIN" "bench/php/$t.php")

  # ratio = berapa kali lebih lambat Garurda dibanding PHP (>1 = PHP menang).
  ratio=$(awk -v g="$g_ms" -v p="$p_ms" 'BEGIN{ if (g>0) printf "%.2f", g/p; else print "n/a" }')

  if [ "$g_ms" -lt "$p_ms" ]; then verdict="Garurda"; else verdict="PHP"; overall_ok=0; fi

  printf "%-8s | %-10s | %-10s | %-9s | %-9s\n" \
    "$t" "${g_ms}ms" "${p_ms}ms" "${ratio}x lebih lambat" "$verdict"
done

printf -- "----------|------------|------------|------------|-------------\n"

echo
echo "Memori puncak (RSS):"
for t in fib loop call; do
  g_kb=$(peak_rss_kb "$GAR" run "bench/$t.ga")
  p_kb=$(peak_rss_kb "$PHP_BIN" "bench/php/$t.php")
  mratio=$(awk -v g="$g_kb" -v p="$p_kb" 'BEGIN{ if (g>0) printf "%.1f", p/g; else print "n/a" }')
  printf "  %-6s Garurda %8s KiB   PHP %8s KiB   (PHP butuh %.1fx)\n" "$t" "$g_kb" "$p_kb" "$mratio"
done

echo
if [ $overall_ok -eq 1 ]; then
  echo "HASIL: Garurda menang di semua tes."
else
  echo "HASIL: PHP menang di setidaknya satu tes."
fi
echo "Jalankan 'make ref' lalu lihat docs/SPEC.md untuk analisis Selisihnya."
