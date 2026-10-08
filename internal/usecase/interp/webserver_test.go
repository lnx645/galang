package interp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"garurda/internal/domain"
)

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		wantOK  bool
		wantID  string
	}{
		{"/", "/", true, ""},
		{"/about", "/about", true, ""},
		{"/about", "/other", false, ""},
		{"/users/{id}", "/users/42", true, "42"},
		{"/users/{id}", "/users/abc", true, "abc"},
		{"/users/{id}", "/users", false, ""},
		{"/a/{b}/c", "/a/x/c", true, "x"},
		{"/a/{b}/c", "/a/x/d", false, ""},
	}
	for _, c := range cases {
		params, ok := matchPattern(c.pattern, c.path)
		if ok != c.wantOK {
			t.Fatalf("matchPattern(%q, %q) ok = %v, want %v", c.pattern, c.path, ok, c.wantOK)
		}
		if !ok {
			continue
		}
		if c.wantID != "" {
			if got := firstParam(params); got != c.wantID {
				t.Fatalf("matchPattern(%q, %q) param = %q, want %q", c.pattern, c.path, got, c.wantID)
			}
		}
	}
}

func firstParam(params map[string]string) string {
	for _, v := range params {
		return v
	}
	return ""
}

func TestWriteResponseScalar(t *testing.T) {
	in := New(nil, nil)
	rec := &testResponseWriter{header: make(http.Header)}
	in.writeResponse(rec, domain.Str("hello"))
	if rec.code != 200 {
		t.Fatalf("status = %d, want 200", rec.code)
	}
	if rec.body != "hello" {
		t.Fatalf("body = %q, want %q", rec.body, "hello")
	}
}

func TestWriteResponseStructured(t *testing.T) {
	in := New(nil, nil)
	obj := domain.NewObj()
	obj.Set("status", domain.Int(201))
	obj.Set("type", domain.Str("application/json"))
	obj.Set("body", domain.Str("{\"ok\":true}"))
	rec := &testResponseWriter{header: make(http.Header)}
	in.writeResponse(rec, obj)
	if rec.code != 201 {
		t.Fatalf("status = %d, want 201", rec.code)
	}
	if rec.header.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type = %q, want application/json", rec.header.Get("Content-Type"))
	}
	if rec.body != "{\"ok\":true}" {
		t.Fatalf("body = %q", rec.body)
	}
}

func TestBuildRequestObj(t *testing.T) {
	in := New(nil, nil)
	u, err := url.Parse("http://x/test?q=1")
	if err != nil {
		t.Fatal(err)
	}
	r := &http.Request{Method: "GET", URL: u, Header: make(http.Header)}
	params := map[string]string{"id": "9"}
	obj := in.buildRequestObj(r, params)
	if v, _ := obj.Get("method"); v.String() != "GET" {
		t.Fatalf("method = %v", v)
	}
	if v, _ := obj.Get("path"); v.String() != "/test" {
		t.Fatalf("path = %v", v)
	}
	qp, _ := obj.Get("query")
	if q, _ := qp.(*domain.Obj).Get("q"); q.String() != "1" {
		t.Fatalf("query.q = %v", q)
	}
	rp, _ := obj.Get("params")
	if id, _ := rp.(*domain.Obj).Get("id"); id.String() != "9" {
		t.Fatalf("params.id = %v", id)
	}
}

// TestWriteThrown pins the thrown-error contract: a domain error answers with
// its own HTTP status and a JSON body; internal errors answer a plain 500.
func TestWriteThrown(t *testing.T) {
	in := New(nil, nil)

	// Domain error → its status + JSON body.
	ev := &domain.ErrorValue{Message: "hilang", Code: "http_error", Status: 404}
	rec := &testResponseWriter{header: make(http.Header)}
	in.writeThrown(rec, in.wrapThrown(ev, domain.Position{}))
	if rec.code != 404 {
		t.Fatalf("status = %d, want 404", rec.code)
	}
	if rec.header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", rec.header.Get("Content-Type"))
	}
	if !strings.Contains(rec.body, `"message":"hilang"`) || !strings.Contains(rec.body, `"status":404`) {
		t.Fatalf("body = %q", rec.body)
	}

	// Internal error → plain 500, no stack trace in the response.
	rec2 := &testResponseWriter{header: make(http.Header)}
	in.writeThrown(rec2, in.errf(domain.Position{}, "boom"))
	if rec2.code != 500 {
		t.Fatalf("status = %d, want 500", rec2.code)
	}
	if strings.Contains(rec2.body, "boom") {
		t.Fatalf("internal error leaked to client: %q", rec2.body)
	}
}

