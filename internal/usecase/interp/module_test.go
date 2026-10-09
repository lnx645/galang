package interp

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tulisFile menulis file di direktori tes dan mengembalikan path-nya.
func tulisFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// jalankanMain mengevaluasi src sebagai main.ga di dalam dir.
func jalankanMain(t *testing.T, dir, src string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	in := New(&out, &out)
	_, err := in.Eval(src, filepath.Join(dir, "main.ga"))
	return out.String(), err
}

// use "db" memuat db.ga di direktori yang sama: fn ter-ekspor sebagai
// namespace, variabel top-level privat tapi terbaca oleh fn modul, dan
// builtin tersedia tanpa import tambahan.
func TestUseFileDasar(t *testing.T) {
	dir := t.TempDir()
	tulisFile(t, dir, "db.ga", `
$rahasia = "privat"

fn sapa($n) {
    return "halo " + $n
}

fn ambil_rahasia() {
    return $rahasia
}

fn panjang($s) {
    return len($s)
}
`)
	out, err := jalankanMain(t, dir, `
use "db"
print(db.sapa("dunia"))
print(db.ambil_rahasia())
print(db.panjang("abcde"))
print(db.rahasia)
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if want := "halo dunia\nprivat\n5\nnull"; strings.TrimSpace(out) != want {
		t.Errorf("output = %q, want %q", strings.TrimSpace(out), want)
	}
}

// Scope modul terisolasi: fn di file modul tidak melihat global pemanggil.
func TestUseFileIsolasiScope(t *testing.T) {
	dir := t.TempDir()
	tulisFile(t, dir, "m.ga", `
fn lihat() {
    return $x
}
`)
	_, err := jalankanMain(t, dir, `
$x = 1
use "m"
print(m.lihat())
`)
	if err == nil {
		t.Fatal("modul seharusnya tidak melihat global pemanggil")
	}
	if !strings.Contains(err.Error(), "x") {
		t.Errorf("pesan galat tidak menyebut nama: %v", err)
	}
}

// State top-level file bertahan antar-panggilan (fn berbagi frame modul).
func TestUseFileStateBersistem(t *testing.T) {
	dir := t.TempDir()
	tulisFile(t, dir, "hitung.ga", `
$hit = 0

fn naik() {
    $hit = $hit + 1
    return $hit
}
`)
	out, err := jalankanMain(t, dir, `
use "hitung"
print(hitung.naik())
print(hitung.naik())
print(hitung.naik())
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if want := "1\n2\n3"; strings.TrimSpace(out) != want {
		t.Errorf("output = %q, want %q", strings.TrimSpace(out), want)
	}
}

// Modul bawaan menang: file strings.ga di direktori kerja tidak
// membelokkan use "strings".
func TestUseFileBawaanMenang(t *testing.T) {
	dir := t.TempDir()
	tulisFile(t, dir, "strings.ga", `print("BOCOR")`)
	out, err := jalankanMain(t, dir, `
use "strings"
print(strings.split("a,b", ",")[1])
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if strings.Contains(out, "BOCOR") {
		t.Errorf("file lokal membajak modul bawaan: %q", out)
	}
	if strings.TrimSpace(out) != "b" {
		t.Errorf("output = %q, want b", strings.TrimSpace(out))
	}
}

// Rantai melingkar ditolak dengan jelas.
func TestUseFileSiklus(t *testing.T) {
	dir := t.TempDir()
	tulisFile(t, dir, "a.ga", "use \"b\"\nfn fa() { return 1 }\n")
	tulisFile(t, dir, "b.ga", "use \"a\"\nfn fb() { return 2 }\n")
	_, err := jalankanMain(t, dir, "use \"a\"\n")
	if err == nil {
		t.Fatal("rantai melingkar seharusnya ditolak")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("pesan galat = %v, want contains \"circular\"", err)
	}
}

// File yang tidak ada: galat menyebut path yang dicoba, dan untuk nama
// telanjang (kandidat GNE ikut dicoba) menyertakan petunjuk pemasangan.
func TestUseFileTidakAda(t *testing.T) {
	dir := t.TempDir()
	_, err := jalankanMain(t, dir, "use \"hilang\"\n")
	if err == nil {
		t.Fatal("file tak ada seharusnya error")
	}
	if !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "hilang") {
		t.Errorf("pesan galat = %v", err)
	}
	if !strings.Contains(err.Error(), "gar gne install hilang") {
		t.Errorf("galat tanpa petunjuk pemasangan GNE: %v", err)
	}
}

// Path eksplisit (bertanda slash) bukan wilayah GNE — tanpa petunjuk
// gar gne install agar tidak menyesatkan.
func TestUseFileTidakAdaPathEksplisitTanpaPetunjukGNE(t *testing.T) {
	dir := t.TempDir()
	_, err := jalankanMain(t, dir, "use \"lib/hilang\"\n")
	if err == nil {
		t.Fatal("path eksplisit tak ada seharusnya error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("pesan galat = %v", err)
	}
	if strings.Contains(err.Error(), "gar gne install") {
		t.Errorf("path eksplisit tidak boleh menyertakan petunjuk GNE: %v", err)
	}
}

// use kedua memakai objek yang sama — badan file hanya sekali jalan.
func TestUseFileCacheSekali(t *testing.T) {
	dir := t.TempDir()
	tulisFile(t, dir, "m.ga", `
print("muat")
fn ok() { return "ok" }
`)
	out, err := jalankanMain(t, dir, `
use "m"
use "m"
print(m.ok())
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if n := strings.Count(out, "muat"); n != 1 {
		t.Errorf("badan modul dijalankan %d kali, want 1 (out=%q)", n, out)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("output = %q", out)
	}
}

// Path subdirektori: use "lib/util.ga" mengikat util; di dalam modul,
// use berikutnya relatif ke direktori modul itu sendiri.
func TestUseFileSubdirektoriRelatif(t *testing.T) {
	dir := t.TempDir()
	tulisFile(t, dir, "lib/lagi.ga", `
fn sapa() {
    return "lagi"
}
`)
	tulisFile(t, dir, "lib/util.ga", `
use "lagi"

fn sapa() {
    return "util:" + lagi.sapa()
}
`)
	out, err := jalankanMain(t, dir, `
use "lib/util.ga"
print(util.sapa())
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if strings.TrimSpace(out) != "util:lagi" {
		t.Errorf("output = %q, want util:lagi", strings.TrimSpace(out))
	}
}

// Galat runtime di dalam fn modul tetap terbaca (module path di error).
func TestUseFileGalatRuntime(t *testing.T) {
	dir := t.TempDir()
	tulisFile(t, dir, "m.ga", `
fn salah() {
    return $tidak_ada
}
`)
	_, err := jalankanMain(t, dir, `
use "m"
print(m.salah())
`)
	if err == nil {
		t.Fatal("membaca variabel tak dikenal seharusnya error")
	}
	if !strings.Contains(err.Error(), "m.ga") {
		t.Errorf("galat tidak menunjuk file modul: %v", err)
	}
}
