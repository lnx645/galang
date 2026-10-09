# Modul Standar

Modul diaktifkan dengan `use "nama"` lalu diakses lewat namespace `nama.fungsi()`.

```garurda
use "strings"
strings.upper("hello")    // HELLO
```

Daftar modul: `strings`, `math`, `time`, `file`, `http`, `database`.

Selain itu ada **ekstensi resmi** `redis` dan `smtp` — modul `use` yang
sama, tetapi binarnya dipasang lewat `gar gne install <nama>` (butuh `gar`
0.6.0+). API lengkapnya ada di bagian paling bawah halaman ini.

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

Koneksi nyata ke **SQLite**, **MySQL**, atau **PostgreSQL** lewat
`database/sql` — driver dipilih dari prefiks DSN.

```garurda
use "database"

$db = database.connect("sqlite:///app.db")   // atau "sqlite:app.db"
$rows = $db.query("SELECT * FROM users")
$one  = $db.query_first("SELECT * FROM users WHERE id = ?", [$id])
$row  = $db.query_row("SELECT count(*) AS n FROM users")
$res  = $db.exec("INSERT INTO users (name) VALUES (?)", ["Dadan"])
$db.close()
```

| Method | Hasil |
|---|---|
| `$db.query(sql, [$params])` | array objek — satu per baris; `[]` bila kosong |
| `$db.query_first(sql, [$params])` | objek baris pertama, atau `null` |
| `$db.query_row(sql, [$params])` | objek baris, atau `null` |
| `$db.exec(sql, [$params])` | `{rows_affected, last_insert_id}` |
| `$db.close()` | tutup koneksi (return `null`) |
| `$db.driver` | nama driver: `sqlite3` / `mysql` / `postgres` |

- **DSN**: `sqlite:path` atau path biasa (termasuk `sqlite::memory:`)
  → SQLite; `mysql:...` / `mysql://...` → MySQL;
  `postgres://...` / `postgresql://...` → PostgreSQL.
- **Parameter** opsional dikirim sebagai **array**. Placeholder mengikuti
  driver: `?` untuk sqlite/mysql, `$1, $2, ...` untuk postgres.
- **Tipe hasil**: NULL → `null`, INTEGER → number, REAL → number,
  teks/`[]byte` → string, waktu (driver yang mendukung) → string
  RFC3339. SQLite menyimpan `true/false` sebagai 1/0 (integer).
- **Galat**: koneksi gagal atau SQL salah melempar error `db_error`
  (status 500) yang bisa ditangkap `try/catch` — `e.code` = `"db_error"`.
  Salah bentuk argumen (mis. parameter bukan array) adalah galat
  pemrograman seperti biasa.
- **CGO untuk SQLite**: `gar-linux-amd64`, `gar-windows-amd64`, dan
  `gar-darwin-*` (dibangun CI dengan CGO) — SQLite aktif di ketiganya;
  biner cross lain (linux/arm64, windows/arm64) dibangun tanpa CGO —
  SQLite tidak aktif di sana, tetapi MySQL dan PostgreSQL (pure Go) tetap
  berfungsi. CGO juga menentukan ketersediaan [ekstensi native
  GNE](gne.md): biner tanpa CGO menolak `use` ekstensi dengan pesan
  "butuh CGO".

---

## http

Server HTTP lengkap: routing, request/response, template Blade, file statis,
SSE, dan WebSocket. Lihat dokumentasi lengkap di
[Web Runtime](web.md).

```garurda
use "http"

http.GET("/", fn($req) { return "Halo" })
http.listen(8869)
```

---

## redis — ekstensi resmi

Klien **Redis** (RESP2) berbasis [GNE](gne.md). Binarnya tidak ikut di
dalam `gar` — pasang dulu dari aset rilis:

```bash
gar gne install redis          # rilis terbaru; versi: gar gne install redis@0.6.0
```

