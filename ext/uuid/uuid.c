/* uuid.c — ekstensi GNE resmi: generator UUID (RFC 9562) murni C.
 *
 * Gaya pemakaian:
 *
 *   use "uuid"
 *   print(uuid.v4())      // acak penuh: 550e8400-e29b-41d4-a716-446655440000
 *   print(uuid.v7())      // berurut waktu: bagian depan = epoch milidetik
 *   print(uuid.is_valid("550e8400-e29b-41d4-a716-446655440000"))  // true
 *
 * Sumber angka acak: /dev/urandom (POSIX) atau BCryptGenRandom
 * (Windows) — BUKAN rand() biasa, supaya token/pratinjau id tidak bisa
 * ditebak. Tanpa dependensi ekstra, seluruhnya C standar + OS API.
 *
 * Galat (catchable try/catch):
 *   - argumen salah tipe        → "type_error" 500
 *   - sumber acak OS tak bisa   → "uuid_error" 500
 *
 * is_valid menerima heksadesimal besar atau kecil, tetapi WAJIB format
 * kanonik 8-4-4-4-12 dengan varian RFC (8/9/a/b) dan versi 0–8.
 * Format lain (braces, URN, tanpa strip) ditolak.
 */
#include "gne.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <windows.h>
#include <bcrypt.h>
#if defined(_MSC_VER)
#pragma comment(lib, "bcrypt.lib")
#endif
#else
#include <errno.h>
#include <fcntl.h>
#include <time.h>
#include <unistd.h>
#endif

static const gne_host_api *api;

/* ---- angka acak aman ---- */

/* 0 = sukses, -1 = gagal. */
static int acak(unsigned char *buf, size_t n)
{
#ifdef _WIN32
	return BCryptGenRandom(NULL, buf, (ULONG)n,
			       BCRYPT_USE_SYSTEM_PREFERRED_RNG) == 0
		       ? 0
		       : -1;
#else
	int fd = open("/dev/urandom", O_RDONLY);
	size_t off = 0;
	if (fd < 0)
		return -1;
	while (off < n) {
		ssize_t k = read(fd, buf + off, n - off);
		if (k < 0) {
			if (errno == EINTR)
				continue;
			close(fd);
			return -1;
		}
		if (k == 0) { /* EOF dini — tidak normal untuk urandom */
			close(fd);
			return -1;
		}
		off += (size_t)k;
	}
	close(fd);
	return 0;
#endif
}

/* Epoch milidetik sekarang (untuk UUID v7). */
static unsigned long long epoch_ms(void)
{
#ifdef _WIN32
	FILETIME ft;
	unsigned long long t;
	GetSystemTimeAsFileTime(&ft);
	t = ((unsigned long long)ft.dwHighDateTime << 32) |
	    (unsigned long long)ft.dwLowDateTime;
	/* FILETIME:100 ns sejak1601-01-01 → Unix epoch (116444736000000000). */
	return (t - 116444736000000000ULL) / 10000ULL;
#else
	struct timespec ts;
	if (clock_gettime(CLOCK_REALTIME, &ts) != 0)
		return (unsigned long long)time(NULL) * 1000ULL;
	return (unsigned long long)ts.tv_sec * 1000ULL +
	       (unsigned long long)(ts.tv_nsec / 1000000);
#endif
}

/* ---- format kanonik 8-4-4-4-12 huruf kecil ---- */
static void format_uuid(const unsigned char b[16], char out[37])
{
	static const char hex[] = "0123456789abcdef";
	int i, o = 0;
	for (i = 0; i < 16; i++) {
		if (i == 4 || i == 6 || i == 8 || i == 10)
			out[o++] = '-';
		out[o++] = hex[b[i] >> 4];
		out[o++] = hex[b[i] & 0x0f];
	}
	out[o] = '\0';
}

static int hexval(char c)
{
	if (c >= '0' && c <= '9')
		return c - '0';
	if (c >= 'a' && c <= 'f')
		return c - 'a' + 10;
	if (c >= 'A' && c <= 'F')
		return c - 'A' + 10;
	return -1;
}

