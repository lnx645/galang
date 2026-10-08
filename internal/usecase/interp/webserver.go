package interp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	"garurda/internal/domain"
)

// webRoute is a single route registered from Garurda code.
type webRoute struct {
	method  string
	pattern string
	handler domain.Value
	kind    string // "http", "ws", "sse", "static"
	dir     string // for static routes
}

// webRoutes collects the routes registered via the http module and
// serves them. The VM's execution state (frames, stacks, call depth)
// is shared across the process, so requests are serialized by execMu
// before any Garurda closure is run.
type webRoutes struct {
	mu     sync.RWMutex   // protects routes and uses
	execMu sync.Mutex     // serializes Garurda closure execution
	routes []webRoute
	uses   []domain.Value // middleware via http.use, in registration order
}

// add registers a route.
func (wr *webRoutes) add(method, pattern string, handler domain.Value) {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	wr.routes = append(wr.routes, webRoute{method: method, pattern: pattern, handler: handler, kind: "http"})
}

// addWS registers a WebSocket route.
func (wr *webRoutes) addWS(pattern string, handler domain.Value) {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	wr.routes = append(wr.routes, webRoute{method: "GET", pattern: pattern, handler: handler, kind: "ws"})
}

// addSSE registers an SSE route.
func (wr *webRoutes) addSSE(pattern string, handler domain.Value) {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	wr.routes = append(wr.routes, webRoute{method: "GET", pattern: pattern, handler: handler, kind: "sse"})
}

// addStatic registers a static file route.
func (wr *webRoutes) addStatic(pattern, dir string) {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	wr.routes = append(wr.routes, webRoute{method: "GET", pattern: pattern, kind: "static", dir: dir})
}

// snapshot returns a copy of the registered routes.
func (wr *webRoutes) snapshot() []webRoute {
	wr.mu.RLock()
	defer wr.mu.RUnlock()
	out := make([]webRoute, len(wr.routes))
	copy(out, wr.routes)
	return out
}

// addUse registers a middleware function passed to http.use.
func (wr *webRoutes) addUse(fn domain.Value) {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	wr.uses = append(wr.uses, fn)
}

// snapshotUses returns a copy of the middleware chain in registration order.
func (wr *webRoutes) snapshotUses() []domain.Value {
	wr.mu.RLock()
	defer wr.mu.RUnlock()
	out := make([]domain.Value, len(wr.uses))
	copy(out, wr.uses)
	return out
}

// startWebServer runs the HTTP server on the given port, blocking
// until the server stops. It is invoked by http.listen(port).
func (in *Interp) startWebServer(port int) error {
	routes := in.webRoutes.snapshot()
	uses := in.webRoutes.snapshotUses()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// Check for WebSocket upgrade first
		isWS := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		var matched *webRoute
		var params map[string]string
		methodMismatch := false
		for i := range routes {
			// Static routes match by prefix, not exact segments.
			if routes[i].kind == "static" {
				prefix := strings.TrimSuffix(routes[i].pattern, "/*")
				prefix = strings.TrimSuffix(prefix, "/")
				if strings.HasPrefix(path, prefix) {
					matched = &routes[i]
					break
				}
				continue
			}
			p, ok := matchPattern(routes[i].pattern, path)
			if !ok {
				continue
			}
			if routes[i].kind == "ws" && isWS {
				matched = &routes[i]
				params = p
				break
			}
			if routes[i].kind == "sse" {
				matched = &routes[i]
				params = p
				break
			}
			if r.Method != routes[i].method {
				methodMismatch = true
				continue
			}
			matched = &routes[i]
			params = p
			break
		}
		if matched == nil {
			if methodMismatch {
				w.Header().Set("Allow", "GET, POST, PUT, DELETE")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			http.NotFound(w, r)
			return
		}
		switch matched.kind {
		case "ws":
			in.serveWS(matched.handler, params, w, r)
		case "sse":
			in.serveSSE(matched.handler, params, w, r)
		case "static":
			in.serveStatic(matched.dir, matched.pattern, w, r)
		default:
			in.serveRequest(matched.handler, params, uses, w, r)
		}
	})
	addr := ":" + strconv.Itoa(port)
	fmt.Fprintf(in.Out, "Garurda HTTP server listening on http://localhost%s\n", addr)
	return http.ListenAndServe(addr, handler)
}