// testResponseWriter is a minimal http.ResponseWriter for tests.
type testResponseWriter struct {
	header http.Header
	code   int
	body   string
}

func (w *testResponseWriter) Header() http.Header { return w.header }
func (w *testResponseWriter) Write(b []byte) (int, error) {
	w.body += string(b)
	return len(b), nil
}
func (w *testResponseWriter) WriteHeader(code int) { w.code = code }

// evalWeb evaluates source that registers routes/middleware and returns
// the interpreter together with its stdout buffer.
func evalWeb(t *testing.T, src string) (*Interp, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	in := New(&out, &out)
	if _, err := in.Eval(src+"\n", "test.ga"); err != nil {
		t.Fatalf("Eval: %v\nsource:\n%s", err, src)
	}
	return in, &out
}

// serveReq sends one synthetic HTTP request to the first matching route,
// exactly as startWebServer would (route match → serveRequest with the
// middleware chain snapshot).
func serveReq(t *testing.T, in *Interp, method, path string) *testResponseWriter {
	t.Helper()
	return serveReqCookie(t, in, method, path, "")
}

// serveReqCookie is serveReq with an explicit Cookie header, so a test
// can replay a Set-Cookie from a previous response.
func serveReqCookie(t *testing.T, in *Interp, method, path, cookie string) *testResponseWriter {
	t.Helper()
	var handler domain.Value
	params := map[string]string{}
	for _, rt := range in.webRoutes.snapshot() {
		if rt.kind != "http" || rt.method != method {
			continue
		}
		p, ok := matchPattern(rt.pattern, path)
		if !ok {
			continue
		}
		handler, params = rt.handler, p
		break
	}
	if handler == nil {
		t.Fatalf("tidak ada rute %s %s", method, path)
	}
	r := httptest.NewRequest(method, path, nil)
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	w := &testResponseWriter{header: make(http.Header)}
	in.serveRequest(handler, params, in.webRoutes.snapshotUses(), w, r)
	return w
}

// Middleware runs in registration order: code before $next first (outermost
// to innermost), code after $next last; the response from $next can be
// modified on the way out.
func TestMiddlewareUrutanDanUbahRespons(t *testing.T) {
	in, out := evalWeb(t, `
use "http"
http.use(fn($req, $next) {
    print("m1-before")
    $res = $next($req)
    print("m1-after")
    return $res + "\n-dari-m1"
})
http.use(fn($req, $next) {
    print("m2-before")
    return $next($req)
})
http.GET("/", fn($req) {
    print("handler")
    return "ok"
})
`)
	w := serveReq(t, in, "GET", "/")
	if w.code != 200 {
		t.Fatalf("status = %d, want 200", w.code)
	}
	if w.body != "ok\n-dari-m1" {
		t.Fatalf("body = %q, want %q", w.body, "ok\n-dari-m1")
	}
	want := "m1-before\nm2-before\nhandler\nm1-after"
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("urutan = %q, want %q", got, want)
	}
}

// A middleware may respond without calling $next (short-circuit); the
// handler must not run. Other routes still pass through.
func TestMiddlewareBlokirPendek(t *testing.T) {
	in, out := evalWeb(t, `
use "http"
http.use(fn($req, $next) {
    if $req.path == "/admin" {
        return http.json({error: "dilarang"}, 403)
    }
    return $next($req)
})
http.GET("/admin", fn($req) { print("tidak-boleh"); return "rahasia" })
http.GET("/", fn($req) { return "publik" })
`)
	w := serveReq(t, in, "GET", "/admin")
	if w.code != 403 {
		t.Fatalf("status = %d, want 403", w.code)
	}
	if strings.Contains(out.String(), "tidak-boleh") {
		t.Fatalf("handler seharusnya tidak dijalankan, stdout: %q", out.String())
	}
	w2 := serveReq(t, in, "GET", "/")
	if w2.body != "publik" {
		t.Fatalf("body = %q, want publik", w2.body)
	}
}

