/* redis.c — ekstensi GNE resmi: klien Redis (RESP2) murni C.
 *
 * Gaya pemakaian:
 *
 *   use "redis"
 *   $r = redis.connect("127.0.0.1", 6379)        // timeout 5000 ms
 *   $r = redis.connect("127.0.0.1", 6379, 3000)  // timeout custom
 *   $r.set("k", "v")
 *   print($r.get("k"))
 *   $r.close()
 *
 * State: tabel koneksi maksimal 64 slot digantungkan lewat
 * api->set_data (aman bila satu .so dipakai beberapa interpreter);
 * tiap objek koneksi menyimpan field "id" → slot. Koneksi yang
 * ditutup atau rusak dilempar sebagai galat catchable "redis_error".
 *
 * Balasan error server (-ERR ...) menjadi throw "redis_error":
 *   status 500 — server menolak / protokol tak terduga / argumen salah
 *   status 502 — gagal terhubung / koneksi terputus
 *   status 504 — waktu tunggu habis (timeout)
 *
 * Lintas platform: POSIX (fcntl/select) dan Winsock (#ifdef _WIN32).
 * Sinyal SIGPIPE ditahan lewat MSG_NOSIGNAL (Linux) atau
 * SO_NOSIGPIPE (macOS) — ekstensi tidak pernah mengubah handler
 * sinyal proses.
 */
#include "gne.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <inttypes.h>

#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
#if defined(_MSC_VER)
#pragma comment(lib, "ws2_32.lib")
#endif
typedef SOCKET rfd_t;
#define RFD_INVALID INVALID_SOCKET
#define rfd_close closesocket
#define SOCK_ERRNO WSAGetLastError()
#define SOCK_EINTR WSAEINTR
#define SOCK_EAGAIN WSAEWOULDBLOCK
#define SOCK_TIMEOUT(e) ((e) == WSAETIMEDOUT || (e) == WSAEWOULDBLOCK)
#define SOCK_INPROGRESS(e) ((e) == WSAEWOULDBLOCK || (e) == WSAEINPROGRESS)
#else
#include <sys/types.h>
#include <sys/socket.h>
#include <sys/select.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <netdb.h>
#include <unistd.h>
#include <fcntl.h>
typedef int rfd_t;
#define RFD_INVALID (-1)
#define rfd_close close
#define SOCK_ERRNO errno
#define SOCK_EINTR EINTR
#define SOCK_EAGAIN EAGAIN
#define SOCK_TIMEOUT(e) ((e) == EAGAIN || (e) == EWOULDBLOCK || (e) == ETIMEDOUT)
#define SOCK_INPROGRESS(e) ((e) == EINPROGRESS)
#ifndef MSG_NOSIGNAL
#define MSG_NOSIGNAL 0
#endif
#endif

static const gne_host_api *api;

/* ---- batas (dokumentasikan di docs) ---- */
#define REDIS_MAX_CONN 64
#define REDIS_TIMEOUT_DEFAULT_MS 5000
#define REDIS_MAX_LINE (1u << 20)      /* baris protokol ≤ 1 MiB */
#define REDIS_MAX_BULK (64u << 20)     /* balasan bulk ≤ 64 MiB */
#define REDIS_MAX_DEPTH 32             /* kedalaman array RESP */
#define REDIS_MAX_ELEMS 1000000        /* jumlah elemen array RESP */
#define REDIS_MAX_CMD (64u << 20)      /* perintah keluar ≤ 64 MiB */

typedef struct {
	int fd; /* -1 = slot kosong */
	int timeout_ms;
	int64_t gen; /* nomor generasi — menolak objek basi dari siklus close/connect */
	char *rbuf;
	size_t rcap, rlen, rpos;
} conn;

typedef struct {
	conn conns[REDIS_MAX_CONN];
} redis_state;

/* Hasil membaca buffer. */
enum {
	BUF_OK = 0,
	BUF_EOF = -1,      /* server menutup koneksi */
	BUF_TIMEOUT = -2,  /* SO_RCVTIMEO tercapai */
	BUF_ERR = -3,      /* galat socket lain */
	BUF_TOOLARGE = -4  /* melewati batas ukuran */
};

