//go:build cgo

package interp

// Test for the official ext/uuid extension: compiles the original source
// (not a copy), runs it via `use "uuid"` in a real GaLang program, and
// checks the canonical 8-4-4-4-12 format, RFC version/variant placement,
// uniqueness across calls, the v7 timestamp, and error mapping.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// bangunUuidC compiles ext/uuid/uuid.c (the original source) into
// <dir>/gne/uuid.so. It fails if gcc is missing — this is our code, not a fixture.
func bangunUuidC(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}
	src := filepath.Join("..", "..", "..", "ext", "uuid", "uuid.c")
	inc := filepath.Join("..", "..", "..", "include")
	out := filepath.Join(dir, "gne", "uuid.so")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("gcc", "-shared", "-fPIC", "-Wall", "-Wextra",
		"-I", inc, "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to compile ext/uuid/uuid.c: %v\n%s", err, b)
	}
}

var (
	reUuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	reUuidV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// TestUuidFormatAndUniqueness checks the canonical v4/v7 format
// (lowercase, exact separator placement), uniqueness across calls, and
// a sensible v7 timestamp (milliseconds ≥ 2020-01-01).
func TestUuidFormatAndUniqueness(t *testing.T) {
	dir := t.TempDir()
	bangunUuidC(t, dir)

	out, err := jalankanMain(t, dir, `
use "uuid"
print(uuid.v4())
print(uuid.v4())
print(uuid.v4())
print(uuid.v7())
print(uuid.v7())
print(uuid.is_valid(uuid.v4()))
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("line count = %d, want 6:\n%s", len(lines), out)
	}
	for i := 0; i < 3; i++ {
		if !reUuidV4.MatchString(lines[i]) {
			t.Errorf("v4 #%d = %q is not in canonical format", i+1, lines[i])
		}
	}
	if lines[0] == lines[1] || lines[1] == lines[2] || lines[0] == lines[2] {
		t.Errorf("v4 not unique: %q/%q/%q", lines[0], lines[1], lines[2])
	}
	for i := 3; i < 5; i++ {
		if !reUuidV7.MatchString(lines[i]) {
			t.Errorf("v7 #%d = %q is not in canonical format", i-2, lines[i])
		}
		// First 48 bits = millisecond timestamp (12 hex digits, the
		// inserted separator is skipped). Reasonable if ≥ 2020-01-01.
		hex48 := lines[i][:8] + lines[i][9:13]
		ms, err := strconv.ParseInt(hex48, 16, 64)
		if err != nil {
			t.Fatalf("parsing v7 timestamp: %v", err)
		}
		if ms < 1577836800000 {
			t.Errorf("v7 timestamp #%d too small: %d ms", i-2, ms)
		}
	}
	if lines[5] != "true" {
		t.Errorf("is_valid(v4) = %q, want true", lines[5])
	}
}

// TestUuidIsValid exercises a valid/invalid case table: versions 0–8
// are accepted, RFC variant (8/9/a/b) is required, hex may be any case.
func TestUuidIsValid(t *testing.T) {
	dir := t.TempDir()
	bangunUuidC(t, dir)

	kasus := []struct {
		uuid string
		sah  bool
	}{
		{"f47ac10b-58cc-4372-a567-0e02b2c3d479", true},  // v4
		{"018f1a2b-3c4d-7ef0-8abc-def123456789", true},  // v7
		{"F47AC10B-58CC-4372-A567-0E02B2C3D479", true},  // uppercase
		{"2d6e4f8a-1b3c-8d9e-af01-23456789abcd", true},  // v8, variant a
		{"f47ac10b-58cc-0372-8567-0e02b2c3d479", true},  // v0, variant 8
		{"f47ac10b-58cc-9372-8567-0e02b2c3d479", false}, // version 9 outside RFC
		{"f47ac10b-58cc-4372-c567-0e02b2c3d479", false}, // variant c (NCS)
		{"f47ac10b-58cc-4372-a5670e02b2c3d479", false},  // without separators
		{"f47ac10b-58cc-4372-a567-0e02b2c3d47", false},  // too short
		{"g47ac10b-58cc-4372-a567-0e02b2c3d479", false}, // non-hex character
		{"", false}, // empty
	}
	var src strings.Builder
	src.WriteString("use \"uuid\"\n")
	for _, k := range kasus {
		fmt.Fprintf(&src, "print(uuid.is_valid(%q))\n", k.uuid)
	}

	out, err := jalankanMain(t, dir, src.String())
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(got) != len(kasus) {
		t.Fatalf("line count = %d, want %d:\n%s", len(got), len(kasus), out)
	}
	for i, k := range kasus {
		want := "false"
		if k.sah {
			want = "true"
		}
		if got[i] != want {
			t.Errorf("is_valid(%q) = %s, want %s", k.uuid, got[i], want)
		}
	}
}

// TestUuidError checks that a non-string argument is rejected with type_error.
func TestUuidError(t *testing.T) {
	dir := t.TempDir()
	bangunUuidC(t, dir)

	out, err := jalankanMain(t, dir, `
use "uuid"
try {
  uuid.is_valid(123)
  print("no")
} catch e {
  print(e.code)
}
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got := strings.TrimSpace(out); got != "type_error" {
		t.Errorf("is_valid(123) = %q, want type_error", got)
	}
}
