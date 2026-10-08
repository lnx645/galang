# Referensi API

Halaman ini mencakup **semua fungsi bawaan (builtins)**, **method string dan
array**, serta **nilai error**. Semua contoh sudah diverifikasi pada runtime
v0.2.

Builtins bisa dipanggil langsung tanpa `use`. Modul standar
(`strings`, `math`, `time`, `file`, `http`, `database`) dibahas di
[Modul Standar](modules.md).

## Daftar Isi

- [Output](#output)
- [Inspeksi tipe](#inspeksi-tipe)
- [Konversi](#konversi)
- [Angka](#angka)
- [Array — baca](#array--baca)
- [Array — ubah (mengembalikan salinan)](#array--ubah-mengembalikan-salinan)
- [Transformasi](#transformasi)
- [Object](#object)
- [String](#string)
- [Serialisasi](#serialisasi)
- [Waktu](#waktu)
- [Async](#async)
- [Error](#error)
- [Method string](#method-string)
- [Method array](#method-array)

---

## Output

### `print(...values)`

Mencetak satu baris tanpa newline akhir. Menerima argumen apa pun, dipisah
spasi. Juga tersedia sebagai pernyataan: `print a, b`.

```garurda
print("a", 1, true)      // a 1 true
```

### `println(...values)`

Sama seperti `print` tetapi diakhiri newline. `println()` tanpa argumen
mencetak baris kosong.

```garurda
println("selesai")
```

---

## Inspeksi tipe

### `type($v)` → `string`

```garurda
type(72)         // "int"
type(3.14)       // "float"
type("x")        // "string"
type(true)       // "bool"
type([1])        // "array"
type({})         // "object"
type(null)       // "null"
type(fn(){})     // "function"
type(error("e")) // "error"
type(async fn(){ return 1 }())  // "promise"
```

### Predikat tipe — `is_*($v)` → `bool`

| Fungsi | true bila |
|---|---|
| `is_int($v)` | `int` |
| `is_float($v)` | `float` |
| `is_string($v)` | `string` |
| `is_bool($v)` | `bool` |
| `is_array($v)` | `array` |
| `is_object($v)` | `object` |
| `is_null($v)` | `null` |
| `is_error($v)` | `error` |
| `is_function($v)` | `function` |
| `is_promise($v)` | `promise` |

```garurda
print(is_int(72))         // true
print(is_promise(f()))    // true — f async
```

---

## Konversi

Konversi selalu **eksplisit** — tidak ada coerciion otomatis antar tipe.

| Fungsi | Hasil | Catatan |
|---|---|---|
| `int($v)` | `int` | `int("42")` → 42; dari float dibuang pecahannya |
| `float($v)` | `float` | `float("1.5")` → 1.5 |
| `str($v)` | `string` | `str(7)` → `"7"` |
| `bool($v)` | `bool` | `bool(0)` → `false` |
| `to_object($pairs)` | `object` | pasangan `[kunci, nilai]` |

```garurda
print(int("42"), float("1.5"), str(7), bool(0))   // 42 1.5 7 false
print(to_object([["a", 1], ["b", 2]]))            // {a: 1, b: 2}
```

---

## Angka

| Fungsi | Keterangan |
|---|---|
| `abs($n)` | nilai mutlak |
| `min($a, $b, ...)` | nilai terkecil |
| `max($a, $b, ...)` | nilai terbesar |
| `sum($arr)` | jumlah seluruh elemen |
| `parse_int($s, $base)` | string → int dengan basis; `parse_int("42", 10)` → 42 |

```garurda
print(abs(-5))              // 5
print(min(3, 7), max(3, 7)) // 3 7
print(sum([1, 2, 3]))       // 6
print(parse_int("ff", 16))  // 255
```

---

## Array — baca

| Fungsi | Keterangan |
|---|---|
| `len($x)` | panjang array/string |
| `first($arr)` | elemen pertama |
| `last($arr)` | elemen terakhir |
| `at($arr, $i, $default)` | elemen pada `$i`; di luar batas → `$default` |
| `has($arr, $v)` | apakah `$v` ada di array |
| `contains($s, $sub)` | apakah substring ada (string) |
| `slice($arr, $from, $to)` | potongan, `$to` eksklusif — **salinan baru** |

```garurda
print(len("garuda"))           // 6
print(first([5, 6]), last([5, 6]))   // 5 6
print(at([1, 2], 9, "def"))    // def
print(has([1, 2], 2))          // true
print(contains("abcd", "bc"))  // true
print(slice([1, 2, 3], 0, 2))  // [1, 2]
```

---

## Array — ubah (mengembalikan salinan)

Semua fungsi berikut **tidak mengubah array asli** — mereka mengembalikan
array baru.

| Fungsi | Keterangan |
|---|---|
| `append($arr, $v)` | array baru + `$v` di akhir |
| `push($arr, $v)` | sama dengan `append` |
| `pop($arr)` | mengembalikan **elemen terakhir**; array asli tidak berubah |
| `sort($arr)` | array baru terurut naik |
| `reverse($arr)` | array baru terbalik |
| `unique($arr)` | array baru tanpa duplikat |
| `merge($a, $b)` | gabungan dua array/object |
| `unset($obj, $kunci)` | object: **mutasi in-place**, hapus kunci |

```garurda
$a = [3, 1, 2]
print(pop($a))         // 3        — $a tetap [3, 1, 2]
print(push($a, 9))     // [3, 1, 2, 9]  — $a tidak berubah
print(sort($a))        // [1, 2, 3]     — $a tidak berubah
print(reverse($a))     // [2, 1, 3]     — $a tidak berubah
print(merge([1], [2])) // [1, 2]
```

---

## Transformasi

| Fungsi | Keterangan |
|---|---|
| `map($arr, fn)` | array baru hasil pemetaan |
| `filter($arr, fn)` | array baru yang lolos filter |
| `reduce($arr, fn, $init)` | akumulasi satu nilai, diawali `$init` |
| `find($arr, fn)` | elemen pertama yang cocok, atau `null` |
| `join($arr, $sep)` | gabung jadi string |

`fn` callback menerima (`$elemen`) atau, untuk `reduce`, (`$acc, $elemen`).

```garurda
print(map([1, 2, 3], fn($x) { return $x * 2 }))        // [2, 4, 6]
print(filter([1, 2, 3], fn($x) { return $x > 1 }))     // [2, 3]
print(reduce([1, 2, 3], fn($acc, $x) { return $acc + $x }, 10))  // 16
print(find([1, 2, 3], fn($x) { return $x > 2 }))       // 3
print(join(["a", "b"], "-"))                           // a-b
```

---

## Object

| Fungsi | Keterangan |
|---|---|
| `keys($obj)` | daftar kunci |
| `values($obj)` | daftar nilai |
| `has($obj, $k)` | cek keberadaan kunci |
| `merge($a, $b)` | gabungan dua object (kunci kanan menang) |
| `unset($obj, $k)` | hapus kunci (mutasi in-place) |

```garurda
$o = {x: 1, y: 2}
print(keys($o))          // [x, y]
print(values($o))        // [1, 2]
print(merge({a: 1}, {b: 2}))   // {a: 1, b: 2}
unset($o, "y")
print($o)                // {x: 1}
```

---

## String

| Fungsi | Keterangan |
|---|---|
| `len($s)` | panjang (rune-aware) |
| `str_repeat($s, $n)` | ulangi string `$n` kali |
| `html_escape($s)` | escape HTML (`<` → `&lt;` dll.) |
| `contains($s, $sub)` | cek substring |

Method langsung tersedia juga: `"abc".upper()`, `"a,b".split(",")` — lengkap
di [Method string](#method-string).

```garurda
print(str_repeat("ab", 3))   // ababab
print(html_escape("<b>"))    // &lt;b&gt;
```

---

## Serialisasi

| Fungsi | Keterangan |
|---|---|
| `json_encode($v)` | nilai → string JSON |
| `json_decode($s)` | string JSON → nilai (`{...}` → object, `[...]` → array) |
| `html_escape($s)` | escape HTML |

```garurda
print(json_encode({k: [1, 2]}))      // {"k":[1,2]}
print(json_decode("{\"z\":9}"))      // {z: 9}
```

---

## Waktu

| Fungsi | Keterangan |
|---|---|
| `now_ms()` | timestamp unix dalam milidetik |

Format & waktu ISO tersedia di modul `time` (`time.now()`, `time.format()`).

```garurda
print(now_ms())   // 1728000000000
```

---

## Async

| Fungsi | Keterangan |
|---|---|
| `gather($p1, $p2, ...)` | jalankan banyak promise → array hasil |
| `spawn($fn, $args...)` | tugas latar belakang fire-and-forget → promise |

```garurda
async fn a() { return 1 }
async fn b() { return 2 }
print(gather(a(), b()))      // [1, 2]

spawn(fn() { print("nanti") })
```

Detail: [Sintaks Bahasa — Async](language.md#12-async--async-fn-await-gather-spawn).

---

## Error

| Fungsi | Status | `e.code` |
|---|---|---|
| `error($pesan)` | 500 | `error` |
| `bad_request($pesan)` | 400 | `http_error` |
| `unauthorized($pesan)` | 401 | `http_error` |
| `forbidden($pesan)` | 403 | `http_error` |
| `not_found($pesan)` | 404 | `http_error` |
| `conflict($pesan)` | 409 | `http_error` |
| `server_error($pesan)` | 500 | `http_error` |

Konstruktor membuat nilai error; lemparkan dengan `throw`:

```garurda
try {
    throw not_found("user tidak ada")
} catch e {
    print(e.message)            // user tidak ada
    print(e.status)             // 404
    print(e.code)               // http_error
    print(e.is("http_error"))   // true
}
```

Field nilai error: `message`, `code`, `status`, `stack`.
Detail: [Sintaks Bahasa — Error Handling](language.md#11-error-handling).

---

## Method string

Dipanggil langsung pada nilai string. Method tanpa argumen juga tersedia
sebagai properti (mis. `$s.len`).

| Method | Keterangan | Contoh |
|---|---|---|
| `.len()` | panjang | `"abc".len()` → 3 |
| `.upper()` | huruf besar | `"abc".upper()` → `ABC` |
| `.lower()` | huruf kecil | `"ABC".lower()` → `abc` |
| `.trim()` | hapus spasi tepi | `"  hi  ".trim()` → `hi` |
| `.split($sep)` | pecah → array | `"a,b".split(",")` → `[a, b]` |
| `.contains($sub)` | cek substring | `"abc".contains("b")` → true |
| `.starts_with($p)` | awalan? | `"abc".starts_with("a")` → true |
| `.ends_with($s)` | akhiran? | `"abc".ends_with("c")` → true |
| `.replace($old, $new)` | ganti semua | `"aaa".replace("a", "x")` → `xxx` |
| `.slice($from, $to)` | potongan | `"Halo"[0:4]` juga berlaku |
| `.repeat($n)` | ulangi | `"ab".repeat(3)` → `ababab` |
| `.escape_html()` | escape HTML | `"<b>".escape_html()` → `&lt;b&gt;` |
| `.reversed()` | balik | `"abc".reversed()` → `cba` |

Properti (tanpa tanda kurung): `.len`, `.upper`, `.lower`, `.trim`,
`.reversed`.

```garurda
$s = "Halo Dunia"
print($s.len)                        // 10
print($s.upper())                    // HALO DUNIA
print($s.split(" "))                 // [Halo, Dunia]
print($s.contains("Dunia"))          // true
print($s.replace("Halo", "Hi"))      // Hi Dunia
print($s.reversed())                 // ainuD olaH
```

Catatan: method **tidak memutasi** string asli (string tidak bisa berubah isi).

---

## Method array

| Method | Keterangan | Contoh |
|---|---|---|
| `.len()` | panjang | `[1,2].len()` → 2 |
| `.first()` | elemen pertama | `[5,6].first()` → 5 |
| `.last()` | elemen terakhir | `[5,6].last()` → 6 |
| `.push($v)` | salinan + elemen baru | `[1].push(2)` → `[1, 2]` |
| `.pop()` | elemen terakhir (tanpa mutasi) | `[1,2].pop()` → 2 |
| `.join($sep)` | gabung → string | `[a,b].join("-")` → `a-b` |
| `.includes($v)` | cek keanggotaan | `[1,2].includes(2)` → true |
| `.slice($from, $to)` | potongan → salinan | `[1,2,3].slice(0,2)` → `[1,2]` |
| `.reverse()` | salinan terbalik | `[1,2].reverse()` → `[2,1]` |
| `.map(fn)` | pemetaan | `[1,2].map(fn($x){ return $x*2 })` |
| `.filter(fn)` | filter | `[1,2,3].filter(fn($x){ return $x>1 })` |
| `.indices()` | daftar indeks | `[7,8].indices()` → `[0, 1]` |

Properti (tanpa tanda kurung): `.len`, `.first`, `.last`, `.joined`
(gabungan dengan `", "`).

```garurda
$a = [1, 2, 3]
print($a.len)        // 3
print($a.first)      // 1
print($a.last)       // 3
print($a.joined)     // 1, 2, 3
print($a.indices)    // [0, 1, 2]
print($a.map(fn($x) { return $x * 2 }))    // [2, 4, 6]
```

Method array **mengembalikan salinan** — array asli tidak berubah.

---

## Method error

| Method | Keterangan |
|---|---|
| `.is($code)` | bandingkan `e.code` dengan `$code` → `bool` |

```garurda
try { throw not_found("x") }
catch e { print(e.is("http_error")) }   // true
```
