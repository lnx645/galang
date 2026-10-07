package interp

import (
	"io"
	"testing"
)

// discard swallows benchmark output.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

const (
	// fibSource is CPU-bound recursion.
	fibSource = `
fn fib(int $n) int {
	if $n < 2 { return $n }
	return fib($n - 1) + fib($n - 2)
}
print fib(25)
`
	// loopSource exercises arithmetic and comparison in a tight loop.
	loopSource = `
$total = 0
for $i in 0..200000 {
	$total = $total + $i * 2 - 1
}
print $total
`
	// callSource measures the function-call path on its own.
	callSource = `
fn kerja(int $n) int {
	$acc = 0
	for $i in 0..$n { $acc = $acc + $i }
	return $acc
}
$hasil = kerja(100000)
`
)

// bench runs src once to warm up, then b.N times.
func bench(b *testing.B, src string) {
	b.Helper()
	in := New(io.Discard, io.Discard)
	if _, err := in.Eval(src, "bench.ga"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := in.Eval(src, "bench.ga"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFib(b *testing.B)  { bench(b, fibSource) }
func BenchmarkLoop(b *testing.B) { bench(b, loopSource) }
func BenchmarkCall(b *testing.B) { bench(b, callSource) }
