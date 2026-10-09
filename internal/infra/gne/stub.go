//go:build !cgo

package gne

import (
	"unsafe"

	"galang/internal/domain"
)

// ErrNoCGO dikembalikan oleh Load pada build tanpa CGO.
const ErrNoCGO = "GNE membutuhkan build dengan CGO di platform ini (runtime tanpa CGO)"

// Available selalu false pada build tanpa CGO: tidak ada loader native.
func Available() bool { return false }

// Load selalu gagal pada build tanpa CGO: tidak ada dlopen.
func (r *Registry) Load(name, path string) (*Module, error) {
	return nil, &noCGOError{path: path}
}

// Invoke tidak pernah tercapai (Load gagal lebih dulu).
func (m *Module) Invoke(name string, fnPtr unsafe.Pointer, self uint64, args []domain.Value, pos domain.Position) (domain.Value, error) {
	return nil, m.reg.hooks.MakeBug(name+": "+ErrNoCGO, pos)
}

type noCGOError struct{ path string }

func (e *noCGOError) Error() string { return ErrNoCGO + ": " + e.path }
