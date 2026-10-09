package gnepkg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"galang/internal/infra/gne"
	"galang/internal/infra/gnepkg"
)

// Options controls the behavior of Install.
type Options struct {
	// Dir is the install directory; empty = ~/.galang/gne.
	Dir string
	// Force overwrites an existing install and gets past the non-CGO /
	// unavailable-platform rejections.
	Force bool
	// Available checks whether this gar binary embeds the GNE loader
	// (default gne.Available); injected to test the non-CGO path.
	Available func() bool
	// Fetch downloads a package from a URL (default gnepkg.FetchURL);
	// injected to test the URL path without a network.
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

// Result is the report of a successful installation.
type Result struct {
	Name     string
	Version  string
	Platform string // platform of the binary that was actually installed
	Path     string // extracted native file
	Sidecar  string // <name>.gne.json
	Previous bool   // an old install was overwritten (requires Force)
	Source   string // URL or path the package came from
}

// parseShortcut splits a channel spec into (name, version, ok).
// "redis" → ("redis", "", true); "redis@0.6.0" → ("redis", "0.6.0", true).
// Callers must check ok before using the result (an empty name means the
// spec is not a shortcut).
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

// resolveSource determines the package source from the user's spec:
// an HTTPS URL, a local file path, or an official-channel shortcut
// ("redis@0.6.0"). HTTP is rejected: a package is native code and must
// never fall back to plaintext.
func resolveSource(spec string) (source string, local bool, err error) {
	if strings.Contains(spec, "://") {
		if !strings.HasPrefix(spec, "https://") {
			return "", false, fmt.Errorf("only HTTPS is allowed, got %q (packages are native code)", spec)
		}
		return spec, false, nil
	}
	// File path: contains a separator or ends with .zip.
	if strings.ContainsAny(spec, `/\`) || strings.HasSuffix(spec, ".zip") {
		if _, statErr := os.Stat(spec); statErr != nil {
			return "", true, fmt.Errorf("package file %q not found", spec)
		}
		return spec, true, nil
	}
	if _, _, ok := parseShortcut(spec); ok {
		u, uerr := gnepkg.OfficialURL(OfficialRepo, spec)
		return u, false, uerr
	}
	return "", false, fmt.Errorf("%q is not an HTTPS URL, a .zip file, or an official extension name", spec)
}

// OfficialURL builds the official-channel shortcut URL ("redis" → latest
// release; "redis@0.6.0" → release v0.6.0).
func OfficialURL(spec string) (string, error) {
	return gnepkg.OfficialURL(OfficialRepo, spec)
}

// Install installs an extension package from spec (HTTPS URL / local file /
// official-channel shortcut) into the install directory.
func Install(ctx context.Context, spec string, o Options) (*Result, error) {
	o.setDefaults()

	source, local, err := resolveSource(spec)
	if err != nil {
		return nil, err
	}

	// 1. Read the package contents (HTTPS download or local file), with a size cap.
	var data []byte
	if local {
		data, err = gnepkg.ReadFile(source, gnepkg.MaxPackageSize)
	} else {
		data, err = o.Fetch(ctx, source)
	}
	if err != nil {
		return nil, fmt.Errorf("read package: %v", err)
	}

	// 2. Open the zip — every entry is validated against zip-slip/symlink attacks.
	entries, err := gnepkg.ReadZip(data)
	if err != nil {
		return nil, fmt.Errorf("corrupt package: %v", err)
	}
	rawManifest, ok := entries[ManifestFile]
	if !ok {
		return nil, fmt.Errorf("package does not contain %s", ManifestFile)
	}
	var m Manifest
	if err := json.Unmarshal(rawManifest, &m); err != nil {
		return nil, fmt.Errorf("%s is invalid: %v", ManifestFile, err)
	}

	// 3. Package contract: name/version/ABI/entries (including whether each
	//    file name matches its platform — rejecting cross-built packages).
	if err := validateManifest(&m); err != nil {
		return nil, err
	}

	// 4. A binary built without CGO cannot load extensions at all.
	if !o.Available() && !o.Force {
		return nil, fmt.Errorf("this gar binary was built without CGO — GNE is not active, install aborted (use --force if you still want to lay out the files)")
	}

	// 5. Pick the platform: this machine's, or force the first one with
	//    --force (bootstrapping a directory for another machine).
	platform := CurrentPlatform()
	if _, ada := m.Entry[platform]; !ada {
		if !o.Force {
			return nil, fmt.Errorf("package does not provide a binary for %s (available: %s)", platform, joinPlatforms(&m))
		}
		platform = firstPlatform(&m)
	}

	e := m.Entry[platform]
	binary, ok := entries[e.File]
	if !ok {
		return nil, fmt.Errorf("package does not contain file %q recorded in the manifest", e.File)
	}

	// 6. Verify integrity BEFORE extracting: sha256 and size must match
	//    the manifest exactly.
	if int64(len(binary)) != e.Size {
		return nil, fmt.Errorf("size of %s does not match the manifest (got %d, want %d)", e.File, len(binary), e.Size)
	}
	if got := gnepkg.SHA256Hex(binary); got != e.SHA256 {
		return nil, fmt.Errorf("sha256 of %s does not match the manifest (got %s, want %s)", e.File, got, e.SHA256)
	}

	// 7. FLAT target: <dir>/<name><installed platform extension>.
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
		return nil, fmt.Errorf("%s is already installed in %s (use --force to overwrite)", m.Name, o.dir())
	}

	if err := os.MkdirAll(o.dir(), 0o755); err != nil {
		return nil, fmt.Errorf("create install directory: %v", err)
	}

	// 8. Extract, then RE-VERIFY the sha256 of the file already written to
	//    disk (detecting a failed write/rotation halfway through).
	if err := gnepkg.WriteFile(target, binary); err != nil {
		return nil, fmt.Errorf("write binary: %v", err)
	}
	diskData, err := gnepkg.ReadFile(target, gnepkg.MaxPackageSize)
	if err != nil {
		os.Remove(target)
		return nil, fmt.Errorf("post-extract verification failed: %v", err)
	}
	if got := gnepkg.SHA256Hex(diskData); got != e.SHA256 {
		os.Remove(target)
		return nil, fmt.Errorf("installed file sha256 does not match (%s) — file discarded", got)
	}

	if err := writeSidecar(o.dir(), &m); err != nil {
		os.Remove(target)
		return nil, fmt.Errorf("write sidecar: %v", err)
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
		return "" // Install will report the error at MkdirAll
	}
	return d
}

// firstPlatform returns the first sorted platform — a deterministic
// choice for cross-platform --force.
func firstPlatform(m *Manifest) string {
	plats := make([]string, 0, len(m.Entry))
	for p := range m.Entry {
		plats = append(plats, p)
	}
	sort.Strings(plats)
	return plats[0]
}

// ---- list & remove ----

// Installed is one entry of the `gar gne list` output.
type Installed struct {
	Name     string
	Version  string
	ABI      int
	Platform []string
	Path     string
	Size     int64
}

// List reads every sidecar in the install directory. dir empty = all
// install locations (the active directory; the legacy ~/.garurda/gne
// location if it still exists) — a name appearing in two places is
// counted once, and the active directory's version wins.
func List(dir string) ([]Installed, error) {
	if dir != "" {
		return listDir(dir)
	}
	dirs, err := SearchDirs()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Installed
	for _, d := range dirs {
		items, err := listDir(d)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if seen[it.Name] {
				continue
			}
			seen[it.Name] = true
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// listDir reads a single install directory; a directory that does not
// exist yet yields an empty list, not an error.
func listDir(dir string) ([]Installed, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no installs at all yet
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
			continue // a corrupt sidecar is skipped instead of failing the whole list
		}
		t := Installed{Name: m.Name, Version: m.Version, ABI: m.GNEABI}
		for p := range m.Entry {
			t.Platform = append(t.Platform, p)
		}
		sort.Strings(t.Platform)
		// Size of the installed binary: look for the extension variant that
		// actually exists on disk. Do not use the manifest's first platform —
		// it may not belong to this machine (e.g. darwin on a linux host).
		for _, ext := range []string{".so", ".dylib", ".dll"} {
			if st, err := os.Stat(filepath.Join(dir, m.Name+ext)); err == nil {
				t.Size = st.Size()
				break
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// Remove uninstalls an extension together with its sidecar. dir empty =
// search every install location (active + legacy) and delete from
// wherever it lives — legacy extensions can be removed without a manual
// --dir.
func Remove(dir, name string) (string, error) {
	if !reName.MatchString(name) {
		return "", fmt.Errorf("name %q is invalid", name)
	}
	if dir == "" {
		dirs, err := SearchDirs()
		if err != nil {
			return "", err
		}
		var targets []string
		for _, d := range dirs {
			if _, statErr := os.Stat(filepath.Join(d, name+suffixSidecar)); statErr == nil {
				targets = append(targets, d)
			}
		}
		if len(targets) == 0 {
			return "", fmt.Errorf("%s is not installed in %s", name, dirs[0])
		}
		var removed []string
		for _, d := range targets {
			r, err := removeFiles(d, name)
			if err != nil {
				return "", err
			}
			removed = append(removed, r...)
		}
		return strings.Join(removed, ", "), nil
	}
	sidecar := filepath.Join(dir, name+suffixSidecar)
	if _, err := os.Stat(sidecar); err != nil {
		return "", fmt.Errorf("%s is not installed in %s", name, dir)
	}
	removed, err := removeFiles(dir, name)
	if err != nil {
		return "", err
	}
	return strings.Join(removed, ", "), nil
}

// removeFiles deletes the native binary and then the name sidecar from dir.
// The sidecar must already have been verified to exist by the caller.
func removeFiles(dir, name string) ([]string, error) {
	var removed []string
	// Delete every native extension variant (the installed platform binary).
	for _, ext := range []string{".so", ".dylib", ".dll"} {
		p := filepath.Join(dir, name+ext)
		if _, err := os.Stat(p); err == nil {
			if err := os.Remove(p); err != nil {
				return nil, fmt.Errorf("remove %s: %v", p, err)
			}
			removed = append(removed, p)
		}
	}
	sidecar := filepath.Join(dir, name+suffixSidecar)
	if err := os.Remove(sidecar); err != nil {
		return nil, fmt.Errorf("remove sidecar: %v", err)
	}
	removed = append(removed, sidecar)
	return removed, nil
}
