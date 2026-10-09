# Standard Library

Modules are enabled with `use "nama"` and then accessed through the
`nama.fungsi()` namespace.

```garurda
use "strings"
strings.upper("hello")    // HELLO
```

Module list: `strings`, `math`, `time`, `file`, `http`, `database`.

There are also the **official extensions** `redis` and `smtp` — the same
`use` modules, but their binaries are installed with
`gar gne install <name>` (requires `gar` 0.6.0+). Their full API is at the
bottom of this page.

---

## strings

12 functions for string manipulation.

```garurda
use "strings"

strings.upper("hello")            // HELLO
strings.lower("HELLO")            // hello
strings.trim("  hi  ")            // "hi"
strings.split("a,b,c", ",")       // [a, b, c]
strings.replace("abcabc", "a", "x") // xbcxbc
strings.starts_with("hello", "he")  // true
strings.ends_with("hello", "lo")    // true
strings.index_of("hello", "ll")     // 2   (position; -1 if not found)
strings.pad_start("7", 3, "0")      // "007"
strings.repeat("ab", 3)             // ababab
strings.char_at("hello", 1)         // "e"
strings.escape_html("<b>")          // &lt;b&gt;
```

| Function | Arguments | Result |
|---|---|---|
| `upper(s)` | 1 | uppercase |
| `lower(s)` | 1 | lowercase |
| `trim(s)` | 1 | strip edge whitespace |
| `split(s, sep)` | 2 | array of pieces |
| `replace(s, old, baru)` | 3 | replace all |
| `starts_with(s, pre)` | 2 | bool |
| `ends_with(s, suf)` | 2 | bool |
| `index_of(s, sub)` | 2 | int position, `-1` if not found |
| `pad_start(s, lebar, char)` | 3 | right-padded string |
| `repeat(s, n)` | 2 | repeat |
| `char_at(s, i)` | 2 | single character |
| `escape_html(s)` | 1 | escape HTML |

Many of the functions above are also available as direct **methods**:
`"abc".upper()`, `"abc".split(",")` — see
[API Reference — String Methods](api.md#string-methods).

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
print(math.pi)      // 3.141592653589793 — a constant, not a function
print(math.e)       // 2.718281828459045 — a constant
```

| Function | Description |
|---|---|
| `floor(x)` | round down |
| `ceil(x)` | round up |
| `round(x)` | round to nearest |
| `pow(b, e)` | exponentiation |
| `sqrt(x)` | square root (x ≥ 0) |
| `abs(x)` | absolute value |
| `min(a, b, ...)` | smallest |
| `max(a, b, ...)` | largest |
| `sum(arr)` | sum of elements |
| `pi`, `e` | constants (accessed as properties) |

Note: `math.random()` **does not exist yet**. Global numeric functions such as
`abs`, `min`, `max`, `sum` are also available without `use`.

---

## time

```garurda
use "time"

time.now_ms()                     // 1728000000000 — unix ms
time.now()                        // "2026-10-08T12:00:00Z" — ISO 8601
time.format(time.now_ms(), "2006-01-02")   // "2026-10-08"
```

| Function | Description |
|---|---|
| `now_ms()` | unix timestamp in milliseconds |
| `now()` | ISO 8601 string |
| `format(ms, layout)` | format a timestamp; the layout follows Go's reference (`2006-01-02`, `15:04:05`) |

---

## file

```garurda
use "file"

file.read("data.txt")                 // file contents as a string
file.write("output.txt", "isi")       // write/overwrite a file
```

| Function | Description |
|---|---|
| `read(path)` | read the whole file → string; missing file → runtime error |
| `write(path, isi)` | write a string to a file (create/overwrite) |

---

## database

A real connection to **SQLite**, **MySQL**, or **PostgreSQL** via
`database/sql` — the driver is chosen from the DSN prefix.

```garurda
use "database"

$db = database.connect("sqlite:///app.db")   // or "sqlite:app.db"
$rows = $db.query("SELECT * FROM users")
$one  = $db.query_first("SELECT * FROM users WHERE id = ?", [$id])
$row  = $db.query_row("SELECT count(*) AS n FROM users")
$res  = $db.exec("INSERT INTO users (name) VALUES (?)", ["Dadan"])
$db.close()
```

| Method | Result |
|---|---|
| `$db.query(sql, [$params])` | array of objects — one per row; `[]` when empty |
| `$db.query_first(sql, [$params])` | first row object, or `null` |
| `$db.query_row(sql, [$params])` | row object, or `null` |
| `$db.exec(sql, [$params])` | `{rows_affected, last_insert_id}` |
| `$db.close()` | close the connection (returns `null`) |
| `$db.driver` | driver name: `sqlite3` / `mysql` / `postgres` |

- **DSN**: `sqlite:path` or a plain path (including `sqlite::memory:`)
  → SQLite; `mysql:...` / `mysql://...` → MySQL;
  `postgres://...` / `postgresql://...` → PostgreSQL.
- **Parameters** are optional and passed as an **array**. Placeholders
  follow the driver: `?` for sqlite/mysql, `$1, $2, ...` for postgres.
- **Result types**: NULL → `null`, INTEGER → number, REAL → number,
  text/`[]byte` → string, time (drivers that support it) → RFC3339
  string. SQLite stores `true/false` as 1/0 (integers).
- **Errors**: a failed connection or bad SQL throws a `db_error`
  (status 500) catchable with `try/catch` — `e.code` is `"db_error"`.
  Malformed arguments (e.g. parameters that are not an array) are
  ordinary programming errors.
- **CGO for SQLite**: `gar-linux-amd64`, `gar-windows-amd64`, and
  `gar-darwin-*` (CI-built with CGO) — SQLite is active in all three; the
  other cross builds (linux/arm64, windows/arm64) are built without CGO —
  SQLite is inactive there, but MySQL and PostgreSQL (pure Go) still work.
  CGO also governs [native extensions (GNE)](gne.md): a CGO-less binary
  rejects extension `use` with a "CGO required" message.

---

## http

A complete HTTP server: routing, request/response, Blade templates, static
files, SSE, and WebSocket. See the full documentation in
[Web Runtime](web.md).

```garurda
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

```garurda
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
  "koneksi sudah ditutup atau tidak valid". Arguments other than
  string/number → `type_error`.
- Maximum **64 connections** per interpreter; replies are strictly capped
  (bulk up to 64 MiB, array depth of 32 levels, element-count limits) so
  a misbehaving server cannot exhaust memory.
- **Requires CGO**: a `gar` binary built without CGO rejects `use` with a
  clear message (platform table in [GNE](gne.md#platform)).

---

## smtp — official extension

An **SMTP** client (RFC 5321/5322) built on [GNE](gne.md) for sending
mail over AUTH LOGIN. Install it first:

```bash
gar gne install smtp
```

One mail = one transaction: `from()` → `to()` (repeatable) →
`send()`/`send_html()`:

```garurda
use "smtp"

$s = smtp.connect("smtp.internal", 25)   // default timeout 5000 ms
$s.auth("pengirim@contoh.id", "rahasia") // optional — AUTH LOGIN
$s.from("pengirim@contoh.id")
$s.to("satu@contoh.id")
$s.to("dua@contoh.id")                   // more recipients, repeat as needed
$s.subject("Halo dari Garurda")
$s.send("First line.\nSecond line.")
$s.close()                               // true; called again → false
```

HTML mail is sent as multipart/alternative (text + HTML versions) so
older clients can still read it:

```garurda
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
