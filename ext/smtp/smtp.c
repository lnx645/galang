/* smtp.c — official GNE extension: pure-C SMTP client (RFC 5321/5322).
 *
 * Usage style:
 *
 *   use "smtp"
 *   $s = smtp.connect("127.0.0.1", 1025)          // greeting 220 + EHLO
 *   $s = smtp.connect("smtp.example.id", 587, 3000)  // custom timeout
 *   $s.auth("user@example.id", "rahasia")          // AUTH LOGIN
 *   $s.from("kirim@example.id")                    // MAIL FROM
 *   $s.to("satu@example.id")                       // RCPT TO (may repeat)
 *   $s.to("dua@example.id")
 *   $s.subject("Hello from GaLang")
 *   $s.send("Hello.\nLine.")                       // DATA text/plain
 *   $s.send_html("Hello", "<p>Hello <b>world</b></p>") // multipart/alternative
 *   $s.close()
 *
 * State: a session table of at most 64 slots is attached via
 * api->set_data (safe when one .so is used by several interpreters);
 * each session object stores an "id" field → slot. An I/O failure closes
 * the session, so the next method throws "connection is already closed
 * or invalid".
 *
 * After MAIL FROM (from()), the transaction stays active until DATA
 * completes; sending the next email requires from() + to() again (the
 * server envelope is reset after end-of-data). subject() persists across
 * transactions.
 *
 * Catchable error "smtp_error":
 *   status 500 — server rejection / unexpected protocol / wrong state
 *                (e.g. send() before from()) / invalid argument
 *   status 502 — failed to connect / connection dropped
 *   status 504 — timeout elapsed
 *
 * LIMITATIONS (honest, also written in docs):
 *   - Plain, no TLS. STARTTLS/OpenSSL is NOT yet supported — a server
 *     that requires TLS before AUTH will reject (the message is passed
 *     through as-is). Do not use this path on public networks.
 *   - The only AUTH mechanism is LOGIN (not PLAIN/CRAM-MD5).
 *   - ASCII-only addresses (no SMTPUTF8); non-ASCII subjects are
 *     automatically encoded as RFC 2047 encoded-words (?=UTF-8?B?...?=).
 *   - The body is not folded: body lines over 1000 octets may be rejected
 *     by servers with a strict line_length limit (Postfix default 2000).
 *   - NUL in messages is rejected (type_error) — violates SMTP.
 *
 * Cross-platform: POSIX (fcntl/select) and Winsock (#ifdef _WIN32).
 * SIGPIPE is suppressed via MSG_NOSIGNAL (Linux) or
 * SO_NOSIGPIPE (macOS) — the extension never changes the process's
 * signal handlers.
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

/* ---- limits (documented in docs) ---- */
#define SMTP_MAX_CONN 64
#define SMTP_MAX_RCPT 100
#define SMTP_TIMEOUT_DEFAULT_MS 5000
#define SMTP_QUIT_WAIT_MS 2000      /* wait limit for the 221 reply on close() */
#define SMTP_MAX_LINE (1u << 20)    /* protocol line ≤ 1 MiB */
#define SMTP_MAX_REPLY (64u << 10)  /* one multi-line reply total ≤ 64 KiB */
#define SMTP_MAX_MSG (32u << 20)    /* outgoing message ≤ 32 MiB */
#define SMTP_MAX_ADDR 320           /* RFC 5321 path + angles */
#define SMTP_MAX_SUBJECT 700        /* subject — encoded-word stays < 998 octets */
#define SMTP_CAPS 8192              /* storage for the EHLO reply text */

typedef struct {
	int fd; /* -1 = empty slot */
	int timeout_ms;
	int64_t gen; /* generation number — rejects stale objects from close/connect cycles */
	char *rbuf;
	size_t rcap, rlen, rpos;
	char me[256];   /* client host name (EHLO + Message-ID) */
	char caps[SMTP_CAPS]; /* EHLO reply text, case-normalized to uppercase */
	int in_txn;     /* MAIL FROM active, awaiting DATA */
	char *subject;  /* stored subject (persists across transactions) */
	char *from_addr; /* last MAIL FROM address */
	char *rcpt[SMTP_MAX_RCPT];
	int nrcpt;
	unsigned seq; /* sequence number for Message-ID & boundary */
} smtp_sess;

