/* httpclient.c — ekstensi GNE resmi: klien HTTP/1.1 murni C.
 *
 * Gaya pemakaian:
 *
 *   use "httpclient"
 *   $r = httpclient.get("http://127.0.0.1:8868/api/ping")
 *   print($r.status)         //200
 *   print($r.ok)             // true (2xx)
 *   print($r.body)           // isi respons (string, boleh biner)
 *   print($r.header("content-type"))
 *
 *   $r = httpclient.post($url, $json, "application/json")
 *   $r = httpclient.post($url, $json, "application/json", 3000)   // timeout ms
 *
 *   // kontrol penuh: metode, body, header tambahan ("Nama: nilai")
 *   $r = httpclient.request("PUT", $url, $body,
 *          ["Authorization: Bearer " + $t, "X-Id: 1"], 3000)
 *   $r = httpclient.request("DELETE", $url)
 *
 *   // respons: $r.status (int), $r.ok (bool), $r.body (string),
 *   //           $r.url (URL akhir setelah pengalihan), $r.redirects (int),
 *   //           $r.headers (array "Nama: nilai"), $r.header(nama) → string|null
 *
 * Cakupan & keputusan desain:
 *   - HANYA skema http://. https:// DITOLAK dengan pesan jelas — tanpa
 *     dependensi TLS bawaan (sama seperti ekstensi smtp); gunakan
 *     proksi TLS lokal (mis. caddy/stunnel) bila perlu https.
 *   - HTTP/1.1 dengan "Connection: close" (satu permintaan per koneksi;
 *     tanpa keep-alihan, tanpa pipeline — jauh lebih sederhana & aman).
 *   - Pengalihan301/302/303/307/308 diikuti sampai5 hop: 303 (dan
 *    301/302 pada non-GET/HEAD) diubah jadi GET tanpa body — perilaku
 *     peramban. Location absolut, protokol-ganda (//), mutlak-akar (/),
 *     relatif, dan "?query" semuanya didukung.
 *   - Body respons: Content-Length, chunked (didekode), atau baca
 *     sampai EOF (HTTP/1.0). Respons204/304/HEAD tanpa body.
 *   - Kirim "Accept-Encoding: identity" — bila server tetap mengirim
 *     Content-Encoding lain (gzip dll), galat jelas dilempar, bukan
 *     body sampah.
 *   - Status4xx/5xx TIDAK melempar galat (respons normal) — periksa
 *     $r.status / $r.ok. Galat jaringan/protokol yang dilempar:
 *
 * Galat (catchable try/catch):
 *   - argumen/url/header tak sah, https ditolak → "type_error" 500
 *   - gagal terhubung / server menutup / terpotong → "httpclient_error" 502
 *   - waktu tunggu habis → "httpclient_error" 504
 *   - respons rusak, body >64 MiB, >5 pengalihan, kompresi tak
 *     didukung, upgrade (101) → "httpclient_error" 500
 *
 * Batas: URL ≤4096, host ≤256, body kirim/terima ≤64 MiB, header
 * respons ≤64 KiB & ≤512 baris, header kirim ≤16 KiB, timeout bawaan
 *5000 ms per operasi jaringan (0 = tanpa batas).
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
typedef SOCKET hfd_t;
#define HFD_INVALID INVALID_SOCKET
#define hfd_close closesocket
#define SOCK_ERRNO WSAGetLastError()
#define SOCK_EINTR WSAEINTR
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
typedef int hfd_t;
#define HFD_INVALID (-1)
#define hfd_close close
#define SOCK_ERRNO errno
#define SOCK_EINTR EINTR
#define SOCK_TIMEOUT(e) ((e) == EAGAIN || (e) == EWOULDBLOCK || (e) == ETIMEDOUT)
#define SOCK_INPROGRESS(e) ((e) == EINPROGRESS)
#ifndef MSG_NOSIGNAL
#define MSG_NOSIGNAL 0
#endif
#endif

static const gne_host_api *api;

/* ---- batas (dokumentasikan di docs) ---- */
#define HC_TIMEOUT_DEFAULT_MS 5000
#define HC_MAX_URL 4096
#define HC_MAX_HOST 256
#define HC_MAX_METHOD 16
#define HC_MAX_BODY (64u << 20)    /*64 MiB */
#define HC_MAX_HDR (64u << 10)     /*64 KiB */
#define HC_MAX_LINES 512           /* baris header respons */
#define HC_MAX_REQHDR (16u << 10)  /*16 KiB */
#define HC_MAX_REDIRECT 5
#define HC_MAX_INTERIM 8

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

/* ---- string helper ---- */

/* Perbandingan tanpa memedulikan huruf besar/kecil. */
static int ci_n(const char *a, const char *b, size_t n)
{
	size_t i;
	for (i = 0; i < n; i++) {
		char x = a[i], y = b[i];
		if (x >= 'A' && x <= 'Z')
			x = (char)(x + 32);
		if (y >= 'A' && y <= 'Z')
			y = (char)(y + 32);
		if (x != y)
			return 0;
	}
	return 1;
}

static int ci_eq(const char *a, const char *b)
{
	while (*a && *b) {
		char x = *a, y = *b;
		if (x >= 'A' && x <= 'Z')
			x = (char)(x + 32);
		if (y >= 'A' && y <= 'Z')
			y = (char)(y + 32);
		if (x != y)
			return 0;
		a++;
		b++;
	}
	return *a == '\0' && *b == '\0';
}

/* sbuf — buffer string tumbuh. */
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

static int sb_puts(sbuf *s, const char *p)
{
	return sb_add(s, p, strlen(p));
}

/* Daftar string milik C (header tambahan / baris respons). */
typedef struct {
	char **v;
	size_t *len;
	int n, cap;
} strlist;

static void sl_init(strlist *l)
{
	l->v = NULL;
	l->len = NULL;
	l->n = l->cap = 0;
}

static void sl_free(strlist *l)
{
	int i;
	for (i = 0; i < l->n; i++)
		free(l->v[i]);
	free(l->v);
	free(l->len);
	sl_init(l);
}

/* Salin baris (tanpa CRLF) ke daftar; -1 = gagal alokasi. */
static int sl_add(strlist *l, const char *p, size_t n)
{
	char *c;
	if (l->n == l->cap) {
		int ncap = l->cap ? l->cap * 2 : 16;
		char **nv = realloc(l->v, (size_t)ncap * sizeof(char *));
		size_t *nl = realloc(l->len, (size_t)ncap * sizeof(size_t));
		if (!nv || !nl) {
			free(nv);
			free(nl);
			return -1;
		}
		l->v = nv;
		l->len = nl;
		l->cap = ncap;
	}
	c = malloc(n + 1);
	if (!c)
		return -1;
	memcpy(c, p, n);
	c[n] = '\0';
	l->v[l->n] = c;
	l->len[l->n] = n;
	l->n++;
	return 0;
}

