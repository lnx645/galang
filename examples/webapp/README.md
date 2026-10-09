# GaLang Public Web Example

A sample application showcasing the `http` module — including **`http.use`
middleware**, **`http.session` sessions**, and a **real SQLite database**
(`database.connect`) — **Blade** templates, static CSS/JS files, and
automatic JSON responses. This is the application served by the
`galang-demo.service` unit on port **5800**.

## Running

```bash
cd examples/webapp
gar run main.ga
# open http://localhost:5800
```

## Structure

```
examples/webapp/
├── main.ga                 # routes + data (port 5800)
├── views/
│   ├── layouts/app.blade   # layout: @yield title/content/scripts + partials
│   ├── partials/nav.blade  # responsive navigation (JS-free toggle)
│   ├── partials/footer.blade
│   ├── partials/kode.blade # data-driven code block (@include + object)
│   ├── home.blade          # home: hero, @foreach cards, try-the-API panel
│   ├── fitur.blade         # feature table: @foreach + @if/@elseif/@else
│   └── welcome.blade       # bare template without a layout (/halo/{nama})
├── public/
│   ├── style.css           # pure CSS, responsive, no framework
│   └── app.js              # plain JS: fetch API, clock, active menu
├── migrate.ga              # separate migration (optional, step-by-step proof)
├── schema.sql              # idempotent schema; read by main.ga at startup
└── demo.db                 # SQLite, created automatically (gitignored)
```

## Routes

| Method | Route | Response |
|---|---|---|
| GET | `/` | home page (Blade) |
| GET | `/fitur` | language feature table (Blade) |
| GET | `/halo/{nama}` | template without a layout |
| GET | `/api/sapa/{nama}` | JSON `{sapaan, panjang, waktu_ms}` |
| POST | `/api/echo` | JSON echo; broken body → **400** `json_error` |
| GET | `/api/user/{id}` | from SQLite (seed ids 1, 2); missing → **404** |
| GET | `/api/daftar` | feature array → JSON array |
| GET | `/api/kunjungan` | per-session counter (HttpOnly cookie; `?reset=1` = destroy) |
| GET | `/public/*` | static files (CSS/JS) |
| — | unknown route | **404**; wrong method → **405** |

## Middleware

A single global middleware logs every request to stdout — readable in the
systemd journal via `journalctl -u galang-demo`:

```ga
http.use(fn($req, $next) {
    print($req.method + " " + $req.path)
    return $next($req)
})
```

## Sessions

`GET /api/kunjungan` uses HTTP sessions: the `galang_session` cookie
(HttpOnly, SameSite=Lax) is issued on the first visit that **writes** to
the session; the `$req.session` object (same as `http.session($req)`)
lives in the in-memory store for the lifetime of the process. `?reset=1`
calls `http.session_destroy($req)` — the session is deleted and an
expired cookie is sent.

## Database

At startup, `main.ga` opens SQLite and runs `schema.sql`
(idempotent — safe to repeat):

```ga
use "database"
use "file"

$db = database.connect("sqlite:demo.db")
$db.exec(file.read("schema.sql"))

http.GET("/api/user/{id}", fn($req) {
    $row = $db.query_first("SELECT id, name, email FROM users WHERE id = ?", [$req.params.id])
    if $row == null {
        throw not_found("user with id " + $req.params.id + " does not exist")
    }
    return $row
})
```

Run `gar run migrate.ga` to watch the migration as separate steps
(it prints the row counts afterwards).

## Deploying as a systemd service

A copy of the unit lives in `deploy/galang-demo.service`:

```bash
# 1. app + binary
mkdir -p /opt/galang-demo
cp main.ga migrate.ga schema.sql /opt/galang-demo/
cp -r views public /opt/galang-demo/
go build -o /usr/local/bin/gar ./cmd/gar

# 2. service user (non-root) + unit
useradd --system --home-dir /opt/galang-demo --shell /usr/sbin/nologin galang
# SQLite writes demo.db at startup — the directory must be owned by the service user
chown -R galang:galang /opt/galang-demo
cp deploy/galang-demo.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now galang-demo.service

# 3. checks
curl -s localhost:5800/api/sapa/dunia
curl -s localhost:5800/api/user/1    # from SQLite
journalctl -u galang-demo -f
```

> **Note:** `demo.db` is created automatically on the first start (the
> idempotent `schema.sql` bootstrap); just delete it to reset to the seed
> data.

## License

MIT
