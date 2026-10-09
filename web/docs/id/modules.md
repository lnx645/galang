# Modul Standar

Modul diaktifkan dengan `use "nama"` lalu diakses lewat namespace `nama.fungsi()`.

```galang
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

```galang
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

```galang
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

```galang
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

```galang
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

```galang
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

```galang
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

```galang
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

```galang
use "smtp"

$s = smtp.connect("smtp.internal", 25)   // timeout bawaan 5000 ms
$s.auth("pengirim@contoh.id", "rahasia") // opsional — AUTH LOGIN
$s.from("pengirim@contoh.id")
$s.to("satu@contoh.id")
$s.to("dua@contoh.id")                   // penerima lain, ulangi sesuai perlu
$s.subject("Halo dari GaLang")
$s.send("Baris pertama.\nBaris kedua.")
$s.close()                               // true; dipanggil lagi → false
```

Surel HTML dikirim multipart/alternative (versi teks + HTML) —
penerima klien lama tetap bisa membaca:

```galang
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

### Contoh nyata: Gmail lewat relay TLS lokal

Ekstensi belum punya TLS, sedangkan Gmail menolak kiriman langsung port 25
dari IP tanpa SPF/DKIM (`550 5.7.26`). Pola yang teruji: `AUTH LOGIN`
dijalankan ke **localhost** — kredensial tidak menyeberangi jaringan publik
tanpa enkripsi — dan stunnel mengenkripsi ke `smtp.gmail.com:465` dengan
verifikasi sertifikat penuh. Bind `127.0.0.1` di bawah disengaja: relay
hanya boleh diakses dari mesin itu sendiri. Skrip siap pakai:
[`examples/smtp-gmail.ga`](https://github.com/lnx645/galang/blob/master/examples/smtp-gmail.ga).

1. **Binari + ekstensi.** Unduh `gar-windows-amd64.zip` (Linux: varian
   sesuai mesin) dari [rilis terbaru](https://github.com/lnx645/galang/releases/latest),
   taruh `gar.exe`/`gar` di PATH, lalu `gar gne install smtp`. Varian
   ekstensi: darwin-{amd64,arm64}, linux-{amd64,arm64}, windows-amd64 —
   di **Windows on ARM** pakai binari amd64 (dijalankan lewat emulasi x64
   Windows).
2. **Sandi aplikasi Google**: Akun Google → Keamanan → Verifikasi 2
   langkah → Sandi aplikasi (16 karakter).
3. **stunnel**: Windows — unduh `stunnel-latest-win64-installer.exe` dari
   [stunnel.org](https://www.stunnel.org/downloads.html); Linux —
   `apt install stunnel4` (nama paket dapat berbeda per distro). Tulis
   konfigurasi (lokasi berkas default ditampilkan `stunnel -version`):

   ```ini
   [gmail-smtps]
   client = yes
   accept = 127.0.0.1:2526
   connect = smtp.gmail.com:465
   verifyChain = yes
   CAfile = ca-certificates.crt
   checkHost = smtp.gmail.com
   sni = smtp.gmail.com
   ```

   `CAfile` menunjuk bundel CA PEM: Debian Linux memakai
   `/etc/ssl/certs/ca-certificates.crt`; bila paket Windows tidak
   menyertakannya, unduh [cacert.pem](https://curl.se/ca/cacert.pem) dan
   simpan dengan nama itu. Di Windows tulis **path absolut** — terutama
   bila stunnel dijalankan sebagai layanan.
4. **Jalankan stunnel.** Windows (prompt admin, dari folder konfigurasi):
   `stunnel -install stunnel.conf` lalu `stunnel -start` (kendali
   `-reload`/`-stop`, lepas `-uninstall`). Linux: tulis berkas
   `/etc/stunnel/gmail.conf` (nama layanan = nama berkas) lalu
   `systemctl enable --now stunnel@gmail`.
5. **Tes.** Isi `<APP_PASSWORD>` pada contoh, lalu
   `gar run examples/smtp-gmail.ga`. Yang diharapkan: `kirim : true` dan
   surel muncul di inbox.
   - `550 5.7.26 ... unauthenticated` — kiriman melewati relay (stunnel
     mati atau port salah).
   - `535` — sandi aplikasi salah atau kedaluwarsa.

Hasil tes nyata 2026-10-09: kiriman langsung ke MX Gmail (port 25)
diterima sampai `RCPT`/`DATA` lalu ditolak kebijakan Google; jalur di
atas diterima `250` dan masuk inbox. Langkah yang sama berlaku di Linux —
bedanya hanya pemasangan stunnel.

---

## uuid — ekstensi resmi

Pembuat dan pemeriksa **UUID** (RFC 9562) berbasis [GNE](gne.md): v4
acak statis dan v7 berstempel waktu. Pasang dulu:

```bash
gar gne install uuid
```

```galang
use "uuid"

