# Modul Standar

Modul diaktifkan dengan `use "nama"` lalu diakses lewat namespace `nama.fungsi()`.

```garurda
use "strings"
strings.upper("hello")    // HELLO
```

Daftar modul: `strings`, `math`, `time`, `file`, `http`, `database`.

---

## strings

12 fungsi untuk manipulasi string.

```garurda
use "strings"

strings.upper("hello")            // HELLO
strings.lower("HELLO")            // hello
strings.trim("  hi  ")            // "hi"
strings.split("a,b,c", ",")       // [a, b, c]
strings.replace("abcabc", "a", "x") // xbcxbc
strings.starts_with("hello", "he")  // true
strings.ends_with("hello", "lo")    // true
strings.index_of("hello", "ll")     // 2   (posisi; -1 bila tidak ada)
strings.pad_start("7", 3, "0")      // "007"
strings.repeat("ab", 3)             // ababab
strings.char_at("hello", 1)         // "e"
strings.escape_html("<b>")          // &lt;b&gt;
```

| Fungsi | Argumen | Hasil |
|---|---|---|
| `upper(s)` | 1 | huruf besar |
| `lower(s)` | 1 | huruf kecil |
| `trim(s)` | 1 | hapus spasi tepi |
| `split(s, sep)` | 2 | array potongan |
| `replace(s, old, baru)` | 3 | ganti semua |
| `starts_with(s, pre)` | 2 | bool |
| `ends_with(s, suf)` | 2 | bool |
| `index_of(s, sub)` | 2 | posisi int, `-1` bila tidak ada |
| `pad_start(s, lebar, char)` | 3 | string rata kanan |
| `repeat(s, n)` | 2 | ulangi |
| `char_at(s, i)` | 2 | satu karakter |
| `escape_html(s)` | 1 | escape HTML |

Banyak fungsi di atas juga tersedia sebagai **method** langsung:
`"abc".upper()`, `"abc".split(",")` — lihat
[Referensi API — Method string](api.md#method-string).

---

## math

```garurda
use "math"

math.floor(3.7)     // 3
math.ceil(3.2)      // 4
math.round(3.5)     // 4
math.pow(2, 10)     // 1024
math.sqrt(16)       // 4
math.abs(-5)        // 5
math.min(3, 7)      // 3
math.max(3, 7)      // 7
math.sum([1, 2, 3]) // 6
print(math.pi)      // 3.141592653589793 — konstanta, bukan fungsi
print(math.e)       // 2.718281828459045 — konstanta
```

| Fungsi | Keterangan |
|---|---|
| `floor(x)` | pembulatan ke bawah |
| `ceil(x)` | pembulatan ke atas |
| `round(x)` | pembulatan terdekat |
| `pow(b, e)` | pangkat |
| `sqrt(x)` | akar (x ≥ 0) |
| `abs(x)` | nilai mutlak |
| `min(a, b, ...)` | terkecil |
| `max(a, b, ...)` | terbesar |
| `sum(arr)` | jumlah elemen |
| `pi`, `e` | konstanta (akses sebagai properti) |

Catatan: `math.random()` **belum ada**. Fungsi angka global seperti `abs`,
`min`, `max`, `sum` juga tersedia tanpa `use`.

---

## time

```garurda
use "time"

time.now_ms()                     // 1728000000000 — unix ms
time.now()                        // "2026-10-08T12:00:00Z" — ISO 8601
time.format(time.now_ms(), "2006-01-02")   // "2026-10-08"
```

| Fungsi | Keterangan |
|---|---|
| `now_ms()` | timestamp unix milidetik |
| `now()` | string ISO 8601 |
| `format(ms, layout)` | format timestamp; layout mengikuti referensi Go (`2006-01-02`, `15:04:05`) |

---

## file

```garurda
use "file"

file.read("data.txt")                 // isi file sebagai string
file.write("output.txt", "isi")       // tulis/timpa file
```

| Fungsi | Keterangan |
|---|---|
| `read(path)` | baca seluruh file → string; file tidak ada → error runtime |
| `write(path, isi)` | tulis string ke file (buat/timpa) |

---

## database

```garurda
use "database"

$db = database.connect("sqlite:///app.db")
$rows = $db.query("SELECT * FROM users")
$one  = $db.query_first("SELECT * FROM users WHERE id = ?", [$id])
$row  = $db.query_row("SELECT count(*) FROM users")
$res  = $db.exec("INSERT INTO users (name) VALUES (?)", ["Dadan"])
```

**Catatan penting: fungsi `database` saat ini masih stub/placeholder** —
nama dan tanda tangannya sudah terpasang, tetapi belum ada koneksi database
nyata di baliknya. Ini direncanakan sebagai langkah berikutnya setelah v0.2.

| Fungsi | Rencana |
|---|---|
| `connect(dsn)` | buka koneksi (dsn `sqlite:///...`, dsb.) |
| `query(sql, params)` | SELECT → semua baris |
| `query_first(sql, params)` | SELECT → satu baris pertama |
| `query_row(sql, params)` | SELECT → satu baris |
| `exec(sql, params)` | INSERT/UPDATE/DELETE → hasil eksekusi |

---

## http

Server HTTP lengkap: routing, request/response, template Blade, file statis,
SSE, dan WebSocket. Lihat dokumentasi lengkap di
[Web Runtime](web.md).

```garurda
use "http"

http.GET("/", fn($req) { return "Halo" })
http.listen(8868)
```
