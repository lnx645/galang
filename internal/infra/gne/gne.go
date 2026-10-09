//go:build cgo

package gne

/*
#cgo CFLAGS: -I${SRCDIR}/../../../include
#include "shim.h"
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"path/filepath"
	"sync"
	"unsafe"

	"galang/internal/domain"
)

// Peta global ctx → modul: ekspor Go dipanggil melewati frame C, jadi dari
// ctx harus bisa mencapai registry milik interpreter mana pun (bisa ada
// beberapa Interp pada goroutine berbeda).
var (
	gmu       sync.RWMutex
	modByID   = map[uint64]*Module{}
	nextModID uint64
)

// Available melaporkan apakah loader native aktif (build dengan CGO).
// Dipakai installer `gar gne` untuk menolak pemasangan pada binari yang
// tidak bisa memuat ekstensi sama sekali.
func Available() bool { return true }

func modFor(ctx *C.gne_ctx) *Module {
	if ctx == nil {
		return nil
	}
	gmu.RLock()
	m := modByID[uint64(ctx.mod)]
	gmu.RUnlock()
	return m
}

func gneClearErr(ctx *C.gne_ctx) {
	if ctx.code != nil {
		C.free(unsafe.Pointer(ctx.code))
		ctx.code = nil
	}
	if ctx.msg != nil {
		C.free(unsafe.Pointer(ctx.msg))
		ctx.msg = nil
	}
	ctx.failed = 0
	ctx.status = 0
}

// gneFail mencatat galat tertunda di ctx; Invoke mengubahnya menjadi
// galat catchable setelah cfunc selesai.
func gneFail(ctx *C.gne_ctx, code string, status int, msg string) {
	gneClearErr(ctx)
	ctx.failed = 1
	ctx.status = C.int32_t(status)
	if code == "" {
		code = "gne_error"
	}
	ctx.code = C.CString(code)
	ctx.msg = C.CString(msg)
}

// takeErr membaca lalu mengosongkan galat tertunda.
func takeErr(ctx *C.gne_ctx) (code string, status int, msg string) {
	code = C.GoString(ctx.code)
	msg = C.GoString(ctx.msg)
	status = int(ctx.status)
	gneClearErr(ctx)
	return
}

// Load membuka berkas .so/.dylib/.dll dan menjalankan gne_module_init.
// Modul yang sudah dimuat di-cache per path; nama berbeda menunjuk berkas
// sama akan berbagi satu instance (init hanya sekali).
func (r *Registry) Load(name, path string) (*Module, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if m, ok := r.byPath[abs]; ok {
		r.byName[name] = m
		return m, nil
	}
	if m, ok := r.byName[name]; ok {
		return m, nil
	}

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	dl := C.gne_dl_open(cPath)
	if dl == nil {
		return nil, fmt.Errorf("gagal memuat %s: %s", path, C.GoString(C.gne_dl_error()))
	}
	cSym := C.CString("gne_module_init")
	sym := C.gne_dl_sym(dl, cSym)
	C.free(unsafe.Pointer(cSym))
	if sym == nil {
		return nil, fmt.Errorf("%s bukan ekstensi GNE: simbol gne_module_init tidak ditemukan (%s)",
			path, C.GoString(C.gne_dl_error()))
	}

	m := &Module{reg: r, name: name, path: abs}
	m.ctx = C.calloc(1, C.sizeof_gne_ctx)
	if m.ctx == nil {
		return nil, fmt.Errorf("gagal mengalokasikan konteks untuk %s", path)
	}
	ctx := (*C.gne_ctx)(m.ctx)

	gmu.Lock()
	nextModID++
	m.id = nextModID
	modByID[m.id] = m
	gmu.Unlock()
	ctx.mod = C.uint64_t(m.id)
	m.ns = domain.NewObj()

	// Jalankan init. Namespace dibuat lebih dulu sehingga define_fn di
	// dalam init bisa mendaftar; init boleh mengembalikan handle itu
	// lewat api->module(ctx), atau 0 (pakai namespace bawaan).
	api := C.gne_get_host_api()
	var out C.uint64_t
	rc := C.gne_invoke_init(sym, api, ctx, &out)

	if ctx.failed != 0 {
		code, status, msg := takeErr(ctx)
		m.abandon()
		return nil, r.hooks.MakeError(code, status, msg, domain.Position{})
	}
	if rc != 0 {
		m.abandon()
		return nil, fmt.Errorf("gne_module_init di %s mengembalikan kode %d", path, int(rc))
	}
	v, ferr := m.finishCall(uint64(out))
	if ferr != nil {
		m.abandon()
		return nil, fmt.Errorf("gne_module_init di %s tidak mengembalikan handle valid", path)
	}
	if v != nil {
		obj, ok := v.(*domain.Obj)
		if !ok || obj != m.ns {
			m.abandon()
			return nil, fmt.Errorf("gne_module_init di %s harus mengembalikan namespace (api->module(ctx))", path)
		}
	}

	r.byName[name] = m
	r.byPath[abs] = m
	return m, nil
}

// abandon membuang modul yang gagal dimuat.
func (m *Module) abandon() {
	for _, h := range m.temps {
		m.reg.release(h)
	}
	m.temps = m.temps[:0]
	gmu.Lock()
	delete(modByID, m.id)
	gmu.Unlock()
	if m.ctx != nil {
		C.free(m.ctx)
		m.ctx = nil
	}
}

// Invoke memanggil satu fungsi native dari Builtin yang dibuat WrapNative.
// Konteks direset per call; semua handle sementara dilepas setelah selesai.
func (m *Module) Invoke(name string, fnPtr unsafe.Pointer, self uint64, args []domain.Value, pos domain.Position) (domain.Value, error) {
	if m.ctx == nil {
		return nil, m.reg.hooks.MakeBug(name+": modul GNE tidak aktif", pos)
	}
	ctx := (*C.gne_ctx)(m.ctx)
	gneClearErr(ctx)
	ctx.self = C.uint64_t(self)
	ctx.line = C.int32_t(pos.Line)
	ctx.col = C.int32_t(pos.Col)

	argv := make([]C.uint64_t, 0, len(args)+1)
	if self != 0 {
		if _, ok := m.reg.lookup(self); !ok {
			return nil, m.reg.hooks.MakeBug(name+": self sudah dilepas", pos)
		}
		argv = append(argv, C.uint64_t(self))
	}
	for _, a := range args {
		argv = append(argv, C.uint64_t(m.newTemp(a)))
	}
	var argvPtr *C.uint64_t
	if len(argv) > 0 {
		argvPtr = &argv[0]
	}
	var ret C.uint64_t
	rc := C.gne_invoke_fn(fnPtr, ctx, C.int(len(argv)), argvPtr, &ret)

	// Galat tertunda dari api->throw selalu menang.
	if ctx.failed != 0 {
		code, status, msg := takeErr(ctx)
		m.releaseTemps()
		return nil, m.reg.hooks.MakeError(code, status, msg, pos)
	}
	if rc != 0 {
		m.releaseTemps()
		return nil, m.reg.hooks.MakeError("gne_error", 500,
			fmt.Sprintf("%s(): ekstensi mengembalikan kode error %d", name, int(rc)), pos)
	}
	v, ferr := m.finishCall(uint64(ret))
	if ferr != nil {
		return nil, m.reg.hooks.MakeBug(name+": handle hasil tidak valid", pos)
	}
	if ret == 0 {
		return nil, m.reg.hooks.MakeBug(name+": ekstensi tidak mengembalikan nilai (*ret)", pos)
	}
	return v, nil
}

// releaseTemps me-release semua handle sementara tanpa hasil.
func (m *Module) releaseTemps() {
	for _, h := range m.temps {
		m.reg.release(h)
	}
	m.temps = m.temps[:0]
}
