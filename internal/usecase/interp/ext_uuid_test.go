//go:build cgo

package interp

// Uji ekstensi resmi ext/uuid: mengompilasi sumber asli (bukan salinan),
// menjalankannya melalui `use "uuid"` di program GaLang nyata, dan
// memeriksa format kanonik 8-4-4-4-12, penempatan versi/varian RFC,
// keunikan antar-panggilan, stempel waktu v7, serta pemetaan galat.

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

// bangunUuidC mengompilasi ext/uuid/uuid.c (sumber asli) menjadi
// <dir>/gne/uuid.so. Gagal bila gcc tidak ada — ini kode kita, bukan fixture.
func bangunUuidC(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc tidak tersedia")
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
		t.Fatalf("gagal mengompilasi ext/uuid/uuid.c: %v\n%s", err, b)
	}
}

var (
	reUuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	reUuidV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// TestUuidFormatDanKeunikan memeriksa format kanonik v4/v7 (huruf
// kecil, tanda pisah tepat), keunikan antar-panggilan, dan stempel
// waktu v7 yang masuk akal (milidetik ≥ 2020-01-01).
func TestUuidFormatDanKeunikan(t *testing.T) {
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
		t.Fatalf("jumlah baris = %d, mau 6:\n%s", len(lines), out)
	}
	for i := 0; i < 3; i++ {
		if !reUuidV4.MatchString(lines[i]) {
			t.Errorf("v4 #%d = %q tidak berformat kanonik", i+1, lines[i])
		}
	}
	if lines[0] == lines[1] || lines[1] == lines[2] || lines[0] == lines[2] {
		t.Errorf("v4 tidak unik: %q/%q/%q", lines[0], lines[1], lines[2])
	}
	for i := 3; i < 5; i++ {
		if !reUuidV7.MatchString(lines[i]) {
			t.Errorf("v7 #%d = %q tidak berformat kanonik", i-2, lines[i])
		}
		// 48 bit pertama = stempel waktu milidetik (12 heksa, sisipan
		// tanda pisah dilewati). Wajar bila ≥ 2020-01-01.
		hex48 := lines[i][:8] + lines[i][9:13]
		ms, err := strconv.ParseInt(hex48, 16, 64)
		if err != nil {
			t.Fatalf("parse stempel waktu v7: %v", err)
		}
		if ms < 1577836800000 {
			t.Errorf("stempel waktu v7 #%d terlalu kecil: %d ms", i-2, ms)
		}
	}
	if lines[5] != "true" {
		t.Errorf("is_valid(v4) = %q, mau true", lines[5])
	}
}

// TestUuidIsValid menguji tabel kasus valid/tidak valid: versi 0–8
// diterima, varian RFC (8/9/a/b) wajib, heksa boleh besar/kecil.
func TestUuidIsValid(t *testing.T) {
	dir := t.TempDir()
	bangunUuidC(t, dir)

	kasus := []struct {
		uuid string
		sah  bool
	}{
		{"f47ac10b-58cc-4372-a567-0e02b2c3d479", true},  // v4
		{"018f1a2b-3c4d-7ef0-8abc-def123456789", true},  // v7
		{"F47AC10B-58CC-4372-A567-0E02B2C3D479", true},  // huruf besar
		{"2d6e4f8a-1b3c-8d9e-af01-23456789abcd", true},  // v8, varian a
		{"f47ac10b-58cc-0372-8567-0e02b2c3d479", true},  // v0, varian 8
		{"f47ac10b-58cc-9372-8567-0e02b2c3d479", false}, // versi 9 di luar RFC
		{"f47ac10b-58cc-4372-c567-0e02b2c3d479", false}, // varian c (NCS)
		{"f47ac10b-58cc-4372-a5670e02b2c3d479", false},  // tanpa tanda pisah
		{"f47ac10b-58cc-4372-a567-0e02b2c3d47", false},  // kependekan
		{"g47ac10b-58cc-4372-a567-0e02b2c3d479", false}, // huruf non-heksa
		{"", false}, // kosong
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
		t.Fatalf("jumlah baris = %d, mau %d:\n%s", len(got), len(kasus), out)
	}
	for i, k := range kasus {
		want := "false"
		if k.sah {
			want = "true"
		}
		if got[i] != want {
			t.Errorf("is_valid(%q) = %s, mau %s", k.uuid, got[i], want)
		}
	}
}

// TestUuidGalat memeriksa argumen non-string ditolak type_error.
func TestUuidGalat(t *testing.T) {
	dir := t.TempDir()
	bangunUuidC(t, dir)

	out, err := jalankanMain(t, dir, `
use "uuid"
try {
  uuid.is_valid(123)
  print("tidak")
} catch e {
  print(e.code)
}
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got := strings.TrimSpace(out); got != "type_error" {
		t.Errorf("is_valid(123) = %q, mau type_error", got)
	}
}
