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

## redis — official extension

A **Redis** client (RESP2) built on [GNE](gne.md). Its binary does not
ship inside `gar` — install it from the release assets first:

```bash
gar gne install redis          # latest release; pin with gar gne install redis@0.6.0
```

```galang
use "redis"

$r = redis.connect("127.0.0.1", 6379)         // default timeout 5000 ms
$r = redis.connect("127.0.0.1", 6379, 1000)   // 1 second timeout

$r.ping()                         // true
$r.set("user:1", "dadan")         // "OK"
$r.get("user:1")                  // "dadan" (missing key → null)
$r.incr("visits")                 // number, value after +1
$r.exists("user:1")               // true / false
$r.del("user:1")                  // number — keys deleted
$r.expire("user:1", 60)           // true — expires in 60 seconds

$r.hset("profil", "nama", "Ayu")  // 1 = new field, 0 = updated
$r.hget("profil", "nama")         // "Ayu" (missing field → null)

$r.lpush("antrian", "a", "b")     // number — list length after push
$r.lrange("antrian", 0, -1)       // [b, a]
$r.keys("user:*")                 // [user:1, user:2]

$r.cmd("TTL", "profil")           // reply converted automatically
$r.close()                        // true; called again → false
```

| Method | Args | Returns |
|---|---|---|
| `redis.connect(host, port[, timeout_ms])` | 2–3 | connection object; default timeout 5000 ms |
| `$r.ping()` | 0 | `true` |
| `$r.get(k)` | 1 | string, or `null` when the key is missing |
| `$r.set(k, v)` | 2 | server reply (`"OK"`); `v` is a string or number |
| `$r.del(k)` | 1 | number — keys deleted |
| `$r.exists(k)` | 1 | bool |
| `$r.incr(k)` | 1 | number — value after increment |
| `$r.expire(k, seconds)` | 2 | bool |
| `$r.hset(k, field, v)` | 3 | number — `1` new field, `0` updated |
| `$r.hget(k, field)` | 2 | string, or `null` |
| `$r.lpush(k, v, ...)` | 2+ | number — list length after push |
| `$r.lrange(k, start, stop)` | 3 | array of strings (`stop = -1` to the end) |
| `$r.keys(pattern)` | 1 | array of strings |
| `$r.cmd(command, ...)` | 1+ | reply converted by type: simple string → string, integer → number, bulk → string, array → array (recursive), `$-1`/`*-1` → `null` |
| `$r.close()` | 0 | `true` once, `false` when repeated (idempotent) |

- **Errors** (all catchable with `try/catch`): a `-ERR` reply from the
  server → `redis_error` status 500; connection refused/dropped → 502;
  timeout → 504. A stale object (used after `close()`, or whose connection
  slot was reused by a newer `connect`) throws `redis_error` 500
  "connection already closed or invalid". Arguments other than
  string/number → `type_error`.
- Maximum **64 connections** per interpreter; replies are strictly capped
  (bulk up to 64 MiB, array depth of 32 levels, element-count limits) so
  a misbehaving server cannot exhaust memory.
