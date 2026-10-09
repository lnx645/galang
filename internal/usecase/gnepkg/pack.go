// Package gnepkg mengelola paket ekstensi GNE (manifest + alur pack,
// install, list, remove). Aturan bisnis ada di sini; mekanik arsip zip,
// hash, dan unduh HTTPS ada di internal/infra/gnepkg.
//
// Struktur paket (zip):
//
//	manifest.json                 — name, version, gne_abi, entry per platform
//	<GOOS-GOARCH>/<name><ext>     — binari native per platform
//
// Pemasangan FLAT: ~/.garurda/gne/<name><ext> + sidecar <name>.gne.json,
// persis pola yang dicari discovery `use "nama"` — tanpa ubahan loader.
package gnepkg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"garurda/internal/infra/gne"
	"garurda/internal/infra/gnepkg"
)

// RepoResmi adalah kanal distribusi ekstensi resmi: shortcut
// `gar gne install redis` diselesaikan ke aset rilis repo ini.
const RepoResmi = "https://github.com/lnx645/galang"

// NamaBerkas adalah berkas manifest di dalam paket dan sidecar terpasang.
const (
	NamaManifest  = "manifest.json"
	suffixSidecar = ".gne.json"
)

var (
	reNama     = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	reVersi    = regexp.MustCompile(`^\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`)
	rePlatform = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)
	reSHA256   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Entry adalah binari satu platform di dalam paket.
type Entry struct {
	File   string `json:"file"`   // path di dalam zip: "linux-amd64/redis.so"
	SHA256 string `json:"sha256"` // sidik jari isi binari
	Size   int64  `json:"size"`   // ukuran binari dalam byte
}

// Manifest adalah isi manifest.json dalam paket (dan sidecar terpasang).
type Manifest struct {
	Name        string           `json:"name"`
	Version     string           `json:"version"`
	GNEABI      int              `json:"gne_abi"`
	Description string           `json:"description,omitempty"`
	Entry       map[string]Entry `json:"entry"` // "GOOS-GOARCH" → Entry
}

// Sumber adalah metadata pengembang di ext/<nama>/gne.json (input pack).
type Sumber struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// ekstensiUntuk mengembalikan ekstensi pustaka wajib untuk sebuah GOOS
// sesuai pola discovery: linux .so, darwin .dylib, windows .dll.
func ekstensiUntuk(goos string) string {
	switch goos {
	case "windows":
		return ".dll"
	case "darwin":
		return ".dylib"
	default:
		return ".so"
	}
}

// pisahPlatform memecah "GOOS-GOARCH"; ok=false bila format salah.
func pisahPlatform(p string) (goos, goarch string, ok bool) {
	if !rePlatform.MatchString(p) {
		return "", "", false
	}
	i := strings.IndexByte(p, '-')
	return p[:i], p[i+1:], true
}

// validasiManifest memeriksa kontrak paket: nama aman untuk namespace,
// versi sah, ABI cocok dengan host, dan tiap entri menunjuk binari yang
// nama+ekstensinya sesuai platformnya (menolak paket silang/tampered).
func validasiManifest(m *Manifest) error {
	if !reNama.MatchString(m.Name) {
		return fmt.Errorf("nama paket %q tidak valid (huruf kecil, diawali huruf, tanpa tanda baca)", m.Name)
	}
	if !reVersi.MatchString(m.Version) {
		return fmt.Errorf("versi paket %q tidak valid (format semver)", m.Version)
	}
	if m.GNEABI != gne.ABI {
		return fmt.Errorf("paket dibuat untuk ABI GNE %d, host memakai ABI %d — tidak kompatibel",
			m.GNEABI, gne.ABI)
	}
	if len(m.Entry) == 0 {
		return fmt.Errorf("paket tidak memuat binari apa pun")
	}
	for plat, e := range m.Entry {
		goos, _, ok := pisahPlatform(plat)
		if !ok {
			return fmt.Errorf("platform %q tidak valid", plat)
		}
		want := plat + "/" + m.Name + ekstensiUntuk(goos)
		if e.File != want {
			return fmt.Errorf("entri %q menunjuk %q, seharusnya %q", plat, e.File, want)
		}
		if !reSHA256.MatchString(e.SHA256) {
			return fmt.Errorf("entri %q tanpa sha256 yang sah", plat)
		}
	}
	return nil
}

