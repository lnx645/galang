package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGneHelp — `gar gne` with no arguments prints English help
// and exits 0.
func TestGneHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runGNE(nil, &out, &errb); code != 0 {
		t.Fatalf("code = %d", code)
	}
	s := out.String()
	for _, want := range []string{"gar gne", "install", "pack", "list", "remove", "https://"} {
		if !strings.Contains(s, want) {
			t.Errorf("help missing %q", want)
		}
	}
}

// TestGneUnknownSubcommand — foreign subcommand → exit 2 + message.
func TestGneUnknownSubcommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runGNE([]string{"nyasar"}, &out, &errb); code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "unknown subcommand") {
		t.Errorf("stderr = %q", errb.String())
	}
}

// TestGneParseOps — flags in any order.
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
			t.Errorf("case %d: pos = %v, want %v", i, pos, c.pos)
		}
	}
	// Unknown flag starting with -- → error.
	if _, _, err := parseGneOps([]string{"--nyasar"}); err == nil {
		t.Error("--nyasar must be rejected")
	}
	// --dir without a value → error.
	if _, _, err := parseGneOps([]string{"--dir"}); err == nil {
		t.Error("--dir without a value must be rejected")
	}
}

// TestGneListEmpty — list with nothing installed → friendly message, exit 0.
func TestGneListEmpty(t *testing.T) {
	var out, errb bytes.Buffer
	code := runGNE([]string{"list", "--dir", filepath.Join(t.TempDir(), "kosong")}, &out, &errb)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "no extensions installed") {
		t.Errorf("output = %q", out.String())
	}
}

// TestGneInstallNoArgs — wrong arity → exit 2.
func TestGneInstallNoArgs(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runGNE([]string{"install"}, &out, &errb); code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if code := runGNE([]string{"pack"}, &out, &errb); code != 2 {
		t.Errorf("pack code = %d, want 2", code)
	}
}

// TestGneRemoveNotInstalled — remove a name that is not installed → exit 1 + message.
func TestGneRemoveNotInstalled(t *testing.T) {
	var out, errb bytes.Buffer
	dir := filepath.Join(t.TempDir(), "gne")
	os.MkdirAll(dir, 0o755)
	code := runGNE([]string{"remove", "tidakada", "--dir", dir}, &out, &errb)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "gar gne remove:") {
		t.Errorf("stderr = %q", errb.String())
	}
}
