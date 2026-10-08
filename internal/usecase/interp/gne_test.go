//go:build cgo

package interp

// Integrasi GNE: membangun ekstensi C nyata dengan gcc, memuatnya lewat
// `use`, lalu memverifikasi seluruh permukaan ABI v1 — nilai, method,
// throw catchable, callback, retain, dan penemuan path.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureGNE adalah ekstensi uji lengkap (dibangun dengan -Wall -Wextra).
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
		api->throw(ctx, "type_error", 500, "add() butuh dua integer");
		return 1;
	}
	call_count++;
	*ret = api->int_new(ctx, a + b);
	return 0;
}

static int boom(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	(void)argc; (void)argv; (void)ret;
	api->throw(ctx, "hello_error", 418, "sengaja meledak");
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

int gne_module_init(const gne_host_api *a, gne_ctx *ctx, gne_handle *out)
{
	gne_handle counter, ns;
	api = a;
	if (api->abi != GNE_ABI) {
		api->throw(ctx, "gne_abi", 500, "ABI berbeda");
		return 1;
	}
	api->define_fn(ctx, "add", 2, 2, add);
	api->define_fn(ctx, "boom", 0, 0, boom);
	api->define_fn(ctx, "join", 0, -1, join);
	api->define_fn(ctx, "twice", 1, 1, twice);
	api->define_fn(ctx, "stash", 1, 1, stash);
	api->define_fn(ctx, "unstash", 0, 0, unstash);
	api->define_fn(ctx, "calls", 0, 0, calls);

	counter = api->object(ctx);
	api->obj_set(ctx, counter, "n", api->int_new(ctx, 0));
	api->define_method(ctx, counter, "next", 0, 0, counter_next, counter);
	ns = api->module(ctx);
	api->obj_set(ctx, ns, "counter", counter);

	*out = api->module(ctx);
	return 0;
}
`

// bangunEkstensi mengompilasi fixture menjadi <dir>/<name>.so; skip bila
// gcc tidak tersedia.
func bangunEkstensi(t *testing.T, dir, name, src string) string {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc tidak tersedia")
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
		t.Skipf("gagal mengompilasi fixture: %v\n%s", err, b)
	}
	return out
}

// TestGNEPenuh menguji seluruh permukaan ABI lewat program Garurda nyata.
func TestGNEPenuh(t *testing.T) {
	dir := t.TempDir()
	bangunEkstensi(t, filepath.Join(dir, "gne"), "hello", fixtureGNE)

	out, err := jalankanMain(t, dir, `
use "hello"

print(hello.add(20, 42))
print(hello.join("a", "b", "c"))

try {
  hello.boom()
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status) + "/" + e.message)
}

try {
  hello.add("x", 1)
} catch e {
  print("tipe:" + e.code)
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
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	want := strings.Join([]string{
		"62",
		"abc",
		"hello_error/418/sengaja meledak",
		"tipe:type_error",
		"1",
		"2",
		"10",
		"999",
		"1",
	}, "\n")
	if strings.TrimSpace(out) != want {
		t.Errorf("output = %q\nwant %q", strings.TrimSpace(out), want)
	}
}

// TestGNEPanggilanTanpaArgumenCukup — galat arity datang dari host, bukan C.
func TestGNEArityHost(t *testing.T) {
	dir := t.TempDir()
	bangunEkstensi(t, filepath.Join(dir, "gne"), "hello", fixtureGNE)
	_, err := jalankanMain(t, dir, `
use "hello"
hello.add(1)
`)
	if err == nil || !strings.Contains(err.Error(), "expects at least 2") {
		t.Errorf("err = %v, want galat arity host", err)
	}
}

// TestGNEDiscovery — urutan: file .ga menang atas GNE, path eksplisit
// bekerja, dan GNE_PATH ditemukan bila direktori proyek kosong.
func TestGNEDiscovery(t *testing.T) {
	dir := t.TempDir()

	// 1. file .ga menang atas .so dengan nama sama
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
		t.Errorf("file .ga harus menang, output = %q", out)
	}

	// 2. path eksplisit use "gne/hello.so" (namespace dari basename)
	tulisFile(t, dir, "main2.ga", "")
	out, err = jalankanMain(t, dir, `
use "gne/hello.so"
print(hello.add(1, 2))
`)
	if err != nil {
		t.Fatalf("eval eksplisit: %v", err)
	}
	if strings.TrimSpace(out) != "3" {
		t.Errorf("path eksplisit, output = %q", out)
	}
}

// TestGNEPathEnv — $GNE_PATH dipakai bila proyek tidak punya ./gne.
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

// TestGNETidakDitemukan — pesan galat menyebut semua kandidat GNE.
func TestGNETidakDitemukan(t *testing.T) {
	dir := t.TempDir()
	_, err := jalankanMain(t, dir, `
use "hilang"
`)
	if err == nil || !strings.Contains(err.Error(), filepath.Join("gne", "hilang")) {
		t.Errorf("err = %v, want menyebut kandidat gne/hilang.so", err)
	}
}

// TestGNEBukanEkstensi — .so tanpa gne_module_init ditolak dengan jelas.
func TestGNEBukanEkstensi(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc tidak tersedia")
	}
	dir := t.TempDir()
	gneDir := filepath.Join(dir, "gne")
	if err := os.MkdirAll(gneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// .so valid tapi tanpa simbol gne_module_init
	cPath := filepath.Join(dir, "kosong.c")
	if err := os.WriteFile(cPath, []byte("int f(void){return 0;}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command("gcc", "-shared", "-fPIC", "-o",
		filepath.Join(gneDir, "kosong.so"), cPath).CombinedOutput(); err != nil {
		t.Skipf("gagal kompilasi: %v\n%s", err, b)
	}
	_, err := jalankanMain(t, dir, `
use "kosong"
`)
	if err == nil || !strings.Contains(err.Error(), "bukan ekstensi GNE") {
		t.Errorf("err = %v, want 'bukan ekstensi GNE'", err)
	}
}

// TestGNEBawaanTetapMenang — `use "strings"` tidak tersentuh GNE.
func TestGNEBawaanTetapMenang(t *testing.T) {
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
