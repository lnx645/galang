/* smtp.c — ekstensi GNE resmi: klien SMTP (RFC 5321/5322) murni C.
 *
 * Gaya pemakaian:
 *
 *   use "smtp"
 *   $s = smtp.connect("127.0.0.1", 1025)          // greeting 220 + EHLO
 *   $s = smtp.connect("smtp.example.id", 587, 3000)  // timeout custom
 *   $s.auth("user@example.id", "rahasia")          // AUTH LOGIN
 *   $s.from("kirim@example.id")                    // MAIL FROM
 *   $s.to("satu@example.id")                       // RCPT TO (boleh berulang)
 *   $s.to("dua@example.id")
 *   $s.subject("Halo dari Garurda")
 *   $s.send("Halo.\nBaris.")                       // DATA text/plain
 *   $s.send_html("Halo", "<p>Halo <b>dunia</b></p>") // multipart/alternative
 *   $s.close()
 *
 * State: tabel sesi maksimal 64 slot digantungkan lewat api->set_data
 * (aman bila satu .so dipakai beberapa interpreter); tiap objek sesi
 * menyimpan field "id" → slot. Gagal I/O menutup sesi sehingga method
 * berikutnya melempar "koneksi sudah ditutup atau tidak valid".
 *
 * Setelah MAIL FROM (from()), transaksi aktif sampai DATA selesai;
 * kirim email berikutnya wajib from() + to() ulang (envelope server
 * reset setelah end-of-data). subject() bertahan antar transaksi.
 *
 * Galat catchable "smtp_error":
 *   status 500 — server menolak / protokol tak terduga / state salah
 *                (mis. send() sebelum from()) / argumen tidak valid
 *   status 502 — gagal terhubung / koneksi terputus
 *   status 504 — waktu tunggu habis (timeout)
 *
 * BATASAN (jujur, tertulis juga di docs):
 *   - Plain tanpa TLS. STARTTLS/OpenSSL BELUM didukung — server yang
 *     mewajibkan TLS sebelum AUTH akan menolak (pesannya diteruskan
 *     apa adanya). Jangan pakai jalur ini di jaringan publik.
 *   - Mekanisme AUTH hanya LOGIN (bukan PLAIN/CRAM-MD5).
 *   - Alamat hanya-ASCII (tanpa SMTPUTF8); subjek non-ASCII otomatis
 *     dikodekan sebagai encoded-word RFC 2047 (?=UTF-8?B?...?=).
 *   - Body tidak di-fold: baris body >1000 oktet bisa ditolak server
 *     dengan batas line_length ketat (Postfix default 2000).
 *   - NUL dalam pesan ditolak (type_error) — melanggar SMTP.
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
#include <time.h>

#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
#if defined(_MSC_VER)
#pragma comment(lib, "ws2_32.lib")
#endif
typedef SOCKET sfd_t;
#define SFD_INVALID INVALID_SOCKET
#define sfd_close closesocket
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
typedef int sfd_t;
#define SFD_INVALID (-1)
#define sfd_close close
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
#define SMTP_MAX_CONN 64
#define SMTP_MAX_RCPT 100
#define SMTP_TIMEOUT_DEFAULT_MS 5000
#define SMTP_QUIT_WAIT_MS 2000      /* batas tunggu balasan 221 saat close() */
#define SMTP_MAX_LINE (1u << 20)    /* baris protokol ≤ 1 MiB */
#define SMTP_MAX_REPLY (64u << 10)  /* total satu balasan multi-baris ≤ 64 KiB */
#define SMTP_MAX_MSG (32u << 20)    /* pesan keluar ≤ 32 MiB */
#define SMTP_MAX_ADDR 320           /* path RFC 5321 + sudut */
#define SMTP_MAX_SUBJECT 700        /* subjek — encoded-word tetap < 998 oktet */
#define SMTP_CAPS 8192              /* ruang simpan balasan EHLO */

typedef struct {
	int fd; /* -1 = slot kosong */
	int timeout_ms;
	int64_t gen; /* nomor generasi — menolak objek basi dari siklus close/connect */
	char *rbuf;
	size_t rcap, rlen, rpos;
	char me[256];   /* nama host klien (EHLO + Message-ID) */
	char caps[SMTP_CAPS]; /* teks balasan EHLO, huruf besar-kecil diseragamkan */
	int in_txn;     /* MAIL FROM aktif, menunggu DATA */
	char *subject;  /* subjek tersimpan (bertahan antar transaksi) */
	char *from_addr; /* alamat MAIL FROM terakhir */
	char *rcpt[SMTP_MAX_RCPT];
	int nrcpt;
	unsigned seq; /* nomor urut untuk Message-ID & boundary */
} smtp_sess;

typedef struct {
	smtp_sess ss[SMTP_MAX_CONN];
} smtp_state;

