# Memulai Cepat

## Instalasi

### Dari Binary

Download binary dari `dist/`, ekstrak, dan pindahkan ke PATH:

```bash
# Linux/macOS
cp gar-linux-amd64 /usr/local/bin/gar
chmod +x /usr/local/bin/gar

# Windows — pindahkan gar-windows-amd64.exe ke C:\Windows
```

### Dari Source

```bash
go build -o bin/gar ./cmd/gar
```

## Hello World

```garurda
// hello.ga
print("Hello, World!")
```

Jalankan:
```bash
gar run hello.ga
```

## Menjalankan Server Web

```garurda
use "http"

http.GET("/", fn($req) {
    return "Hello from Garurda!"
})

http.listen(8080)
```

```bash
gar run server.ga
# Buka http://localhost:8080
```

## Struktur Project

```
project/
  main.ga          # Entry point
  views/           # Template Blade
    home.blade
  public/          # File statis (CSS, JS, gambar)
    style.css
  views/           # Template engine
```
