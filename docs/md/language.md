# Sintaks Bahasa Garurda

## Komentar

```garurda
// Komentar satu baris
```

## Variabel

Sigil `$` opsional — `nama` dan `$nama` menunjuk ke variabel yang sama:

```garurda
$nama = "Garurda"
umur = 25
$pi = 3.14
$aktif = true
$kosong = null
```

## Tipe Data

| Tipe | Contoh |
|---|---|
| `int` | `72`, `0xFF`, `1_000` |
| `float` | `3.14`, `1.0e3` |
| `string` | `"teks"`, backtick raw string |
| `bool` | `true`, `false` |
| `array` | `[1, 2, 3]` |
| `object` | `{a: 1, b: 2}` |
| `null` | `null` |

## Operator

```
+  -  *  /  %   // aritmatika
== != < > <= >= // perbandingan
and or not       // logika (keyword, bukan && || !)
in                // keanggotaan array/object
```

**Catatan:** Garurda memakai keyword `and`, `or`, `not` — bukan `&&`, `||`, `!`.

## String

```garurda
$nama = "Garurda"
print("Hello, $nama!")       // interpolasi
print("Hello, ${nama}!")     // interpolasi ekspresi
```

## Array

```garurda
$angka = [1, 2, 3, 4, 5]
print($angka[0])       // 1
print($angka.len)      // 5
```

## Object

```garurda
$user = {nama: "Dadan", umur: 25}
print($user.nama)      // Dadan
```

## Percabangan

```garurda
if $n > 0 {
    print("positif")
} else if $n < 0 {
    print("negatif")
} else {
    print("nol")
}

$status = $n > 0 ? "ok" : "gagal"   // ternary
```

## Perulangan

```garurda
for $i < 10 {
    print($i)
}

for $item in $angka {
    print($item)
}

while $i < 10 {
    print($i)
    $i = $i + 1
}
```

## Fungsi

```garurda
fn tambah(int $a, int $b) int {
    return $a + $b
}
print(tambah(3, 5))    // 8

// Closure
fn penghitung() {
    $n = 0
    return fn() {
        $n = $n + 1
        return $n
    }
}
```

## Modul

```garurda
use "strings"
use "math"
use "time"
use "http"
```
