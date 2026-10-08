# Garurda — Dokumentasi

Garurda adalah bahasa pemrograman web yang ditulis dalam **Go**. Runtime webnya
lengkap: server HTTP, routing, template Blade, file statis, WebSocket, dan SSE.
Eksekusinya memakai bytecode VM dengan konvensi pemanggilan unboxed — ringan dan
cepat, dengan jejak memori (RSS puncak) terukur di bawah JavaScript pada seluruh
pola benchmark.

```garurda
use "http"

http.GET("/", fn($req) {
    return {msg: "Halo dari Garurda!"}
})

http.listen(8868)
```

## Daftar Isi

| Halaman | Isi |
|---|---|
| [Memulai Cepat](getting-started.md) | Instalasi, Hello World, REPL, proyek pertama |
| [Sintaks Bahasa](language.md) | Referensi bahasa lengkap — leksikal, tipe, operator, kontrol alur, fungsi, async, error |
| [Referensi API](api.md) | Semua fungsi bawaan (builtins), method string/array, nilai error |
| [Modul Standar](modules.md) | `strings`, `math`, `time`, `file`, `database`, `http` |
| [Web Runtime](web.md) | Server HTTP, routing, request/response, Blade, SSE, WebSocket |
| [CLI](cli.md) | Perintah `gar`, argumen program, REPL, deployment systemd |

## Status Versi

Versi saat ini: **v0.2.1** — rilis binari tersedia di
[GitHub Releases](https://github.com/lnx645/galang/releases).

Sudah lengkap:

- HTTP server, routing, path params, 404/405, auto-JSON, cookies
- Template Blade (`@if`, `@foreach`, `@extends`, `@section`, `@yield`, `@include`)
- SSE (`http.stream`) dan WebSocket (`http.ws`)
- Operator `&&`, `||`, `!` selain `and`, `or`, `not`
- `async fn` / `await` / `gather` / `spawn` (eksekusi kooperatif)
- `try` / `catch` / `finally`
- Middleware HTTP (`http.use`) + sesi HTTP (`http.session`, cookie HttpOnly)
- Modul `database` nyata: SQLite, MySQL, PostgreSQL
- Bytecode VM + fast path int

Belum ada (direncanakan):

- SMTP dan unit-test framework
- Generic `<T>` (v1), class & method (v2)
- Paralelisme I/O async — async saat ini kooperatif

## Download Binary

Binary siap pakai tersedia di direktori `dist/` dan di
[Releases](https://github.com/lnx645/galang/releases):

| Platform | File |
|---|---|
| Linux AMD64 | `gar-linux-amd64` |
| Linux ARM64 | `gar-linux-arm64` |
| Windows AMD64 | `gar-windows-amd64` |
| Windows ARM64 | `gar-windows-arm64` |
| macOS AMD64 | `gar-darwin-amd64` |
| macOS ARM64 | `gar-darwin-arm64` |

## Quick Start

```bash
# Jalankan script
gar run hello.ga

# Coba contoh web (port 5800)
cd examples/webapp && gar run main.ga

# REPL interaktif
gar repl
```

Selanjutnya: [Memulai Cepat](getting-started.md).
