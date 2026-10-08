package interp

import (
	"bytes"
	"strings"
	"testing"

	"garurda/internal/domain"
)

// runSnippet evaluates src and returns stdout, the REPL's view of the final
// expression, and any error.
func runSnippet(t *testing.T, src string) (string, domain.Value, error) {
	t.Helper()
	var out bytes.Buffer
	in := New(&out, &out)
	v, err := in.Eval(src, "test.ga")
	return out.String(), v, err
}

// mustRun fails the test if evaluation errors, returning trimmed stdout.
func mustRun(t *testing.T, src string) string {
	t.Helper()
	out, _, err := runSnippet(t, src)
	if err != nil {
		t.Fatalf("unexpected error: %v\nsource:\n%s", err, src)
	}
	return strings.TrimRight(out, "\n")
}

// evalStr evaluates src and renders the result as a string.
func evalStr(t *testing.T, src string) string {
	t.Helper()
	out, v, err := runSnippet(t, src)
	if err != nil {
		t.Fatalf("unexpected error: %v\nsource:\n%s", err, src)
	}
	if out != "" {
		return strings.TrimRight(out, "\n")
	}
	if v == nil {
		return ""
	}
	return v.String()
}

// evalFails asserts that src produces a runtime error containing want.
func evalFails(t *testing.T, src, want string) {
	t.Helper()
	_, _, err := runSnippet(t, src)
	if err == nil {
		t.Fatalf("expected error containing %q, got success\nsource:\n%s", want, src)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got: %v", want, err)
	}
}

