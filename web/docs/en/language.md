# Garurda Language Reference

Complete reference. All examples on this page have been run and verified on
the v0.2 runtime.

## 1. Lexical

### Comments

```garurda
// single-line comment
/* block comment
   can span multiple lines */
```

### End of statement

A newline ends a statement; `;` is optional. A line is continued automatically
when the last character is a binary operator or `(`, `[`, `{`.

```garurda
$total = 1 +
         2          // continues on the next line — OK
```

### The `$` sigil

The `$` sigil is **optional**. `nama` and `$nama` refer to the same variable:

```garurda
$nama = "Garurda"
print(nama)   // Garurda
```

`$` must be used when a variable name matches a keyword, e.g. `$string`.

### Identifiers

Variable/function names: letters, digits, `_`; they may not start with a digit.
Object property names follow the same rules.

## 2. Literals

### Numbers

```garurda
$desimal   = 42          // int
$heksa     = 0xFF        // hexadecimal int (255)
$underscore = 1_000_000   // thousands separator (1000000)
$pecahan   = 3.14        // float
$scientific = 1.0e3      // float, 1000.0
```

### Strings

```garurda
$nama = "Garurda"
print("Halo, $nama!")        // identifier interpolation
print("Halo, ${nama}!")      // expression interpolation: ${...}
print("harga 5$")            // $ without an identifier = safe literal

$raw = `baris literal
multi baris, tanpa escape dan tanpa interpolasi`
```

- The `"..."` string supports escapes (`\n`, `\t`, `\"`, `\\`) and interpolation.
- Backtick `` `...` `` strings are raw: no escapes, no interpolation.
- String indexing is **rune-aware** (safe for UTF-8): `"é"[0]` → `é`.

### Booleans and null

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

## 3. Data Types

| Type | Example | `type()` returns |
|---|---|---|
| `int` | `72`, `0xFF`, `1_000` | `"int"` |
| `float` | `3.14`, `1.0e3` | `"float"` |
| `string` | `"teks"`, `` `raw` `` | `"string"` |
| `bool` | `true` / `false` | `"bool"` |
| `array` | `[1, 2]` | `"array"` |
| `object` | `{a: 1}` | `"object"` |
| `null` | `null` | `"null"` |
| `function` | `fn() { }` | `"function"` |
| `promise` | result of an `async fn` | `"promise"` |
| `error` | value from `throw`/`catch` | `"error"` |
| `any` | — | any type, no checking |
| `number` | — | supertype of `int`/`float`, for annotations only |

Check types with `is_int`, `is_float`, `is_string`, `is_bool`, `is_array`,
`is_object`, `is_null`, `is_error`, `is_function`, `is_promise`
(see the [API Reference](api.md)).

### No coercion

```garurda
print("1" == 1)     // false — string "1" ≠ int 1
print(1 == 1.0)     // true  — int and float compare numerically
// "n=" + 5         // ERROR: cannot add int to string (use str(int))
```

Conversion must be explicit: `int("42")`, `str(7)`, `float("1.5")`, `bool(0)`.

## 4. Variables and Typed Declarations

```garurda
$x = 10                    // automatic type inference
int $n = 72                // typed declaration: must be int
?string $s = null          // may be null
array<int> $ids = [1, 2, 3] // array of int — elements are checked
any $bebas = "apa saja"    // no checking
```

Type annotations are **checked and then erased** at runtime — zero cost on the
hot path. Assigning a mismatched value produces a clear runtime error.

## 5. Operators

### Arithmetic

```
+   -   *   /   %
```

- `/` on two `int` values yields an `int` (truncated toward zero).
- `%` follows the sign of the operands, like Go: `-7 % 3` → `-1`.

### Comparison

```
==   !=   <   <=   >   >=
```

### Logic — two syntaxes, both valid

```garurda
if $a and $b { }      if $a && $b { }
if $a or  $b { }      if $a || $b { }
if not $a { }         if !$a { }
```

### Membership with `in`

```garurda
print(1 in [1, 2])          // true   — array
print("a" in {a: 1})        // true   — object (checks the key)
print("bc" in "abcd")       // true   — substring
print(9 in [1, 2])          // false
```

### Ranges `..` (inclusive on both ends)

```garurda
for $i in 1..5 { }      // 1 2 3 4 5
for $i in 0..10..5 { }  // 0 5 10  — with a step
for $i in 3..1 { }      // 3 2 1    — descending, direction follows the sign
```

### Ternary

```garurda
$status = $n > 0 ? "ok" : "gagal"
```

### Precedence (low → high)

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

Important: `not`/`!` binds **more loosely** than comparison:
`not $a == $b` means `not ($a == $b)`.

There are no compound assignment operators (`+=`, `-=`) and no null-coalescing
(`??`) yet.

## 6. Branching

```garurda
if $n > 0 {
    print("positif")
} else if $n < 0 {
    print("negatif")
} else {
    print("nol")
}

// shorthand with a ternary
$label = $n > 0 ? "positif" : "nol/minus"
```

