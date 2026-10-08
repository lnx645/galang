# Contoh Web Publik Garurda

Aplikasi contoh yang menampilkan modul `http` — termasuk **middleware
`http.use`** — template **Blade**, file statis CSS/JS, dan respons JSON
otomatis. Inilah aplikasi yang disajikan service `garurda-demo.service` di
port **5800**.

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
├── migrate.ga              # contoh migrasi (modul database — v0.3)
└── schema.sql              # contoh skema  (modul database — v0.3)
```

## Rute

| Method | Rute | Respons |
|---|---|---|
| GET | `/` | beranda (Blade) |
| GET | `/fitur` | tabel fitur bahasa (Blade) |
| GET | `/halo/{nama}` | template tanpa layout |
| GET | `/api/sapa/{nama}` | JSON `{sapaan, panjang, waktu_ms}` |
| POST | `/api/echo` | JSON echo; body rusak → **400** `json_error` |
| GET | `/api/user/{id}` | id `"1"` → user, selain itu → **404** |
| GET | `/api/daftar` | array fitur → JSON array |
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

## Deploy sebagai service systemd

Salinan unit ada di `deploy/garurda-demo.service`:

```bash
# 1. aplikasi + binary
mkdir -p /opt/garurda-demo
cp main.ga /opt/garurda-demo/
cp -r views public /opt/garurda-demo/
go build -o /usr/local/bin/gar ./cmd/gar

# 2. user service (non-root) + unit
useradd --system --home-dir /opt/garurda-demo --shell /usr/sbin/nologin garurda
cp deploy/garurda-demo.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now garurda-demo.service

# 3. cek
curl -s localhost:5800/api/sapa/dunia
journalctl -u garurda-demo -f
```

> **Catatan:** `migrate.ga` dan `schema.sql` adalah contoh skema untuk
> modul `database` yang dikerjakan pada v0.3 — belum bisa dijalankan
> sekarang.

## Lisensi

MIT
