# Modul Standar

## strings

```garurda
use "strings"

strings.upper("hello")     // "HELLO"
strings.lower("HELLO")     // "hello"
strings.title("hello")     // "Hello"
strings.trim("  hi  ")     // "hi"
strings.contains("abc", "b") // true
strings.replace("abcabc", "a", "x") // "xbcxbc"
strings.split("a,b,c", ",") // ["a", "b", "c"]
```

## math

```garurda
use "math"

math.max(3, 7)         // 7
math.min(3, 7)         // 3
math.floor(3.7)        // 3
math.ceil(3.2)         // 4
math.sqrt(16)          // 4
math.pow(2, 10)        // 1024
math.random()           // 0..1
```

## time

```garurda
use "time"

time.now()             // ISO 8601 string
time.now_ms()          // unix timestamp ms
time.format(ms, "2006-01-02") // format
```

## file

```garurda
use "file"

file.read("data.txt")
file.write("output.txt", "isi")
```

## database

```garurda
use "database"

$db = database.connect("sqlite:///app.db")
$result = $db.query("SELECT * FROM users")
```

*Catatan: saat ini fungsi database masih stub/placeholder.*

## http

Lihat dokumentasi lengkap di [Web Runtime](web.md).
