package async

import (
	"errors"
	"testing"
	"time"
)

// Race harus menyelesaikan promise hasil SEGERA oleh promise pertama yang
// selesai — dahulu parameter men-shadow promise hasil sehingga hasilnya
// tidak pernah selesai (hang).
func TestRaceSelesaiDenganPertama(t *testing.T) {
	a := NewPromise()
	b := NewPromise()
	r := Race(a, b)

	go func() {
		time.Sleep(20 * time.Millisecond)
		a.Resolve("menang")
		// b sengaja tidak pernah diselesaikan.
	}()

	done := make(chan struct{})
	var v interface{}
	var err error
	go func() {
		v, err = r.Await()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Race tidak pernah selesai (promise hasil tidak diselesaikan)")
	}
	if err != nil {
		t.Fatalf("Race: tak terduga error: %v", err)
	}
	if v != "menang" {
		t.Errorf("Race: got %v, want %q", v, "menang")
	}
}

// Race juga harus meneruskan penolakan pertama (dengan penjaga waktu supaya
// kegagalan tidak menggantung sampai timeout paket).
func TestRaceMeneruskanReject(t *testing.T) {
	a := NewPromise()
	r := Race(a)
	a.Reject(errors.New("gagal"))
	done := make(chan error, 1)
	go func() {
		_, err := r.Await()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || err.Error() != "gagal" {
			t.Errorf("Race reject: got %v, want %q", err, "gagal")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Race reject tidak pernah selesai")
	}
}

// Any: resolusi pertama menang; menolak hanya setelah SEMUA menolak.
func TestAnyResolusiPertamaMenang(t *testing.T) {
	a := NewPromise()
	b := NewPromise()
	r := Any(a, b)
	a.Reject(errors.New("kalah")) // ditolak dulu — tak menghalangi b menang
	b.Resolve("menang")
	done := make(chan struct{})
	var v interface{}
	var err error
	go func() {
		v, err = r.Await()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Any tidak pernah selesai")
	}
	if err != nil || v != "menang" {
		t.Errorf("Any: got (%v, %v), want menang", v, err)
	}
}

func TestAnyMenolakBilaSemuaMenolak(t *testing.T) {
	a := NewPromise()
	b := NewPromise()
	r := Any(a, b)
	a.Reject(errors.New("A"))
	b.Reject(errors.New("B"))
	done := make(chan error, 1)
	go func() {
		_, err := r.Await()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Errorf("Any: seharusnya menolak saat semua menolak")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Any tidak pernah menolak meski semua promise menolak")
	}
}

// All harus mengumpulkan semua nilai.
func TestAllMengumpulkanNilai(t *testing.T) {
	a := NewPromise()
	b := NewPromise()
	all := All(a, b)
	go a.Resolve(1)
	go b.Resolve(2)
	v, err := all.Await()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	has := func(want interface{}) bool {
		for _, x := range v.([]interface{}) {
			if x == want {
				return true
			}
		}
		return false
	}
	if !has(1) || !has(2) {
		t.Errorf("All: got %v", v)
	}
}