- **Requires CGO**: a `gar` binary built without CGO rejects `use` with a
  clear message (platform table in [GNE](gne.md#platforms)).

---

## smtp — official extension

An **SMTP** client (RFC 5321/5322) built on [GNE](gne.md) for sending
mail over AUTH LOGIN. Install it first:

```bash
gar gne install smtp
```

One mail = one transaction: `from()` → `to()` (repeatable) →
`send()`/`send_html()`:

```galang
use "smtp"

$s = smtp.connect("smtp.internal", 25)   // default timeout 5000 ms
$s.auth("pengirim@contoh.id", "rahasia") // optional — AUTH LOGIN
$s.from("pengirim@contoh.id")
$s.to("satu@contoh.id")
$s.to("dua@contoh.id")                   // more recipients, repeat as needed
$s.subject("Hello from GaLang")
$s.send("First line.\nSecond line.")
$s.close()                               // true; called again → false
```

HTML mail is sent as multipart/alternative (text + HTML versions) so
older clients can still read it:

```galang
$s.from("pengirim@contoh.id")
$s.to("penerima@contoh.id")
$s.subject("News")
$s.send_html("Important <b>news</b> (text version)", "<p>Important <b>news</b></p>")
```

| Method | Args | Returns |
|---|---|---|
| `smtp.connect(host, port[, timeout_ms])` | 2–3 | session object; default timeout 5000 ms |
| `$s.auth(user, pass)` | 2 | `true` after the server replies 235 |
| `$s.from(addr)` | 1 | `true` — opens a transaction (sends `RSET` first if one is still open) |
| `$s.to(addr)` | 1 | `true` — add a recipient; maximum 100 per transaction |
| `$s.subject(text)` | 1 | `true` — at most 700 bytes, no CR/LF |
| `$s.send(body)` | 1 | `true` — send as `text/plain` |
| `$s.send_html(text, html)` | 2 | `true` — send multipart/alternative |
| `$s.close()` | 0 | `true` once, `false` when repeated (`QUIT` best-effort — never throws) |

- **Required order**: `from()` then at least one `to()` before `send()` /
  `send_html()`. `subject()` and `auth()` can be called at any time while
  the session is alive; the last `subject()` set wins when the mail is
  sent.
- **Handled automatically**: dot-stuffing and CRLF normalization of the
  body; Content-Transfer-Encoding selection (pure ASCII → `7bit`,
  non-ASCII with the `8BITMIME` capability → `8bit`, otherwise `base64`);
  non-ASCII subjects become RFC 2047 encoded-words (`=?UTF-8?B?...?=`);
  the `Date` header (UTC, locale-free) and `Message-ID` are always
  generated; the `To:` header is folded past 78 octets.
- **Limits**: messages up to 32 MiB; addresses must be 1–320 printable
  ASCII octets with no spaces or `<>,`; one session object = one
  connection — `close()` first if you want a new session. Once `send()`
  succeeds the transaction is closed and the recipient list cleared — the
  next mail must start over with `from()` then `to()` (calling `from()`
  while a transaction is still open automatically sends `RSET` first).
- **Errors** — code `smtp_error`: **500** for rejection/protocol/validation
  (server replies such as `550` on a recipient or `535` on authentication
  are included in the message), **502** connection refused/dropped, **504**
  timeout. An I/O failure mid-session kills the session — the next method
  throws 500. Malformed arguments (a subject smuggling CR/LF, a non-numeric
  port) → `type_error`.
- **Known limitation — no TLS**: STARTTLS/SSL are not implemented in
  v0.6.0; the connection and AUTH LOGIN run in **plaintext**. Only use this
  against servers that accept AUTH without TLS (internal relays, a local
  MTA) — never send credentials over the public internet. The EHLO
  capabilities are checked first: a server without EHLO falls back to
  `HELO` automatically, and `auth()` will reject with a message mentioning
  STARTTLS.

### Real-world example: Gmail through a local TLS relay

The extension has no TLS yet, while Gmail rejects direct delivery on
port 25 from an IP without SPF/DKIM (`550 5.7.26`). The proven pattern:
run `AUTH LOGIN` against **localhost** — credentials never cross the
public network unencrypted — and let stunnel encrypt to
`smtp.gmail.com:465` with full certificate verification. The `127.0.0.1`
bind below is intentional: the relay must only be reachable from the
machine itself. Ready-to-run script:
[`examples/smtp-gmail.ga`](https://github.com/lnx645/galang/blob/master/examples/smtp-gmail.ga).

1. **Binary + extension.** Download `gar-windows-amd64.zip` (Linux: the
   variant for your machine) from the
   [latest release](https://github.com/lnx645/galang/releases/latest),
   put `gar.exe`/`gar` on PATH, then `gar gne install smtp`. Extension
   variants: darwin-{amd64,arm64}, linux-{amd64,arm64}, windows-amd64 —
   on **Windows on ARM**, use the amd64 binary (runs through Windows x64
   emulation).
2. **Google app password**: Google Account → Security → 2-Step
   Verification → App passwords (16 characters).
3. **stunnel**: Windows — download `stunnel-latest-win64-installer.exe`
   from [stunnel.org](https://www.stunnel.org/downloads.html); Linux —
   `apt install stunnel4` (package name may vary per distro). Write the
   configuration (the default file location is shown by
   `stunnel -version`):

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

   `CAfile` points to a PEM CA bundle: Debian Linux uses
   `/etc/ssl/certs/ca-certificates.crt`; if the Windows package does not
   include one, download [cacert.pem](https://curl.se/ca/cacert.pem) and
   save it under that name. On Windows use an **absolute path** —
   especially when stunnel runs as a service.
4. **Start stunnel.** Windows (admin prompt, from the configuration
   folder): `stunnel -install stunnel.conf` then `stunnel -start`
   (manage with `-reload`/`-stop`, remove with `-uninstall`). Linux:
   write `/etc/stunnel/gmail.conf` (service name = file name) then
   `systemctl enable --now stunnel@gmail`.
5. **Test.** Fill in `<APP_PASSWORD>` in the example, then
   `gar run examples/smtp-gmail.ga`. Expected: `kirim : true` and the
   mail appears in the inbox.
   - `550 5.7.26 ... unauthenticated` — the mail bypassed the relay
     (stunnel down or wrong port).
   - `535` — the app password is wrong or expired.

Real test of 2026-10-09: direct delivery to the Gmail MX (port 25) was
accepted through `RCPT`/`DATA` and then rejected by Google's policy; the
path above was accepted with `250` and reached the inbox. The same steps
apply on Linux — only the stunnel installation differs.

---

## uuid — official extension

A **UUID** (RFC 9562) generator and checker built on [GNE](gne.md): v4
random and v7 time-stamped. Install first:

```bash
gar gne install uuid
```

```galang
use "uuid"

uuid.v4()                 // "f47ac10b-58cc-4372-a567-0e02b2c3d479"
uuid.v7()                 // "018f1a2b-3c4d-7ef0-..." — time-stamped prefix
uuid.is_valid(uuid.v4())  // true
uuid.is_valid("book")     // false
```

| Function | Args | Result |
|---|---|---|
| `uuid.v4()` | 0 | canonical 8-4-4-4-12 string (lowercase), version 4, RFC variant |
| `uuid.v7()` | 0 | same shape with version 7 — the first 48 bits are the millisecond timestamp (Unix epoch), the rest random |
| `uuid.is_valid(s)` | 1 | bool — canonical format + hexadecimal + version 0–8 + RFC variant (8/9/a/b); both upper and lower case are accepted |

- Randomness comes from `/dev/urandom` (Linux/macOS) or
  `BCryptGenRandom` (Windows) — a CSPRAN, not `rand()`.
- Errors: non-string argument → `type_error`. `is_valid` never throws
  for any string (returns `false`).
- **Requires CGO** (platform table in [GNE](gne.md#platforms)).

---

## jwt — official extension

**JWT** (RFC 7519) built on [GNE](gne.md) with HMAC-SHA2 signatures:
HS256/HS384/HS512. Install first:

```bash
gar gne install jwt
```

```galang
use "jwt"

$t = jwt.sign({"sub": "budi"}, "secret")            // object — HS256 (default)
$t = jwt.sign({"sub": "budi"}, "secret", "HS512")
$c = jwt.verify($t, "secret")  // claims OBJECT — signature + exp/nbf pass
print($c.sub)                  // field access on the returned object
jwt.decode($t)                 // claims OBJECT, unverified
```

| Function | Args | Result |
|---|---|---|
| `jwt.sign(claims, secret[, alg])` | 2–3 | token string; `claims` is a GaLang **object** (serialized compactly in C — no manual `json_encode`/escaping) or a raw JSON object string; `alg` is `HS256` (default), `HS384`, or `HS512` |
| `jwt.verify(token, secret)` | 2 | claims **object** — the signature is compared in constant time, then `exp`/`nbf` claims (if present) must be numbers and hold |
| `jwt.decode(token)` | 1 | claims **object** without verification — for reading tokens verified elsewhere |

- Errors — `jwt_error` code: **400** for broken structure (not three
  `header.payload.signature` segments, a segment that is not base64url,
  header/payload that are not JSON objects, non-numeric `exp`/`nbf`),
  **401** for a mismatched signature, an `alg` outside the allowlist
  (e.g. `none`, `RS256` — alg-confusion resistant), an expired token
  (`exp`) or one not yet valid (`nbf`). Wrong argument types →
  `type_error`.
- `verify` checks in this order: format → header → `alg` allowlist →
  payload parsed into a JSON **object** → signature (constant time) →
  `exp`/`nbf`. The payload is parsed before the signature check so
  "payload is not JSON" is distinguishable from "signature invalid" —
  the claims object is only returned once every check passes.
- **No OpenSSL**: RSA/ECDSA are intentionally unsupported — adding
  OpenSSL would break the build matrix (mingw cross + macOS CI). Tokens
  are capped at 2 MiB; an empty `secret` is rejected.
- **Requires CGO** (platform table in [GNE](gne.md#platforms)).

---

## httpclient — official extension

An **HTTP/1.1** client built on [GNE](gne.md) — without TLS, like
`smtp`. The module name `http` is taken by the built-in web server,
hence the name `httpclient`. Install first:

```bash
gar gne install httpclient
```

```galang
use "httpclient"

$r = httpclient.get("http://127.0.0.1:8868/status")
print($r.status)                // 200
print($r.ok)                    // true — 2xx status
print($r.body)                  // response body
print($r.header("content-type")) // case-insensitive; missing → null

$p = httpclient.post("http://api.example.com/ingest", "a=1&b=2",
                     "application/x-www-form-urlencoded")

$q = httpclient.request("PUT", "http://api.example.com/item/1", "{\"name\":\"x\"}",
                        ["X-Api-Key: secret"])
```

| Function | Args | Result |
|---|---|---|
| `httpclient.get(url[, timeout_ms])` | 1–2 | response object |
| `httpclient.post(url, body[, content_type[, timeout_ms]])` | 2–4 | response object; default `content_type` is `application/octet-stream` |
| `httpclient.request(method, url[, body[, headers[, timeout_ms]]])` | 2–5 | response object; `headers` is an array of `"Name: value"` strings |
| `$r.header(name)` | 1 | first-value string (case-insensitive) or `null` |

Response object: `$r.status` (number), `$r.ok` (bool — 2xx status),
`$r.body` (string), `$r.url` (final URL after redirects),
`$r.redirects` (number), `$r.headers` (array of `"Name: value"`).

- The default timeout is 5000 ms (`0` = unlimited) and covers the whole
  cycle: name resolution, connect, send, and read.
- Redirects 301/302/303/307/308 are followed automatically (at most 5);
  on 301/302/303 a method other than GET/HEAD becomes GET without a body
  (browser behavior), while 307/308 keep the method and body.
  `Connection: close` — one request = one connection.
- 4xx/5xx responses are **not** thrown — check `$r.status` or `$r.ok`.
  Errors — `httpclient_error` code: **500** for an invalid response or
  an exceeded limit (redirects, size, headers), **502** for a
  refused/broken connection, **504** for a timeout. Invalid arguments →
  `type_error`: a url that is not `http://`, extra headers containing
  CR/LF (injection), overwriting a transport-managed header (`Host`,
  `Content-Length`, `Connection`, `Transfer-Encoding`), a negative or
  too-large timeout.
- **https is rejected** — no built-in TLS (OpenSSL would break the build
  matrix, same reasoning as smtp): use a local TLS proxy (caddy/stunnel)
  when the target only serves https. Only `http://` is accepted; a url
  without a scheme is rejected.
- Sent automatically: `User-Agent: galang-httpclient`, `Accept: */*`,
  `Accept-Encoding: identity`, `Connection: close`.
- Limits: url 4096 characters, host 255, port 1–65535, response body up
  to 64 MiB (Content-Length, chunked, or EOF), response header lines up
  to 64 KiB/512 lines, request headers 16 KiB.
- **Requires CGO** (platform table in [GNE](gne.md#platforms)).
