# Garurda CLI

The Garurda interpreter is the `gar` binary.

## Commands

```bash
gar run <file.ga> [-- argumen...]   # run the program
gar repl [file.ga]                  # interactive session (aliases: shell, sh)
gar gne <subcommand>                # manage native extensions (install/pack/list/remove)
gar version                         # show the version (aliases: -v, --version)
gar help                            # help (aliases: -h, --help)
```

## Running a Program

```bash
gar run hello.ga
```

The `.ga` extension is required; missing file → an error message and exit
code 2.

### Program arguments — `--`

Everything **after `--`** is passed on to the program and available in the
global `args` variable (an array of strings):

```bash
gar run tugas.ga -- alice 30 --port 9000
```

```garurda
// tugas.ga
print(args)            // [alice, 30, --port, 9000]
print(args[0])         // alice
```

Without `--`, `args` is empty (`[]`).

### Exit code

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | runtime error (message + `file:line:column` position on stderr) |
| `2` | incorrect CLI usage (missing file, unknown command) |

## REPL

```bash
gar repl               # start empty
gar repl setup.ga      # preload a script first, then the REPL is ready
```

Example session:

```text
> 2 + 2
4
> "abc".upper()
ABC
> .help
> .exit
```

### REPL Commands

| Command | Purpose |
|---|---|
| `.help` / `help` | show help |
| `.exit` / `.quit` / `exit` / `quit` | exit |

Multi-line blocks (the body of `fn`, `if`, `for`, `try`, etc.) are continued
automatically until the closing `}` brace — you don't have to write the whole
expression on one line.

Preloading `file.ga` is useful for setting up functions/modules before you
start experimenting.

## Extension Manager — `gar gne`

Native extensions (GNE) are installed, listed, and removed with the
`gar gne` subcommand (available since `gar` 0.6.0):

```bash
gar gne pack <extension-dir> [-o output.zip]     # package a build into a zip
gar gne install <spec> [--force] [--dir D]       # install a package
gar gne list [--dir D]                           # list installed extensions
gar gne remove <name> [--dir D]                  # remove an extension
```

### The `install` spec

| Form | Meaning |
|---|---|
| `redis` | official-channel shortcut — downloaded from the latest GitHub release |
| `redis@0.6.0` | pinned version (the `v` prefix is accepted: `redis@v0.6.0`) |
| `https://.../redis.zip` | HTTPS URL — plain HTTP is rejected |
| `./redis.zip` | local package file |

The official extensions shipped as release assets are **`redis`** and
**`smtp`** (see [Standard Modules](modules.md)).

```bash
gar gne install redis         # latest release
gar gne install smtp@0.6.0    # pin a version
gar gne install ./redis.zip   # from a file
gar gne list
gar gne remove smtp
```

### Install-time safety

- Packages install **flat** into `~/.garurda/gne/` — the
  `<name>.so`/`.dylib`/`.dll` binary plus a `<name>.gne.json` sidecar,
  exactly where `use "name"` looks. `--dir D` changes the target (point
  `GNE_PATH` there if you use a non-default location).
- A package is a multi-platform zip: `manifest.json` (name, version,
  `gne_abi`, one entry per `GOOS-GOARCH` with sha256 + size) plus the
  per-platform binaries. The installer validates the manifest and ABI,
  **recomputes sha256 before and after** writing to disk, rejects
  zip-slip/symlink entries, and caps package size at 64 MiB.
- It is a hard error when the package provides no binary for the running
  platform (the available platforms are listed in the message).
- `--force` only overrides: an existing install, installing on a `gar`
  binary built without CGO, or preparing a cross-platform install (the
  first platform in sorted order is chosen).

### `pack`

`pack` packages the builds found in `ext/<name>/build/<GOOS-GOARCH>/`:

```bash
make ext                                  # cross-compile linux/windows/ARM64
gar gne pack ext/redis -o dist/redis.zip
```

## Environment

`gar run` does not read special flags; application configuration (port etc.)
is set inside the script, e.g. `http.listen(8869)`.

## Systemd Service (deployment)

Example unit for production:

```ini
[Unit]
Description=Garurda App
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/gar run /srv/app/main.ga
WorkingDirectory=/srv/app
Restart=always
RestartSec=3
User=www-data

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable gapp.service
sudo systemctl start gapp.service
sudo systemctl status gapp.service
sudo journalctl -u gapp.service -f    # tail the log (including stderr)
```

Note: internal errors in HTTP handlers are logged to **stderr** — `journalctl`
picks them up.

## Building from Source

```bash
export HOME=/root          # if HOME is empty in your environment
go build -o bin/gar ./cmd/gar
./bin/gar version
```

Running the test suite:

```bash
go test ./...
```
