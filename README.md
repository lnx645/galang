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

Diukur head-to-head dengan PHP 8.2 pada program identik (`./bench/compare.sh`):

| Tes | Garurda v0.1 | PHP 8.2 | Go native |
|---|---|---|---|
| rekursi `fib(25)` | 219 ms | **31 ms** | 8 ms |
| loop 200k iterasi | 93 ms | **25 ms** | 5 ms |
| 100k pemanggilan fungsi | 29 ms | **24 ms** | 5 ms |
| memori (RSS) | 8-10 MB | **17-19 MB** | — |

**PHP menang CPU di semua tes; Garurda menang memori 1,8-2,4x.** Target "menang
telak atas PHP" belum tercapai —-analisis lengkap dan rencana untuk mencapainya
ada di `docs/SPEC.md` bab 10. Klaim performa tidak boleh dibuat sebelum
`./bench/compare.sh` benar-benar menaruh Garurda di semua baris.

Benchmark ulang kapan saja:

```console
make ref      # jalankan ./bench/compare.sh
```

## Lisensi

MIT
# galang
