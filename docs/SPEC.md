# Spesifikasi Bahasa GaLang

Status: **v0.1 — inti bahasa sudah berjalan & teruji**
Terakhir diperbarui: 2026-10-07

Dokumen ini adalah kontrak bahasa. Setiap keputusan di sini bisa diubah, tapi
perubahan harus lewat revisi dokumen ini dulu — bukan diam-diam di kode.

---

## 1. Prinsip

1. **Web adalah use case utama.** Sintaks, pustaka, dan error model dirancang
   untuk menulis aplikasi web, bukan untuk bahasa umum.
2. **Standar, bukan ciptaan baru.** Nama fungsi mengikuti Go (`len`, `append`,
   `sort`...). Pustaka memakai nama paket stdlib Go (`strings`, `math`, `time`).
   Yang tidak ada padanannya saja yang baru.
3. **Interpreter murni.** Tidak ada kompilasi ke bahasa lain. Target performa:
   menang telak atas PHP pada beban web.
4. **Async bawaan.** Konkurensi I/O adalah fitur bahasa, bukan pustaka pihak
   ketiga (kelemahan utama PHP).
5. **Aman secara default.** Template meng-escape secara default; query wajib
   diparameterisasi.
6. **Nol alokasi saat tidak dipakai.** Error handling, defer, dan anotasi tipe
   tidak boleh menambah biaya ketika tidak aktif.

---

## 2. Status implementasi

| Bagian | Status |
|---|---|
| Lexer (string, template, komentar, angka heksa/underscore) | ✅ selesai |
| Parser (ekspresi, kontrol alur, fungsi, anotasi tipe, kompresi) | ✅ selesai |
| Kompilator AST → pohon closure (`compiler.go`) | ✅ selesai |
| Runtime: frame slot bertipe, free-list, argumen stack (`frame.go`) | ✅ selesai |
| Closure by-reference (cell), try/catch | ✅ selesai |
| Bawaan: array/object/strconv/json/html-escape | ✅ selesai |
| Modul: `strings`, `math`, `time` | ✅ selesai |
| CLI: `gar run`, `gar repl`, `gar version`, `gar help` | ✅ selesai |
| Server HTTP + routing + `response` | ✅ selesai |
| Template Blade | ✅ selesai |
| Async (`async fn`, `await`, `gather`, `spawn`) | ✅ selesai |
| Database (SQLite/MySQL/PostgreSQL) | ✅ selesai |
| WebSocket + SSE | ✅ selesai |
| HTTP client | ⬜*v0.3* |
| GNE — ekstensi native C + `gar gne` (v0.5/v0.6) | ✅ selesai |
| Ekstensi resmi `redis`, `smtp` (v0.6.0, tanpa TLS) | ✅ selesai |
| Generic `<T>` | ⬜*v1* (anotasi `array<T>` sudah ada di v0.1) |

---

## 3. Leksikal

```ga
// komentar baris
/* komentar blok */

$nama = "GaLang"        // escaped, mendukung ${expr} dan $nama
$teks = `baris literal
multi baris, tanpa escape`   // backtick = raw, tanpa interpolasi
$jumlah = 1_000_000      // underscore
$heksa = 0xFF
$desimal = 3.14
$aktif = true            // true / false / null
```

**Sigil `$` opsional.** `$nama` dan `nama` menunjuk variabel yang sama. `$`
wajib menyertai kata kunci agar boleh dipakai sebagai nama: `$string`.

**Interpolasi.** Dua bentuk, keduanya aktif pada string `"`:
- `${expr}` — ekspresi apa pun
- `$nama` — hanya pengenal sederhana

`$` yang tidak diikuti pengenal tetap literal (`"harga 5$"` aman).
Raw string (backtick) tidak menginterpolasi.

**Akhir statement.** Newline mengakhiri statement; `;` opsional. Baris
dilanjutkan otomatis bila karakter sebelumnya operator biner atau `(`/`[`/`{`.

---

## 4. Tipe data