/* ---- teks galat socket (deterministik, tanpa strerror) ---- */
static const char *sock_text(int e, char *buf, size_t cap)
{
	const char *s = NULL;
#ifdef _WIN32
	switch (e) {
	case WSAETIMEDOUT: s = "waktu tunggu habis"; break;
	case WSAECONNREFUSED: s = "koneksi ditolak"; break;
	case WSAECONNRESET: s = "koneksi direset"; break;
	case WSAEHOSTUNREACH: s = "tujuan tak terjangkau"; break;
	case WSAENETUNREACH: s = "jaringan tak terjangkau"; break;
	case WSAENOTCONN: s = "koneksi belum terbuka"; break;
	default: break;
	}
#else
	switch (e) {
	case ETIMEDOUT: s = "waktu tunggu habis"; break;
	case ECONNREFUSED: s = "koneksi ditolak"; break;
	case ECONNRESET: s = "koneksi direset"; break;
	case EHOSTUNREACH: s = "tujuan tak terjangkau"; break;
	case ENETUNREACH: s = "jaringan tak terjangkau"; break;
	case ENOTCONN: s = "koneksi belum terbuka"; break;
	case EPIPE: s = "pipa tertutup"; break;
	default: break;
	}
#endif
	if (s)
		snprintf(buf, cap, "%s (kode %d)", s, e);
	else
		snprintf(buf, cap, "galat socket %d", e);
	return buf;
}

static int is_timeout_err(int e)
{
	return SOCK_TIMEOUT(e);
}

static int is_retry_err(int e)
{
	return e == SOCK_EINTR;
}

/* ---- buffer baca ---- */

static int buf_grow(conn *c, size_t need)
{
	size_t cap;
	char *nb;
	if (need <= c->rcap)
		return BUF_OK;
	if (need > REDIS_MAX_BULK + REDIS_MAX_LINE)
		return BUF_TOOLARGE;
	cap = c->rcap ? c->rcap : 4096;
	while (cap < need)
		cap *= 2;
	if (cap > REDIS_MAX_BULK + REDIS_MAX_LINE)
		cap = REDIS_MAX_BULK + REDIS_MAX_LINE;
	nb = realloc(c->rbuf, cap);
	if (!nb)
		return BUF_ERR;
	c->rbuf = nb;
	c->rcap = cap;
	return BUF_OK;
}

/* Pindahkan data belum terpakai ke awal buffer. */
static void buf_compact(conn *c)
{
	if (c->rpos == 0)
		return;
	if (c->rlen > c->rpos)
		memmove(c->rbuf, c->rbuf + c->rpos, c->rlen - c->rpos);
	c->rlen -= c->rpos;
	c->rpos = 0;
}

/* Terima minimal satu byte lagi (setelah compact bila perlu). */
static int buf_refill(conn *c)
{
	int rc;
	if (c->rpos > 0) {
		if (c->rlen == c->rpos) { /* hanya sisa terpakai */
			c->rlen = 0;
			c->rpos = 0;
		} else {
			buf_compact(c);
		}
	}
	if (c->rlen == c->rcap) {
		rc = buf_grow(c, c->rcap ? c->rcap * 2 : 4096);
		if (rc != BUF_OK)
			return rc;
	}
	for (;;) {
		int k;
#ifdef _WIN32
		k = (int)recv(c->fd, c->rbuf + c->rlen, (int)(c->rcap - c->rlen), 0);
#else
		{
			ssize_t n = recv(c->fd, c->rbuf + c->rlen, c->rcap - c->rlen, 0);
			k = (n < 0) ? -1 : (n == 0 ? 0 : 1);
			if (n > 0)
				c->rlen += (size_t)n;
		}
#endif
#ifdef _WIN32
		if (k > 0) {
			c->rlen += (size_t)k;
			return BUF_OK;
		}
		if (k == 0)
			return BUF_EOF;
		{
			int e = SOCK_ERRNO;
			if (is_retry_err(e))
				continue;
			if (is_timeout_err(e))
				return BUF_TIMEOUT;
			return BUF_ERR;
		}
#else
		if (k == 1)
			return BUF_OK;
		if (k == 0)
			return BUF_EOF;
		{
			int e = errno;
			if (is_retry_err(e))
				continue;
			if (is_timeout_err(e))
				return BUF_TIMEOUT;
			return BUF_ERR;
		}
#endif
	}
}

/* Pastikan ada ≥ n byte di buffer (dari posisi baca). */
static int buf_ensure(conn *c, size_t n)
{
	while (c->rlen - c->rpos < n) {
		int rc = buf_refill(c);
		if (rc != BUF_OK)
			return rc;
	}
	return BUF_OK;
}

/* Baca satu baris protokol (tanpa CRLF). *out menunjuk ke dalam buffer —
 * SAH hanya sampai operasi baca berikutnya. */
static int buf_line(conn *c, const char **out, size_t *outlen)
{
	for (;;) {
		size_t i;
		for (i = c->rpos; i < c->rlen; i++) {
			if (c->rbuf[i] == '\n') {
				size_t len = i - c->rpos;
				if (len > 0 && c->rbuf[c->rpos + len - 1] == '\r')
					len--;
				*out = c->rbuf + c->rpos;
				*outlen = len;
				c->rpos = i + 1;
				return BUF_OK;
			}
		}
		if (c->rlen - c->rpos > REDIS_MAX_LINE)
			return BUF_TOOLARGE;
		{
			int rc = buf_refill(c);
			if (rc != BUF_OK)
				return rc;
		}
	}
}

