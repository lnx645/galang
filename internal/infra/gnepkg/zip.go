// Package gnepkg is the GNE extension packaging infrastructure: a safe
// zip archive (zip-slip proof), SHA-256 hashes, file read/write, and
// HTTPS downloads. Pure mechanics — business rules (manifest, platform
// validation, install flow) live in internal/usecase/gnepkg.
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

// SHA256Hex returns the content fingerprint of a file as hexadecimal.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// validateEntryName rejects dangerous paths before they are touched:
// absolute, escaping the parent (..), Windows separators in the zip,
// or a drive field.
func validateEntryName(name string) error {
	if name == "" {
		return fmt.Errorf("zip entry without a name")
	}
	if strings.Contains(name, "\\") {
		return fmt.Errorf("zip entry %q uses a Windows separator", name)
	}
	if strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return fmt.Errorf("zip entry %q is absolute", name)
	}
	clean := path.Clean(name)
	if clean != name || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("zip entry %q escapes the parent directory (zip-slip)", name)
	}
	return nil
}

// ReadZip splits the contents of a zip into a name → data map. Every entry
// is validated first (zip-slip, symbolic links, duplicates) so the install
// flow never extracts a file whose path is unsafe.
func ReadZip(data []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a valid zip: %v", err)
	}
	out := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		if err := validateEntryName(f.Name); err != nil {
			return nil, err
		}
		// Symlinks/pipes are forbidden: official packages only contain
		// regular files.
		if f.Mode()&os.ModeSymlink != 0 || !f.Mode().IsRegular() {
			return nil, fmt.Errorf("zip entry %q is not a regular file (rejected)", f.Name)
		}
		if _, dup := out[f.Name]; dup {
			return nil, fmt.Errorf("duplicate zip entry %q", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open entry %q: %v", f.Name, err)
		}
		// The per-entry cap guards against zip bombs; extension packages
		// are far below it.
		b, err := io.ReadAll(io.LimitReader(rc, maxEntrySize+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read entry %q: %v", f.Name, err)
		}
		if int64(len(b)) > maxEntrySize {
			return nil, fmt.Errorf("entry %q exceeds the %d byte limit", f.Name, maxEntrySize)
		}
		out[f.Name] = b
	}
	return out, nil
}

// maxEntrySize limits the size of a single zip entry (zip-bomb proof).
const maxEntrySize = 256 << 20 // 256 MiB

// packTimestamp is the fixed timestamp for every packed entry: the same
// zip is produced from the same sources (reproducible).
var packTimestamp = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// WriteZip writes a list of files to a zip at path, using a fixed
// timestamp so the packed result is reproducible (a stable package
// fingerprint across builds).
func WriteZip(dst string, files []File) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create output directory: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		if err := validateEntryName(f.Name); err != nil {
			zw.Close()
			return err
		}
		hdr := &zip.FileHeader{Name: f.Name, Method: zip.Deflate}
		hdr.Modified = packTimestamp // fixed time → reproducible zip
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			zw.Close()
			return fmt.Errorf("write entry %q: %v", f.Name, err)
		}
		if _, err := w.Write(f.Data); err != nil {
			zw.Close()
			return fmt.Errorf("fill entry %q: %v", f.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("close zip: %v", err)
	}
	return WriteFile(dst, buf.Bytes())
}

// File is a single zip entry to be written.
type File struct {
	Name string
	Data []byte
}

// WriteFile writes data to path atomically (temporary file + rename) so a
// failure halfway through never leaves a half-written file behind.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %v", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %v", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write: %v", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync: %v", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close: %v", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("set mode: %v", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %v", path, err)
	}
	return nil
}

// ReadFile loads a reasonably sized file into memory.
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
		return nil, fmt.Errorf("%s exceeds the %d byte limit", path, limit)
	}
	return b, nil
}