| Tipe | Contoh | Inferensi |
|---|---|---|
| `int` | `72`, `0xFF`, `1_000` | `$a = 72` → `int` |
| `float` | `3.14`, `1.0e3` | `$a = 3.14` → `float` |
| `number` | — | tipe induk, hanya untuk anotasi |
| `string` | `"teks"`, `` `raw` `` | ✓ |
| `bool` | `true`/`false` | ✓ |
| `null` | `null` | ✓ |
| `array` | `[1, 2]`, `[]`, `[0..10]` | ✓ |
| `object` | `{a: 1}`, `{}` | ✓ |

Modifier: `?T` (boleh null), `any` (tanpa pemeriksaan),
`array<T>` (tipe elemen — diperiksa, sekaligus membuka jalan ke generic v1).

Tipe bawaan untuk anotasi: `int float number string bool array object any
function error request response connection session upload rows module mailer`.

**Tanpa coercion.** `"1" == 1` → `false`. `int` dan `float` dibanding
numerik (`1 == 1.0` → `true`). Konversi eksplisit: `int("42")`, `str(1)`.

---

## 5. Pernyataan & kendali alur

```ga
if $x > 10 { } else if $x > 3 { } else { }   // kurung kurawal wajib
while $cond { }
for $x in [1, 2, 3] { }
for $i in 1..10 { }        // inklusif di kedua sisi
for $i in 0..10..5 { }     // dengan langkah
for $i in 3..1 { }         // arah mengikuti tanda (turun)
for $k, $v in $obj { }     // kunci + nilai
break / continue
print a, b                 // satu baris
println a                  // eksplisit satu baris
[$n * 2 for $n in $xs]     // kompresi
[$n for $n in $xs if $n > 0]
$status = $n > 0 ? "ok" : "gagal"
```

### Presedensi operator (rendah → tinggi)

| # | Operator |
|---|---|
| 1 | `or` |
| 2 | `and` |
| 3 | `not` (unary) |
| 4 | `==` `!=` |
| 5 | `<` `<=` `>` `>=` `in` `..` |
| 6 | `+` `-` |
| 7 | `*` `/` `%` |
| 8 | unary `-` |
| 9 | `()` `[]` `.` |

Penting: `not` mengikat **lebih longgar** dari perbandingan, jadi
`not a == b` berarti `not (a == b)`.

---

## 6. Fungsi

```ga
fn greet($nama, $sapaan = "Halo") { return "${sapaan}, ${nama}!" }
fn hitung(int $a, int $b) int { return $a + $b }   // anotasi opsional
fn kali($x) => $x * 2                                // arrow function

$fn = fn($a, $b) { return $a + $b }                 // first-class
```

- Anotasi tipe **diperiksa lalu dihapus** saat runtime: nol biaya.
- Closure menangkap lingkungan; scope fungsi adalah turunan scope pendefinisian.
- Batas kedalaman rekursi 2000 dengan pesan yang jelas.

---

## 7. Array & objek

**Semantik referensi pada penugasan, salinan pada builtins.** Assignment
`$b = $a` membagi referensi yang sama: menulis `$b[0] = 99` mengubah `$a`
juga. Sebaliknya fungsi bawaan **mengembalikan array baru** — `append`,
`push`, `pop`, `sort`, `slice`, `map`, `filter` tidak mengubah argumennya.

```ga
$a = [1, 2]
$b = $a
$b = append($b, 3)     // $a tetap [1, 2] — append mengembalikan salinan
$c = [1, 2]
$d = $c
$d[0] = 99             // $c[0] juga 99 — penugasan = referensi bersama

$a[0]     $a[-1]     $a[1:]     $a[1:3]
$o.kunci  $o["kunci"]
$o.kunci = "baru"      // mutasi objek
unset($o, "kunci")
```

Indeks di luar batas → **error runtime**, bukan `null` senyap. Gunakan
`at($a, $i, $default)` bila ingin nilai cadangan.

### Fungsi bawaan

| Kategori | Fungsi |
|---|---|
| Baca | `len` `first` `last` `at` `min` `max` `sum` |
| Cek | `has` `in` `keys` `values` |
| Ubah | `append` `push` `pop` `slice` `sort` `sort_by` `reverse` `unique` `merge` `unset` |
| Transformasi | `map` `filter` `reduce` `find` `join` |
| Konversi | `int` `float` `str` `bool` `to_object` `type` `is_*` |
| Serialisasi | `json_encode` `json_decode` `html_escape` |
| Error | `throw` `error` `not_found` `bad_request` `unauthorized` `forbidden` `conflict` `server_error` |