/* ---- galat ---- */

static int fail_io(gne_ctx *ctx, conn *c, int rc, const char *what)
{
	char buf[256];
	if (rc == BUF_TIMEOUT) {
		snprintf(buf, sizeof buf, "%s: waktu tunggu %d ms habis", what, c->timeout_ms);
		api->throw(ctx, "redis_error", 504, buf);
	} else if (rc == BUF_EOF) {
		snprintf(buf, sizeof buf, "%s: server menutup koneksi", what);
		api->throw(ctx, "redis_error", 502, buf);
	} else if (rc == BUF_TOOLARGE) {
		snprintf(buf, sizeof buf, "%s: balasan melewati batas ukuran", what);
		api->throw(ctx, "redis_error", 500, buf);
	} else {
		int e = SOCK_ERRNO;
		char txt[96];
		snprintf(buf, sizeof buf, "%s: %s", what, sock_text(e, txt, sizeof txt));
		api->throw(ctx, "redis_error", 502, buf);
	}
	return -1;
}

/* ---- kumpulan argumen ---- */

typedef struct {
	char **v;
	size_t *len;
	int n;
} argvec;

static void args_free(argvec *av)
{
	int i;
	for (i = 0; i < av->n; i++)
		free(av->v[i]);
	free(av->v);
	free(av->len);
	av->v = NULL;
	av->len = NULL;
	av->n = 0;
}

/* Konversi satu handle menjadi string protokol; galat tipe → throw. */
static char *arg_to_str(gne_ctx *ctx, gne_handle h, size_t *len)
{
	int32_t t;
	if (api->type_of(ctx, h, &t) != 0) {
		api->throw(ctx, "type_error", 500, "argumen tidak valid");
		return NULL;
	}
	switch (t) {
	case GNE_STRING: {
		size_t n;
		char *out;
		if (api->str_len(ctx, h, &n) != 0) {
			api->throw(ctx, "type_error", 500, "argumen string tidak terbaca");
			return NULL;
		}
		out = malloc(n + 1);
		if (!out) {
			api->throw(ctx, "redis_oom", 500, "gagal mengalokasikan memori");
			return NULL;
		}
		if (api->str_copy(ctx, h, out, n + 1) < 0) {
			free(out);
			api->throw(ctx, "type_error", 500, "argumen string tidak terbaca");
			return NULL;
		}
		*len = n;
		return out;
	}
	case GNE_INT: {
		int64_t v;
		char tmp[32], *out;
		api->get_int(ctx, h, &v);
		snprintf(tmp, sizeof tmp, "%" PRId64, v);
		out = strdup(tmp);
		if (!out) {
			api->throw(ctx, "redis_oom", 500, "gagal mengalokasikan memori");
			return NULL;
		}
		*len = strlen(tmp);
		return out;
	}
	case GNE_FLOAT: {
		double d;
		char tmp[40], *out;
		api->get_float(ctx, h, &d);
		snprintf(tmp, sizeof tmp, "%.15g", d);
		if (strtod(tmp, NULL) != d)
			snprintf(tmp, sizeof tmp, "%.17g", d);
		out = strdup(tmp);
		if (!out) {
			api->throw(ctx, "redis_oom", 500, "gagal mengalokasikan memori");
			return NULL;
		}
		*len = strlen(tmp);
		return out;
	}
	default:
		api->throw(ctx, "type_error", 500,
			   "argumen Redis harus string atau angka");
		return NULL;
	}
}

/* Kumpulkan argv[from..argc) ke wadah malloc; 0 = sukses, -1 = throw. */
static int args_collect(gne_ctx *ctx, int argc, const gne_handle *argv, int from, argvec *av)
{
	int i, n = argc - from;
	av->v = NULL;
	av->len = NULL;
	av->n = 0;
	if (n <= 0)
		return 0;
	av->v = calloc((size_t)n, sizeof(char *));
	av->len = calloc((size_t)n, sizeof(size_t));
	if (!av->v || !av->len) {
		args_free(av);
		api->throw(ctx, "redis_oom", 500, "gagal mengalokasikan memori");
		return -1;
	}
	for (i = 0; i < n; i++) {
		av->v[i] = arg_to_str(ctx, argv[from + i], &av->len[i]);
		if (!av->v[i]) {
			args_free(av);
			return -1;
		}
		av->n = i + 1;
	}
	return 0;
}

/* ---- kirim + baca balasan ---- */

typedef struct {
	char *b;
	size_t len, cap;
} sbuf;

static int sb_reserve(sbuf *s, size_t n)
{
	if (s->len + n <= s->cap)
		return 0;
	{
		size_t cap = s->cap ? s->cap : 512;
		char *nb;
		while (cap < s->len + n)
			cap *= 2;
		nb = realloc(s->b, cap);
		if (!nb)
			return -1;
		s->b = nb;
		s->cap = cap;
	}
	return 0;
}