// matchPattern matches a route pattern against a request path.
// It supports static segments and {name} placeholders, e.g.
// "/users/{id}" matches "/users/42" with params["id"]="42".
func matchPattern(pattern, path string) (map[string]string, bool) {
	if pattern == path {
		return nil, true
	}
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	if len(patternParts) != len(pathParts) {
		return nil, false
	}
	params := make(map[string]string)
	for i, p := range patternParts {
		if len(p) > 1 && p[0] == '{' && p[len(p)-1] == '}' {
			params[p[1:len(p)-1]] = pathParts[i]
		} else if p != pathParts[i] {
			return nil, false
		}
	}
	return params, true
}

// serveRequest invokes a Garurda route handler for an incoming HTTP
// request and writes the result back to the client. The middleware chain
// registered via http.use wraps the handler: each entry receives
// ($req, $next) and its return value becomes the response.
func (in *Interp) serveRequest(handler domain.Value, params map[string]string, uses []domain.Value, w http.ResponseWriter, r *http.Request) {
	in.webRoutes.execMu.Lock()
	defer in.webRoutes.execMu.Unlock()

	reqObj := in.buildRequestObj(r, params)
	in.curReq = reqObj
	defer func() { in.curReq = nil }()
	result, hreq, err := in.runChain(uses, 0, reqObj, handler)
	if err == nil {
		// An async handler or middleware returns a promise; resolve it,
		// then run any fire-and-forget spawns before the response goes out.
		if result, err = in.awaitValue(result, domain.Position{}); err == nil {
			err = in.drainPending()
		}
	}
	// Sesi yang baru ditulis perlu id + Set-Cookie — bahkan bila handler
	// melempar, supaya data yang sudah masuk store tidak yatim.
	target := reqObj
	if o, ok := hreq.(*domain.Obj); ok {
		target = o
	}
	if ck, serr := in.finalizeSession(target); serr != nil {
		if err == nil {
			err = serr
		}
	} else if ck != "" {
		w.Header().Add("Set-Cookie", ck)
	}
	if err != nil {
		in.writeThrown(w, err)
		return
	}
	in.writeResponse(w, result)
}

// runChain runs middleware from index i onwards around handler. The last
// middleware's $next invokes the handler itself. Each middleware gets
// ($req, $next): calling $next runs the rest of the chain and returns the
// final response (awaited, so middleware always sees a plain value);
// returning without calling $next short-circuits the chain. A middleware
// may pass a modified request object to $next — objects share references,
// so mutations are visible downstream. The request object that reached
// the handler is returned alongside the response so serveRequest can
// finalize the session against it.
func (in *Interp) runChain(uses []domain.Value, i int, req domain.Value, handler domain.Value) (domain.Value, domain.Value, error) {
	if i >= len(uses) {
		res, err := in.callValue(handler, []domain.Value{req}, domain.Position{})
		return res, req, err
	}
	next := &domain.Builtin{
		Name:    "next",
		MinArgs: 0,
		VarArgs: true,
		Fn: func(args []domain.Value, pos domain.Position) (domain.Value, error) {
			r := req
			if len(args) > 0 {
				r = args[0]
			}
			res, _, err := in.runChain(uses, i+1, r, handler)
			if err != nil {
				return nil, err
			}
			return in.awaitValue(res, pos)
		},
	}
	res, err := in.callValue(uses[i], []domain.Value{req, next}, domain.Position{})
	if err != nil {
		return nil, req, err
	}
	v, aerr := in.awaitValue(res, domain.Position{})
	return v, req, aerr
}

