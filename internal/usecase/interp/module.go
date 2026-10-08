package interp

// Modul dari file: `use "db"` / `use "lib/util.ga"` memuat file .ga
// relatif terhadap file yang memanggil `use`. File dieksekusi sekali
// dalam scope-nya sendiri (terisolasi dari program pemanggil); semua
// `fn` top-level diekspor sebagai metode namespace — sama seperti
// `use "strings"`. Variabel top-level file bersifat privat: terbaca
// oleh fn di file yang sama, tidak ikut ter-ekspor.
//
// Modul bawaan selalu menang lebih dulu (lihat compileUse), jadi
// `use "http"` tidak akan pernah memuat http.ga di direktori kerja.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"garurda/internal/domain"
)

// fileModuleName is the namespace a file module binds: its base name
// without the .ga suffix ("lib/util.ga" → "util", "db" → "db").
func fileModuleName(path string) string {
	base := filepath.Base(filepath.ToSlash(path))
	return strings.TrimSuffix(base, ".ga")
}

// loadFileModule reads, compiles, and runs the file module for path,
// resolved against the directory of the file that issued `use`, and
// returns its exported namespace object. The body runs once per
// interpreter; later uses share the object. pos is the `use` statement
// for error reporting.
func (in *Interp) loadFileModule(path string, pos domain.Position) (domain.Value, error) {
	base := path
	if !filepath.IsAbs(base) {
		base = filepath.Join(in.curDir, path)
	}
	cands := []string{base}
	if !strings.HasSuffix(base, ".ga") {
		cands = append(cands, base+".ga")
	}
	var (
		abs   string
		data  []byte
		found bool
	)
	for _, c := range cands {
		if d, err := os.ReadFile(c); err == nil {
			abs, _ = filepath.Abs(c)
			data, found = d, true
			break
		}
	}
	if !found {
		return nil, in.errf(pos, "module '%s' not found (tried %s)", path, strings.Join(cands, ", "))
	}
	if v, ok := in.fileModules[abs]; ok {
		return v, nil
	}
	if _, active := in.loading.LoadOrStore(abs, true); active {
		return nil, in.errf(pos, "circular use of module '%s'", path)
	}
	defer in.loading.Delete(abs)

	prog, err := in.programFrom(string(data), abs)
	if err != nil {
		return nil, err
	}

	// Konteks file: di dalam modul, galat kompilasi maupun runtime
	// membawa nama dan direktori file modul itu sendiri.
	prevFile, prevDir := in.File, in.curDir
	in.File = prog.File
	in.curDir = filepath.Dir(abs)
	defer func() { in.File, in.curDir = prevFile, prevDir }()

	// Scope modul: tiruan gscope — global, terisolasi dari program
	// pemanggil, berisi salinan nilai builtin sebagai prelude.
	mscope := newCScope(nil, true)
	mscope.isGlobal = true
	preload := make(map[string]bool, len(in.builtinVals))
	for name := range in.builtinVals {
		mscope.names[name] = &cslot{kind: slotVal, idx: mscope.layout.addVal(), global: true}
		preload[name] = true
	}
	c := &compiler{in: in, globalScope: mscope, captures: &[]capture{}}
	body := c.compileStmts(prog.Stmts, mscope)
	if c.err != nil {
		return nil, c.err
	}
	mframe := &frame{}
	mframe.grow(mscope.layout)
	mframe.glob = mframe
	for name := range in.builtinVals {
		if gs := in.gscope.names[name]; gs != nil && gs.idx < len(in.globals.vals) {
			mframe.vals[mscope.names[name].idx] = in.globals.vals[gs.idx]
		}
	}
	if _, _, err := body(mframe); err != nil {
		return nil, err
	}

	// Ekspor: semua fn top-level selain prelude builtin. Slot sel slotVal
	// berisi fungsi; variabel privat dan namespace hasil `use` ikut
	// terlewat (bukan tipe func).
	names := make([]string, 0, len(mscope.names))
	for name := range mscope.names {
		if !preload[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	obj := domain.NewObj()
	for _, name := range names {
		cs := mscope.names[name]
		if cs.kind != slotVal || cs.idx >= len(mframe.vals) {
			continue
		}
		if v, ok := mframe.vals[cs.idx].(domain.Value); ok && v != nil && v.Type() == domain.TypeFunc {
			obj.Set(name, v)
		}
	}
	in.fileModules[abs] = obj
	return obj, nil
}
