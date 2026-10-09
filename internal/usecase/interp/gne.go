package interp

// GNE — GaLang Native Extension.
//
// Fallback resolution for `use`: when no source file is found (or the
// explicit path points at a native library), the interpreter looks for
// a C extension — .so/.dylib/.dll — in:
//
//	1. <caller dir>/gne   (project — wins, for development)
//	2. every $GNE_PATH entry   (os.PathListSeparator separated)
//	3. ~/.galang/gne             (global)
//
// The resolution order for `use` is unchanged: builtins (in.Modules)
// → .ga file → GNE. Neither parser nor compiler changes: compileUse
// already binds the namespace name (fileModuleName) before this
// runtime path runs.
// See include/gne.h for the ABI and docs/en/gne.md for the authoring
// guide.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"galang/internal/domain"
	"galang/internal/infra/gne"
)

// gneSidecarSuffix is the per-extension manifest written by
// `gar gne install` next to the library (<name>.gne.json). Its
// gne_abi field tells the loader which ABI view the library was built
// for.
const gneSidecarSuffix = ".gne.json"

// gneNativeSuffixes are the native library suffixes we recognize.
var gneNativeSuffixes = []string{".so", ".dylib", ".dll"}

// gneIsNative reports whether path points at a native library rather
// than a GaLang source file.
func gneIsNative(path string) bool {
	lp := strings.ToLower(path)
	for _, s := range gneNativeSuffixes {
		if strings.HasSuffix(lp, s) {
			return true
		}
	}
	return false
}

// gneStripSuffix drops the native suffix: "lib/redis.so" → "redis".
func gneStripSuffix(path string) string {
	lp := strings.ToLower(path)
	for _, s := range gneNativeSuffixes {
		if strings.HasSuffix(lp, s) {
			return strings.TrimSuffix(path, path[len(path)-len(s):])
		}
	}
	return path
}

// gneLibExts returns the library suffixes for the running platform
// (macOS also accepts .so because dlopen does).
func gneLibExts() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{".dll"}
	case "darwin":
		return []string{".dylib", ".so"}
	default:
		return []string{".so"}
	}
}

// gneDeclaredABI reads gne_abi from the sidecar sitting next to the
// library. Returns 0 when the sidecar is missing or unreadable — the
// loader then assumes the current host ABI (correct for manually
// dropped libraries built against the current header).
func gneDeclaredABI(dir, name string) int {
	b, err := os.ReadFile(filepath.Join(dir, name+gneSidecarSuffix))
	if err != nil {
		return 0
	}
	var sc struct {
		GNEABI int `json:"gne_abi"`
	}
	if err := json.Unmarshal(b, &sc); err != nil {
		return 0
	}
	return sc.GNEABI
}

// gneRegistry creates the GNE registry on first use, complete with the
// interpreter's semantic hooks (Builtin, catchable errors, callbacks).
func (in *Interp) gneRegistry() *gne.Registry {
	if in.gne == nil {
		in.gne = gne.NewRegistry(gne.Hooks{
			WrapNative: func(name string, min, max int, fnPtr unsafe.Pointer, self uint64, mod *gne.Module) domain.Value {
				return in.strictFn(name, min, max, func(_ *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
					return mod.Invoke(name, fnPtr, self, args, pos)
				})
			},
			MakeError: func(code string, status int, msg string, pos domain.Position) error {
				return in.Throw(msg, code, status, pos)
			},
			MakeBug: func(msg string, pos domain.Position) error {
				return in.errf(pos, "%s", msg)
			},
			CallValue: in.callValue,
			DescribeError: func(err error) (string, int, string) {
				if e, ok := err.(*Error); ok && e.Value != nil {
					return e.Value.Code, e.Value.Status, e.Value.Message
				}
				if e, ok := err.(*Error); ok {
					return "gne_error", 500, e.Msg
				}
				return "gne_error", 500, err.Error()
			},
		})
	}
	return in.gne
}

// gneDirs returns the search directories, in priority order.
func (in *Interp) gneDirs() []string {
	dirs := []string{filepath.Join(in.curDir, "gne")}
	if env := os.Getenv("GNE_PATH"); env != "" {
		for _, d := range strings.Split(env, string(os.PathListSeparator)) {
			if d == "" {
				continue
			}
			if !filepath.IsAbs(d) {
				d = filepath.Join(in.curDir, d)
			}
			dirs = append(dirs, d)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".galang", "gne"))
		// Old location from before the v0.7.0 rebrand — searched when
		// it exists so previously installed extensions keep working
		// without reinstalling.
		legacy := filepath.Join(home, ".garurda", "gne")
		if st, statErr := os.Stat(legacy); statErr == nil && st.IsDir() {
			dirs = append(dirs, legacy)
		}
	}
	return dirs
}

// gneCandidatesList returns every path that will be tried for a bare
// name — used in the "not found" message so users know exactly where
// to drop an extension.
func (in *Interp) gneCandidatesList(name string) []string {
	var out []string
	for _, d := range in.gneDirs() {
		for _, ext := range gneLibExts() {
			out = append(out, filepath.Join(d, name+ext))
		}
	}
	return out
}

// gneAdopt applies Registry.Load's result: giving the init error a
// catchable position context, or turning a mechanical error into errf.
func (in *Interp) gneAdopt(v domain.Value, err error, pos domain.Position) (domain.Value, error) {
	if err == nil {
		return v, nil
	}
	if e, ok := err.(*Error); ok {
		if e.Pos == (domain.Position{}) {
			e.Pos = pos
		}
		if e.File == "" {
			e.File = in.curFile()
		}
		return nil, e
	}
	return nil, in.errf(pos, "%v", err)
}

// loadGNEExplicit loads an extension from an explicit path, e.g.
// use "lib/foo.so" or use "foo.dll". Relative paths are resolved
// against the calling file's directory.
func (in *Interp) loadGNEExplicit(path string, pos domain.Position) (domain.Value, error) {
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(in.curDir, path)
	}
	name := gneStripSuffix(filepath.Base(path))
	reg := in.gneRegistry()
	m, err := reg.Load(name, p, gneDeclaredABI(filepath.Dir(p), name))
	if err != nil {
		_, aerr := in.gneAdopt(nil, err, pos)
		return nil, aerr
	}
	return m.Namespace(), nil
}

// loadGNEBare searches for a bare name in the GNE search directories.
// found=false when no candidate exists.
func (in *Interp) loadGNEBare(name string, pos domain.Position) (domain.Value, bool, error) {
	reg := in.gneRegistry()
	for _, d := range in.gneDirs() {
		for _, ext := range gneLibExts() {
			p := filepath.Join(d, name+ext)
			if st, err := os.Stat(p); err != nil || st.IsDir() {
				continue
			}
			v, lerr := reg.Load(name, p, gneDeclaredABI(d, name))
			if lerr != nil {
				_, aerr := in.gneAdopt(nil, lerr, pos)
				return nil, true, aerr
			}
			return v.Namespace(), true, nil
		}
	}
	return nil, false, nil
}
