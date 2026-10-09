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

// Opsi mengatur perilaku Install.
type Opsi struct {
	// Dir adalah direktori instalasi; kosong = ~/.garurda/gne.
	Dir string
	// Force menimpa instalasi yang ada dan menembus penolakan
	// non-CGO / platform yang tidak tersedia.
	Force bool
	// Available memeriksa apakah binari gar ini memuat loader GNE
	// (default gne.Available); disuntik untuk menguji jalur non-CGO.
	Available func() bool
	// Ambil mengunduh paket dari URL (default gnepkg.AmbilURL);
	// disuntik untuk menguji jalur URL tanpa jaringan.
	Ambil func(ctx context.Context, url string) ([]byte, error)
}

func (o *Opsi) isiDefault() {
	if o.Available == nil {
		o.Available = gne.Available
	}
	if o.Ambil == nil {
		o.Ambil = gnepkg.AmbilURL
	}
}

// Hasil adalah laporan pemasangan yang sukses.
type Hasil struct {
	Nama       string
	Versi      string
	Platform   string // platform binari yang benar-benar dipasang
	Path       string // berkas native hasil ekstrak
	Sidecar    string // <nama>.gne.json
	Sebelumnya bool   // ada instalasi lama yang ditimpa (butuh Force)
	Sumber     string // URL atau path asal paket
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
	if !reNama.MatchString(name) {
		return "", "", false
	}
	if ver != "" {
		ver = strings.TrimPrefix(ver, "v")
		if !reVersi.MatchString(ver) {
			return "", "", false
		}
	}
	return name, ver, true
}

// selesaiSumber menentukan sumber paket dari spesifikasi user:
// URL HTTPS, path berkas lokal, atau shortcut kanal resmi ("redis@0.6.0").
// HTTP ditolak: paket adalah kode native dan tidak boleh turun ke plaintext.
func selesaiSumber(spec string) (sumber string, lokal bool, err error) {
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
		u, uerr := gnepkg.URLResmi(RepoResmi, spec)
		return u, false, uerr
	}
	return "", false, fmt.Errorf("%q bukan URL HTTPS, berkas .zip, atau nama ekstensi resmi", spec)
}

// URLResmi membangun URL shortcut kanal resmi ("redis" → rilis terbaru;
// "redis@0.6.0" → rilis v0.6.0).
func URLResmi(spec string) (string, error) {
	return gnepkg.URLResmi(RepoResmi, spec)
}

// Install memasang paket ekstensi dari spec (URL HTTPS / berkas lokal /
// shortcut kanal resmi) ke direktori instalasi.
func Install(ctx context.Context, spec string, o Opsi) (*Hasil, error) {
	o.isiDefault()

	sumber, lokal, err := selesaiSumber(spec)
	if err != nil {
		return nil, err
	}

	// 1. Baca isi paket (unduh HTTPS atau berkas lokal), dengan cap ukuran.
	var data []byte
	if lokal {
		data, err = gnepkg.BacaBerkas(sumber, gnepkg.BatasUkuranPaket)
	} else {
		data, err = o.Ambil(ctx, sumber)
	}
	if err != nil {
		return nil, fmt.Errorf("baca paket: %v", err)
	}

	// 2. Buka zip — seluruh entri divalidasi anti zip-slip/tautan simbolik.
	entries, err := gnepkg.BacaZip(data)
	if err != nil {
		return nil, fmt.Errorf("paket rusak: %v", err)
	}
	rawManifest, ok := entries[NamaManifest]
	if !ok {
		return nil, fmt.Errorf("paket tidak memuat %s", NamaManifest)
	}
	var m Manifest
	if err := json.Unmarshal(rawManifest, &m); err != nil {
		return nil, fmt.Errorf("%s tidak valid: %v", NamaManifest, err)
	}

	// 3. Kontrak paket: nama/versi/ABI/entri (termasuk kecocokan nama
	//    berkas dengan platformnya — menolak paket silang).
	if err := validasiManifest(&m); err != nil {
		return nil, err
	}

	// 4. Binari tanpa CGO tidak akan bisa memuat ekstensi sama sekali.
	if !o.Available() && !o.Force {
		return nil, fmt.Errorf("binari gar ini dibangun tanpa CGO — GNE tidak aktif, pemasangan dibatalkan (pakai --force bila ingin tetap menyiapkan berkas)")
	}

	// 5. Pilih platform: milik mesin ini, atau paksa yang pertama bila
	//    --force (prakarsa direktori untuk mesin lain).
	plat := PlatformBerjalan()
	if _, ada := m.Entry[plat]; !ada {
		if !o.Force {
			return nil, fmt.Errorf("paket tidak menyediakan binari untuk %s (tersedia: %s)", plat, daftarPlatform(&m))
		}
		plat = daftarPlatformPertama(&m)
	}

	e := m.Entry[plat]
	binari, ok := entries[e.File]
	if !ok {
		return nil, fmt.Errorf("paket tidak memuat berkas %q yang tercatat di manifest", e.File)
	}

	// 6. Verifikasi integritas SEBELUM ekstrak: sha256 + ukuran harus
	//    persis seperti yang dijanjikan manifest.
	if int64(len(binari)) != e.Size {
		return nil, fmt.Errorf("ukuran %s tidak cocok manifest (dapat %d, harap %d)", e.File, len(binari), e.Size)
	}
	if got := gnepkg.SHA256Hex(binari); got != e.SHA256 {
		return nil, fmt.Errorf("sha256 %s tidak cocok manifest (dapat %s, harap %s)", e.File, got, e.SHA256)
	}

	// 7. Target FLAT: <dir>/<nama><ekstensi platform terpasang>.
	goos, _, _ := pisahPlatform(plat)
	target := filepath.Join(o.dir(), m.Name+ekstensiUntuk(goos))
	sidecar := filepath.Join(o.dir(), m.Name+suffixSidecar)

	sebelumnya := false
	if _, err := os.Stat(target); err == nil {
		sebelumnya = true
	}
	if _, err := os.Stat(sidecar); err == nil {
		sebelumnya = true
	}
	if sebelumnya && !o.Force {
		return nil, fmt.Errorf("%s sudah terpasang di %s (pakai --force untuk menimpa)", m.Name, o.dir())
	}

	if err := os.MkdirAll(o.dir(), 0o755); err != nil {
		return nil, fmt.Errorf("buat direktori instalasi: %v", err)
	}

	// 8. Ekstrak, lalu VERIFIKASI ULANG sha256 dari berkas yang sudah
	//    tertulis di disk (mendeteksi kegagalan tulis/rotasi di tengah).
	if err := gnepkg.TulisBerkas(target, binari); err != nil {
		return nil, fmt.Errorf("tulis binari: %v", err)
	}
	ulang, err := gnepkg.BacaBerkas(target, gnepkg.BatasUkuranPaket)
	if err != nil {
		os.Remove(target)
		return nil, fmt.Errorf("verifikasi pasca-ekstrak gagal: %v", err)
	}
	if got := gnepkg.SHA256Hex(ulang); got != e.SHA256 {
		os.Remove(target)
		return nil, fmt.Errorf("sha256 berkas terpasang tidak cocok (%s) — berkas dibuang", got)
	}

	if err := tulisSidecar(o.dir(), &m); err != nil {
		os.Remove(target)
		return nil, fmt.Errorf("tulis sidecar: %v", err)
	}

	return &Hasil{
		Nama:       m.Name,
		Versi:      m.Version,
		Platform:   plat,
		Path:       target,
		Sidecar:    sidecar,
		Sebelumnya: sebelumnya,
		Sumber:     sumber,
	}, nil
}

