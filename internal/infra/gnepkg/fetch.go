package gnepkg

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MaxPackageSize membatasi ukuran zip yang diunduh/dibaca dari disk.
// Paket ekstensi resmi berukuran ratusan KB; batas ini menahan paket
// nakal yang membanjiri memori.
const MaxPackageSize = 64 << 20 // 64 MiB

// DownloadTimeout adalah timeout pengunduhan paket.
const DownloadTimeout = 60 * time.Second

// FetchURL mengunduh paket dari URL. Hanya HTTPS yang diterima — paket
// adalah kode native, transport tidak boleh bisa di-upgrade jadi plaintext.
// Mengembalikan data (≤ MaxPackageSize) atau galat.
func FetchURL(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("URL tidak valid: %v", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("hanya HTTPS yang diizinkan, dapat %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("URL tanpa host: %q", raw)
	}

	ctx, cancel := context.WithTimeout(ctx, DownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/zip, application/octet-stream")

	// Ikuti maksimum 5 pengalihan; tiap lompatan wajib tetap HTTPS.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("terlalu banyak pengalihan")
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("pengalihan ke non-HTTPS ditolak")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unduh gagal: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unduh gagal: server membalas %s", resp.Status)
	}
	// Content-Length besar langsung ditolak tanpa membaca badan.
	if resp.ContentLength > MaxPackageSize {
		return nil, fmt.Errorf("paket terlalu besar (%d byte)", resp.ContentLength)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxPackageSize+1))
	if err != nil {
		return nil, fmt.Errorf("baca badan respons: %v", err)
	}
	if len(b) > MaxPackageSize {
		return nil, fmt.Errorf("paket melebihi batas %d byte", MaxPackageSize)
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("server mengirim paket kosong")
	}
	return b, nil
}

// OfficialURL menyusun URL aset rilis GitHub untuk kanal resmi.
// name@0.6.0 → releases/download/v0.6.0/name.zip
// name       → releases/latest/download/name.zip
func OfficialURL(repo, spec string) (string, error) {
	name, ver := spec, ""
	if i := strings.IndexByte(spec, '@'); i >= 0 {
		name, ver = spec[:i], spec[i+1:]
	}
	repo = strings.TrimSuffix(repo, "/")
	if ver == "" {
		return fmt.Sprintf("%s/releases/latest/download/%s.zip", repo, name), nil
	}
	ver = strings.TrimPrefix(ver, "v")
	return fmt.Sprintf("%s/releases/download/v%s/%s.zip", repo, ver, name), nil
}