/* Hasil membaca buffer. */
enum {
	BUF_OK = 0,
	BUF_EOF = -1,     /* server menutup koneksi */
	BUF_TIMEOUT = -2, /* SO_RCVTIMEO tercapai */
	BUF_ERR = -3,     /* galat socket lain */
	BUF_TOOLARGE = -4 /* melewati batas ukuran */
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

static int buf_grow(smtp_sess *s, size_t need)
{
	size_t cap;
	char *nb;
	if (need <= s->rcap)
		return BUF_OK;
	if (need > SMTP_MAX_LINE * 2)
		return BUF_TOOLARGE;
	cap = s->rcap ? s->rcap : 4096;
	while (cap < need)
		cap *= 2;
	if (cap > SMTP_MAX_LINE * 2)
		cap = SMTP_MAX_LINE * 2;
	nb = realloc(s->rbuf, cap);
	if (!nb)
		return BUF_ERR;
	s->rbuf = nb;
	s->rcap = cap;
	return BUF_OK;
}

/* Pindahkan data belum terpakai ke awal buffer. */
static void buf_compact(smtp_sess *s)
{
	if (s->rpos == 0)
		return;
	if (s->rlen > s->rpos)
		memmove(s->rbuf, s->rbuf + s->rpos, s->rlen - s->rpos);
	s->rlen -= s->rpos;
	s->rpos = 0;
}

/* Terima minimal satu byte lagi (setelah compact bila perlu). */
static int buf_refill(smtp_sess *s)
{
	int rc;
	if (s->rpos > 0) {
		if (s->rlen == s->rpos) {
			s->rlen = 0;
			s->rpos = 0;
		} else {
			buf_compact(s);
		}
	}
	if (s->rlen == s->rcap) {
		rc = buf_grow(s, s->rcap ? s->rcap * 2 : 4096);
		if (rc != BUF_OK)
			return rc;
	}
	for (;;) {
		int k;
#ifdef _WIN32
		k = (int)recv(s->fd, s->rbuf + s->rlen, (int)(s->rcap - s->rlen), 0);
		if (k > 0) {
			s->rlen += (size_t)k;
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
			ssize_t n = recv(s->fd, s->rbuf + s->rlen, s->rcap - s->rlen, 0);
			k = (n < 0) ? -1 : (n == 0 ? 0 : 1);
			if (n > 0)
				s->rlen += (size_t)n;
		}
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

/* Baca satu baris protokol (tanpa CRLF). *out menunjuk ke dalam buffer —
 * SAH hanya sampai operasi baca berikutnya. */
static int buf_line(smtp_sess *s, const char **out, size_t *outlen)
{
	for (;;) {
		size_t i;
		for (i = s->rpos; i < s->rlen; i++) {
			if (s->rbuf[i] == '\n') {
				size_t len = i - s->rpos;
				if (len > 0 && s->rbuf[s->rpos + len - 1] == '\r')
					len--;
				*out = s->rbuf + s->rpos;
				*outlen = len;
				s->rpos = i + 1;
				return BUF_OK;
			}
		}
		if (s->rlen - s->rpos > SMTP_MAX_LINE)
			return BUF_TOOLARGE;
		{
			int rc = buf_refill(s);
			if (rc != BUF_OK)
				return rc;
		}
	}
}

/* ---- galat I/O ---- */

static int fail_io(gne_ctx *ctx, smtp_sess *s, int rc, const char *what)
{
	char buf[256];
	if (rc == BUF_TIMEOUT) {
		snprintf(buf, sizeof buf, "%s: waktu tunggu %d ms habis", what, s->timeout_ms);
		api->throw(ctx, "smtp_error", 504, buf);
	} else if (rc == BUF_EOF) {
		snprintf(buf, sizeof buf, "%s: server menutup koneksi", what);
		api->throw(ctx, "smtp_error", 502, buf);
	} else if (rc == BUF_TOOLARGE) {
		snprintf(buf, sizeof buf, "%s: balasan melewati batas ukuran", what);
		api->throw(ctx, "smtp_error", 500, buf);
	} else {
		int e = SOCK_ERRNO;
		char txt[96];
		snprintf(buf, sizeof buf, "%s: %s", what, sock_text(e, txt, sizeof txt));
		api->throw(ctx, "smtp_error", 502, buf);
	}
	return -1;
}

/* ---- kumpulan argumen string ---- */

/* Konversi satu handle menjadi string; galat tipe → throw. */
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
			api->throw(ctx, "smtp_oom", 500, "gagal mengalokasikan memori");
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
			api->throw(ctx, "smtp_oom", 500, "gagal mengalokasikan memori");
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
			api->throw(ctx, "smtp_oom", 500, "gagal mengalokasikan memori");
			return NULL;
		}
		*len = strlen(tmp);
		return out;
	}
	default:
		api->throw(ctx, "type_error", 500, "argumen harus berupa string atau angka");
		return NULL;
	}
}

/* ---- buffer tulis ---- */

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

static int send_all(smtp_sess *s, const char *p, size_t n)
{
	while (n > 0) {
#ifdef _WIN32
		int chunk = n > 0x7fffffffu ? 0x7fffffff : (int)n;
		int k = send(s->fd, p, chunk, 0);
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
		ssize_t k = send(s->fd, p, n, MSG_NOSIGNAL);
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
		return -1;
	}
	return 0;
}

/* ---- sesi ---- */

/* Cari sesi dari self objek; galat bila basi/tertutup. */
static smtp_sess *self_sess(gne_ctx *ctx, gne_handle self)
{
	smtp_state *st = api->get_data(ctx);
	gne_handle h;
	int64_t id, gen;
	if (!st) {
		api->throw(ctx, "smtp_error", 500, "state modul SMTP hilang");
		return NULL;
	}
	if (api->obj_get(ctx, self, "id", &h) != 0 ||
	    api->get_int(ctx, h, &id) != 0 ||
	    id < 0 || id >= SMTP_MAX_CONN ||
	    api->obj_get(ctx, self, "gen", &h) != 0 ||
	    api->get_int(ctx, h, &gen) != 0 ||
	    st->ss[id].fd < 0 || st->ss[id].gen != gen) {
		api->throw(ctx, "smtp_error", 500, "koneksi sudah ditutup atau tidak valid");
		return NULL;
	}
	return &st->ss[id];
}

static void sess_clear(smtp_sess *s)
{
	int i;
	free(s->rbuf);
	s->rbuf = NULL;
	s->rcap = s->rlen = s->rpos = 0;
	free(s->subject);
	s->subject = NULL;
	free(s->from_addr);
	s->from_addr = NULL;
	for (i = 0; i < s->nrcpt; i++) {
		free(s->rcpt[i]);
		s->rcpt[i] = NULL;
	}
	s->nrcpt = 0;
	s->in_txn = 0;
	s->caps[0] = 0;
	s->seq = 0;
}

/* Tutup paksa sesi (dipakai setelah galat I/O): koneksi dianggap rusak,
 * method berikutnya melempar "sudah ditutup". */
static void sess_drop(smtp_sess *s)
{
	if (s->fd >= 0)
		sfd_close(s->fd);
	s->fd = -1;
	sess_clear(s);
}

static void set_timeouts(sfd_t fd, int ms)
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
static int conn_dial(sfd_t fd, const struct sockaddr *sa, socklen_t slen, int timeout_ms)
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
static int slot_free(smtp_state *st)
{
	int i;
	for (i = 0; i < SMTP_MAX_CONN; i++)
		if (st->ss[i].fd < 0)
			return i;
	return -1;
}

/* ---- kirim perintah + baca balasan ---- */

/* Kirim satu baris perintah (tanpa CRLF) + CRLF. Gagal → tutup sesi + throw. */
static int cmd_line(gne_ctx *ctx, smtp_sess *s, const char *line)
{
	sbuf out = { NULL, 0, 0 };
	int ok;
	if (sb_puts(&out, line) != 0 || sb_add(&out, "\r\n", 2) != 0) {
		free(out.b);
		sess_drop(s);
		api->throw(ctx, "smtp_oom", 500, "gagal mengalokasikan memori");
		return -1;
	}
	ok = send_all(s, out.b, out.len) == 0;
	free(out.b);
	if (!ok) {
		int e = SOCK_ERRNO;
		char buf[256], txt[96];
		sess_drop(s);
		if (is_timeout_err(e))
			snprintf(buf, sizeof buf, "mengirim perintah: waktu tunggu %d ms habis", s->timeout_ms);
		else
			snprintf(buf, sizeof buf, "mengirim perintah: %s", sock_text(e, txt, sizeof txt));
		api->throw(ctx, "smtp_error", is_timeout_err(e) ? 504 : 502, buf);
		return -1;
	}
	return 0;
}

