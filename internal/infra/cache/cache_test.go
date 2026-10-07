package cache

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"garurda/internal/usecase/interp"
)

func TestCache(t *testing.T) {
	// Use a discard writer for test output
	discard := io.Discard

	dir, err := os.MkdirTemp("", "garurda-cache-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	p := New()
	p.Dir = dir

	in := interp.New(discard, discard)
	in.UseParser(p.Parse)

	src := `fn add(int $a, int $b) int { return $a + $b }
print add(2, 3)
`

	// First run - should populate cache
	if _, err := in.Eval(src, "test.ga"); err != nil {
		t.Fatalf("First run error: %v", err)
	}
	if p.Hits != 0 || p.Misses != 1 {
		t.Errorf("After first run: hits=%d misses=%d want hits=0 misses=1", p.Hits, p.Misses)
	}

	// Second run - should hit cache
	if _, err := in.Eval(src, "test.ga"); err != nil {
		t.Fatalf("Second run error: %v", err)
	}
	if p.Hits != 1 || p.Misses != 1 {
		t.Errorf("After second run: hits=%d misses=%d want hits=1 misses=1", p.Hits, p.Misses)
	}

	// Verify cache file exists
	files, _ := filepath.Glob(filepath.Join(dir, "*.ast"))
	if len(files) != 1 {
		t.Errorf("Expected 1 cache file, got %d", len(files))
	}

	// Different source - should miss cache
	src2 := `fn mul(int $a, int $b) int { return $a * $b }
print mul(3, 4)
`
	if _, err := in.Eval(src2, "test2.ga"); err != nil {
		t.Fatalf("Third run error: %v", err)
	}
	if p.Hits != 1 || p.Misses != 2 {
		t.Errorf("After different source: hits=%d misses=%d want hits=1 misses=2", p.Hits, p.Misses)
	}

	// Second run of second source - should hit
	if _, err := in.Eval(src2, "test2.ga"); err != nil {
		t.Fatalf("Fourth run error: %v", err)
	}
	if p.Hits != 2 || p.Misses != 2 {
		t.Errorf("After second run of src2: hits=%d misses=%d want hits=2 misses=2", p.Hits, p.Misses)
	}
}
