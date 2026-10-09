# CLI GaLang

Interpreter GaLang adalah binary `gar`.

## Perintah

```bash
gar run <file.ga> [-- argumen...]   # jalankan program
gar repl [file.ga]                  # sesi interaktif (alias: shell, sh)
gar gne <subperintah>               # kelola ekstensi native (install/pack/list/remove)
gar version                         # tampilkan versi (alias: -v, --version)
gar help                            # bantuan (alias: -h, --help)
```

## Menjalankan Program

```bash
gar run hello.ga
```

Ekstensi `.ga` wajib; file tidak ada → pesan error dan exit code 2.

### Argumen program — `--`

Semua yang ada **setelah `--`** dioper ke program dan tersedia di variabel
global `args` (array of string):

```bash
gar run tugas.ga -- alice 30 --port 9000
```

```galang
// tugas.ga
print(args)            // [alice, 30, --port, 9000]
print(args[0])         // alice
```

Tanpa `--`, `args` kosong (`[]`).

### Exit code

| Kode | Arti |
|---|---|
| `0` | sukses |
| `1` | runtime error (pesan + posisi `file:line:kolom` di stderr) |
| `2` | penggunaan CLI salah (file tidak ada, perintah tak dikenal) |

## REPL

```bash
gar repl               # mulai kosong
gar repl setup.ga      # preload script dulu, lalu REPL siap
```

Contoh sesi:

```text
> 2 + 2
4
> "abc".upper()
ABC
> .help
> .exit
```

### Perintah REPL

| Perintah | Fungsi |
|---|---|
| `.help` / `help` | tampilkan bantuan |
| `.exit` / `.quit` / `exit` / `quit` | keluar |

Blok multi-baris (isi `fn`, `if`, `for`, `try`, dll.) otomatis dilanjutkan
sampai kurawal `}` ditutup — tidak perlu menulis ekspresi dalam satu baris.

Preload `file.ga` berguna untuk menyiapkan fungsi/modul sebelum bereksperimen.

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

The official extensions shipped as release assets are **`redis`**,
**`smtp`**, **`uuid`**, **`jwt`**, and **`httpclient`** (see
[Standard Modules](modules.md)).

```bash
gar gne install redis         # latest release
gar gne install smtp@0.6.0    # pin a version
gar gne install uuid          # from the latest release
gar gne install ./redis.zip   # from a file
gar gne list
gar gne remove smtp
```

### Install-time safety

- Packages install **flat** into `~/.galang/gne/` — the
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

`gar run` tidak membaca flag khusus; konfigurasi aplikasi (port dsb.) diatur
di dalam script, mis. `http.listen(8869)`.

## Systemd Service (deployment)

Contoh unit untuk production:

```ini
[Unit]
Description=GaLang App
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
sudo journalctl -u gapp.service -f    # pantau log (termasuk stderr)
```

Catatan: error internal handler HTTP dicatat ke **stderr** — `journalctl`
menangkapnya.

## Build dari Source

```bash
export HOME=/root          # bila HOME kosong di environment
go build -o bin/gar ./cmd/gar
./bin/gar version
```

Menjalankan test suite:

```bash
go test ./...
```
