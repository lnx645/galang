/* gne.h — ABI Garurda Native Extension (GNE) versi 1.
 *
 * Kontrak singkat untuk penulis ekstensi:
 *
 *   1. Nilai Garurda disebut lewat handle (uint64_t). 0 SELALU tidak valid;
 *      null Garurda punya handle sendiri (api->null()).
 *   2. Semua fungsi host hanya boleh dipanggil dari satu goroutine yang
 *      memanggil ekstensi; ctx hanya berlaku selama satu panggilan —
 *      JANGAN disimpan atau dikirim ke thread lain.
 *   3. Jangan longjmp/abort/exit dari dalam ekstensi (mematikan runtime Go).
 *      Laporkan error dengan api->throw lalu return kode non-zero.
 *   4. Simpan api (valid selama proses) dan state per-instance lewat
 *      set_data/get_data — jangan gunakan variabel global sembarangan bila
 *      satu .so dipakai beberapa interpreter.
 *   5. String lewat batas Go↔C disalin (str_copy / konstruktor) — tidak ada
 *      pointer milik host yang boleh disimpan.
 *
 * Kepemilikan handle (reference count):
 *   - SEMUA handle yang dibuat selama satu call — argumen, konstruktor,
 *     hasil get_*, hasil call — adalah SEMENTARA milik host: host
 *     me-release semuanya begitu cfunc selesai. JANGAN release() sendiri
 *     (bisa memutus pinning yang belum selesai); cukup retain() bila
 *     sebuah handle harus bertahan setelah call (mis. disimpan di state).
 *   - Nilai yang dikembalikan lewat *ret: kepemilikan BERPINDAH ke host.
 *     Jangan release() sesudah mengembalikannya; kalau C juga menyimpannya,
 *     retain() dulu SEBELUM mengembalikan.
 *   - release() hanya untuk membuang handle yang Anda retain() sendiri;
 *     sampai rc=0 entri dihapus, dan handle basi yang dipakai lagi
 *     menghasilkan galat jelas (bukan membaca nilai orang lain).
 *   - define_method() mem-pin self miliknya (host sudah retain), jadi
 *     method thunks tetap valid selama modul hidup.
 *
 * Cara membangun:
 *   gcc -shared -fPIC -I<path ke gne.h> -o redis.so redis.c
 *   (macOS: -dynamiclib; Windows mingw: -shared)
 *   Taruh hasilnya di ./gne/ atau ~/.garurda/gne, lalu: use "redis".
 */
#ifndef GNE_H
#define GNE_H

#include <stddef.h>
#include <stdint.h>

#if defined(_WIN32)
#  define GNE_EXPORT __declspec(dllexport)
#else
#  define GNE_EXPORT __attribute__((visibility("default")))
#endif