// Mutations on $req in a middleware are visible to the handler: objects
// share references, and $next receives the same object.
func TestMiddlewareUbahRequest(t *testing.T) {
	in, _ := evalWeb(t, `
use "http"
http.use(fn($req, $next) {
    $req.user = "dari-middleware"
    return $next($req)
})
http.GET("/", fn($req) { return {user: $req.user} })
`)
	w := serveReq(t, in, "GET", "/")
	if w.body != `{"user":"dari-middleware"}` {
		t.Fatalf("body = %q", w.body)
	}
}

// A throw inside middleware becomes an error response, just like from a
// handler: domain errors keep their HTTP status.
func TestMiddlewareThrow(t *testing.T) {
	in, _ := evalWeb(t, `
use "http"
http.use(fn($req, $next) {
    throw not_found("rusak")
})
http.GET("/", fn($req) { return "ok" })
`)
	w := serveReq(t, in, "GET", "/")
	if w.code != 404 {
		t.Fatalf("status = %d, want 404", w.code)
	}
	if !strings.Contains(w.body, "rusak") {
		t.Fatalf("body = %q, want contains rusak", w.body)
	}
}

// An async handler still flows through the chain: $next awaits the promise
// so middleware always sees a plain value.
func TestMiddlewareHandlerAsync(t *testing.T) {
	in, _ := evalWeb(t, `
use "http"
http.use(fn($req, $next) { return $next($req) })
http.GET("/", async fn($req) { return "async-ok" })
`)
	w := serveReq(t, in, "GET", "/")
	if w.body != "async-ok" {
		t.Fatalf("body = %q, want async-ok", w.body)
	}
}

// sessionSid mengambil nilai garurda_session dari header Set-Cookie.
func sessionSid(t *testing.T, w *testResponseWriter) string {
	t.Helper()
	sc := w.header.Get("Set-Cookie")
	if sc == "" {
		t.Fatal("tidak ada header Set-Cookie")
	}
	parts := strings.SplitN(strings.Split(sc, ";")[0], "=", 2)
	if len(parts) != 2 || parts[1] == "" {
		t.Fatalf("Set-Cookie tak terbaca: %q", sc)
	}
	return parts[1]
}

// Sesi: request pertama yang menulis mendapat cookie HttpOnly + id acak;
// request berikutnya dengan cookie itu memakai objek sesi yang sama.
func TestSessionTulisDanBaca(t *testing.T) {
	in, _ := evalWeb(t, `
use "http"
http.GET("/tulis", fn($req) {
    $s = http.session($req)
    $s.n = 41
    return {n: $s.n, baru: $req.session_id == null}
})
http.GET("/baca", fn($req) {
    $s = $req.session
    $s.n = $s.n + 1
    return {n: $s.n}
})
`)
	w1 := serveReq(t, in, "GET", "/tulis")
	if w1.body != `{"baru":true,"n":41}` {
		t.Fatalf("respons pertama = %q", w1.body)
	}
	sc := w1.header.Get("Set-Cookie")
	if !strings.Contains(sc, "HttpOnly") {
		t.Fatalf("cookie seharusnya HttpOnly: %q", sc)
	}
	if !strings.Contains(sc, "SameSite=Lax") {
		t.Fatalf("cookie seharusnya SameSite=Lax: %q", sc)
	}
	sid := sessionSid(t, w1)
	w2 := serveReqCookie(t, in, "GET", "/baca", "garurda_session="+sid)
	if w2.body != `{"n":42}` {
		t.Fatalf("baca-silang = %q, want {\"n\":42}", w2.body)
	}
	if w2.header.Get("Set-Cookie") != "" {
		t.Fatalf("sesi lama tak perlu cookie baru: %q", w2.header.Get("Set-Cookie"))
	}
}

