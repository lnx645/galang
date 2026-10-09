//go:build cgo

package interp

// Uji ekstensi resmi ext/redis: mengompilasi sumber asli (bukan salinan),
// menjalankannya melalui `use "redis"` di program GaLang nyata, dengan
// server RESP2 tiruan in-process (tanpa jaringan). Satu uji tambahan
// memakai redis-server sungguhan bila tersedia (dev-only, auto-skip).

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- server RESP2 tiruan ----

// storeRESP adalah penyimpanan mini sekelas Redis untuk uji.
type storeRESP struct {
	kv     map[string]string
	hashes map[string]map[string]string
	lists  map[string][]string
}

func storeBaruRESP() *storeRESP {
	return &storeRESP{
		kv:     map[string]string{},
		hashes: map[string]map[string]string{},
		lists:  map[string][]string{},
	}
}

func bulkRESP(v string) string {
	return fmt.Sprintf("$%d\r\n%s\r\n", len(v), v)
}

// balasRESP mengeksekusi satu perintah dan menghasilkan balasan RESP mentah.
func balasRESP(st *storeRESP, cmd []string) string {
	if len(cmd) == 0 {
		return "-ERR perintah kosong\r\n"
	}
	switch strings.ToUpper(cmd[0]) {
	case "PING":
		return "+PONG\r\n"
	case "SET":
		if len(cmd) != 3 {
			return "-ERR wrong number of arguments for 'set'\r\n"
		}
		st.kv[cmd[1]] = cmd[2]
		return "+OK\r\n"
	case "GET":
		if len(cmd) != 2 {
			return "-ERR wrong number of arguments for 'get'\r\n"
		}
		v, ok := st.kv[cmd[1]]
		if !ok {
			return "$-1\r\n"
		}
		return bulkRESP(v)
	case "DEL":
		n := 0
		for _, k := range cmd[1:] {
			if _, ok := st.kv[k]; ok {
				delete(st.kv, k)
				n++
				continue
			}
			if _, ok := st.hashes[k]; ok {
				delete(st.hashes, k)
				n++
				continue
			}
			if _, ok := st.lists[k]; ok {
				delete(st.lists, k)
				n++
				continue
			}
		}
		return fmt.Sprintf(":%d\r\n", n)
	case "EXISTS":
		n := 0
		for _, k := range cmd[1:] {
			if _, ok := st.kv[k]; ok {
				n++
				continue
			}
			if _, ok := st.hashes[k]; ok {
				n++
				continue
			}
			if _, ok := st.lists[k]; ok {
				n++
				continue
			}
		}
		return fmt.Sprintf(":%d\r\n", n)
	case "INCR":
		if len(cmd) != 2 {
			return "-ERR wrong number of arguments for 'incr'\r\n"
		}
		v, _ := st.kv[cmd[1]]
		var n int64
		if v != "" {
			parsed, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return "-ERR value is not an integer or out of range\r\n"
			}
			n = parsed
		}
		n++
		st.kv[cmd[1]] = strconv.FormatInt(n, 10)
		return fmt.Sprintf(":%d\r\n", n)
	case "EXPIRE":
		if len(cmd) != 3 {
			return "-ERR wrong number of arguments for 'expire'\r\n"
		}
		if _, ok := st.kv[cmd[1]]; ok {
			return ":1\r\n"
		}
		if _, ok := st.hashes[cmd[1]]; ok {
			return ":1\r\n"
		}
		if _, ok := st.lists[cmd[1]]; ok {
			return ":1\r\n"
		}
		return ":0\r\n"
	case "HSET":
		if len(cmd) != 4 {
			return "-ERR wrong number of arguments for 'hset'\r\n"
		}
		h, ok := st.hashes[cmd[1]]
		if !ok {
			h = map[string]string{}
			st.hashes[cmd[1]] = h
		}
		_, ada := h[cmd[2]]
		h[cmd[2]] = cmd[3]
		if ada {
			return ":0\r\n"
		}
		return ":1\r\n"
	case "HGET":
		if len(cmd) != 3 {
			return "-ERR wrong number of arguments for 'hget'\r\n"
		}
		h, ok := st.hashes[cmd[1]]
		if !ok {
			return "$-1\r\n"
		}
		v, ok := h[cmd[2]]
		if !ok {
			return "$-1\r\n"
		}
		return bulkRESP(v)
	case "LPUSH":
		if len(cmd) < 3 {
			return "-ERR wrong number of arguments for 'lpush'\r\n"
		}
		l, ok := st.lists[cmd[1]]
		if !ok {
			l = []string{}
		}
		for i := 2; i < len(cmd); i++ {
			l = append([]string{cmd[i]}, l...)
		}
		st.lists[cmd[1]] = l
		return fmt.Sprintf(":%d\r\n", len(l))
	case "LRANGE":
		if len(cmd) != 4 {
			return "-ERR wrong number of arguments for 'lrange'\r\n"
		}
		l := st.lists[cmd[1]]
		start, err1 := strconv.Atoi(cmd[2])
		stop, err2 := strconv.Atoi(cmd[3])
		if err1 != nil || err2 != nil {
			return "-ERR value is not an integer or out of range\r\n"
		}
		n := len(l)
		if start < 0 {
			start += n
		}
		if stop < 0 {
			stop += n
		}
		if start < 0 {
			start = 0
		}
		if stop > n-1 {
			stop = n - 1
		}
		var out []string
		for i := start; i <= stop && i < n; i++ {
			if i >= 0 {
				out = append(out, l[i])
			}
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "*%d\r\n", len(out))
		for _, v := range out {
			sb.WriteString(bulkRESP(v))
		}
		return sb.String()
	case "KEYS":
		if len(cmd) != 2 {
			return "-ERR wrong number of arguments for 'keys'\r\n"
		}
		pat := cmd[1]
		var semua []string
		kumpul := func(k string) {
			if strings.HasSuffix(pat, "*") {
				if strings.HasPrefix(k, pat[:len(pat)-1]) {
					semua = append(semua, k)
				}
			} else if k == pat {
				semua = append(semua, k)
			}
		}
		for k := range st.kv {
			kumpul(k)
		}
		for k := range st.hashes {
			kumpul(k)
		}
		for k := range st.lists {
			kumpul(k)
		}
		sort.Strings(semua)
		var sb strings.Builder
		fmt.Fprintf(&sb, "*%d\r\n", len(semua))
		for _, v := range semua {
			sb.WriteString(bulkRESP(v))
		}
		return sb.String()
	default:
		return "-ERR unknown command '" + cmd[0] + "'\r\n"
	}
}

