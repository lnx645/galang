# Memulai Cepat

## Instalasi

### Pemasang Resmi

Setiap [rilis](https://github.com/lnx645/galang/releases) menyertakan
pemasang siap pakai:

| Platform | Aset | Cara pakai |
|---|---|---|
| Windows x64 & ARM | `Garurda-Setup-<v>.exe` (Inno Setup) | jalankan wizard (izin admin); `gar` masuk PATH otomatis; cabut lewat "Tambah atau hapus program" |
| Debian / Ubuntu | `garurda_<v>_amd64.deb` / `garurda_<v>_arm64.deb` | `sudo dpkg -i garurda_<v>_amd64.deb` — terpasang di `/usr/bin/gar` |
| macOS (Intel + Apple Silicon) | `Garurda-<v>-macos.pkg` (universal) | `sudo installer -pkg Garurda-<v>-macos.pkg -target /` — terpasang di `/usr/local/bin/gar` |

Catatan:

- Paket `.deb` khusus Debian/Ubuntu; distro lain pakai binari manual di
  bawah.
- Paket macOS belum ditandatangani/notarized Developer ID — bila macOS
  menolak membukanya, jalankan perintah `installer` di atas dari terminal
  (atau klik kanan → Buka).

### Dari Binary

Download binary dari [Releases](https://github.com/lnx645/galang/releases)
atau direktori `dist/`, lalu pindahkan ke PATH:

```bash
# Linux
sudo cp gar-linux-amd64 /usr/local/bin/gar
sudo chmod +x /usr/local/bin/gar

# macOS
sudo cp gar-darwin-arm64 /usr/local/bin/gar
sudo chmod +x /usr/local/bin/gar

# Windows — pindahkan gar-windows-amd64.exe ke folder di PATH, mis. C:\Windows
```

Pastikan terpasang:

```bash
gar version
# gar 0.2.0
```

### Dari Source

Prasyarat: Go (dikompilasi dengan Go 1.19+).

```bash
git clone https://github.com/lnx645/galang.git
cd galang
export HOME=/root          # bila HOME kosong di environment Anda
go build -o bin/gar ./cmd/gar
./bin/gar version
```

## Hello World

Simpan sebagai `hello.ga`:

```garurda
// hello.ga
$name = "Garurda"
print("Halo, $name!")
```

Jalankan:

```bash
gar run hello.ga
# Halo, Garurda!
```

## Variabel dan Tipe

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

Sigil `$` **opsional** — `nama` dan `$nama` adalah variabel yang sama.

Konversi eksplisit, tanpa coerciion otomatis:

```garurda
print(int("42"))      // 42
print(str(7))         // "7"
print(float("1.5"))   // 1.5
// "5" + 5            // ERROR: tidak ada coerciion — pakai int()/str()
```

## Fungsi Pertama

```garurda
fn greet($nama, $sapaan = "Halo") {
    return "${sapaan}, ${nama}!"
}

print(greet("Dadan"))            // Halo, Dadan!
print(greet("Dadan", "Selamat")) // Selamat, Dadan!
```

## REPL Interaktif

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

REPL mendukung beberapa perintah:

| Perintah | Fungsi |
|---|---|
| `.help` atau `help` | Tampilkan bantuan |
| `.exit`, `.quit`, `exit`, `quit` | Keluar |

Blok multi-baris (mis. isi `fn` / `if` / `for`) otomatis dilanjutkan sampai
kurawal `}` ditutup.

Preload script saat membuka REPL:

```bash
gar repl setup.ga     // setup.ga dieksekusi dulu, lalu REPL siap
```

## Proyek Web Pertama

Struktur minimal:

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

http.listen(8869)
```

`views/home.blade`:

```blade
<h1>{{ $title }}</h1>
<p>Halo dari Garurda.</p>
```

Jalankan lalu buka `http://localhost:8869`:

```bash
gar run main.ga
```

Detail lengkap: [Web Runtime](web.md).

## Argumen Program

Argumen setelah `--` tersedia di variabel global `args` (array of string):

```bash
gar run tugas.ga -- alice 30
```

```garurda
// tugas.ga
print(args)          // [alice, 30]
```

## Langkah Berikutnya

- [Sintaks Bahasa](language.md) — referensi bahasa lengkap
- [Referensi API](api.md) — semua fungsi bawaan
- [Modul Standar](modules.md) — `strings`, `math`, `time`, `file`
- [Web Runtime](web.md) — HTTP, Blade, SSE, WebSocket
- [CLI](cli.md) — perintah dan deployment