typedef struct {
	smtp_sess ss[SMTP_MAX_CONN];
} smtp_state;

/* Buffer read results. */
enum {
	BUF_OK = 0,
	BUF_EOF = -1,     /* server closed the connection */
	BUF_TIMEOUT = -2, /* SO_RCVTIMEO reached */
	BUF_ERR = -3,     /* other socket error */
	BUF_TOOLARGE = -4 /* size limit exceeded */
};

/* ---- socket error text (deterministic, no strerror) ---- */
static const char *sock_text(int e, char *buf, size_t cap)
{
	const char *s = NULL;
#ifdef _WIN32
	switch (e) {
	case WSAETIMEDOUT: s = "timed out"; break;
	case WSAECONNREFUSED: s = "connection refused"; break;
	case WSAECONNRESET: s = "connection reset"; break;
	case WSAEHOSTUNREACH: s = "host unreachable"; break;
	case WSAENETUNREACH: s = "network unreachable"; break;
	case WSAENOTCONN: s = "not connected"; break;
	default: break;
	}
#else
	switch (e) {
	case ETIMEDOUT: s = "timed out"; break;
	case ECONNREFUSED: s = "connection refused"; break;
	case ECONNRESET: s = "connection reset"; break;
	case EHOSTUNREACH: s = "host unreachable"; break;
	case ENETUNREACH: s = "network unreachable"; break;
	case ENOTCONN: s = "not connected"; break;
	case EPIPE: s = "broken pipe"; break;
	default: break;
	}
#endif
	if (s)
		snprintf(buf, cap, "%s (code %d)", s, e);
	else
		snprintf(buf, cap, "socket error %d", e);
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

/* ---- read buffer ---- */

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

/* Move unconsumed data to the start of the buffer. */
static void buf_compact(smtp_sess *s)
{
	if (s->rpos == 0)
		return;
	if (s->rlen > s->rpos)
		memmove(s->rbuf, s->rbuf + s->rpos, s->rlen - s->rpos);
	s->rlen -= s->rpos;
	s->rpos = 0;
}

/* Receive at least one more byte (compacting first if needed). */
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

/* Read one protocol line (without CRLF). *out points into the buffer —
 * VALID only until the next read operation. */
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

/* ---- I/O errors ---- */

static int fail_io(gne_ctx *ctx, smtp_sess *s, int rc, const char *what)
{
	char buf[256];
	if (rc == BUF_TIMEOUT) {
		snprintf(buf, sizeof buf, "%s: timed out after %d ms", what, s->timeout_ms);
		api->throw(ctx, "smtp_error", 504, buf);
	} else if (rc == BUF_EOF) {
		snprintf(buf, sizeof buf, "%s: server closed the connection", what);
		api->throw(ctx, "smtp_error", 502, buf);
	} else if (rc == BUF_TOOLARGE) {
		snprintf(buf, sizeof buf, "%s: reply exceeded the size limit", what);
		api->throw(ctx, "smtp_error", 500, buf);
	} else {
		int e = SOCK_ERRNO;
		char txt[96];
		snprintf(buf, sizeof buf, "%s: %s", what, sock_text(e, txt, sizeof txt));
		api->throw(ctx, "smtp_error", 502, buf);
	}
	return -1;
}

/* ---- string argument helpers ---- */

/* Convert one handle to a string; type error → throw. */
static char *arg_to_str(gne_ctx *ctx, gne_handle h, size_t *len)
{
	int32_t t;
	if (api->type_of(ctx, h, &t) != 0) {
		api->throw(ctx, "type_error", 500, "invalid argument");
		return NULL;
	}
	switch (t) {
	case GNE_STRING: {
		size_t n;
		char *out;
		if (api->str_len(ctx, h, &n) != 0) {
			api->throw(ctx, "type_error", 500, "string argument could not be read");
			return NULL;
		}
		out = malloc(n + 1);
		if (!out) {
			api->throw(ctx, "smtp_oom", 500, "failed to allocate memory");
			return NULL;
		}
		if (api->str_copy(ctx, h, out, n + 1) < 0) {
			free(out);
			api->throw(ctx, "type_error", 500, "string argument could not be read");
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
			api->throw(ctx, "smtp_oom", 500, "failed to allocate memory");
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
			api->throw(ctx, "smtp_oom", 500, "failed to allocate memory");
			return NULL;
		}
		*len = strlen(tmp);
		return out;
	}
	default:
		api->throw(ctx, "type_error", 500, "argument must be a string or a number");
		return NULL;
	}
}

/* ---- write buffer ---- */

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

/* ---- sessions ---- */

/* Look up the session from the self object; error if stale/closed. */
static smtp_sess *self_sess(gne_ctx *ctx, gne_handle self)
{
	smtp_state *st = api->get_data(ctx);
	gne_handle h;
	int64_t id, gen;
	if (!st) {
		api->throw(ctx, "smtp_error", 500, "SMTP module state is missing");
		return NULL;
	}
	if (api->obj_get(ctx, self, "id", &h) != 0 ||
	    api->get_int(ctx, h, &id) != 0 ||
	    id < 0 || id >= SMTP_MAX_CONN ||
	    api->obj_get(ctx, self, "gen", &h) != 0 ||
	    api->get_int(ctx, h, &gen) != 0 ||
	    st->ss[id].fd < 0 || st->ss[id].gen != gen) {
		api->throw(ctx, "smtp_error", 500, "connection is already closed or invalid");
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

/* Force-close the session (used after an I/O error): the connection is
 * considered broken; the next method throws "already closed". */
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
		return; /* 0 = no limit (purely blocking) */
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

/* Nonblocking connect + select(timeout). 0 = success; other value = errno/WSA. */
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

/* Find a free slot; -1 if full. */
static int slot_free(smtp_state *st)
{
	int i;
	for (i = 0; i < SMTP_MAX_CONN; i++)
		if (st->ss[i].fd < 0)
			return i;
	return -1;
}

/* ---- send commands + read replies ---- */

/* Send one command line (without CRLF) + CRLF. Failure → close session + throw. */
static int cmd_line(gne_ctx *ctx, smtp_sess *s, const char *line)
{
	sbuf out = { NULL, 0, 0 };
	int ok;
	if (sb_puts(&out, line) != 0 || sb_add(&out, "\r\n", 2) != 0) {
		free(out.b);
		sess_drop(s);
		api->throw(ctx, "smtp_oom", 500, "failed to allocate memory");
		return -1;
	}
	ok = send_all(s, out.b, out.len) == 0;
	free(out.b);
	if (!ok) {
		int e = SOCK_ERRNO;
		char buf[256], txt[96];
		sess_drop(s);
		if (is_timeout_err(e))
			snprintf(buf, sizeof buf, "sending command: timed out after %d ms", s->timeout_ms);
		else
			snprintf(buf, sizeof buf, "sending command: %s", sock_text(e, txt, sizeof txt));
		api->throw(ctx, "smtp_error", is_timeout_err(e) ? 504 : 502, buf);
		return -1;
	}
	return 0;
}

/* Read one multi-line reply (250-... / 250 ...). *code = official code;
 * *msg = text of all lines (code stripped), NUL-terminated.
 * 0 = success; -1 = failure. I/O / protocol failure = the stream can no
 * longer be trusted, so the session is dropped as well.
 * quiet=1: do not throw any error (used for QUIT during close()). */
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
				fail_io(ctx, s, rc, "reading SMTP reply");
			sess_drop(s);
			return -1;
		}
		if (linelen < 3 ||
		    line[0] < '0' || line[0] > '9' ||
		    line[1] < '0' || line[1] > '9' ||
		    line[2] < '0' || line[2] > '9') {
			if (!quiet)
				api->throw(ctx, "smtp_error", 500, "unexpected SMTP reply (not a 3-digit code)");
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
				api->throw(ctx, "smtp_error", 500, "SMTP reply too large");
			sess_drop(s);
			return -1;
		}
		/* Text after "NNN " or "NNN-" (empty when the line is just the code). */
		if (linelen > 4) {
			size_t n = linelen - 4;
			size_t used = strlen(msg);
			if (used + n + 2 >= msgcap) {
				/* Truncate: enough for the error message & capability check. */
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
			return 0; /* terminating line */
		if (line[3] != '-') {
			if (!quiet)
				api->throw(ctx, "smtp_error", 500, "unexpected SMTP reply (separator is neither '-' nor ' ')");
			sess_drop(s);
			return -1;
		}
		/* continue: continuation line "NNN-..." */
	}
}

/* Noisy variant (throws errors as needed). */
static int read_reply(gne_ctx *ctx, smtp_sess *s, int *code, char *msg, size_t msgcap)
{
	return read_reply_ex(ctx, s, code, msg, msgcap, 0);
}

/* Read a reply then match it against a single expected code. */
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
		snprintf(buf, sizeof buf, "%s: server replied %d %s", what, code, msg);
		api->throw(ctx, "smtp_error", 500, buf);
	}
	return -1;
}

