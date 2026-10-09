// Package gnepkg adalah infrastruktur pemaketan ekstensi GNE: arsip zip
// aman (anti zip-slip), hash SHA-256, baca/tulis berkas, dan pengunduhan
// HTTPS. Mekanik murni — aturan bisnis (manifest, validasi platform,
// alur install) ada di internal/usecase/gnepkg.
package gnepkg

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// SHA256Hex mengembalikan sidik jari isi berkas dalam heksadesimal.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// validateEntryName menolak path berbahaya sebelum disentuh: absolut,
// melewati induk (..), pemisah Windows di zip, atau bidang drive.
func validateEntryName(name string) error {
	if name == "" {
		return fmt.Errorf("entri zip tanpa nama")
	}
	if strings.Contains(name, "\\") {
		return fmt.Errorf("entri zip %q memakai pemisah Windows", name)
	}
	if strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return fmt.Errorf("entri zip %q bersifat absolut", name)
	}
	clean := path.Clean(name)
	if clean != name || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("entri zip %q menembus direktori induk (zip-slip)", name)
	}
	return nil
}

// ReadZip memecah isi zip menjadi peta nama → data. Seluruh entri divalidasi
// lebih dulu (zip-slip, tautan simbolik, duplikat) sehingga alur install
// tidak pernah mengekstrak berkas yang path-nya tidak aman.
func ReadZip(data []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("bukan zip yang valid: %v", err)
	}
	out := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		if err := validateEntryName(f.Name); err != nil {
			return nil, err
		}
		// Tautan simbolik/pipa dilarang: paket resmi hanya berisi berkas biasa.
		if f.Mode()&os.ModeSymlink != 0 || !f.Mode().IsRegular() {
			return nil, fmt.Errorf("entri zip %q bukan berkas biasa (ditolak)", f.Name)
		}
		if _, dup := out[f.Name]; dup {
			return nil, fmt.Errorf("entri zip %q ganda", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("buka entri %q: %v", f.Name, err)
		}
		// Batas per entri mencegah zip-bom; paket ekstensi jauh di bawah ini.
		b, err := io.ReadAll(io.LimitReader(rc, maxEntrySize+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("baca entri %q: %v", f.Name, err)
		}
		if int64(len(b)) > maxEntrySize {
			return nil, fmt.Errorf("entri %q melebihi batas %d byte", f.Name, maxEntrySize)
		}
		out[f.Name] = b
	}
	return out, nil
}

// maxEntrySize membatasi ukuran satu entri zip (anti zip-bom).
const maxEntrySize = 256 << 20 // 256 MiB

// packTimestamp adalah timestamp tetap untuk semua entri hasil pack:
// zip yang sama dihasilkan dari sumber yang sama (reproducible).
var packTimestamp = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// WriteZip menulis daftar berkas ke zip pada path, dengan timestamp tetap
// supaya hasil pack reproducible (sidik jari paket stabil antar build).
func WriteZip(dst string, files []File) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("buat direktori output: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		if err := validateEntryName(f.Name); err != nil {
			zw.Close()
			return err
		}
		hdr := &zip.FileHeader{Name: f.Name, Method: zip.Deflate}
		hdr.Modified = packTimestamp // waktu tetap → zip reproducible
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			zw.Close()
			return fmt.Errorf("tulis entri %q: %v", f.Name, err)
		}
		if _, err := w.Write(f.Data); err != nil {
			zw.Close()
			return fmt.Errorf("isi entri %q: %v", f.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("tutup zip: %v", err)
	}
	return WriteFile(dst, buf.Bytes())
}

// File adalah satu entri zip yang akan ditulis.
type File struct {
	Name string
	Data []byte
}

// WriteFile menulis data ke path secara atomik (file sementara + rename)
// supaya kegagalan di tengah tidak meninggak setengah berkas.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("buat direktori %s: %v", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("buat berkas sementara: %v", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("tulis: %v", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync: %v", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("tutup: %v", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("setel mode: %v", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("ganti %s: %v", path, err)
	}
	return nil
}

// ReadFile memuat berkas berukuran wajar ke memori.
func ReadFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s melebihi batas %d byte", path, limit)
	}
	return b, nil
}
