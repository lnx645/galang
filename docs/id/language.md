# Sintaks Bahasa Garurda

## Komentar

```garurda
// Komentar satu baris
```

## Variabel

Sigil `$` opsional — `nama` dan `$nama` menunjuk ke variabel yang sama:

```garurda
$nama = "Garurda"
umur = 25
$pi = 3.14
$aktif = true
$kosong = null
```

## Tipe Data

| Tipe | Contoh | Keterangan |
|---|---|---|
| `int` | `72`, `0xFF`, `1_000` | Bilangan bulat (support hex & underscore) |
| `float` | `3.14`, `1.0e3` | Bilangan desimal |
| `string` | `"teks"`, backtick raw | String |
| `bool` | `true`, `false` | Boolean |
| `array` | `[1, 2, 3]` | Daftar |
| `object` | `{a: 1, b: 2}` | Peta/kamus |
| `function` | `fn() { ... }` | Fungsi |
| `error` | — | Error value |
| `null` | `null` | Null |
| `any` | — | Tipe apa saja (tanpa pemeriksaan) |

## Number Literals

```garurda
$desimal = 42
$heksa = 0xFF
$bawah = 1_000_000
$pecahan = 3.14
$scientific = 1.0e3
```

## Operator

```
+  -  *  /  %   // aritmatika
== != < > <= >= // perbandingan
and or not   // atau gunakan && || ! — keduanya valid
in                // keanggotaan array/object
```

## String

```garurda
$nama = "Garurda"
print("Hello, $nama!")       // interpolasi
print("Hello, ${nama}!")     // interpolasi ekspresi
$raw = `baris literal
multi baris`                  // backtick = raw string
```

## Array

```garurda
$angka = [1, 2, 3, 4, 5]
print($angka[0])       // 1
print($angka.len)      // 5
print($angka.first)    // 1
print($angka.last)     // 5
print($angka.joined)   // "1,2,3,4,5"
```

## Array Comprehension

```garurda
$kali2 = [$n * 2 for $n in [1, 2, 3]]   // [2, 4, 6]
$besar = [$n for $n in $xs if $n > 10]
```

## Object

```garurda
$user = {nama: "Dadan", umur: 25}
print($user.nama)      // Dadan
$user.nama = "Dad"     // set property
```

## Percabangan

```garurda
if $n > 0 {
    print("positif")
} else if $n < 0 {
    print("negatif")
} else {
    print("nol")
}

$status = $n > 0 ? "ok" : "gagal"   // ternary
```

## Perulangan

```garurda
// while
while $i < 10 {
    print($i)
    $i = $i + 1
}

// for array
for $item in [1, 2, 3] {
    print($item)
}

// for range — inklusif kedua sisi
for $i in 1..10 {
    print($i)
}

// for range dengan langkah
for $i in 0..10..5 {
    print($i)
}

// for range turun
for $i in 3..1 {
    print($i)
}

// for key-value pada object
for $k, $v in {a: 1, b: 2} {
    print("$k -> $v")
}
```

## Fungsi

```garurda
fn tambah(int $a, int $b) int {
    return $a + $b
}
print(tambah(3, 5))    // 8

// Closure
fn penghitung() {
    $n = 0
    return fn() {
        $n = $n + 1
        return $n
    }
}
```

## Fungsi dengan Default Parameter

```garurda
fn greet($nama, $sapaan = "Halo") {
    return "${sapaan}, ${nama}!"
}
print(greet("Dadan"))          // Halo, Dadan!
print(greet("Dadan", "Selamat")) // Selamat, Dadan!
```

## Anotasi Tipe

```garurda
fn hitung(int $a, int $b) int {
    return $a + $b
}
```

Tipe yang tersedia: `int`, `float`, `number`, `string`, `bool`, `array`, `object`, `any`, `function`, `error`, `request`, `response`, `module`.

## Modul

```garurda
use "strings"
use "math"
use "time"
use "http"
use "file"
use "database"
```

## Penanganan Error

```garurda
try {
    $u = null
    if $u == null { throw not_found("user tidak ada") }
} catch e {
    print("Error: ${e.message} (code: ${e.code})")
}
```

Error constructors:

```garurda
throw not_found("...")       // 404
throw bad_request("...")     // 400
throw unauthorized("...")    // 401
throw forbidden("...")       // 403
throw conflict("...")        // 409
throw server_error("...")    // 500
```

Error value memiliki 3 field:
- `e.message` — pesan error
- `e.code` — kode error (`not_found`, `bad_request`, ...)
- `e.status` — HTTP status code (404, 400, ...)

### finally

Blok `finally` berjalan **selalu** — saat try sukses, saat catch menangani
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
throw $r   // error lalu diteruskan setelah finally jalan
```

## Async — `async fn`, `await`, `gather`, `spawn`

Fungsi yang dideklarasikan `async` **mengembalikan promise** saat dipanggil,
bukan hasilnya langsung. Promise berisi tugas yang belum dijalankan.

```garurda
async fn ambil_user($id) {
    return {"id": $id, "nama": "Budi"}
}

$u = await ambil_user(7)     // jalankan tugasnya, ambil hasilnya
print($u.nama)
```

Aturan `await`:

- `await <promise>` — menjalankan tugasnya **sekarang** lalu mengembalikan hasil.
- `await <nilai biasa>` — nilai dilepas begitu saja (`await 42` → `42`).
- `await` pada promise yang gagal (ada `throw` di dalamnya) akan melempar error
  yang sama — bisa ditangkap `try/catch`.
- Promise bisa disimpan di variabel, lalu di-await nanti:

```garurda
$p = ambil_user(9)          // dipanggil, hasilnya belum ada
print(type($p))             // "promise"
$hasil = await $p           // dikerjakan di sini
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
// ... tugas spawn dijalankan saat drain (akhir eksekusi / akhir request HTTP)
```

`spawn` selalu menghasilkan promise; jika di-await, hasilnya dikembalikan.

### promise sebagai nilai

```garurda
print(type(await ambil_user(1)))   // "object"
print(is_promise(ambil_user(1)))   // true
```

Promise adalah tipe nilai sendiri (`promise`): bisa disimpan, dikirim ke
fungsi, dan di-await berulang kali (tugasnya hanya berjalan sekali).

*Cara kerja: eksekusi async bersifat kooperatif — tugas dijalankan oleh
`await`, `gather`, atau drain. Belum ada paralelisme I/O; itu menyusul bersama
builtin async I/O.*

## print / println

Dua bentuk didukung:

```garurda
print("nilai:", $x)      // gaya pemanggilan
print "nilai:", $x       // gaya pernyataan
println()                // baris kosong
```

## Yang Belum Ada

- Generic `<T>` — direncanakan v1
- Class & method — direncanakan v2
- Paralelisme I/O async (async I/O builtin) — eksekusi async saat ini kooperatif
- Modul `database` — fungsi masih stub/placeholder (lihat modul)
- `session`, SMTP, dan unit-test framework — direncanakan v0.3