/* Read a reply, accept one of two codes (e.g. RCPT: 250/251). */
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
		snprintf(buf, sizeof buf, "%s: server replied %d %s", what, code, msg);
		api->throw(ctx, "smtp_error", 500, buf);
	}
	return -1;
}

/* ---- base64 (AUTH LOGIN & Content-Transfer-Encoding) ---- */

static const char b64tab[] =
	"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/* Write base64 to sbuf. wrap>0: insert CRLF after every `wrap` characters
 * (a finished line is not terminated with CRLF here — the caller handles it). */
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

/* ---- text utilities ---- */

/* Does the substring exist? (memmem is not portable on Windows) */
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

/* ---- argument validation ---- */

/* Envelope address: 1–320 printable-ASCII octets, no space/control/comma/angle
 * (prevents line injection & command-argument smuggling). */
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

/* Subject: no CR/LF/NUL controls (anti header injection). The 700-octet
 * limit keeps non-ASCII encoded-words under 998 octets per line. */
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

/* Message body free of NUL (SMTP forbids NUL in mail data). */
static int body_check(gne_ctx *ctx, const char *p, size_t n, const char *what)
{
	if (memchr(p, 0, n) != NULL) {
		char buf[160];
		snprintf(buf, sizeof buf, "%s contains NUL characters, forbidden by SMTP", what);
		api->throw(ctx, "type_error", 500, buf);
		return -1;
	}
	return 0;
}

