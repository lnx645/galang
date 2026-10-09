//go:build cgo

package interp

// Tests the official ext/httpclient extension: compiles the original source
// and runs it against an in-process fake HTTP server (httptest)
// — methods, headers, chunked, redirects, 4xx status without errors, plus
// error paths: https rejected, connection refused (502), timeout (504),
// CRLF injection, and managed headers that must not be overridden.

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

// bangunHttpClientC compiles ext/httpclient/httpclient.c (the original
// source) into <dir>/gne/httpclient.so. Fails if gcc is missing —
// this is our code, not a fixture.
func bangunHttpClientC(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
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
		t.Fatalf("failed to compile ext/httpclient/httpclient.c: %v\n%s", err, b)
	}
}

// servicerHTTP builds a test server with routes:
//
//	GET/POST/PUT /echo → method + key headers + body (cross-check
//	                     from the server side against what the client sent)
//	/chunk            → body written in stages (Transfer-Encoding chunked)
//	/besar            → 130 KB (past the 64 KB read-stop threshold)
//	/r1 → 302 /r2?a=1  → relative redirect
//	/r301             → 301 (POST changed to GET)
//	/r307             → 307 (POST preserved)
//	/r2               → method + current query
//	/loop             → 302 to itself
//	/lambat           → sleeps 1.5 seconds (timeout test)
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
		for _, s := range []string{"onetwo", "threefive"} {
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

// TestHttpClientBasic tests GET/POST/PUT, header reflection from the
// server side, default content-type, case-insensitive headers, large
// body (over 64 KB), chunked responses, and 4xx status without errors.
func TestHttpClientBasic(t *testing.T) {
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
$p = httpclient.post("%s/echo", "name=budi", "text/plain")
print($p.body)
$d = httpclient.post("%s/echo", "plain")
print($d.body)
$u = httpclient.request("PUT", "%s/echo", "content", ["X-Api-Key: secret"])
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
		"GET|galang-httpclient|" + alamat, // UA + Host accepted by the server
		"text/plain; charset=utf-8",
		"text/plain; charset=utf-8", // case-insensitive
		"null",                      // absent header → null
		"POST|text/plain|name=budi",
		"POST|application/octet-stream|plain", // default content-type
		"PUT|secret|content",
		"200",
		"133120", // full 130 KB (130×1024; above the 64 KB read-stop threshold)
		"onetwothreefive",
		"404",
		"false",
	}, "\n")
	if got := strings.TrimRight(out, "\n"); got != want {
		t.Errorf("wrong output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestHttpClientRedirect tests relative Location resolution, the
// 301/302/303 method-change rule (POST → GET without body), 307
// preserving method+body, redirect counting, and the five-redirect
// limit.
func TestHttpClientRedirect(t *testing.T) {
	srv := servicerHTTP(t)
	dir := t.TempDir()
	bangunHttpClientC(t, dir)

	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "httpclient"
$r1 = httpclient.get("%s/r1")
print($r1.body)
print($r1.redirects)
print($r1.url)
$p3 = httpclient.post("%s/r301", "old")
print($p3.body)
$p7 = httpclient.post("%s/r307", "sent")
print($p7.body)
print($p7.redirects)
try {
  httpclient.get("%s/loop")
  print("nope")
} catch e {
  print(e.code + "/" + str(e.status))
}
`, srv.URL, srv.URL, srv.URL, srv.URL))
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	want := strings.Join([]string{
		"GET|a=1|", // Location relative to the request URL
		"1",        // one redirect
		srv.URL + "/r2?a=1",
		"GET||",      // 301: POST changed to GET, body dropped
		"POST||sent", // 307: method and body preserved
		"1",
		"httpclient_error/500", // 5-redirect limit
	}, "\n")
	if got := strings.TrimRight(out, "\n"); got != want {
		t.Errorf("wrong output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestHttpClientError tests the error paths: https rejected
// (type_error), connection refused (502), timeout (504), non-http(s)
// url, CRLF injection in extra headers, managed-header override, and
// invalid timeout — all catchable without killing the process.
func TestHttpClientError(t *testing.T) {
	srv := servicerHTTP(t)
	dir := t.TempDir()
	bangunHttpClientC(t, dir)

	out, err := jalankanMain(t, dir, fmt.Sprintf(`
use "httpclient"
try {
  httpclient.get("https://contoh.id/")
  print("nope")
} catch e {
  print(e.code)
}
try {
  httpclient.get("http://127.0.0.1:%d/", 500)
  print("nope")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  httpclient.get("%s/lambat", 300)
  print("nope")
} catch e {
  print(e.code + "/" + str(e.status))
}
try {
  httpclient.get("bukanurl")
  print("nope")
} catch e {
  print(e.code)
}
try {
  httpclient.request("GET", "%s/echo", null, ["X-A: b\nX-B: c"])
  print("nope")
} catch e {
  print(e.code)
}
try {
  httpclient.request("GET", "%s/echo", null, ["Host: jahat"])
  print("nope")
} catch e {
  print(e.code)
}
try {
  httpclient.get("%s/echo", -5)
  print("nope")
} catch e {
  print(e.code)
}
`, portMati(t), srv.URL, srv.URL, srv.URL, srv.URL))
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	want := strings.Join([]string{
		"type_error",           // https rejected (no built-in TLS)
		"httpclient_error/502", // connection refused
		"httpclient_error/504", // read timeout
		"type_error",           // not an http:// url
		"type_error",           // CRLF injection rejected
		"type_error",           // managed Host must not be overridden
		"type_error",           // negative timeout
	}, "\n")
	if got := strings.TrimRight(out, "\n"); got != want {
		t.Errorf("wrong output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
