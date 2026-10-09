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

// MaxPackageSize limits the size of a zip downloaded/read from disk.
// Official extension packages are a few hundred KB; this cap holds back
// rogue packages that would flood memory.
const MaxPackageSize = 64 << 20 // 64 MiB

// DownloadTimeout is the package download timeout.
const DownloadTimeout = 60 * time.Second

// FetchURL downloads a package from a URL. Only HTTPS is accepted — a
// package is native code, and the transport must never be upgradable to
// plaintext. Returns the data (≤ MaxPackageSize) or an error.
func FetchURL(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %v", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("only HTTPS is allowed, got %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("URL without a host: %q", raw)
	}

	ctx, cancel := context.WithTimeout(ctx, DownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/zip, application/octet-stream")

	// Follow at most 5 redirects; every hop must stay HTTPS.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to non-HTTPS rejected")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: server responded %s", resp.Status)
	}
	// A large Content-Length is rejected outright, without reading the body.
	if resp.ContentLength > MaxPackageSize {
		return nil, fmt.Errorf("package too large (%d bytes)", resp.ContentLength)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxPackageSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %v", err)
	}
	if len(b) > MaxPackageSize {
		return nil, fmt.Errorf("package exceeds the %d byte limit", MaxPackageSize)
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("server sent an empty package")
	}
	return b, nil
}

// OfficialURL builds the GitHub release asset URL for the official channel.
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
