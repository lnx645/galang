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

	"galang/internal/infra/gne"
	"galang/internal/infra/gnepkg"
)

// bangunFakeExt builds a fake ext/redis structure: gne.json + build/<plat>.
func bangunFakeExt(t *testing.T, name, versi string, binari map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	src, err := json.Marshal(Source{Name: name, Version: versi, Description: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gne.json"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	for plat, isi := range binari {
		goos, _, ok := splitPlatform(plat)
		if !ok {
			t.Fatalf("invalid fake platform: %s", plat)
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

// TestPackInstallListRemove — full round-trip: pack → install (local file)
// → list → remove, plus verification of the installed file's contents.
func TestPackInstallListRemove(t *testing.T) {
	plat := CurrentPlatform()
	// darwin-amd64 is included on purpose: the first sorted platform in the
	// manifest, even though it does not belong to the test machine — List
	// must still report its size.
	ext := bangunFakeExt(t, "redis", "0.6.0", map[string]string{
		plat:            "ELF-fake-redis",
		"darwin-amd64":  "Mach-fake",
		"windows-amd64": "PE-fake",
	})
	zipPath := filepath.Join(t.TempDir(), "redis.zip")
	gotZip, m, err := Pack(ext, zipPath)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if gotZip != zipPath {
		t.Errorf("Pack = %q, want %q", gotZip, zipPath)
	}
	if m.Name != "redis" || m.Version != "0.6.0" || m.GNEABI != gne.ABI {
		t.Errorf("wrong manifest: %+v", m)
	}
	if _, ok := m.Entry[plat]; !ok {
		t.Fatalf("entry for platform %s missing: %v", plat, m.Entry)
	}

	dir := filepath.Join(t.TempDir(), "gne")
	res, err := Install(context.Background(), zipPath, Options{Dir: dir})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.Platform != plat {
		t.Errorf("installed platform %q, want %q", res.Platform, plat)
	}
	// File contents are exactly the binary (and the sha matches — verified twice).
	isi, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read installed file: %v", err)
	}
	if string(isi) != "ELF-fake-redis" {
		t.Errorf("file contents = %q", isi)
	}
	if filepath.Base(res.Path) != "redis"+libExtFor(runtime.GOOS) {
		t.Errorf("installed file name %q does not match the discovery pattern", filepath.Base(res.Path))
	}
	if _, err := os.Stat(filepath.Join(dir, "redis.gne.json")); err != nil {
		t.Errorf("sidecar missing: %v", err)
	}

	// Reinstall without --force must be rejected.
	if _, err := Install(context.Background(), zipPath, Options{Dir: dir}); err == nil {
		t.Error("reinstall without --force must be rejected")
	}
	// With --force it is overwritten.
	res2, err := Install(context.Background(), zipPath, Options{Dir: dir, Force: true})
	if err != nil {
		t.Fatalf("install --force: %v", err)
	}
	if !res2.Previous {
		t.Error("Result.Previous must be true when overwriting")
	}

	// List shows a single entry.
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
	if items[0].Size != int64(len("ELF-fake-redis")) {
		t.Errorf("list Size = %d, want %d (the installed binary must be readable)",
			items[0].Size, len("ELF-fake-redis"))
	}

	// Remove cleans up the file + sidecar.
	dihapus, err := Remove(dir, "redis")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(dihapus, "redis") {
		t.Errorf("odd remove report: %s", dihapus)
	}
	if _, err := os.Stat(filepath.Join(dir, "redis.gne.json")); !os.IsNotExist(err) {
		t.Error("sidecar still present after remove")
	}
	if _, err := List(dir); err != nil {
		t.Errorf("list after remove: %v", err)
	}
	// The second Remove must fail (no longer installed).
	if _, err := Remove(dir, "redis"); err == nil {
		t.Error("second remove must be rejected")
	}
}

// TestListRemoveLegacyDir — v0.7.0 rebrand: without --dir, new installs
// write to ~/.galang/gne, while extensions in the legacy ~/.garurda/gne
// location remain readable by List and removable by Remove.
func TestListRemoveLegacyDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	baru := filepath.Join(home, ".galang", "gne")
	lama := filepath.Join(home, ".garurda", "gne")

	ext := bangunFakeExt(t, "redis", "0.6.0", map[string]string{
		CurrentPlatform(): "ELF-fake-redis",
	})
	zipPath := filepath.Join(t.TempDir(), "redis.zip")
	if _, _, err := Pack(ext, zipPath); err != nil {
		t.Fatalf("pack: %v", err)
	}

	// A new install writes to the active location.
	res, err := Install(context.Background(), zipPath, Options{})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if filepath.Dir(res.Path) != baru {
		t.Fatalf("installed in %q, want the new location %q", filepath.Dir(res.Path), baru)
	}

	// Move the install to the legacy location (a pre-v0.7.0 machine state).
	if err := os.MkdirAll(lama, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"redis" + libExtFor(runtime.GOOS), "redis" + suffixSidecar} {
		if err := os.Rename(filepath.Join(baru, f), filepath.Join(lama, f)); err != nil {
			t.Fatalf("move %s: %v", f, err)
		}
	}

	// List without --dir still finds the extension in the legacy location.
	items, err := List("")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Name != "redis" {
		t.Fatalf("list = %+v, want the legacy-location redis", items)
	}

	// Remove without --dir deletes it from the legacy location.
	if _, err := Remove("", "redis"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	for _, f := range []string{"redis" + libExtFor(runtime.GOOS), "redis" + suffixSidecar} {
		if _, err := os.Stat(filepath.Join(lama, f)); !os.IsNotExist(err) {
			t.Errorf("%s still present after remove", f)
		}
	}
	// The second Remove must be rejected (no longer installed anywhere).
	if _, err := Remove("", "redis"); err == nil {
		t.Error("second remove must be rejected")
	}
}

// bacaZipBalik turns a map of entries into a valid zip (used to build
// naughty packages that pass reading but break other rules).
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

// instalDariZip installs raw package bytes through a temporary file.
func instalDariZip(t *testing.T, data []byte, o Options) error {
	t.Helper()
	if o.Dir == "" {
		o.Dir = filepath.Join(t.TempDir(), "gne")
	}
	p := filepath.Join(t.TempDir(), "package.zip")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Install(context.Background(), p, o)
	return err
}

// manifestNakal builds a manifest.json with an entry for the test platform.
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

// TestInstallRejectZipSlip — a `..` entry inside the zip is rejected at
// read time, even when the manifest itself looks valid (layered defense).
func TestInstallRejectZipSlip(t *testing.T) {
	plat := CurrentPlatform()
	man := manifestNakal(t, nil) // valid manifest — the malicious entry is extra
	paket := bacaZipBalik(t, map[string][]byte{
		ManifestFile: man,
		plat + "/redis" + libExtFor(runtime.GOOS): []byte("ELF"),
		"../redis" + libExtFor(runtime.GOOS):      []byte("evil"),
	})
	err := instalDariZip(t, paket, Options{})
	if err == nil || !strings.Contains(err.Error(), "zip-slip") {
		t.Errorf("zip-slip must be rejected, got: %v", err)
	}
}

// TestInstallRejectAbsolutePath — absolute entries are rejected.
func TestInstallRejectAbsolutePath(t *testing.T) {
	man := manifestNakal(t, nil)
	paket := bacaZipBalik(t, map[string][]byte{
		ManifestFile: man,
		"/etc/evil":  []byte("x"),
	})
	err := instalDariZip(t, paket, Options{})
	if err == nil || !strings.Contains(err.Error(), "absolut") {
		t.Errorf("absolute path must be rejected, got: %v", err)
	}
}

// TestInstallRejectShaMismatch — a binary that does not match the manifest
// is rejected before anything is written to disk.
func TestInstallRejectShaMismatch(t *testing.T) {
	plat := CurrentPlatform()
	man := manifestNakal(t, nil) // sha = aaa... (not the sha of "ELF")
	paket := bacaZipBalik(t, map[string][]byte{
		ManifestFile: man,
		plat + "/redis" + libExtFor(runtime.GOOS): []byte("ELF"),
	})
	dir := filepath.Join(t.TempDir(), "gne")
	err := instalDariZip(t, paket, Options{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("mismatched sha must be rejected, got: %v", err)
	}
	// Nothing must have been written.
	if des, _ := os.ReadDir(dir); len(des) != 0 {
		t.Errorf("target directory must be empty, contains %d", len(des))
	}
}

// TestInstallRejectNewerABI — a package built for a newer ABI than this
// build is rejected (older ABIs remain compatible).
func TestInstallRejectNewerABI(t *testing.T) {
	plat := CurrentPlatform()
	man := manifestNakal(t, func(m *Manifest) { m.GNEABI = gne.ABI + 1 })
	paket := bacaZipBalik(t, map[string][]byte{
		ManifestFile: man,
		plat + "/redis" + libExtFor(runtime.GOOS): []byte("ELF"),
	})
	err := instalDariZip(t, paket, Options{})
	if err == nil || !strings.Contains(err.Error(), "ABI") {
		t.Errorf("newer ABI must be rejected, got: %v", err)
	}
}

// TestInstallRejectMissingPlatform — the machine's platform is missing
// from the package: rejected; --force picks the first platform (cross-machine bootstrap).
func TestInstallRejectMissingPlatform(t *testing.T) {
	// Package built exclusively for another platform.
	other := "linux-arm64"
	if other == CurrentPlatform() {
		other = "windows-amd64"
	}
	ext := bangunFakeExt(t, "smtp", "0.6.0", map[string]string{other: "BINARY"})
	zipPath := filepath.Join(t.TempDir(), "smtp.zip")
	if _, _, err := Pack(ext, zipPath); err != nil {
		t.Fatalf("pack: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "gne")

	_, err := Install(context.Background(), zipPath, Options{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "does not provide a binary") {
		t.Errorf("missing platform must be rejected, got: %v", err)
	}

	res, err := Install(context.Background(), zipPath, Options{Dir: dir, Force: true})
	if err != nil {
		t.Errorf("cross-platform install --force: %v", err)
	} else if res.Platform != other {
		t.Errorf("installed platform %q, want %q", res.Platform, other)
	}
}

// TestInstallRejectWithoutCGO — a stub binary rejects installation without --force.
func TestInstallRejectWithoutCGO(t *testing.T) {
	plat := CurrentPlatform()
	ext := bangunFakeExt(t, "redis", "0.6.0", map[string]string{plat: "ELF"})
	zipPath := filepath.Join(t.TempDir(), "redis.zip")
	if _, _, err := Pack(ext, zipPath); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "gne")

	_, err := Install(context.Background(), zipPath, Options{Dir: dir, Available: func() bool { return false }})
	if err == nil || !strings.Contains(err.Error(), "CGO") {
		t.Errorf("non-CGO must be rejected, got: %v", err)
	}
	if _, err := Install(context.Background(), zipPath, Options{
		Dir: dir, Force: true, Available: func() bool { return false },
	}); err != nil {
		t.Errorf("non-CGO + --force must pass, got: %v", err)
	}
}

// TestInstallRejectHTTP — an http:// URL is rejected without touching the network.
func TestInstallRejectHTTP(t *testing.T) {
	dipanggil := false
	_, err := Install(context.Background(), "http://example.com/redis.zip", Options{
		Fetch: func(ctx context.Context, url string) ([]byte, error) {
			dipanggil = true
			return nil, fmt.Errorf("must not be reached")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Errorf("http:// must be rejected, got: %v", err)
	}
	if dipanggil {
		t.Error("Fetch must not be called for http://")
	}
}

// TestInstallOfficialURL — the shortcut resolves to a GitHub release asset.
func TestInstallOfficialURL(t *testing.T) {
	var urlDipakai string
	_, err := Install(context.Background(), "redis@0.6.0", Options{
		Fetch: func(ctx context.Context, url string) ([]byte, error) {
			urlDipakai = url
			return nil, fmt.Errorf("dummy")
		},
	})
	if err == nil {
		t.Fatal("it must fail (dummy Fetch), but that is not what is being tested")
	}
	want := "https://github.com/lnx645/galang/releases/download/v0.6.0/redis.zip"
	if urlDipakai != want {
		t.Errorf("URL = %q, want %q", urlDipakai, want)
	}

	// Without a version → the latest release.
	_, _ = Install(context.Background(), "redis", Options{
		Fetch: func(ctx context.Context, url string) ([]byte, error) {
			urlDipakai = url
			return nil, fmt.Errorf("dummy")
		},
	})
	want = "https://github.com/lnx645/galang/releases/latest/download/redis.zip"
	if urlDipakai != want {
		t.Errorf("latest URL = %q, want %q", urlDipakai, want)
	}
}

// TestPackRejectMixedPlatform — a build whose name/extension belongs to the
// wrong platform is rejected before the zip is produced.
func TestPackRejectMixedPlatform(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gne.json"),
		[]byte(`{"name":"redis","version":"0.6.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// linux-amd64 contains a .dll (wrong platform).
	bedir := filepath.Join(dir, "build", "linux-amd64")
	if err := os.MkdirAll(bedir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bedir, "redis.dll"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Pack(dir, filepath.Join(t.TempDir(), "x.zip")); err == nil {
		t.Error("build with a wrong-platform extension must be rejected")
	}
}

// TestPackRejectInvalidName — an invalid name/version in gne.json is rejected.
func TestPackRejectInvalidName(t *testing.T) {
	cases := []struct{ nama, versi string }{
		{"Re-dis", "0.6.0"}, // uppercase + hyphen
		{"redis", "6-5"},    // version is not semver
	}
	for _, c := range cases {
		dir := t.TempDir()
		b, _ := json.Marshal(Source{Name: c.nama, Version: c.versi})
		os.WriteFile(filepath.Join(dir, "gne.json"), b, 0o644)
		d := filepath.Join(dir, "build", CurrentPlatform())
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "x"), []byte("y"), 0o644)
		if _, _, err := Pack(dir, filepath.Join(t.TempDir(), "x.zip")); err == nil {
			t.Errorf("pack name=%q version=%q must be rejected", c.nama, c.versi)
		}
	}
}

// TestReadZipRejectSymlink — a symlink entry is rejected.
func TestReadZipRejectSymlink(t *testing.T) {
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
		t.Error("symlink must be rejected")
	}
}

// TestOfficialURL — the shape of official-channel URLs.
func TestOfficialURL(t *testing.T) {
	got, err := OfficialURL("redis")
	if err != nil || got != "https://github.com/lnx645/galang/releases/latest/download/redis.zip" {
		t.Errorf("OfficialURL(redis) = %q, %v", got, err)
	}
	got, err = OfficialURL("smtp@v1.2.3")
	if err != nil || got != "https://github.com/lnx645/galang/releases/download/v1.2.3/smtp.zip" {
		t.Errorf("OfficialURL(smtp@v1.2.3) = %q, %v", got, err)
	}
}

// TestParseShortcut — channel spec variations.
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
		{"my-ext", false, "", ""}, // hyphen not valid for a namespace
		{"redis@6-5", false, "", ""},
	}
	for _, c := range cases {
		name, ver, ok := parseShortcut(c.spec)
		if ok != c.ok || name != c.name || ver != c.ver {
			t.Errorf("parseShortcut(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.spec, name, ver, ok, c.name, c.ver, c.ok)
		}
	}
}
