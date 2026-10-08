# Garurda — Documentation

Garurda is a web programming language written in **Go**. Its web runtime is
complete: HTTP server, routing, Blade templates, static files, WebSocket, and
SSE. Execution uses a bytecode VM with an unboxed calling convention — light and
fast, with measured peak memory (RSS) below JavaScript across the whole
benchmark suite.

```garurda
use "http"

http.GET("/", fn($req) {
    return {msg: "Halo dari Garurda!"}
})

http.listen(8868)
```

## Table of Contents

| Page | Contents |
|---|---|
| [Getting Started](getting-started.md) | Installation, Hello World, REPL, your first project |
| [Language Reference](language.md) | Complete language reference — lexical, types, operators, control flow, functions, async, errors |
| [API Reference](api.md) | All built-in functions (builtins), string/array methods, error values |
| [Standard Library](modules.md) | `strings`, `math`, `time`, `file`, `database`, `http` |
| [Web Runtime](web.md) | HTTP server, routing, request/response, Blade, SSE, WebSocket |
| [CLI](cli.md) | The `gar` command, program arguments, REPL, systemd deployment |

## Version Status

Current version: **v0.2.0** — binary releases are available on
[GitHub Releases](https://github.com/lnx645/galang/releases).

Already complete:

- HTTP server, routing, path params, 404/405, auto-JSON, cookies
- Blade templates (`@if`, `@foreach`, `@extends`, `@section`, `@yield`, `@include`)
- SSE (`http.stream`) and WebSocket (`http.ws`)
- The `&&`, `||`, `!` operators in addition to `and`, `or`, `not`
- `async fn` / `await` / `gather` / `spawn` (cooperative execution)
- `try` / `catch` / `finally`
- Bytecode VM + fast path int

Not yet available (planned):

- The `database` module — function names exist, the contents are still stubs
- HTTP sessions, SMTP, a unit-test framework — planned for v0.3
- Generics `<T>` (v1), classes & methods (v2)
- Async I/O parallelism — async is currently cooperative

## Download the Binary

Ready-to-use binaries are available in the `dist/` directory and on
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
# Run a script
gar run hello.ga

# Try the web example (port 5800)
cd examples/webapp && gar run main.ga

# Interactive REPL
gar repl
```

Next: [Getting Started](getting-started.md).