static int sb_add(sbuf *s, const char *p, size_t n)
{
	if (sb_reserve(s, n) != 0)
		return -1;
	memcpy(s->b + s->len, p, n);
	s->len += n;
	return 0;
}

static int send_all(conn *c, const char *p, size_t n)
{
	while (n > 0) {
#ifdef _WIN32
		int chunk = n > 0x7fffffffu ? 0x7fffffff : (int)n;
		int k = send(c->fd, p, chunk, 0);
		if (k > 0) {
			p += k;
			n -= (size_t)k;
			continue;
		}
		if (k == SOCKET_ERROR) {
			int e = SOCK_ERRNO;
			if (is_retry_err(e))
				continue;
			return -1;
		}
#else
		ssize_t k = send(c->fd, p, n, MSG_NOSIGNAL);
		if (k > 0) {
			p += (size_t)k;
			n -= (size_t)k;
			continue;
		}
		if (k < 0) {
			int e = errno;
			if (is_retry_err(e))
				continue;
			return -1;
		}
#endif
		return -1; /* EOF pada send tidak terjadi di stream, tetap gagal */
	}
	return 0;
}

/* Susun RESP: *N\r\n $len\r\n arg\r\n ... lalu kirim. */
static int send_cmd(conn *c, const char *name, const argvec *av)
{
	sbuf s = { NULL, 0, 0 };
	char hdr[64];
	int i, nargs = av->n + 1;
	size_t total;

	total = 32 + strlen(name);
	for (i = 0; i < av->n; i++)
		total += 32 + av->len[i];
	if (total > REDIS_MAX_CMD)
		return -1;
	if (sb_reserve(&s, total) != 0)
		return -1;

	snprintf(hdr, sizeof hdr, "*%d\r\n", nargs);
	if (sb_add(&s, hdr, strlen(hdr)) != 0)
		goto fail;
	snprintf(hdr, sizeof hdr, "$%zu\r\n", strlen(name));
	if (sb_add(&s, hdr, strlen(hdr)) != 0 ||
	    sb_add(&s, name, strlen(name)) != 0 ||
	    sb_add(&s, "\r\n", 2) != 0)
		goto fail;
	for (i = 0; i < av->n; i++) {
		snprintf(hdr, sizeof hdr, "$%zu\r\n", av->len[i]);
		if (sb_add(&s, hdr, strlen(hdr)) != 0 ||
		    sb_add(&s, av->v[i], av->len[i]) != 0 ||
		    sb_add(&s, "\r\n", 2) != 0)
			goto fail;
	}
	{
		int rc = send_all(c, s.b, s.len);
		free(s.b);
		return rc;
	}
fail:
	free(s.b);
	return -1;
}

/* Baca satu balasan RESP menjadi handle Garurda (rekursif).
 * 0 = sukses, -1 = sudah throw (atau galat I/O dengan throw). */
static int read_reply(gne_ctx *ctx, conn *c, gne_handle *out, int depth)
{
	const char *line;
	size_t linelen;
	int rc;

	if (depth > REDIS_MAX_DEPTH) {
		api->throw(ctx, "redis_error", 500, "balasan Redis terlalu bertingkat");
		return -1;
	}
	rc = buf_line(c, &line, &linelen);
	if (rc != BUF_OK)
		return fail_io(ctx, c, rc, "membaca balasan");
	if (linelen == 0) {
		api->throw(ctx, "redis_error", 500, "balasan Redis kosong");
		return -1;
	}

	switch (line[0]) {
	case '+':
		*out = api->string(ctx, line + 1, linelen - 1);
		return 0;
	case '-': {
		char msg[512];
		size_t n = linelen - 1;
		if (n >= sizeof msg)
			n = sizeof msg - 1;
		memcpy(msg, line + 1, n);
		msg[n] = 0;
		api->throw(ctx, "redis_error", 500, msg);
		return -1;
	}
	case ':': {
		char tmp[32];
		if (linelen - 1 >= sizeof tmp) {
			api->throw(ctx, "redis_error", 500, "balasan integer tidak terduga");
			return -1;
		}
		memcpy(tmp, line + 1, linelen - 1);
		tmp[linelen - 1] = 0;
		*out = api->int_new(ctx, strtoll(tmp, NULL, 10));
		return 0;
	}
	case '$': {
		long long n;
		char tmp[32];
		if (linelen - 1 >= sizeof tmp) {
			api->throw(ctx, "redis_error", 500, "panjang bulk tidak terduga");
			return -1;
		}
		memcpy(tmp, line + 1, linelen - 1);
		tmp[linelen - 1] = 0;
		n = strtoll(tmp, NULL, 10);
		if (n < 0) { /* $-1 = null */
			*out = api->null(ctx);
			return 0;
		}
		if ((unsigned long long)n > REDIS_MAX_BULK) {
			api->throw(ctx, "redis_error", 500, "balasan bulk melewati batas 64 MiB");
			return -1;
		}
		rc = buf_ensure(c, (size_t)n + 2);
		if (rc != BUF_OK)
			return fail_io(ctx, c, rc, "membaca balasan bulk");
		*out = api->string(ctx, c->rbuf + c->rpos, (size_t)n);
		c->rpos += (size_t)n + 2;
		return 0;
	}
	case '*': {
		long long n;
		char tmp[32];
		long long i;
		gne_handle arr;
		if (linelen - 1 >= sizeof tmp) {
			api->throw(ctx, "redis_error", 500, "jumlah elemen tidak terduga");
			return -1;
		}
		memcpy(tmp, line + 1, linelen - 1);
		tmp[linelen - 1] = 0;
		n = strtoll(tmp, NULL, 10);
		if (n < 0) { /* *-1 = null */
			*out = api->null(ctx);
			return 0;
		}
		if (n > REDIS_MAX_ELEMS) {
			api->throw(ctx, "redis_error", 500, "terlalu banyak elemen balasan");
			return -1;
		}
		arr = api->array(ctx);
		for (i = 0; i < n; i++) {
			gne_handle elem;
			if (read_reply(ctx, c, &elem, depth + 1) != 0)
				return -1;
			if (api->arr_push(ctx, arr, elem) != 0) {
				api->throw(ctx, "redis_error", 500, "gagal menyusun array balasan");
				return -1;
			}
		}
		*out = arr;
		return 0;
	}
	default:
		api->throw(ctx, "redis_error", 500, "balasan Redis tidak dikenal");
		return -1;
	}
}

