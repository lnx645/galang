# Sintaks Bahasa Garurda

Referensi lengkap. Semua contoh di halaman ini sudah dijalankan dan diverifikasi
pada runtime v0.2.

## 1. Leksikal

### Komentar

```garurda
// komentar satu baris
/* komentar blok
   bisa multi baris */
```

### Akhir statement

Newline mengakhiri statement; `;` opsional. Baris otomatis dilanjutkan bila
karakter terakhir adalah operator biner atau `(`, `[`, `{`.

```garurda
$total = 1 +
         2          // diteruskan ke baris berikutnya — OK
```

### Sigil `$`

Sigil `$` **opsional**. `nama` dan `$nama` menunjuk ke variabel yang sama:

```garurda
$nama = "Garurda"
print(nama)   // Garurda
```

`$` wajib dipakai bila nama variabel sama dengan kata kunci, mis. `$string`.

### Identifer

Nama variabel/fungsi: huruf, angka, `_`; tidak boleh diawali angka.
Nama properti object juga mengikuti aturan ini.

## 2. Literal

### Angka

```garurda
$desimal   = 42          // int
$heksa     = 0xFF        // int heksadesimal (255)
$underscore = 1_000_000   // pemisah ribuan (1000000)
$pecahan   = 3.14        // float
$scientific = 1.0e3      // float, 1000.0
```

### String

```garurda
$nama = "Garurda"
print("Halo, $nama!")        // interpolasi identifer
print("Halo, ${nama}!")      // interpolasi ekspresi: ${...}
print("harga 5$")            // $ tanpa identifer = literal aman

$raw = `baris literal
multi baris, tanpa escape dan tanpa interpolasi`
```

- String `"..."` mendukung escape (`\n`, `\t`, `\"`, `\\`) dan interpolasi.
- String backtick `` `...` `` raw: tanpa escape, tanpa interpolasi.
- Index string bersifat **rune-aware** (aman untuk UTF-8): `"é"[0]` → `é`.

### Boolean dan null

```garurda
$aktif = true
$kosong = null
```

### Array

```garurda
$kosong  = []
$angka   = [1, 2, 3]
$campur  = ["a", 1, true, null, [2]]
$rentang = [1..5]        // [1, 2, 3, 4, 5]
```

### Object

```garurda
$kosong = {}
$user   = {nama: "Dadan", umur: 25, "kunci spasi": true}
```

## 3. Tipe Data

| Tipe | Contoh | `type()` menghasilkan |
|---|---|---|
| `int` | `72`, `0xFF`, `1_000` | `"int"` |
| `float` | `3.14`, `1.0e3` | `"float"` |
| `string` | `"teks"`, `` `raw` `` | `"string"` |
| `bool` | `true` / `false` | `"bool"` |
| `array` | `[1, 2]` | `"array"` |
| `object` | `{a: 1}` | `"object"` |
| `null` | `null` | `"null"` |
| `function` | `fn() { }` | `"function"` |
| `promise` | hasil `async fn` | `"promise"` |
| `error` | nilai dari `throw`/`catch` | `"error"` |
| `any` | — | tipe apa saja, tanpa pemeriksaan |
| `number` | — | tipe induk `int`/`float`, hanya untuk anotasi |

Cek tipe dengan `is_int`, `is_float`, `is_string`, `is_bool`, `is_array`,
`is_object`, `is_null`, `is_error`, `is_function`, `is_promise`
(lihat [Referensi API](api.md)).

### Tanpa coerciion

```garurda
print("1" == 1)     // false — string "1" ≠ int 1
print(1 == 1.0)     // true  — int dan float dibanding numerik
// "n=" + 5         // ERROR: cannot add int to string (use str(int))
```

Konversi harus eksplisit: `int("42")`, `str(7)`, `float("1.5")`, `bool(0)`.

## 4. Variabel dan Deklarasi Bertipe

```garurda
$x = 10                    // inferensi tipe otomatis
int $n = 72                // deklarasi bertipe: wajib int
?string $s = null          // boleh null
array<int> $ids = [1, 2, 3] // array of int — elemen diperiksa
any $bebas = "apa saja"    // tanpa pemeriksaan
```