/* ---- koneksi ---- */

enum {
	BUF_OK = 0,
	BUF_EOF = -1,
	BUF_TIMEOUT = -2,
	BUF_ERR = -3,
	BUF_TOOLARGE = -4
};

typedef struct {
	hfd_t fd;
	int timeout_ms;
	char *rbuf;
	size_t rcap, rlen, rpos;
} hconn;

/* Batas memori buffer baca: header + body + jangkar. */
static size_t hc_buf_cap(void)
{
	return (size_t)HC_MAX_BODY + HC_MAX_HDR + 4096;
}

static int buf_grow(hconn *c, size_t need)
{
	size_t cap;
	char *nb;
	if (need <= c->rcap)
		return BUF_OK;
	if (need > hc_buf_cap())
		return BUF_TOOLARGE;
	cap = c->rcap ? c->rcap : 4096;
	while (cap < need)
		cap *= 2;
	if (cap > hc_buf_cap())
		cap = hc_buf_cap();
	nb = realloc(c->rbuf, cap);
	if (!nb)
		return BUF_ERR;
	c->rbuf = nb;
	c->rcap = cap;
	return BUF_OK;
}

static int buf_refill(hconn *c)
{
	int rc;
	if (c->rpos > 0) {
		if (c->rlen == c->rpos) {
			c->rlen = 0;
			c->rpos = 0;
		} else {
			memmove(c->rbuf, c->rbuf + c->rpos, c->rlen - c->rpos);
			c->rlen -= c->rpos;
			c->rpos = 0;
		}
	}
	if (c->rlen == c->rcap) {
		rc = buf_grow(c, c->rcap ? c->rcap * 2 : 4096);
		if (rc != BUF_OK)
			return rc;
	}
	for (;;) {
#ifdef _WIN32
		int k = recv(c->fd, c->rbuf + c->rlen,
			     (int)(c->rcap - c->rlen), 0);
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
		{
			ssize_t n = recv(c->fd, c->rbuf + c->rlen,
					 c->rcap - c->rlen, 0);
			if (n > 0) {
				c->rlen += (size_t)n;
				return BUF_OK;
			}
			if (n == 0)
				return BUF_EOF;
			{
				int e = errno;
				if (is_retry_err(e))
					continue;
				if (is_timeout_err(e))
					return BUF_TIMEOUT;
				return BUF_ERR;
			}
		}
#endif
	}
}

/* Pastikan ada ≥ n byte dari posisi baca. */
static int buf_ensure(hconn *c, size_t n)
{
	while (c->rlen - c->rpos < n) {
		int rc = buf_refill(c);
		if (rc != BUF_OK)
			return rc;
	}
	return BUF_OK;
}