/* ---- koneksi ---- */

static conn *self_conn(gne_ctx *ctx, gne_handle self)
{
	redis_state *st = api->get_data(ctx);
	gne_handle h;
	int64_t id, gen;
	if (!st) {
		api->throw(ctx, "redis_error", 500, "state modul Redis hilang");
		return NULL;
	}
	if (api->obj_get(ctx, self, "id", &h) != 0 ||
	    api->get_int(ctx, h, &id) != 0 ||
	    id < 0 || id >= REDIS_MAX_CONN ||
	    api->obj_get(ctx, self, "gen", &h) != 0 ||
	    api->get_int(ctx, h, &gen) != 0 ||
	    st->conns[id].fd < 0 || st->conns[id].gen != gen) {
		api->throw(ctx, "redis_error", 500, "koneksi sudah ditutup atau tidak valid");
		return NULL;
	}
	return &st->conns[id];
}

static void set_timeouts(rfd_t fd, int ms)
{
	if (ms <= 0)
		return; /* 0 = tanpa batas (blocking murni) */
#ifdef _WIN32
	{
		DWORD t = (DWORD)ms;
		setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, (const char *)&t, (int)sizeof t);
		setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, (const char *)&t, (int)sizeof t);
	}
#else
	{
		struct timeval tv;
		tv.tv_sec = ms / 1000;
		tv.tv_usec = (suseconds_t)(ms % 1000) * 1000;
		setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof tv);
		setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof tv);
	}
#endif
}

