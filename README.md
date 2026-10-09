# GaLang (Garuda Language)

Bahasa pemrograman interpreted untuk web, dengan ekstensi `.ga`, ditulis di atas
interpreter Go. Identifier, komentar, dan pesan error memakai bahasa Inggris.

```ga
// examples/hello.ga
print "Halo dari GaLang!"

$nama = "Dunia"
print "Halo, ${nama}!"
```

```console
$ make build && ./bin/gar run examples/hello.ga
Halo dari GaLang!
Halo, Dunia!
```

## Yang sudah bisa dipakai sekarang

- Bahasa lengkap: variabel bertipe (`int $stok = 72`) maupun dinamis (`$x = 1`)
- Fungsi, closure, arrow function, rekursi
- `if`/`else if`/`else`, `while`, `for x in`, rentang `1..10`, `break`/`continue`
- Array & objek: **penugasan membagi referensi** (`$b = $a` lalu `$b[0] = 99`
  mengubah `$a` juga); fungsi bawaan (`append`, `push`, `sort`, `slice`,
  `map`, `filter`) selalu **mengembalikan salinan baru** sehingga `$b =
  append($b, 3)` tidak menyentuh `$a`
- Kompresi: `[$n * 2 for $n in $xs]`
- Interpolasi string: `"Halo ${nama}"` dan `"Halo $nama"`
- Penanganan error: `throw` + `try`/`catch e` dengan `status` HTTP
- `async`/`await`, `spawn`, Promise (`race`, `any`)
- Web: server HTTP + routing, middleware `http.use`, sesi, template Blade,
  file statis, SSE, WebSocket — [dokumentasi web](docs/id/web.md)
- Database: `database.connect` (SQLite, MySQL, PostgreSQL)
- Pustaka bawaan: `strings`, `math`, `time`, `json_encode`/`json_decode`, `html_escape`
- Modul: `use "strings"` memuat pustaka bawaan; `use "db.ga"` memuat file
  `.ga` lokal menjadi namespace (dieksekusi sekali)
- **Ekstensi native C (GNE)**: `use "redis"` memuat `redis.so`/`.dylib`/`.dll`
  dari `./gne` → `$GNE_PATH` → `~/.galang/gne` — menulis ekstensi dalam C
  tanpa menyentuh compiler/runtime ([panduan GNE](docs/id/gne.md),
  [contoh](https://github.com/lnx645/galang-gne-examples))
- **Ekstensi resmi `redis`, `smtp`, `uuid`, `jwt`, `httpclient`** dipasang
  dari aset rilis lewat `gar gne install <nama>` (pasang, daftar, lepas;
  juga URL HTTPS atau berkas zip) — API-nya ada di
  [Modul Standar](docs/id/modules.md)
- CLI: `gar run`, `gar repl`, `gar gne`
- 120+ test, benchmark dengan `-benchmem`

Lihat tur lengkap sintaks yang berjalan hari ini:

```console
$ make run
```

## Yang belum ada (lihat `docs/SPEC.md`)

Unit-test framework (`gar test`), operator compound
(`+=`) dan `??`, generic `<T>`, class & method, async I/O. Daftar terbaru:
[Yang Belum Ada](docs/id/language.md).

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

| Tes | GaLang | PHP 8.2 | Go native | Pemenang |
|---|---|---|---|---|
| rekursi `fib(25)` | **23-28 ms** | 30-36 ms | 8 ms | **GaLang (1,2-1,4x)** |
| loop 200.001 iterasi | **12-15 ms** | 27-32 ms | 5 ms | **GaLang (2,1-2,3x)** |
| 100.001 pemanggilan fungsi | **6-7 ms** | 27-31 ms | 5 ms | **GaLang (4,0-4,5x)** |
| memori (RSS) | **4-6 MB** | 19 MB | — | **GaLang (3-5x)** |
| startup proses kosong | **4 ms** | 24-45 ms | — | **GaLang (6-10x)** |

GaLang menang di ketiga tes CPU pada setiap pengukuran. Angkanya berupa
rentang karena PHP di mesin ini berfluktuasi besar (28-45 ms) sedangkan GaLang
stabil (23-28 ms). Margin `fib` paling tipis karena itu tes yang paling banyak
memanggil fungsi.

Cara kerjanya: program dikompilasi ke pohon closure Go dengan frame slot
bertipe, dan fungsi bilangan bulat murni dikompilasi lagi ke **bytecode** yang
dijalankan mesin virtual datar. Fungsi yang mengandung loop tetap memakai
closure, karena loop jadi loop Go asli dan itu lebih cepat. Detailnya ada di
`docs/SPEC.md` bab 10.

Benchmark ulang kapan saja:

```console
make ref      # jalankan ./bench/compare.sh
```

## Lisensi

MIT
# galang
