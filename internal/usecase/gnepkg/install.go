package gnepkg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"garurda/internal/infra/gne"
	"garurda/internal/infra/gnepkg"
)

// Options mengatur perilaku Install.
type Options struct {
	// Dir adalah direktori instalasi; kosong = ~/.garurda/gne.
	Dir string
	// Force menimpa instalasi yang ada dan menembus penolakan
	// non-CGO / platform yang tidak tersedia.
	Force bool
	// Available memeriksa apakah binari gar ini memuat loader GNE
	// (default gne.Available); disuntik untuk menguji jalur non-CGO.
	Available func() bool
	// Fetch mengunduh paket dari URL (default gnepkg.FetchURL);
	// disuntik untuk menguji jalur URL tanpa jaringan.
	Fetch func(ctx context.Context, url string) ([]byte, error)
}

func (o *Options) setDefaults() {
	if o.Available == nil {
		o.Available = gne.Available
	}
	if o.Fetch == nil {
		o.Fetch = gnepkg.FetchURL
	}
}

// Result adalah laporan pemasangan yang sukses.
type Result struct {
	Name     string
	Version  string
	Platform string // platform binari yang benar-benar dipasang
	Path     string // berkas native hasil ekstrak
	Sidecar  string // <nama>.gne.json
	Previous bool   // ada instalasi lama yang ditimpa (butuh Force)
	Source   string // URL atau path asal paket
}

// parseShortcut memecah spesifikasi kanal jadi (nama, versi, ok).
// "redis" → ("redis", "", true); "redis@0.6.0" → ("redis", "0.6.0", true).
// Pemanggil wajib memeriksa ok sebelum memakai hasilnya (nama kosong
// berarti spesifikasi bukan shortcut).
func parseShortcut(spec string) (name, ver string, ok bool) {
	if i := strings.IndexByte(spec, '@'); i >= 0 {
		name, ver = spec[:i], spec[i+1:]
	} else {
		name = spec
	}
	if !reName.MatchString(name) {
		return "", "", false
	}
	if ver != "" {
		ver = strings.TrimPrefix(ver, "v")
		if !reVersion.MatchString(ver) {
			return "", "", false
		}
	}
	return name, ver, true
}

