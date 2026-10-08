package interp

import (
	"net/http"
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
