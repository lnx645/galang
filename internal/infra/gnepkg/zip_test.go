package gnepkg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAmbilURLTolakNonHTTPS — skema selain https ditolak sebelum request.
func TestAmbilURLTolakNonHTTPS(t *testing.T) {
	for _, u := range []string{
		"http://contoh.com/x.zip",
		"ftp://contoh.com/x.zip",
		"file:///etc/passwd",
	} {
		if _, err := AmbilURL(context.Background(), u); err == nil || !strings.Contains(err.Error(), "HTTPS") {
			t.Errorf("AmbilURL(%q) harus ditolak dengan pesan HTTPS, dapat: %v", u, err)
		}
	}
}

// TestAmbilURLTolakTanpaHost — URL kosong/host kosong ditolak.
func TestAmbilURLTolakTanpaHost(t *testing.T) {
	if _, err := AmbilURL(context.Background(), "https:///tanpa-host.zip"); err == nil {
		t.Error("URL tanpa host harus ditolak")
	}
}

// TestBacaTulisBerkasRoundtrip — TulisBerkas atomik + BacaBerkas cap.
func TestBacaTulisBerkasRoundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "f.bin")
	data := []byte("isi-berkas")
	if err := TulisBerkas(p, data); err != nil {
		t.Fatalf("tulis: %v", err)
	}
	got, err := BacaBerkas(p, 1024)
	if err != nil {
		t.Fatalf("baca: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("isi = %q", got)
	}
	// Cap: 3 byte tapi isi 10 → ditolak.
	if _, err := BacaBerkas(p, 3); err == nil {
		t.Error("melebihi cap harus ditolak")
	}
	// File sementara tidak boleh tertinggal.
	des, _ := os.ReadDir(filepath.Dir(p))
	for _, d := range des {
		if strings.HasPrefix(d.Name(), ".tmp-") {
			t.Errorf("file sementara tertinggal: %s", d.Name())
		}
	}
}

// TestTulisZipBacaZipRoundtrip — zip reproducible (isi identik) dan
// entri terjaga.
func TestTulisZipBacaZipRoundtrip(t *testing.T) {
	files := []File{
		{Name: "manifest.json", Data: []byte(`{"x":1}`)},
		{Name: "linux-amd64/foo.so", Data: []byte("ELF")},
	}
	p1 := filepath.Join(t.TempDir(), "a.zip")
	p2 := filepath.Join(t.TempDir(), "b.zip")
	if err := TulisZip(p1, files); err != nil {
		t.Fatalf("tulis zip 1: %v", err)
	}
	if err := TulisZip(p2, files); err != nil {
		t.Fatalf("tulis zip 2: %v", err)
	}
	b1, _ := os.ReadFile(p1)
	b2, _ := os.ReadFile(p2)
	if string(b1) != string(b2) {
		t.Error("zip harus reproducible (byte identik untuk isi sama)")
	}

	entries, err := BacaZip(b1)
	if err != nil {
		t.Fatalf("baca zip: %v", err)
	}
	if string(entries["manifest.json"]) != `{"x":1}` {
		t.Errorf("manifest = %q", entries["manifest.json"])
	}
	if string(entries["linux-amd64/foo.so"]) != "ELF" {
		t.Errorf("binari = %q", entries["linux-amd64/foo.so"])
	}
}

// TestTulisZipTolakNamaBerbahaya — nama entri jahat ditolak menulis.
func TestTulisZipTolakNamaBerbahaya(t *testing.T) {
	bahaya := []string{"../evil.so", "/abs/evil.so", `win\evil.so`, ""}
	for _, n := range bahaya {
		err := TulisZip(filepath.Join(t.TempDir(), "x.zip"), []File{{Name: n, Data: []byte("x")}})
		if err == nil {
			t.Errorf("nama %q harus ditolak", n)
		}
	}
}

// TestBacaZipRusak — data bukan zip → galat jelas.
func TestBacaZipRusak(t *testing.T) {
	if _, err := BacaZip([]byte("bukan-zip")); err == nil {
		t.Error("data bukan zip harus ditolak")
	}
}

// TestURLResmi — kanal resmi latest & versi.
func TestURLResmi(t *testing.T) {
	got, err := URLResmi("https://github.com/lnx645/galang", "redis")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://github.com/lnx645/galang/releases/latest/download/redis.zip" {
		t.Errorf("latest = %q", got)
	}
	got, _ = URLResmi("https://github.com/lnx645/galang", "redis@1.2.3")
	if got != "https://github.com/lnx645/galang/releases/download/v1.2.3/redis.zip" {
		t.Errorf("versi = %q", got)
	}
	got, _ = URLResmi("https://github.com/lnx645/galang/", "redis@v9.9.9")
	if got != "https://github.com/lnx645/galang/releases/download/v9.9.9/redis.zip" {
		t.Errorf("prefix v ganda harus dihilangkan: %q", got)
	}
}
