# Web Runtime (HTTP)

The `http` module provides a real HTTP server (built on Go's `net/http`):
routing, path parameters, request/response, Blade templates, static files,
SSE, and WebSocket.

```galang
use "http"

http.GET("/", fn($req) {
    return "Hello from GaLang!"
})

http.listen(8869)
```

## Table of Contents

- [Routing](#routing)
- [HTTP Methods](#http-methods)
- [Middleware](#middleware)
- [Request Object](#request-object)
- [Response Rules](#response-rules)
- [json() — full control](#json--full-control)
- [Error & Status Code](#error--status-code)
- [Cookie](#cookie)
- [Sessions](#sessions)
- [Static Files](#static-files)
- [Template Blade](#template-blade)
- [SSE (Server-Sent Events)](#sse-server-sent-events)
- [WebSocket](#websocket)
- [Full Example](#full-example)

## Routing

```galang
use "http"

http.GET("/", fn($req) {
    return "Home"
})

http.POST("/users", fn($req) {
    return "Created"
})

http.PUT("/users/{id}", fn($req) {
    return "Updated " + $req.params.id
})

http.DELETE("/users/{id}", fn($req) {
    return "Deleted"
})

http.listen(8869)
```

- Path parameters are written as `{nama}` and are available at
  `$req.params.nama`.
- Unknown path → **404**.
- Known path but mismatched method → **405** (Method Not Allowed).

## HTTP Methods

| Function | Method |
|---|---|
| `http.GET(path, handler)` | GET |
| `http.POST(path, handler)` | POST |
| `http.PUT(path, handler)` | PUT |
| `http.DELETE(path, handler)` | DELETE |
| `http.use(fn)` | register global middleware |
| `http.session([$req])` | get the session object |
| `http.session_destroy([$req])` | destroy session + expiring cookie |
| `http.listen(port)` | start the server |

`handler` is an `fn($req)` function that returns the response.

## Middleware

`http.use(fn)` registers **global** middleware — it runs for every HTTP
route (GET/POST/PUT/DELETE) before the handler. A middleware receives two
arguments: `($req, $next)`.

```galang
use "http"

// Log every request
http.use(fn($req, $next) {
    print($req.method + " " + $req.path)
    return $next($req)
})

// Block /admin without the right token
http.use(fn($req, $next) {
    if $req.path == "/admin" and $req.headers.authorization != "rahasia" {
        return http.json({error: "unauthorized"}, 401)
    }
    return $next($req)
})

http.GET("/", fn($req) { return "public" })
http.GET("/admin", fn($req) { return "secret" })
http.listen(8869)
```

Rules:

- Code **before** `$next($req)` runs before the handler (or the next
  middleware).
- `$next($req)` runs the rest of the chain and **returns the handler's
  response**. The result is awaited, so middleware always sees a plain
  value — `$res.status` or `$res + "text"` can be used directly.
- **Not calling** `$next` = *short-circuit*: the handler never runs and the
  middleware's return value becomes the response (e.g. block with
  401/403).
- `$req` may be mutated (e.g. `$req.user = $token`) and passed to
  `$next($req)` — the handler sees the changes, because objects share
  references.
- `throw` inside middleware produces an error response with the same
  status handling as from a handler (e.g. `not_found(...)` → 404 with a
  `{code, message, status}` body).
- Order = registration order: the first `http.use` is the outermost.
- Applies to regular HTTP routes only. Static files, SSE, and WebSocket
  do **not** go through the middleware chain.

A working example lives in
[`examples/webapp`](https://github.com/lnx645/galang/tree/master/examples/webapp)
(middleware logging to the systemd journal).

## Request Object

```galang
http.GET("/cari/{id}", fn($req) {
    $req.method          // "GET", "POST", "PUT", "DELETE"
    $req.path            // "/cari/42"
    $req.url             // "/cari/42?q=1"
    $req.query.q         // query parameter (?q=1)
    $req.params.id       // path parameter {id} → "42"
    $req.headers.accept  // header (lowercase)
    $req.cookies.sid     // cookie
    $req.session         // session object (same as http.session($req))
    $req.session_id      // session id; null before the session exists
    $req.body            // raw body as a string
    return "ok"
})
```

For a JSON body, decode it first:

```galang
http.POST("/api", fn($req) {
    $data = json_decode($req.body)
    return {diterima: $data}
})
```

## Response Rules

Just `return` a value — the runtime determines the status, type, and body:

| Returned | Response |
|---|---|
| `string` | `200 OK`, `text/html` |
| `array` / `object` | `200 OK`, `application/json` (JSON automatically) |
| structured object with `body` | see below |

```galang
// String → HTML 200
return "Hello"

// Array/Object → automatic JSON 200
return ["a", "b"]             // [{"..."}] JSON array
return {status: "ok"}         // JSON object

// Structured response — full control over status/type/body/headers/cookies
return {status: 404, type: "text/html", body: "<h1>Not Found</h1>"}
return {status: 201, body: "dibuat"}
return {type: "text/html", body: "OK", cookies: {session: "abc"}}
```

Structured response keys:

| Key | Default | Description |
|---|---|---|
| `status` | 200 | HTTP status code |
| `type` | `text/html` (string) / `application/json` (structured object) | Content-Type |
| `body` | — | response body |
| `headers` | — | additional header object |
| `cookies` | — | object of cookies to set |

## json() — full control

```galang
return http.json({id: 1}, 201)
return http.json({msg: "ok"}, 200, {"X-Custom": "value"})
```

Signature: `http.json($data, $status = 200, $headers = {})`.
The Content-Type is automatically `application/json; charset=utf-8`.

## Error & Status Code

### Errors from `throw` → status + JSON

An error thrown inside a handler is answered by the server with **the matching
status** and a JSON body `{code, message, status}`:

```galang
http.GET("/users/{id}", fn($req) {
    $user = cari_user($req.params.id)
    if $user == null {
        throw not_found("user not found")
    }
    return $user
})
```

```text
HTTP/1.1 404 Not Found
Content-Type: application/json

{"code":"http_error","message":"user not found","status":404}
```

| Constructor | Response status |
|---|---|
| `bad_request(...)` | 400 |
| `unauthorized(...)` | 401 |
| `forbidden(...)` | 403 |
| `not_found(...)` | 404 |
| `conflict(...)` | 409 |
| `error(...)` / `server_error(...)` | 500 |

### `json_decode` → thrown error 400

Invalid JSON body makes `json_decode()` throw an `json_error` with status
**400**. The error is catchable with `try/catch`; if it escapes a handler,
the server answers:

```json
{"code":"json_error","message":"invalid JSON: ...","status":400}
```

### Internal errors → plain 500

Internal bugs (division by zero, out-of-bounds index, undefined variable)
produce **500** with a plain body. **Error details and the stack trace are only
logged to the server's stderr** — they never leak to the client.

```galang
http.GET("/rusak", fn($req) {
    return 1 / 0        // → 500, plain body; details go to stderr
})
```

## Cookie

```galang
// Set a cookie via a structured response
return {type: "text/html", body: "OK", cookies: {session: "abc"}}

// Read a cookie from the request
http.GET("/cek", fn($req) {
    print($req.cookies.session)
    return "ok"
})
```

## Sessions

Sessions bind per-visitor data through the `galang_session` cookie
(HttpOnly, SameSite=Lax, Path=/) and the in-memory `$req.session`
object.

```galang
http.GET("/cart", fn($req) {
    $s = http.session($req)   // the same object as $req.session
    $n = 0
    if $s.item != null {
        $n = $s.item + 1
    }
    $s.item = $n
    return {item: $n, fresh: $req.session_id == null}
})
```

Rules:

- `$req.session`, `http.session($req)`, and `http.session()` (without
  arguments, inside a handler only) all refer to the **same session
  object** — a plain object, read and write fields freely.
- A cookie is issued only when the session is **written** on a request
  that has no id yet — anonymous traffic produces no cookie and no store
  entry.
- The session id is a random 128-bit value; every later request carrying
  that cookie uses the same session object.
- `http.session_destroy($req)` empties the session, deletes it from the
  store, and sends an expiring cookie (`Max-Age=0`).
- Calling `http.session()` / `http.session_destroy()` outside a handler
  is an error with a clear message.
- The store is **in-memory**: it vanishes when the process ends
  (restart) and has no automatic expiry — honest for demo/development
  scale; multi-process production would need an external store.
- Regular HTTP routes only; static/SSE/WS bypass it (same as
  middleware).

A working example: `GET /api/kunjungan` in
[`examples/webapp`](https://github.com/lnx645/galang/tree/master/examples/webapp)
(per-session counter, `?reset=1` to destroy).

## Static Files

```galang
http.static("/public/", "public")
// GET /public/style.css → serves the file public/style.css
```

The `/public/` URL prefix is mapped to the `public/` directory in the working
directory.

## Template Blade

```galang
http.views("views")            // template directory

http.GET("/", fn($req) {
    $data = {title: "Home", user: {name: "Dadan"}, items: [1, 2, 3]}
    return http.render("home.blade", $data)
})
```

- `http.views($dir)` — set the template directory.
- `http.render($namaFile, $data)` — render → an HTML string (200).
- Variables from `$data.kunci` are available as `$nama` in the template.

### Blade Syntax

```blade
{{-- blade comment --}}
<h1>{{ $title }}</h1>          {{-- HTML-escaped automatically --}}
{!! $rawHTML !!}              {{-- no escaping --}}

@if($x > 3 && !$admin)
  <p>Large</p>
@elseif($x == 2)
  <p>Two</p>
@else
  <p>Small</p>
@endif

@foreach($items as $item)
  <li>{{ $item }}</li>
@else
  <li>Empty</li>
@endforeach

@foreach($ages as $name => $value) {{-- key => value form --}}
  <li>{{ $name }}: {{ $value }}</li>
@endforeach

@extends("layouts/app")
@section("title", "Home")      {{-- inline section --}}
@section("content")
  <p>Page content…</p>
@endsection
@yield("title")                {{-- slot in the layout --}}
@yield("scripts", "")          {{-- default when the child leaves it empty --}}

@include("partials/nav")
@include("partials/kode", {judul: "main.ga", isi: $contoh}) {{-- partial with data --}}
```

Expressions inside `{{ }}` and `@if` conditions support the full operator
set: comparisons (`>`, `>=`, `==`, `!=`, `<`, `<=`), logic (`&&`, `||`, `!`,
`and`, `or`, `not`), arithmetic (`+`, `-`, `*`, `/`), function calls
(`count($x)`, `len($x)`), pipes (`| upper`), and object literals `{k: v}`
(used as `@include` data). Outside `{{ }}`, `$var` is plain text.

| Directive | Purpose |
|---|---|
| `{{ $x }}` | expression, HTML-escaped |
| `{!! $x !!}` | raw expression |
| `@if` `@elseif` `@else` `@endif` | conditionals (operators allowed) |
| `@foreach` `@else` `@endforeach` | loops (`@else` when empty); `@foreach($m as $k => $v)` for keys |
| `@extends("layout")` | layout inheritance |
| `@section("name")` `@section("name", "value")` | fill a slot (block / inline) |
| `@yield("name")` `@yield("name", "default")` | slot in the layout + default |
| `@include("partial")` `@include("partial", {k: v})` | insert a partial (with data) |
| `{{-- --}}` | comment |

## SSE (Server-Sent Events)

```galang
http.stream("/events", fn($req) {
    return "ping"      // each return = one event
})
```

Client: `new EventSource("/events")`.

## WebSocket

```galang
http.ws("/chat", fn($msg) {
    return "echo: " + $msg    // the returned value is sent back to the client
})
```

## Full Example

```galang
use "http"
use "strings"

http.views("views")
http.static("/public/", "public")

http.GET("/", fn($req) {
    return http.render("home.blade", {title: "Beranda"})
})

http.GET("/health", fn($req) {
    return {status: "ok"}
})

http.GET("/greet/{name}", fn($req) {
    return "Hello, " + strings.upper($req.params.name) + "!"
})

http.POST("/api/users", fn($req) {
    $data = json_decode($req.body)
    if $data.name == null { throw bad_request("name wajib") }
    return http.json({id: 1, name: $data.name}, 201)
})

http.listen(8869)
```

Run it:

```bash
gar run main.ga
curl http://localhost:8869/health     # {"status":"ok"}
```