/* Baca satu balasan multi-baris (250-... / 250 ...). *code = kode resmi;
 * *msg = teks semua baris (kode dibuang), diakhiri NUL.
 * 0 = sukses; -1 = gagal. Gagal I/O / protokol = stream tidak dapat
 * dipercaya lagi sehingga sesi ikut dibuang.
 * quiet=1: jangan melempar galat apa pun (dipakai QUIT saat close()). */
static int read_reply_ex(gne_ctx *ctx, smtp_sess *s, int *code, char *msg,
			 size_t msgcap, int quiet)
{
	size_t total = 0;
	int first = 1;
	msg[0] = 0;
	for (;;) {
		const char *line;
		size_t linelen, k;
		int rc = buf_line(s, &line, &linelen);
		if (rc != BUF_OK) {
			if (!quiet)
				fail_io(ctx, s, rc, "membaca balasan SMTP");
			sess_drop(s);
			return -1;
		}
		if (linelen < 3 ||
		    line[0] < '0' || line[0] > '9' ||
		    line[1] < '0' || line[1] > '9' ||
		    line[2] < '0' || line[2] > '9') {
			if (!quiet)
				api->throw(ctx, "smtp_error", 500, "balasan SMTP tidak terduga (bukan kode 3 digit)");
			sess_drop(s);
			return -1;
		}
		if (first) {
			*code = (line[0] - '0') * 100 + (line[1] - '0') * 10 + (line[2] - '0');
			first = 0;
		}
		total += linelen;
		if (total > SMTP_MAX_REPLY) {
			if (!quiet)
				api->throw(ctx, "smtp_error", 500, "balasan SMTP terlalu besar");
			sess_drop(s);
			return -1;
		}
		/* Teks setelah "NNN " atau "NNN-" (kosong bila baris hanya kode). */
		if (linelen > 4) {
			size_t n = linelen - 4;
			size_t used = strlen(msg);
			if (used + n + 2 >= msgcap) {
				/* Truncate: cukup untuk pesan galat & cek kemampuan. */
				k = msgcap - used - 2;
				if (k > 0) {
					memcpy(msg + used, line + 4, k);
					msg[used + k] = '\n';
					msg[used + k + 1] = 0;
				}
			} else {
				memcpy(msg + used, line + 4, n);
				msg[used + n] = '\n';
				msg[used + n + 1] = 0;
			}
		}
		if (linelen < 4 || line[3] == ' ')
			return 0; /* baris penutup */
		if (line[3] != '-') {
			if (!quiet)
				api->throw(ctx, "smtp_error", 500, "balasan SMTP tidak terduga (pemisah bukan '-' atau ' ')");
			sess_drop(s);
			return -1;
		}
		/* lanjut: baris lanjutan "NNN-..." */
	}
}

/* Varian berisik (lempar galat sesuai kebutuhan). */
static int read_reply(gne_ctx *ctx, smtp_sess *s, int *code, char *msg, size_t msgcap)
{
	return read_reply_ex(ctx, s, code, msg, msgcap, 0);
}

/* Baca balasan lalu cocokkan dengan satu kode yang diharapkan. */
static int expect_code(gne_ctx *ctx, smtp_sess *s, int want, const char *what)
{
	int code;
	char msg[512];
	if (read_reply(ctx, s, &code, msg, sizeof msg) != 0)
		return -1;
	if (code == want)
		return 0;
	{
		char buf[640];
		size_t n = strlen(msg);
		while (n > 0 && (msg[n - 1] == '\n' || msg[n - 1] == ' '))
			msg[--n] = 0;
		snprintf(buf, sizeof buf, "%s: server membalas %d %s", what, code, msg);
		api->throw(ctx, "smtp_error", 500, buf);
	}
	return -1;
}

/* Baca balasan, terima satu dari dua kode (mis. RCPT: 250/251). */
static int expect_code2(gne_ctx *ctx, smtp_sess *s, int want, int want2, const char *what)
{
	int code;
	char msg[512];
	if (read_reply(ctx, s, &code, msg, sizeof msg) != 0)
		return -1;
	if (code == want || code == want2)
		return 0;
	{
		char buf[640];
		size_t n = strlen(msg);
		while (n > 0 && (msg[n - 1] == '\n' || msg[n - 1] == ' '))
			msg[--n] = 0;
		snprintf(buf, sizeof buf, "%s: server membalas %d %s", what, code, msg);
		api->throw(ctx, "smtp_error", 500, buf);
	}
	return -1;
}

/* ---- base64 (AUTH LOGIN & Content-Transfer-Encoding) ---- */

static const char b64tab[] =
	"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/* Tulis base64 ke sbuf. wrap>0: sisipkan CRLF setelah tiap `wrap` karakter
 * (baris selesai tidak diakhiri CRLF di sini — pemanggil mengatur). */
static int b64_write(sbuf *out, const unsigned char *in, size_t n, unsigned wrap)
{
	size_t i = 0;
	unsigned col = 0;
	while (i + 3 <= n) {
		unsigned v = ((unsigned)in[i] << 16) | ((unsigned)in[i + 1] << 8) | in[i + 2];
		char q[4];
		q[0] = b64tab[(v >> 18) & 63];
		q[1] = b64tab[(v >> 12) & 63];
		q[2] = b64tab[(v >> 6) & 63];
		q[3] = b64tab[v & 63];
		if (wrap && col + 4 > wrap) {
			if (sb_add(out, "\r\n", 2) != 0)
				return -1;
			col = 0;
		}
		if (sb_add(out, q, 4) != 0)
			return -1;
		col += 4;
		i += 3;
	}
	if (n - i == 1) {
		unsigned v = (unsigned)in[i] << 16;
		char q[4];
		q[0] = b64tab[(v >> 18) & 63];
		q[1] = b64tab[(v >> 12) & 63];
		q[2] = '=';
		q[3] = '=';
		if (wrap && col + 4 > wrap) {
			if (sb_add(out, "\r\n", 2) != 0)
				return -1;
			col = 0;
		}
		if (sb_add(out, q, 4) != 0)
			return -1;
	} else if (n - i == 2) {
		unsigned v = ((unsigned)in[i] << 16) | ((unsigned)in[i + 1] << 8);
		char q[4];
		q[0] = b64tab[(v >> 18) & 63];
		q[1] = b64tab[(v >> 12) & 63];
		q[2] = b64tab[(v >> 6) & 63];
		q[3] = '=';
		if (wrap && col + 4 > wrap) {
			if (sb_add(out, "\r\n", 2) != 0)
				return -1;
			col = 0;
		}
		if (sb_add(out, q, 4) != 0)
			return -1;
	}
	return 0;
}