/* ---- capabilities & CTE selection ---- */

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

/* Choose Content-Transfer-Encoding: 7bit for pure ASCII; 8bit if the server
 * advertises 8BITMIME; otherwise base64 (always valid everywhere). */
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

/* ---- RFC 5322 date (UTC; manual English day/month names, no
 * locale dependency) ---- */

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
	if (rem < 0) { /* floor division for times before 1970 */
		rem += 86400;
		days--;
	}
	w = (int)(((days + 4) % 7 + 7) % 7); /* 1970-01-01 = Thursday (index 4) */
	{ /* civil days → y/m/d (civil_from_days algorithm) */
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

/* ---- DATA content builders ---- */

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

/* Multipart part delimiter line: "--" + boundary + tail ("", "/--") + CRLF. */
static int put_boundary(sbuf *out, const char *bnd, const char *tail)
{
	if (sb_puts(out, "--") != 0 || sb_puts(out, bnd) != 0 ||
	    sb_puts(out, tail) != 0 || sb_add(out, "\r\n", 2) != 0)
		return -1;
	return 0;
}

/* Write the message body: normalize line endings (\r\n, \n, lone \r → \r\n)
 * and dot-stuffing (lines starting with "." become ".."). The last line is
 * always terminated with CRLF; empty input produces empty output. */
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
			i++; /* skip the LF of a CRLF pair */
		start = i + 1;
	}
	if (start < n) { /* last line without a terminator */
		if (p[start] == '.' && sb_add(out, ".", 1) != 0)
			return -1;
		if (sb_add(out, p + start, n - start) != 0)
			return -1;
		if (sb_add(out, "\r\n", 2) != 0)
			return -1;
	}
	return 0;
}

/* Write the message body per the chosen CTE (raw / base64). */
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