uuid.v4()                 // "f47ac10b-58cc-4372-a567-0e02b2c3d479"
uuid.v7()                 // "018f1a2b-3c4d-7ef0-..." — awalan stempel waktu
uuid.is_valid(uuid.v4())  // true
uuid.is_valid("buku")     // false
```

| Fungsi | Argumen | Hasil |
|---|---|---|
| `uuid.v4()` | 0 | string kanonik 8-4-4-4-12 (huruf kecil), versi 4, varian RFC |
| `uuid.v7()` | 0 | idem dengan versi 7 — 48 bit pertama adalah stempel waktu milidetik (epoch Unix), sisanya acak |
| `uuid.is_valid(s)` | 1 | bool — format kanonik + heksadesimal + versi 0–8 + varian RFC (8/9/a/b); huruf besar dan kecil keduanya diterima |

- Angka acak diambil dari `/dev/urandom` (Linux/macOS) atau
  `BCryptGenRandom` (Windows) — CSPRAN, bukan `rand()`.
- Galat: argumen selain string → `type_error`. `is_valid` tidak pernah
  melempar galat untuk string apa pun (kembali `false`).
- **Butuh CGO** (tabel platform di [GNE](gne.md#platform)).

---

## jwt — ekstensi resmi

**JWT** (RFC 7519) berbasis [GNE](gne.md) dengan tanda tangan HMAC-SHA2:
HS256/HS384/HS512. Pasang dulu:

```bash
gar gne install jwt
```

```galang
use "jwt"

$t = jwt.sign("{\"sub\":\"budi\"}", "rahasia")   // HS256 (bawaan)
$t = jwt.sign("{\"sub\":\"budi\"}", "rahasia", "HS512")
jwt.verify($t, "rahasia")  // string klaim JSON — signature + exp/nbf lolos
jwt.decode($t)             // string klaim JSON tanpa verifikasi
```

| Fungsi | Argumen | Hasil |
|---|---|---|
| `jwt.sign(claims, secret[, alg])` | 2–3 | string token; `claims` berupa string JSON objek; `alg` salah satu `HS256` (bawaan), `HS384`, `HS512` |
| `jwt.verify(token, secret)` | 2 | string klaim JSON — signature dicocokkan constant-time, lalu klaim `exp`/`nbf` (bila ada) wajib angka dan terpenuhi |
| `jwt.decode(token)` | 1 | string klaim JSON tanpa verifikasi — untuk membaca token yang sudah diverifikasi terpisah |

- Galat — kode `jwt_error`: **400** struktur rusak (bukan tiga segmen
  `header.payload.signature`, segmen bukan base64url, header/payload
  bukan JSON objek, klaim `exp`/`nbf` bukan angka), **401** signature
  tidak cocok, `alg` di luar allowlist (mis. `none`, `RS256` — tahan
  alg-confusion), token kedaluwarsa (`exp`) atau belum berlaku (`nbf`).
  Argumen salah tipe → `type_error`.
- Urutan pemeriksaan `verify`: format → header → allowlist `alg` →
  payload sebagai JSON → signature (constant-time) → `exp`/`nbf`.
  Payload dinilai sebelum signature agar pesan "payload bukan JSON"
  terbedakan dari "signature tidak valid" — klaim tidak pernah
  dikembalikan sebelum seluruh pemeriksaan lolos.
- **Tanpa OpenSSL**: RSA/ECDSA sengaja tidak didukung — menambah
  OpenSSL mematahkan matriks build (cross mingw + CI macOS). Token
  dibatasi 2 MiB; `secret` kosong ditolak.
- **Butuh CGO** (tabel platform di [GNE](gne.md#platform)).

---

## httpclient — ekstensi resmi

Klien **HTTP/1.1** berbasis [GNE](gne.md) — tanpa TLS, sama seperti
`smtp`. Nama modul `http` sudah dipakai server web bawaan, karena itu
ekstensi ini bernama `httpclient`. Pasang dulu:

```bash
gar gne install httpclient
```

```galang
use "httpclient"

