# Web Runtime (HTTP)

Modul `http` menyediakan server HTTP nyata (berbasis `net/http` Go): routing,
path parameter, request/response, template Blade, file statis, SSE, dan
WebSocket.

```galang
use "http"

http.GET("/", fn($req) {
    return "Halo dari GaLang!"
})

http.listen(8869)
```

## Daftar Isi

- [Routing](#routing)
- [Method HTTP](#method-http)
- [Middleware](#middleware)
- [Request Object](#request-object)
- [Aturan Response](#aturan-response)
- [json() — kontrol penuh](#json--kontrol-penuh)
- [Error & Status Code](#error--status-code)
- [Cookie](#cookie)
- [Sesi (Session)](#sesi-session)
- [Static Files](#static-files)
- [Template Blade](#template-blade)
- [SSE (Server-Sent Events)](#sse-server-sent-events)
- [WebSocket](#websocket)
- [Contoh Lengkap](#contoh-lengkap)

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

- Path parameter ditulis `{nama}` dan tersedia di `$req.params.nama`.
- Path yang tidak dikenal → **404**.
- Path dikenal tetapi method tidak cocok → **405** (Method Not Allowed).

## Method HTTP

| Fungsi | Method |
|---|---|
| `http.GET(path, handler)` | GET |
| `http.POST(path, handler)` | POST |
| `http.PUT(path, handler)` | PUT |
| `http.DELETE(path, handler)` | DELETE |
| `http.use(fn)` | pasang middleware global |
| `http.session([$req])` | ambil objek sesi |
| `http.session_destroy([$req])` | hapus sesi + cookie kedaluwarsa |
| `http.listen(port)` | jalankan server |

`handler` adalah fungsi `fn($req)` yang mengembalikan response.

## Middleware

`http.use(fn)` memasang middleware **global** — dijalankan untuk semua rute
HTTP (GET/POST/PUT/DELETE) sebelum handler. Middleware menerima dua
argumen: `($req, $next)`.

```galang
use "http"

// Catat setiap request
http.use(fn($req, $next) {
    print($req.method + " " + $req.path)
    return $next($req)
})

// Larang /admin tanpa token yang benar
http.use(fn($req, $next) {
    if $req.path == "/admin" and $req.headers.authorization != "rahasia" {
        return http.json({error: "unauthorized"}, 401)
    }
    return $next($req)
})

http.GET("/", fn($req) { return "publik" })
http.GET("/admin", fn($req) { return "rahasia" })
http.listen(8869)
```

Aturan main:

- Kode **sebelum** `$next($req)` berjalan sebelum handler (atau middleware
  berikutnya).
- `$next($req)` menjalankan rantai sisanya dan **mengembalikan respons
  handler**. Hasilnya sudah di-await, jadi middleware selalu melihat nilai
  jadi — `$res.status` atau `$res + "teks"` langsung bisa dipakai.
- **Tidak memanggil** `$next` = *short-circuit*: handler tidak dijalankan,
  nilai kembali middleware langsung menjadi respons (mis. blokir 401/403).
- `$req` boleh dimodifikasi (mis. `$req.user = $token`) lalu diteruskan ke
  `$next($req)` — handler melihat perubahannya, karena objek by referensi.
- `throw` di middleware menghasilkan respons error berstatus sama seperti
  dari handler (mis. `not_found(...)` → 404, body `{code, message, status}`).
- Urutan = urutan pendaftaran: `http.use` pertama berada paling luar.
- Berlaku untuk rute HTTP biasa. File statis, SSE, dan WebSocket **tidak**
  melewati rantai middleware.

Contoh nyata ada di [`examples/webapp`](https://github.com/lnx645/galang/tree/master/examples/webapp)
(middleware logging ke jurnal systemd).

## Request Object

```galang
http.GET("/cari/{id}", fn($req) {
    $req.method          // "GET", "POST", "PUT", "DELETE"
    $req.path            // "/cari/42"
    $req.url             // "/cari/42?q=1"
    $req.query.q         // query parameter (?q=1)
    $req.params.id       // path parameter {id} → "42"
    $req.headers.accept  // header (huruf kecil)
    $req.cookies.sid     // cookie
    $req.session         // objek sesi (sama dengan http.session($req))
    $req.session_id      // id sesi; null bila sesi belum ada
    $req.body            // body mentah sebagai string
    return "ok"
})
```

Untuk body JSON, decode dulu:

```galang
http.POST("/api", fn($req) {
    $data = json_decode($req.body)
    return {diterima: $data}
})
```

## Aturan Response

Cukup `return` nilai — runtime menentukan status, type, dan body:

| Yang di-return | Response |
|---|---|
| `string` | `200 OK`, `text/html` |
| `array` / `object` | `200 OK`, `application/json` (JSON otomatis) |
| object terstruktur dengan `body` | lihat di bawah |

```galang
// String → HTML 200
return "Halo"

// Array/Object → JSON 200 otomatis
return ["a", "b"]             // [{"..."}] JSON array
return {status: "ok"}         // JSON object

// Structured response — kendali penuh atas status/type/body/headers/cookies
return {status: 404, type: "text/html", body: "<h1>Not Found</h1>"}
return {status: 201, body: "dibuat"}
return {type: "text/html", body: "OK", cookies: {session: "abc"}}
```

Kunci structured response:

| Kunci | Default | Keterangan |
|---|---|---|
| `status` | 200 | kode status HTTP |
| `type` | `text/html` (string) / `application/json` (objek terstruktur) | Content-Type |
| `body` | — | isi response |
| `headers` | — | object header tambahan |
| `cookies` | — | object cookie yang di-set |

## json() — kontrol penuh

```galang
return http.json({id: 1}, 201)
return http.json({msg: "ok"}, 200, {"X-Custom": "value"})
```

Signature: `http.json($data, $status = 200, $headers = {})`.
Content-Type otomatis `application/json; charset=utf-8`.

## Error & Status Code

### Error dari `throw` → status + JSON

Error yang dilempar di dalam handler dijawab server dengan **status sesuai
error** dan body JSON `{code, message, status}`:

```galang
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

| Konstruktor | Status respons |
|---|---|
| `bad_request(...)` | 400 |
| `unauthorized(...)` | 401 |
| `forbidden(...)` | 403 |
| `not_found(...)` | 404 |
| `conflict(...)` | 409 |
| `error(...)` / `server_error(...)` | 500 |

### `json_decode` → error lempar 400

Body JSON yang rusak membuat `json_decode()` melempar error `json_error`
dengan status **400**. Error ini bisa ditangkap `try/catch`; bila lolos dari
handler, server menjawab:

```json
{"code":"json_error","message":"invalid JSON: ...","status":400}
```

### Error internal → 500 polos

Bug internal (pembagian nol, index di luar batas, variabel undefined)
menghasilkan **500** dengan body polos. **Detail error dan stack trace hanya
dicatat ke stderr server** — tidak pernah bocor ke klien.

```galang
http.GET("/rusak", fn($req) {
    return 1 / 0        // → 500, body polos; detail di stderr
})
```

## Cookie

```galang
// Set cookie lewat structured response
return {type: "text/html", body: "OK", cookies: {session: "abc"}}

// Baca cookie di request
http.GET("/cek", fn($req) {
    print($req.cookies.session)
    return "ok"
})
```

## Sesi (Session)

Sesi mengikat data per pengunjung lewat cookie `galang_session`
(HttpOnly, SameSite=Lax, Path=/) dan objek `$req.session` yang disimpan
di memori.

```galang
http.GET("/keranjang", fn($req) {
    $s = http.session($req)   // objek yang sama dengan $req.session
    $n = 0
    if $s.item != null {
        $n = $s.item + 1
    }
    $s.item = $n
    return {item: $n, baru: $req.session_id == null}
})
```

Aturan:

- `$req.session`, `http.session($req)`, dan `http.session()` (tanpa
  argumen, hanya di dalam handler) menunjuk ke **objek sesi yang sama** —
  obyek biasa, baca/tulis medan bebas.
- Cookie hanya terbit bila sesi **ditulis** pada request yang belum punya
  id — trafik anonim tidak menghasilkan cookie dan tidak masuk store.
- Id sesi acak 128-bit; semua request berikutnya yang membawa cookie itu
  memakai objek sesi yang sama.
- `http.session_destroy($req)` mengosongkan sesi, menghapusnya dari
  store, dan mengirim cookie kedaluwarsa (`Max-Age=0`).
- `http.session()` / `http.session_destroy()` di luar handler adalah
  galat dengan pesan jelas.
- Store **in-memory**: hilang saat proses berakhir (restart) dan tanpa
  kadaluwarsa otomatis — jujur untuk skala demo/pengembangan; untuk
  produksi multi-proses diperlukan store eksternal.
- Berlaku untuk rute HTTP biasa; static/SSE/WS tidak melewatinya
  (sama seperti middleware).

Contoh nyata: `GET /api/kunjungan` di
[`examples/webapp`](https://github.com/lnx645/galang/tree/master/examples/webapp)
(hitungan per sesi + `?reset=1` untuk destroy).

## Static Files

```galang
http.static("/public/", "public")
// GET /public/style.css → menyajikan file public/style.css
```

URL prefix `/public/` dipetakan ke direktori `public/` di working directory.

## Template Blade

```galang
http.views("views")            // direktori template

http.GET("/", fn($req) {
    $data = {title: "Home", user: {name: "Dadan"}, items: [1, 2, 3]}
    return http.render("home.blade", $data)
})
```

- `http.views($dir)` — set direktori template.
- `http.render($namaFile, $data)` — render → string HTML (200).
- Variabel `$data.kunci` tersedia sebagai `$nama` di template.

### Sintaks Blade

```blade
{{-- komentar blade --}}
<h1>{{ $title }}</h1>          {{-- escape HTML otomatis --}}
{!! $rawHTML !!}              {{-- tanpa escape --}}

@if($x > 3 && !$admin)
  <p>Besar</p>
@elseif($x == 2)
  <p>Dua</p>
@else
  <p>Kecil</p>
@endif

@foreach($items as $item)
  <li>{{ $item }}</li>
@else
  <li>Kosong</li>
@endforeach

@foreach($umur as $nama => $nilai) {{-- bentuk kunci => nilai --}}
  <li>{{ $nama }}: {{ $nilai }}</li>
@endforeach

@extends("layouts/app")
@section("judul", "Beranda")   {{-- section inline --}}
@section("content")
  <p>Isi halaman…</p>
@endsection
@yield("judul")                {{-- slot di layout --}}
@yield("scripts", "")          {{-- default bila child tidak mengisi --}}

@include("partials/nav")
@include("partials/kode", {judul: "main.ga", isi: $contoh}) {{-- partial berdata --}}
```

Ekspresi di dalam `{{ }}` dan kondisi `@if` mendukung operator lengkap:
perbandingan (`>`, `>=`, `==`, `!=`, `<`, `<=`), logika (`&&`, `||`, `!`,
`and`, `or`, `not`), aritmetika (`+`, `-`, `*`, `/`), pemanggilan fungsi
(`count($x)`, `len($x)`), pipe (`| upper`), dan literal object `{k: v}`
(dipakai untuk data `@include`). Di luar `{{ }}`, `$var` adalah teks biasa.

| Directive | Fungsi |
|---|---|
| `{{ $x }}` | ekspresi, HTML-escape |
| `{!! $x !!}` | ekspresi mentah |
| `@if` `@elseif` `@else` `@endif` | percabangan (boleh pakai operator) |
| `@foreach` `@else` `@endforeach` | perulangan (`@else` saat kosong); `@foreach($m as $k => $v)` untuk kunci |
| `@extends("layout")` | pewarisan layout |
| `@section("nama")` `@section("nama", "nilai")` | isi slot layout (blok / inline) |
| `@yield("nama")` `@yield("nama", "default")` | slot di layout + default |
| `@include("partial")` `@include("partial", {k: v})` | sisipkan partial (dengan data) |
| `{{-- --}}` | komentar |

## SSE (Server-Sent Events)

```galang
http.stream("/events", fn($req) {
    return "ping"      // setiap return = satu event
})
```

Klien: `new EventSource("/events")`.

## WebSocket

```galang
http.ws("/chat", fn($msg) {
    return "echo: " + $msg    // nilai return dikirim balik ke klien
})
```

## Contoh Lengkap

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

Jalankan:

```bash
gar run main.ga
curl http://localhost:8869/health     # {"status":"ok"}
```