/* Standard headers for DATA. ctype is required; cte may be NULL (multipart:
 * Content-Transfer-Encoding is written per part). */
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
	if (s->nrcpt > 0) { /* To: folded at commas when the line exceeds 78 octets */
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

/* Send a data chunk as-is. Failure → close session + throw. */
static int send_blob(gne_ctx *ctx, smtp_sess *s, const char *p, size_t n, const char *what)
{
	if (send_all(s, p, n) == 0)
		return 0;
	{
		int e = SOCK_ERRNO;
		char buf[320], txt[96];
		if (is_timeout_err(e))
			snprintf(buf, sizeof buf, "%s: timed out after %d ms", what, s->timeout_ms);
		else
			snprintf(buf, sizeof buf, "%s: %s", what, sock_text(e, txt, sizeof txt));
		sess_drop(s);
		api->throw(ctx, "smtp_error", is_timeout_err(e) ? 504 : 502, buf);
	}
	return -1;
}

/* Terminate the content (ensure CRLF, then "." CRLF), send, read 250, and
 * reset the envelope. The content is modified but stays owned by the caller.
 * I/O/protocol failure → the session was already dropped by the reply
 * reader. */
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
	if (send_blob(ctx, s, content->b, content->len, "sending the message body") != 0)
		return -1;
	if (read_reply(ctx, s, &code, msg, sizeof msg) != 0)
		return -1;
	/* Whatever the outcome, the server envelope has consumed the DATA. */
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
		snprintf(buf, sizeof buf, "server rejected the message body: %d %s", code, msg);
		api->throw(ctx, "smtp_error", 500, buf);
		return -1;
	}
	return 0;
oom:
	sess_drop(s);
	api->throw(ctx, "smtp_oom", 500, "failed to allocate memory");
	return -1;
}

/* ---- method ---- */

/* auth(user, pass) → true (235). AUTH LOGIN mechanism. */
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
			   "AUTH unavailable: the server does not support EHLO (TLS may be required — STARTTLS is not supported yet)");
		goto out;
	}
	if (!caps_has(s, "AUTH") || !caps_has(s, "LOGIN")) {
		api->throw(ctx, "smtp_error", 500,
			   "the server does not advertise AUTH LOGIN in its EHLO reply (this client only supports the LOGIN mechanism)");
		goto out;
	}
	if (cmd_line(ctx, s, "AUTH LOGIN") != 0)
		goto out;
	if (expect_code(ctx, s, 334, "AUTH LOGIN") != 0)
		goto out;
	{ /* username (single-line base64) */
		sbuf b = { NULL, 0, 0 };
		if (b64_write(&b, (const unsigned char *)user, ulen, 0) != 0 ||
		    sb_add(&b, "\r\n", 2) != 0) {
			free(b.b);
			sess_drop(s); /* the server is waiting for a data line — disconnect */
			api->throw(ctx, "smtp_oom", 500, "failed to allocate memory");
			goto out;
		}
		if (send_blob(ctx, s, b.b, b.len, "sending the username") != 0) {
			free(b.b);
			goto out;
		}
		free(b.b);
	}
	if (expect_code(ctx, s, 334, "AUTH LOGIN (username)") != 0)
		goto out;
	{ /* password */
		sbuf b = { NULL, 0, 0 };
		if (b64_write(&b, (const unsigned char *)pass, plen, 0) != 0 ||
		    sb_add(&b, "\r\n", 2) != 0) {
			free(b.b);
			sess_drop(s);
			api->throw(ctx, "smtp_oom", 500, "failed to allocate memory");
			goto out;
		}
		if (send_blob(ctx, s, b.b, b.len, "sending the password") != 0) {
			free(b.b);
			goto out;
		}
		free(b.b);
	}
	if (expect_code(ctx, s, 235, "AUTH LOGIN (password)") != 0)
		goto out;
	*ret = api->bool_new(ctx, 1);
	rc = 0;
out:
	free(user);
	free(pass);
	return rc;
}