$r = httpclient.get("http://127.0.0.1:8868/status")
print($r.status)                // 200
print($r.ok)                    // true — status 2xx
print($r.body)                  // isi respons
print($r.header("content-type")) // case-insensitive; absen → null

$p = httpclient.post("http://api.contoh.id/masuk", "a=1&b=2",
                     "application/x-www-form-urlencoded")

$q = httpclient.request("PUT", "http://api.contoh.id/item/1", "{\"nama\":\"x\"}",
                        ["X-Api-Key: rahasia"])
```

| Fungsi | Argumen | Hasil |
|---|---|---|
| `httpclient.get(url[, timeout_ms])` | 1–2 | objek respons |
| `httpclient.post(url, body[, content_type[, timeout_ms]])` | 2–4 | objek respons; `content_type` bawaan `application/octet-stream` |
| `httpclient.request(method, url[, body[, headers[, timeout_ms]]])` | 2–5 | objek respons; `headers` berupa array `"Nama: nilai"` |
| `$r.header(nama)` | 1 | string nilai pertama (case-insensitive) atau `null` |

Objek respons: `$r.status` (number), `$r.ok` (bool — status 2xx),
`$r.body` (string), `$r.url` (URL final sesudah pengalihan),
`$r.redirects` (number), `$r.headers` (array `"Nama: nilai"`).

- Timeout bawaan 5000 ms (`0` = tanpa batas) menutupi seluruh siklus:
  resolusi nama, koneksi, kirim, dan baca.
- Pengalihan 301/302/303/307/308 diikuti otomatis (maksimal 5); pada
  301/302/303 metode selain GET/HEAD diubah jadi GET tanpa body
  (perilaku peramban), sedangkan 307/308 mempertahankan metode dan
  body. `Connection: close` — satu permintaan = satu koneksi.
- Respons 4xx/5xx **tidak** dilempar sebagai galat — periksa
  `$r.status` atau `$r.ok`. Galat — kode `httpclient_error`: **500**
  respons tidak sah / batas terlampaui (pengalihan, ukuran, header),
  **502** koneksi ditolak/putus, **504** waktu tunggu habis. Argumen
  tidak sah → `type_error`: url bukan `http://`, header tambahan
  berisi CR/LF (injeksi), penimpaan header terkelola (`Host`,
  `Content-Length`, `Connection`, `Transfer-Encoding`), timeout
  negatif atau terlalu besar.
- **https ditolak** — tanpa TLS bawaan (OpenSSL mematahkan matriks
  build, alasannya sama dengan smtp): gunakan proksi TLS lokal
  (caddy/stunnel) bila target hanya melayani https. Hanya `http://`
  yang diterima; url tanpa skema ditolak.
- Dikirim otomatis: `User-Agent: galang-httpclient`, `Accept: */*`,
  `Accept-Encoding: identity`, `Connection: close`.
- Batas: url 4096 karakter, host 255, port 1–65535, body respons
  maksimal 64 MiB (Content-Length, chunked, maupun EOF), baris header
  respons maksimal 64 KiB/512 baris, header permintaan 16 KiB.
- **Butuh CGO** (tabel platform di [GNE](gne.md#platform)).