/* ---- utilitas teks ---- */

/* Ada substring? (memmem tidak portabel di Windows) */
static int has_substr(const char *h, size_t hlen, const char *n)
{
	size_t nl = strlen(n), i;
	if (nl == 0 || hlen < nl)
		return 0;
	for (i = 0; i + nl <= hlen; i++)
		if (memcmp(h + i, n, nl) == 0)
			return 1;
	return 0;
}

/* ---- validasi argumen ---- */

/* Alamat envelope: ASCII cetak 1–320 oktet, tanpa ruang/kontrol/koma/sudut
 * (mencegah injeksi baris & argumen perintah). */
static int addr_ok(const char *a, size_t n)
{
	size_t i;
	if (n == 0 || n >= SMTP_MAX_ADDR)
		return 0;
	for (i = 0; i < n; i++) {
		unsigned char c = (unsigned char)a[i];
		if (c <= 0x20 || c >= 0x7f)
			return 0;
		if (c == '<' || c == '>' || c == ',' || c == ';')
			return 0;
	}
	return 1;
}

/* Subjek: tanpa kontrol CR/LF/NUL (anti injeksi header). Batas 700 oktet
 * membuat encoded-word non-ASCII tetap di bawah 998 oktet per baris. */
static int subject_ok(const char *a, size_t n)
{
	size_t i;
	if (n > SMTP_MAX_SUBJECT)
		return 0;
	for (i = 0; i < n; i++)
		if ((unsigned char)a[i] < 0x20)
			return 0;
	return 1;
}

/* Isi pesan bebas NUL (SMTP melarang NUL dalam mail data). */
static int body_check(gne_ctx *ctx, const char *p, size_t n, const char *what)
{
	if (memchr(p, 0, n) != NULL) {
		char buf[160];
		snprintf(buf, sizeof buf, "%s mengandung karakter NUL yang dilarang SMTP", what);
		api->throw(ctx, "type_error", 500, buf);
		return -1;
	}
	return 0;
}

/* ---- kemampuan & pilihan CTE ---- */

static int caps_has(const smtp_sess *s, const char *up)
{
	return strstr(s->caps, up) != NULL;
}

static int has_highbit(const char *p, size_t n)
{
	size_t i;
	for (i = 0; i < n; i++)
		if ((unsigned char)p[i] >= 0x80)
			return 1;
	return 0;
}

/* Pilih Content-Transfer-Encoding: 7bit untuk ASCII murni; 8bit bila server
 * mengiklankan 8BITMIME; selain itu base64 (selalu sah di mana pun). */
static const char *pick_cte(const smtp_sess *s, const char *body, size_t n, int *use_b64)
{
	*use_b64 = 0;
	if (!has_highbit(body, n))
		return "7bit";
	if (caps_has(s, "8BITMIME"))
		return "8bit";
	*use_b64 = 1;
	return "base64";
}

/* ---- tanggal RFC 5322 (UTC; nama hari/bulan Inggris manual, tanpa
 * dependensi locale) ---- */

static void fmt_date(char *out, size_t cap)
{
	static const char *const wd[7] = { "Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat" };
	static const char *const mo[12] = {
		"Jan", "Feb", "Mar", "Apr", "May", "Jun",
		"Jul", "Aug", "Sep", "Oct", "Nov", "Dec"
	};
	int64_t now = (int64_t)time(NULL);
	int64_t days = now / 86400;
	int64_t rem = now - days * 86400;
	int64_t era, doe, yoe, yy, doy, mp;
	int y, m, d, w;
	if (rem < 0) { /* pembagian lantai buat waktu sebelum 1970 */
		rem += 86400;
		days--;
	}
	w = (int)(((days + 4) % 7 + 7) % 7); /* 1970-01-01 = Kamis (indeks 4) */
	{ /* hari sipil → y/m/d (algoritma civil_from_days) */
		int64_t z = days + 719468;
		era = (z >= 0 ? z : z - 146096) / 146097;
		doe = z - era * 146097;                             /* [0, 146096] */
		yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
		yy = yoe + era * 400;
		doy = doe - (365 * yoe + yoe / 4 - yoe / 100);      /* [0, 364] */
		mp = (5 * doy + 2) / 153;                           /* [0, 11] */
		d = (int)(doy - (153 * mp + 2) / 5 + 1);            /* [1, 31] */
		m = (int)(mp + (mp < 10 ? 3 : -9));                 /* [1, 12] */
		y = (int)(yy + (m <= 2 ? 1 : 0));
	}
	snprintf(out, cap, "%s, %02d %s %04d %02d:%02d:%02d +0000",
		 wd[w], d, mo[m - 1], y,
		 (int)(rem / 3600), (int)(rem / 60 % 60), (int)(rem % 60));
}

/* ---- penyusun konten DATA ---- */

static int put_hdr_n(sbuf *out, const char *key, const char *val, size_t vlen)
{
	if (sb_puts(out, key) != 0 || sb_add(out, ": ", 2) != 0 ||
	    sb_add(out, val, vlen) != 0 || sb_add(out, "\r\n", 2) != 0)
		return -1;
	return 0;
}

static int put_hdr(sbuf *out, const char *key, const char *val)
{
	return put_hdr_n(out, key, val, strlen(val));
}

/* Baris pemisah bagian multipart: "--" + boundary + tail ("", "/--") + CRLF. */
static int put_boundary(sbuf *out, const char *bnd, const char *tail)
{
	if (sb_puts(out, "--") != 0 || sb_puts(out, bnd) != 0 ||
	    sb_puts(out, tail) != 0 || sb_add(out, "\r\n", 2) != 0)
		return -1;
	return 0;
}

/* Tulis isi pesan: normalisasi akhir baris (\r\n, \n, \r tunggal → \r\n)
 * dan dot-stuffing (baris diawali "." menjadi ".."). Baris terakhir selalu
 * diakhiri CRLF; input kosong menghasilkan tulisan kosong. */
