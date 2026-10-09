package gne

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"garurda/internal/domain"
)

// rc dasar: release pertama menghapus entri, retain menahan penghapusan.
func TestTabelHandleRC(t *testing.T) {
	r := NewRegistry(Hooks{})
	h := r.newHandle(domain.Int(7))

	if v, ok := r.lookup(h); !ok || v.(domain.Int) != 7 {
		t.Fatalf("lookup(%d) = %v, %v", h, v, ok)
	}
	r.release(h)
	if _, ok := r.lookup(h); ok {
		t.Error("handle harus hilang setelah rc=0")
	}
	if _, ok := r.lookup(0); ok {
		t.Error("handle 0 tidak pernah valid")
	}

	// retain menahan host dari penghapusan (pola argumen yang di-retain C)
	h2 := r.newHandle(domain.Str("x"))
	r.retain(h2)
	r.release(h2) // rilis sementara milik host
	if _, ok := r.lookup(h2); !ok {
		t.Error("handle yang di-retain harus bertahan dari rilis host")
	}
	r.release(h2)
	if _, ok := r.lookup(h2); ok {
		t.Error("release kedua harus menghapus entri")
	}
}

// finishCall me-release semua handle sementara kecuali *ret, lalu
// mengambil nilai *ret (kepemilikan berpindah dari C ke host).
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
	// dua sementara dilepas, *ret dilepas setelah diambil
	if _, ok := r.lookup(a); ok {
		t.Error("temp a harus dilepas")
	}
	if _, ok := r.lookup(b); ok {
		t.Error("temp b harus dilepas")
	}
	if _, ok := r.lookup(ret); ok {
		t.Error("referensi host atas *ret harus dilepas")
	}
	if len(m.temps) != 0 {
		t.Errorf("temps = %d, want 0", len(m.temps))
	}

	// ret=0: semua dilepas, tidak ada galat
	m.newTemp(domain.Int(3))
	if v, err := m.finishCall(0); v != nil || err != nil {
		t.Errorf("finishCall(0) = %v, %v", v, err)
	}

	// *ret basi → galat jelas
	m.newTemp(domain.Int(4))
	if _, err := m.finishCall(999999); err == nil {
		t.Error("ret basi harus menghasilkan galat")
	}
}

// ABI di Go harus sinkron dengan GNE_ABI di header publik — sumber
// kebenaran yang dikompilasi bersama ekstensi pihak ketiga.
func TestABISinkronHeader(t *testing.T) {
	b, err := os.ReadFile("../../../include/gne.h")
	if err != nil {
		t.Fatalf("baca gne.h: %v", err)
	}
	want := fmt.Sprintf("#define GNE_ABI %d", ABI)
	if !strings.Contains(string(b), want) {
		t.Errorf("gne.h tidak memuat %q sedangkan ABI Go = %d", want, ABI)
	}
}
