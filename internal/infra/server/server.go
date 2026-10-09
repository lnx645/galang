// Package server provides the HTTP server for GaLang web applications.
package server

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"galang/internal/usecase/web"
)

// HandlerFunc is the function signature for route handlers.
type HandlerFunc func(*web.Request) *web.Response

// Route represents a single route.
type Route struct {
	Method  string
	Path    string
	Handler HandlerFunc
}

// Router is a simple HTTP router using Go's built-in net/http.
type Router struct {
	routes     []Route
	middleware []MiddlewareFunc
	mu         sync.RWMutex
}

// MiddlewareFunc is the function signature for middleware.
type MiddlewareFunc func(HandlerFunc) HandlerFunc

// NewRouter creates a new router.
func NewRouter() *Router {
	return &Router{
		routes:     make([]Route, 0),
		middleware: make([]MiddlewareFunc, 0),
	}
}

// Use adds middleware to the router.
func (r *Router) Use(middleware MiddlewareFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.middleware = append(r.middleware, middleware)
}

// GET adds a GET route.
func (r *Router) GET(path string, handler HandlerFunc) {
	r.addRoute("GET", path, handler)
}

// POST adds a POST route.
func (r *Router) POST(path string, handler HandlerFunc) {
	r.addRoute("POST", path, handler)
}

// PUT adds a PUT route.
func (r *Router) PUT(path string, handler HandlerFunc) {
	r.addRoute("PUT", path, handler)
}

// PATCH adds a PATCH route.
func (r *Router) PATCH(path string, handler HandlerFunc) {
	r.addRoute("PATCH", path, handler)
}

// DELETE adds a DELETE route.
func (r *Router) DELETE(path string, handler HandlerFunc) {
	r.addRoute("DELETE", path, handler)
}

// ANY adds a route for all HTTP methods.
func (r *Router) ANY(path string, handler HandlerFunc) {
	r.addRoute("", path, handler)
}

func (r *Router) addRoute(method, path string, handler HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes = append(r.routes, Route{
		Method:  method,
		Path:    path,
		Handler: handler,
	})
}

// match matches a request against registered routes.
func (r *Router) match(method, path string) (HandlerFunc, map[string]string) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, route := range r.routes {
		if route.Method != "" && route.Method != method {
			continue
		}
		params, ok := matchPath(route.Path, path)
		if ok {
			return route.Handler, params
		}
	}
	return nil, nil
}

// matchPath matches a path pattern against a request path.
// Supports static paths and parameterized paths like /users/{id}.
func matchPath(pattern, path string) (map[string]string, bool) {
	if pattern == path {
		return nil, true
	}

	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")

	if len(patternParts) != len(pathParts) {
		return nil, false
	}

	params := make(map[string]string)
	for i, p := range patternParts {
		if len(p) > 0 && p[0] == '{' && p[len(p)-1] == '}' {
			key := p[1 : len(p)-1]
			params[key] = pathParts[i]
		} else if p != pathParts[i] {
			return nil, false
		}
	}
	return params, true
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	handler, params := r.match(req.Method, req.URL.Path)
	if handler == nil {
		http.NotFound(w, req)
		return
	}

	ctx := &web.Context{
		Request:  web.NewRequest(req, params),
		Response: web.NewResponse(w),
		Params:   params,
	}

	// Apply middleware chain
	h := handler
	for i := len(r.middleware) - 1; i >= 0; i-- {
		h = r.middleware[i](h)
	}
	_ = h(ctx.Request)
}

// Server represents the HTTP server.
type Server struct {
	router *Router
	server *http.Server
	addr   string
}

// NewServer creates a new server.
func NewServer(addr string) *Server {
	router := NewRouter()
	return &Server{
		router: router,
		addr:   addr,
		server: &http.Server{
			Addr:    addr,
			Handler: router,
		},
	}
}

// Router returns the router for route registration.
func (s *Server) Router() *Router {
	return s.router
}

// Start starts the server.
func (s *Server) Start() error {
	fmt.Printf("Server starting on %s\n", s.addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	return s.server.Close()
}