static int put_body(sbuf *out, const char *p, size_t n)
{
	size_t i, start = 0;
	for (i = 0; i < n; i++) {
		char c = p[i];
		if (c != '\r' && c != '\n')
			continue;
		if (start < i && p[start] == '.' && sb_add(out, ".", 1) != 0)
			return -1;
		if (i > start && sb_add(out, p + start, i - start) != 0)
			return -1;
		if (sb_add(out, "\r\n", 2) != 0)
			return -1;
		if (c == '\r' && i + 1 < n && p[i + 1] == '\n')
			i++; /* lewati LF pasangan CRLF */
		start = i + 1;
	}
	if (start < n) { /* baris terakhir tanpa terminator */
		if (p[start] == '.' && sb_add(out, ".", 1) != 0)
			return -1;
		if (sb_add(out, p + start, n - start) != 0)
			return -1;
		if (sb_add(out, "\r\n", 2) != 0)
			return -1;
	}
	return 0;
}

/* Tulis badan pesan sesuai CTE terpilih (mentah / base64). */
static int put_payload(sbuf *content, const char *body, size_t n, int use_b64)
{
	if (!use_b64)
		return put_body(content, body, n);
	if (n == 0)
		return 0;
	if (b64_write(content, (const unsigned char *)body, n, 76) != 0)
		return -1;
	return sb_add(content, "\r\n", 2);
}

/* Headers standar untuk DATA. ctype wajib; cte boleh NULL (multipart:
 * Content-Transfer-Encoding ditulis per bagian). */
static int put_std_headers(sbuf *content, smtp_sess *s,
			   const char *ctype, const char *cte)
{
	char date[64], mid[160];
	int i;
	fmt_date(date, sizeof date);
	if (put_hdr(content, "Date", date) != 0)
		return -1;
	if (s->from_addr && put_hdr(content, "From", s->from_addr) != 0)
		return -1;
	if (s->nrcpt > 0) { /* To: dilipat di koma bila baris melebihi 78 oktet */
		size_t linelen;
		if (sb_puts(content, "To: ") != 0)
			return -1;
		linelen = 4;
		for (i = 0; i < s->nrcpt; i++) {
			size_t rl = strlen(s->rcpt[i]);
			if (i > 0) {
				if (linelen + 2 + rl > 78) {
					if (sb_add(content, ",\r\n ", 4) != 0)
						return -1;
					linelen = 1;
				} else {
					if (sb_puts(content, ", ") != 0)
						return -1;
					linelen += 2;
				}
			}
			if (sb_puts(content, s->rcpt[i]) != 0)
				return -1;
			linelen += rl;
		}
		if (sb_add(content, "\r\n", 2) != 0)
			return -1;
	}
	if (s->subject) {
		size_t slen = strlen(s->subject);
		if (has_highbit(s->subject, slen)) {
			/* Non-ASCII → encoded-word RFC 2047 (base64). */
			sbuf ew = { NULL, 0, 0 };
			if (sb_puts(&ew, "=?UTF-8?B?") != 0 ||
			    b64_write(&ew, (const unsigned char *)s->subject, slen, 0) != 0 ||
			    sb_puts(&ew, "?=") != 0) {
				free(ew.b);
				return -1;
			}
			if (put_hdr_n(content, "Subject", ew.b, ew.len) != 0) {
				free(ew.b);
				return -1;
			}
			free(ew.b);
		} else if (put_hdr_n(content, "Subject", s->subject, slen) != 0) {
			return -1;
		}
	}
	snprintf(mid, sizeof mid, "<%lld.%u@%.100s>",
		 (long long)time(NULL), ++s->seq, s->me);
	if (put_hdr(content, "Message-ID", mid) != 0)
		return -1;
	if (put_hdr(content, "MIME-Version", "1.0") != 0)
		return -1;
	if (put_hdr(content, "Content-Type", ctype) != 0)
		return -1;
	if (cte && put_hdr(content, "Content-Transfer-Encoding", cte) != 0)
		return -1;
	return 0;
}

/* Kirim potongan data apa adanya. Gagal → tutup sesi + throw. */
static int send_blob(gne_ctx *ctx, smtp_sess *s, const char *p, size_t n, const char *what)
{
	if (send_all(s, p, n) == 0)
		return 0;
	{
		int e = SOCK_ERRNO;
		char buf[320], txt[96];
		if (is_timeout_err(e))
			snprintf(buf, sizeof buf, "%s: waktu tunggu %d ms habis", what, s->timeout_ms);
		else
			snprintf(buf, sizeof buf, "%s: %s", what, sock_text(e, txt, sizeof txt));
		sess_drop(s);
		api->throw(ctx, "smtp_error", is_timeout_err(e) ? 504 : 502, buf);
	}
	return -1;
}

/* Terminasi konten (pastikan CRLF, lalu "." CRLF), kirim, baca 250, dan
 * reset envelope. Konten diubah tapi tetap milik pemanggil.
 * Gagal I/O/protokol → sesi sudah dibuang pembaca balasan. */
static int data_transaction(gne_ctx *ctx, smtp_sess *s, sbuf *content)
{
	int code;
	char msg[512];
	if (content->len < 2 ||
	    content->b[content->len - 2] != '\r' || content->b[content->len - 1] != '\n') {
		if (sb_add(content, "\r\n", 2) != 0)
			goto oom;
	}
	if (sb_add(content, ".\r\n", 3) != 0)
		goto oom;
	if (send_blob(ctx, s, content->b, content->len, "mengirim isi pesan") != 0)
		return -1;
	if (read_reply(ctx, s, &code, msg, sizeof msg) != 0)
		return -1;
	/* Apa pun hasilnya, envelope server sudah mengonsumsi DATA. */
	s->in_txn = 0;
	while (s->nrcpt > 0) {
		s->nrcpt--;
		free(s->rcpt[s->nrcpt]);
		s->rcpt[s->nrcpt] = NULL;
	}
	if (code != 250) {
		char buf[640];
		size_t n = strlen(msg);
		while (n > 0 && (msg[n - 1] == '\n' || msg[n - 1] == ' '))
			msg[--n] = 0;
		snprintf(buf, sizeof buf, "server menolak isi pesan: %d %s", code, msg);
		api->throw(ctx, "smtp_error", 500, buf);
		return -1;
	}
	return 0;
oom:
	sess_drop(s);
	api->throw(ctx, "smtp_oom", 500, "gagal mengalokasikan memori");
	return -1;
}

/* ---- method ---- */

