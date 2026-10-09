// Package gnepkg manages GNE extension packages (manifest plus the pack,
// install, list, remove flows). Business rules live here; the zip archive,
// hash, and HTTPS download mechanics live in internal/infra/gnepkg.
//
// Package layout (zip):
//
//	manifest.json                 — name, version, gne_abi, entry per platform
//	<GOOS-GOARCH>/<name><ext>     — native binary per platform
//
// FLAT installation: ~/.galang/gne/<name><ext> + sidecar <name>.gne.json,
// exactly the pattern discovery `use "name"` looks for — no loader changes.
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

	"galang/internal/infra/gne"
	"galang/internal/infra/gnepkg"
)

// OfficialRepo is the official distribution channel for extensions: the
// shortcut `gar gne install redis` resolves to this repo's release assets.
const OfficialRepo = "https://github.com/lnx645/galang"

// ManifestFile is the manifest file inside a package and the installed sidecar.
const (
	ManifestFile  = "manifest.json"
	suffixSidecar = ".gne.json"
)

var (
	reName     = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	reVersion  = regexp.MustCompile(`^\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`)
	rePlatform = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)
	reSHA256   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Entry is one platform's native binary inside a package.
type Entry struct {
	File   string `json:"file"`   // path inside the zip: "linux-amd64/redis.so"
	SHA256 string `json:"sha256"` // fingerprint of the binary content
	Size   int64  `json:"size"`   // binary size in bytes
}

// Manifest is the content of manifest.json in a package (and the installed sidecar).
type Manifest struct {
	Name        string           `json:"name"`
	Version     string           `json:"version"`
	GNEABI      int              `json:"gne_abi"`
	Description string           `json:"description,omitempty"`
	Entry       map[string]Entry `json:"entry"` // "GOOS-GOARCH" → Entry
}

// Source is developer metadata in ext/<name>/gne.json (the pack input).
type Source struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// libExtFor returns the required library extension for a GOOS according to
// the discovery pattern: linux .so, darwin .dylib, windows .dll.
func libExtFor(goos string) string {
	switch goos {
	case "windows":
		return ".dll"
	case "darwin":
		return ".dylib"
	default:
		return ".so"
	}
}

// splitPlatform splits "GOOS-GOARCH"; ok=false when the format is wrong.
func splitPlatform(p string) (goos, goarch string, ok bool) {
	if !rePlatform.MatchString(p) {
		return "", "", false
	}
	i := strings.IndexByte(p, '-')
	return p[:i], p[i+1:], true
}

// validateManifest checks the package contract: a namespace-safe name, a
// valid version, an ABI this host can run, and every entry pointing to a
// binary whose name+extension matches its platform (rejecting cross-built
// or tampered packages).
func validateManifest(m *Manifest) error {
	if !reName.MatchString(m.Name) {
		return fmt.Errorf("package name %q is invalid (lowercase, starts with a letter, no punctuation)", m.Name)
	}
	if !reVersion.MatchString(m.Version) {
		return fmt.Errorf("package version %q is invalid (semver format)", m.Version)
	}
	if m.GNEABI > gne.ABI {
		return fmt.Errorf("package was built for GNE ABI %d, this build supports ABI %d — not compatible",
			m.GNEABI, gne.ABI)
	}
	if len(m.Entry) == 0 {
		return fmt.Errorf("package contains no binary")
	}
	for platform, e := range m.Entry {
		goos, _, ok := splitPlatform(platform)
		if !ok {
			return fmt.Errorf("platform %q is invalid", platform)
		}
		want := platform + "/" + m.Name + libExtFor(goos)
		if e.File != want {
			return fmt.Errorf("entry %q points to %q, expected %q", platform, e.File, want)
		}
		if !reSHA256.MatchString(e.SHA256) {
			return fmt.Errorf("entry %q has no valid sha256", platform)
		}
	}
	return nil
}

// joinPlatforms returns the sorted list of platforms for error messages.
func joinPlatforms(m *Manifest) string {
	plats := make([]string, 0, len(m.Entry))
	for p := range m.Entry {
		plats = append(plats, p)
	}
	sort.Strings(plats)
	return strings.Join(plats, ", ")
}

// CurrentPlatform is the current machine's GOOS-GOARCH.
func CurrentPlatform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// ---- sidecar ----

// readSidecar loads <name>.gne.json from the install directory.
func readSidecar(dir, name string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, name+suffixSidecar))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("sidecar %s%s is corrupt: %v", name, suffixSidecar, err)
	}
	return &m, nil
}