static int send_all(hfd_t fd, const char *p, size_t n)
{
	while (n > 0) {
#ifdef _WIN32
		int chunk = n > 0x7fffffffu ? 0x7fffffff : (int)n;
		int k = send(fd, p, chunk, 0);
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
		ssize_t k = send(fd, p, n, MSG_NOSIGNAL);
		if (k > 0) {
			p += k;
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
		return -1;
	}
	return 0;
}

static void set_timeouts(hfd_t fd, int ms)
{
	if (ms <= 0)
		return; /* 0 = tanpa batas (blocking murni) */
#ifdef _WIN32
	{
		DWORD t = (DWORD)ms;
		setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, (const char *)&t,
			   (int)sizeof t);
		setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, (const char *)&t,
			   (int)sizeof t);
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

/* Konek nonblocking + select(timeout).0 = sukses; lain = errno/WSA. */
static int conn_dial(hfd_t fd, const struct sockaddr *sa, socklen_t slen,
		     int timeout_ms)
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
			if (getsockopt(fd, SOL_SOCKET, SO_ERROR,
				       (char *)&soerr, &len) != 0)
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
			sel = select(fd + 1, NULL, &w, NULL,
				     timeout_ms > 0 ? &tv : NULL);
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
			if (getsockopt(fd, SOL_SOCKET, SO_ERROR, &soerr,
				       &len) != 0) {
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

/* ---- URL ---- */

typedef struct {
	char host[HC_MAX_HOST];
	int port;
	int ipv6;
	char path[HC_MAX_URL]; /* path + query, selalu diawali '/' */
} url_t;

/*0 = sukses; -1 = salah (pesan di err). */
static int url_parse(const char *url, size_t ulen, url_t *u, char *err,
		     size_t errcap)
{
	const char *p, *e = url + ulen, *h0, *h1;
	int colon = -1;
	size_t i;

	if (ulen >= 8 && ci_n(url, "https://", 8)) {
		snprintf(err, errcap,
			 "https belum didukung httpclient (tanpa TLS bawaan, "
			 "sama seperti smtp) — gunakan proksi TLS lokal untuk https");
		return -1;
	}
	if (ulen < 7 || !ci_n(url, "http://", 7)) {
		snprintf(err, errcap, "url harus diawali http://");
		return -1;
	}
	for (i = 0; i < ulen; i++) {
		unsigned char ch = (unsigned char)url[i];
		if (ch < 0x21 || ch > 0x7e) {
			snprintf(err, errcap,
				 "url hanya boleh ASCII (spasi/karakter non-ASCII "
				 "harus %%encoding)");
			return -1;
		}
	}
	p = url + 7;
	h0 = p;
	while (p < e && *p != '/' && *p != '?' && *p != '#' && *p != '@')
		p++;
	h1 = p;
	if (h1 == h0) {
		snprintf(err, errcap, "url tanpa host");
		return -1;
	}
	if (p < e && *p == '@') {
		snprintf(err, errcap,
			 "info pengguna (user:pass@) di url tidak didukung — "
			 "kirim lewat header");
		return -1;
	}
	/* host + port */
	u->ipv6 = 0;
	if (*h0 == '[') {
		const char *rb = memchr(h0, ']', (size_t)(h1 - h0));
		if (!rb) {
			snprintf(err, errcap, "host ipv6 tanpa ] penutup");
			return -1;
		}
		if ((size_t)(rb - h0 - 1) >= HC_MAX_HOST) {
			snprintf(err, errcap, "host terlalu panjang");
			return -1;
		}
		memcpy(u->host, h0 + 1, (size_t)(rb - h0 - 1));
		u->host[rb - h0 - 1] = '\0';
		u->ipv6 = 1;
		if (rb + 1 < h1) {
			if (rb[1] != ':') {
				snprintf(err, errcap, "url host salah");
				return -1;
			}
			colon = 1; /* tanda ada port */
			h0 = rb + 2;
			if (h0 > h1) {
				snprintf(err, errcap, "port kosong");
				return -1;
			}
		}
	} else {
		for (i = 0; i < (size_t)(h1 - h0); i++)
			if (h0[i] == ':')
				break;
		if (i < (size_t)(h1 - h0)) {
			colon = (int)i;
			if (memchr(h0 + i + 1, ':',
				   (size_t)(h1 - h0) - i - 1)) {
				snprintf(err, errcap,
					 "url host salah (ipv6 wajib pakai [ ... ])");
				return -1;
			}
			if ((size_t)colon == 0) {
				snprintf(err, errcap, "host kosong");
				return -1;
			}
			if ((size_t)colon >= HC_MAX_HOST) {
				snprintf(err, errcap, "host terlalu panjang");
				return -1;
			}
			memcpy(u->host, h0, (size_t)colon);
			u->host[colon] = '\0';
			h0 = h0 + colon + 1;
		} else {
			if ((size_t)(h1 - h0) >= HC_MAX_HOST) {
				snprintf(err, errcap, "host terlalu panjang");
				return -1;
			}
			memcpy(u->host, h0, (size_t)(h1 - h0));
			u->host[h1 - h0] = '\0';
		}
	}
	u->port = 80;
	if (colon >= 0 && (!u->ipv6 || (h1 > h0))) {
		long v = 0;
		if (h0 == h1) {
			snprintf(err, errcap, "port kosong");
			return -1;
		}
		for (; h0 < h1; h0++) {
			if (*h0 < '0' || *h0 > '9') {
				snprintf(err, errcap, "port bukan angka");
				return -1;
			}
			v = v * 10 + (*h0 - '0');
			if (v > 65535) {
				snprintf(err, errcap, "port di luar1-65535");
				return -1;
			}
		}
		if (v < 1) {
			snprintf(err, errcap, "port di luar1-65535");
			return -1;
		}
		u->port = (int)v;
	}
	/* path + query (potong fragment) */
	{
		size_t n = 0;
		if (p >= e || *p == '#') {
			u->path[0] = '/';
			u->path[1] = '\0';
			return 0;
		}
		if (*p != '/')
			u->path[n++] = '/';
		while (p < e && *p != '#') {
			if (n + 1 >= sizeof u->path) {
				snprintf(err, errcap, "url terlalu panjang");
				return -1;
			}
			u->path[n++] = *p++;
		}
		u->path[n] = '\0';
	}
	return 0;
}

/* Authority (host:port) untuk header Host — ipv6 pakai kurung. */
static void url_authority(const url_t *u, char *out, size_t cap)
{
	if (u->ipv6) {
		if (u->port == 80)
			snprintf(out, cap, "[%s]", u->host);
		else
			snprintf(out, cap, "[%s]:%d", u->host, u->port);
	} else {
		if (u->port == 80)
			snprintf(out, cap, "%s", u->host);
		else
			snprintf(out, cap, "%s:%d", u->host, u->port);
	}
}

/* Resolusi nilai Location terhadap URL sekarang.0 = sukses. */
static int loc_resolve(const url_t *cur, const char *loc, size_t llen,
		       char *out, size_t cap)
{
	char auth[HC_MAX_HOST + 16];
	if (llen == 0)
		return -1;
	if (llen >= 7 && ci_n(loc, "http://", 7)) {
		if (llen >= cap)
			return -1;
		memcpy(out, loc, llen);
		out[llen] = '\0';
		return 0;
	}
	if (llen >= 2 && loc[0] == '/' && loc[1] == '/') { /* protokol ganda */
		if (llen + 5 >= cap)
			return -1;
		memcpy(out, "http:", 5);
		memcpy(out + 5, loc, llen);
		out[5 + llen] = '\0';
		return 0;
	}
	url_authority(cur, auth, sizeof auth);
	if (loc[0] == '/') {
		if (7 + strlen(auth) + llen + 1 >= cap)
			return -1;
		snprintf(out, cap, "http://%s%.*s", auth, (int)llen, loc);
		return 0;
	}
	if (loc[0] == '?') { /* ganti query, pertahankan path */
		const char *q = strchr(cur->path, '?');
		size_t base = q ? (size_t)(q - cur->path) : strlen(cur->path);
		if (7 + strlen(auth) + base + llen + 1 >= cap)
			return -1;
		snprintf(out, cap, "http://%s%.*s%.*s", auth, (int)base,
			 cur->path, (int)llen, loc);
		return 0;
	}
	/* relatif: dasar = path sampai terakhir '/' */
	{
		const char *slash = NULL, *q = strchr(cur->path, '?');
		size_t i;
		for (i = 0; cur->path[i] && (!q || cur->path + i < q); i++)
			if (cur->path[i] == '/')
				slash = cur->path + i;
		if (!slash) {
			if (7 + strlen(auth) + 1 + llen + 1 >= cap)
				return -1;
			snprintf(out, cap, "http://%s/%.*s", auth, (int)llen,
				 loc);
			return 0;
		}
		{
			size_t base = (size_t)(slash - cur->path) + 1;
			if (7 + strlen(auth) + base + llen + 1 >= cap)
				return -1;
			snprintf(out, cap, "http://%s%.*s%.*s", auth,
				 (int)base, cur->path, (int)llen, loc);
		}
	}
	return 0;
}

/* ---- argumen ---- */

/* String ketat; null → *out=NULL,*len=0 (tanpa galat). */
static int opt_str(gne_ctx *ctx, gne_handle h, char **out, size_t *len,
		   const char *what)
{
	int32_t t;
	size_t n;
	char *s;
	*out = NULL;
	*len = 0;
	if (api->type_of(ctx, h, &t) != 0)
		return -1;
	if (t == GNE_NULL)
		return 0;
	if (t != GNE_STRING) {
		char msg[96];
		snprintf(msg, sizeof msg, "%s harus string atau null", what);
		api->throw(ctx, "type_error", 500, msg);
		return -1;
	}
	if (api->str_len(ctx, h, &n) != 0)
		return -1;
	s = malloc(n + 1);
	if (!s) {
		api->throw(ctx, "httpclient_oom", 500,
			   "gagal mengalokasikan memori");
		return -1;
	}
	if (api->str_copy(ctx, h, s, n + 1) < 0) {
		free(s);
		api->throw(ctx, "type_error", 500, "argumen string tidak terbaca");
		return -1;
	}
	*out = s;
	*len = n;
	return 0;
}

/* Integer opsional: null → default; int → nilai; lain → galat. */
static int opt_int(gne_ctx *ctx, gne_handle h, int64_t def, int64_t *out,
		   const char *what)
{
	int32_t t;
	if (api->type_of(ctx, h, &t) != 0)
		return -1;
	if (t == GNE_NULL) {
		*out = def;
		return 0;
	}
	if (t != GNE_INT) {
		char msg[96];
		snprintf(msg, sizeof msg, "%s harus angka (milidetik)", what);
		api->throw(ctx, "type_error", 500, msg);
		return -1;
	}
	api->get_int(ctx, h, out);
	if (*out > 2147483647LL) {
		api->throw(ctx, "type_error", 500,
			   "timeout terlalu besar (maks 2147483647 ms)");
		return -1;
	}
	if (*out < 0) {
		api->throw(ctx, "type_error", 500,
			   "timeout tidak boleh negatif (0 = tanpa batas)");
		return -1;
	}
	return 0;
}

static int is_token_char(char c)
{
	if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
	    (c >= '0' && c <= '9'))
		return 1;
	switch (c) {
	case '!': case '#': case '$': case '%': case '&': case '\'':
	case '*': case '+': case '-': case '.': case '^': case '_':
	case '`': case '|': case '~':
		return 1;
	default:
		return 0;
	}
}

/* Kumpulkan header tambahan dari array "Nama: nilai" + validasi anti
 * injeksi CRLF. Kepemilikan pindah ke daftar. */
static int headers_collect(gne_ctx *ctx, gne_handle arr, strlist *out)
{
	int32_t t;
	size_t n = 0, i;
	if (api->type_of(ctx, arr, &t) != 0)
		return -1;
	if (t == GNE_NULL)
		return 0;
	if (t != GNE_ARRAY) {
		api->throw(ctx, "type_error", 500,
			   "headers harus array \"Nama: nilai\" atau null");
		return -1;
	}
	if (api->len(ctx, arr, &n) != 0)
		return -1;
	for (i = 0; i < n; i++) {
		gne_handle hv;
		char *s = NULL;
		size_t sl = 0, j;
		const char *colon;
		if (api->arr_get(ctx, arr, i, &hv) != 0)
			return -1;
		if (opt_str(ctx, hv, &s, &sl, "header") != 0)
			return -1;
		if (!s) {
			api->throw(ctx, "type_error", 500,
				   "header berisi null — harus \"Nama: nilai\"");
			return -1;
		}
		colon = memchr(s, ':', sl);
		if (!colon || colon == s) {
			free(s);
			api->throw(ctx, "type_error", 500,
				   "header harus berformat \"Nama: nilai\"");
			return -1;
		}
		for (j = 0; j < (size_t)(colon - s); j++) {
			if (!is_token_char(s[j])) {
				free(s);
				api->throw(ctx, "type_error", 500,
					   "nama header mengandung karakter tak sah");
				return -1;
			}
		}
		if (strpbrk(s, "\r\n") != NULL) {
			free(s);
			api->throw(ctx, "type_error", 500,
				   "header tidak boleh berisi CR/LF (injeksi)");
			return -1;
		}
		/* header terkelola transport tidak boleh ditimpa */
		{
			size_t nl = (size_t)(colon - s);
			if ((nl == 4 && ci_n(s, "host", 4)) ||
			    (nl == 14 && ci_n(s, "content-length", 14)) ||
			    (nl == 10 && ci_n(s, "connection", 10)) ||
			    (nl == 17 && ci_n(s, "transfer-encoding", 17))) {
				free(s);
				api->throw(ctx, "type_error", 500,
					   "header ini dikelola httpclient dan tidak boleh ditimpa");
				return -1;
			}
		}
		if (sl > HC_MAX_REQHDR) {
			free(s);
			api->throw(ctx, "type_error", 500,
				   "header terlalu panjang");
			return -1;
		}
		if (sl_add(out, s, sl) != 0) {
			free(s);
			api->throw(ctx, "httpclient_oom", 500,
				   "gagal mengalokasikan memori");
			return -1;
		}
		free(s);
	}
	return 0;
}

static int headers_has(const strlist *l, const char *name, size_t nl)
{
	int i;
	for (i = 0; i < l->n; i++) {
		const char *colon = memchr(l->v[i], ':', l->len[i]);
		if (colon && (size_t)(colon - l->v[i]) == nl &&
		    ci_n(l->v[i], name, nl))
			return 1;
	}
	return 0;
}

/* ---- respons ---- */

typedef struct {
	int code;
	strlist lines; /* baris "Nama: nilai" (tanpa CRLF) */
	char *body;
	size_t blen;
	int chunked;
	int has_clen;
	unsigned long long clen;
	char *location;
	int has_location;
	int has_ctenc; /* Content-Encoding != identity */
	char ctenc[32];
} hresp;

static void resp_init(hresp *r)
{
	memset(r, 0, sizeof *r);
	sl_init(&r->lines);
}

static void resp_free(hresp *r)
{
	sl_free(&r->lines);
	free(r->body);
	free(r->location);
	resp_init(r);
}

static int fail_io(gne_ctx *ctx, hconn *c, int rc, const char *what)
{
	char buf[256];
	if (rc == BUF_TIMEOUT) {
		snprintf(buf, sizeof buf, "%s: waktu tunggu %d ms habis", what,
			 c->timeout_ms);
		api->throw(ctx, "httpclient_error", 504, buf);
	} else if (rc == BUF_EOF) {
		snprintf(buf, sizeof buf, "%s: server menutup koneksi", what);
		api->throw(ctx, "httpclient_error", 502, buf);
	} else if (rc == BUF_TOOLARGE) {
		snprintf(buf, sizeof buf, "%s: melewati batas ukuran", what);
		api->throw(ctx, "httpclient_error", 500, buf);
	} else if (rc == BUF_ERR) {
		int e = SOCK_ERRNO;
		char txt[96];
		snprintf(buf, sizeof buf, "%s: %s", what,
			 sock_text(e, txt, sizeof txt));
		api->throw(ctx, "httpclient_error", 502, buf);
	} else {
		api->throw(ctx, "httpclient_error", 500, what);
	}
	return -1;
}

/* Cari akhir blok header "\r\n\r\n". *end = posisi sesudah blok. */
static int hdr_block_end(hconn *c, size_t *end)
{
	for (;;) {
		size_t i;
		for (i = c->rpos; i + 3 < c->rlen; i++) {
			if (c->rbuf[i] == '\r' && c->rbuf[i + 1] == '\n' &&
			    c->rbuf[i + 2] == '\r' && c->rbuf[i + 3] == '\n') {
				*end = i + 4;
				return BUF_OK;
			}
		}
		if (c->rlen - c->rpos > HC_MAX_HDR)
			return BUF_TOOLARGE;
		{
			int rc = buf_refill(c);
			if (rc != BUF_OK)
				return rc;
		}
	}
}

/* Baca status + header (melewati respons interim1xx). */
static int read_head(gne_ctx *ctx, hconn *c, int *code, strlist *lines)
{
	int iterasi;
	for (iterasi = 0;; iterasi++) {
		size_t end, i, ls;
		const char *b;
		char line[256];
		int rc;
		if (iterasi > HC_MAX_INTERIM) {
			api->throw(ctx, "httpclient_error", 500,
				   "terlalu banyak respons interim");
			return -1;
		}
		rc = hdr_block_end(c, &end);
		if (rc != BUF_OK)
			return fail_io(ctx, c, rc, "membaca header respons");
		b = c->rbuf + c->rpos;
		/* baris status */
		for (i = 0; i + 1 < end - c->rpos; i++)
			if (b[i] == '\r' && b[i + 1] == '\n')
				break;
		ls = i;
		if (ls >= sizeof line)
			ls = sizeof line - 1;
		memcpy(line, b, ls);
		line[ls] = '\0';
		if (strncmp(line, "HTTP/1.", 7) != 0 ||
		    (line[7] != '0' && line[7] != '1') ||
		    line[8] != ' ' ||
		    line[9] < '0' || line[9] > '9' ||
		    line[10] < '0' || line[10] > '9' ||
		    line[11] < '0' || line[11] > '9') {
			api->throw(ctx, "httpclient_error", 500,
				   "respons HTTP tidak sah");
			return -1;
		}
		*code = (line[9] - '0') * 100 + (line[10] - '0') * 10 +
			(line[11] - '0');
		/* potong buffer: buang baris status + blok header */
		if (*code >= 100 && *code < 200) {
			if (*code == 101) {
				api->throw(ctx, "httpclient_error", 500,
					   "upgrade protokol (101) tidak didukung");
				return -1;
			}
			c->rpos = end; /* interim — lanjut baca berikutnya */
			continue;
		}
		/* baris-baris header */
		{
			size_t pos = c->rpos + ls + 2;
			size_t stop = end;
			while (pos < stop) {
				size_t k = pos;
				while (k + 1 < stop &&
				       !(c->rbuf[k] == '\r' &&
					 c->rbuf[k + 1] == '\n'))
					k++;
				if (k > pos) {
					if (sl_add(lines, c->rbuf + pos,
						    k - pos) != 0) {
						api->throw(ctx,
							   "httpclient_oom",
							   500,
							   "gagal mengalokasikan memori");
						return -1;
					}
					if (lines->n > HC_MAX_LINES) {
						api->throw(ctx,
							   "httpclient_error",
							   500,
							   "terlalu banyak baris header respons");
						return -1;
					}
				}
				pos = k + 2;
			}
		}
		c->rpos = end;
		return 0;
	}
}

/* Pindai baris header → field. */
static void head_scan(hresp *r)
{
	int i;
	for (i = 0; i < r->lines.n; i++) {
		const char *s = r->lines.v[i];
		size_t n = r->lines.len[i];
		const char *colon = memchr(s, ':', n);
		size_t nl;
		if (!colon)
			continue;
		nl = (size_t)(colon - s);
		{
			const char *v = colon + 1;
			size_t vn = n - nl - 1;
			while (vn > 0 && (*v == ' ' || *v == '\t')) {
				v++;
				vn--;
			}
			if (nl == 14 && ci_n(s, "content-length", 14)) {
				unsigned long long q = 0;
				size_t k;
				int ok = vn > 0;
				for (k = 0; k < vn; k++) {
					if (v[k] < '0' || v[k] > '9') {
						ok = 0;
						break;
					}
					q = q * 10 + (unsigned long long)(v[k] - '0');
					if (q > (unsigned long long)HC_MAX_BODY) {
						ok = 2;
						break;
					}
				}
				if (ok) {
					r->has_clen = 1;
					r->clen = q;
				}
			} else if (nl == 17 &&
				   ci_n(s, "transfer-encoding", 17)) {
				if (vn >= 7 && ci_n(v, "chunked", 7))
					r->chunked = 1;
			} else if (nl == 8 && ci_n(s, "location", 8)) {
				if (!r->location) {
					r->location = malloc(vn + 1);
					if (r->location) {
						memcpy(r->location, v, vn);
						r->location[vn] = '\0';
						r->has_location = 1;
					}
				}
			} else if (nl == 16 && ci_n(s, "content-encoding", 16)) {
				if (!(vn == 8 && ci_n(v, "identity", 8))) {
					r->has_ctenc = 1;
					snprintf(r->ctenc, sizeof r->ctenc,
						 "%.*s", (int)(vn > 31 ? 31 : vn),
						 v);
				}
			}
		}
	}
}

/* Baca body chunked dari posisi baca. */
static int read_chunked(gne_ctx *ctx, hconn *c, hresp *r)
{
	sbuf out = { NULL, 0, 0 };
	for (;;) {
		size_t eol = 0, i, size = 0;
		int rc;
		/* cari CRLF baris ukuran */
		for (;;) {
			for (i = c->rpos; i + 1 < c->rlen; i++)
				if (c->rbuf[i] == '\r' &&
				    c->rbuf[i + 1] == '\n') {
					eol = i;
					break;
				}
			if (i + 1 < c->rlen)
				break;
			if (c->rlen - c->rpos > 64)
				goto rusak;
			rc = buf_refill(c);
			if (rc != BUF_OK)
				return fail_io(ctx, c, rc, "membaca chunk");
		}
		/* parse heksadesimal (abaikan ekstensi ';...') */
		{
			size_t k = c->rpos;
			int ada = 0;
			for (; k < eol; k++) {
				char ch = c->rbuf[k];
				int d;
				if (ch >= '0' && ch <= '9')
					d = ch - '0';
				else if (ch >= 'a' && ch <= 'f')
					d = ch - 'a' + 10;
				else if (ch >= 'A' && ch <= 'F')
					d = ch - 'A' + 10;
				else if (ch == ';' && ada)
					break;
				else if (ch == ' ' && ada)
					break;
				else
					goto rusak;
				size = size * 16 + (size_t)d;
				ada = 1;
				if (size > HC_MAX_BODY)
					goto besar;
			}
			if (!ada)
				goto rusak;
		}
		c->rpos = eol + 2;
		if (size == 0) {
			/* trailer sampai baris kosong */
			for (;;) {
				size_t te = 0, j;
				int rc2;
				for (;;) {
					for (j = c->rpos; j + 1 < c->rlen; j++)
						if (c->rbuf[j] == '\r' &&
						    c->rbuf[j + 1] == '\n') {
							te = j;
							break;
						}
					if (j + 1 < c->rlen)
						break;
					rc2 = buf_refill(c);
					if (rc2 == BUF_EOF)
						goto selesai; /* tanpa trailer */
					if (rc2 != BUF_OK)
						return fail_io(ctx, c, rc2,
							       "membaca trailer");
				}
				if (te == c->rpos) { /* baris kosong */
					c->rpos = te + 2;
					goto selesai;
				}
				c->rpos = te + 2;
			}
		}
		/* data chunk + CRLF */
		rc = buf_ensure(c, size + 2);
		if (rc != BUF_OK)
			return fail_io(ctx, c, rc, "membaca data chunk");
		if (out.len + size > HC_MAX_BODY)
			goto besar;
		if (sb_add(&out, c->rbuf + c->rpos, size) != 0)
			goto oom;
		c->rpos += size;
		if (c->rbuf[c->rpos] != '\r' || c->rbuf[c->rpos + 1] != '\n')
			goto rusak;
		c->rpos += 2;
	}
selesai:
	r->body = out.b ? out.b : malloc(1);
	if (!r->body) {
		free(out.b);
		goto oom2;
	}
	if (!out.b)
		r->body[0] = '\0';
	r->blen = out.len;
	return 0;
rusak:
	free(out.b);
	api->throw(ctx, "httpclient_error", 500,
		   "respons chunked rusak");
	return -1;
besar:
	free(out.b);
	api->throw(ctx, "httpclient_error", 500,
		   "body melebihi batas64 MiB");
	return -1;
oom:
	free(out.b);
oom2:
	api->throw(ctx, "httpclient_oom", 500,
		   "gagal mengalokasikan memori");
	return -1;
}

/* Baca body ber-Content-Length. */
static int read_clen(gne_ctx *ctx, hconn *c, hresp *r)
{
	int rc;
	if (r->clen > HC_MAX_BODY) {
		api->throw(ctx, "httpclient_error", 500,
			   "body melebihi batas64 MiB");
		return -1;
	}
	rc = buf_ensure(c, (size_t)r->clen);
	if (rc != BUF_OK)
		return fail_io(ctx, c, rc, "membaca body");
	r->body = malloc((size_t)r->clen + 1);
	if (!r->body) {
		api->throw(ctx, "httpclient_oom", 500,
			   "gagal mengalokasikan memori");
		return -1;
	}
	memcpy(r->body, c->rbuf + c->rpos, (size_t)r->clen);
	r->body[r->clen] = '\0';
	r->blen = (size_t)r->clen;
	c->rpos += (size_t)r->clen;
	return 0;
}

/* Baca sampai EOF (tanpa Content-Length / chunked). */
static int read_eof(gne_ctx *ctx, hconn *c, hresp *r)
{
	for (;;) {
		int rc = buf_refill(c);
		if (rc == BUF_EOF)
			break;
		if (rc != BUF_OK)
			return fail_io(ctx, c, rc, "membaca body");
	}
	r->blen = c->rlen - c->rpos;
	if (r->blen > HC_MAX_BODY) {
		api->throw(ctx, "httpclient_error", 500,
			   "body melebihi batas64 MiB");
		return -1;
	}
	r->body = malloc(r->blen + 1);
	if (!r->body) {
		api->throw(ctx, "httpclient_oom", 500,
			   "gagal mengalokasikan memori");
		return -1;
	}
	memcpy(r->body, c->rbuf + c->rpos, r->blen);
	r->body[r->blen] = '\0';
	return 0;
}

/* ---- dial + kirim + baca satu kali ---- */

static int dial(gne_ctx *ctx, const url_t *u, int timeout_ms, hfd_t *out,
		     char *errbuf, size_t errcap)
{
	(void)ctx;
	struct addrinfo hints, *res = NULL, *ai;
	char ports[16];
	int e = 0;
	hfd_t fd = HFD_INVALID;

	snprintf(ports, sizeof ports, "%d", u->port);
	memset(&hints, 0, sizeof hints);
	hints.ai_family = AF_UNSPEC;
	hints.ai_socktype = SOCK_STREAM;
	if (getaddrinfo(u->host, ports, &hints, &res) != 0) {
		snprintf(errbuf, errcap, "gagal memecahkan alamat host %.200s",
			 u->host);
		return -1;
	}
	for (ai = res; ai; ai = ai->ai_next) {
		fd = socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
		if (fd == HFD_INVALID) {
			e = SOCK_ERRNO;
			continue;
		}
#ifdef SO_NOSIGPIPE
		{
			int on = 1;
			setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &on,
				   sizeof on);
		}
#endif
		set_timeouts(fd, timeout_ms);
		e = conn_dial(fd, ai->ai_addr, (socklen_t)ai->ai_addrlen,
			      timeout_ms);
		if (e == 0) {
			*out = fd;
			freeaddrinfo(res);
			return 0;
		}
		hfd_close(fd);
		fd = HFD_INVALID;
	}
	freeaddrinfo(res);
	{
		char txt[96];
		if (e == ETIMEDOUT || is_timeout_err(e))
			snprintf(errbuf, errcap,
				 "gagal terhubung ke %.200s:%d (waktu tunggu %d ms habis)",
				 u->host, u->port, timeout_ms);
		else
			snprintf(errbuf, errcap, "gagal terhubung ke %.200s:%d: %s",
				 u->host, u->port, sock_text(e, txt, sizeof txt));
	}
	return -1;
}