// Sesi yang tidak pernah ditulis tidak mengeluarkan cookie dan tidak
// masuk store — trafik anonim tak menumpuk.
func TestSessionAnonimTanpaCookie(t *testing.T) {
	in, _ := evalWeb(t, `
use "http"
http.GET("/", fn($req) { return {n: $req.session.n} })
`)
	w := serveReq(t, in, "GET", "/")
	if w.header.Get("Set-Cookie") != "" {
		t.Fatalf("sesi kosong seharusnya tak mengeluarkan cookie: %q", w.header.Get("Set-Cookie"))
	}
	if w.body != `{"n":null}` {
		t.Fatalf("body = %q, want {\"n\":null}", w.body)
	}
}

// http.session_destroy mengosongkan sesi, menghapus entri store, dan
// mengirim cookie kedaluwarsa; cookie lama tidak berlaku lagi.
func TestSessionDestroy(t *testing.T) {
	in, _ := evalWeb(t, `
use "http"
http.GET("/isi", fn($req) {
    $s = http.session($req)
    $s.n = 7
    return {n: $s.n}
})
http.GET("/keluar", fn($req) {
    http.session_destroy($req)
    return "selesai"
})
http.GET("/baca", fn($req) {
    return {n: $req.session.n}
})
`)
	w1 := serveReq(t, in, "GET", "/isi")
	sid := sessionSid(t, w1)
	w2 := serveReqCookie(t, in, "GET", "/keluar", "garurda_session="+sid)
	if sc := w2.header.Get("Set-Cookie"); !strings.Contains(sc, "Max-Age=0") {
		t.Fatalf("destroy seharusnya mengirim cookie kedaluwarsa: %q", sc)
	}
	w3 := serveReqCookie(t, in, "GET", "/baca", "garurda_session="+sid)
	if w3.body != `{"n":null}` {
		t.Fatalf("sesi lama harus sudah mati, body = %q", w3.body)
	}
}

// http.session() tanpa argumen memakai request yang sedang dilayani.
func TestSessionTanpaArgumen(t *testing.T) {
	in, _ := evalWeb(t, `
use "http"
http.GET("/", fn($req) {
    $s = http.session()
    $n = 1
    if $s.langkah != null {
        $n = $s.langkah + 1
    }
    $s.langkah = $n
    return {langkah: $n}
})
`)
	w1 := serveReq(t, in, "GET", "/")
	if w1.body != `{"langkah":1}` {
		t.Fatalf("langkah pertama = %q", w1.body)
	}
	sid := sessionSid(t, w1)
	w2 := serveReqCookie(t, in, "GET", "/", "garurda_session="+sid)
	if w2.body != `{"langkah":2}` {
		t.Fatalf("langkah kedua = %q, want {\"langkah\":2}", w2.body)
	}
}

// http.session() di luar handler adalah galat, bukan keheningan.
func TestSessionDiLuarHandler(t *testing.T) {
	var out bytes.Buffer
	in := New(&out, &out)
	_, err := in.Eval("use \"http\"\nhttp.session()\n", "test.ga")
	if err == nil {
		t.Fatal("http.session() di luar handler seharusnya error")
	}
	if !strings.Contains(err.Error(), "handler") {
		t.Fatalf("pesan error tak menjelaskan konteks: %v", err)
	}
}

// Respons object harus memuat null JSON sungguhan, bukan string "null"
// (konsisten dengan json_encode).
func TestWriteResponseNullJSON(t *testing.T) {
	in := New(nil, nil)
	rec := &testResponseWriter{header: make(http.Header)}
	obj := domain.NewObj()
	obj.Set("n", domain.Null{})
	obj.Set("s", domain.Str("x"))
	in.writeResponse(rec, obj)
	if rec.body != `{"n":null,"s":"x"}` {
		t.Fatalf("body = %q, want {\"n\":null,\"s\":\"x\"}", rec.body)
	}
}
