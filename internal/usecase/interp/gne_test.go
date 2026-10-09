//go:build cgo

package interp

// GNE integration: builds a real C extension with gcc, loads it via
// `use`, then verifies the whole ABI surface — values, methods,
// catchable throws, callbacks, retain, object key enumeration (ABI 2),
// and path discovery.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureGNE is a complete test extension (built with -Wall -Wextra).
const fixtureGNE = `#include "gne.h"
#include <stdint.h>

static const gne_host_api *api;
static int64_t call_count;
static uint64_t g_stash;

static int counter_next(gne_ctx *ctx, int argc, const gne_handle *argv,
			gne_handle *ret)
{
	int64_t n = 0;
	gne_handle v;
	(void)argc;
	if (api->obj_get(ctx, argv[0], "n", &v) == 0)
		api->get_int(ctx, v, &n);
	n++;
	v = api->int_new(ctx, n);
	api->obj_set(ctx, argv[0], "n", v);
	*ret = api->int_new(ctx, n);
	return 0;
}

static int add(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	int64_t a, b;
	(void)argc;
	if (api->get_int(ctx, argv[0], &a) != 0 ||
	    api->get_int(ctx, argv[1], &b) != 0) {
		api->throw(ctx, "type_error", 500, "add() requires two integers");
		return 1;
	}
	call_count++;
	*ret = api->int_new(ctx, a + b);
	return 0;
}

static int boom(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	(void)argc; (void)argv; (void)ret;
	api->throw(ctx, "hello_error", 418, "deliberate explosion");
	return 1;
}

static int join(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	char buf[256];
	size_t n = 0;
	int i;
	buf[0] = 0;
	for (i = 0; i < argc; i++) {
		size_t len;
		if (api->str_len(ctx, argv[i], &len) != 0)
			continue;
		if (len + 1 >= sizeof(buf) - n)
			break;
		api->str_copy(ctx, argv[i], buf + n, sizeof(buf) - n);
		n += len;
	}
	*ret = api->string(ctx, buf, n);
	return 0;
}

static int twice(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	gne_handle one, out;
	(void)argc;
	one = api->int_new(ctx, 1);
	if (api->call(ctx, argv[0], 1, &one, &out) != 0)
		return 1;
	*ret = out;
	return 0;
}

static int stash(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	(void)argc;
	api->retain(ctx, argv[0]);
	api->set_data(ctx, (void *)1);
	g_stash = (uint64_t)argv[0];
	*ret = api->bool_new(ctx, 1);
	return 0;
}

static int unstash(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	(void)argc; (void)argv;
	if (!g_stash) {
		*ret = api->null(ctx);
		return 0;
	}
	api->retain(ctx, g_stash);
	*ret = g_stash;
	return 0;
}

static int calls(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	(void)argc; (void)argv;
	*ret = api->int_new(ctx, call_count);
	return 0;
}

/* ABI 2 surface: enumerate object keys via api->obj_keys and return
 * them joined with commas — also proves insertion order is preserved. */
static int keys(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	gne_handle ks;
	size_t n = 0, i, used = 0;
	char buf[256];
	(void)argc;
	if (api->obj_keys(ctx, argv[0], &ks) != 0) {
		api->throw(ctx, "type_error", 500, "keys() expects an object");
		return 1;
	}
	api->len(ctx, ks, &n);
	buf[0] = 0;
	for (i = 0; i < n; i++) {
		gne_handle k;
		size_t len = 0;
		if (api->arr_get(ctx, ks, i, &k) != 0 ||
		    api->str_len(ctx, k, &len) != 0)
			continue;
		if (used && used + 1 < sizeof(buf))
			buf[used++] = ',';
		if (used + len + 1 >= sizeof(buf))
			break;
		api->str_copy(ctx, k, buf + used, sizeof(buf) - used);
		used += len;
	}
	*ret = api->string(ctx, buf, used);
	return 0;
}

int gne_module_init(const gne_host_api *a, gne_ctx *ctx, gne_handle *out)
{
	gne_handle counter, ns;
	api = a;
	if (api->abi != GNE_ABI) {
		api->throw(ctx, "gne_abi", 500, "ABI mismatch");
		return 1;
	}
	api->define_fn(ctx, "add", 2, 2, add);
	api->define_fn(ctx, "boom", 0, 0, boom);
	api->define_fn(ctx, "join", 0, -1, join);
	api->define_fn(ctx, "twice", 1, 1, twice);
	api->define_fn(ctx, "stash", 1, 1, stash);
	api->define_fn(ctx, "unstash", 0, 0, unstash);
	api->define_fn(ctx, "calls", 0, 0, calls);
	api->define_fn(ctx, "keys", 1, 1, keys);

	counter = api->object(ctx);
	api->obj_set(ctx, counter, "n", api->int_new(ctx, 0));
	api->define_method(ctx, counter, "next", 0, 0, counter_next, counter);
	ns = api->module(ctx);
	api->obj_set(ctx, ns, "counter", counter);

	*out = api->module(ctx);
	return 0;
}
`

