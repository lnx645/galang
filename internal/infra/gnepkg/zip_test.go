package gnepkg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFetchURLRejectsNonHTTPS — a scheme other than https is rejected
// before any request is made.
func TestFetchURLRejectsNonHTTPS(t *testing.T) {
	for _, u := range []string{
		"http://example.com/x.zip",
		"ftp://example.com/x.zip",
		"file:///etc/passwd",
	} {
		if _, err := FetchURL(context.Background(), u); err == nil || !strings.Contains(err.Error(), "HTTPS") {
			t.Errorf("FetchURL(%q) must be rejected with an HTTPS message, got: %v", u, err)
		}
	}
}

// TestFetchURLRejectsNoHost — an empty URL / empty host is rejected.
func TestFetchURLRejectsNoHost(t *testing.T) {
	if _, err := FetchURL(context.Background(), "https:///no-host.zip"); err == nil {
		t.Error("URL without a host must be rejected")
	}
}

// TestWriteReadFileRoundtrip — WriteFile is atomic + ReadFile enforces
// its cap.
func TestWriteReadFileRoundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "f.bin")
	data := []byte("file-content")
	if err := WriteFile(p, data); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadFile(p, 1024)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("content = %q", got)
	}
	// Cap: 3 bytes but the content is 10 → rejected.
	if _, err := ReadFile(p, 3); err == nil {
		t.Error("exceeding the cap must be rejected")
	}
	// No temporary file may be left behind.
	des, _ := os.ReadDir(filepath.Dir(p))
	for _, d := range des {
		if strings.HasPrefix(d.Name(), ".tmp-") {
			t.Errorf("temporary file left behind: %s", d.Name())
		}
	}
}

// TestWriteZipReadZipRoundtrip — the zip is reproducible (identical
// contents) and the entries are preserved.
func TestWriteZipReadZipRoundtrip(t *testing.T) {
	files := []File{
		{Name: "manifest.json", Data: []byte(`{"x":1}`)},
		{Name: "linux-amd64/foo.so", Data: []byte("ELF")},
	}
	p1 := filepath.Join(t.TempDir(), "a.zip")
	p2 := filepath.Join(t.TempDir(), "b.zip")
	if err := WriteZip(p1, files); err != nil {
		t.Fatalf("write zip 1: %v", err)
	}
	if err := WriteZip(p2, files); err != nil {
		t.Fatalf("write zip 2: %v", err)
	}
	b1, _ := os.ReadFile(p1)
	b2, _ := os.ReadFile(p2)
	if string(b1) != string(b2) {
		t.Error("zip must be reproducible (identical bytes for identical contents)")
	}

	entries, err := ReadZip(b1)
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	if string(entries["manifest.json"]) != `{"x":1}` {
		t.Errorf("manifest = %q", entries["manifest.json"])
	}
	if string(entries["linux-amd64/foo.so"]) != "ELF" {
		t.Errorf("binary = %q", entries["linux-amd64/foo.so"])
	}
}

// TestWriteZipRejectsMaliciousNames — malicious entry names are rejected
// on write.
func TestWriteZipRejectsMaliciousNames(t *testing.T) {
	bahaya := []string{"../evil.so", "/abs/evil.so", `win\evil.so`, ""}
	for _, n := range bahaya {
		err := WriteZip(filepath.Join(t.TempDir(), "x.zip"), []File{{Name: n, Data: []byte("x")}})
		if err == nil {
			t.Errorf("name %q must be rejected", n)
		}
	}
}

// TestReadZipCorrupt — data that is not a zip → a clear error.
func TestReadZipCorrupt(t *testing.T) {
	if _, err := ReadZip([]byte("not-a-zip")); err == nil {
		t.Error("data that is not a zip must be rejected")
	}
}

// TestOfficialURL — official channel, latest and pinned version.
func TestOfficialURL(t *testing.T) {
	got, err := OfficialURL("https://github.com/lnx645/galang", "redis")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://github.com/lnx645/galang/releases/latest/download/redis.zip" {
		t.Errorf("latest = %q", got)
	}
	got, _ = OfficialURL("https://github.com/lnx645/galang", "redis@1.2.3")
	if got != "https://github.com/lnx645/galang/releases/download/v1.2.3/redis.zip" {
		t.Errorf("version = %q", got)
	}
	got, _ = OfficialURL("https://github.com/lnx645/galang/", "redis@v9.9.9")
	if got != "https://github.com/lnx645/galang/releases/download/v9.9.9/redis.zip" {
		t.Errorf("double v prefix must be stripped: %q", got)
	}
}
