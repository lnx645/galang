package gne

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"galang/internal/domain"
)

// Basic RC: the first release removes the entry, retain keeps it alive.
func TestHandleTableRC(t *testing.T) {
	r := NewRegistry(Hooks{})
	h := r.newHandle(domain.Int(7))

	if v, ok := r.lookup(h); !ok || v.(domain.Int) != 7 {
		t.Fatalf("lookup(%d) = %v, %v", h, v, ok)
	}
	r.release(h)
	if _, ok := r.lookup(h); ok {
		t.Error("handle must be gone once rc=0")
	}
	if _, ok := r.lookup(0); ok {
		t.Error("handle 0 is never valid")
	}

	// retain keeps the host from removing it (C-retained argument pattern)
	h2 := r.newHandle(domain.Str("x"))
	r.retain(h2)
	r.release(h2) // transient release owned by the host
	if _, ok := r.lookup(h2); !ok {
		t.Error("retained handle must survive the host release")
	}
	r.release(h2)
	if _, ok := r.lookup(h2); ok {
		t.Error("second release must remove the entry")
	}
}

// finishCall releases every temporary handle except *ret, then takes the
// value of *ret (ownership moves from C to the host).
func TestFinishCall(t *testing.T) {
	r := NewRegistry(Hooks{})
	m := &Module{reg: r}

	a := m.newTemp(domain.Int(1))
	b := m.newTemp(domain.Int(2))
	ret := m.newTemp(domain.Str("hasil"))

	v, err := m.finishCall(ret)
	if err != nil {
		t.Fatalf("finishCall: %v", err)
	}
	if v.(domain.Str) != "hasil" {
		t.Errorf("v = %v", v)
	}
	// both temporaries are released, *ret after it is taken
	if _, ok := r.lookup(a); ok {
		t.Error("temp a must be released")
	}
	if _, ok := r.lookup(b); ok {
		t.Error("temp b must be released")
	}
	if _, ok := r.lookup(ret); ok {
		t.Error("host reference to *ret must be released")
	}
	if len(m.temps) != 0 {
		t.Errorf("temps = %d, want 0", len(m.temps))
	}

	// ret=0: everything released, no error
	m.newTemp(domain.Int(3))
	if v, err := m.finishCall(0); v != nil || err != nil {
		t.Errorf("finishCall(0) = %v, %v", v, err)
	}

	// stale *ret → clear error
	m.newTemp(domain.Int(4))
	if _, err := m.finishCall(999999); err == nil {
		t.Error("stale ret must produce an error")
	}
}

// The ABI in Go must match GNE_ABI in the public header — the source of
// truth compiled together with third-party extensions.
func TestABIMatchesHeader(t *testing.T) {
	b, err := os.ReadFile("../../../include/gne.h")
	if err != nil {
		t.Fatalf("read gne.h: %v", err)
	}
	want := fmt.Sprintf("#define GNE_ABI %d", ABI)
	if !strings.Contains(string(b), want) {
		t.Errorf("gne.h does not contain %q while the Go ABI = %d", want, ABI)
	}
}