// fixtureLegacyABI simulates an extension compiled against ABI 1 (the
// pre-obj_keys header): it checks api->abi with strict equality, exactly
// as the extensions shipped before the ABI 2 bump did.
const fixtureLegacyABI = `#include "gne.h"
#include <stdint.h>

static const gne_host_api *api;

static int ping(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	(void)argc; (void)argv;
	*ret = api->string(ctx, "pong", 4);
	return 0;
}

int gne_module_init(const gne_host_api *a, gne_ctx *ctx, gne_handle *out)
{
	api = a;
	if (api->abi != 1) {
		api->throw(ctx, "gne_abi", 500, "extension expects ABI 1");
		return 1;
	}
	api->define_fn(ctx, "ping", 0, 0, ping);
	*out = api->module(ctx);
	return 0;
}
`

// bangunEkstensi compiles the fixture into <dir>/<name>.so; skips when
// gcc is not available.
func bangunEkstensi(t *testing.T, dir, name, src string) string {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cPath := filepath.Join(dir, name+".c")
	if err := os.WriteFile(cPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, name+".so")
	inc := filepath.Join("..", "..", "..", "include")
	cmd := exec.Command("gcc", "-shared", "-fPIC", "-Wall", "-Wextra",
		"-I", inc, "-o", out, cPath)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot compile the fixture: %v\n%s", err, b)
	}
	return out
}