/* ---- uuid.v4() ---- */
static int uuid_v4(gne_ctx *ctx, int argc, const gne_handle *argv,
		   gne_handle *ret)
{
	unsigned char b[16];
	char s[37];
	(void)argc;
	(void)argv;
	if (acak(b, sizeof b) != 0) {
		api->throw(ctx, "uuid_error", 500,
			   "gagal membaca angka acak aman dari sistem");
		return -1;
	}
	b[6] = (unsigned char)((b[6] & 0x0f) | 0x40); /* versi 4 */
	b[8] = (unsigned char)((b[8] & 0x3f) | 0x80); /* varian RFC */
	format_uuid(b, s);
	*ret = api->string(ctx, s, 36);
	return 0;
}

/* ---- uuid.v7() — bagian depan = waktu (urut untuk indeks DB) ---- */
static int uuid_v7(gne_ctx *ctx, int argc, const gne_handle *argv,
		   gne_handle *ret)
{
	unsigned char b[16];
	unsigned long long ms = epoch_ms();
	char s[37];
	int i;
	(void)argc;
	(void)argv;
	if (acak(b, sizeof b) != 0) {
		api->throw(ctx, "uuid_error", 500,
			   "gagal membaca angka acak aman dari sistem");
		return -1;
	}
	for (i = 0; i < 6; i++) { /*48-bit waktu besar-endian */
		b[i] = (unsigned char)((ms >> (40 - 8 * i)) & 0xff);
	}
	b[6] = (unsigned char)((b[6] & 0x0f) | 0x70); /* versi 7 */
	b[8] = (unsigned char)((b[8] & 0x3f) | 0x80); /* varian RFC */
	format_uuid(b, s);
	*ret = api->string(ctx, s, 36);
	return 0;
}

/* ---- uuid.is_valid(s) ---- */
static int uuid_is_valid(gne_ctx *ctx, int argc, const gne_handle *argv,
			 gne_handle *ret)
{
	char s[37];
	size_t n;
	int32_t t;
	int i;
	(void)argc;
	if (api->type_of(ctx, argv[0], &t) != 0 || t != GNE_STRING ||
	    api->str_len(ctx, argv[0], &n) != 0) {
		api->throw(ctx, "type_error", 500,
			   "argumen harus string");
		return -1;
	}
	if (n != 36) { /* cepat: panjang salah langsung false */
		*ret = api->bool_new(ctx, 0);
		return 0;
	}
	if (api->str_copy(ctx, argv[0], s, sizeof s) < 0) {
		api->throw(ctx, "type_error", 500, "argumen string tidak terbaca");
		return -1;
	}
	for (i = 0; i < 36; i++) {
		if (i == 8 || i == 13 || i == 18 || i == 23) {
			if (s[i] != '-') {
				*ret = api->bool_new(ctx, 0);
				return 0;
			}
		} else if (hexval(s[i]) < 0) {
			*ret = api->bool_new(ctx, 0);
			return 0;
		}
	}
	/* Versi0–8 pada posisi14; varian 8/9/a/b pada posisi19. */
	{
		int ver = hexval(s[14]), var = hexval(s[19]);
		if (ver < 0 || ver > 8 || var < 8 || var > 11) {
			*ret = api->bool_new(ctx, 0);
			return 0;
		}
	}
	*ret = api->bool_new(ctx, 1);
	return 0;
}

int gne_module_init(const gne_host_api *a, gne_ctx *ctx, gne_handle *out)
{
	api = a;
	if (api->abi != GNE_ABI) {
		api->throw(ctx, "gne_abi", 500, "ABI berbeda");
		return 1;
	}
	api->define_fn(ctx, "v4", 0, 0, uuid_v4);
	api->define_fn(ctx, "v7", 0, 0, uuid_v7);
	api->define_fn(ctx, "is_valid", 1, 1, uuid_is_valid);
	*out = api->module(ctx);
	return 0;
}