// daftarPlatform mengembalikan daftar platform terurut untuk pesan galat.
func daftarPlatform(m *Manifest) string {
	plats := make([]string, 0, len(m.Entry))
	for p := range m.Entry {
		plats = append(plats, p)
	}
	sort.Strings(plats)
	return strings.Join(plats, ", ")
}

// PlatformBerjalan adalah GOOS-GOARCH mesin sekarang.
func PlatformBerjalan() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// ---- sidecar ----

// bacaSidecar memuat <name>.gne.json dari direktori instalasi.
func bacaSidecar(dir, name string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, name+suffixSidecar))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("sidecar %s%s rusak: %v", name, suffixSidecar, err)
	}
	return &m, nil
}

// tulisSidecar menyimpan manifest terpasang agar `gar gne list` tidak
// perlu membaca ulang zip.
func tulisSidecar(dir string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return gnepkg.TulisBerkas(filepath.Join(dir, m.Name+suffixSidecar), append(b, '\n'))
}

// ---- direktori instalasi ----

// DirektoriInstal mengembalikan direktori instalasi default
// (~/.garurda/gne) — sama dengan kandidat discovery global `use`.
func DirektoriInstal() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("tidak dapat menentukan direktori home — berikan --dir secara eksplisit")
	}
	return filepath.Join(home, ".garurda", "gne"), nil
}

// ---- pack ----

// Pack membaca ext/<nama>/ (gne.json + build/<GOOS-GOARCH>/<nama><ext>)
// lalu menulis paket zip multi-platform. Mengembalikan path zip dan
// manifest yang dihasilkan.
func Pack(dir, keluaran string) (string, *Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "gne.json"))
	if err != nil {
		return "", nil, fmt.Errorf("baca gne.json: %v", err)
	}
	var s Sumber
	if err := json.Unmarshal(b, &s); err != nil {
		return "", nil, fmt.Errorf("gne.json tidak valid: %v", err)
	}
	m := &Manifest{
		Name:        s.Name,
		Version:     s.Version,
		GNEABI:      gne.ABI,
		Description: s.Description,
		Entry:       map[string]Entry{},
	}
	if !reNama.MatchString(m.Name) {
		return "", nil, fmt.Errorf("nama %q di gne.json tidak valid (huruf kecil, diawali huruf, tanpa tanda baca)", m.Name)
	}
	if !reVersi.MatchString(m.Version) {
		return "", nil, fmt.Errorf("versi %q di gne.json tidak valid (format semver)", m.Version)
	}

	// Pindai build/<platform>/ — setiap direktori harus berisi tepat
	// satu binari bernama <nama><ekstensi platform>.
	buildDir := filepath.Join(dir, "build")
	des, err := os.ReadDir(buildDir)
	if err != nil {
		return "", nil, fmt.Errorf("baca build/: %v (jalankan make ext dulu)", err)
	}
	var files []gnepkg.File
	manifestRaw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", nil, err
	}
	files = append(files, gnepkg.File{Name: NamaManifest, Data: append(manifestRaw, '\n')})

	nBuild := 0
	for _, de := range des {
		if !de.IsDir() {
			continue
		}
		plat := de.Name()
		goos, _, ok := pisahPlatform(plat)
		if !ok {
			return "", nil, fmt.Errorf("direktori build/%q bukan platform GOOS-GOARCH yang sah", plat)
		}
		berkas := filepath.Join(buildDir, plat, m.Name+ekstensiUntuk(goos))
		data, err := gnepkg.BacaBerkas(berkas, gnepkg.BatasUkuranPaket)
		if err != nil {
			return "", nil, fmt.Errorf("baca %s: %v", berkas, err)
		}
		rel := plat + "/" + m.Name + ekstensiUntuk(goos)
		m.Entry[plat] = Entry{
			File:   rel,
			SHA256: gnepkg.SHA256Hex(data),
			Size:   int64(len(data)),
		}
		files = append(files, gnepkg.File{Name: rel, Data: data})
		nBuild++
	}
	if nBuild == 0 {
		return "", nil, fmt.Errorf("build/ kosong — jalankan make ext dulu")
	}

	// Manifest ditulis ulang setelah entry terisi.
	if manifestRaw, err = json.MarshalIndent(m, "", "  "); err != nil {
		return "", nil, err
	}
	files[0].Data = append(manifestRaw, '\n')

	if keluaran == "" {
		keluaran = filepath.Join("dist", m.Name+".zip")
	}
	if err := gnepkg.TulisZip(keluaran, files); err != nil {
		return "", nil, err
	}
	return keluaran, m, nil
}