// writeThrown turns an error that escaped a route handler into a response.
//
// A thrown error carries its own HTTP status (not_found → 404,
// error(msg, {status: 418}) → 418), so the client receives that status with a
// JSON body {message, code, status}. Internal errors — undefined variable,
// division by zero, ... — are not domain errors and fall back to a plain 500.
func (in *Interp) writeThrown(w http.ResponseWriter, err error) {
	if e, ok := err.(*Error); ok && e.Value != nil {
		status := e.Value.Status
		if status < 100 || status > 599 {
			status = http.StatusInternalServerError
		}
		body, merr := json.Marshal(map[string]interface{}{
			"message": e.Value.Message,
			"code":    e.Value.Code,
			"status":  status,
		})
		if merr != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		w.Write(body)
		return
	}
	// Internal errors carry file paths and a stack trace — useful in the
	// terminal, but never in a public response. Log them and answer plainly.
	if in.Err != nil {
		fmt.Fprintf(in.Err, "http handler error: %v\n", err)
	} else if in.Out != nil {
		fmt.Fprintf(in.Out, "http handler error: %v\n", err)
	}
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// serveWS upgrades the connection to WebSocket and calls the Garurda
// handler with each incoming message. The handler's return value is
// sent back as a WebSocket text message.
func (in *Interp) serveWS(handler domain.Value, params map[string]string, w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		in.webRoutes.execMu.Lock()
		result, err := in.callValue(handler, []domain.Value{domain.Str(string(msg))}, domain.Position{})
		in.webRoutes.execMu.Unlock()
		if err != nil {
			conn.WriteMessage(websocket.CloseMessage, []byte(err.Error()))
			break
		}
		conn.WriteMessage(websocket.TextMessage, []byte(result.String()))
	}
}

// serveSSE sends an initial connection event, then calls the Garurda
// handler which can return a string to be sent as SSE data. The
// connection stays open until the client disconnects.
func (in *Interp) serveSSE(handler domain.Value, params map[string]string, w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Initial event
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"ok\"}\n\n")
	flusher.Flush()

	// Call the Garurda handler once — it can return a string to
	// be sent as the first data event.
	in.webRoutes.execMu.Lock()
	reqObj := in.buildRequestObj(r, params)
	result, err := in.callValue(handler, []domain.Value{reqObj}, domain.Position{})
	in.webRoutes.execMu.Unlock()
	if err == nil && result != nil {
		fmt.Fprintf(w, "data: %s\n\n", result.String())
		flusher.Flush()
	}

	// Keep connection open until client disconnects
	<-r.Context().Done()
}

// serveStatic serves files from a directory. The pattern is the URL
// prefix, e.g. "/static/" serves files from the directory.
func (in *Interp) serveStatic(dir, pattern string, w http.ResponseWriter, r *http.Request) {
	// Normalize prefix: "/static/" or "/static/*" → "/static"
	prefix := strings.TrimSuffix(pattern, "/*")
	prefix = strings.TrimSuffix(prefix, "/")
	fs := http.FileServer(http.Dir(dir))
	http.StripPrefix(prefix, fs).ServeHTTP(w, r)
}

// buildRequestObj wraps an *http.Request into a Garurda object with
// method, path, url, headers, query, params and body fields.
func (in *Interp) buildRequestObj(r *http.Request, params map[string]string) *domain.Obj {
	obj := domain.NewObj()
	obj.Set("method", domain.Str(r.Method))
	obj.Set("path", domain.Str(r.URL.Path))
	obj.Set("url", domain.Str(r.URL.String()))

	headers := domain.NewObj()
	for k, v := range r.Header {
		headers.Set(strings.ToLower(k), domain.Str(strings.Join(v, ", ")))
	}
	obj.Set("headers", headers)

	query := domain.NewObj()
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			query.Set(k, domain.Str(v[0]))
		}
	}
	obj.Set("query", query)

	routeParams := domain.NewObj()
	for k, v := range params {
		routeParams.Set(k, domain.Str(v))
	}
	obj.Set("params", routeParams)

	cookies := domain.NewObj()
	for _, c := range r.Cookies() {
		cookies.Set(c.Name, domain.Str(c.Value))
	}
	obj.Set("cookies", cookies)

	// Sesi: ambil objek sesi dari store bila cookie valid, kalau tidak
	// sediakan objek kosong — baru disimpan saat benar-benar ditulis
	// (lihat finalizeSession).
	if in.sessions == nil {
		in.sessions = newSessionStore()
	}
	sess := domain.NewObj()
	sid := ""
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		if s := in.sessions.get(c.Value); s != nil {
			sess, sid = s, c.Value
		}
	}
	obj.Set("session", sess)
	if sid != "" {
		obj.Set("session_id", domain.Str(sid))
	} else {
		obj.Set("session_id", domain.Null{})
	}

	body := ""
	if r.Body != nil {
		data, err := io.ReadAll(r.Body)
		if err == nil {
			body = string(data)
		}
	}
	obj.Set("body", domain.Str(body))
	return obj
}