Anotasi tipe **diperiksa lalu dihapus** saat runtime — nol biaya di hot path.
Mencocokkan nilai yang salah menghasilkan error runtime yang jelas.

## 5. Operator

### Aritmatika

```
+   -   *   /   %
```

- `/` pada dua `int` menghasilkan `int` (pembulatan ke nol).
- `%` mengikuti tanda operand seperti Go: `-7 % 3` → `-1`.

### Perbandingan

```
==   !=   <   <=   >   >=
```

### Logika — dua sintaks, keduanya valid

```garurda
if $a and $b { }      if $a && $b { }
if $a or  $b { }      if $a || $b { }
if not $a { }         if !$a { }
```

### Keanggotaan `in`

```garurda
print(1 in [1, 2])          // true   — array
print("a" in {a: 1})        // true   — object (cek kunci)
print("bc" in "abcd")       // true   — substring
print(9 in [1, 2])          // false
```

### Rentang `..` (inklusif kedua sisi)

```garurda
for $i in 1..5 { }      // 1 2 3 4 5
for $i in 0..10..5 { }  // 0 5 10  — dengan langkah
for $i in 3..1 { }      // 3 2 1    — turun, arah mengikuti tanda
```

### Ternary

```garurda
$status = $n > 0 ? "ok" : "gagal"
```

### Presedensi (rendah → tinggi)

| # | Operator |
|---|---|
| 1 | `or` `\|\|` |
| 2 | `and` `&&` |
| 3 | `not` `!` (unary) |
| 4 | `==` `!=` |
| 5 | `<` `<=` `>` `>=` `in` `..` |
| 6 | `+` `-` |
| 7 | `*` `/` `%` |
| 8 | unary `-` |
| 9 | `()` `[]` `.` |

Penting: `not`/`!` mengikat **lebih longgar** dari perbandingan:
`not $a == $b` berarti `not ($a == $b)`.

Belum ada operator compound (`+=`, `-=`) dan null-coalescing (`??`).

## 6. Percabangan

```garurda
if $n > 0 {
    print("positif")
} else if $n < 0 {
    print("negatif")
} else {
    print("nol")
}

// singkat dengan ternary
$label = $n > 0 ? "positif" : "nol/minus"
```

Kurung kurawal `{ }` wajib — tidak ada bentuk tanpa kurawal.

## 7. Perulangan

### while

```garurda
$i = 0
while $i < 3 {
    $i = $i + 1
}
print($i)   // 3
```

### for-in array

```garurda
for $item in [1, 2, 3] {
    print($item)
}
```

### for-in object (kunci + nilai)

```garurda
for $k, $v in {a: 1, b: 2} {
    print("$k -> $v")     // a -> 1 ; b -> 2
}
```

### for pada string (per karakter)

```garurda
for $ch in "abc" {
    print($ch)            // a b c
}
```

### Rentang, langkah, dan arah

```garurda
for $i in 1..10 { }        // inklusif: 1..10
for $i in 0..10..5 { }     // langkah 5: 0 5 10
for $i in 3..1 { }         // turun: 3 2 1
```

### break / continue

```garurda
for $i in 1..10 {
    if $i == 5 { continue }
    if $i > 7 { break }
    print($i)              // 1 2 3 4 6 7
}
```

## 8. Array

```garurda
$a = [1, 2, 3, 4, 5]
print($a[0])      // indeks 0-based → 1
print($a[-1])     // indeks negatif (dari akhir) → 5
print($a.len)     // 5
print($a.first)   // 1
print($a.last)    // 5
print($a.joined)  // "1,2,3,4,5"
```

### Slicing

```garurda
$a = [1, 2, 3, 4, 5]
print($a[1:3])    // [2, 3]        — from:to, to eksklusif
print($a[2:])     // [3, 4, 5]     — dari index 2
print($a[:2])     // [1, 2]        — sampai index 2
print($a[:])      // [1, 2, 3, 4, 5] — salinan penuh

$s = "Halo Dunia"
print($s[0:4])    // "Halo" — slicing string juga berlaku
```

