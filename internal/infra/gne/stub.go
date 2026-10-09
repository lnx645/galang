//go:build !cgo

package gne

import (
	"unsafe"

	"galang/internal/domain"
)

// ErrNoCGO is returned by Load on builds without CGO.
const ErrNoCGO = "GNE requires a CGO-enabled build on this platform (runtime built without CGO)"

// Available is always false without CGO: there is no native loader.
func Available() bool { return false }

// Load always fails without CGO: there is no dlopen. declaredABI is
// accepted for signature parity with the cgo build.
func (r *Registry) Load(name, path string, declaredABI int) (*Module, error) {
	return nil, &noCGOError{path: path}
}

// Invoke is never reached (Load fails first).
func (m *Module) Invoke(name string, fnPtr unsafe.Pointer, self uint64, args []domain.Value, pos domain.Position) (domain.Value, error) {
	return nil, m.reg.hooks.MakeBug(name+": "+ErrNoCGO, pos)
}

type noCGOError struct{ path string }

func (e *noCGOError) Error() string { return ErrNoCGO + ": " + e.path }
