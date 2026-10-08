# Web Runtime (HTTP)

The `http` module provides a real HTTP server (built on Go's `net/http`):
routing, path parameters, request/response, Blade templates, static files,
SSE, and WebSocket.

```garurda
use "http"

http.GET("/", fn($req) {
    return "Halo dari Garurda!"
})

http.listen(8868)
```

## Table of Contents

- [Routing](#routing)
- [HTTP Methods](#http-methods)
- [Request Object](#request-object)
- [Response Rules](#response-rules)
- [json() — full control](#json--full-control)
- [Error & Status Code](#error--status-code)
- [Cookie](#cookie)
- [Static Files](#static-files)
- [Template Blade](#template-blade)
- [SSE (Server-Sent Events)](#sse-server-sent-events)
- [WebSocket](#websocket)
- [Full Example](#full-example)

## Routing

```garurda
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

http.listen(8868)
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
| `http.listen(port)` | start the server |

`handler` is an `fn($req)` function that returns the response.

## Request Object

```garurda
http.GET("/cari/{id}", fn($req) {
    $req.method          // "GET", "POST", "PUT", "DELETE"
    $req.path            // "/cari/42"
    $req.url             // "/cari/42?q=1"
    $req.query.q         // query parameter (?q=1)
    $req.params.id       // path parameter {id} → "42"
    $req.headers.accept  // header (lowercase)
    $req.cookies.sid     // cookie
    $req.body            // raw body as a string
    return "ok"
})
```

For a JSON body, decode it first:

```garurda
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

```garurda
// String → HTML 200
return "Halo"

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

```garurda
return http.json({id: 1}, 201)
return http.json({msg: "ok"}, 200, {"X-Custom": "value"})
```

Signature: `http.json($data, $status = 200, $headers = {})`.
The Content-Type is automatically `application/json; charset=utf-8`.

## Error & Status Code

### Errors from `throw` → status + JSON

An error thrown inside a handler is answered by the server with **the matching
status** and a JSON body `{code, message, status}`:

```garurda
http.GET("/users/{id}", fn($req) {
    $user = cari_user($req.params.id)
    if $user == null {
        throw not_found("user tidak ada")
    }
    return $user
})
```

```text
HTTP/1.1 404 Not Found
Content-Type: application/json

{"code":"http_error","message":"user tidak ada","status":404}
```

| Constructor | Response status |
|---|---|
| `bad_request(...)` | 400 |
| `unauthorized(...)` | 401 |
| `forbidden(...)` | 403 |
| `not_found(...)` | 404 |
| `conflict(...)` | 409 |
| `error(...)` / `server_error(...)` | 500 |

### Internal errors → plain 500

Internal bugs (division by zero, out-of-bounds index, undefined variable)
produce **500** with a plain body. **Error details and the stack trace are only
logged to the server's stderr** — they never leak to the client.

```garurda
http.GET("/rusak", fn($req) {
    return 1 / 0        // → 500, plain body; details go to stderr
})
```

## Cookie

```garurda
// Set a cookie via a structured response
return {type: "text/html", body: "OK", cookies: {session: "abc"}}

// Read a cookie from the request
http.GET("/cek", fn($req) {
    print($req.cookies.session)
    return "ok"
})
```

## Static Files

```garurda
http.static("/public/", "public")
// GET /public/style.css → serves the file public/style.css
```

The `/public/` URL prefix is mapped to the `public/` directory in the working
directory.

## Template Blade

```garurda
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

@if($user)
  <p>Hello, {{ $user.name }}</p>
@elseif($admin)
  <p>Admin</p>
@else
  <p>Tamu</p>
@endif

@foreach($items as $item)
  <li>{{ $item }}</li>
@else
  <li>Kosong</li>
@endforeach

@extends("layouts/app")
@yield("content")

@include("partials/header")
```

| Directive | Purpose |
|---|---|
| `{{ $x }}` | expression, HTML-escaped |
| `{!! $x !!}` | raw expression |
| `@if` `@elseif` `@else` `@endif` | conditionals |
| `@foreach` `@else` `@endforeach` | loops (`@else` when empty) |
| `@extends("layout")` | layout inheritance |
| `@section("nama")` `@yield("nama")` | layout content & slots |
| `@include("partial")` | insert a partial |
| `{{-- --}}` | comment |

## SSE (Server-Sent Events)

```garurda
http.stream("/events", fn($req) {
    return "ping"      // each return = one event
})
```

Client: `new EventSource("/events")`.

## WebSocket

```garurda
http.ws("/chat", fn($msg) {
    return "echo: " + $msg    // the returned value is sent back to the client
})
```

## Full Example

```garurda
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

http.listen(8868)
```

Run it:

```bash
gar run main.ga
curl http://localhost:8868/health     # {"status":"ok"}
```