func TestPrintAndArithmetic(t *testing.T) {
	cases := []struct{ src, want string }{
		{`print 1 + 2`, "3"},
		{`print 10 / 4`, "2.5"},
		{`print 10 / 5`, "2"},
		{`print 7 % 3`, "1"},
		{`print 2 * 3 + 4`, "10"},
		{`print (2 + 3) * 4`, "20"},
		{`print 2.5 + 2.5`, "5"},
		{`print -5 + 3`, "-2"},
		{`print "a" + "b"`, "ab"},
		{`println "halo"`, "halo"},
		{`print 1.0`, "1"},
		{`print 0.1 + 0.2`, "0.30000000000000004"},
	}
	for _, c := range cases {
		if got := mustRun(t, c.src); got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
}

func TestVariablesAndTypes(t *testing.T) {
	if got := evalStr(t, `$nama = "Garurda"
print $nama`); got != "Garurda" {
		t.Errorf("got %q", got)
	}
	// The `$` sigil is optional: both spellings hit the same variable.
	if got := evalStr(t, `nama = "sama"
print $nama`); got != "sama" {
		t.Errorf("optional sigil: got %q", got)
	}
	// Inference.
	if got := evalStr(t, `$a = 5
print type($a)`); got != "int" {
		t.Errorf("inferred type: got %q", got)
	}
	if got := evalStr(t, `$a = 5.5
print type($a)`); got != "float" {
		t.Errorf("inferred float: got %q", got)
	}
	// Explicit annotations and zero values.
	if got := evalStr(t, `int $x
print $x`); got != "0" {
		t.Errorf("zero int: got %q", got)
	}
	if got := evalStr(t, `array $xs
print $xs`); got != "[]" {
		t.Errorf("zero array: got %q", got)
	}
	if got := evalStr(t, `object $o
print $o`); got != "{}" {
		t.Errorf("zero object: got %q", got)
	}
	if got := evalStr(t, `?string $s = null
print $s`); got != "null" {
		t.Errorf("nullable default: got %q", got)
	}
}

func TestTypeChecking(t *testing.T) {
	// Assigning a wrong type to a declared variable must fail.
	evalFails(t, `int $a = 72
$a = "teks"`, "expected int")
	evalFails(t, `int $a = 72.5`, "expected int")
	evalFails(t, `string $s = "a"
$s = 1`, "expected string")
	evalFails(t, `bool $b = true
$b = 1`, "expected bool")
	// Passing a wrong argument type must fail.
	evalFails(t, `fn f(int $x) { return $x }
f("bukan int")`, "expected int")
	// Null only fits nullable annotations.
	evalFails(t, `int $a = null`, "expected int")
	// any accepts everything.
	if got := evalStr(t, `any $x = 1
$x = "teks"
print type($x)`); got != "string" {
		t.Errorf("any should accept reassignment, got %q", got)
	}
}

func TestFunctionsAndClosures(t *testing.T) {
	if got := evalStr(t, `fn tambah(int $a, int $b) int { return $a + $b }
print tambah(2, 3)`); got != "5" {
		t.Errorf("got %q", got)
	}
	// Default parameter values.
	if got := evalStr(t, `fn greet($nama, $sapaan = "Halo") { return $sapaan + ", " + $nama }
print greet("Garurda")
print greet("Garurda", "Selamat pagi")`); got != "Halo, Garurda\nSelamat pagi, Garurda" {
		t.Errorf("defaults: got %q", got)
	}
	// Arrow functions.
	if got := evalStr(t, `fn kali($x) => $x * 2
print kali(21)`); got != "42" {
		t.Errorf("arrow fn: got %q", got)
	}
	// Anonymous function passed to map.
	if got := evalStr(t, `print map([1, 2, 3], fn($x) { return $x * 10 })`); got != "[10, 20, 30]" {
		t.Errorf("anon fn: got %q", got)
	}
	// Closures capture the environment.
	if got := evalStr(t, `fn penghitung() {
		$total = 0
		return fn($n) { $total = $total + $n; return $total }
	}
$hitung = penghitung()
$hitung(5)
print $hitung(3)`); got != "8" {
		t.Errorf("closure: got %q", got)
	}
	// Recursion.
	if got := evalStr(t, `fn fib($n) {
		if $n < 2 { return $n }
		return fib($n - 1) + fib($n - 2)
	}
print fib(15)`); got != "610" {
		t.Errorf("recursion: got %q", got)
	}
	// Arity errors.
	evalFails(t, `fn f($a, $b) { return $a }
f(1)`, "missing argument")
	evalFails(t, `fn f($a) { return $a }
f(1, 2)`, "takes 1 argument")
	// Calling a non-function is an error.
	evalFails(t, `$a = 5
$a()`, "not callable")
}

func TestControlFlow(t *testing.T) {
	if got := evalStr(t, `$x = 5
if $x > 10 {
	print "besar"
} else if $x > 3 {
	print "sedang"
} else {
	print "kecil"
}`); got != "sedang" {
		t.Errorf("if/else if: got %q", got)
	}
	// while with break and continue.
	if got := evalStr(t, `$i = 0
$s = 0
while $i < 10 {
	$i = $i + 1
	if $i % 2 == 0 { continue }
	if $i > 7 { break }
	$s = $s + $i
}
print $s`); got != "16" {
		t.Errorf("while: got %q", got)
	}
	// for over a list.
	if got := evalStr(t, `$jumlah = 0
for $x in [1, 2, 3] { $jumlah = $jumlah + $x }
print $jumlah`); got != "6" {
		t.Errorf("for list: got %q", got)
	}
	// Inclusive range and index variable.
	if got := evalStr(t, `$pairs = []
for $i in 1..3 { $pairs = append($pairs, $i * 10) }
print $pairs`); got != "[10, 20, 30]" {
		t.Errorf("range: got %q", got)
	}
	// Range with a step.
	if got := evalStr(t, `$out = []
for $i in 0..10..5 { $out = append($out, $i) }
print $out`); got != "[0, 5, 10]" {
		t.Errorf("range step: got %q", got)
	}
	// Descending range.
	if got := evalStr(t, `$out = []
for $i in 3..1 { $out = append($out, $i) }
print $out`); got != "[3, 2, 1]" {
		t.Errorf("descending range: got %q", got)
	}
	// Iterating an object with two loop variables.
	if got := evalStr(t, `$o = {a: 1, b: 2}
$keys = []
for $k, $v in $o { $keys = append($keys, "${k}=${v}") }
print $keys`); got != "[a=1, b=2]" {
		t.Errorf("object iteration: got %q", got)
	}
	// Iterating a string.
	if got := evalStr(t, `$s = ""
for $c in "abc" { $s = $s + $c + "-" }
print $s`); got != "a-b-c-" {
		t.Errorf("string iteration: got %q", got)
	}
	// Ternary.
	if got := evalStr(t, `$umur = 20
print $umur >= 18 ? "dewasa" : "anak"`); got != "dewasa" {
		t.Errorf("ternary: got %q", got)
	}
	// Logical short circuit.
	if got := evalStr(t, `print false and 1 / 0 == 1`); got != "false" {
		t.Errorf("short circuit and: got %q", got)
	}
	if got := evalStr(t, `print true or 1 / 0 == 1`); got != "true" {
		t.Errorf("short circuit or: got %q", got)
	}
	if got := evalStr(t, `print not false`); got != "true" {
		t.Errorf("not: got %q", got)
	}
}

func TestEqualityHasNoCoercion(t *testing.T) {
	if got := evalStr(t, `print 1 == 1.0`); got != "true" {
		t.Errorf("int/float compare: got %q", got)
	}
	if got := evalStr(t, `print "1" == 1`); got != "false" {
		t.Errorf("string/int must not coerce: got %q", got)
	}
	if got := evalStr(t, `print 1 != 2`); got != "true" {
		t.Errorf("neq: got %q", got)
	}
	// Deep comparison of arrays and objects.
	if got := evalStr(t, `print [1, 2] == [1, 2]`); got != "true" {
		t.Errorf("array equality: got %q", got)
	}
	if got := evalStr(t, `print {a: 1} == {a: 1}`); got != "true" {
		t.Errorf("object equality: got %q", got)
	}
	if got := evalStr(t, `print {a: 1} == {a: 2}`); got != "false" {
		t.Errorf("object inequality: got %q", got)
	}
}

func TestArraysAndObjects(t *testing.T) {
	if got := evalStr(t, `$a = [1, 2, 3]
print $a[0]
print $a[-1]
print $a[1:]
print $a[0:2]`); got != "1\n3\n[2, 3]\n[1, 2]" {
		t.Errorf("index/slice: got %q", got)
	}
	// Out-of-range index is an error, not silent null.
	evalFails(t, `$a = [1, 2]
print $a[5]`, "out of range")
	// at() supplies a default instead.
	if got := evalStr(t, `$a = [1, 2]
print at($a, 5, "kosong")`); got != "kosong" {
		t.Errorf("at default: got %q", got)
	}
	// Value semantics: append does not mutate the original.
	if got := evalStr(t, `$a = [1]
$b = append($a, 2)
print $a
print $b`); got != "[1]\n[1, 2]" {
		t.Errorf("value semantics: got %q", got)
	}
	// Append respects a declared element type.
	evalFails(t, `array<int> $ids = [1]
$ids = append($ids, "dua")`, "expected int")
	// Object access and mutation.
	if got := evalStr(t, `$u = {nama: "Siti", umur: 25}
$u.umur = 26
print $u.umur
print $u["nama"]
print $u.tidak_ada`); got != "26\nSiti\nnull" {
		t.Errorf("object access: got %q", got)
	}
	// Deleting a field.
	if got := evalStr(t, `$u = {a: 1, b: 2}
print unset($u, "a")
print $u`); got != "true\n{b: 2}" {
		t.Errorf("unset: got %q", got)
	}
	// Nested structures.
	if got := evalStr(t, `$data = {user: {nama: "Ada", tag: [1, 2]}}
print $data.user.nama
print $data.user.tag[1]`); got != "Ada\n2" {
		t.Errorf("nested: got %q", got)
	}
}

func TestComprehensions(t *testing.T) {
	if got := evalStr(t, `print [$n * 2 for $n in [1, 2, 3]]`); got != "[2, 4, 6]" {
		t.Errorf("comprehension: got %q", got)
	}
	if got := evalStr(t, `print [$n for $n in [1, 2, 3, 4] if $n % 2 == 0]`); got != "[2, 4]" {
		t.Errorf("comprehension with if: got %q", got)
	}
	if got := evalStr(t, `$pasangan = [$k + "=" + str($v) for $k, $v in {a: 1}]
print $pasangan`); got != "[a=1]" {
		t.Errorf("object comprehension: got %q", got)
	}
}

func TestStringInterpolation(t *testing.T) {
	// Both interpolation forms work.
	if got := evalStr(t, `$nama = "Ada"
print "Halo ${nama} dan $nama"`); got != "Halo Ada dan Ada" {
		t.Errorf("short and braced form: got %q", got)
	}
	// A `$` that is not followed by a name stays literal.
	if got := evalStr(t, `print "harga 5$ dan 100$ dan $"`); got != "harga 5$ dan 100$ dan $" {
		t.Errorf("literal dollar: got %q", got)
	}
	// A nested string inside an interpolation must not confuse the lexer.
	if got := evalStr(t, `$jenis = "kota"
print "anda di ${$jenis + "!"}"`); got != "anda di kota!" {
		t.Errorf("nested string in interpolation: got %q", got)
	}
	// An object literal inside an interpolation.
	if got := evalStr(t, `$u = {nama: "Siti"}
print "user: ${$u.nama} (${$u.umur})"`); got != "user: Siti (null)" {
		t.Errorf("object access in interpolation: got %q", got)
	}
	// Backtick strings do not interpolate.
	if got := evalStr(t, "$nama = \"x\"\nprint `$nama`"); got != "$nama" {
		t.Errorf("raw string must not interpolate: got %q", got)
	}
	// math.pi is a value, not a function.
	if got := evalStr(t, `use "math"
print math.pi`); got != "3.141592653589793" {
		t.Errorf("math.pi: got %q", got)
	}
}

func TestStrings(t *testing.T) {
	if got := evalStr(t, `$nama = "Garurda"
print "Halo ${nama}, kamu ${1 + 1} tahun"`); got != "Halo Garurda, kamu 2 tahun" {
		t.Errorf("interpolation: got %q", got)
	}
	// Escapes and raw strings.
	if got := evalStr(t, "print \"a\\tb\""); got != "a\tb" {
		t.Errorf("escape: got %q", got)
	}
	if got := evalStr(t, "print `a\\nb`"); got != `a\nb` {
		t.Errorf("raw string: got %q", got)
	}
	if got := evalStr(t, `print "garurda".upper()`); got != "GARURDA" {
		t.Errorf("method: got %q", got)
	}
	if got := evalStr(t, `use "strings"
print strings.split("a,b,c", ",")`); got != "[a, b, c]" {
		t.Errorf("strings module: got %q", got)
	}
	evalFails(t, `use "tidakada"`, "not found")
	// UTF-8 aware length and indexing.
	if got := evalStr(t, `print len("garuda")`); got != "6" {
		t.Errorf("len: got %q", got)
	}
	if got := evalStr(t, `print "é"[0]`); got != "é" {
		t.Errorf("rune index: got %q", got)
	}
}

func TestErrorsAndThrow(t *testing.T) {
	// throw + catch.
	if got := evalStr(t, `try {
	throw not_found("user tidak ada")
} catch e {
	println e.message
	println e.status
}`); got != "user tidak ada\n404" {
		t.Errorf("catch: got %q", got)
	}
	// Catch binding is optional.
	if got := evalStr(t, `try { throw "bocah" } catch { println "tertangkap" }`); got != "tertangkap" {
		t.Errorf("catch without binding: got %q", got)
	}
	// Errors propagate out of try without catch.
	evalFails(t, `try { throw "boom" }`, "boom")
	// Errors cross function boundaries.
	if got := evalStr(t, `fn gagal() { throw bad_request("input salah") }
try { gagal() } catch e { println "${e.status} ${e.message}" }`); got != "400 input salah" {
		t.Errorf("cross-function throw: got %q", got)
	}
	// An error value carries code and status.
	if got := evalStr(t, `$e = error("x", {code: "kustom", status: 418})
print $e.code`); got != "kustom" {
		t.Errorf("error(): got %q", got)
	}
	// error.is(code) compares against the argument, not the receiver.
	if got := evalStr(t, `$e = error("x", {code: "kustom"})
print $e.is("kustom")`); got != "true" {
		t.Errorf("error.is match: got %q", got)
	}
	if got := evalStr(t, `not_found("cari").is("http_error")`); got != "true" {
		t.Errorf("error.is http_error: got %q", got)
	}
	if got := evalStr(t, `not_found("cari").is("conflict")`); got != "false" {
		t.Errorf("error.is mismatch: got %q", got)
	}
	// Undefined variables are errors.
	evalFails(t, `print $tidak_ada`, "undefined variable")
	// Division by zero.
	evalFails(t, `print 1 / 0`, "division by zero")
	// Internal errors are not catchable.
	evalFails(t, `try { print $tak_ada } catch e { print " catching" }`, "undefined variable")
}

// TestOperatorPrecedence pins the documented precedence table.
func TestOperatorPrecedence(t *testing.T) {
	cases := []struct{ src, want string }{
		{`print 1 + 2 * 3`, "7"},
		{`print (1 + 2) * 3`, "9"},
		{`print 2 * 3 % 4`, "2"},
		{`print 10 - 2 - 3`, "5"},
		{`print 10 / 2 / 5`, "1"},
		{`print 1 < 2 == true`, "true"},
		{`print not 1 > 2`, "true"},
		{`print 1 + 1 == 2 and 2 + 2 == 4`, "true"},
		{`print false or true and false`, "false"},
		{`print true or false and false`, "true"},
		{`print 2 * 3 + 4 * 5`, "26"},
		{`print -2 * -3`, "6"},
		{`print not true or true`, "true"},
		{`print 1 + 1 == 2 ? "ya" : "tidak"`, "ya"},
		{`print 2 * 3 == 6 and not false`, "true"},
	}
	for _, c := range cases {
		if got := mustRun(t, c.src); got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
}

func TestBuiltinModules(t *testing.T) {
	if got := evalStr(t, `use "math"
print math.floor(3.7)
print math.round(2.5)
print math.max(3, 9, 4)`); got != "3\n3\n9" {
		t.Errorf("math: got %q", got)
	}
	if got := evalStr(t, `use "time"
print type(time.now_ms())`); got != "int" {
		t.Errorf("time: got %q", got)
	}
}

func TestJSONAndEscaping(t *testing.T) {
	if got := evalStr(t, `$o = {nama: "Ada", umur: 30, tag: [1, 2]}
print json_encode($o)`); got != `{"nama":"Ada","umur":30,"tag":[1,2]}` {
		t.Errorf("json_encode: got %q", got)
	}
	if got := evalStr(t, `$v = json_decode(`+"`"+`{"a":1,"b":[true,null]}`+"`"+`)
print $v.a
print $v.b[0]`); got != "1\ntrue" {
		t.Errorf("json_decode: got %q", got)
	}
	evalFails(t, `json_decode("{bukan json")`, "invalid JSON")
	// Input rusak = error lempar: bisa ditangkap dan membawa status 400.
	if got := evalStr(t, `try {
	json_decode("{rusak")
} catch e {
	println e.code
	println e.status
}`); got != "json_error\n400" {
		t.Errorf("json_decode tidak bisa ditangkap: got %q", got)
	}
	// XSS protection helper.
	if got := evalStr(t, `print html_escape("<b>a & b</b>")`); got != "&lt;b&gt;a &amp; b&lt;/b&gt;" {
		t.Errorf("html_escape: got %q", got)
	}
}

func TestArrayBuiltins(t *testing.T) {
	// Note: the transforming builtins return new arrays. `$a` keeps its own
	// order, which is the value semantics the language promises.
	src := `
$a = [5, 3, 9, 1]
print len($a)
print first($a)
print last($a)
print sort($a)
print join($a, "-")
print reverse($a)
print unique([1, 1, 2])
print sum([1, 2, 3])
print filter([1, 2, 3, 4], fn($x) { return $x > 2 })
print map([1, 2], fn($x) { return $x + 1 })
print reduce([1, 2, 3], fn($acc, $x) { return $acc + $x }, 0)
print find([1, 2, 3], fn($x) { return $x > 1 })
print 2 in [1, 2, 3]
print has({a: 1}, "a")
print slice([1, 2, 3, 4], 1, 3)
print keys({x: 1, y: 2})
print merge({a: 1}, {b: 2})
`
	want := `4
5
1
[1, 3, 5, 9]
5-3-9-1
[1, 9, 3, 5]
[1, 2]
6
[3, 4]
[2, 3]
6
2
true
true
[2, 3]
[x, y]
{a: 1, b: 2}`
	if got := mustRun(t, src); got != want {
		t.Errorf("array builtins:\n got: %q\nwant: %q", got, want)
	}
}

// TestToObjectPairs pins the canonical [key, value] pair form.
func TestToObjectPairs(t *testing.T) {
	if got := evalStr(t, `print to_object([["k", 1], ["j", 2]])`); got != "{k: 1, j: 2}" {
		t.Errorf("to_object pairs: got %q", got)
	}
	// Non-pair elements fall back to indexed keys.
	if got := evalStr(t, `print to_object([7])`); got != "{0: 7}" {
		t.Errorf("to_object fallback: got %q", got)
	}
}

func TestReservedWordsUsableWithSigil(t *testing.T) {
	// `$string` and friends are legal variable names thanks to the sigil.
	if got := evalStr(t, `$string = "nilai"
print $string`); got != "nilai" {
		t.Errorf("reserved word as variable: got %q", got)
	}
}

// TestIntSlotDemotion pins the demotion rules: a demoted int slot keeps its
// boxed value in the int index space (never colliding with val slots, which
// used to corrupt builtins at random), a float is never squeezed into the
// int64 slot, and a nullable int parameter binds its null default instead of
// panicking in newFrame.
func TestIntSlotDemotion(t *testing.T) {
	cases := []struct{ src, want string }{
		// any reassigns across types; the old code wrote the string into
		// vals[int-idx] and clobbered whichever val slot shared the index
		// (a builtin, most often `print` → "string is not callable").
		{`any $x = 1
$x = "teks"
print type($x)`, "string"},
		// A float demotes instead of truncating ($x = 72; $x = 3.9 used to
		// store int64(3.9) = 3).
		{`$x = 72
$x = 3.9
print($x)`, "3.9"},
		// number parameters keep their fraction.
		{`fn h(number $x) { print($x) }
h(3.5)`, "3.5"},
		// Nullable int parameter with a null default: nvals == 0 used to
		// panic with index out of range in newFrame.
		{`fn g(?int $x = null) { print($x) }
g()`, "null"},
		// Demotion inside a function whose frame has no val slots at all.
		{`fn local_demote() {
    $y = 1
    $y = "teks"
    print($y)
}
local_demote()`, "teks"},
		// A demoted slot reads back as its new value, not the stale int.
		{`$a = 1
$a = "lima"
print($a)`, "lima"},
	}
	for _, c := range cases {
		if got := evalStr(t, c.src); got != c.want {
			t.Errorf("demotion: got %q, want %q\nsource:\n%s", got, c.want, c.src)
		}
	}
	// An int-annotated variable still rejects a string.
	evalFails(t, `int $a = 72
$a = "teks"`, "expected int")
}

// TestCallDispatchAcrossBackends: an unboxed caller must be able to reach a
// callee on any backend. Demanding cl.int failed valid programs with
// "'g' is not an integer function", and entering bytecode with runInt from
// inside an active VM loop reset live frames and panicked.
func TestCallDispatchAcrossBackends(t *testing.T) {
	// int-specialised caller (has a loop) → general callee.
	if got := evalStr(t, `fn gen($a) { return $a * 2 }
fn f(int $a) int {
    $t = 0
    for $i in 1..2 { $t = $t + 1 }
    return gen($a) + $t
}
print(f(5))`); got != "12" {
		t.Errorf("intfn → general: got %q, want 12", got)
	}
	// int-specialised caller → bytecode callee.
	if got := evalStr(t, `fn bc(int $a) int { return $a * 2 }
fn f(int $a) int {
    $t = 0
    for $i in 1..2 { $t = $t + 1 }
    return bc($a) + $t
}
print(f(5))`); got != "12" {
		t.Errorf("intfn → bytecode: got %q, want 12", got)
	}
	// bytecode caller → general callee → bytecode callee: the nested entry
	// used to panic with index out of range inside runLoop.
	if got := evalStr(t, `fn bc(int $a) int { return $a * 2 }
fn gen($a) { return bc($a) }
fn f(int $a) int { return gen($a) + 1 }
print(f(5))`); got != "11" {
		t.Errorf("bytecode → general → bytecode: got %q, want 11", got)
	}
}

// TestValueGlobalInIntFn: a global holding a plain value lives in the val
// index space. The specialised backends used to read it from local slot 0
// (the return channel) or write to it, silently producing garbage; they must
// fall back to the general path instead.
func TestValueGlobalInIntFn(t *testing.T) {
	// Reading a string global in an int function: the general path computes
	// "x" + 5 and reports the type error; the old code returned 5.
	evalFails(t, `$s = "x"
fn f(int $a) int {
    $b = $s
    return $b + $a
}
print(f(5))`, "cannot add int to string")
	// Assigning a string global from an int function reaches the general
	// path, so the global really changes.
	if got := evalStr(t, `$s = "a"
fn f(int $a) int {
    $s = "b"
    return $a
}
print(f(5))
print($s)`); got != "5\nb" {
		t.Errorf("value global assign: got %q, want \"5\\nb\"", got)
	}
}
