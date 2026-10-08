package interp

import (
	"testing"
	"unsafe"
)

// TestPromiseSizeCap menjaga janji tetap ramping. Satu loop `spawn` bisa
// membuat seratus ribu promise sebelum drain, jadi setiap byte yang bertambah
// dikalikan seratus ribu. Ukuran melebihi 64 byte = kembali ke masalah lama
// (memori puncak spawn melonjak di atas JavaScript).
func TestPromiseSizeCap(t *testing.T) {
	const cap = 64
	got := int(unsafe.Sizeof(promise{}))
	if got > cap {
		t.Errorf("sizeof(promise) = %d byte, batas %d byte — perkecil struct-nya", got, cap)
	}
}
