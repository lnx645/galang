package interp

import (
	"net/http"
	"net/url"
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