/* Konek nonblocking + select(timeout). 0 = sukses; nilai lain = errno/WSA. */
static int conn_dial(rfd_t fd, const struct sockaddr *sa, socklen_t slen, int timeout_ms)
{
	int rc;
#ifdef _WIN32
	u_long nb = 1;
	if (ioctlsocket(fd, FIONBIO, &nb) != 0)
		return SOCK_ERRNO;
	rc = connect(fd, sa, slen);
	if (rc == 0) {
		nb = 0;
		ioctlsocket(fd, FIONBIO, &nb);
		return 0;
	}
	{
		int e = SOCK_ERRNO;
		if (!SOCK_INPROGRESS(e))
			return e;
	}
	{
		fd_set w;
		struct timeval tv;
		int sel;
		FD_ZERO(&w);
		FD_SET(fd, &w);
		tv.tv_sec = timeout_ms / 1000;
		tv.tv_usec = (timeout_ms % 1000) * 1000;
		sel = select(0, NULL, &w, NULL, timeout_ms > 0 ? &tv : NULL);
		if (sel == 0)
			return WSAETIMEDOUT;
		if (sel < 0)
			return SOCK_ERRNO;
		{
			int soerr = 0;
			int len = sizeof soerr;
			if (getsockopt(fd, SOL_SOCKET, SO_ERROR, (char *)&soerr, &len) != 0)
				return SOCK_ERRNO;
			if (soerr != 0)
				return soerr;
		}
	}
	nb = 0;
	ioctlsocket(fd, FIONBIO, &nb);
	return 0;
#else
	int fl = fcntl(fd, F_GETFL, 0);
	if (fl < 0)
		return errno;
	if (fcntl(fd, F_SETFL, fl | O_NONBLOCK) < 0)
		return errno;
	rc = connect(fd, sa, slen);
	if (rc == 0) {
		fcntl(fd, F_SETFL, fl);
		return 0;
	}
	{
		int e = errno;
		if (!SOCK_INPROGRESS(e)) {
			fcntl(fd, F_SETFL, fl);
			return e;
		}
	}
	{
		fd_set w;
		struct timeval tv;
		int sel;
		FD_ZERO(&w);
		FD_SET(fd, &w);
		tv.tv_sec = timeout_ms / 1000;
		tv.tv_usec = (timeout_ms % 1000) * 1000;
		for (;;) {
			sel = select(fd + 1, NULL, &w, NULL, timeout_ms > 0 ? &tv : NULL);
			if (sel < 0 && errno == EINTR)
				continue;
			break;
		}
		if (sel == 0) {
			fcntl(fd, F_SETFL, fl);
			return ETIMEDOUT;
		}
		if (sel < 0) {
			int err = errno;
			fcntl(fd, F_SETFL, fl);
			return err;
		}
		{
			int soerr = 0;
			socklen_t len = sizeof soerr;
			if (getsockopt(fd, SOL_SOCKET, SO_ERROR, &soerr, &len) != 0) {
				int err = errno;
				fcntl(fd, F_SETFL, fl);
				return err;
			}
			if (soerr != 0) {
				fcntl(fd, F_SETFL, fl);
				return soerr;
			}
		}
	}
	if (fcntl(fd, F_SETFL, fl) < 0)
		return errno;
	return 0;
#endif
}

/* Cari slot kosong; -1 bila penuh. */
static int slot_free(redis_state *st)
{
	int i;
	for (i = 0; i < REDIS_MAX_CONN; i++)
		if (st->conns[i].fd < 0)
			return i;
	return -1;
}

/* ---- metode koneksi ---- */

/* Eksekusi nama perintah + args → *ret (balasan mentah). 0 sukses. */
static int exec_cmd(gne_ctx *ctx, conn *c, const char *name, const argvec *av, gne_handle *ret)
{
	if (send_cmd(c, name, av) != 0) {
		int e = SOCK_ERRNO;
		char buf[160], txt[96];
		if (is_timeout_err(e))
			snprintf(buf, sizeof buf, "mengirim perintah %s: waktu tunggu %d ms habis", name, c->timeout_ms);
		else
			snprintf(buf, sizeof buf, "mengirim perintah %s: %s", name, sock_text(e, txt, sizeof txt));
		api->throw(ctx, "redis_error", is_timeout_err(e) ? 504 : 502, buf);
		return -1;
	}
	return read_reply(ctx, c, ret, 0);
}

/* Balasan integer → bool (untuk exists/expire). */
static int int_to_bool(gne_ctx *ctx, gne_handle *h, int negate)
{
	int64_t n;
	if (api->get_int(ctx, *h, &n) != 0) {
		api->throw(ctx, "redis_error", 500, "balasan Redis tidak terduga (bukan integer)");
		return -1;
	}
	*h = api->bool_new(ctx, negate ? n == 0 : n != 0);
	return 0;
}

/* Pola metode umum: self + args → kirim nama → balasan mentah. */
static int method_simple(gne_ctx *ctx, int argc, const gne_handle *argv,
			 gne_handle *ret, const char *name, int konversi)
{
	conn *c = self_conn(ctx, argv[0]);
	argvec av;
	if (!c)
		return -1;
	if (args_collect(ctx, argc, argv, 1, &av) != 0)
		return -1;
	if (exec_cmd(ctx, c, name, &av, ret) != 0) {
		args_free(&av);
		return -1;
	}
	args_free(&av);
	if (konversi == 1) /* balasan integer → bool */
		return int_to_bool(ctx, ret, 0);
	if (konversi == 2) /* balasan integer → bool terbalik */
		return int_to_bool(ctx, ret, 1);
	return 0;
}