Indeks di luar batas → **error runtime** (bukan `null` senyap). Bila ingin
nilai cadangan, pakai `at($a, $i, $default)`.

### Array comprehension

```garurda
$kali2 = [$n * 2 for $n in [1, 2, 3]]        // [2, 4, 6]
$besar = [$n for $n in [1, 12, 3] if $n > 10] // [12]
```

### Referensi vs salinan (penting)

- **Penugasan membagi referensi** untuk array dan object:
  `$b = $a` lalu `$b[0] = 99` membuat `$a` ikut berubah.
- **Fungsi bawaan mengembalikan salinan baru**: `append`, `push`, `pop`,
  `sort`, `reverse`, `slice`, `map`, `filter` tidak mengubah array asli.

```garurda
$a = [1, 2]
$b = $a
$b = append($b, 3)     // $a tetap [1, 2]  — append mengembalikan array baru
$c = [1, 2]
$d = $c
$d[0] = 99             // $c[0] juga jadi 99 — assignment = referensi bersama
```

## 9. Object

```garurda
$user = {nama: "Dadan", umur: 25}
print($user.nama)       // akses dot
print($user["umur"])    // akses bracket
$user.nama = "Dad"      // set property (mutasi in-place)
$user.email = "a@b.c"   // tambah kunci baru
unset($user, "umur")    // hapus kunci
print(keys($user))      // [nama, email]
print(values($user))    // [Dad, a@b.c]
print($user in {nama: 1}) // true — cek keberadaan kunci
```

Object juga bersifat **referensi** saat penugasan (sama seperti array).

## 10. Fungsi

```garurda
fn tambah(int $a, int $b) int {
    return $a + $b
}
print(tambah(3, 5))         // 8

fn greet($nama, $sapaan = "Halo") {
    return "${sapaan}, ${nama}!"
}
print(greet("Dadan"))           // Halo, Dadan!
print(greet("Dadan", "Selamat")) // Selamat, Dadan!
```

- Anotasi tipe parameter & return bersifat **opsional** dan diperiksa saat
  runtime, lalu dihapus (nol biaya).
- Parameter default boleh dicampur dengan wajib (yang default di belakang).

### Arrow function (satu ekspresi)

```garurda
fn kali($x) => $x * 2
print(kali(21))    // 42
```

### Fungsi first-class & closure

```garurda
$f = fn($a, $b) { return $a + $b }
print($f(2, 3))    // 5

fn penghitung() {
    $n = 0
    return fn() {
        $n = $n + 1
        return $n
    }
}
$c = penghitung()
print($c())   // 1
print($c())   // 2
```

Closure menangkap lingkungannya; scope fungsi adalah turunan dari scope
pendefinisian. Batas kedalaman rekursi **2000** dengan pesan yang jelas.

## 11. Error Handling

```garurda
try {
    $u = null
    if $u == null { throw not_found("user tidak ada") }
} catch e {
    print("Error: ${e.message} (code: ${e.code}, status: ${e.status})")
}
```

### Konstruktor error

```garurda
throw error("kegagalan umum")     // status 500
throw bad_request("input salah")  // 400
throw unauthorized("belum login") // 401
throw forbidden("dilarang")       // 403
throw not_found("tidak ada")      // 404
throw conflict("duplikat")        // 409
throw server_error("gagal")       // 500
```

### Nilai error

Setiap nilai error punya field:

| Field | Isi |
|---|---|
| `e.message` | pesan error |
| `e.code` | `"error"` untuk `error()`, `"http_error"` untuk keenam konstruktor HTTP |
| `e.status` | kode status HTTP (404, 400, 401, 403, 409, 500) |
| `e.stack` | stack trace (untuk error internal) |

Untuk membedakan jenis error, gunakan `e.status` (mis. `e.status == 404`)
atau `e.is($code)` yang membandingkan `e.code`:

