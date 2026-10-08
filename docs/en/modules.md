# Standard Library

Modules are enabled with `use "nama"` and then accessed through the
`nama.fungsi()` namespace.

```garurda
use "strings"
strings.upper("hello")    // HELLO
```

Module list: `strings`, `math`, `time`, `file`, `http`, `database`.

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
- **CGO for SQLite**: native binaries (e.g. `gar-linux-amd64`) include
  SQLite support; cross-compiled binaries (windows/darwin from Linux)
  are built without CGO — SQLite is inactive there, but MySQL and
  PostgreSQL (pure Go) still work.

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