/* Susun + kirim permintaan. -1 = sudah throw. */
static int kirim_permintaan(gne_ctx *ctx, hfd_t fd, const url_t *u,
			    const char *method, const char *body, size_t blen,
			    const char *ctype, const strlist *hdrs)
{
	sbuf s = { NULL, 0, 0 };
	char auth[HC_MAX_HOST + 16], cl[64];
	int i, rc = -1;

	url_authority(u, auth, sizeof auth);
	if (sb_puts(&s, method) || sb_puts(&s, " ") ||
	    sb_puts(&s, u->path) || sb_puts(&s, " HTTP/1.1\r\n") ||
	    sb_puts(&s, "Host: ") || sb_puts(&s, auth) || sb_puts(&s, "\r\n") ||
	    sb_puts(&s, "User-Agent: galang-httpclient\r\n") ||
	    sb_puts(&s, "Accept: */*\r\n") ||
	    sb_puts(&s, "Accept-Encoding: identity\r\n") ||
	    sb_puts(&s, "Connection: close\r\n"))
		goto oom;
	for (i = 0; i < hdrs->n; i++) {
		if (sb_puts(&s, hdrs->v[i]) || sb_puts(&s, "\r\n"))
			goto oom;
	}
	if (body && blen > 0) {
		if (!headers_has(hdrs, "content-type", 12)) {
			if (sb_puts(&s, "Content-Type: "))
				goto oom;
			if (sb_puts(&s, ctype ? ctype : "application/octet-stream"))
				goto oom;
			if (sb_puts(&s, "\r\n"))
				goto oom;
		}
		snprintf(cl, sizeof cl, "Content-Length: %zu\r\n\r\n", blen);
		if (sb_puts(&s, cl))
			goto oom;
		if (sb_add(&s, body, blen))
			goto oom;
	} else if (sb_puts(&s, "\r\n")) {
		goto oom;
	}
	if (send_all(fd, s.b, s.len) != 0) {
		int e = SOCK_ERRNO;
		char buf[160], txt[96];
		if (is_timeout_err(e))
			snprintf(buf, sizeof buf,
				 "mengirim permintaan: waktu tunggu habis");
		else
			snprintf(buf, sizeof buf, "mengirim permintaan: %s",
				 sock_text(e, txt, sizeof txt));
		api->throw(ctx, "httpclient_error",
			   is_timeout_err(e) ? 504 : 502, buf);
		goto beres;
	}
	rc = 0;
	goto beres;
oom:
	api->throw(ctx, "httpclient_oom", 500,
		   "gagal mengalokasikan memori");
beres:
	free(s.b);
	return rc;
}

