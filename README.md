# Garurda

Bahasa pemrograman interpreted untuk web, dengan ekstensi `.ga`, ditulis di atas
interpreter Go. Identifier, komentar, dan pesan error memakai bahasa Inggris.

```ga
// examples/hello.ga
print "Halo dari Garurda!"

$nama = "Dunia"
print "Halo, ${nama}!"
```

```console
$ make build && ./bin/gar run examples/hello.ga
Halo dari Garurda!
Halo, Dunia!
```

## Yang sudah bisa dipakai sekarang

- Bahasa lengkap: variabel bertipe (`int $stok = 72`) maupun dinamis (`$x = 1`)
- Fungsi, closure, arrow function, rekursi
- `if`/`else if`/`else`, `while`, `for x in`, rentang `1..10`, `break`/`continue`
- Array & objek dengan **semantik nilai** (copy-on-write, tanpa bug aliasing)
- Kompresi: `[$n * 2 for $n in $xs]`
- Interpolasi string: `"Halo ${nama}"` dan `"Halo $nama"`
- Penanganan error: `throw` + `try`/`catch e` dengan `status` HTTP
- Pustaka bawaan: `strings`, `math`, `time`, `json_encode`/`json_decode`, `html_escape`
- CLI: `gar run`, `gar repl`
- 60+ test, benchmark dengan `-benchmem`

Lihat tur lengkap sintaks yang berjalan hari ini:

```console
$ make run
```

## Yang belum ada (lihat `docs/SPEC.md`)

Server HTTP + template Blade, `async`/`await`, database, WebSocket, SSE, SMTP.
Peta jalan lengkap ada di spesifikasi.

## Menjalankan

```console
make build      # build ./bin/gar
make test       # seluruh test
make bench      # benchmark + laporan alokasi
make run        # jalankan tur sintaks
make repl       # sesi interaktif
```

## Struktur

```
cmd/gar              CLI
internal/domain      token, AST, nilai runtime (tanpa import luar)
internal/usecase/lex lexer
internal/usecase/parse parser
internal/usecase/interp evaluator + pustaka bawaan
internal/infra/repl   REPL
internal/delivery/cli CLI
examples/            contoh program
docs/SPEC.md         kontrak bahasa & peta jalan
```

Arah import hanya ke bawah; `internal/domain` tidak mengimpor apa pun dari luar
dirinya sendiri.

## Performa — hasil jujur

Diukur head-to-head dengan PHP 8.2 pada program identik (`./bench/compare.sh`,
9 run, ambil tercepat):

| Tes | Garurda | PHP 8.2 | Go native | Pemenang |
|---|---|---|---|---|
| rekursi `fib(25)` | 42 ms | 28 ms | 8 ms | PHP (1,5x) |
| loop 200.001 iterasi | **12 ms** | 25 ms | 5 ms | **Garurda (2,1x)** |
| 100.001 pemanggilan fungsi | **6 ms** | 22 ms | 5 ms | **Garurda (3,7x)** |
| memori (RSS) | **4,2 MB** | 19 MB | — | **Garurda (4,5x)** |
| startup proses kosong | **4 ms** | 24-45 ms | — | **Garurda (6-10x)** |

Dua dari tiga tes CPU dimenangkan: Garurda menang 2,1x dan 3,7x, memori
4,5x lebih hemat, dan startup 6-10x lebih cepat. Yang masih kalah adalah `fib`,
yaitu kode yang memanggil fungsi sangat berat — analisis penyebab dan dua
langkah penutupnya ada di `docs/SPEC.md` bab 10. Klaim kemenangan di semua tes
belum sah sampai `./bench/compare.sh` benar-benar menaruh Garurda di setiap
baris.

Interpreter dikompilasi ke pohon closure Go dengan frame slot bertipe, bukan
tree-walker, sehingga aritmetika integer pada loop tidak mengalokasikan apa pun.

Benchmark ulang kapan saja:

```console
make ref      # jalankan ./bench/compare.sh
```

## Lisensi

MIT
# galang
