# Spesifikasi Bahasa Garurda

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
| Interpreter (tree-walking, closure, try/catch) | ✅ selesai |
| Bawaan: array/object/strconv/json/html-escape | ✅ selesai |
| Modul: `strings`, `math`, `time` | ✅ selesai |
| CLI: `gar run`, `gar repl`, `gar version`, `gar help` | ✅ selesai |
| Server HTTP + routing + `response` | ⬜*v0.2* |
| Template Blade | ⬜*v0.2* |
| Async (`async fn`, `await`, `gather`, `spawn`) | ⬜*v0.2* |
| Database (MySQL/PostgreSQL/Site) | ⬜*v0.3* |
| WebSocket + SSE | ⬜*v0.3* |
| SMTP, HTTP client | ⬜*v0.3* |
| Generic `<T>` | ⬜*v1* (anotasi `array<T>` sudah ada di v0.1) |

---

## 3. Leksikal

```ga
// komentar baris
/* komentar blok */

$nama = "Garurda"        // escaped, mendukung ${expr} dan $nama
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

**Semantik nilai dengan copy-on-write.** Assignment menyalin; tidak ada bug
aliasing. `append` selalu mengembalikan array baru.

```ga
$a = [1, 2]
$b = $a
$b = append($b, 3)     // $a tetap [1, 2]

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

## 10. Performa (terukur vs PHP, 2026-10-07)

Mesin: 2 vCPU Xeon E5-2680 v4, RAM 2 GB, Go 1.19.8, PHP 8.2.34 (Zend Engine v4.2.34).
Program identik ada di `bench/` (`.ga` dan `php/`), dijalankan lewat
`./bench/compare.sh` — 5 kali, yang dicatat waktu tercepat. Output kedua
program diverifikasi identik sebelum dibandingkan.

**PHP berjalan apa adanya:** `opcache.enable_cli=Off`, `JIT=Off` — konfigurasi
CLI PHP yang normal. Mengaktifkan opcache tidak mengubahnya (fib 31 ms).

### CPU (waktu proses terbaik dari 5 run)

| Tes | Garurda v0.1 | PHP 8.2 | Go native | Selisih vs PHP |
|---|---|---|---|---|
| `fib(25)` rekursi | 219 ms | **31 ms** | 8 ms | **7,1x lebih lambat** |
| loop 200.001 iterasi | 93 ms | **25 ms** | 5 ms | **3,7x lebih lambat** |
| 100.001 pemanggilan fungsi | 29 ms | **24 ms** | 5 ms | **1,2x lebih lambat** |

### Memori (RSS puncak)

| Tes | Garurda | PHP | Garurda hemat |
|---|---|---|---|
| fib | 10,3 MB | 18,9 MB | **1,8x** |
| loop | 8,6 MB | 16,6 MB | **1,9x** |
| call | 8,0 MB | 19,0 MB | **2,4x** |

### Kesimpulan jujur

**Target "menang telak atas PHP" TIDAK tercapai di v0.1. PHP menang di ketiga
tes CPU; Garurda menang di memori.** Ini hasil yang harus dicatat apa adanya,
bukan dikasih jargon.

Mengapa tree-walker kalah dari PHP 8: PHP mengubah kode menjadi **opcode** lalu
menjalankannya di VM yang sangat rapat, dengan opcode khusus integer (tanpa
boxing) dan dispatch lewat lompat langsung. Tree-walker pays every
`switch node.Type` dan pays every name lookup. Itu bukan detail kecil; itu
perbedaan arsitektur.

### Di mana waktu hilang (hasil pprof)

- **36% `runtime.mallocgc`** — masih banyak boxing nilai.
- **~20% `mapaccess2_faststr` + `aeshashbody`** — pencarian variabel berbasis
  nama tiap akses.
- Sisanya: dispatch AST dan alokasi scope per panggilan fungsi.

### Rencana performantya (urutan impact)

| Langkah | Dampak harapan | Status |
|---|---|---|
| Frame slot: resolve nama → `(depth, index)` saat parse | Menghapus map lookup seluruhnya (~20%) | v0.2 |
| Arena untuk AST (satu alokasi per program) | Menghapus alokasi parse berulang | v0.2 |
| Stack & frame di-preallocate; nol alokasi per panggilan | Menghapus `mallocgc` dari call path | v1 |
| **Bytecode VM** dengan opcode bertipe (`ADD_INT`) | Menyamai arsitektur PHP — ini syarat menang | v1 |
| Unboxed storage untuk `array<int>` | Memori, bukan CPU | v1 |

Jujur soal batasnya: **menang telak atas PHP pada beban CPU murni butuh bytecode
VM**, dan itu v1. Dengan tree-walker, target realistis adalah "sebanding atau
kalah tipis" setelah frame slot + arena. Klaim kemenangan baru sah setelah
`./bench/compare.sh` benar-benar menaruh Garurda di semua baris.

Yang **sudah** unggul dan relevan untuk use case web: memori (1,8-2,4x lebih
hemat), dan async I/O yang akan tiba di v0.2 — dua hal yang tidak diukur di
sini, tapi justru penyebab utama PHP lambat untuk web.

---

## 11. Peta jalan

- **v0.2** — HTTP server (port 8868), routing, objek `request`/`response`,
  template Blade, `async fn`/`await`/`gather`/`spawn`.
- **v0.3** — database (SQLite/MySQL/PostgreSQL via `database/sql`), WebSocket,
  SSE, SMTP, HTTP client, session, upload, `gar test`.
- **v1** — generic `<T>` + analyzer tipe statis, bytecode VM + cache opcode,
  benchmark pembanding PHP/Go.
- **v2** — class & method, generic pada struct, batasan tipe (`<T: int|string>`),
  sisi browser (`.ga` → JS).