Modul: `use "strings"`, `use "math"`, `use "time"`.
Method langsung juga tersedia: `"abc".upper()`, `$arr.map(fn)`, `$arr.join(",")`.

---

## 8. Penanganan error

```ga
try {
	$u = await db.first("select * from users where id = ?", [$id])
	if $u == null { throw not_found("user tidak ada") }
} catch e {
	println "${e.status}: ${e.message}"    // e: message, code, status
	throw e                                // lempar lagi
}
```

- Hanya error dari `throw` yang bisa ditangkap; bug internal interpreter tidak.
- Error domain membawa `status` HTTP sehingga server bisa menjawab otomatis.
- Error internal (undefined variable, index out of range, division by zero)
  menyertakan stack trace dan posisi `file:line:kolom`.

---

## 9. CLI

```
gar run <file.ga> [-- args...]   jalankan program; argumen tersedia di `args`
gar repl [file.ga]               sesi interaktif
gar version | gar help
```

---

## 10. Performa (terukur vs PHP 8.2)

Mesin: 2 vCPU Xeon E5-2680 v4, RAM 2 GB, Go 1.19.8, PHP 8.2.34 (Zend Engine
v4.2.34, `opcache.enable_cli=Off`, `JIT=Off` — konfigurasi CLI PHP normal).
Program identik ada di `bench/`, dijalankan `./bench/compare.sh` (9 run, waktu
tercepat). Output kedua program diverifikasi sama sebelum dibandingkan.

### CPU

| Tes | tree-walker (v0.1) | GaLang sekarang | PHP 8.2 | Go native | GaLang vs PHP |
|---|---|---|---|---|---|
| `fib(25)` rekursi | 219 ms | **23-28 ms** | 30-36 ms | 8 ms | **menang 1,2-1,4x** |
| loop 200.001 iterasi | 93 ms | **12-15 ms** | 27-32 ms | 5 ms | **menang 2,1-2,3x** |
| 100.001 pemanggilan | 29 ms | **6-7 ms** | 27-31 ms | 5 ms | **menang 4,0-4,5x** |

Rentang, bukan satu angka: PHP di mesin ini sangat berfluktuasi (28-45 ms)
karena startup-nya besar, sedangkan GaLang stabil (23-28 ms) karena startup 4 ms.
Tiga pengukuran berturut-turut selalu GaLang menang di ketiga tes.

### Memori (RSS puncak) dan startup

| Ukuran | GaLang | PHP |
|---|---|---|
| RSS saat fib | **4-6 MB** | 19 MB (3-5x lebih besar) |
| RSS saat loop | **4 MB** | 19 MB (4-5x lebih besar) |
| Proses kosong (startup) | **4 ms** | 24-45 ms (6-10x lebih lambat) |

### Alokasi pada hot path

| Bentuk kode | v0.1 | Sekarang |
|---|---|---|
| `$t = $t + 1` per iterasi | 4 alokasi | **0** |
| `$t = $t + $i * 2 - 1` per iterasi | 4 alokasi | **0** |
| `fib(25)` total (~242.000 panggilan) | 1.092.000 alokasi | **503 alokasi** |

### Yang berubah arsitekturnya

v0.1 adalah tree-walker: tiap node membayar `switch` tipe dan tiap variabel
membayar pencarian nama. Sekarang program **dikompilasi sekali menjadi pohon
closure Go** (`compiler.go`), lalu closure itu yang dijalankan:

| Teknik | Dampak terukur |
|---|---|
| Kompilasi ke closure (hilang dispatch node) | loop 93 → 67 ms |
| Frame slot bertipe (`compiler.go`): nama → indeks, tanpa map | lanjutan |
| Aritmetika int unboxed: `frame.ints []int64`, tanpa boxing | loop alokasi 4 → 0 |
| Argumen di stack bersama, bukan slice per panggilan | fib alokasi 243.288 → 503 |
| Frame dari free-list per interpreter (bukan `sync.Pool`) | fib −20% (pool = 24% CPU) |
| Trace callsite 24 byte, dibangun lazy saat error | fib −13% |
| `checkType` jalur cepat untuk `int`/`string` | kecil, tapi di setiap return |

