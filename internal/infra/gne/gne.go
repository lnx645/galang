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

// Global ctx → module map: the Go exports are reached through a C
// frame, so from ctx we must be able to reach whichever interpreter's
// registry is running (there may be several Interps on different
// goroutines).
var (
	gmu       sync.RWMutex
	modByID   = map[uint64]*Module{}
	nextModID uint64
)

// Available reports whether the native loader is active (built with
// CGO). Used by the `gar gne` installer to reject installs on binaries
// that cannot load extensions at all.
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

// gneFail records a pending error on ctx; Invoke turns it into a
// catchable error once the cfunc returns.
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

// takeErr reads then clears the pending error.
func takeErr(ctx *C.gne_ctx) (code string, status int, msg string) {
	code = C.GoString(ctx.code)
	msg = C.GoString(ctx.msg)
	status = int(ctx.status)
	gneClearErr(ctx)
	return
}

// Load opens a .so/.dylib/.dll and runs gne_module_init. Modules are
// cached per path; different names pointing at the same file share one
// instance (init runs only once).
//
// declaredABI is the gne_abi recorded in the package sidecar next to
// the library (0 = unknown). An older declared ABI gets a legacy table
// view so extensions built before an ABI bump keep working; the
// extension's own equality check still rejects anything it cannot run.
func (r *Registry) Load(name, path string, declaredABI int) (*Module, error) {
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
		return nil, fmt.Errorf("cannot load %s: %s", path, C.GoString(C.gne_dl_error()))
	}
	cSym := C.CString("gne_module_init")
	sym := C.gne_dl_sym(dl, cSym)
	C.free(unsafe.Pointer(cSym))
	if sym == nil {
		return nil, fmt.Errorf("%s is not a GNE extension: symbol gne_module_init not found (%s)",
			path, C.GoString(C.gne_dl_error()))
	}

	m := &Module{reg: r, name: name, path: abs}
	m.ctx = C.calloc(1, C.sizeof_gne_ctx)
	if m.ctx == nil {
		return nil, fmt.Errorf("failed to allocate a context for %s", path)
	}
	ctx := (*C.gne_ctx)(m.ctx)

	gmu.Lock()
	nextModID++
	m.id = nextModID
	modByID[m.id] = m
	gmu.Unlock()
	ctx.mod = C.uint64_t(m.id)
	m.ns = domain.NewObj()

	// Run init. The namespace is created first so define_fn inside init
	// can register; init may return that handle through
	// api->module(ctx), or 0 (use the default namespace).
	api := C.gne_get_host_api_abi(C.int32_t(declaredABI))
	var out C.uint64_t
	rc := C.gne_invoke_init(sym, api, ctx, &out)

	if ctx.failed != 0 {
		code, status, msg := takeErr(ctx)
		m.abandon()
		return nil, r.hooks.MakeError(code, status, msg, domain.Position{})
	}
	if rc != 0 {
		m.abandon()
		return nil, fmt.Errorf("gne_module_init in %s returned code %d", path, int(rc))
	}
	v, ferr := m.finishCall(uint64(out))
	if ferr != nil {
		m.abandon()
		return nil, fmt.Errorf("gne_module_init in %s did not return a valid handle", path)
	}
	if v != nil {
		obj, ok := v.(*domain.Obj)
		if !ok || obj != m.ns {
			m.abandon()
			return nil, fmt.Errorf("gne_module_init in %s must return the namespace (api->module(ctx))", path)
		}
	}

	r.byName[name] = m
	r.byPath[abs] = m
	return m, nil
}

// abandon discards a module that failed to load.
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

// Invoke calls one native function from a Builtin created by
// WrapNative. The context is reset per call; all temporary handles are
// released when it finishes.
func (m *Module) Invoke(name string, fnPtr unsafe.Pointer, self uint64, args []domain.Value, pos domain.Position) (domain.Value, error) {
	if m.ctx == nil {
		return nil, m.reg.hooks.MakeBug(name+": GNE module is not active", pos)
	}
	ctx := (*C.gne_ctx)(m.ctx)
	gneClearErr(ctx)
	ctx.self = C.uint64_t(self)
	ctx.line = C.int32_t(pos.Line)
	ctx.col = C.int32_t(pos.Col)

	argv := make([]C.uint64_t, 0, len(args)+1)
	if self != 0 {
		if _, ok := m.reg.lookup(self); !ok {
			return nil, m.reg.hooks.MakeBug(name+": self has been released", pos)
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

	// A pending error from api->throw always wins.
	if ctx.failed != 0 {
		code, status, msg := takeErr(ctx)
		m.releaseTemps()
		return nil, m.reg.hooks.MakeError(code, status, msg, pos)
	}
	if rc != 0 {
		m.releaseTemps()
		return nil, m.reg.hooks.MakeError("gne_error", 500,
			fmt.Sprintf("%s(): extension returned error code %d", name, int(rc)), pos)
	}
	v, ferr := m.finishCall(uint64(ret))
	if ferr != nil {
		return nil, m.reg.hooks.MakeBug(name+": invalid result handle", pos)
	}
	if ret == 0 {
		return nil, m.reg.hooks.MakeBug(name+": extension returned no value (*ret)", pos)
	}
	return v, nil
}

// releaseTemps releases every temporary handle that has no result.
func (m *Module) releaseTemps() {
	for _, h := range m.temps {
		m.reg.release(h)
	}
	m.temps = m.temps[:0]
}