```garurda
try {
    throw not_found("x")
} catch e {
    print(e.is("http_error"))    // true
    print(e.is("not_found"))     // false — code-nya "http_error"
    print(e.status)              // 404
}
```

### Apa yang bisa ditangkap?

- **Bisa** ditangkap: error dari `throw` (termasuk konstruktor error).
- **Tidak bisa** ditangkap — menghentikan program: error internal interpreter
  seperti variabel undefined, index di luar batas, pembagian nol.

```garurda
try { $x = undefined_var } catch e { print("tangkap") }
// → program berhenti dengan stack trace, catch TIDAK dijalankan
```

### finally — selalu berjalan

Blok `finally` dieksekusi **selalu**: saat try sukses, saat catch menangani
error, maupun saat error diteruskan (rethrow). Ini tempat membersihkan sumber
daya.

```garurda
try {
    $data = file.read("config.json")
} catch $e {
    print("gagal baca")
} finally {
    print("bersihkan di sini")   // selalu dieksekusi
}
```

`finally` tanpa `catch` juga valid:

```garurda
try {
    $r = risky()
} finally {
    $conn.close()
}
throw $r   // error diteruskan setelah finally jalan
```

## 12. Async — `async fn`, `await`, `gather`, `spawn`

Fungsi yang dideklarasikan `async` **mengembalikan promise** saat dipanggil,
bukan hasilnya langsung.

```garurda
async fn ambil_user($id) {
    return {"id": $id, "nama": "Budi"}
}

$u = await ambil_user(7)     // jalankan tugasnya, ambil hasilnya
print($u.nama)               // Budi
```

Aturan `await`:

- `await <promise>` — menjalankan tugasnya **sekarang** lalu mengembalikan hasil.
- `await <nilai biasa>` — nilai dilewat begitu saja (`await 42` → `42`).
- `await` pada promise yang gagal (ada `throw` di dalamnya) melempar error yang
  sama — bisa ditangkap `try/catch`.
- Promise bisa disimpan dulu, di-await nanti, dan **di-await berulang kali**
  (tugasnya hanya berjalan sekali):

```garurda
$p = ambil_user(9)
print(type($p))             // "promise"
$hasil = await $p           // dikerjakan di sini
print(is_promise($p))       // true
```

### `gather` — selesaikan banyak promise sekaligus

```garurda
async fn a() { return 1 }
async fn b() { return 2 }

$r = gather(a(), b())       // [1, 2]
print($r[0] + $r[1])        // 3
```

`gather(...)` menerima nilai biasa juga (langsung dipakai apa adanya).
Error dari salah satu promise langsung melempar.

### `spawn` — tugas latar belakang (fire-and-forget)

```garurda
fn catat($pesan) { print("log: " + $pesan) }

spawn(catat, "user login")  // dijanjikan, bukan dijalankan di sini
print("program lanjut")
// tugas spawn dijalankan saat drain (akhir eksekusi / akhir request HTTP)
```

`spawn` selalu menghasilkan promise; jika di-await, hasilnya dikembalikan.

*Cara kerja:* eksekusi async bersifat **kooperatif** — tugas dijalankan oleh
`await`, `gather`, atau drain. Belum ada paralelisme I/O; itu menyusul bersama
builtin async I/O.

## 13. print / println

Dua bentuk didukung:

```garurda
print("nilai:", $x)      // gaya pemanggilan
print "nilai:", $x       // gaya pernyataan
println("selesai")       // satu baris
println()                // baris kosong
println "a", "b"         // pernyataan
```

## 14. Modul

```garurda
use "strings"
use "math"
use "time"
use "file"
use "http"
use "database"    // masih stub
```

Detail: [Modul Standar](modules.md).

## 15. Yang Belum Ada

- Generic `<T>` — direncanakan v1
- Class & method — direncanakan v2
- Operator compound (`+=`) dan `??`
- Paralelisme I/O async (async I/O builtin) — async saat ini kooperatif
- Modul `database` — fungsi masih stub (lihat [Modul Standar](modules.md))
- `session`, SMTP, dan unit-test framework — direncanakan v0.3
