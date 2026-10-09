package interp

// GNE — GaLang Native Extension.
//
// Fallback resolusi `use`: bila berkas sumber tidak ditemukan (atau path
// eksplisit berupa pustaka native), interpreter mencari ekstensi C berupa
// .so/.dylib/.dll pada:
//
//	1. <direktori pemanggil>/gne   (proyek — menang untuk pengembangan)
//	2. setiap entri $GNE_PATH     (pemisah os.PathListSeparator)
//	3. ~/.galang/gne             (global)
//
// Urutan resolusi `use` tetap: bawaan (in.Modules) → file .ga → GNE.
// Tidak ada perubahan parser atau compiler: compileUse sudah terikat ke
// nama namespace (fileModuleName) sebelum jalur runtime ini dijalankan.
// Lihat include/gne.h untuk ABI dan docs/id/gne.md untuk panduan penulis.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"galang/internal/domain"
	"galang/internal/infra/gne"
)

// gneNativeSuffixes adalah ekstensi pustaka native yang dikenali.
var gneNativeSuffixes = []string{".so", ".dylib", ".dll"}

// gneIsNative melaporkan apakah path menunjuk pustaka native, bukan
// berkas sumber GaLang.
func gneIsNative(path string) bool {
	lp := strings.ToLower(path)
	for _, s := range gneNativeSuffixes {
		if strings.HasSuffix(lp, s) {
			return true
		}
	}
	return false
}

// gneStripSuffix membuang ekstensi native: "lib/redis.so" → "redis".
func gneStripSuffix(path string) string {
	lp := strings.ToLower(path)
	for _, s := range gneNativeSuffixes {
		if strings.HasSuffix(lp, s) {
			return strings.TrimSuffix(path, path[len(path)-len(s):])
		}
	}
	return path
}

// gneLibExts mengembalikan ekstensi pustaka untuk platform berjalan
// (macOS juga menerima .so karena dlopen menerimanya).
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

// gneRegistry membuat registry GNE pada pertama kalinya, lengkap dengan
// hooks semantik interpreter (Builtin, galat catchable, callback).
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

// gneDirs mengembalikan direktori pencarian, urut prioritas.
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
		// Lokasi lama sebelum rebrand v0.7.0 — dicari bila masih ada
		// agar ekstensi terpasang lama tidak perlu dipasang ulang.
		legacy := filepath.Join(home, ".garurda", "gne")
		if st, statErr := os.Stat(legacy); statErr == nil && st.IsDir() {
			dirs = append(dirs, legacy)
		}
	}
	return dirs
}

// gneCandidatesList mengembalikan semua path yang akan dicoba untuk nama
// telanjang — dipakai pada pesan "not found" agar pengguna tahu persis
// ke mana meletakkan ekstensi.
func (in *Interp) gneCandidatesList(name string) []string {
	var out []string
	for _, d := range in.gneDirs() {
		for _, ext := range gneLibExts() {
			out = append(out, filepath.Join(d, name+ext))
		}
	}
	return out
}

// gneAdopt memakai hasil Registry.Load: memberi konteks posisi pada galat
// init yang catchable, atau mengubah galat mekanis menjadi errf.
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

// loadGNEExplicit memuat ekstensi dari path eksplisit, mis.
// use "lib/foo.so" atau use "foo.dll". Path relatif diselesaikan
// terhadap direktori file pemanggil.
func (in *Interp) loadGNEExplicit(path string, pos domain.Position) (domain.Value, error) {
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(in.curDir, path)
	}
	name := gneStripSuffix(filepath.Base(path))
	reg := in.gneRegistry()
	m, err := reg.Load(name, p)
	if err != nil {
		_, aerr := in.gneAdopt(nil, err, pos)
		return nil, aerr
	}
	return m.Namespace(), nil
}

// loadGNEBare mencari nama telanjang pada direktori pencarian GNE.
// found=false bila tidak ada satu pun kandidat yang ada.
func (in *Interp) loadGNEBare(name string, pos domain.Position) (domain.Value, bool, error) {
	reg := in.gneRegistry()
	for _, d := range in.gneDirs() {
		for _, ext := range gneLibExts() {
			p := filepath.Join(d, name+ext)
			if st, err := os.Stat(p); err != nil || st.IsDir() {
				continue
			}
			v, lerr := reg.Load(name, p)
			if lerr != nil {
				_, aerr := in.gneAdopt(nil, lerr, pos)
				return nil, true, aerr
			}
			return v.Namespace(), true, nil
		}
	}
	return nil, false, nil
}