/* from(address) → true. Starts a MAIL transaction; any previous transaction
 * is reset with RSET first to keep call ordering forgiving. */
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
			   "invalid address (1–320 printable-ASCII octets, no spaces and '<>,')");
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
		api->throw(ctx, "smtp_oom", 500, "failed to allocate memory");
		goto out;
	}
	*ret = api->bool_new(ctx, 1);
	rc = 0;
out:
	free(addr);
	return rc;
}

/* to(address) → true. Adds a recipient (RCPT TO, may repeat). */
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
			   "invalid address (1–320 printable-ASCII octets, no spaces and '<>,')");
		goto out;
	}
	if (!s->in_txn) {
		api->throw(ctx, "smtp_error", 500, "call from() before to()");
		goto out;
	}
	if (s->nrcpt >= SMTP_MAX_RCPT) {
		api->throw(ctx, "smtp_error", 500,
			   "too many recipients (at most 100 per transaction)");
		goto out;
	}
	snprintf(line, sizeof line, "RCPT TO:<%s>", addr);
	if (cmd_line(ctx, s, line) != 0)
		goto out;
	if (expect_code2(ctx, s, 250, 251, "RCPT TO") != 0)
		goto out;
	s->rcpt[s->nrcpt] = strdup(addr);
	if (!s->rcpt[s->nrcpt]) {
		api->throw(ctx, "smtp_oom", 500, "failed to allocate memory");
		goto out;
	}
	s->nrcpt++;
	*ret = api->bool_new(ctx, 1);
	rc = 0;
out:
	free(addr);
	return rc;
}

/* subject(text) → true. Stores the subject for the next send()
 * (persists across transactions; does not touch the protocol). */
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
			   "invalid subject (no CR/LF controls, at most 700 octets)");
		free(txt);
		return -1;
	}
	free(s->subject);
	s->subject = strdup(txt); /* failure = no subject; state stays consistent */
	free(txt);
	if (!s->subject) {
		api->throw(ctx, "smtp_oom", 500, "failed to allocate memory");
		return -1;
	}
	*ret = api->bool_new(ctx, 1);
	return 0;
}

/* send(text) → true. DATA text/plain. */
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
			   "transaction not ready — call from() then to() at least once before send()");
		return -1;
	}
	body = arg_to_str(ctx, argv[1], &blen);
	if (!body)
		return -1;
	if (body_check(ctx, body, blen, "message body") != 0)
		goto out;
	if (blen > SMTP_MAX_MSG) {
		api->throw(ctx, "smtp_error", 500, "message body exceeds the 32 MiB limit");
		goto out;
	}
	cte = pick_cte(s, body, blen, &use_b64);
	if (cmd_line(ctx, s, "DATA") != 0)
		goto out; /* session already dropped */
	if (expect_code(ctx, s, 354, "DATA") != 0)
		goto out; /* rejected: the stream is still in sync */
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
	api->throw(ctx, "smtp_oom", 500, "failed to allocate memory");
out:
	free(content.b);
	free(body);
	return rc;
}