Curly braces `{ }` are mandatory — there is no brace-less form.

## 7. Loops

### while

```garurda
$i = 0
while $i < 3 {
    $i = $i + 1
}
print($i)   // 3
```

### for-in over arrays

```garurda
for $item in [1, 2, 3] {
    print($item)
}
```

### for-in over objects (key + value)

```garurda
for $k, $v in {a: 1, b: 2} {
    print("$k -> $v")     // a -> 1 ; b -> 2
}
```

### for over strings (per character)

```garurda
for $ch in "abc" {
    print($ch)            // a b c
}
```

### Ranges, steps, and direction

```garurda
for $i in 1..10 { }        // inclusive: 1..10
for $i in 0..10..5 { }     // step 5: 0 5 10
for $i in 3..1 { }         // descending: 3 2 1
```

### break / continue

```garurda
for $i in 1..10 {
    if $i == 5 { continue }
    if $i > 7 { break }
    print($i)              // 1 2 3 4 6 7
}
```

## 8. Arrays

```garurda
$a = [1, 2, 3, 4, 5]
print($a[0])      // 0-based index → 1
print($a[-1])     // negative index (from the end) → 5
print($a.len)     // 5
print($a.first)   // 1
print($a.last)    // 5
print($a.joined)  // "1,2,3,4,5"
```

### Slicing

```garurda
$a = [1, 2, 3, 4, 5]
print($a[1:3])    // [2, 3]        — from:to, to exclusive
print($a[2:])     // [3, 4, 5]     — from index 2
print($a[:2])     // [1, 2]        — up to index 2
print($a[:])      // [1, 2, 3, 4, 5] — full copy

$s = "Halo Dunia"
print($s[0:4])    // "Halo" — string slicing works too
```

An out-of-bounds index → **runtime error** (not a silent `null`). If you want a
fallback value, use `at($a, $i, $default)`.

### Array comprehension

```garurda
$kali2 = [$n * 2 for $n in [1, 2, 3]]        // [2, 4, 6]
$besar = [$n for $n in [1, 12, 3] if $n > 10] // [12]
```

### References vs copies (important)

- **Assignment shares references** for arrays and objects:
  `$b = $a` then `$b[0] = 99` also changes `$a`.
- **Built-in functions return a new copy**: `append`, `push`, `pop`,
  `sort`, `reverse`, `slice`, `map`, `filter` do not modify the original array.

```garurda
$a = [1, 2]
$b = $a
$b = append($b, 3)     // $a stays [1, 2]  — append returns a new array
$c = [1, 2]
$d = $c
$d[0] = 99             // $c[0] also becomes 99 — assignment = shared reference
```

## 9. Objects

```garurda
$user = {nama: "Dadan", umur: 25}
print($user.nama)       // dot access
print($user["umur"])    // bracket access
$user.nama = "Dad"      // set property (in-place mutation)
$user.email = "a@b.c"   // add a new key
unset($user, "umur")    // delete a key
print(keys($user))      // [nama, email]
print(values($user))    // [Dad, a@b.c]
print($user in {nama: 1}) // true — check whether the key exists
```

Objects are also **reference** values on assignment (just like arrays).

## 10. Functions

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

- Parameter & return type annotations are **optional**; they are checked at
  runtime and then erased (zero cost).
- Default parameters may be mixed with required ones (defaults come last).

### Arrow functions (single expression)

```garurda
fn kali($x) => $x * 2
print(kali(21))    // 42
```

### First-class functions & closures

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

A closure captures its environment; a function's scope is derived from the
scope that defined it. Recursion depth is capped at **2000** with a clear
message.

## 11. Error Handling

```garurda
try {
    $u = null
    if $u == null { throw not_found("user tidak ada") }
} catch e {
    print("Error: ${e.message} (code: ${e.code}, status: ${e.status})")
}
```

### Error constructors

```garurda
throw error("kegagalan umum")     // status 500
throw bad_request("input salah")  // 400
throw unauthorized("belum login") // 401
throw forbidden("dilarang")       // 403
throw not_found("tidak ada")      // 404
throw conflict("duplikat")        // 409
throw server_error("gagal")       // 500
```

### Error values

Every error value has these fields:

| Field | Value |
|---|---|
| `e.message` | the error message |
| `e.code` | `"error"` for `error()`, `"http_error"` for the six HTTP constructors |
| `e.status` | HTTP status code (404, 400, 401, 403, 409, 500) |
| `e.stack` | stack trace (for internal errors) |

To tell error kinds apart, use `e.status` (e.g. `e.status == 404`) or
`e.is($code)`, which compares `e.code`:

```garurda
try {
    throw not_found("x")
} catch e {
    print(e.is("http_error"))    // true
    print(e.is("not_found"))     // false — its code is "http_error"
    print(e.status)              // 404
}
```

### What can be caught?

- **Can** be caught: errors thrown by `throw` (including the error
  constructors).