#ifdef __cplusplus
extern "C" {
#endif

/* Versi ABI. Host menolak ekstensi dengan nomor berbeda. */
#define GNE_ABI 1

/* Konteks panggilan — FIELD INTERNAL HOST, jangan diakses langsung dari
 * ekstensi (bisa berubah tanpa pemberitahuan pada versi ABI berikutnya).
 * Berlaku hanya selama satu call; jangan disimpan. */
struct gne_ctx {
	uint64_t mod;      /* id modul (internal host) */
	uint64_t self;     /* self method, 0 bila bukan method */
	int32_t  line, col; /* posisi panggilan berjalan */
	int32_t  failed;   /* != 0 bila api->throw sudah dipanggil */
	int32_t  status;
	char    *code, *msg; /* galat tertunda (milik host) */
	void    *data;       /* instance data modul (api->set_data) */
};

typedef struct gne_ctx gne_ctx;
typedef uint64_t gne_handle;

/* Tipe nilai (enum ringkas). */
enum {
	GNE_NULL = 0,
	GNE_BOOL,
	GNE_INT,
	GNE_FLOAT,
	GNE_STRING,
	GNE_ARRAY,
	GNE_OBJECT,
	GNE_FUNCTION,
	GNE_PROMISE,
	GNE_ERROR
};

/* Fungsi native yang didaftarkan ke Garurda.
 * Untuk method (define_method), argv[0] adalah self; min/max tidak
 * menghitung self. Return 0 dengan *ret terisi = sukses. */
typedef int (*gne_cfunc)(gne_ctx *ctx, int argc, const gne_handle *argv,
			 gne_handle *ret);

/* Tabel API host — diisi oleh runtime Garurda, dipanggil selama hidup
 * proses. Ekstensi menyimpan pointer ini di variabel statis pada
 * gne_module_init(). Semua fungsi butuh ctx dari call berjalan. */
typedef struct gne_host_api {
	int32_t abi; /* selalu GNE_ABI milik host */

	/* --- konstruktor (hasil: milik C, lihat aturan rc) --- */
	gne_handle (*null)(gne_ctx *ctx);
	gne_handle (*bool_new)(gne_ctx *ctx, int v);
	gne_handle (*int_new)(gne_ctx *ctx, int64_t v);
	gne_handle (*float_new)(gne_ctx *ctx, double v);
	gne_handle (*string)(gne_ctx *ctx, const char *s, size_t len);
	gne_handle (*array)(gne_ctx *ctx);
	gne_handle (*object)(gne_ctx *ctx);

	/* --- aksesor: 0 = sukses, -1 = gagal (tipe/handle salah);
	 *     tidak melempar error, tidak mengubah status throw. --- */
	int (*type_of)(gne_ctx *ctx, gne_handle h, int32_t *out);
	int (*get_bool)(gne_ctx *ctx, gne_handle h, int *out);
	int (*get_int)(gne_ctx *ctx, gne_handle h, int64_t *out);
	int (*get_float)(gne_ctx *ctx, gne_handle h, double *out);
	/* str_len: panjang byte tanpa NUL.
	 * str_copy: salinan + NUL ke buf milik penerima; cap wajib
	 * >= str_len+1; mengembalikan jumlah byte (tanpa NUL) atau -1. */
	int (*str_len)(gne_ctx *ctx, gne_handle h, size_t *out);
	int (*str_copy)(gne_ctx *ctx, gne_handle h, char *buf, size_t cap);

	/* --- kontainer (len berlaku untuk array & string) --- */
	int (*len)(gne_ctx *ctx, gne_handle h, size_t *out);
	int (*arr_push)(gne_ctx *ctx, gne_handle arr, gne_handle val);
	int (*arr_get)(gne_ctx *ctx, gne_handle arr, size_t idx, gne_handle *out);
	int (*arr_set)(gne_ctx *ctx, gne_handle arr, size_t idx, gne_handle val);
	int (*obj_set)(gne_ctx *ctx, gne_handle obj, const char *key,
		       gne_handle val);
	int (*obj_get)(gne_ctx *ctx, gne_handle obj, const char *key,
		       gne_handle *out); /* -1 bila kunci tidak ada */
	int (*obj_has)(gne_ctx *ctx, gne_handle obj, const char *key, int *out);

	/* --- fungsi & panggilan balik ---
	 * define_fn/define_method mendaftarkan ke namespace modul ctx
	 * (boleh saat init maupun saat call berjalan). call memanggil nilai
	 * apa pun yang bisa dipanggil; hasil async berupa promise — kirimkan
	 * apa adanya. module() mengembalikan namespace modul ctx (biasanya
	 * ditulis ke *out pada gne_module_init). */
	int (*define_fn)(gne_ctx *ctx, const char *name, int32_t min_args,
			 int32_t max_args, gne_cfunc fn);
	int (*define_method)(gne_ctx *ctx, gne_handle obj, const char *name,
			     int32_t min_args, int32_t max_args, gne_cfunc fn,
			     gne_handle self);
	int (*call)(gne_ctx *ctx, gne_handle fn, int argc,
		    const gne_handle *argv, gne_handle *ret);
	gne_handle (*module)(gne_ctx *ctx);

	/* --- error: throw lalu return non-zero dari cfunc.
	 * code bebas (mis. "redis_error"), status dipakai HTTP (mis. 500);
	 * error menjadi catchable try/catch e { e.code, e.status }. --- */
	void (*throw)(gne_ctx *ctx, const char *code, int32_t status,
		      const char *msg);
	int (*failed)(gne_ctx *ctx);

	/* --- data instance per modul (aman untuk multi-interpreter) --- */
	void (*set_data)(gne_ctx *ctx, void *data);
	void *(*get_data)(gne_ctx *ctx);

	/* --- umur handle --- */
	void (*retain)(gne_ctx *ctx, gne_handle h);
	void (*release)(gne_ctx *ctx, gne_handle h);
} gne_host_api;

/* Titik masuk WAJIB yang diekspor ekstensi.
 * Bangun namespace (biasanya api->object + api->define_fn/define_method)
 * lalu tulis handle-nya ke *out. Return 0 = sukses; untuk gagal:
 * api->throw(...) lalu return non-zero. */
GNE_EXPORT int gne_module_init(const gne_host_api *api, gne_ctx *ctx,
			       gne_handle *out);

#ifdef __cplusplus
}
#endif

#endif /* GNE_H */
