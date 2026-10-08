# Getting Started

## Installation

### From a Binary

Download the binary from [Releases](https://github.com/lnx645/galang/releases)
or the `dist/` directory, then move it onto your PATH:

```bash
# Linux
sudo cp gar-linux-amd64 /usr/local/bin/gar
sudo chmod +x /usr/local/bin/gar

# macOS
sudo cp gar-darwin-arm64 /usr/local/bin/gar
sudo chmod +x /usr/local/bin/gar

# Windows — move gar-windows-amd64.exe to a folder on your PATH, e.g. C:\Windows
```

Verify that it is installed:

```bash
gar version
# gar 0.2.0
```

### From Source

Prerequisites: Go (built with Go 1.19+).

```bash
git clone https://github.com/lnx645/galang.git
cd galang
export HOME=/root          # if HOME is empty in your environment
go build -o bin/gar ./cmd/gar
./bin/gar version
```

## Hello World

Save it as `hello.ga`:

```garurda
// hello.ga
$name = "Garurda"
print("Halo, $name!")
```

Run it:

```bash
gar run hello.ga
# Halo, Garurda!
```

## Variables and Types

```garurda
$umur = 25            // int
$pi = 3.14            // float
$aktif = true         // bool
$kosong = null        // null
$nama = "Dadan"       // string
$angka = [1, 2, 3]    // array
$user = {nama: "Budi", umur: 30}   // object

print(type($umur))    // "int"
print(type($user))    // "object"
```

The `$` sigil is **optional** — `nama` and `$nama` are the same variable.

Conversion is explicit, with no automatic coercion:

```garurda
print(int("42"))      // 42
print(str(7))         // "7"
print(float("1.5"))   // 1.5
// "5" + 5            // ERROR: no coercion — use int()/str()
```

## Your First Function

```garurda
fn greet($nama, $sapaan = "Halo") {
    return "${sapaan}, ${nama}!"
}

print(greet("Dadan"))            // Halo, Dadan!
print(greet("Dadan", "Selamat")) // Selamat, Dadan!
```

## Interactive REPL

```bash
gar repl
```

```
> 2 + 2
4
> "abc".upper()
ABC
> .help
> .exit
```

The REPL supports a few commands:

| Command | Purpose |
|---|---|
| `.help` or `help` | Show help |
| `.exit`, `.quit`, `exit`, `quit` | Exit |

Multi-line blocks (e.g. the body of `fn` / `if` / `for`) are continued
automatically until the closing `}` brace.

Preload a script when starting the REPL:

```bash
gar repl setup.ga     // setup.ga runs first, then the REPL is ready
```

## Your First Web Project

Minimal structure:

```
myapp/
├── main.ga
└── views/
    └── home.blade
```

`main.ga`:

```garurda
use "http"

http.views("views")

http.GET("/", fn($req) {
    return http.render("home.blade", {title: "Beranda"})
})

http.listen(8868)
```

`views/home.blade`:

```blade
<h1>{{ $title }}</h1>
<p>Halo dari Garurda.</p>
```

Run it, then open `http://localhost:8868`:

```bash
gar run main.ga
```

Full details: [Web Runtime](web.md).

## Program Arguments

Arguments after `--` are available in the global `args` variable (an array of
strings):

```bash
gar run tugas.ga -- alice 30
```

```garurda
// tugas.ga
print(args)          // [alice, 30]
```

## Next Steps

- [Language Reference](language.md) — complete language reference
- [API Reference](api.md) — all built-in functions
- [Standard Library](modules.md) — `strings`, `math`, `time`, `file`
- [Web Runtime](web.md) — HTTP, Blade, SSE, WebSocket
- [CLI](cli.md) — commands and deployment
