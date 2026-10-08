# Garurda Web App Example

Contoh aplikasi web lengkap menggunakan Garurda v0.2.

## Fitur Demo

- **Authentication**: Login, Register, Logout dengan session
- **CRUD Posts**: Create, Read, Update, Delete dengan Blade template
- **REST API**: Endpoint `/api/posts` untuk integrasi frontend
- **WebSocket**: Chat real-time
- **SSE**: Notifikasi real-time
- **Database**: SQLite (default), support MySQL/PostgreSQL

## Struktur

```
examples/webapp/
├── main.ga              # Entry point
├── migrate.ga           # Database migration
├── schema.sql           # Database schema
├── views/
│   ├── layouts/app.blade        # Layout utama
│   ├── home.blade               # Beranda
│   ├── auth/login.blade         # Login
│   ├── dashboard.blade          # Dashboard
│   └── posts/
│       ├── index.blade          # List posts
│       ├── create.blade         # Buat post
│       └── edit.blade           # Edit post
├── public/
│   └── style.css                # Custom CSS
└── schema.sql                   # Database schema
```

## Menjalankan

```bash
# 1. Migrasi database
gar run examples/webapp/migrate.ga

# 2. Jalankan server
gar run examples/webapp/main.ga

# 3. Buka browser
# http://localhost:8868
```

## Konfigurasi Environment

Buat file `.env` di root project:

```env
APP_ENV=development
DATABASE_URL=sqlite3:./app.db
SESSION_SECRET=your-secret-key-here
```

## Fitur Utama yang Didemonstrasikan

### 1. Routing & Controller
- Route grouping (web, api, ws)
- Parameter binding (`{id}`)
- Method spoofing (`_method=PUT`)

### 2. Blade Template Engine
- Layout inheritance (`@extends`, `@section`, `@yield`)
- Control structures (`@if`, `@foreach`, `@ifset`)
- Partials (`@include`)
- Auto-escape XSS protection (`{{ }}`)

### 3. Database Layer
- Query builder (`$db.query`, `$db.query_first`, `$db.exec`)
- Transactions (`$db.transaction()`)
- Parameterized queries (anti-SQL injection)

### 4. Authentication
- Session-based auth
- Password hashing (bcrypt)
- CSRF protection (`@csrf`)

### 5. Async Runtime
- `async fn`, `await`, `gather`
- Background tasks

### 6. Real-time
- WebSocket (`http.ws()`)
- Server-Sent Events (`response.stream()`)

### 7. REST API
- JSON response (`response.json()`)
- Pagination
- Error handling

## Database Schema

Lihat `schema.sql` untuk detail tabel:
- `users` - Pengguna
- `posts` - Artikel/Postingan
- `comments` - Komentar
- `sessions` - Session storage

## API Endpoints

| Method | Endpoint | Deskripsi |
|--------|----------|-----------|
| GET | `/` | Beranda |
| GET | `/login` | Halaman login |
| POST | `/login` | Proses login |
| POST | `/logout` | Logout |
| GET | `/dashboard` | Dashboard (auth) |
| GET | `/api/posts` | List posts (paginated) |
| GET | `/api/posts/{id}` | Detail post |
| POST | `/api/posts` | Buat post (auth) |
| PUT | `/api/posts/{id}` | Update post (auth) |
| DELETE | `/api/posts/{id}` | Hapus post (auth) |
| WS | `/ws/chat` | WebSocket chat |
| SSE | `/events/notifications` | SSE notifications |

## Testing

```bash
# Run tests
gar test

# Benchmark
make bench
```

## Deployment

```bash
# Build production binary
make build

# Run dengan env production
APP_ENV=production DATABASE_URL=postgres://... ./bin/gar run examples/webapp/main.ga
```

## Struktur Kode

```
main.ga              # Bootstrap & routing
├── http.GET/POST/.. # Route definitions
├── http.ws()        # WebSocket
├── http.GET SSE     # Server-Sent Events
└── http.listen()    # Start server
```

## Customization

Tambahkan middleware custom:
```ga
http.use(fn($req, $next) {
    // logging, auth check, rate limit, etc.
    return $next($req)
})
```

Tambahkan helper template:
```ga
use "template"
template.func("money", fn($v) => "Rp " + number_format($v, 0, ",", "."))
```

## License

MIT License - Garurda Team