/* auth(user, pass) → true (235). Mekanisme AUTH LOGIN. */
static int m_auth(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	smtp_sess *s = self_sess(ctx, argv[0]);
	char *user = NULL, *pass = NULL;
	size_t ulen = 0, plen = 0;
	int rc = -1;
	(void)argc;
	if (!s)
		return -1;
	user = arg_to_str(ctx, argv[1], &ulen);
	if (!user)
		return -1;
	pass = arg_to_str(ctx, argv[2], &plen);
	if (!pass) {
		free(user);
		return -1;
	}
	if (s->caps[0] == 0) {
		api->throw(ctx, "smtp_error", 500,
			   "AUTH tidak tersedia: server tidak mendukung EHLO (kemungkinan perlu TLS — STARTTLS belum didukung)");
		goto out;
	}
	if (!caps_has(s, "AUTH") || !caps_has(s, "LOGIN")) {
		api->throw(ctx, "smtp_error", 500,
			   "server tidak melaporkan AUTH LOGIN pada balasan EHLO (klien ini hanya punya mekanisme LOGIN)");
		goto out;
	}
	if (cmd_line(ctx, s, "AUTH LOGIN") != 0)
		goto out;
	if (expect_code(ctx, s, 334, "AUTH LOGIN") != 0)
		goto out;
	{ /* nama pengguna (base64 satu baris) */
		sbuf b = { NULL, 0, 0 };
		if (b64_write(&b, (const unsigned char *)user, ulen, 0) != 0 ||
		    sb_add(&b, "\r\n", 2) != 0) {
			free(b.b);
			sess_drop(s); /* server menunggu baris data — putus */
			api->throw(ctx, "smtp_oom", 500, "gagal mengalokasikan memori");
			goto out;
		}
		if (send_blob(ctx, s, b.b, b.len, "mengirim nama pengguna") != 0) {
			free(b.b);
			goto out;
		}
		free(b.b);
	}
	if (expect_code(ctx, s, 334, "AUTH LOGIN (nama pengguna)") != 0)
		goto out;
	{ /* kata sandi */
		sbuf b = { NULL, 0, 0 };
		if (b64_write(&b, (const unsigned char *)pass, plen, 0) != 0 ||
		    sb_add(&b, "\r\n", 2) != 0) {
			free(b.b);
			sess_drop(s);
			api->throw(ctx, "smtp_oom", 500, "gagal mengalokasikan memori");
			goto out;
		}
		if (send_blob(ctx, s, b.b, b.len, "mengirim kata sandi") != 0) {
			free(b.b);
			goto out;
		}
		free(b.b);
	}
	if (expect_code(ctx, s, 235, "AUTH LOGIN (kata sandi)") != 0)
		goto out;
	*ret = api->bool_new(ctx, 1);
	rc = 0;
out:
	free(user);
	free(pass);
	return rc;
}

/* from(alamat) → true. Memulai transaksi MAIL; transaksi lama di-reset
 * dulu dengan RSET agar urutan pakai tetap ramah. */
static int m_from(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	smtp_sess *s = self_sess(ctx, argv[0]);
	char *addr = NULL;
	size_t alen = 0;
	char line[SMTP_MAX_ADDR + 32];
	int rc = -1;
	(void)argc;
	if (!s)
		return -1;
	addr = arg_to_str(ctx, argv[1], &alen);
	if (!addr)
		return -1;
	if (!addr_ok(addr, alen)) {
		api->throw(ctx, "type_error", 500,
			   "alamat tidak valid (ASCII cetak 1–320 oktet, tanpa ruang dan '<>,')");
		goto out;
	}
	if (s->in_txn) {
		if (cmd_line(ctx, s, "RSET") != 0)
			goto out;
		if (expect_code(ctx, s, 250, "RSET") != 0)
			goto out;
		s->in_txn = 0;
	}
	snprintf(line, sizeof line, "MAIL FROM:<%s>", addr);
	if (cmd_line(ctx, s, line) != 0)
		goto out;
	if (expect_code(ctx, s, 250, "MAIL FROM") != 0)
		goto out;
	while (s->nrcpt > 0) {
		s->nrcpt--;
		free(s->rcpt[s->nrcpt]);
		s->rcpt[s->nrcpt] = NULL;
	}
	s->in_txn = 1;
	free(s->from_addr);
	s->from_addr = strdup(addr);
	if (!s->from_addr) {
		api->throw(ctx, "smtp_oom", 500, "gagal mengalokasikan memori");
		goto out;
	}
	*ret = api->bool_new(ctx, 1);
	rc = 0;
out:
	free(addr);
	return rc;
}

/* to(alamat) → true. Menambah penerima (RCPT TO, boleh berulang). */
static int m_to(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	smtp_sess *s = self_sess(ctx, argv[0]);
	char *addr = NULL;
	size_t alen = 0;
	char line[SMTP_MAX_ADDR + 32];
	int rc = -1;
	(void)argc;
	if (!s)
		return -1;
	addr = arg_to_str(ctx, argv[1], &alen);
	if (!addr)
		return -1;
	if (!addr_ok(addr, alen)) {
		api->throw(ctx, "type_error", 500,
			   "alamat tidak valid (ASCII cetak 1–320 oktet, tanpa ruang dan '<>,')");
		goto out;
	}
	if (!s->in_txn) {
		api->throw(ctx, "smtp_error", 500, "panggil from() dulu sebelum to()");
		goto out;
	}
	if (s->nrcpt >= SMTP_MAX_RCPT) {
		api->throw(ctx, "smtp_error", 500,
			   "terlalu banyak penerima (maksimal 100 per transaksi)");
		goto out;
	}
	snprintf(line, sizeof line, "RCPT TO:<%s>", addr);
	if (cmd_line(ctx, s, line) != 0)
		goto out;
	if (expect_code2(ctx, s, 250, 251, "RCPT TO") != 0)
		goto out;
	s->rcpt[s->nrcpt] = strdup(addr);
	if (!s->rcpt[s->nrcpt]) {
		api->throw(ctx, "smtp_oom", 500, "gagal mengalokasikan memori");
		goto out;
	}
	s->nrcpt++;
	*ret = api->bool_new(ctx, 1);
	rc = 0;
out:
	free(addr);
	return rc;
}

/* subject(teks) → true. Menyimpan subjek untuk send() berikutnya
 * (bertahan antar transaksi; tidak menyentuh protokol). */
static int m_subject(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	smtp_sess *s = self_sess(ctx, argv[0]);
	char *txt = NULL;
	size_t tlen = 0;
	(void)argc;
	if (!s)
		return -1;
	txt = arg_to_str(ctx, argv[1], &tlen);
	if (!txt)
		return -1;
	if (!subject_ok(txt, tlen)) {
		api->throw(ctx, "type_error", 500,
			   "subjek tidak valid (tanpa kontrol CR/LF, maksimal 700 oktet)");
		free(txt);
		return -1;
	}
	free(s->subject);
	s->subject = strdup(txt); /* gagal = tanpa subjek; keadaan tetap konsisten */
	free(txt);
	if (!s->subject) {
		api->throw(ctx, "smtp_oom", 500, "gagal mengalokasikan memori");
		return -1;
	}
	*ret = api->bool_new(ctx, 1);
	return 0;
}

