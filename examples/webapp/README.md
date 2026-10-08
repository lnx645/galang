# Contoh Web Publik Garurda

Aplikasi contoh yang menampilkan modul `http` — termasuk **middleware
`http.use`**, **sesi `http.session`**, dan **database SQLite nyata**
(`database.connect`) — template **Blade**, file statis CSS/JS, dan
respons JSON otomatis. Inilah aplikasi yang disajikan service
`garurda-demo.service` di port **5800**.

## Menjalankan

```bash
cd examples/webapp
gar run main.ga
# buka http://localhost:5800
```

## Struktur

```
examples/webapp/
├── main.ga                 # route + data (port 5800)
├── views/
│   ├── layouts/app.blade   # layout: @yield judul/isi/skrip + partial
│   ├── partials/nav.blade  # navigasi responsif (toggle tanpa JS)
│   ├── partials/footer.blade
│   ├── partials/kode.blade # blok kode berdata (@include + object)
│   ├── home.blade          # beranda: hero, kartu @foreach, panel coba API
│   ├── fitur.blade         # tabel fitur: @foreach + @if/@elseif/@else
│   └── welcome.blade       # template polos tanpa layout (/halo/{nama})
├── public/
│   ├── style.css           # CSS murni, responsif, tanpa framework
│   └── app.js              # JS polos: fetch API, jam, menu aktif
├── migrate.ga              # migrasi terpisah (opsional, bukti langkah)
├── schema.sql              # skema idempoten; dibaca main.ga saat start
└── demo.db                 # SQLite, dibuat otomatis (gitignored)
```

## Rute

| Method | Rute | Respons |
|---|---|---|
| GET | `/` | beranda (Blade) |
| GET | `/fitur` | tabel fitur bahasa (Blade) |
| GET | `/halo/{nama}` | template tanpa layout |
| GET | `/api/sapa/{nama}` | JSON `{sapaan, panjang, waktu_ms}` |
| POST | `/api/echo` | JSON echo; body rusak → **400** `json_error` |
| GET | `/api/user/{id}` | dari SQLite (seed id 1, 2); tak ada → **404** |
| GET | `/api/daftar` | array fitur → JSON array |
| GET | `/api/kunjungan` | hitungan per sesi (cookie HttpOnly; `?reset=1` = destroy) |
| GET | `/public/*` | file statis (CSS/JS) |
| — | rute tak dikenal | **404**; method salah → **405** |

## Middleware

Satu middleware global mencatat setiap request ke stdout — terbaca di
jurnal systemd via `journalctl -u garurda-demo`:

```ga
http.use(fn($req, $next) {
    print($req.method + " " + $req.path)
    return $next($req)
})
```

## Sesi

`GET /api/kunjungan` memakai sesi HTTP: cookie `garurda_session`
(HttpOnly, SameSite=Lax) terbit pada kunjungan pertama yang **menulis**
sesi; objek `$req.session` (sama dengan `http.session($req)`) hidup di
store in-memory selama proses. `?reset=1` memanggil
`http.session_destroy($req)` — sesi dihapus dan cookie dikirim kedaluwarsa.

## Database

Saat start, `main.ga` membuka SQLite dan menjalankan `schema.sql`
(idempoten — aman diulang):

```ga
use "database"
use "file"

$db = database.connect("sqlite:demo.db")
$db.exec(file.read("schema.sql"))

http.GET("/api/user/{id}", fn($req) {
    $row = $db.query_first("SELECT id, name, email FROM users WHERE id = ?", [$req.params.id])
    if $row == null {
        throw not_found("user dengan id " + $req.params.id + " tidak ada")
    }
    return $row
})
```

Jalankan `gar run migrate.ga` untuk melihat migrasi sebagai langkah
terpisah (mencetak jumlah baris setelahnya).

## Deploy sebagai service systemd

Salinan unit ada di `deploy/garurda-demo.service`:

```bash
# 1. aplikasi + binary
mkdir -p /opt/garurda-demo
cp main.ga migrate.ga schema.sql /opt/garurda-demo/
cp -r views public /opt/garurda-demo/
go build -o /usr/local/bin/gar ./cmd/gar

# 2. user service (non-root) + unit
useradd --system --home-dir /opt/garurda-demo --shell /usr/sbin/nologin garurda
# SQLite menulis demo.db saat start — direktori harus milik user service
chown -R garurda:garurda /opt/garurda-demo
cp deploy/garurda-demo.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now garurda-demo.service

# 3. cek
curl -s localhost:5800/api/sapa/dunia
curl -s localhost:5800/api/user/1    # dari SQLite
journalctl -u garurda-demo -f
```

> **Catatan:** `demo.db` dibuat otomatis saat pertama start (bootstrap
> `schema.sql` yang idempoten); hapus saja untuk reset ke data seed.

## Lisensi

MIT
