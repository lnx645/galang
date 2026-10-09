# API Reference

This page covers **all built-in functions (builtins)**, **string and array
methods**, and **error values**. All examples have been verified on the v0.2
runtime.

Builtins can be called directly without `use`. The standard modules
(`strings`, `math`, `time`, `file`, `http`, `database`) are covered in the
[Standard Library](modules.md).

## Table of Contents

- [Output](#output)
- [Type inspection](#type-inspection)
- [Conversion](#conversion)
- [Numbers](#numbers)
- [Array — read](#array--read)
- [Array — mutate (returns a copy)](#array--mutate-returns-a-copy)
- [Transformation](#transformation)
- [Object](#object)
- [String](#string)
- [Serialization](#serialization)
- [Time](#time)
- [Async](#async)
- [Error](#error)
- [String methods](#string-methods)
- [Array methods](#array-methods)

---

## Output

### `print(...values)`

Prints a single line without a trailing newline. Accepts any arguments,
separated by spaces. Also available as a statement: `print a, b`.

```galang
print("a", 1, true)      // a 1 true
```

### `println(...values)`

Same as `print` but ends with a newline. `println()` with no arguments prints
a blank line.

```galang
println("selesai")
```

---

## Type inspection

### `type($v)` → `string`

```galang
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

### Type predicates — `is_*($v)` → `bool`

| Function | true when |
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

```galang
print(is_int(72))         // true
print(is_promise(f()))    // true — f is async
```

---

## Conversion

Conversion is always **explicit** — there is no automatic coercion between
types.

| Function | Result | Notes |
|---|---|---|
| `int($v)` | `int` | `int("42")` → 42; from a float the fraction is dropped |
| `float($v)` | `float` | `float("1.5")` → 1.5 |
| `str($v)` | `string` | `str(7)` → `"7"` |
| `bool($v)` | `bool` | `bool(0)` → `false` |
| `to_object($pairs)` | `object` | `[key, value]` pairs |

```galang
print(int("42"), float("1.5"), str(7), bool(0))   // 42 1.5 7 false
print(to_object([["a", 1], ["b", 2]]))            // {a: 1, b: 2}
```

---

## Numbers

| Function | Description |
|---|---|
| `abs($n)` | absolute value |
| `min($a, $b, ...)` | smallest value |
| `max($a, $b, ...)` | largest value |
| `sum($arr)` | sum of all elements |
| `parse_int($s, $base)` | string → int with a base; `parse_int("42", 10)` → 42 |

```galang
print(abs(-5))              // 5
print(min(3, 7), max(3, 7)) // 3 7
print(sum([1, 2, 3]))       // 6
print(parse_int("ff", 16))  // 255
```

---

## Array — read

| Function | Description |
|---|---|
| `len($x)` | length of an array/string |
| `first($arr)` | first element |
| `last($arr)` | last element |
| `at($arr, $i, $default)` | element at `$i`; out of bounds → `$default` |
| `has($arr, $v)` | whether `$v` is in the array |
| `contains($s, $sub)` | whether the substring exists (string) |
| `slice($arr, $from, $to)` | slice, `$to` exclusive — **a new copy** |

```galang
print(len("garuda"))           // 6
print(first([5, 6]), last([5, 6]))   // 5 6
print(at([1, 2], 9, "def"))    // def
print(has([1, 2], 2))          // true
print(contains("abcd", "bc"))  // true
print(slice([1, 2, 3], 0, 2))  // [1, 2]
```

---

## Array — mutate (returns a copy)

All of the functions below **do not modify the original array** — they return
a new array.

| Function | Description |
|---|---|
| `append($arr, $v)` | new array plus `$v` at the end |
| `push($arr, $v)` | same as `append` |
| `pop($arr)` | returns the **last element**; the original array is unchanged |
| `sort($arr)` | new array sorted ascending |
| `reverse($arr)` | new reversed array |
| `unique($arr)` | new array without duplicates |
| `merge($a, $b)` | merge of two arrays/objects |
| `unset($obj, $kunci)` | object: **in-place mutation**, delete a key |

```galang
$a = [3, 1, 2]
print(pop($a))         // 3        — $a stays [3, 1, 2]
print(push($a, 9))     // [3, 1, 2, 9]  — $a unchanged
print(sort($a))        // [1, 2, 3]     — $a unchanged
print(reverse($a))     // [2, 1, 3]     — $a unchanged
print(merge([1], [2])) // [1, 2]
```

---

## Transformation

| Function | Description |
|---|---|
| `map($arr, fn)` | new array after mapping |
| `filter($arr, fn)` | new array of the items that pass the filter |
| `reduce($arr, fn, $init)` | accumulate into a single value, starting from `$init` |
| `find($arr, fn)` | first matching element, or `null` |
| `join($arr, $sep)` | join into a string |

The `fn` callback receives (`$elemen`) or, for `reduce`, (`$acc, $elemen`).

```galang
print(map([1, 2, 3], fn($x) { return $x * 2 }))        // [2, 4, 6]
print(filter([1, 2, 3], fn($x) { return $x > 1 }))     // [2, 3]
print(reduce([1, 2, 3], fn($acc, $x) { return $acc + $x }, 10))  // 16
print(find([1, 2, 3], fn($x) { return $x > 2 }))       // 3
print(join(["a", "b"], "-"))                           // a-b
```

---

## Object

| Function | Description |
|---|---|
| `keys($obj)` | list of keys |
| `values($obj)` | list of values |
| `has($obj, $k)` | check whether a key exists |
| `merge($a, $b)` | merge of two objects (right-hand keys win) |
| `unset($obj, $k)` | delete a key (in-place mutation) |

```galang
$o = {x: 1, y: 2}
print(keys($o))          // [x, y]
print(values($o))        // [1, 2]
print(merge({a: 1}, {b: 2}))   // {a: 1, b: 2}
unset($o, "y")
print($o)                // {x: 1}
```

---

## String

| Function | Description |
|---|---|
| `len($s)` | length (rune-aware) |
| `str_repeat($s, $n)` | repeat the string `$n` times |
| `html_escape($s)` | escape HTML (`<` → `&lt;`, etc.) |
| `contains($s, $sub)` | check for a substring |

Methods are also available directly: `"abc".upper()`, `"a,b".split(",")` —
the full list is under [String methods](#string-methods).

```galang
print(str_repeat("ab", 3))   // ababab
print(html_escape("<b>"))    // &lt;b&gt;
```

---

## Serialization

| Function | Description |
|---|---|
| `json_encode($v)` | value → JSON string |
| `json_decode($s)` | JSON string → value (`{...}` → object, `[...]` → array) |
| `html_escape($s)` | escape HTML |

```galang
print(json_encode({k: [1, 2]}))      // {"k":[1,2]}
print(json_decode("{\"z\":9}"))      // {z: 9}
```

---

## Time

| Function | Description |
|---|---|
| `now_ms()` | unix timestamp in milliseconds |

Formatting & ISO time are available in the `time` module (`time.now()`,
`time.format()`).

```galang
print(now_ms())   // 1728000000000
```

---

## Async

| Function | Description |
|---|---|
| `gather($p1, $p2, ...)` | run many promises → array of results |
| `spawn($fn, $args...)` | fire-and-forget background task → promise |

```galang
async fn a() { return 1 }
async fn b() { return 2 }
print(gather(a(), b()))      // [1, 2]

spawn(fn() { print("nanti") })
```

Details: [Language Reference — Async](language.md#12-async--async-fn-await-gather-spawn).

---

## Error

| Function | Status | `e.code` |
|---|---|---|
| `error($pesan)` | 500 | `error` |
| `bad_request($pesan)` | 400 | `http_error` |
| `unauthorized($pesan)` | 401 | `http_error` |
| `forbidden($pesan)` | 403 | `http_error` |
| `not_found($pesan)` | 404 | `http_error` |
| `conflict($pesan)` | 409 | `http_error` |
| `server_error($pesan)` | 500 | `http_error` |

The constructors create error values; throw them with `throw`:

```galang
try {
    throw not_found("user tidak ada")
} catch e {
    print(e.message)            // user tidak ada
    print(e.status)             // 404
    print(e.code)               // http_error
    print(e.is("http_error"))   // true
}
```

Error value fields: `message`, `code`, `status`, `stack`.
Details: [Language Reference — Error Handling](language.md#11-error-handling).

---

## String Methods

Called directly on string values. Methods without arguments are also available
as properties (e.g. `$s.len`).

| Method | Description | Example |
|---|---|---|
| `.len()` | length | `"abc".len()` → 3 |
| `.upper()` | uppercase | `"abc".upper()` → `ABC` |
| `.lower()` | lowercase | `"ABC".lower()` → `abc` |
| `.trim()` | strip edge whitespace | `"  hi  ".trim()` → `hi` |
| `.split($sep)` | split → array | `"a,b".split(",")` → `[a, b]` |
| `.contains($sub)` | check for a substring | `"abc".contains("b")` → true |
| `.starts_with($p)` | prefix? | `"abc".starts_with("a")` → true |
| `.ends_with($s)` | suffix? | `"abc".ends_with("c")` → true |
| `.replace($old, $new)` | replace all | `"aaa".replace("a", "x")` → `xxx` |
| `.slice($from, $to)` | slice | `"Halo"[0:4]` also works |
| `.repeat($n)` | repeat | `"ab".repeat(3)` → `ababab` |
| `.escape_html()` | escape HTML | `"<b>".escape_html()` → `&lt;b&gt;` |
| `.reversed()` | reverse | `"abc".reversed()` → `cba` |

Properties (no parentheses): `.len`, `.upper`, `.lower`, `.trim`,
`.reversed`.

```galang
$s = "Halo Dunia"
print($s.len)                        // 10
print($s.upper())                    // HALO DUNIA
print($s.split(" "))                 // [Halo, Dunia]
print($s.contains("Dunia"))          // true
print($s.replace("Halo", "Hi"))      // Hi Dunia
print($s.reversed())                 // ainuD olaH
```

Note: methods **do not mutate** the original string (a string's contents cannot
change).

---

## Array Methods

| Method | Description | Example |
|---|---|---|
| `.len()` | length | `[1,2].len()` → 2 |
| `.first()` | first element | `[5,6].first()` → 5 |
| `.last()` | last element | `[5,6].last()` → 6 |
| `.push($v)` | copy plus a new element | `[1].push(2)` → `[1, 2]` |
| `.pop()` | last element (no mutation) | `[1,2].pop()` → 2 |
| `.join($sep)` | join → string | `[a,b].join("-")` → `a-b` |
| `.includes($v)` | membership check | `[1,2].includes(2)` → true |
| `.slice($from, $to)` | slice → copy | `[1,2,3].slice(0,2)` → `[1,2]` |
| `.reverse()` | reversed copy | `[1,2].reverse()` → `[2,1]` |
| `.map(fn)` | mapping | `[1,2].map(fn($x){ return $x*2 })` |
| `.filter(fn)` | filter | `[1,2,3].filter(fn($x){ return $x>1 })` |
| `.indices()` | list of indices | `[7,8].indices()` → `[0, 1]` |

Properties (no parentheses): `.len`, `.first`, `.last`, `.joined`
(joined with `", "`).

```galang
$a = [1, 2, 3]
print($a.len)        // 3
print($a.first)      // 1
print($a.last)       // 3
print($a.joined)     // 1, 2, 3
print($a.indices)    // [0, 1, 2]
print($a.map(fn($x) { return $x * 2 }))    // [2, 4, 6]
```

Array methods **return a copy** — the original array is unchanged.

---

## Error Methods

| Method | Description |
|---|---|
| `.is($code)` | compare `e.code` with `$code` → `bool` |

```galang
try { throw not_found("x") }
catch e { print(e.is("http_error")) }   // true
```