/* send(teks) → true. DATA text/plain. */
static int m_send(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	smtp_sess *s = self_sess(ctx, argv[0]);
	char *body = NULL;
	size_t blen = 0;
	sbuf content = { NULL, 0, 0 };
	const char *cte;
	int use_b64, rc = -1;
	(void)argc;
	if (!s)
		return -1;
	if (!s->in_txn || s->nrcpt == 0) {
		api->throw(ctx, "smtp_error", 500,
			   "transaksi belum siap — panggil from() lalu to() minimal sekali sebelum send()");
		return -1;
	}
	body = arg_to_str(ctx, argv[1], &blen);
	if (!body)
		return -1;
	if (body_check(ctx, body, blen, "isi pesan") != 0)
		goto out;
	if (blen > SMTP_MAX_MSG) {
		api->throw(ctx, "smtp_error", 500, "isi pesan melewati batas 32 MiB");
		goto out;
	}
	cte = pick_cte(s, body, blen, &use_b64);
	if (cmd_line(ctx, s, "DATA") != 0)
		goto out; /* sesi sudah dibuang */
	if (expect_code(ctx, s, 354, "DATA") != 0)
		goto out; /* ditolak: stream masih sinkron */
	if (put_std_headers(&content, s, "text/plain; charset=utf-8", cte) != 0 ||
	    sb_add(&content, "\r\n", 2) != 0 ||
	    put_payload(&content, body, blen, use_b64) != 0)
		goto oom;
	if (data_transaction(ctx, s, &content) != 0)
		goto out;
	*ret = api->bool_new(ctx, 1);
	rc = 0;
	goto out;
oom:
	sess_drop(s);
	api->throw(ctx, "smtp_oom", 500, "gagal mengalokasikan memori");
out:
	free(content.b);
	free(body);
	return rc;
}

/* send_html(teks, html) → true. DATA multipart/alternative. */
static int m_send_html(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	smtp_sess *s = self_sess(ctx, argv[0]);
	char *text = NULL, *html = NULL;
	size_t tlen = 0, hlen = 0;
	char bnd[96];
	sbuf content = { NULL, 0, 0 };
	const char *cte1, *cte2;
	int ub1, ub2, rc = -1;
	(void)argc;
	if (!s)
		return -1;
	if (!s->in_txn || s->nrcpt == 0) {
		api->throw(ctx, "smtp_error", 500,
			   "transaksi belum siap — panggil from() lalu to() minimal sekali sebelum send_html()");
		return -1;
	}
	text = arg_to_str(ctx, argv[1], &tlen);
	if (!text)
		return -1;
	html = arg_to_str(ctx, argv[2], &hlen);
	if (!html) {
		free(text);
		return -1;
	}
	if (body_check(ctx, text, tlen, "isi teks") != 0 ||
	    body_check(ctx, html, hlen, "isi HTML") != 0)
		goto out;
	if (tlen > SMTP_MAX_MSG || hlen > SMTP_MAX_MSG) {
		api->throw(ctx, "smtp_error", 500, "isi pesan melewati batas 32 MiB");
		goto out;
	}
	{ /* boundary unik: ulangi bila kebetulan muncul di isi pesan */
		int tries;
		for (tries = 0; tries < 8; tries++) {
			char suf[16];
			if (tries == 0)
				suf[0] = 0;
			else
				snprintf(suf, sizeof suf, "-%d", tries);
			snprintf(bnd, sizeof bnd, "garurda=%lld=%u%s",
				 (long long)time(NULL), ++s->seq, suf);
			if (!has_substr(text, tlen, bnd) && !has_substr(html, hlen, bnd))
				break;
		}
		if (tries == 8) {
			api->throw(ctx, "smtp_error", 500, "gagal membuat boundary multipart unik");
			goto out;
		}
	}
	cte1 = pick_cte(s, text, tlen, &ub1);
	cte2 = pick_cte(s, html, hlen, &ub2);
	if (cmd_line(ctx, s, "DATA") != 0)
		goto out;
	if (expect_code(ctx, s, 354, "DATA") != 0)
		goto out;
	{
		char ct[160];
		snprintf(ct, sizeof ct, "multipart/alternative; boundary=\"%s\"", bnd);
		if (put_std_headers(&content, s, ct, NULL) != 0 ||
		    sb_add(&content, "\r\n", 2) != 0)
			goto oom;
		if (put_boundary(&content, bnd, "") != 0 ||
		    put_hdr(&content, "Content-Type", "text/plain; charset=utf-8") != 0 ||
		    put_hdr(&content, "Content-Transfer-Encoding", cte1) != 0 ||
		    sb_add(&content, "\r\n", 2) != 0 ||
		    put_payload(&content, text, tlen, ub1) != 0)
			goto oom;
		if (put_boundary(&content, bnd, "") != 0 ||
		    put_hdr(&content, "Content-Type", "text/html; charset=utf-8") != 0 ||
		    put_hdr(&content, "Content-Transfer-Encoding", cte2) != 0 ||
		    sb_add(&content, "\r\n", 2) != 0 ||
		    put_payload(&content, html, hlen, ub2) != 0)
			goto oom;
		if (put_boundary(&content, bnd, "--") != 0)
			goto oom;
	}
	if (data_transaction(ctx, s, &content) != 0)
		goto out;
	*ret = api->bool_new(ctx, 1);
	rc = 0;
	goto out;
oom:
	sess_drop(s);
	api->throw(ctx, "smtp_oom", 500, "gagal mengalokasikan memori");
out:
	free(content.b);
	free(text);
	free(html);
	return rc;
}

/* close() → true bila menutup sesi hidup (idempoten; QUIT best-effort —
 * balasan 221 tidak pernah membuat close() melempar galat). */
