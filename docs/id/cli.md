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

## Pengelola Ekstensi — `gar gne`

Ekstensi native (GNE) dipasang, didaftar, dan dilepas lewat subperintah
`gar gne` (tersedia sejak `gar` 0.6.0):

```bash
gar gne pack <dir-ekstensi> [-o keluaran.zip]     # kemas hasil build jadi paket
gar gne install <spesifikasi> [--force] [--dir D]  # pasang paket ekstensi
gar gne list [--dir D]                            # daftar ekstensi terpasang
gar gne remove <nama> [--dir D]                   # lepas ekstensi
```

### Spesifikasi `install`

| Bentuk | Arti |
|---|---|
| `redis` | shortcut kanal resmi — unduh dari rilis GitHub terbaru |
| `redis@0.6.0` | shortcut versi tertentu (awalan `v` boleh: `redis@v0.6.0`) |
| `https://.../redis.zip` | URL HTTPS — HTTP polos ditolak |
| `./redis.zip` | berkas paket lokal |

Ekstensi resmi yang tersedia sebagai aset rilis: **`redis`**, **`smtp`**,
**`uuid`**, **`jwt`**, dan **`httpclient`** (lihat
[Modul Standar](modules.md)).

```bash
gar gne install redis         # rilis terbaru
gar gne install smtp@0.6.0    # kunci versi
gar gne install uuid          # dari rilis terbaru
gar gne install ./redis.zip   # dari berkas
gar gne list
gar gne remove smtp
```

### Keamanan pemasangan

- Pemasangan **FLAT** ke `~/.galang/gne/` — `<nama>.so`/`.dylib`/`.dll` plus
  sidecar `<nama>.gne.json`, persis direktori yang dicari `use "nama"`.
  `--dir D` mengalihkan tujuan (arahkan `GNE_PATH` ke situ bila di luar
  default).
- Paket adalah zip multi-platform: `manifest.json` (nama, versi, `gne_abi`,
  entri per `GOOS-GOARCH` dengan sha256 + ukuran) dan binari per platform.
  Pemasang memvalidasi manifest dan ABI, **menghitung ulang sha256 sebelum
  dan sesudah** tulis ke disk, menolak entri zip-slip/tautan simbolik, dan
  membatasi ukuran paket 64 MiB.
- Galat keras bila paket tidak menyediakan binari untuk platform berjalan
  (daftar platform yang tersedia ikut disebut).
- `--force` hanya untuk: menimpa instalasi yang sudah ada, memasang di
  mesin yang binari `gar`-nya dibangun tanpa CGO, atau menyiapkan paket
  lintas-platform (platform pertama terurut dipilih).

### `pack`

`pack` mengemas hasil build yang ada di `ext/<nama>/build/<GOOS-GOARCH>/`:

```bash
make ext                                  # kompilasi linux/windows/ARM64
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
