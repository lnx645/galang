// Package web provides the web framework types for Garurda.
package web

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Context holds the request/response context for a single HTTP request.
type Context struct {
	Request  *Request
	Response *Response
	Params   map[string]string
}

// Request wraps http.Request with additional helpers.
type Request struct {
	*http.Request
	Params map[string]string
}

// NewRequest creates a new Request wrapper.
func NewRequest(req *http.Request, params map[string]string) *Request {
	return &Request{
		Request: req,
		Params:  params,
	}
}

// Param returns a route parameter by name.
func (r *Request) Param(name string) string {
	if r.Params == nil {
		return ""
	}
	return r.Params[name]
}

// Query returns a query parameter by name.
func (r *Request) Query(name string) string {
	return r.URL.Query().Get(name)
}

// QueryDefault returns a query parameter with a default value.
func (r *Request) QueryDefault(name, def string) string {
	if v := r.Query(name); v != "" {
		return v
	}
	return def
}

// Body reads the request body.
func (r *Request) Body() ([]byte, error) {
	return io.ReadAll(r.Request.Body)
}

// JSON parses the request body as JSON.
func (r *Request) JSON(v interface{}) error {
	data, err := r.Body()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// Form parses the request body as form data.
func (r *Request) Form() (url.Values, error) {
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	return r.PostForm, nil
}

// File returns a file from multipart form.
func (r *Request) File(key string) (*multipart.FileHeader, error) {
	_, fh, err := r.Request.FormFile(key)
	return fh, err
}

// MultipartForm returns the multipart form.
func (r *Request) MultipartForm() (*multipart.Form, error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, err
	}
	return r.Request.MultipartForm, nil
}

// Header returns a request header.
func (r *Request) Header(name string) string {
	return r.Request.Header.Get(name)
}

// Cookie returns a cookie by name.
func (r *Request) Cookie(name string) (*http.Cookie, error) {
	return r.Request.Cookie(name)
}

// IP returns the client IP.
func (r *Request) IP() string {
	// Check X-Forwarded-For header
	if xff := r.Header("X-Forwarded-For"); xff != "" {
		return strings.Split(xff, ",")[0]
	}
	// Check X-Real-IP header
	if xri := r.Header("X-Real-IP"); xri != "" {
		return xri
	}
	// Fallback to RemoteAddr
	return strings.Split(r.RemoteAddr, ":")[0]
}

// UserAgent returns the User-Agent header.
func (r *Request) UserAgent() string {
	return r.Header("User-Agent")
}

// Response wraps http.ResponseWriter with additional helpers.
type Response struct {
	Writer  http.ResponseWriter
	status  int
	written bool
}

// NewResponse creates a new Response.
func NewResponse(w http.ResponseWriter) *Response {
	return &Response{
		Writer: w,
		status: 200,
		written: false,
	}
}

// Status sets the HTTP status code.
func (r *Response) Status(code int) *Response {
	r.status = code
	return r
}

// Write writes the response body.
func (r *Response) Write(data []byte) (int, error) {
	if !r.written {
		r.Writer.WriteHeader(r.status)
		r.written = true
	}
	return r.Writer.Write(data)
}

// WriteString writes a string as the response body.
func (r *Response) WriteString(s string) (int, error) {
	return r.Write([]byte(s))
}

// JSON writes a JSON response.
func (r *Response) JSON(v interface{}) *Response {
	data, err := json.Marshal(v)
	if err != nil {
		return r.Error(err.Error(), 500)
	}
	r.Header().Set("Content-Type", "application/json")
	r.Write(data)
	return r
}

// Text writes a text response.
func (r *Response) Text(s string) *Response {
	r.Header().Set("Content-Type", "text/plain; charset=utf-8")
	r.WriteString(s)
	return r
}

// HTML writes an HTML response.
func (r *Response) HTML(s string) *Response {
	r.Header().Set("Content-Type", "text/html; charset=utf-8")
	r.WriteString(s)
	return r
}

// Redirect sends a redirect response.
func (r *Response) Redirect(url string, code int) *Response {
	r.Status(code)
	r.Header().Set("Location", url)
	return r
}

// Header returns the response headers.
func (r *Response) Header() http.Header {
	return r.Writer.Header()
}

// SetCookie sets a cookie.
func (r *Response) SetCookie(cookie *http.Cookie) {
	http.SetCookie(r.Writer, cookie)
}

// WriteFile serves a file.
func (r *Response) WriteFile(path string) *Response {
	http.ServeFile(r.Writer, nil, path)
	return r
}

// Error writes an error response.
func (r *Response) Error(msg string, code int) *Response {
	r.Status(code)
	return r.JSON(map[string]string{"error": msg})
}

// WriteHeader implements the http.ResponseWriter interface.
func (r *Response) WriteHeader(statusCode int) {
	if !r.written {
		r.Writer.WriteHeader(statusCode)
		r.written = true
	}
}

// Flush implements the http.Flusher interface.
func (r *Response) Flush() {
	if f, ok := r.Writer.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements the http.Hijacker interface.
func (r *Response) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.Writer.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, ErrHijackNotSupported
}

var ErrHijackNotSupported = errors.New("hijack not supported")