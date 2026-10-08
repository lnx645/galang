# Garurda v0.2 — Dokumentasi

Garurda adalah bahasa pemrograman web yang ditulis dalam Go. Runtime webnya (HTTP server, template Blade, WebSocket, SSE, opcache) sudah terimplementasi penuh.

## Daftar Isi

- [Memulai Cepat](getting-started.md)
- [Sintaks Bahasa](language.md)
- [Modul Standar](modules.md)
- [Web Runtime (HTTP)](web.md)
- [CLI](cli.md)

## Download Binary

Binary siap pakai tersedia di direktori `dist/`:

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

# Coba contoh web
gar run examples/webapp/main.ga
```