static int hc_header(gne_ctx *ctx, int argc, const gne_handle *argv,
		     gne_handle *ret);

/* ---- inti: satu siklus permintaan + pengalihan ---- */

static int hc_lakukan(gne_ctx *ctx, const char *method_in, const char *url,
		      size_t ulen, const char *body, size_t blen,
		      const char *ctype, const strlist *hdrs, int64_t timeout_ms,
		      gne_handle *ret)
{
	char cururl[HC_MAX_URL], errbuf[384], meth[HC_MAX_METHOD + 1];
	size_t methlen;
	int hop, rc = -1;
	hresp resp;
	url_t u;
	int have_body;
	const char *cur_body;
	size_t cur_blen;

	if (ulen == 0 || ulen >= sizeof cururl) {
		api->throw(ctx, "type_error", 500,
			   "url kosong atau melebihi4096 karakter");
		return -1;
	}
	memcpy(cururl, url, ulen);
	cururl[ulen] = '\0';

	/* metode: token, kapital, ≤16 */
	methlen = strlen(method_in);
	if (methlen == 0 || methlen > HC_MAX_METHOD) {
		api->throw(ctx, "type_error", 500,
			   "metode kosong atau terlalu panjang");
		return -1;
	}
	{
		size_t i;
		for (i = 0; i < methlen; i++) {
			char ch = method_in[i];
			if (!is_token_char(ch)) {
				api->throw(ctx, "type_error", 500,
					   "metode mengandung karakter tak sah");
				return -1;
			}
			if (ch >= 'a' && ch <= 'z')
				ch = (char)(ch - 32);
			meth[i] = ch;
		}
		meth[methlen] = '\0';
	}
	if (blen > HC_MAX_BODY) {
		api->throw(ctx, "type_error", 500,
			   "body melebihi batas64 MiB");
		return -1;
	}
	have_body = body != NULL && blen > 0;
	cur_body = body;
	cur_blen = blen;
	resp_init(&resp);

	for (hop = 0;; hop++) {
		hconn c;
		int code;

		if (url_parse(cururl, strlen(cururl), &u, errbuf,
			      sizeof errbuf) != 0) {
			api->throw(ctx, "type_error", 500, errbuf);
			goto beres;
		}
		memset(&c, 0, sizeof c);
		c.fd = HFD_INVALID;
		c.timeout_ms = (int)timeout_ms;
		if (dial(ctx, &u, (int)timeout_ms, &c.fd, errbuf,
			 sizeof errbuf) != 0) {
			api->throw(ctx, "httpclient_error", 502, errbuf);
			goto beres;
		}
		if (kirim_permintaan(ctx, c.fd, &u, meth, have_body ? cur_body : NULL,
				      cur_blen, ctype, hdrs) != 0) {
			hfd_close(c.fd);
			free(c.rbuf);
			goto beres;
		}
		if (read_head(ctx, &c, &code, &resp.lines) != 0) {
			hfd_close(c.fd);
			free(c.rbuf);
			goto beres;
		}
		resp.code = code;
		head_scan(&resp);
		if (resp.has_ctenc) {
			hfd_close(c.fd);
			free(c.rbuf);
			api->throw(ctx, "httpclient_error", 500,
				   "respons terkompresi (Content-Encoding) tidak "
				   "didukung — httpclient sudah meminta identity");
			goto beres;
		}
		/* body (atau tanpa body untuk HEAD/204/304) */
		if (ci_eq(meth, "HEAD") || code == 204 || code == 304) {
			resp.body = malloc(1);
			if (resp.body)
				resp.body[0] = '\0';
			resp.blen = 0;
		} else if (resp.chunked) {
			if (read_chunked(ctx, &c, &resp) != 0) {
				hfd_close(c.fd);
				free(c.rbuf);
				goto beres;
			}
		} else if (resp.has_clen) {
			if (read_clen(ctx, &c, &resp) != 0) {
				hfd_close(c.fd);
				free(c.rbuf);
				goto beres;
			}
		} else if (read_eof(ctx, &c, &resp) != 0) {
			hfd_close(c.fd);
			free(c.rbuf);
			goto beres;
		}
		hfd_close(c.fd);
		free(c.rbuf);

		/* pengalihan? */
		if (resp.has_location &&
		    (code == 301 || code == 302 || code == 303 || code == 307 ||
		     code == 308)) {
			if (hop >= HC_MAX_REDIRECT) {
				api->throw(ctx, "httpclient_error", 500,
					   "terlalu banyak pengalihan (maksimal5)");
				goto beres;
			}
			{
				char next[HC_MAX_URL];
				if (loc_resolve(&u, resp.location,
						strlen(resp.location), next,
						sizeof next) != 0) {
					api->throw(ctx, "httpclient_error", 500,
						   "Location tidak dapat diresolusikan");
					goto beres;
				}
				/* 301/302/303: metode selain GET/HEAD diubah jadi GET
					 * tanpa body (perilaku peramban);307/308 dipertahankan. */
				if (code == 301 || code == 302 || code == 303) {
					if (!ci_eq(meth, "GET") && !ci_eq(meth, "HEAD")) {
						memcpy(meth, "GET", 4);
						methlen = 3;
						have_body = 0;
						cur_body = NULL;
						cur_blen = 0;
					}
				}
				memcpy(cururl, next, strlen(next) + 1);
				resp_free(&resp);
				continue;
			}
		}
		break;
	}

	/* susun objek respons */
	{
		gne_handle obj = api->object(ctx), hv, arr = api->array(ctx);
		int i;
		for (i = 0; i < resp.lines.n; i++) {
			hv = api->string(ctx, resp.lines.v[i], resp.lines.len[i]);
			api->arr_push(ctx, arr, hv);
		}
		hv = api->int_new(ctx, resp.code);
		api->obj_set(ctx, obj, "status", hv);
		hv = api->bool_new(ctx, resp.code >= 200 && resp.code < 300);
		api->obj_set(ctx, obj, "ok", hv);
		hv = api->string(ctx, resp.body ? resp.body : "",
				 resp.blen);
		api->obj_set(ctx, obj, "body", hv);
		hv = api->string(ctx, cururl, strlen(cururl));
		api->obj_set(ctx, obj, "url", hv);
		hv = api->int_new(ctx, hop);
		api->obj_set(ctx, obj, "redirects", hv);
		api->obj_set(ctx, obj, "headers", arr);
		api->define_method(ctx, obj, "header", 1, 1, hc_header, obj);
		*ret = obj;
		rc = 0;
	}

beres:
	resp_free(&resp);
	return rc;
}