- **Cannot** be caught — they halt the program: internal interpreter errors
  such as undefined variables, out-of-bounds indexes, and division by zero.

```garurda
try { $x = undefined_var } catch e { print("tangkap") }
// → the program stops with a stack trace; catch is NOT executed
```

### finally — always runs

The `finally` block **always** executes: when the try succeeds, when the catch
handles an error, and when the error is rethrown. This is the place to clean up
resources.

```garurda
try {
    $data = file.read("config.json")
} catch $e {
    print("gagal baca")
} finally {
    print("bersihkan di sini")   // always executed
}
```

`finally` without `catch` is also valid:

```garurda
try {
    $r = risky()
} finally {
    $conn.close()
}
throw $r   // the error is rethrown after finally runs
```

## 12. Async — `async fn`, `await`, `gather`, `spawn`

A function declared `async` **returns a promise** when it is called, rather
than the result itself.

```garurda
async fn ambil_user($id) {
    return {"id": $id, "nama": "Budi"}
}

$u = await ambil_user(7)     // run the task, get the result
print($u.nama)               // Budi
```

`await` rules:

- `await <promise>` — runs the task **now** and returns the result.
- `await <plain value>` — the value passes straight through (`await 42` → `42`).
- `await` on a failed promise (one containing a `throw`) rethrows the same
  error — it can be caught with `try/catch`.
- A promise can be stored first, awaited later, and **awaited repeatedly**
  (the task still runs only once):

```garurda
$p = ambil_user(9)
print(type($p))             // "promise"
$hasil = await $p           // the work happens here
print(is_promise($p))       // true
```

### `gather` — resolve many promises at once

```garurda
async fn a() { return 1 }
async fn b() { return 2 }

$r = gather(a(), b())       // [1, 2]
print($r[0] + $r[1])        // 3
```

`gather(...)` also accepts plain values (they are used as-is). An error from
any of the promises is thrown immediately.

### `spawn` — background tasks (fire-and-forget)

```garurda
fn catat($pesan) { print("log: " + $pesan) }

spawn(catat, "user login")  // scheduled, not run here
print("program lanjut")
// spawn tasks run at drain time (end of execution / end of the HTTP request)
```

`spawn` always produces a promise; if awaited, its result is returned.

*How it works:* async execution is **cooperative** — tasks are run by `await`,
`gather`, or drain. There is no I/O parallelism yet; that will follow along
with built-in async I/O.

## 13. print / println

Two forms are supported:

```garurda
print("nilai:", $x)      // call style
print "nilai:", $x       // statement style
println("selesai")       // one line
println()                // blank line
println "a", "b"         // statement
```

## 14. Modules

```garurda
use "strings"
use "math"
use "time"
use "file"
use "http"
use "database"    // SQLite, MySQL, PostgreSQL
```

Details: [Standard Library](modules.md).

### Modules from your own files

`use` also loads other `.ga` files — paths are relative to the file that
issues the `use`. The file becomes a namespace holding all of its
top-level `fn`s:

```garurda
// db.ga — same directory as the main program
$koneksi = database.connect("sqlite:app.db")   // private: not exported

fn ambil_user($id) {
    return $koneksi.query_first("SELECT * FROM users WHERE id = ?", [$id])
}
```

```garurda
// main.ga
use "db"              // looks for db.ga next to main.ga
use "lib/util.ga"     // relative path; the bound namespace: util
print(db.ambil_user(1).name)
```

Rules:

- **Built-in modules win first** — `use "http"` always loads the built-in
  module, whatever `http.ga` sits in the working directory.
- **Only `fn` is exported** — top-level variables are private: readable
  by `fn`s in the same file (like `$koneksi` above), but `db.rahasia`
  reads as `null`.
- **The file runs exactly once** — a second `use` shares the same object;
  circular chains (`a.ga` → `b.ga` → `a.ga`) are rejected with a clear
  message.
- **`use` inside a module file is fine** — the next path is relative to
  that module file's own directory.
- Compile-time and runtime errors inside a module are reported against
  the module's file name.

### Native extensions (GNE)

Besides `.ga` files, `use` can also load **native C extensions** (GNE —
Garurda Native Extension) without changing the parser or the compiler.
When no source file is found, the search order is `./gne` → `$GNE_PATH`
→ `~/.garurda/gne`:

```garurda
use "redis"         // looks for redis.so / .dylib / .dll
use "lib/foo.so"    // explicit paths are fine too
```

The resolution order remains **builtin → `.ga` file → GNE**. The full
guide to writing C extensions is at
[Native Extensions — GNE](gne.md); working examples live in the
[garurda-gne-examples](https://github.com/lnx645/garurda-gne-examples)
repo.

## 15. Not Yet Available

- Generics `<T>` — planned for v1
- Classes & methods — planned for v2
- Compound assignment (`+=`) and `??`
- Async I/O parallelism (built-in async I/O) — async is currently cooperative
- SMTP and a unit-test framework — planned