```garurda
use "redis"

$r = redis.connect("127.0.0.1", 6379)         // timeout bawaan 5000 ms
$r = redis.connect("127.0.0.1", 6379, 1000)   // timeout 1 detik

$r.ping()                         // true
$r.set("user:1", "dadan")         // "OK"
$r.get("user:1")                  // "dadan" (kunci absen → null)
$r.incr("visits")                 // number, nilai sesudah +1
$r.exists("user:1")               // true / false
$r.del("user:1")                  // number — jumlah kunci terhapus
$r.expire("user:1", 60)           // true — kedaluwarsa dalam 60 detik

$r.hset("profil", "nama", "Ayu")  // 1 = medan baru, 0 = perbarui
$r.hget("profil", "nama")         // "Ayu" (medan absen → null)

$r.lpush("antrian", "a", "b")     // number — panjang list sesudah push
$r.lrange("antrian", 0, -1)       // [b, a]
$r.keys("user:*")                 // [user:1, user:2]

$r.cmd("TTL", "profil")           // balasan diolah otomatis
$r.close()                        // true; dipanggil lagi → false
```

| Method | Argumen | Hasil |
|---|---|---|
| `redis.connect(host, port[, timeout_ms])` | 2–3 | objek koneksi; timeout bawaan 5000 ms |
| `$r.ping()` | 0 | `true` |
| `$r.get(k)` | 1 | string, atau `null` bila kunci absen |
| `$r.set(k, v)` | 2 | balasan server (`"OK"`); `v` berupa string atau number |
| `$r.del(k)` | 1 | number — jumlah kunci terhapus |
| `$r.exists(k)` | 1 | bool |
| `$r.incr(k)` | 1 | number — nilai sesudah inkrementasi |
| `$r.expire(k, detik)` | 2 | bool |
| `$r.hset(k, medan, v)` | 3 | number — `1` medan baru, `0` perbarui |
| `$r.hget(k, medan)` | 2 | string, atau `null` |
| `$r.lpush(k, v, ...)` | 2+ | number — panjang list sesudah push |
| `$r.lrange(k, start, stop)` | 3 | array string (`stop = -1` sampai akhir) |
| `$r.keys(pola)` | 1 | array string |
| `$r.cmd(perintah, ...)` | 1+ | balasan diolah sesuai tipenya: simple string → string, integer → number, bulk → string, array → array (rekursif), `$-1`/`*-1` → `null` |
| `$r.close()` | 0 | `true` sekali, `false` bila diulang (idempoten) |

- **Galat** (semua bisa ditangkap `try/catch`): balasan error `-ERR` dari
  server → `redis_error` status 500; koneksi ditolak/putus → 502; waktu
  tunggu habis → 504. Objek bekas (`setelah close()`, atau slot koneksi
  yang terpakai ulang oleh `connect` baru) melempar `redis_error` 500
  "koneksi sudah ditutup atau tidak valid". Argumen selain string/number
  → `type_error`.
- Maksimal **64 koneksi** per interpreter; balasan dibatasi ketat
  (bulk maksimal 64 MiB, kedalaman array 32 tingkat, dan batas jumlah
  elemen) supaya server nakal tidak bisa menghabiskan memori.