/* Metode respons: header(nama) → nilai string pertama atau null. */
static int hc_header(gne_ctx *ctx, int argc, const gne_handle *argv,
		     gne_handle *ret)
{
	gne_handle arr, item;
	int32_t t;
	size_t n = 0, i;
	char name[128];
	size_t nlen;
	(void)argc;
	if (api->str_len(ctx, argv[1], &nlen) != 0 || nlen == 0 ||
	    nlen >= sizeof name) {
		api->throw(ctx, "type_error", 500,
			   "nama header harus string1–127 karakter");
		return -1;
	}
	if (api->str_copy(ctx, argv[1], name, sizeof name) < 0) {
		api->throw(ctx, "type_error", 500, "argumen string tidak terbaca");
		return -1;
	}
	if (api->obj_get(ctx, argv[0], "headers", &arr) != 0 ||
	    api->type_of(ctx, arr, &t) != 0 || t != GNE_ARRAY ||
	    api->len(ctx, arr, &n) != 0) {
		*ret = api->null(ctx);
		return 0;
	}
	for (i = 0; i < n; i++) {
		char line[1024];
		size_t ln;
		const char *colon;
		if (api->arr_get(ctx, arr, i, &item) != 0)
			break;
		if (api->str_len(ctx, item, &ln) != 0 || ln >= sizeof line)
			continue;
		if (api->str_copy(ctx, item, line, sizeof line) < 0)
			continue;
		colon = strchr(line, ':');
		if (!colon)
			continue;
		if ((size_t)(colon - line) == nlen &&
		    ci_n(line, name, nlen)) {
			const char *v = colon + 1;
			while (*v == ' ' || *v == '\t')
				v++;
			*ret = api->string(ctx, v, strlen(v));
			return 0;
		}
	}
	*ret = api->null(ctx);
	return 0;
}

