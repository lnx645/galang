package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGneHelp — `gar gne` tanpa argumen menampilkan bantuan Bahasa
// Indonesia dan keluar 0.
func TestGneHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runGNE(nil, &out, &errb); code != 0 {
		t.Fatalf("kode = %d", code)
	}
	s := out.String()
	for _, want := range []string{"gar gne", "install", "pack", "list", "remove", "https://"} {
		if !strings.Contains(s, want) {
			t.Errorf("help kehilangan %q", want)
		}
	}
}

// TestGneSubTidakDikenal — subperintah asing → exit 2 + pesan.
func TestGneSubTidakDikenal(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runGNE([]string{"nyasar"}, &out, &errb); code != 2 {
		t.Errorf("kode = %d, mau 2", code)
	}
	if !strings.Contains(errb.String(), "tidak dikenal") {
		t.Errorf("stderr = %q", errb.String())
	}
}

// TestGneParseOps — flag bebas urutan.
func TestGneParseOps(t *testing.T) {
	cases := []struct {
		args  []string
		force bool
		dir   string
		out   string
		pos   []string
	}{
		{[]string{"redis"}, false, "", "", []string{"redis"}},
		{[]string{"--force", "redis"}, true, "", "", []string{"redis"}},
		{[]string{"redis", "--force"}, true, "", "", []string{"redis"}},
		{[]string{"--dir", "/tmp/x", "redis"}, false, "/tmp/x", "", []string{"redis"}},
		{[]string{"--dir=/tmp/y", "a", "b"}, false, "/tmp/y", "", []string{"a", "b"}},
		{[]string{"-o", "z.zip", "ext/redis"}, false, "", "z.zip", []string{"ext/redis"}},
	}
	for i, c := range cases {
		o, pos, err := parseGneOps(c.args)
		if err != nil {
			t.Errorf("case %d: %v", i, err)
			continue
		}
		if o.force != c.force || o.dir != c.dir || o.out != c.out {
			t.Errorf("case %d: ops = %+v", i, o)
		}
		if strings.Join(pos, ",") != strings.Join(c.pos, ",") {
			t.Errorf("case %d: pos = %v, mau %v", i, pos, c.pos)
		}
	}
	// Flag tak dikenal berawal -- → galat.
	if _, _, err := parseGneOps([]string{"--nyasar"}); err == nil {
		t.Error("--nyasar harus ditolak")
	}
	// --dir tanpa nilai → galat.
	if _, _, err := parseGneOps([]string{"--dir"}); err == nil {
		t.Error("--dir tanpa nilai harus ditolak")
	}
}

// TestGneListKosong — list tanpa instalasi → pesan ramah, exit 0.
func TestGneListKosong(t *testing.T) {
	var out, errb bytes.Buffer
	code := runGNE([]string{"list", "--dir", filepath.Join(t.TempDir(), "kosong")}, &out, &errb)
	if code != 0 {
		t.Fatalf("kode = %d, stderr = %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "belum ada") {
		t.Errorf("keluaran = %q", out.String())
	}
}

// TestGneInstallTanpaArgumen — arity salah → exit 2.
func TestGneInstallTanpaArgumen(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runGNE([]string{"install"}, &out, &errb); code != 2 {
		t.Errorf("kode = %d, mau 2", code)
	}
	if code := runGNE([]string{"pack"}, &out, &errb); code != 2 {
		t.Errorf("pack kode = %d, mau 2", code)
	}
}

// TestGneRemoveKosong — remove nama yang tidak terpasang → exit 1 + pesan.
func TestGneRemoveKosong(t *testing.T) {
	var out, errb bytes.Buffer
	dir := filepath.Join(t.TempDir(), "gne")
	os.MkdirAll(dir, 0o755)
	code := runGNE([]string{"remove", "tidakada", "--dir", dir}, &out, &errb)
	if code != 1 {
		t.Errorf("kode = %d, mau 1", code)
	}
	if !strings.Contains(errb.String(), "tidak terpasang") {
		t.Errorf("stderr = %q", errb.String())
	}
}