#define METHOD_SIMPLE(nm, konv)                                              \
	static int nm(gne_ctx *ctx, int argc, const gne_handle *argv,        \
		      gne_handle *ret)                                       \
	{                                                                    \
		return method_simple(ctx, argc, argv, ret, #nm, konv);       \
	}

/* Nama perintah berbeda dari nama metode. */
static int m_del(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	return method_simple(ctx, argc, argv, ret, "DEL", 0);
}
static int m_exists(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	return method_simple(ctx, argc, argv, ret, "EXISTS", 1);
}
static int m_expire(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	return method_simple(ctx, argc, argv, ret, "EXPIRE", 1);
}
static int m_hset(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	return method_simple(ctx, argc, argv, ret, "HSET", 0);
}
static int m_hget(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	return method_simple(ctx, argc, argv, ret, "HGET", 0);
}
static int m_lpush(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	return method_simple(ctx, argc, argv, ret, "LPUSH", 0);
}
static int m_lrange(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	return method_simple(ctx, argc, argv, ret, "LRANGE", 0);
}
static int m_keys(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	return method_simple(ctx, argc, argv, ret, "KEYS", 0);
}
static int m_get(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	return method_simple(ctx, argc, argv, ret, "GET", 0);
}
static int m_set(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	return method_simple(ctx, argc, argv, ret, "SET", 0);
}
static int m_incr(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	return method_simple(ctx, argc, argv, ret, "INCR", 0);
}

/* ping() → true (balasan apapun non-galat berarti hidup). */
static int m_ping(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	conn *c = self_conn(ctx, argv[0]);
	argvec av = { NULL, NULL, 0 };
	gne_handle dummy;
	(void)argc;
	if (!c)
		return -1;
	if (exec_cmd(ctx, c, "PING", &av, &dummy) != 0)
		return -1;
	*ret = api->bool_new(ctx, 1);
	return 0;
}

/* cmd(nama, ...args) → balasan mentah. */
static int m_cmd(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	conn *c = self_conn(ctx, argv[0]);
	argvec av;
	char *name;
	if (!c)
		return -1;
	if (args_collect(ctx, argc, argv, 1, &av) != 0)
		return -1;
	if (av.n == 0) {
		args_free(&av);
		api->throw(ctx, "type_error", 500, "cmd() butuh nama perintah");
		return -1;
	}
	name = av.v[0]; /* nama perintah = argumen pertama */
	{
		argvec rest;
		int i;
		rest.n = av.n - 1;
		rest.v = av.v + 1;
		rest.len = av.len + 1;
		if (exec_cmd(ctx, c, name, &rest, ret) != 0) {
			args_free(&av);
			return -1;
		}
		/* rest menunjuk ke dalam av — bebaskan semua lewat av. */
		(void)i;
	}
	args_free(&av);
	return 0;
}

/* close() → true bila menutup koneksi hidup, false bila sudah tertutup
 * atau objek basi (idempoten; metode lain tetap melempar "sudah ditutup").
 * Generasi dinaikkan supaya objek lama tidak bisa mengendalikan koneksi
 * baru yang kebetulan memakai slot yang sama. */
static int m_close(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	redis_state *st = api->get_data(ctx);
	gne_handle h;
	int64_t id, gen;
	(void)argc;
	if (!st || api->obj_get(ctx, argv[0], "id", &h) != 0 ||
	    api->get_int(ctx, h, &id) != 0 ||
	    id < 0 || id >= REDIS_MAX_CONN ||
	    api->obj_get(ctx, argv[0], "gen", &h) != 0 ||
	    api->get_int(ctx, h, &gen) != 0 ||
	    st->conns[id].fd < 0 || st->conns[id].gen != gen) {
		*ret = api->bool_new(ctx, 0);
		return 0;
	}
	rfd_close(st->conns[id].fd);
	st->conns[id].fd = -1;
	st->conns[id].gen++; /* invalidate objek ini untuk koneksi berikutnya */
	free(st->conns[id].rbuf);
	st->conns[id].rbuf = NULL;
	st->conns[id].rcap = st->conns[id].rlen = st->conns[id].rpos = 0;
	*ret = api->bool_new(ctx, 1);
	return 0;
}

/* ---- redis.connect(host, port [, timeout_ms]) ---- */
static int redis_connect(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	redis_state *st = api->get_data(ctx);
	char host[256], ports[16];
	size_t hlen;
	int64_t port, timeout = REDIS_TIMEOUT_DEFAULT_MS;
	int slot, e = 0;
	struct addrinfo hints, *res = NULL, *ai;
	conn *c;
	gne_handle obj, hv;

	if (!st) {
		api->throw(ctx, "redis_error", 500, "state modul Redis hilang");
		return -1;
	}
	if (argc >= 3 && api->get_int(ctx, argv[2], &timeout) != 0) {
		api->throw(ctx, "type_error", 500, "timeout harus berupa angka (milidetik)");
		return -1;
	}
	if (timeout < 0) {
		api->throw(ctx, "type_error", 500, "timeout tidak boleh negatif (0 = tanpa batas)");
		return -1;
	}
	if (api->str_len(ctx, argv[0], &hlen) != 0 || hlen == 0 || hlen >= sizeof host) {
		api->throw(ctx, "type_error", 500, "host harus string 1–255 karakter");
		return -1;
	}
	api->str_copy(ctx, argv[0], host, sizeof host);
	if (api->get_int(ctx, argv[1], &port) != 0 || port < 1 || port > 65535) {
		api->throw(ctx, "type_error", 500, "port harus angka 1–65535");
		return -1;
	}
	slot = slot_free(st);
	if (slot < 0) {
		api->throw(ctx, "redis_error", 500,
			   "koneksi penuh (maksimal 64 koneksi terbuka; tutup yang tidak dipakai)");
		return -1;
	}

	snprintf(ports, sizeof ports, "%" PRId64, port);
	memset(&hints, 0, sizeof hints);
	hints.ai_family = AF_UNSPEC;
	hints.ai_socktype = SOCK_STREAM;
	if (getaddrinfo(host, ports, &hints, &res) != 0) {
		char msg[320];
		snprintf(msg, sizeof msg, "gagal memecahkan alamat host %s", host);
		api->throw(ctx, "redis_error", 502, msg);
		return -1;
	}

	for (ai = res; ai; ai = ai->ai_next) {
		rfd_t fd = socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
		if (fd == RFD_INVALID) {
			e = SOCK_ERRNO;
			continue;
		}
#ifdef SO_NOSIGPIPE
		{
			int on = 1;
			setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &on, sizeof on);
		}
#endif
		set_timeouts(fd, (int)timeout);
		e = conn_dial(fd, ai->ai_addr, (socklen_t)ai->ai_addrlen, (int)timeout);
		if (e == 0) {
			c = &st->conns[slot];
			c->fd = (int)fd;
			c->timeout_ms = (int)timeout;
			c->gen++; /* siklus hidup baru — invalidasi objek lama */
			c->rbuf = NULL;
			c->rcap = c->rlen = c->rpos = 0;
			freeaddrinfo(res);

			obj = api->object(ctx);
			hv = api->int_new(ctx, slot);
			api->obj_set(ctx, obj, "id", hv);
			hv = api->int_new(ctx, c->gen);
			api->obj_set(ctx, obj, "gen", hv);
			hv = api->string(ctx, host, strlen(host));
			api->obj_set(ctx, obj, "host", hv);
			hv = api->int_new(ctx, port);
			api->obj_set(ctx, obj, "port", hv);

			api->define_method(ctx, obj, "ping", 0, 0, m_ping, obj);
			api->define_method(ctx, obj, "get", 1, 1, m_get, obj);
			api->define_method(ctx, obj, "set", 2, 2, m_set, obj);
			api->define_method(ctx, obj, "del", 1, 1, m_del, obj);
			api->define_method(ctx, obj, "exists", 1, 1, m_exists, obj);
			api->define_method(ctx, obj, "incr", 1, 1, m_incr, obj);
			api->define_method(ctx, obj, "expire", 2, 2, m_expire, obj);
			api->define_method(ctx, obj, "hset", 3, 3, m_hset, obj);
			api->define_method(ctx, obj, "hget", 2, 2, m_hget, obj);
			api->define_method(ctx, obj, "lpush", 2, -1, m_lpush, obj);
			api->define_method(ctx, obj, "lrange", 3, 3, m_lrange, obj);
			api->define_method(ctx, obj, "keys", 1, 1, m_keys, obj);
			api->define_method(ctx, obj, "cmd", 1, -1, m_cmd, obj);
			api->define_method(ctx, obj, "close", 0, 0, m_close, obj);

			*ret = obj;
			return 0;
		}
		rfd_close(fd);
	}
	freeaddrinfo(res);

	{
		char msg[384], txt[96];
		if (e == ETIMEDOUT || is_timeout_err(e))
			snprintf(msg, sizeof msg, "gagal terhubung ke %.200s:%" PRId64 " (waktu tunggu %d ms habis)",
				 host, port, (int)timeout);
		else
			snprintf(msg, sizeof msg, "gagal terhubung ke %.200s:%" PRId64 ": %s",
				 host, port, sock_text(e, txt, sizeof txt));
		api->throw(ctx, "redis_error", 502, msg);
	}
	return -1;
}

int gne_module_init(const gne_host_api *a, gne_ctx *ctx, gne_handle *out)
{
	redis_state *st;
	int i;
	api = a;
	if (api->abi != GNE_ABI) {
		api->throw(ctx, "gne_abi", 500, "ABI berbeda");
		return 1;
	}
#ifdef _WIN32
	{
		static int wsa_done;
		if (!wsa_done) {
			WSADATA wsa;
			if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) {
				api->throw(ctx, "redis_error", 502, "WSAStartup gagal");
				return 1;
			}
			wsa_done = 1;
		}
	}
#endif
	st = calloc(1, sizeof(*st));
	if (!st) {
		api->throw(ctx, "redis_oom", 500, "gagal mengalokasikan state koneksi");
		return 1;
	}
	for (i = 0; i < REDIS_MAX_CONN; i++)
		st->conns[i].fd = -1;
	api->set_data(ctx, st);

	api->define_fn(ctx, "connect", 2, 3, redis_connect);
	*out = api->module(ctx);
	return 0;
}