- **Butuh CGO**: binari `gar` tanpa CGO menolak `use` dengan pesan jelas
  (tabel platform di [GNE](gne.md#platform)).

---

## smtp — ekstensi resmi

Klien **SMTP** (RFC 5321/5322) berbasis [GNE](gne.md) untuk mengirim surel
lewat AUTH LOGIN. Pasang dulu:

```bash
gar gne install smtp
```

Satu surel = satu transaksi `from()` → `to()` (boleh berulang) →
`send()`/`send_html()`:

```garurda
use "smtp"

$s = smtp.connect("smtp.internal", 25)   // timeout bawaan 5000 ms
$s.auth("pengirim@contoh.id", "rahasia") // opsional — AUTH LOGIN
$s.from("pengirim@contoh.id")
$s.to("satu@contoh.id")
$s.to("dua@contoh.id")                   // penerima lain, ulangi sesuai perlu
$s.subject("Halo dari Garurda")
$s.send("Baris pertama.\nBaris kedua.")
$s.close()                               // true; dipanggil lagi → false
```

Surel HTML dikirim multipart/alternative (versi teks + HTML) —
penerima klien lama tetap bisa membaca:

```garurda
$s.from("pengirim@contoh.id")
$s.to("penerima@contoh.id")
$s.subject("Kabar")
$s.send_html("Kabar <b>penting</b> (versi teks)", "<p>Kabar <b>penting</b></p>")
```

| Method | Argumen | Hasil |
|---|---|---|
| `smtp.connect(host, port[, timeout_ms])` | 2–3 | objek sesi; timeout bawaan 5000 ms |
| `$s.auth(user, pass)` | 2 | `true` setelah server membalas 235 |
| `$s.from(alamat)` | 1 | `true` — buka transaksi (mengirim `RSET` dulu bila transaksi lama belum terkirim) |
| `$s.to(alamat)` | 1 | `true` — tambah penerima; maksimal 100 per transaksi |
| `$s.subject(teks)` | 1 | `true` — maksimal 700 oktet, tanpa CR/LF |
| `$s.send(body)` | 1 | `true` — kirim sebagai `text/plain` |
| `$s.send_html(teks, html)` | 2 | `true` — kirim multipart/alternative |
| `$s.close()` | 0 | `true` sekali, `false` bila diulang (`QUIT` best-effort — tidak pernah melempar galat) |

- **Urutan wajib**: `from()` lalu minimal satu `to()` sebelum `send()` /
  `send_html()`. `subject()` dan `auth()` bebas dipanggil kapan saja
  selama sesi hidup; `subject()` terakhir yang dipakai saat surel dikirim.
- **Yang dikerjakan otomatis**: dot-stuffing dan normalisasi CRLF pada
  badan; pemilihan Content-Transfer-Encoding (ASCII murni → `7bit`,
  non-ASCII + kemampuan `8BITMIME` → `8bit`, selain itu `base64`);
  subjek non-ASCII diubah menjadi encoded-word RFC 2047
  (`=?UTF-8?B?...?=`); header `Date` (UTC, tanpa locale) dan `Message-ID`
  selalu dibuat; header `To:` dilipat bila melebihi 78 oktet.
- **Batas**: pesan maksimal 32 MiB; alamat harus ASCII cetak 1–320 oktet
  tanpa spasi dan tanda `<>,`; satu objek sesi = satu koneksi — `close()`
  dulu bila ingin membuka sesi baru. Setelah `send()` sukses, transaksi
  ditutup dan daftar penerima dikosongkan — surel berikutnya harus mulai
  lagi dari `from()` lalu `to()` (memanggil `from()` saat transaksi lama
  masih berjalan otomatis mengirim `RSET` lebih dulu).
- **Galat** — kode `smtp_error`: **500** untuk penolakan/protokol/validasi
  (balasan server seperti `550` penerima atau `535` autentikasi ikut
  disebut di pesan), **502** koneksi ditolak/putus, **504** waktu tunggu
  habis. Galat I/O di tengah sesi membuat sesi gugur — method berikutnya
  melempar 500. Argumen salah bentuk (subjek menyisipkan CR/LF, port
  bukan angka) → `type_error`.
- **Batasan yang perlu diketahui — tanpa TLS**: STARTTLS/SSL belum ada di
  v0.6.0; koneksi dan AUTH LOGIN berjalan **plaintext**. Hanya gunakan
  untuk server yang memang menerima AUTH tanpa TLS (relay internal, MTA
  lokal) — jangan mengirim kredensial ke jaringan publik. Kemampuan EHLO
  dicek lebih dulu: server tanpa EHLO otomatis fallback `HELO`, dan
  `auth()` akan menolak dengan pesan yang menyebut STARTTLS.
