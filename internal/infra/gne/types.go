// Package gne memuat ekstensi native GaLang (GaLang Native Extension):
// berkas .so/.dylib/.dll yang mengekspor gne_module_init sesuai include/gne.h.
//
// Pembagian tanggung jawab: paket ini murni mekanik C-ABI (dlopen, tabel
// handle, jembatan panggilan). Semantik bahasa (membuat Builtin, galat
// catchable, memanggil closure GaLang) disuntikkan lewat Hooks dari sisi
// interpreter, sehingga arah import tetap domain → usecase → infra.
package gne

import (
	"unsafe"

	"galang/internal/domain"
)

// ABI adalah versi ABI GNE yang didukung build ini. Nilainya WAJIB sama
// dengan GNE_ABI di include/gne.h (diuji di types_test.go) dan dipakai
// installer `gar gne` untuk menolak paket yang tidak kompatibel.
const ABI = 1

// Hooks adalah jembatan semantik dari interpreter. Semua fungsi dipanggil
// pada goroutine interpreter (kontrak satu-goroutine mesin).
type Hooks struct {
	// WrapNative membangun domain.Value (Builtin) untuk satu fungsi
	// native. name sudah berupa "modul.fn". self=0 bila fungsi biasa;
	// min/max tidak menghitung self (untuk method, argv[0] = self).
	WrapNative func(name string, min, max int, fnPtr unsafe.Pointer, self uint64, mod *Module) domain.Value
	// MakeError membangun galat catchable (throw dengan code+status).
	MakeError func(code string, status int, msg string, pos domain.Position) error
	// MakeBug membangun galat internal engine (errf, tidak catchable).
	MakeBug func(msg string, pos domain.Position) error
	// CallValue memanggil nilai GaLang (callback dari C).
	CallValue func(fn domain.Value, args []domain.Value, pos domain.Position) (domain.Value, error)
	// DescribeError memecah error pemanggilan balik menjadi code/status/msg.
	DescribeError func(err error) (code string, status int, msg string)
}

// hentry adalah satu slot tabel handle: nilai + reference count.
type hentry struct {
	v  domain.Value
	rc int
}

// errInvalidRet dipakai bila *ret menunjuk handle yang sudah tidak ada.
var errInvalidRet = &retErr{}

type retErr struct{}

func (*retErr) Error() string { return "handle hasil tidak valid" }

// Registry berstate per-Interp: tabel handle + cache modul yang sudah
// dimuat. Satu registry hanya disentuh oleh goroutine interp-nya.
type Registry struct {
	hooks  Hooks
	values map[uint64]*hentry
	next   uint64
	byName map[string]*Module
	byPath map[string]*Module
}

// Module adalah satu ekstensi yang sudah dimuat: namespace + konteks C.
type Module struct {
	reg  *Registry
	ns   *domain.Obj
	name string
	path string
	id   uint64
	// ctx adalah *C.gne_ctx yang dialokasikan C (bukan memori Go),
	// disimpan sebagai unsafe.Pointer supaya tipe ini tetap bisa
	// dipakai oleh kode interpreter yang tidak memakai cgo.
	ctx unsafe.Pointer
	// temps mengumpulkan handle yang dibuat selama satu call; dilepas
	// host setelah cfunc selesai (kecuali *ret atau yang di-retain).
	temps []uint64
}

// NewRegistry membuat registry dengan hooks interpreter.
func NewRegistry(h Hooks) *Registry {
	return &Registry{
		hooks:  h,
		values: map[uint64]*hentry{},
		byName: map[string]*Module{},
		byPath: map[string]*Module{},
	}
}

// Namespace mengembalikan objek namespace modul (untuk diikat oleh use).
func (m *Module) Namespace() *domain.Obj { return m.ns }

// Name mengembalikan nama namespace (mis. "redis").
func (m *Module) Name() string { return m.name }

// Path mengembalikan path .so yang dimuat.
func (m *Module) Path() string { return m.path }

// ---- tabel handle (murni Go, dipakai sisi cgo dan stub) ----

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

// release mengurangi rc; pada 0 entri dihapus sehingga handle basi
// terdeteksi sebagai "tidak valid", bukan membaca nilai lain.
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

// newTemp membuat handle sementara milik call berjalan (di-release host
// setelah cfunc selesai, kecuali dikembalikan lewat *ret atau di-retain).
func (m *Module) newTemp(v domain.Value) uint64 {
	h := m.reg.newHandle(v)
	m.temps = append(m.temps, h)
	return h
}

// finishCall me-release semua handle sementara kecuali ret, lalu
// me-referensikan nilai *ret (kepemilikan berpindah dari C ke host).
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
	m.reg.release(ret) // satu referensi host dari perpindahan *ret
	if !ok {
		return nil, errInvalidRet
	}
	return v, nil
}

// resetCall membersihkan state call sebelumnya.
func (m *Module) resetCall(self uint64, pos domain.Position) {
	for _, h := range m.temps {
		m.reg.release(h)
	}
	m.temps = m.temps[:0]
	_ = self
	_ = pos
}