/* send_html(text, html) → true. DATA multipart/alternative. */
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
			   "transaction not ready — call from() then to() at least once before send_html()");
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
	if (body_check(ctx, text, tlen, "text body") != 0 ||
	    body_check(ctx, html, hlen, "HTML body") != 0)
		goto out;
	if (tlen > SMTP_MAX_MSG || hlen > SMTP_MAX_MSG) {
		api->throw(ctx, "smtp_error", 500, "message body exceeds the 32 MiB limit");
		goto out;
	}
	{ /* unique boundary: retry if it happens to appear in the message body */
		int tries;
		for (tries = 0; tries < 8; tries++) {
			char suf[16];
			if (tries == 0)
				suf[0] = 0;
			else
				snprintf(suf, sizeof suf, "-%d", tries);
			snprintf(bnd, sizeof bnd, "galang=%lld=%u%s",
				 (long long)time(NULL), ++s->seq, suf);
			if (!has_substr(text, tlen, bnd) && !has_substr(html, hlen, bnd))
				break;
		}
		if (tries == 8) {
			api->throw(ctx, "smtp_error", 500, "failed to create a unique multipart boundary");
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
	api->throw(ctx, "smtp_oom", 500, "failed to allocate memory");
out:
	free(content.b);
	free(text);
	free(html);
	return rc;
}

/* close() → true when closing a live session (idempotent; QUIT is
 * best-effort — the 221 reply never makes close() throw an error). */
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
		st->ss[id].gen++; /* invalidate this object for the next connection */
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
		api->throw(ctx, "smtp_error", 500, "SMTP module state is missing");
		return -1;
	}
	if (argc >= 3 && api->get_int(ctx, argv[2], &timeout) != 0) {
		api->throw(ctx, "type_error", 500, "timeout must be a number (milliseconds)");
		return -1;
	}
	if (timeout < 0) {
		api->throw(ctx, "type_error", 500, "timeout must not be negative (0 = no limit)");
		return -1;
	}
	if (api->str_len(ctx, argv[0], &hlen) != 0 || hlen == 0 || hlen >= sizeof host) {
		api->throw(ctx, "type_error", 500, "host must be a 1–255 character string");
		return -1;
	}
	api->str_copy(ctx, argv[0], host, sizeof host);
	if (api->get_int(ctx, argv[1], &port) != 0 || port < 1 || port > 65535) {
		api->throw(ctx, "type_error", 500, "port must be a number 1–65535");
		return -1;
	}
	slot = slot_free(st);
	if (slot < 0) {
		api->throw(ctx, "smtp_error", 500,
			   "session table full (at most 64 open connections; close unused ones)");
		return -1;
	}
	s = &st->ss[slot];
	sess_clear(s); /* clear leftovers from a previous life in this slot */

	snprintf(ports, sizeof ports, "%" PRId64, port);
	memset(&hints, 0, sizeof hints);
	hints.ai_family = AF_UNSPEC;
	hints.ai_socktype = SOCK_STREAM;
	if (getaddrinfo(host, ports, &hints, &res) != 0) {
		char msg[320];
		snprintf(msg, sizeof msg, "failed to resolve host address %s", host);
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
			s->gen++; /* new lifecycle — invalidate old objects */
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
				 "failed to connect to %.200s:%" PRId64 " (timed out after %d ms)",
				 host, port, (int)timeout);
		else
			snprintf(msg, sizeof msg, "failed to connect to %.200s:%" PRId64 ": %s",
				 host, port, sock_text(e, txt, sizeof txt));
		api->throw(ctx, "smtp_error", 502, msg);
		return -1;
	}

	/* Client host name for EHLO & Message-ID. */
	if (gethostname(s->me, (int)sizeof s->me) != 0)
		s->me[0] = 0;
	s->me[sizeof s->me - 1] = 0;
	if (s->me[0] == 0)
		snprintf(s->me, sizeof s->me, "localhost");

	/* Mandatory 220 greeting. */
	if (expect_code(ctx, s, 220, "greeting") != 0) {
		sess_drop(s);
		return -1;
	}

	/* EHLO → store capabilities (normalized to uppercase). If rejected,
	 * fall back to HELO without extensions (RFC 5321 section 4.1.4). */
	snprintf(ehlo, sizeof ehlo, "EHLO %.200s", s->me);
	if (cmd_line(ctx, s, ehlo) != 0)
		return -1; /* session already dropped */
	{
		int code;
		char msg[SMTP_CAPS];
		if (read_reply(ctx, s, &code, msg, sizeof msg) != 0)
			return -1; /* session already dropped by the reply reader */
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
		api->throw(ctx, "gne_abi", 500, "ABI mismatch");
		return 1;
	}
#ifdef _WIN32
	{
		static int wsa_done;
		if (!wsa_done) {
			WSADATA wsa;
			if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) {
				api->throw(ctx, "smtp_error", 502, "WSAStartup failed");
				return 1;
			}
			wsa_done = 1;
		}
	}
#endif
	st = calloc(1, sizeof(*st));
	if (!st) {
		api->throw(ctx, "smtp_oom", 500, "failed to allocate session state");
		return 1;
	}
	for (i = 0; i < SMTP_MAX_CONN; i++)
		st->ss[i].fd = -1;
	api->set_data(ctx, st);

	api->define_fn(ctx, "connect", 2, 3, smtp_connect);
	*out = api->module(ctx);
	return 0;
}