/* ---- wajah modul ---- */

/* httpclient.get(url[, timeout]) */
static int hc_get(gne_ctx *ctx, int argc, const gne_handle *argv,
		  gne_handle *ret)
{
	char *url = NULL;
	size_t ulen = 0;
	int64_t timeout = HC_TIMEOUT_DEFAULT_MS;
	strlist hdrs;
	int rc;
	if (opt_str(ctx, argv[0], &url, &ulen, "url") != 0)
		return -1;
	if (!url || ulen == 0) {
		api->throw(ctx, "type_error", 500, "url harus string");
		return -1;
	}
	if (argc >= 2 && opt_int(ctx, argv[1], HC_TIMEOUT_DEFAULT_MS, &timeout,
				 "timeout") != 0) {
		free(url);
		return -1;
	}
	sl_init(&hdrs);
	rc = hc_lakukan(ctx, "GET", url, ulen, NULL, 0, NULL, &hdrs, timeout,
			ret);
	sl_free(&hdrs);
	free(url);
	return rc;
}

/* httpclient.post(url, body[, content_type[, timeout]]) */
static int hc_post(gne_ctx *ctx, int argc, const gne_handle *argv,
		   gne_handle *ret)
{
	char *url = NULL, *body = NULL, *ct = NULL;
	size_t ulen = 0, blen = 0, ctl = 0;
	int64_t timeout = HC_TIMEOUT_DEFAULT_MS;
	strlist hdrs;
	int rc;
	if (opt_str(ctx, argv[0], &url, &ulen, "url") != 0)
		return -1;
	if (!url || ulen == 0) {
		api->throw(ctx, "type_error", 500, "url harus string");
		free(url);
		return -1;
	}
	if (opt_str(ctx, argv[1], &body, &blen, "body") != 0) {
		free(url);
		return -1;
	}
	if (argc >= 3 && opt_str(ctx, argv[2], &ct, &ctl, "content_type") != 0) {
		free(url);
		free(body);
		return -1;
	}
	if (argc >= 4 &&
	    opt_int(ctx, argv[3], HC_TIMEOUT_DEFAULT_MS, &timeout,
		    "timeout") != 0) {
		free(url);
		free(body);
		free(ct);
		return -1;
	}
	if (ct && strpbrk(ct, "\r\n")) {
		api->throw(ctx, "type_error", 500,
			   "content_type tidak boleh berisi CR/LF");
		free(url);
		free(body);
		free(ct);
		return -1;
	}
	sl_init(&hdrs);
	rc = hc_lakukan(ctx, "POST", url, ulen, body, blen, ct, &hdrs, timeout,
			ret);
	sl_free(&hdrs);
	free(url);
	free(body);
	free(ct);
	return rc;
}

