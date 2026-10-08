# Ekstensi Native — GNE

**GNE** (Garurda Native Extension) memungkinkan Anda menulis ekstensi dalam
**C** — klien Redis, driver database, kripto, SMTP, apa pun — tanpa menyentuh
compiler maupun runtime Garurda. Runtime tetap Go; ekstensi dimuat saat `use`
memanggilnya lewat `dlopen` (Linux/macOS) atau `LoadLibrary` (Windows).

> Contoh yang sudah jalan: repo
> [garurda-gne-examples](https://github.com/lnx645/garurda-gne-examples)
> (`hello` dan `kv`). Syarat: `gar` **0.5.0+** dan gcc.

## Contoh kilat

```c
/* hello.c */
#include "gne.h"

static const gne_host_api *api;

static int add(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret) {
    int64_t a, b;
    (void)argc;
    if (api->get_int(ctx, argv[0], &a) != 0 ||
        api->get_int(ctx, argv[1], &b) != 0) {
        api->throw(ctx, "type_error", 500, "add() butuh dua integer");
        return 1;
    }
    *ret = api->int_new(ctx, a + b);
    return 0;
}

int gne_module_init(const gne_host_api *a, gne_ctx *ctx, gne_handle *out) {
    api = a;                              // simpan api: valid selama proses
    if (api->abi != GNE_ABI) {            // WAJIB: tolak ABI berbeda
        api->throw(ctx, "gne_abi", 500, "ABI berbeda");
        return 1;
    }
    api->define_fn(ctx, "add", 2, 2, add);
    *out = api->module(ctx);              // namespace modul
    return 0;
}
```

```sh
gcc -shared -fPIC -I <arah gne.h> -o hello.so hello.c   # macOS: -dynamiclib
mkdir -p gne && mv hello.so gne/
```

```garurda
use "hello"
print(hello.add(2, 3))    // 5
```

## Resolusi `use`

Urutan resolusi tetap: **bawaan → file `.ga` → GNE** (parser dan compiler
tidak berubah sama sekali). Bila langkah file tidak menemukan apa pun,
interpreter mencari ekstensi native pada urutan:

1. `./gne/` — relatif terhadap **direktori file yang memanggil `use`**
   (menang untuk pengembangan proyek);
2. setiap entri `$GNE_PATH` (dipisah `:` di Linux/macOS, `;` di Windows;
   path relatif diselesaikan terhadap direktori pemanggil);
3. `~/.garurda/gne` — global per pengguna.

Untuk `use "redis"`, yang dicari: `redis.so` (Linux), `redis.dylib` atau
`redis.so` (macOS), `redis.dll` (Windows). Path eksplisit juga boleh:
`use "lib/redis.so"` langsung memuat berkas itu (relatif terhadap file
pemanggil). Galat "not found" menyebut **semua** kandidat yang dicoba.

Berkas yang sudah dimuat di-cache: `use` kedua memakai objek namespace yang
sama; dua nama yang menunjuk berkas sama berbagi satu instance.

## Titik masuk

Ekstensi **wajib** mengekspor tepat satu simbol:

```c
GNE_EXPORT int gne_module_init(const gne_host_api *api, gne_ctx *ctx,
                               gne_handle *out);
```

- Bangun namespace (biasanya `api->object()` + `api->define_fn()` /
  `api->define_method()`), lalu tulis handle-nya ke `*out`
  (`api->module(ctx)`), atau tulis `0` untuk memakai objek bawaan.
- Return `0` = sukses. Untuk gagal: `api->throw(...)` lalu return non-zero.
- Selalu cek `api->abi != GNE_ABI` lebih dulu — cara inilah ekstensi menolak
  runtime dengan versi ABI berbeda.

## Referensi API

Semua fungsi ada di tabel `const gne_host_api *api` (disimpan di variabel
statis pada init).

| Kelompok | Fungsi |
|---|---|
| Konstruktor | `null`, `bool_new`, `int_new`, `float_new`, `string`, `array`, `object` |
| Aksesor | `type_of`, `get_bool`, `get_int`, `get_float`, `str_len`, `str_copy` |
| Kontainer | `len`, `arr_push`, `arr_get`, `arr_set`, `obj_set`, `obj_get`, `obj_has` |
| Fungsi | `define_fn`, `define_method`, `module`, `call` |
| Galat | `throw`, `failed` |
| State | `set_data`, `get_data` |
| Umur | `retain`, `release` |

Konvensi aksesor: **0 = sukses, -1 = gagal** (tipe atau handle salah) —
aksesor tidak pernah melempar galat; terserah ekstensi mau menolak atau
tidak. `len` berlaku untuk array dan string (string: panjang **byte**).

## Nilai dan aturan handle

Nilai Garurda disebut lewat `gne_handle` (`uint64_t`). **0 selalu tidak
valid**; `null` Garurda punya handle sendiri dari `api->null()`.
Handle tidak pernah dipakai ulang — `release()` sampai rc=0 menghapus
entri, sehingga handle basi menghasilkan kegagalan `get_*` yang jelas,
bukan kebocoran diam-diam.

Aturan kepemilikan (hafalkan ini):

- **Semua handle yang dibuat selama satu call** — argumen, hasil
  konstruktor, hasil `get_*`, hasil `call` — adalah **sementara milik
  host**. Host me-release semuanya begitu `cfunc` selesai. **Jangan
  `release()` sendiri**; cukup `retain()` bila handle harus bertahan
  setelah call (mis. disimpan di state).
- **`*ret` memindahkan kepemilikan ke host.** Jangan `release()` sesudah
  mengembalikannya; kalau C juga menyimpannya, `retain()` **dulu**
  sebelum mengembalikan.
- `define_method()` me-pin `self` miliknya (host sudah retain), jadi
  method tetap valid selama modul hidup.
- String lewat batas C↔Go selalu **disalin** (`str_copy` ke buffer milik
  Anda, atau konstruktor `string`). Tidak ada pointer host yang boleh
  disimpan — ini syarat kedisiplinan cgo.

## Error

```c
api->throw(ctx, "redis_error", 502, "koneksi ditolak");
return 1;
```

- `throw` lalu **return non-zero** → galat catchable di Garurda dengan
  `e.code`, `e.status`, `e.message`:

```garurda
try {
    redis.connect($dsn)
} catch e {
    print(e.code + "/" + str(e.status))   // redis_error/502
}
```

- Return non-zero **tanpa** `throw` → galat `gne_error` (status 500).
- Return 0 tetapi `*ret` tidak diisi → galat engine (program berhenti) —
  ini bug ekstensi, bukan kondisi runtime.
- `api->throw` dari dalam panggilan balik (callback `call`) juga diteruskan
  utuh: code/status asli dipertahankan.
- Panik di dalam host dipulihkan menjadi galat `gne_panic` (catchable) —
  runtime Go tidak akan mati.

## Method dan objek native

`define_method(ctx, obj, nama, min, max, fn, self)` mengikat fungsi ke
objek; pada waktu dipanggil, **`argv[0]` = `self`** (min/max tidak menghitung
self):

```c
static int next(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret) {
    /* argv[0] = objek counter */
    ...
}
/* pada init: */
counter = api->object(ctx);
api->define_method(ctx, counter, "next", 0, 0, next, counter);
api->obj_set(ctx, api->module(ctx), "counter", counter);
```

```garurda
$c = hello.counter
print($c.next())
```

## Panggilan balik ke Garurda

```c
gne_handle out;
if (api->call(ctx, argv[0], 1, &argv[1], &out) != 0)
    return 1;            // galat callback sudah tercatat di ctx
*ret = out;
```

`call` menerima nilai apa pun yang bisa dipanggil (closure, builtin). Bila
hasilnya promise (fungsi async), kirimkan apa adanya — jangan ditunggu;
runtime Garurda yang menanganinya.

## State per-modul

`set_data`/`get_data` menggantungkan satu `void *` pada **instance modul
ini** — aman bila satu `.so` dimuat oleh beberapa interpreter (state tidak
bocor antar instance):

```c
typedef struct { ... } kv_state;

int gne_module_init(const gne_host_api *a, gne_ctx *ctx, gne_handle *out) {
    kv_state *s = calloc(1, sizeof *s);
    a->set_data(ctx, s);
    ...
}
/* di dalam fungsi: kv_state *s = a->get_data(ctx); */
```

Varabel global statis dalam `.so` juga hidup satu proses — jangan
digunakan untuk state per-instance. Lihat `kv/kv.c` di repo contoh untuk
pola lengkap (key-value + simpan/muat berkas).

## Membangun

| Platform | Perintah |
|---|---|
| Linux | `gcc -shared -fPIC -Wall -Wextra -I <gne.h> -o x.so x.c` |
| macOS | `gcc -dynamiclib -Wall -Wextra -I <gne.h> -o x.dylib x.c` |
| Windows | `x86_64-w64-mingw32-gcc -shared -Wall -Wextra -I <gne.h> -o x.dll x.c` (MSYS2: `pacman -S mingw-w64-x86_64-gcc`) |

Header `gne.h` ada di **setiap zip rilis** dan di repo Garurda
(`include/gne.h`). Setelah build, taruh hasilnya di `./gne/` atau
`~/.garurda/gne`.

## Batasan yang perlu diketahui

- **Performa**: setiap panggilan C↔Go melintasi boundary cgo (ratusan
  nanosekon per lintasan). GNE untuk I/O, binding pustaka, dan pekerjaan
  berat — bukan untuk dipakai di jantung hot loop (hitung dulu dulu;
  ukur dengan benchmark).
- **Satu goroutine**: semua fungsi host hanya dipanggil dari goroutine
  interpreter yang sama; `ctx` hanya berlaku selama satu call — jangan
  disimpan atau dikirim ke thread lain.
- **Tidak ada unload**: modul hidup selama proses (`dlclose` tidak pernah
  dipanggil) — atur ulang state di `init` bila perlu.
- **Tanpa CGO tidak ada GNE**: biner cross-build tertentu memakai stub —
  `use` ekstensi akan menolak dengan pesan "butuh CGO" (lihat tabel
  platform di bawah).

## Keamanan

Memuat ekstensi native = **menjalankan kode C di proses Anda** — sama
riskannya dengan meng-install paket npm ber-addon native atau menjalankan
binary dari repository. GNE tidak mengubah perhitungan itu, tetapi
membatasi jalurnya:

- hanya tiga lokasi eksplisit (`./gne`, `$GNE_PATH`, `~/.garurda/gne`) —
  tidak ada pemindaian `PATH`/`LD_LIBRARY_PATH` yang bisa dibajak;
- `dlopen` dengan `RTLD_NOW | RTLD_LOCAL` (simbol tidak bocor ke global);
- ABI dicek saat init; handle tidak pernah dipakai ulang;
- penulis ekstensi tidak perlu — dan tidak bisa — menyentuh compiler
  atau runtime Garurda.

Pasang ekstensi hanya dari sumber yang Anda percaya.

## Platform

| Zip rilis | CGO | GNE | SQLite |
|---|---|---|---|
| `gar-linux-amd64` | ✔ | ✔ | ✔ |
| `gar-windows-amd64` | ✔ (mingw) | ✔ | ✔ |
| `gar-linux/arm64`, `gar-windows/arm64`, `gar-darwin-*` | ✘ | stub | ✘ |

Binari `CGO=0` tetap berjalan normal; hanya `use` ekstensi native (dan
SQLite) yang menolak dengan pesan jelas. Build native di platform
tersebut (mis. meng-`make build` di macOS sendiri) mendapatkan GNE penuh.

## Contoh

Repo [garurda-gne-examples](https://github.com/lnx645/garurda-gne-examples):

- **`hello/`** — fungsi, method objek, `throw` catchable, callback ke
  closure Garurda, state antar panggilan;
- **`kv/`** — key-value dengan state per-modul + persistensi berkas
  (pola yang sama untuk ekstensi Database/Redis);
- `build.sh` / `build.ps1` — build Linux/macOS/Windows.
