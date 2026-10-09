//go:build cgo

package interp

// Uji ekstensi resmi ext/httpclient: mengompilasi sumber asli lalu
// menjalankannya terhadap server HTTP tiruan in-process (httptest)
// — metode, header, chunked, pengalihan, status4xx tanpa galat, plus
// jalur galat: https ditolak, koneksi ditolak (502), timeout (504),
// injeksi CRLF, dan header terkelola yang tidak boleh ditimpa.

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bangunHttpClientC mengompilasi ext/httpclient/httpclient.c (sumber
// asli) menjadi <dir>/gne/httpclient.so. Gagal bila gcc tidak ada —
// ini kode kita, bukan fixture.
func bangunHttpClientC(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc tidak tersedia")
	}
	src := filepath.Join("..", "..", "..", "ext", "httpclient", "httpclient.c")
	inc := filepath.Join("..", "..", "..", "include")
	out := filepath.Join(dir, "gne", "httpclient.so")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("gcc", "-shared", "-fPIC", "-Wall", "-Wextra",
		"-I", inc, "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gagal mengompilasi ext/httpclient/httpclient.c: %v\n%s", err, b)
	}
}

// servicerHTTP membangun server uji dengan rute:
//
//	GET/POST/PUT /echo → metode + header penting + body (cek silang
//	                     dari sisi server terhadap apa yang dikirim klien)
//	/chunk            → body ditulis bertahap (Transfer-Encoding chunked)
//	/besar            →130 KB (melewati ambang baca berhenti di64 KB)
//	/r1 →302 /r2?a=1  → pengalihan relatif
//	/r301             →301 (POST berubah jadi GET)
//	/r307             →307 (POST dipertahankan)
//	/r2               → metode + query terkini
//	/loop             →302 ke dirinya sendiri
//	/lambat           → tidur1,5 detik (uji timeout)
func servicerHTTP(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch r.Method {
		case http.MethodGet:
			fmt.Fprintf(w, "GET|%s|%s", r.Header.Get("User-Agent"), r.Host)
		case http.MethodPost:
			fmt.Fprintf(w, "POST|%s|%s", r.Header.Get("Content-Type"), b)
		default:
			fmt.Fprintf(w, "%s|%s|%s", r.Method, r.Header.Get("X-Api-Key"), b)
		}
	})
	mux.HandleFunc("/chunk", func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		for _, s := range []string{"satudua", "tigalima"} {
			io.WriteString(w, s)
			if ok {
				f.Flush()
			}
		}
	})
	mux.HandleFunc("/besar", func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), 130*1024))
	})
	mux.HandleFunc("/r1", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/r2?a=1", http.StatusFound)
	})
	mux.HandleFunc("/r301", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/r2", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/r307", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/r2", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/r2", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s|%s|%s", r.Method, r.URL.RawQuery, b)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	mux.HandleFunc("/lambat", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		io.WriteString(w, "lambat")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestHttpClientDasar menguji GET/POST/PUT, ekspresi header terhadap
// sisi server, default content-type, header case-insensitive, body
// besar (di atas64 KB), respons chunked, dan status4xx tanpa galat.
func TestHttpClientDasar(t *testing.T) {
	srv := servicerHTTP(t)
	dir := t.TempDir()
	bangunHttpClientC(t, dir)

	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "httpclient"
$g = httpclient.get("%s/echo")
print($g.status)
print($g.ok)
print($g.body)
print($g.header("content-type"))
print($g.header("CONTENT-TYPE"))
print($g.header("x-tidak-ada"))
$p = httpclient.post("%s/echo", "nama=budi", "text/plain")
print($p.body)
$d = httpclient.post("%s/echo", "polos")
print($d.body)
$u = httpclient.request("PUT", "%s/echo", "isi", ["X-Api-Key: rahasia"])
print($u.body)
print($u.status)
$b = httpclient.get("%s/besar")
print(len($b.body))
$c = httpclient.get("%s/chunk")
print($c.body)
$nf = httpclient.get("%s/tidak-ada")
print($nf.status)
print($nf.ok)
`, srv.URL, srv.URL, srv.URL, srv.URL, srv.URL, srv.URL, srv.URL))
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	alamat := strings.TrimPrefix(srv.URL, "http://")
	want := strings.Join([]string{
		"200",
		"true",
		"GET|galang-httpclient|" + alamat, // UA + Host diterima server
		"text/plain; charset=utf-8",
		"text/plain; charset=utf-8", // case-insensitive
		"null",                      // header absen → null
		"POST|text/plain|nama=budi",
		"POST|application/octet-stream|polos", // default content-type
		"PUT|rahasia|isi",
		"200",
		"133120", // 130 KB utuh (130×1024; di atas ambang baca berhenti64 KB)
		"satuduatigalima",
		"404",
		"false",
	}, "\n")
	if got := strings.TrimRight(out, "\n"); got != want {
		t.Errorf("keluaran salah:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestHttpClientPengalihan menguji resolusi Location relatif, aturan
// ganti-metode301/302/303 (POST → GET tanpa body),307 yang
// mempertahankan metode+body, penghitungan redirects, dan batas lima
// pengalihan.
func TestHttpClientPengalihan(t *testing.T) {
	srv := servicerHTTP(t)
	dir := t.TempDir()
	bangunHttpClientC(t, dir)

	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "httpclient"
$r1 = httpclient.get("%s/r1")
print($r1.body)
print($r1.redirects)
print($r1.url)
$p3 = httpclient.post("%s/r301", "lama")
print($p3.body)
$p7 = httpclient.post("%s/r307", "dikirim")
print($p7.body)
print($p7.redirects)
try {
  httpclient.get("%s/loop")
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
`, srv.URL, srv.URL, srv.URL, srv.URL))
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	want := strings.Join([]string{
		"GET|a=1|", // Location relatif terhadap URL permintaan
		"1",        // satu pengalihan
		srv.URL + "/r2?a=1",
		"GET||",         //301: POST diubah jadi GET, body dibuang
		"POST||dikirim", //307: metode dan body dipertahankan
		"1",
		"httpclient_error/500", // batas5 pengalihan
	}, "\n")
	if got := strings.TrimRight(out, "\n"); got != want {
		t.Errorf("keluaran salah:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestHttpClientGalat menguji jalur galat: https ditolak (type_error),
// koneksi ditolak (502), timeout (504), url bukan http(s), injeksi
// CRLF pada header tambahan, penimpaan header terkelola, dan timeout
// tidak sah — semuanya catchable tanpa mematikan proses.
func TestHttpClientGalat(t *testing.T) {
	srv := servicerHTTP(t)
	dir := t.TempDir()
	bangunHttpClientC(t, dir)

	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "httpclient"
try {
  httpclient.get("https://contoh.id/")
  print("tidak")
} catch e {
  print(e.code)
}
try {
  httpclient.get("http://127.0.0.1:%d/", 500)
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  httpclient.get("%s/lambat", 300)
  print("tidak")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  httpclient.get("bukanurl")
  print("tidak")
} catch e {
  print(e.code)
}
try {
  httpclient.request("GET", "%s/echo", null, ["X-A: b\nX-B: c"])
  print("tidak")
} catch e {
  print(e.code)
}
try {
  httpclient.request("GET", "%s/echo", null, ["Host: jahat"])
  print("tidak")
} catch e {
  print(e.code)
}
try {
  httpclient.get("%s/echo", -5)
  print("tidak")
} catch e {
  print(e.code)
}
`, portMati(t), srv.URL, srv.URL, srv.URL, srv.URL))
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	want := strings.Join([]string{
		"type_error",           // https ditolak (tanpa TLS bawaan)
		"httpclient_error/502", // koneksi ditolak
		"httpclient_error/504", // timeout baca
		"type_error",           // bukan url http://
		"type_error",           // injeksi CRLF ditolak
		"type_error",           // Host dikelola tidak boleh ditimpa
		"type_error",           // timeout negatif
	}, "\n")
	if got := strings.TrimRight(out, "\n"); got != want {
		t.Errorf("keluaran salah:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