static int m_close(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	smtp_state *st = api->get_data(ctx);
	gne_handle h;
	int64_t id, gen;
	(void)argc;
	if (!st || api->obj_get(ctx, argv[0], "id", &h) != 0 ||
	    api->get_int(ctx, h, &id) != 0 ||
	    id < 0 || id >= SMTP_MAX_CONN ||
	    api->obj_get(ctx, argv[0], "gen", &h) != 0 ||
	    api->get_int(ctx, h, &gen) != 0 ||
	    st->ss[id].fd < 0 || st->ss[id].gen != gen) {
		*ret = api->bool_new(ctx, 0);
		return 0;
	}
	{
		smtp_sess *s = &st->ss[id];
		int saved = s->timeout_ms;
		if (send_all(s, "QUIT\r\n", 6) == 0) {
			int code;
			char msg[256];
			if (saved <= 0 || saved > SMTP_QUIT_WAIT_MS)
				s->timeout_ms = SMTP_QUIT_WAIT_MS;
			set_timeouts(s->fd, s->timeout_ms);
			read_reply_ex(ctx, s, &code, msg, sizeof msg, 1);
			(void)code;
			s->timeout_ms = saved;
		}
		if (s->fd >= 0)
			sfd_close(s->fd);
		s->fd = -1;
		sess_clear(s);
		st->ss[id].gen++; /* invalidate objek ini untuk koneksi berikutnya */
	}
	*ret = api->bool_new(ctx, 1);
	return 0;
}

/* ---- smtp.connect(host, port [, timeout_ms]) ---- */
static int smtp_connect(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret)
{
	smtp_state *st = api->get_data(ctx);
	char host[256], ports[16], ehlo[320];
	size_t hlen;
	int64_t port, timeout = SMTP_TIMEOUT_DEFAULT_MS;
	int slot, e = 0;
	struct addrinfo hints, *res = NULL, *ai;
	smtp_sess *s;
	gne_handle obj, hv;

	if (!st) {
		api->throw(ctx, "smtp_error", 500, "state modul SMTP hilang");
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
		api->throw(ctx, "smtp_error", 500,
			   "sesi penuh (maksimal 64 koneksi terbuka; tutup yang tidak dipakai)");
		return -1;
	}
	s = &st->ss[slot];
	sess_clear(s); /* bersihkan sisa kehidupan lama pada slot ini */

	snprintf(ports, sizeof ports, "%" PRId64, port);
	memset(&hints, 0, sizeof hints);
	hints.ai_family = AF_UNSPEC;
	hints.ai_socktype = SOCK_STREAM;
	if (getaddrinfo(host, ports, &hints, &res) != 0) {
		char msg[320];
		snprintf(msg, sizeof msg, "gagal memecahkan alamat host %s", host);
		api->throw(ctx, "smtp_error", 502, msg);
		return -1;
	}

	for (ai = res; ai; ai = ai->ai_next) {
		sfd_t fd = socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
		if (fd == SFD_INVALID) {
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
			s->fd = (int)fd;
			s->timeout_ms = (int)timeout;
			s->gen++; /* siklus hidup baru — invalidasi objek lama */
			freeaddrinfo(res);
			res = NULL;
			break;
		}
		sfd_close(fd);
	}
	if (s->fd < 0) {
		char msg[384], txt[96];
		freeaddrinfo(res);
		if (e == ETIMEDOUT || is_timeout_err(e))
			snprintf(msg, sizeof msg,
				 "gagal terhubung ke %.200s:%" PRId64 " (waktu tunggu %d ms habis)",
				 host, port, (int)timeout);
		else
			snprintf(msg, sizeof msg, "gagal terhubung ke %.200s:%" PRId64 ": %s",
				 host, port, sock_text(e, txt, sizeof txt));
		api->throw(ctx, "smtp_error", 502, msg);
		return -1;
	}

	/* Nama host klien untuk EHLO & Message-ID. */
	if (gethostname(s->me, (int)sizeof s->me) != 0)
		s->me[0] = 0;
	s->me[sizeof s->me - 1] = 0;
	if (s->me[0] == 0)
		snprintf(s->me, sizeof s->me, "localhost");

	/* Sapaan wajib 220. */
	if (expect_code(ctx, s, 220, "sapaan") != 0) {
		sess_drop(s);
		return -1;
	}

	/* EHLO → simpan kemampuan (diseragamkan huruf besar). Bila ditolak,
	 * fallback HELO tanpa ekstensi (RFC 5321 bagian 4.1.4). */
	snprintf(ehlo, sizeof ehlo, "EHLO %.200s", s->me);
	if (cmd_line(ctx, s, ehlo) != 0)
		return -1; /* sesi sudah dibuang */
	{
		int code;
		char msg[SMTP_CAPS];
		if (read_reply(ctx, s, &code, msg, sizeof msg) != 0)
			return -1; /* sesi sudah dibuang pembaca balasan */
		if (code == 250) {
			size_t i;
			for (i = 0; msg[i]; i++)
				if (msg[i] >= 'a' && msg[i] <= 'z')
					msg[i] = (char)(msg[i] - 'a' + 'A');
			snprintf(s->caps, sizeof s->caps, "%s", msg);
		} else {
			char heo[320];
			snprintf(heo, sizeof heo, "HELO %.200s", s->me);
			if (cmd_line(ctx, s, heo) != 0)
				return -1;
			if (expect_code(ctx, s, 250, "HELO") != 0) {
				sess_drop(s);
				return -1;
			}
			s->caps[0] = 0;
		}
	}

	obj = api->object(ctx);
	hv = api->int_new(ctx, slot);
	api->obj_set(ctx, obj, "id", hv);
	hv = api->int_new(ctx, s->gen);
	api->obj_set(ctx, obj, "gen", hv);
	hv = api->string(ctx, host, strlen(host));
	api->obj_set(ctx, obj, "host", hv);
	hv = api->int_new(ctx, port);
	api->obj_set(ctx, obj, "port", hv);

	api->define_method(ctx, obj, "auth", 2, 2, m_auth, obj);
	api->define_method(ctx, obj, "from", 1, 1, m_from, obj);
	api->define_method(ctx, obj, "to", 1, 1, m_to, obj);
	api->define_method(ctx, obj, "subject", 1, 1, m_subject, obj);
	api->define_method(ctx, obj, "send", 1, 1, m_send, obj);
	api->define_method(ctx, obj, "send_html", 2, 2, m_send_html, obj);
	api->define_method(ctx, obj, "close", 0, 0, m_close, obj);

	*ret = obj;
	return 0;
}

int gne_module_init(const gne_host_api *a, gne_ctx *ctx, gne_handle *out)
{
	smtp_state *st;
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
				api->throw(ctx, "smtp_error", 502, "WSAStartup gagal");
				return 1;
			}
			wsa_done = 1;
		}
	}
#endif
	st = calloc(1, sizeof(*st));
	if (!st) {
		api->throw(ctx, "smtp_oom", 500, "gagal mengalokasikan state sesi");
		return 1;
	}
	for (i = 0; i < SMTP_MAX_CONN; i++)
		st->ss[i].fd = -1;
	api->set_data(ctx, st);

	api->define_fn(ctx, "connect", 2, 3, smtp_connect);
	*out = api->module(ctx);
	return 0;
}