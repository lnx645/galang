// Package gne loads native GaLang extensions (GaLang Native Extension):
// .so/.dylib/.dll files exporting gne_module_init per include/gne.h.
//
// Responsibility split: this package is pure C-ABI mechanics (dlopen, the
// handle table, the call bridge). Language semantics (creating Builtins,
// catchable errors, invoking GaLang closures) is injected through Hooks
// from the interpreter side, keeping the import direction domain →
// usecase → infra.
package gne

import (
	"unsafe"

	"galang/internal/domain"
)

// ABI is the GNE ABI version supported by this build. It MUST equal
// GNE_ABI in include/gne.h (tested in types_test.go) and is used by the
// `gar gne` installer to reject incompatible packages. The host still
// LOADS packages built for an older ABI through a legacy table view
// (the struct is append-only); only newer-than-host packages are
// rejected.
const ABI = 2

// Hooks is the semantic bridge from the interpreter. All functions are
// called on the interpreter's goroutine (single-goroutine machine
// contract).
type Hooks struct {
	// WrapNative builds a domain.Value (Builtin) for one native
	// function. name is already "module.fn". self=0 for a plain
	// function; min/max do not count self (for methods, argv[0] = self).
	WrapNative func(name string, min, max int, fnPtr unsafe.Pointer, self uint64, mod *Module) domain.Value
	// MakeError builds a catchable error (throw with code+status).
	MakeError func(code string, status int, msg string, pos domain.Position) error
	// MakeBug builds an internal engine error (errf, not catchable).
	MakeBug func(msg string, pos domain.Position) error
	// CallValue invokes a GaLang value (callback from C).
	CallValue func(fn domain.Value, args []domain.Value, pos domain.Position) (domain.Value, error)
	// DescribeError splits a callback error into code/status/msg.
	DescribeError func(err error) (code string, status int, msg string)
}

// hentry is one slot of the handle table: value + reference count.
type hentry struct {
	v  domain.Value
	rc int
}

// errInvalidRet is used when *ret points at a handle that no longer exists.
var errInvalidRet = &retErr{}

type retErr struct{}

func (*retErr) Error() string { return "invalid result handle" }

// Registry holds per-Interp state: the handle table + a cache of loaded
// modules. A registry is only touched by its interp goroutine.
type Registry struct {
	hooks  Hooks
	values map[uint64]*hentry
	next   uint64
	byName map[string]*Module
	byPath map[string]*Module
}

// Module is one loaded extension: namespace + C context.
type Module struct {
	reg  *Registry
	ns   *domain.Obj
	name string
	path string
	id   uint64
	// ctx is *C.gne_ctx allocated by C (not Go memory), stored as
	// unsafe.Pointer so this type stays usable from interpreter code
	// built without cgo.
	ctx unsafe.Pointer
	// temps collects handles created during one call; released by
	// the host after the cfunc finishes (unless retained or returned
	// through *ret).
	temps []uint64
}

// NewRegistry creates a registry with the interpreter's hooks.
func NewRegistry(h Hooks) *Registry {
	return &Registry{
		hooks:  h,
		values: map[uint64]*hentry{},
		byName: map[string]*Module{},
		byPath: map[string]*Module{},
	}
}

// Namespace returns the module namespace object (to be bound by use).
func (m *Module) Namespace() *domain.Obj { return m.ns }

// Name returns the namespace name (e.g. "redis").
func (m *Module) Name() string { return m.name }

// Path returns the path of the loaded .so.
func (m *Module) Path() string { return m.path }

// ---- handle table (pure Go, used by both the cgo and stub sides) ----

func (r *Registry) newHandle(v domain.Value) uint64 {
	r.next++
	r.values[r.next] = &hentry{v: v, rc: 1}
	return r.next
}

func (r *Registry) lookup(h uint64) (domain.Value, bool) {
	if h == 0 {
		return nil, false
	}
	e, ok := r.values[h]
	if !ok {
		return nil, false
	}
	return e.v, true
}

func (r *Registry) retain(h uint64) {
	if e, ok := r.values[h]; ok {
		e.rc++
	}
}

// release decrements rc; at 0 the entry is dropped so a stale handle is
// detected as "invalid" instead of reading someone else's value.
func (r *Registry) release(h uint64) {
	e, ok := r.values[h]
	if !ok {
		return
	}
	e.rc--
	if e.rc <= 0 {
		delete(r.values, h)
	}
}

// newTemp creates a temporary handle owned by the running call (released
// by the host after the cfunc finishes, unless returned through *ret or
// retained).
func (m *Module) newTemp(v domain.Value) uint64 {
	h := m.reg.newHandle(v)
	m.temps = append(m.temps, h)
	return h
}

// finishCall releases every temporary handle except ret, then
// dereferences *ret (ownership transfers from C to the host).
func (m *Module) finishCall(ret uint64) (domain.Value, error) {
	for _, h := range m.temps {
		if h == ret {
			continue
		}
		m.reg.release(h)
	}
	m.temps = m.temps[:0]
	if ret == 0 {
		return nil, nil
	}
	v, ok := m.reg.lookup(ret)
	m.reg.release(ret) // one host reference from the *ret transfer
	if !ok {
		return nil, errInvalidRet
	}
	return v, nil
}

// resetCall clears state left by the previous call.
func (m *Module) resetCall(self uint64, pos domain.Position) {
	for _, h := range m.temps {
		m.reg.release(h)
	}
	m.temps = m.temps[:0]
	_ = self
	_ = pos
}