// resolveSource menentukan sumber paket dari spesifikasi user:
// URL HTTPS, path berkas lokal, atau shortcut kanal resmi ("redis@0.6.0").
// HTTP ditolak: paket adalah kode native dan tidak boleh turun ke plaintext.
func resolveSource(spec string) (source string, local bool, err error) {
	if strings.Contains(spec, "://") {
		if !strings.HasPrefix(spec, "https://") {
			return "", false, fmt.Errorf("hanya HTTPS yang diizinkan, dapat %q (paket adalah kode native)", spec)
		}
		return spec, false, nil
	}
	// Path berkas: mengandung pemisah atau berakhiran .zip.
	if strings.ContainsAny(spec, `/\`) || strings.HasSuffix(spec, ".zip") {
		if _, statErr := os.Stat(spec); statErr != nil {
			return "", true, fmt.Errorf("berkas paket %q tidak ditemukan", spec)
		}
		return spec, true, nil
	}
	if _, _, ok := parseShortcut(spec); ok {
		u, uerr := gnepkg.OfficialURL(OfficialRepo, spec)
		return u, false, uerr
	}
	return "", false, fmt.Errorf("%q bukan URL HTTPS, berkas .zip, atau nama ekstensi resmi", spec)
}

// OfficialURL membangun URL shortcut kanal resmi ("redis" → rilis terbaru;
// "redis@0.6.0" → rilis v0.6.0).
func OfficialURL(spec string) (string, error) {
	return gnepkg.OfficialURL(OfficialRepo, spec)
}

// Install memasang paket ekstensi dari spec (URL HTTPS / berkas lokal /
// shortcut kanal resmi) ke direktori instalasi.
func Install(ctx context.Context, spec string, o Options) (*Result, error) {
	o.setDefaults()

	source, local, err := resolveSource(spec)
	if err != nil {
		return nil, err
	}

	// 1. Baca isi paket (unduh HTTPS atau berkas lokal), dengan cap ukuran.
	var data []byte
	if local {
		data, err = gnepkg.ReadFile(source, gnepkg.MaxPackageSize)
	} else {
		data, err = o.Fetch(ctx, source)
	}
	if err != nil {
		return nil, fmt.Errorf("baca paket: %v", err)
	}

	// 2. Buka zip — seluruh entri divalidasi anti zip-slip/tautan simbolik.
	entries, err := gnepkg.ReadZip(data)
	if err != nil {
		return nil, fmt.Errorf("paket rusak: %v", err)
	}
	rawManifest, ok := entries[ManifestFile]
	if !ok {
		return nil, fmt.Errorf("paket tidak memuat %s", ManifestFile)
	}
	var m Manifest
	if err := json.Unmarshal(rawManifest, &m); err != nil {
		return nil, fmt.Errorf("%s tidak valid: %v", ManifestFile, err)
	}

	// 3. Kontrak paket: nama/versi/ABI/entri (termasuk kecocokan nama
	//    berkas dengan platformnya — menolak paket silang).
	if err := validateManifest(&m); err != nil {
		return nil, err
	}

	// 4. Binari tanpa CGO tidak akan bisa memuat ekstensi sama sekali.
	if !o.Available() && !o.Force {
		return nil, fmt.Errorf("binari gar ini dibangun tanpa CGO — GNE tidak aktif, pemasangan dibatalkan (pakai --force bila ingin tetap menyiapkan berkas)")
	}

	// 5. Pilih platform: milik mesin ini, atau paksa yang pertama bila
	//    --force (prakarsa direktori untuk mesin lain).
	platform := CurrentPlatform()
	if _, ada := m.Entry[platform]; !ada {
		if !o.Force {
			return nil, fmt.Errorf("paket tidak menyediakan binari untuk %s (tersedia: %s)", platform, joinPlatforms(&m))
		}
		platform = firstPlatform(&m)
	}

	e := m.Entry[platform]
	binary, ok := entries[e.File]
	if !ok {
		return nil, fmt.Errorf("paket tidak memuat berkas %q yang tercatat di manifest", e.File)
	}

	// 6. Verifikasi integritas SEBELUM ekstrak: sha256 + ukuran harus
	//    persis seperti yang dijanjikan manifest.
	if int64(len(binary)) != e.Size {
		return nil, fmt.Errorf("ukuran %s tidak cocok manifest (dapat %d, harap %d)", e.File, len(binary), e.Size)
	}
	if got := gnepkg.SHA256Hex(binary); got != e.SHA256 {
		return nil, fmt.Errorf("sha256 %s tidak cocok manifest (dapat %s, harap %s)", e.File, got, e.SHA256)
	}

	// 7. Target FLAT: <dir>/<nama><ekstensi platform terpasang>.
	goos, _, _ := splitPlatform(platform)
	target := filepath.Join(o.dir(), m.Name+libExtFor(goos))
	sidecar := filepath.Join(o.dir(), m.Name+suffixSidecar)

	existed := false
	if _, err := os.Stat(target); err == nil {
		existed = true
	}
	if _, err := os.Stat(sidecar); err == nil {
		existed = true
	}
	if existed && !o.Force {
		return nil, fmt.Errorf("%s sudah terpasang di %s (pakai --force untuk menimpa)", m.Name, o.dir())
	}

	if err := os.MkdirAll(o.dir(), 0o755); err != nil {
		return nil, fmt.Errorf("buat direktori instalasi: %v", err)
	}

	// 8. Ekstrak, lalu VERIFIKASI ULANG sha256 dari berkas yang sudah
	//    tertulis di disk (mendeteksi kegagalan tulis/rotasi di tengah).
	if err := gnepkg.WriteFile(target, binary); err != nil {
		return nil, fmt.Errorf("tulis binari: %v", err)
	}
	diskData, err := gnepkg.ReadFile(target, gnepkg.MaxPackageSize)
	if err != nil {
		os.Remove(target)
		return nil, fmt.Errorf("verifikasi pasca-ekstrak gagal: %v", err)
	}
	if got := gnepkg.SHA256Hex(diskData); got != e.SHA256 {
		os.Remove(target)
		return nil, fmt.Errorf("sha256 berkas terpasang tidak cocok (%s) — berkas dibuang", got)
	}

	if err := writeSidecar(o.dir(), &m); err != nil {
		os.Remove(target)
		return nil, fmt.Errorf("tulis sidecar: %v", err)
	}

	return &Result{
		Name:     m.Name,
		Version:  m.Version,
		Platform: platform,
		Path:     target,
		Sidecar:  sidecar,
		Previous: existed,
		Source:   source,
	}, nil
}

func (o Options) dir() string {
	if o.Dir != "" {
		return o.Dir
	}
	d, err := InstallDir()
	if err != nil {
		return "" // Install akan menemukan galat saat MkdirAll
	}
	return d
}

// firstPlatform mengembalikan platform terurut pertama — pilihan
// deterministik untuk --force lintas-platform.
func firstPlatform(m *Manifest) string {
	plats := make([]string, 0, len(m.Entry))
	for p := range m.Entry {
		plats = append(plats, p)
	}
	sort.Strings(plats)
	return plats[0]
}

// ---- list & remove ----

// Installed adalah satu entri hasil `gar gne list`.
type Installed struct {
	Name     string
	Version  string
	ABI      int
	Platform []string
	Path     string
	Size     int64
}

// List membaca semua sidecar di direktori instalasi.
func List(dir string) ([]Installed, error) {
	if dir == "" {
		d, err := InstallDir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // belum ada instalasi sama sekali
		}
		return nil, err
	}
	var out []Installed
	for _, de := range ents {
		if de.IsDir() || !strings.HasSuffix(de.Name(), suffixSidecar) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, de.Name()))
		if err != nil {
			continue
		}
		var m Manifest
		if err := json.Unmarshal(b, &m); err != nil {
			continue // sidecar rusak dilewati, bukan menggagalkan seluruh list
		}
		t := Installed{Name: m.Name, Version: m.Version, ABI: m.GNEABI}
		for p := range m.Entry {
			t.Platform = append(t.Platform, p)
		}
		sort.Strings(t.Platform)
		if st, err := os.Stat(filepath.Join(dir, m.Name+libExtFor(firstPlatformGOOS(&m)))); err == nil {
			t.Size = st.Size()
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// firstPlatformGOOS memilih GOOS platform pertama untuk membaca ukuran
// berkas terpasang (hanya kosmetik untuk list).
func firstPlatformGOOS(m *Manifest) string {
	p := firstPlatform(m)
	goos, _, _ := splitPlatform(p)
	return goos
}

// Remove melepas ekstensi beserta sidecar-nya.
func Remove(dir, name string) (string, error) {
	if !reName.MatchString(name) {
		return "", fmt.Errorf("nama %q tidak valid", name)
	}
	if dir == "" {
		d, err := InstallDir()
		if err != nil {
			return "", err
		}
		dir = d
	}
	sidecar := filepath.Join(dir, name+suffixSidecar)
	if _, err := os.Stat(sidecar); err != nil {
		return "", fmt.Errorf("%s tidak terpasang di %s", name, dir)
	}
	var removed []string
	// Hapus semua varian ekstensi native (binari platform terpasang).
	for _, ext := range []string{".so", ".dylib", ".dll"} {
		p := filepath.Join(dir, name+ext)
		if _, err := os.Stat(p); err == nil {
			if err := os.Remove(p); err != nil {
				return "", fmt.Errorf("hapus %s: %v", p, err)
			}
			removed = append(removed, p)
		}
	}
	if err := os.Remove(sidecar); err != nil {
		return "", fmt.Errorf("hapus sidecar: %v", err)
	}
	removed = append(removed, sidecar)
	return strings.Join(removed, ", "), nil
}