// writeSidecar stores the installed manifest so that `gar gne list` does
// not need to re-read the zip.
func writeSidecar(dir string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return gnepkg.WriteFile(filepath.Join(dir, m.Name+suffixSidecar), append(b, '\n'))
}

// ---- installation directories ----

// InstallDir returns the default install directory
// (~/.galang/gne) — same as the global `use` discovery candidate.
func InstallDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("cannot determine home directory — pass --dir explicitly")
	}
	return filepath.Join(home, ".galang", "gne"), nil
}

// LegacyInstallDir is the install location from before the v0.7.0 rebrand
// (~/.garurda/gne). Still used for READING (list/remove/discovery) so that
// already-installed extensions are not lost the moment the binary is
// upgraded; new installs always write to InstallDir.
func LegacyInstallDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("cannot determine home directory — pass --dir explicitly")
	}
	return filepath.Join(home, ".garurda", "gne"), nil
}

// SearchDirs returns the install directories for READING without an
// explicit --dir: the active directory first, followed by the legacy
// location if it still exists. The order decides which one wins when the
// same name exists in both places.
func SearchDirs() ([]string, error) {
	cur, err := InstallDir()
	if err != nil {
		return nil, err
	}
	dirs := []string{cur}
	if legacy, err := LegacyInstallDir(); err == nil && legacy != cur {
		if st, statErr := os.Stat(legacy); statErr == nil && st.IsDir() {
			dirs = append(dirs, legacy)
		}
	}
	return dirs, nil
}

// ---- pack ----

// Pack reads ext/<name>/ (gne.json + build/<GOOS-GOARCH>/<name><ext>) and
// then writes a multi-platform zip package. Returns the zip path and the
// generated manifest.
func Pack(dir, out string) (string, *Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "gne.json"))
	if err != nil {
		return "", nil, fmt.Errorf("read gne.json: %v", err)
	}
	var s Source
	if err := json.Unmarshal(b, &s); err != nil {
		return "", nil, fmt.Errorf("invalid gne.json: %v", err)
	}
	m := &Manifest{
		Name:        s.Name,
		Version:     s.Version,
		GNEABI:      gne.ABI,
		Description: s.Description,
		Entry:       map[string]Entry{},
	}
	if !reName.MatchString(m.Name) {
		return "", nil, fmt.Errorf("name %q in gne.json is invalid (lowercase, starts with a letter, no punctuation)", m.Name)
	}
	if !reVersion.MatchString(m.Version) {
		return "", nil, fmt.Errorf("version %q in gne.json is invalid (semver format)", m.Version)
	}

	// Scan build/<platform>/ — each directory must contain exactly one
	// binary named <name><platform extension>.
	buildDir := filepath.Join(dir, "build")
	ents, err := os.ReadDir(buildDir)
	if err != nil {
		return "", nil, fmt.Errorf("read build/: %v (run make ext first)", err)
	}
	var files []gnepkg.File
	manifestRaw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", nil, err
	}
	files = append(files, gnepkg.File{Name: ManifestFile, Data: append(manifestRaw, '\n')})

	nBuild := 0
	for _, de := range ents {
		if !de.IsDir() {
			continue
		}
		platform := de.Name()
		goos, _, ok := splitPlatform(platform)
		if !ok {
			return "", nil, fmt.Errorf("build directory %q is not a valid GOOS-GOARCH platform", platform)
		}
		binPath := filepath.Join(buildDir, platform, m.Name+libExtFor(goos))
		data, err := gnepkg.ReadFile(binPath, gnepkg.MaxPackageSize)
		if err != nil {
			return "", nil, fmt.Errorf("read %s: %v", binPath, err)
		}
		rel := platform + "/" + m.Name + libExtFor(goos)
		m.Entry[platform] = Entry{
			File:   rel,
			SHA256: gnepkg.SHA256Hex(data),
			Size:   int64(len(data)),
		}
		files = append(files, gnepkg.File{Name: rel, Data: data})
		nBuild++
	}
	if nBuild == 0 {
		return "", nil, fmt.Errorf("build/ is empty — run make ext first")
	}

	// Manifest is rewritten once the entries are filled in.
	if manifestRaw, err = json.MarshalIndent(m, "", "  "); err != nil {
		return "", nil, err
	}
	files[0].Data = append(manifestRaw, '\n')

	if out == "" {
		out = filepath.Join("dist", m.Name+".zip")
	}
	if err := gnepkg.WriteZip(out, files); err != nil {
		return "", nil, err
	}
	return out, m, nil
}