### Konvensi panggilan unboxed (bagian penutup)

Setelah kompilasi ke closure, `fib` masih kalah karena setiap panggilan tetap
melewati nilai kotak: daftar argumen, satu interface per argumen, dan pemeriksaan
tipe pada jalan keluar. Untuk kode angka itu seluruh biayanya.

Sekarang ada kompilator kedua, di `intfn.go`, yang mencoba membangun versi
*fungsi bilangan bulat murni* dari fungsi yang parameter dan nilai kembalinya
integer. Yang dikompilasi: aritmetika, perbandingan, variabel integer, `if`,
`while`, `for` rentang, `break`/`continue`, dan pemanggilan fungsi. Segala
selainnya membuat kompilator menyerah, dan fungsi itu tetap memakai jalur umum —
jadi kelayakan diputuskan dengan mencoba, bukan aturan terpisah.

Pada jalur ini argumen tidak pernah menjadi interface: nilainya langsung
disalin ke slot `int64` di frame. Efeknya pada `fib`:

| Tahap | fib(25) | Alokasi |
|---|---|---|
| tree-walker (v0.1) | 219 ms | 1.092.000 |
| kompilasi ke closure | 95 ms | 243.288 |
| frame slot + argumen stack + free-list | 42 ms | 503 |
| **+ konvensi panggilan unboxed** | **26 ms** | **217** |

### Bytecode VM (sudah dikerjakan)

Kompilasi ke closure menghapus dispatch, tapi satu panggilan masih berjalan
melalui rantai closure. Sekarang ada mesin virtual kedua (`intvm.go`) dengan
instruksi datar, stack frame eksplisit, dan stack nilai eksplisit. Satu
panggilan = dua operasi slice plus lompatan, tanpa rekursi Go dan tanpa alokasi
setelah stack terisi.

Ada **dua backend**, masing-masing unggul di hal berbeda, dan pilihan dibuat
otomatis per fungsi:

| Backend | Unggul pada | Alasan |
|---|---|---|
| Bytecode | kode yang banyak memanggil fungsi | panggilan jadi lompatan, bukan rantai closure |
| Closure | kode dengan loop | loop menjadi loop Go asli, bukan instruksi bytecode |

Fungsi yang mengandung loop memakai backend closure; fungsi tanpa loop memakai
bytecode. Keduanya sudah menghasilkan angka yang menang di atas PHP.

### Yang belum: cache opcode ke disk

Instruksi bytecode adalah data biasa, jadi sekarang bisa disimpan ke disk dan
dimuat ulang, persis seperti **opcache** PHP:

```
sekarang:   request → baca .ga → lex → parse → compile → jalankan
nanti:      request → baca .ga → cocokkan mtime → jalankan
```

Untuk aplikasi web, compile adalah biaya tetap per request; cache opcode
menghapusnya dengan pencocokan timestamp.

### Yang masih tersisa

`fib` menang, tapi marginnya paling tipis (1,2-1,4x) karena itu tes yang paling
banyak memanggil fungsi. Setelah cache opcode, langkah berikutnya adalah
**bytecode VM untuk string dan float** — saat ini hanya integer yang punya jalur
unboxed, jadi kode numerik dengan desimal masih melalui jalur umum.

---

## 11. Peta jalan

- **v0.2** — HTTP server (port 8869), routing, objek `request`/`response`,
  template Blade, `async fn`/`await`/`gather`/`spawn`.
- **v0.3** — database (SQLite/MySQL/PostgreSQL via `database/sql`), WebSocket,
  SSE, HTTP client, session, upload, `gar test`.
- **v0.6** — pengelola ekstensi `gar gne` (pack/install/list/remove) dan
  ekstensi resmi `redis` serta `smtp` (GNE; SMTP masih tanpa TLS).
- **v1** — generic `<T>` + analyzer tipe statis, bytecode VM + cache opcode
  (padanan `opcache` PHP), perluasan konvensi unboxed ke float dan string.
- **v2** — class & method, generic pada struct, batasan tipe (`<T: int|string>`),
  sisi browser (`.ga` → JS).