// bacaPerintahRESP membaca satu array RESP (nama perintah + argumen).
func bacaPerintahRESP(br *bufio.Reader) ([]string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("bukan array RESP: %q", line)
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil || n < 0 {
		return nil, fmt.Errorf("array tidak valid: %q", line)
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		hdr, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		hdr = strings.TrimRight(hdr, "\r\n")
		if !strings.HasPrefix(hdr, "$") {
			return nil, fmt.Errorf("bulk tidak valid: %q", hdr)
		}
		l, err := strconv.Atoi(hdr[1:])
		if err != nil || l < 0 {
			return nil, fmt.Errorf("panjang bulk tidak valid: %q", hdr)
		}
		buf := make([]byte, l+2)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:l]))
	}
	return args, nil
}

// serverRESP menjalankan server mini in-process dan mengembalikan
// (host, port) untuk dipakai redis.connect.
func serverRESP(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	st := storeBaruRESP()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					args, err := bacaPerintahRESP(br)
					if err != nil {
						return
					}
					if _, err := io.WriteString(c, balasRESP(st, args)); err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return "127.0.0.1", portDari(ln)
}

func portDari(ln net.Listener) int {
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	n, _ := strconv.Atoi(p)
	return n
}

// serverGantung menerima koneksi tapi tidak pernah membalas (uji timeout).
func serverGantung(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	t.Cleanup(func() {
		close(done)
		ln.Close()
		wg.Wait()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				select {
				case <-done:
				case <-time.After(30 * time.Second):
				}
			}(c)
		}
	}()
	return "127.0.0.1", portDari(ln)
}

