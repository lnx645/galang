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

## Performa

Interpreter *tree-walking*: AST dievaluasi langsung. Angka benchmark saat ini
ada di `docs/SPEC.md`. Targetnya menang telak atas PHP pada beban web, tetapi
benchmark pembanding PHP **belum dijalankan** — klaim belum bisa dibuat.

## Lisensi

MIT
# galang