// writeResponse turns a handler's return value into an HTTP response.
//
//   - string             → text/html (200)
//   - object (no "body") → application/json auto-encoded (200)
//   - object with "body" → structured: status/type/body/headers
//   - array              → application/json auto-encoded (200)
//   - int/float/bool     → text/plain (200)
//
// Structured object fields: status (int), type (string),
// body (string), headers (object), cookies (object).
func (in *Interp) writeResponse(w http.ResponseWriter, result domain.Value) {
	// Structured response: explicit "body" field.
	if obj, ok := result.(*domain.Obj); ok {
		if _, hasBody := obj.Get("body"); hasBody {
			status := http.StatusOK
			contentType := "text/html; charset=utf-8"
			if s, found := obj.Get("status"); found {
				if code, ok := domain.AsInt(s); ok {
					status = int(code)
				}
			}
			if ct, found := obj.Get("type"); found {
				if ctStr, ok := ct.(domain.Str); ok {
					contentType = string(ctStr)
				}
			}
			body := ""
			if b, found := obj.Get("body"); found {
				body = b.String()
			}
			if hdrs, found := obj.Get("headers"); found {
				if ho, ok := hdrs.(*domain.Obj); ok {
					for _, k := range ho.Keys() {
						v, _ := ho.Get(k)
						w.Header().Set(k, v.String())
					}
				}
			}
			if cks, found := obj.Get("cookies"); found {
				if co, ok := cks.(*domain.Obj); ok {
					for _, k := range co.Keys() {
						v, _ := co.Get(k)
						http.SetCookie(w, &http.Cookie{Name: k, Value: v.String()})
					}
				}
			}
			w.Header().Set("Content-Type", contentType)
			w.WriteHeader(status)
			io.WriteString(w, body)
			return
		}
		// Any other object is data — auto-JSON.
		data, err := json.Marshal(domainValueToInterface(obj))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		status := http.StatusOK
		if s, found := obj.Get("status"); found {
			if code, ok := domain.AsInt(s); ok {
				status = int(code)
			}
		}
		if hdrs, found := obj.Get("headers"); found {
			if ho, ok := hdrs.(*domain.Obj); ok {
				for _, k := range ho.Keys() {
					v, _ := ho.Get(k)
					w.Header().Set(k, v.String())
				}
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		w.Write(data)
		return
	}
	// Array auto-JSON.
	if arr, ok := result.(*domain.Arr); ok {
		data, err := json.Marshal(domainValueToInterface(arr))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(data)
		return
	}
	switch result.(type) {
	case domain.Str:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, result.String())
}

// renderTemplate renders a Blade template by name with the given data.
func (in *Interp) renderTemplate(name string, data domain.Value) (domain.Str, error) {
	dataMap := make(map[string]interface{})
	if obj, ok := data.(*domain.Obj); ok {
		for _, k := range obj.Keys() {
			v, _ := obj.Get(k)
			dataMap[k] = domainValueToInterface(v)
		}
	}
	html, err := in.engine.Render(name, dataMap)
	if err != nil {
		return "", err
	}
	return domain.Str(html), nil
}

// domainValueToInterface converts a domain.Value to a Go interface{}
// for template data.
func domainValueToInterface(v domain.Value) interface{} {
	switch v.(type) {
	case domain.Str:
		return string(v.(domain.Str))
	case domain.Int:
		return int64(v.(domain.Int))
	case domain.Float:
		return float64(v.(domain.Float))
	case domain.Bool:
		return bool(v.(domain.Bool))
	case domain.Null:
		// JSON harus memuat null sungguhan, bukan string "null"
		// (json_encode sudah menulis null; respons HTTP harus sama).
		return nil
	case *domain.Arr:
		arr := v.(*domain.Arr)
		items := make([]interface{}, len(arr.Items))
		for i, it := range arr.Items {
			items[i] = domainValueToInterface(it)
		}
		return items
	case *domain.Obj:
		obj := v.(*domain.Obj)
		m := make(map[string]interface{})
		for _, k := range obj.Keys() {
			vv, _ := obj.Get(k)
			m[k] = domainValueToInterface(vv)
		}
		return m
	default:
		return v.String()
	}
}