// serverTutupCepat menerima lalu langsung menutup koneksi (uji 502).
func serverTutupCepat(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return "127.0.0.1", portDari(ln)
}

// portMati mengembalikan port yang tidak melayani (listener dibuka lalu
// ditutup) untuk uji koneksi ditolak.
func portMati(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := portDari(ln)
	ln.Close()
	return p
}

// bangunRedisC mengompilasi ext/redis/redis.c (sumber asli) menjadi
// <dir>/gne/redis.so. Gagal bila gcc tidak ada — ini kode kita, bukan fixture.
func bangunRedisC(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc tidak tersedia")
	}
	src := filepath.Join("..", "..", "..", "ext", "redis", "redis.c")
	inc := filepath.Join("..", "..", "..", "include")
	out := filepath.Join(dir, "gne", "redis.so")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("gcc", "-shared", "-fPIC", "-Wall", "-Wextra",
		"-I", inc, "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gagal mengompilasi ext/redis/redis.c: %v\n%s", err, b)
	}
}

// TestRedisPenuh menguji seluruh lingkup API + pemetaan tipe + galat
// protokol dan pemakaian ulang slot koneksi.
func TestRedisPenuh(t *testing.T) {
	host, port := serverRESP(t)
	dir := t.TempDir()
	bangunRedisC(t, dir)

	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "redis"

$r = redis.connect("%s", %d, 3000)
print($r.ping())
print($r.set("ku:1", "a"))
$r.set("ku:2", "b")
print($r.get("ku:1"))
print($r.get("tidakada"))
$r.set("n", 41)
print($r.incr("n"))
print($r.exists("ku:1"))
print($r.exists("tidakada"))
print($r.expire("ku:1", 60))
print($r.hset("h", "f", "v"))
print($r.hset("h", "f", "w"))
print($r.hget("h", "f"))
print($r.hget("h", "z"))
$r.lpush("l", "a")
$r.lpush("l", "b")
print($r.lrange("l", 0, -1))
print($r.keys("ku:*"))
print($r.del("n"))
print($r.cmd("PING"))

try {
  $r.cmd("NGAWUR")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}

try {
  $r.set("k", true)
  print("tidak")
} catch e {
  print(e.code)
}

print($r.close())
try {
  $r.get("ku:1")
  print("tidak")
} catch e {
  print(e.message)
}

// Slot yang sama dipakai koneksi baru; objek lama harus tetap basi.
$r2 = redis.connect("%s", %d, 3000)
print($r2.get("ku:1"))
try {
  $r.get("ku:1")
  print("tidak")
} catch e {
  print("basi")
}
print($r2.close())
print($r2.close())
`, host, port, host, port))
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	want := strings.Join([]string{
		"true",   // ping
		"OK",     // set balas +OK
		"a",      // get ada
		"null",   // get absen → null
		"42",     // incr 41 → 42
		"true",   // exists ada
		"false",  // exists absen
		"true",   // expire (kunci ada)
		"1",      // hset baru
		"0",      // hset perbarui
		"w",      // hget sesudah perbarui
		"null",   // hget medan absen
		"[b, a]", // lrange 0..-1
		"[ku:1, ku:2]",
		"1",               // del
		"PONG",            // cmd PING
		"redis_error/500", // -ERR server → throw
		"type_error",      // argumen bool ditolak
		"true",            // close pertama
		"koneksi sudah ditutup atau tidak valid",
		"a",     // koneksi baru pada slot sama
		"basi",  // objek lama tetap basi walau slot terpakai ulang
		"true",  // close r2
		"false", // close ulang → idempoten
	}, "\n")
	if got := strings.TrimRight(out, "\n"); got != want {
		t.Errorf("keluaran salah:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestRedisGalatKoneksi menguji tiga jalur galat jaringan: ditolak (502),
// server menutup di tengah (502), dan timeout (504) — semuanya catchable.
func TestRedisGalatKoneksi(t *testing.T) {
	// 1. Koneksi ditolak.
	dir := t.TempDir()
	bangunRedisC(t, dir)
	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "redis"
try {
  $r = redis.connect("127.0.0.1", %d, 1000)
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
`, portMati(t)))
	if err != nil {
		t.Fatalf("eval ditolak: %v", err)
	}
	if got := strings.TrimSpace(out); got != "redis_error/502" {
		t.Errorf("ditolak = %q, mau redis_error/502", got)
	}

	// 2. Server menutup koneksi segera.
	host, port := serverTutupCepat(t)
	out, err = jalankanMain(t, dir, fmt.Sprintf(`
use "redis"
try {
  $r = redis.connect("%s", %d, 1000)
  $r.get("x")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
`, host, port))
	if err != nil {
		t.Fatalf("eval putus: %v", err)
	}
	if got := strings.TrimSpace(out); got != "redis_error/502" {
		t.Errorf("putus = %q, mau redis_error/502", got)
	}

	// 3. Server gantung → timeout 504.
	host, port = serverGantung(t)
	out, err = jalankanMain(t, dir, fmt.Sprintf(`
use "redis"
$t0 = str(len("x"))
try {
  $r = redis.connect("%s", %d, 300)
  $r.get("x")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
`, host, port))
	if err != nil {
		t.Fatalf("eval timeout: %v", err)
	}
	if got := strings.TrimSpace(out); got != "redis_error/504" {
		t.Errorf("timeout = %q, mau redis_error/504", got)
	}
}

