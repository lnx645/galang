package gnepkg

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"garurda/internal/infra/gne"
	"garurda/internal/infra/gnepkg"
)

// bangunFakeExt membuat struktur ext/redis tiruan: gne.json + build/<plat>.
func bangunFakeExt(t *testing.T, name, versi string, binari map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	src, err := json.Marshal(Source{Name: name, Version: versi, Description: "uji"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gne.json"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	for plat, isi := range binari {
		goos, _, ok := splitPlatform(plat)
		if !ok {
			t.Fatalf("platform tiruan tidak sah: %s", plat)
		}
		d := filepath.Join(dir, "build", plat)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, name+libExtFor(goos)), []byte(isi), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestPackInstallListRemove — round-trip penuh: pack → install (file
// lokal) → list → remove, plus verifikasi isi file terpasang.
func TestPackInstallListRemove(t *testing.T) {
	plat := CurrentPlatform()
	// darwin-amd64 sengaja disertakan: platform terurut pertama di manifest,
	// padahal bukan milik mesin uji — ukuran list wajib tetap terbaca.
	ext := bangunFakeExt(t, "redis", "0.6.0", map[string]string{
		plat:            "ELF-palsu-redis",
		"darwin-amd64":  "Mach-palsu",
		"windows-amd64": "PE-palsu",
	})
	zipPath := filepath.Join(t.TempDir(), "redis.zip")
	gotZip, m, err := Pack(ext, zipPath)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if gotZip != zipPath {
		t.Errorf("Pack = %q, mau %q", gotZip, zipPath)
	}
	if m.Name != "redis" || m.Version != "0.6.0" || m.GNEABI != gne.ABI {
		t.Errorf("manifest salah: %+v", m)
	}
	if _, ok := m.Entry[plat]; !ok {
		t.Fatalf("entry platform %s hilang: %v", plat, m.Entry)
	}

	dir := filepath.Join(t.TempDir(), "gne")
	res, err := Install(context.Background(), zipPath, Options{Dir: dir})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.Platform != plat {
		t.Errorf("platform terpasang %q, mau %q", res.Platform, plat)
	}
	// Isi file persis binari (dan sha cocok — sudah diverifikasi 2x).
	isi, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("baca file terpasang: %v", err)
	}
	if string(isi) != "ELF-palsu-redis" {
		t.Errorf("isi file = %q", isi)
	}
	if filepath.Base(res.Path) != "redis"+libExtFor(runtime.GOOS) {
		t.Errorf("nama file terpasang %q tidak sesuai pola discovery", filepath.Base(res.Path))
	}
	if _, err := os.Stat(filepath.Join(dir, "redis.gne.json")); err != nil {
		t.Errorf("sidecar tidak ada: %v", err)
	}

	// Install ulang tanpa --force harus ditolak.
	if _, err := Install(context.Background(), zipPath, Options{Dir: dir}); err == nil {
		t.Error("install ulang tanpa --force harus ditolak")
	}
	// Dengan --force ditimpa.
	res2, err := Install(context.Background(), zipPath, Options{Dir: dir, Force: true})
	if err != nil {
		t.Fatalf("install --force: %v", err)
	}
	if !res2.Previous {
		t.Error("Result.Previous harus true saat menimpa")
	}

	// List menampilkan satu entri.
	items, err := List(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Name != "redis" || items[0].Version != "0.6.0" {
		t.Errorf("list = %+v", items)
	}
	if items[0].ABI != gne.ABI {
		t.Errorf("list ABI = %d", items[0].ABI)
	}
	if items[0].Size != int64(len("ELF-palsu-redis")) {
		t.Errorf("list Size = %d, mau %d (binari terpasang harus terbaca)",
			items[0].Size, len("ELF-palsu-redis"))
	}

	// Remove membersihkan file + sidecar.
	dihapus, err := Remove(dir, "redis")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(dihapus, "redis") {
		t.Errorf("laporan remove aneh: %s", dihapus)
	}
	if _, err := os.Stat(filepath.Join(dir, "redis.gne.json")); !os.IsNotExist(err) {
		t.Error("sidecar masih ada setelah remove")
	}
	if _, err := List(dir); err != nil {
		t.Errorf("list setelah remove: %v", err)
	}
	// Remove kedua harus gagal (sudah tidak terpasang).
	if _, err := Remove(dir, "redis"); err == nil {
		t.Error("remove kedua harus ditolak")
	}
}

// bacaZipBalik menulis map entri jadi zip valid (untuk membuat paket
// nakal yang lolos pembacaan tapi melanggar aturan lain).
func bacaZipBalik(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// instalDariZip memasang byte paket mentah lewat jalur file sementara.
func instalDariZip(t *testing.T, data []byte, o Options) error {
	t.Helper()
	if o.Dir == "" {
		o.Dir = filepath.Join(t.TempDir(), "gne")
	}
	p := filepath.Join(t.TempDir(), "paket.zip")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Install(context.Background(), p, o)
	return err
}

// manifestNakal membangun manifest.json dengan entri untuk platform uji.
func manifestNakal(t *testing.T, mutasi func(m *Manifest)) []byte {
	t.Helper()
	plat := CurrentPlatform()
	m := Manifest{
		Name:    "redis",
		Version: "0.6.0",
		GNEABI:  gne.ABI,
		Entry: map[string]Entry{
			plat: {File: plat + "/redis" + libExtFor(runtime.GOOS), SHA256: strings.Repeat("a", 64), Size: 3},
		},
	}
	if mutasi != nil {
		mutasi(&m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestInstallTolakZipSlip — entri `..` di dalam zip ditolak saat pembacaan,
// bahkan bila manifest-nya sendiri tampak sah (pertahanan berlapis).
func TestInstallTolakZipSlip(t *testing.T) {
	plat := CurrentPlatform()
	man := manifestNakal(t, nil) // manifest valid — entri jahat jadi ekstra
	paket := bacaZipBalik(t, map[string][]byte{
		ManifestFile: man,
		plat + "/redis" + libExtFor(runtime.GOOS): []byte("ELF"),
		"../redis" + libExtFor(runtime.GOOS):      []byte("jahat"),
	})
	err := instalDariZip(t, paket, Options{})
	if err == nil || !strings.Contains(err.Error(), "zip-slip") {
		t.Errorf("zip-slip harus ditolak, dapat: %v", err)
	}
}

// TestInstallTolakPathAbsolut — entri absolut ditolak.
func TestInstallTolakPathAbsolut(t *testing.T) {
	man := manifestNakal(t, nil)
	paket := bacaZipBalik(t, map[string][]byte{
		ManifestFile: man,
		"/etc/evil":  []byte("x"),
	})
	err := instalDariZip(t, paket, Options{})
	if err == nil || !strings.Contains(err.Error(), "absolut") {
		t.Errorf("path absolut harus ditolak, dapat: %v", err)
	}
}

// TestInstallTolakShaTidakCocok — binari yang tidak cocok manifest
// ditolak sebelum menulis apa pun ke disk.
func TestInstallTolakShaTidakCocok(t *testing.T) {
	plat := CurrentPlatform()
	man := manifestNakal(t, nil) // sha = aaaa... (bukan sha "ELF")
	paket := bacaZipBalik(t, map[string][]byte{
		ManifestFile: man,
		plat + "/redis" + libExtFor(runtime.GOOS): []byte("ELF"),
	})
	dir := filepath.Join(t.TempDir(), "gne")
	err := instalDariZip(t, paket, Options{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("sha tidak cocok harus ditolak, dapat: %v", err)
	}
	// Tidak boleh ada file yang tertulis.
	if des, _ := os.ReadDir(dir); len(des) != 0 {
		t.Errorf("direktori target harus kosong, berisi %d", len(des))
	}
}

// TestInstallTolakABIBeda — paket untuk ABI lain ditolak.
func TestInstallTolakABIBeda(t *testing.T) {
	plat := CurrentPlatform()
	man := manifestNakal(t, func(m *Manifest) { m.GNEABI = gne.ABI + 1 })
	paket := bacaZipBalik(t, map[string][]byte{
		ManifestFile: man,
		plat + "/redis" + libExtFor(runtime.GOOS): []byte("ELF"),
	})
	err := instalDariZip(t, paket, Options{})
	if err == nil || !strings.Contains(err.Error(), "ABI") {
		t.Errorf("ABI beda harus ditolak, dapat: %v", err)
	}
}

// TestInstallTolakPlatformKurang — platform mesin tidak ada di paket:
// ditolak; --force memilih platform pertama (prakarsa lintas-mesin).
func TestInstallTolakPlatformKurang(t *testing.T) {
	// Paket khusus platform lain.
	other := "linux-arm64"
	if other == CurrentPlatform() {
		other = "windows-amd64"
	}
	ext := bangunFakeExt(t, "smtp", "0.6.0", map[string]string{other: "BINARI"})
	zipPath := filepath.Join(t.TempDir(), "smtp.zip")
	if _, _, err := Pack(ext, zipPath); err != nil {
		t.Fatalf("pack: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "gne")

	_, err := Install(context.Background(), zipPath, Options{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "tidak menyediakan binari") {
		t.Errorf("platform kurang harus ditolak, dapat: %v", err)
	}

	res, err := Install(context.Background(), zipPath, Options{Dir: dir, Force: true})
	if err != nil {
		t.Errorf("install --force lintas-platform: %v", err)
	} else if res.Platform != other {
		t.Errorf("platform terpasang %q, mau %q", res.Platform, other)
	}
}

// TestInstallTolakTanpaCGO — binari stub menolak pemasangan tanpa --force.
func TestInstallTolakTanpaCGO(t *testing.T) {
	plat := CurrentPlatform()
	ext := bangunFakeExt(t, "redis", "0.6.0", map[string]string{plat: "ELF"})
	zipPath := filepath.Join(t.TempDir(), "redis.zip")
	if _, _, err := Pack(ext, zipPath); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "gne")

	_, err := Install(context.Background(), zipPath, Options{Dir: dir, Available: func() bool { return false }})
	if err == nil || !strings.Contains(err.Error(), "CGO") {
		t.Errorf("non-CGO harus ditolak, dapat: %v", err)
	}
	if _, err := Install(context.Background(), zipPath, Options{
		Dir: dir, Force: true, Available: func() bool { return false },
	}); err != nil {
		t.Errorf("non-CGO + --force harus lolos, dapat: %v", err)
	}
}

// TestInstallTolakHTTP — URL http:// ditolak tanpa menyentuh jaringan.
func TestInstallTolakHTTP(t *testing.T) {
	dipanggil := false
	_, err := Install(context.Background(), "http://contoh.com/redis.zip", Options{
		Fetch: func(ctx context.Context, url string) ([]byte, error) {
			dipanggil = true
			return nil, fmt.Errorf("tidak boleh tercapai")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Errorf("http:// harus ditolak, dapat: %v", err)
	}
	if dipanggil {
		t.Error("Fetch tidak boleh dipanggil untuk http://")
	}
}

// TestInstallURLResmi — shortcut diselesaikan ke aset rilis GitHub.
func TestInstallURLResmi(t *testing.T) {
	var urlDipakai string
	_, err := Install(context.Background(), "redis@0.6.0", Options{
		Fetch: func(ctx context.Context, url string) ([]byte, error) {
			urlDipakai = url
			return nil, fmt.Errorf("dummy")
		},
	})
	if err == nil {
		t.Fatal("harus gagal (dummy Fetch), tapi bukan itu yang diuji")
	}
	want := "https://github.com/lnx645/galang/releases/download/v0.6.0/redis.zip"
	if urlDipakai != want {
		t.Errorf("URL = %q, mau %q", urlDipakai, want)
	}

	// Tanpa versi → rilis terbaru.
	_, _ = Install(context.Background(), "redis", Options{
		Fetch: func(ctx context.Context, url string) ([]byte, error) {
			urlDipakai = url
			return nil, fmt.Errorf("dummy")
		},
	})
	want = "https://github.com/lnx645/galang/releases/latest/download/redis.zip"
	if urlDipakai != want {
		t.Errorf("URL latest = %q, mau %q", urlDipakai, want)
	}
}

// TestPackTolakCampuran — build dengan nama/ekstensi salah platform
// ditolak sebelum zip terbentuk.
func TestPackTolakCampuran(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gne.json"),
		[]byte(`{"name":"redis","version":"0.6.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// linux-amd64 berisi .dll (salah platform).
	bedir := filepath.Join(dir, "build", "linux-amd64")
	if err := os.MkdirAll(bedir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bedir, "redis.dll"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Pack(dir, filepath.Join(t.TempDir(), "x.zip")); err == nil {
		t.Error("build dengan ekstensi salah platform harus ditolak")
	}
}

// TestPackTolakNamaSampah — nama/versi tidak valid di gne.json ditolak.
func TestPackTolakNamaSampah(t *testing.T) {
	cases := []struct{ nama, versi string }{
		{"Re-dis", "0.6.0"}, // huruf kapital + tanda hubung
		{"redis", "6-5"},    // versi bukan semver
	}
	for _, c := range cases {
		dir := t.TempDir()
		b, _ := json.Marshal(Source{Name: c.nama, Version: c.versi})
		os.WriteFile(filepath.Join(dir, "gne.json"), b, 0o644)
		d := filepath.Join(dir, "build", CurrentPlatform())
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "x"), []byte("y"), 0o644)
		if _, _, err := Pack(dir, filepath.Join(t.TempDir(), "x.zip")); err == nil {
			t.Errorf("pack name=%q versi=%q harus ditolak", c.nama, c.versi)
		}
	}
}

// TestBacaZipTolakTautanSimbolik — entri symlink ditolak.
func TestBacaZipTolakTautanSimbolik(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "redis.so", Method: zip.Deflate}
	hdr.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("/etc/passwd"))
	zw.Close()

	if _, err := gnepkg.ReadZip(buf.Bytes()); err == nil {
		t.Error("tautan simbolik harus ditolak")
	}
}

// TestURLResmi — bentuk URL kanal resmi.
func TestURLResmi(t *testing.T) {
	got, err := OfficialURL("redis")
	if err != nil || got != "https://github.com/lnx645/galang/releases/latest/download/redis.zip" {
		t.Errorf("OfficialURL(redis) = %q, %v", got, err)
	}
	got, err = OfficialURL("smtp@v1.2.3")
	if err != nil || got != "https://github.com/lnx645/galang/releases/download/v1.2.3/smtp.zip" {
		t.Errorf("OfficialURL(smtp@v1.2.3) = %q, %v", got, err)
	}
}

// TestParseShortcut — variasi spesifikasi kanal.
func TestParseShortcut(t *testing.T) {
	cases := []struct {
		spec string
		ok   bool
		name string
		ver  string
	}{
		{"redis", true, "redis", ""},
		{"redis@0.6.0", true, "redis", "0.6.0"},
		{"redis@v0.6.0", true, "redis", "0.6.0"},
		{"Redis", false, "", ""},
		{"my-ext", false, "", ""}, // tanda hubung tidak sah utk namespace
		{"redis@bukan", false, "", ""},
	}
	for _, c := range cases {
		name, ver, ok := parseShortcut(c.spec)
		if ok != c.ok || name != c.name || ver != c.ver {
			t.Errorf("parseShortcut(%q) = (%q,%q,%v), mau (%q,%q,%v)",
				c.spec, name, ver, ok, c.name, c.ver, c.ok)
		}
	}
}