// TestGNEFull exercises the entire ABI surface through a real GaLang
// program.
func TestGNEFull(t *testing.T) {
	dir := t.TempDir()
	bangunEkstensi(t, filepath.Join(dir, "gne"), "hello", fixtureGNE)

	out, err := jalankanMain(t, dir, `
use "hello"

print(hello.add(20, 42))
print(hello.join("a", "b", "c"))

try {
  hello.boom()
  print("no")
} catch e {
  print(e.code + "/" + str(e.status) + "/" + e.message)
}

try {
  hello.add("x", 1)
} catch e {
  print("type:" + e.code)
}

$c = hello.counter
print($c.next())
print($c.next())

print(hello.twice(fn($x) {
  return $x * 10
}))

hello.stash(999)
print(hello.unstash())
print(hello.calls())
print(hello.keys({"b": 1, "a": 2, "c": 3}))
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	want := strings.Join([]string{
		"62",
		"abc",
		"hello_error/418/deliberate explosion",
		"type:type_error",
		"1",
		"2",
		"10",
		"999",
		"1",
		"b,a,c", // obj_keys preserves insertion order
	}, "\n")
	if strings.TrimSpace(out) != want {
		t.Errorf("output = %q\nwant %q", strings.TrimSpace(out), want)
	}
}

// TestGNEArityHost — the arity error comes from the host, not from C.
func TestGNEArityHost(t *testing.T) {
	dir := t.TempDir()
	bangunEkstensi(t, filepath.Join(dir, "gne"), "hello", fixtureGNE)
	_, err := jalankanMain(t, dir, `
use "hello"
hello.add(1)
`)
	if err == nil || !strings.Contains(err.Error(), "expects at least 2") {
		t.Errorf("err = %v, want the host's arity error", err)
	}
}

// TestGNEDiscovery — order: a .ga file wins over GNE, explicit paths
// work, and GNE_PATH is found when the project directory is empty.
func TestGNEDiscovery(t *testing.T) {
	dir := t.TempDir()

	// 1. a .ga file wins over a .so with the same name
	tulisFile(t, dir, "hello.ga", `fn add($a, $b) { return 111 }`)
	bangunEkstensi(t, filepath.Join(dir, "gne"), "hello", fixtureGNE)
	out, err := jalankanMain(t, dir, `
use "hello"
print(hello.add(1, 2))
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if strings.TrimSpace(out) != "111" {
		t.Errorf("the .ga file must win, output = %q", out)
	}

	// 2. explicit path use "gne/hello.so" (namespace from the basename)
	tulisFile(t, dir, "main2.ga", "")
	out, err = jalankanMain(t, dir, `
use "gne/hello.so"
print(hello.add(1, 2))
`)
	if err != nil {
		t.Fatalf("explicit eval: %v", err)
	}
	if strings.TrimSpace(out) != "3" {
		t.Errorf("explicit path, output = %q", out)
	}
}

// TestGNEPathEnv — $GNE_PATH is used when the project has no ./gne.
func TestGNEPathEnv(t *testing.T) {
	dir := t.TempDir()
	extDir := t.TempDir()
	bangunEkstensi(t, extDir, "hello", fixtureGNE)
	t.Setenv("GNE_PATH", extDir)
	out, err := jalankanMain(t, dir, `
use "hello"
print(hello.add(4, 5))
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if strings.TrimSpace(out) != "9" {
		t.Errorf("output = %q, want 9", out)
	}
}

// TestGNELegacyDir — the old ~/.garurda/gne location (pre-v0.7.0
// rebrand) is still searched by `use` when the active directory does
// not contain the extension yet.
func TestGNELegacyDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	lama := filepath.Join(home, ".garurda", "gne")
	bangunEkstensi(t, lama, "hello", fixtureGNE)
	out, err := jalankanMain(t, dir, `
use "hello"
print(hello.add(6, 7))
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if strings.TrimSpace(out) != "13" {
		t.Errorf("output = %q, want 13 (the legacy-location extension must load)", out)
	}
}

// TestGNELegacyABIView — a package whose sidecar declares gne_abi 1 is
// served a legacy table view (identical layout, older abi number) so
// extensions built before the ABI bump keep loading after a `gar`
// upgrade; without a sidecar the host assumes the current ABI and the
// extension's strict equality check rejects it loudly.
func TestGNELegacyABIView(t *testing.T) {
	dir := t.TempDir()
	gneDir := filepath.Join(dir, "gne")
	so := bangunEkstensi(t, gneDir, "tua", fixtureLegacyABI)
	sidecar := strings.TrimSuffix(so, ".so") + ".gne.json"
	if err := os.WriteFile(sidecar,
		[]byte(`{"name":"tua","version":"0.0.1","gne_abi":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := jalankanMain(t, dir, `
use "tua"
print(tua.ping())
`)
	if err != nil {
		t.Fatalf("eval with a legacy sidecar: %v", err)
	}
	if strings.TrimSpace(out) != "pong" {
		t.Errorf("output = %q, want pong (the legacy view must load ABI 1 extensions)", out)
	}

	// No sidecar → the host serves the current view; the old
	// extension's own check must reject it with a clear error.
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	_, err = jalankanMain(t, dir, `
use "tua"
`)
	if err == nil || !strings.Contains(err.Error(), "expects ABI 1") {
		t.Errorf("err = %v, want the extension's own ABI rejection", err)
	}
}

// TestGNENotFound — the error message lists every GNE candidate.
func TestGNENotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := jalankanMain(t, dir, `
use "hilang"
`)
	if err == nil || !strings.Contains(err.Error(), filepath.Join("gne", "hilang")) {
		t.Errorf("err = %v, want it to mention the gne/hilang.so candidate", err)
	}
}

// TestGNENotAnExtension — a .so without gne_module_init is rejected clearly.
func TestGNENotAnExtension(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}
	dir := t.TempDir()
	gneDir := filepath.Join(dir, "gne")
	if err := os.MkdirAll(gneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// a valid .so but without the gne_module_init symbol
	cPath := filepath.Join(dir, "kosong.c")
	if err := os.WriteFile(cPath, []byte("int f(void){return 0;}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command("gcc", "-shared", "-fPIC", "-o",
		filepath.Join(gneDir, "kosong.so"), cPath).CombinedOutput(); err != nil {
		t.Skipf("cannot compile: %v\n%s", err, b)
	}
	_, err := jalankanMain(t, dir, `
use "kosong"
`)
	if err == nil || !strings.Contains(err.Error(), "is not a GNE extension") {
		t.Errorf("err = %v, want 'is not a GNE extension'", err)
	}
}

// TestGNEBuiltinsStillWin — `use "strings"` is untouched by GNE.
func TestGNEBuiltinsStillWin(t *testing.T) {
	dir := t.TempDir()
	bangunEkstensi(t, filepath.Join(dir, "gne"), "strings", fixtureGNE)
	out, err := jalankanMain(t, dir, `
use "strings"
print(strings.upper("ok"))
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if strings.TrimSpace(out) != "OK" {
		t.Errorf("output = %q, want OK", out)
	}
}