// TestRedisHidup menguji terhadap redis-server sungguhan bila tersedia
// di mesin (dev-only); otomatis skip pada mesin tanpa redis.
func TestRedisHidup(t *testing.T) {
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skip("redis-server tidak tersedia")
	}
	dir := t.TempDir()
	bangunRedisC(t, dir)

	// Pilih port bebas, lalu jalankan redis-server di port itu.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := portDari(ln)
	ln.Close()
	tmp := t.TempDir()
	cmd := exec.Command("redis-server",
		"--port", strconv.Itoa(port),
		"--bind", "127.0.0.1",
		"--save", "",
		"--appendonly", "no",
		"--dir", tmp,
		"--logfile", filepath.Join(tmp, "redis.log"),
	)
	if err := cmd.Start(); err != nil {
		t.Skipf("gagal menjalankan redis-server: %v", err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	// Tunggu siap (maks 5 detik).
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	siap := false
	for i := 0; i < 50; i++ {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			siap = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !siap {
		b, _ := os.ReadFile(filepath.Join(tmp, "redis.log"))
		t.Fatalf("redis-server tidak siap dalam 5 detik:\n%s", b)
	}

	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "redis"
$r = redis.connect("127.0.0.1", %d, 3000)
print($r.ping())
$r.set("k", "v")
print($r.get("k"))
$r.set("n", 10)
print($r.incr("n"))
print($r.exists("k"))
$r.hset("h", "f", "x")
print($r.hget("h", "f"))
$r.lpush("q", "a")
$r.lpush("q", "b")
print($r.lrange("q", 0, -1))
print($r.cmd("PING"))
$r.set("p1", "1")
print($r.keys("p1"))
print($r.del("p1"))
print($r.expire("k", 60))
print($r.close())
`, port))
	if err != nil {
		t.Fatalf("eval live: %v", err)
	}
	want := strings.Join([]string{
		"true", "v", "11", "true", "x",
		"[b, a]", "PONG", "[p1]", "1", "true", "true",
	}, "\n")
	if got := strings.TrimRight(out, "\n"); got != want {
		t.Errorf("keluaran live salah:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