/* httpclient.request(method, url[, body[, headers[, timeout]]]) */
static int hc_request(gne_ctx *ctx, int argc, const gne_handle *argv,
		      gne_handle *ret)
{
	char *method = NULL, *url = NULL, *body = NULL;
	size_t mlen = 0, ulen = 0, blen = 0;
	int64_t timeout = HC_TIMEOUT_DEFAULT_MS;
	strlist hdrs;
	int rc;
	if (opt_str(ctx, argv[0], &method, &mlen, "metode") != 0)
		return -1;
	if (!method || mlen == 0) {
		api->throw(ctx, "type_error", 500, "metode harus string");
		free(method);
		return -1;
	}
	if (opt_str(ctx, argv[1], &url, &ulen, "url") != 0) {
		free(method);
		return -1;
	}
	if (!url || ulen == 0) {
		api->throw(ctx, "type_error", 500, "url harus string");
		free(method);
		free(url);
		return -1;
	}
	if (argc >= 3 && opt_str(ctx, argv[2], &body, &blen, "body") != 0) {
		free(method);
		free(url);
		return -1;
	}
	sl_init(&hdrs);
	if (argc >= 4 && headers_collect(ctx, argv[3], &hdrs) != 0) {
		free(method);
		free(url);
		free(body);
		sl_free(&hdrs);
		return -1;
	}
	if (argc >= 5 &&
	    opt_int(ctx, argv[4], HC_TIMEOUT_DEFAULT_MS, &timeout,
		    "timeout") != 0) {
		free(method);
		free(url);
		free(body);
		sl_free(&hdrs);
		return -1;
	}
	rc = hc_lakukan(ctx, method, url, ulen, body, blen, NULL, &hdrs,
			timeout, ret);
	sl_free(&hdrs);
	free(method);
	free(url);
	free(body);
	return rc;
}

int gne_module_init(const gne_host_api *a, gne_ctx *ctx, gne_handle *out)
{
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
				api->throw(ctx, "httpclient_error", 502,
					   "WSAStartup gagal");
				return 1;
			}
			wsa_done = 1;
		}
	}
#endif
	api->define_fn(ctx, "get", 1, 2, hc_get);
	api->define_fn(ctx, "post", 2, 4, hc_post);
	api->define_fn(ctx, "request", 2, 5, hc_request);
	*out = api->module(ctx);
	return 0;
}
