# CLI Garurda

Interpreter Garurda adalah binary `gar`.

## Perintah

```bash
gar run <file.ga> [-- argumen...]   # jalankan program
gar repl [file.ga]                  # sesi interaktif (alias: shell, sh)
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

```garurda
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

## Environment

`gar run` tidak membaca flag khusus; konfigurasi aplikasi (port dsb.) diatur
di dalam script, mis. `http.listen(8868)`.

## Systemd Service (deployment)

Contoh unit untuk production:

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