func (o Opsi) dir() string {
	if o.Dir != "" {
		return o.Dir
	}
	d, err := DirektoriInstal()
	if err != nil {
		return "" // Install akan menemukan galat saat MkdirAll
	}
	return d
}

// daftarPlatformPertama mengembalikan platform terurut pertama — pilihan
// deterministik untuk --force lintas-platform.
func daftarPlatformPertama(m *Manifest) string {
	plats := make([]string, 0, len(m.Entry))
	for p := range m.Entry {
		plats = append(plats, p)
	}
	sort.Strings(plats)
	return plats[0]
}

// ---- list & remove ----

// Terpasang adalah satu entri hasil `gar gne list`.
type Terpasang struct {
	Nama     string
	Versi    string
	ABI      int
	Platform []string
	Path     string
	Ukuran   int64
}

// List membaca semua sidecar di direktori instalasi.
func List(dir string) ([]Terpasang, error) {
	if dir == "" {
		d, err := DirektoriInstal()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // belum ada instalasi sama sekali
		}
		return nil, err
	}
	var out []Terpasang
	for _, de := range des {
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
		t := Terpasang{Nama: m.Name, Versi: m.Version, ABI: m.GNEABI}
		for p := range m.Entry {
			t.Platform = append(t.Platform, p)
		}
		sort.Strings(t.Platform)
		if st, err := os.Stat(filepath.Join(dir, m.Name+ekstensiUntuk(platformGoosPertama(&m)))); err == nil {
			t.Ukuran = st.Size()
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nama < out[j].Nama })
	return out, nil
}

// platformGoosPertama memilih GOOS platform pertama untuk membaca ukuran
// berkas terpasang (hanya kosmetik untuk list).
func platformGoosPertama(m *Manifest) string {
	p := daftarPlatformPertama(m)
	goos, _, _ := pisahPlatform(p)
	return goos
}

// Remove melepas ekstensi beserta sidecar-nya.
func Remove(dir, name string) (string, error) {
	if !reNama.MatchString(name) {
		return "", fmt.Errorf("nama %q tidak valid", name)
	}
	if dir == "" {
		d, err := DirektoriInstal()
		if err != nil {
			return "", err
		}
		dir = d
	}
	sidecar := filepath.Join(dir, name+suffixSidecar)
	if _, err := os.Stat(sidecar); err != nil {
		return "", fmt.Errorf("%s tidak terpasang di %s", name, dir)
	}
	var dihapus []string
	// Hapus semua varian ekstensi native (binari platform terpasang).
	for _, ext := range []string{".so", ".dylib", ".dll"} {
		p := filepath.Join(dir, name+ext)
		if _, err := os.Stat(p); err == nil {
			if err := os.Remove(p); err != nil {
				return "", fmt.Errorf("hapus %s: %v", p, err)
			}
			dihapus = append(dihapus, p)
		}
	}
	if err := os.Remove(sidecar); err != nil {
		return "", fmt.Errorf("hapus sidecar: %v", err)
	}
	dihapus = append(dihapus, sidecar)
	return strings.Join(dihapus, ", "), nil
}
