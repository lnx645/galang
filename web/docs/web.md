# Web Runtime

Modul `http` menyediakan server HTTP, routing, template Blade, file statis, WebSocket, dan SSE.

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

http.listen(8080)
```

### Request Object

```garurda
$req.method       // "GET", "POST", ...
$req.path         // "/users/42"
$req.url          // "/users/42?q=1"
$req.query.name   // query parameter
$req.params.id    // route parameter {id}
$req.headers.accept
$req.cookies.session_id
$req.body         // request body string
```

### Response

```garurda
// String → text/html 200
return "Hello"

// Array/Object → application/json 200 (otomatis)
return ["a", "b"]            // JSON array
return {status: "ok"}        // JSON object

// json(data, status, headers) — kontrol penuh
return http.json({id: 1}, 201)
return http.json({msg: "ok"}, 200, {"X-Custom": "value"})

// Structured response — status, type, body, headers, cookies
return {status: 404, type: "text/html", body: "<h1>Not Found</h1>"}
return {type: "text/html", body: "OK", cookies: {session: "abc"}}
```

## Static Files

```garurda
http.static("/public/", "public")
// GET /public/style.css → serve public/style.css
```

## Template Blade

```garurda
http.views("views")

http.GET("/", fn($req) {
    $data = {title: "Home", name: "Dadan"}
    return http.render("home.blade", $data)
})
```

Blade syntax:

```blade
<h1>{{ $title }}</h1>

@if($user)
  <p>Hello, {{ $user.name }}</p>
@endif

@foreach($items as $item)
  <li>{{ $item }}</li>
@endforeach

{!! $rawHTML !!}

@extends("layouts/app")
@yield("content")

@include("partials/header")
```

## SSE (Server-Sent Events)

```garurda
http.stream("/events", fn($req) {
    return "ping"
})
```

## WebSocket

```garurda
http.ws("/chat", fn($msg) {
    return "echo: " + $msg
})
```

## Contoh Lengkap

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

http.listen(8080)
```
