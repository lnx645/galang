/* redis.c — official GNE extension: a pure C Redis (RESP2) client.
 *
 * Usage style:
 *
 *   use "redis"
 *   $r = redis.connect("127.0.0.1", 6379)        // timeout 5000 ms
 *   $r = redis.connect("127.0.0.1", 6379, 3000)  // custom timeout
 *   $r.set("k", "v")
 *   print($r.get("k"))
 *   $r.close()
 *
 * State: the connection table (max 64 slots) is attached via
 * api->set_data (safe when one .so is used by several interpreters);
 * each connection object stores an "id" field → slot. Connections that
 * are closed or broken are thrown as the catchable "redis_error".
 *
 * Server error replies (-ERR ...) become a throw "redis_error":
 *   status 500 — server rejection / unexpected protocol / bad argument
 *   status 502 — connection failed / connection dropped
 *   status 504 — timed out (timeout)
 *
 * Cross-platform: POSIX (fcntl/select) and Winsock (#ifdef _WIN32).
 * SIGPIPE is suppressed via MSG_NOSIGNAL (Linux) or
 * SO_NOSIGPIPE (macOS) — the extension never changes the process
 * signal handler.
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

/* ---- limits (document them in docs) ---- */
#define REDIS_MAX_CONN 64
#define REDIS_TIMEOUT_DEFAULT_MS 5000
#define REDIS_MAX_LINE (1u << 20)      /* protocol line ≤ 1 MiB */
#define REDIS_MAX_BULK (64u << 20)     /* bulk reply ≤ 64 MiB */
#define REDIS_MAX_DEPTH 32             /* RESP array depth */
#define REDIS_MAX_ELEMS 1000000        /* RESP array element count */
#define REDIS_MAX_CMD (64u << 20)      /* outgoing command ≤ 64 MiB */

typedef struct {
	int fd; /* -1 = empty slot */
	int timeout_ms;
	int64_t gen; /* generation number — rejects stale objects from a close/connect cycle */
	char *rbuf;
	size_t rcap, rlen, rpos;
} conn;

typedef struct {
	conn conns[REDIS_MAX_CONN];
} redis_state;

/* Buffer read results. */
enum {
	BUF_OK = 0,
	BUF_EOF = -1,      /* server closed the connection */
	BUF_TIMEOUT = -2,  /* SO_RCVTIMEO reached */
	BUF_ERR = -3,      /* other socket error */
	BUF_TOOLARGE = -4  /* exceeded the size limit */
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
	case WSAEHOSTUNREACH: s = "destination unreachable"; break;
	case WSAENETUNREACH: s = "network unreachable"; break;
	case WSAENOTCONN: s = "not connected"; break;
	default: break;
	}
#else
	switch (e) {
	case ETIMEDOUT: s = "timed out"; break;
	case ECONNREFUSED: s = "connection refused"; break;
	case ECONNRESET: s = "connection reset"; break;
	case EHOSTUNREACH: s = "destination unreachable"; break;
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

/* Move unused data to the start of the buffer. */
static void buf_compact(conn *c)
{
	if (c->rpos == 0)
		return;
	if (c->rlen > c->rpos)
		memmove(c->rbuf, c->rbuf + c->rpos, c->rlen - c->rpos);
	c->rlen -= c->rpos;
	c->rpos = 0;
}

/* Receive at least one more byte (compacting first if needed). */
static int buf_refill(conn *c)
{
	int rc;
	if (c->rpos > 0) {
		if (c->rlen == c->rpos) { /* only used data remains */
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

/* Make sure ≥ n bytes are in the buffer (from the read position). */
static int buf_ensure(conn *c, size_t n)
{
	while (c->rlen - c->rpos < n) {
		int rc = buf_refill(c);
		if (rc != BUF_OK)
			return rc;
	}
	return BUF_OK;
}

/* Read one protocol line (without CRLF). *out points into the buffer —
 * VALID only until the next read operation. */
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

/* ---- errors ---- */

static int fail_io(gne_ctx *ctx, conn *c, int rc, const char *what)
{
	char buf[256];
	if (rc == BUF_TIMEOUT) {
		snprintf(buf, sizeof buf, "%s: timed out after %d ms", what, c->timeout_ms);
		api->throw(ctx, "redis_error", 504, buf);
	} else if (rc == BUF_EOF) {
		snprintf(buf, sizeof buf, "%s: server closed the connection", what);
		api->throw(ctx, "redis_error", 502, buf);
	} else if (rc == BUF_TOOLARGE) {
		snprintf(buf, sizeof buf, "%s: reply exceeded the size limit", what);
		api->throw(ctx, "redis_error", 500, buf);
	} else {
		int e = SOCK_ERRNO;
		char txt[96];
		snprintf(buf, sizeof buf, "%s: %s", what, sock_text(e, txt, sizeof txt));
		api->throw(ctx, "redis_error", 502, buf);
	}
	return -1;
}

/* ---- argument collection ---- */

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

/* Convert one handle into a protocol string; type error → throw. */
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
			api->throw(ctx, "redis_oom", 500, "failed to allocate memory");
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
			api->throw(ctx, "redis_oom", 500, "failed to allocate memory");
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
			api->throw(ctx, "redis_oom", 500, "failed to allocate memory");
			return NULL;
		}
		*len = strlen(tmp);
		return out;
	}
	default:
		api->throw(ctx, "type_error", 500,
			   "Redis arguments must be strings or numbers");
		return NULL;
	}
}

/* Collect argv[from..argc) into a malloc'd container; 0 = success, -1 = throw. */
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
		api->throw(ctx, "redis_oom", 500, "failed to allocate memory");
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

/* ---- send + read reply ---- */

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
		return -1; /* EOF on send does not occur on a stream; fail anyway */
	}
	return 0;
}

/* Build RESP: *N\r\n $len\r\n arg\r\n ... then send. */
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

/* Read one RESP reply into a GaLang handle (recursive).
 * 0 = success, -1 = already thrown (or an I/O error with a throw). */
static int read_reply(gne_ctx *ctx, conn *c, gne_handle *out, int depth)
{
	const char *line;
	size_t linelen;
	int rc;

	if (depth > REDIS_MAX_DEPTH) {
		api->throw(ctx, "redis_error", 500, "Redis reply is too deeply nested");
		return -1;
	}
	rc = buf_line(c, &line, &linelen);
	if (rc != BUF_OK)
		return fail_io(ctx, c, rc, "reading reply");
	if (linelen == 0) {
		api->throw(ctx, "redis_error", 500, "empty Redis reply");
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
			api->throw(ctx, "redis_error", 500, "unexpected integer reply");
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
			api->throw(ctx, "redis_error", 500, "unexpected bulk length");
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
			api->throw(ctx, "redis_error", 500, "bulk reply exceeds the 64 MiB limit");
			return -1;
		}
		rc = buf_ensure(c, (size_t)n + 2);
		if (rc != BUF_OK)
			return fail_io(ctx, c, rc, "reading bulk reply");
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
			api->throw(ctx, "redis_error", 500, "unexpected element count");
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
			api->throw(ctx, "redis_error", 500, "too many reply elements");
			return -1;
		}
		arr = api->array(ctx);
		for (i = 0; i < n; i++) {
			gne_handle elem;
			if (read_reply(ctx, c, &elem, depth + 1) != 0)
				return -1;
			if (api->arr_push(ctx, arr, elem) != 0) {
				api->throw(ctx, "redis_error", 500, "failed to build the reply array");
				return -1;
			}
		}
		*out = arr;
		return 0;
	}
	default:
		api->throw(ctx, "redis_error", 500, "unknown Redis reply");
		return -1;
	}
}

/* ---- connections ---- */

static conn *self_conn(gne_ctx *ctx, gne_handle self)
{
	redis_state *st = api->get_data(ctx);
	gne_handle h;
	int64_t id, gen;
	if (!st) {
		api->throw(ctx, "redis_error", 500, "Redis module state is missing");
		return NULL;
	}
	if (api->obj_get(ctx, self, "id", &h) != 0 ||
	    api->get_int(ctx, h, &id) != 0 ||
	    id < 0 || id >= REDIS_MAX_CONN ||
	    api->obj_get(ctx, self, "gen", &h) != 0 ||
	    api->get_int(ctx, h, &gen) != 0 ||
	    st->conns[id].fd < 0 || st->conns[id].gen != gen) {
		api->throw(ctx, "redis_error", 500, "connection already closed or invalid");
		return NULL;
	}
	return &st->conns[id];
}

static void set_timeouts(rfd_t fd, int ms)
{
	if (ms <= 0)
		return; /* 0 = no limit (pure blocking) */
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

/* Nonblocking connect + select(timeout). 0 = success; other values = errno/WSA. */
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

/* Find a free slot; -1 if full. */
static int slot_free(redis_state *st)
{
	int i;
	for (i = 0; i < REDIS_MAX_CONN; i++)
		if (st->conns[i].fd < 0)
			return i;
	return -1;
}

/* ---- connection methods ---- */

/* Execute command name + args → *ret (raw reply). 0 on success. */
static int exec_cmd(gne_ctx *ctx, conn *c, const char *name, const argvec *av, gne_handle *ret)
{
	if (send_cmd(c, name, av) != 0) {
		int e = SOCK_ERRNO;
		char buf[160], txt[96];
		if (is_timeout_err(e))
			snprintf(buf, sizeof buf, "sending command %s: timed out after %d ms", name, c->timeout_ms);
		else
			snprintf(buf, sizeof buf, "sending command %s: %s", name, sock_text(e, txt, sizeof txt));
		api->throw(ctx, "redis_error", is_timeout_err(e) ? 504 : 502, buf);
		return -1;
	}
	return read_reply(ctx, c, ret, 0);
}

/* Integer reply → bool (for exists/expire). */
static int int_to_bool(gne_ctx *ctx, gne_handle *h, int negate)
{
	int64_t n;
	if (api->get_int(ctx, *h, &n) != 0) {
		api->throw(ctx, "redis_error", 500, "unexpected Redis reply (not an integer)");
		return -1;
	}
	*h = api->bool_new(ctx, negate ? n == 0 : n != 0);
	return 0;
}

/* Common method pattern: self + args → send name → raw reply. */
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
	if (konversi == 1) /* integer reply → bool */
		return int_to_bool(ctx, ret, 0);
	if (konversi == 2) /* integer reply → negated bool */
		return int_to_bool(ctx, ret, 1);
	return 0;
}

#define METHOD_SIMPLE(nm, konv)                                              \
	static int nm(gne_ctx *ctx, int argc, const gne_handle *argv,        \
		      gne_handle *ret)                                       \
	{                                                                    \
		return method_simple(ctx, argc, argv, ret, #nm, konv);       \
	}

/* Command name differs from the method name. */
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

/* ping() → true (any non-error reply means the server is alive). */
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

/* cmd(name, ...args) → raw reply. */
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
		api->throw(ctx, "type_error", 500, "cmd() requires a command name");
		return -1;
	}
	name = av.v[0]; /* command name = first argument */
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
		/* rest points into av — free everything through av. */
		(void)i;
	}
	args_free(&av);
	return 0;
}

/* close() → true when it closes a live connection, false when already closed
 * or the object is stale (idempotent; other methods still throw "already closed").
 * The generation is bumped so an old object cannot control a
 * new connection that happens to use the same slot. */
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
	st->conns[id].gen++; /* invalidate this object for the next connection */
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
		api->throw(ctx, "redis_error", 500, "Redis module state is missing");
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
		api->throw(ctx, "type_error", 500, "host must be a string of 1–255 characters");
		return -1;
	}
	api->str_copy(ctx, argv[0], host, sizeof host);
	if (api->get_int(ctx, argv[1], &port) != 0 || port < 1 || port > 65535) {
		api->throw(ctx, "type_error", 500, "port must be a number in the range 1–65535");
		return -1;
	}
	slot = slot_free(st);
	if (slot < 0) {
		api->throw(ctx, "redis_error", 500,
			   "too many connections (max 64 open; close unused ones)");
		return -1;
	}

	snprintf(ports, sizeof ports, "%" PRId64, port);
	memset(&hints, 0, sizeof hints);
	hints.ai_family = AF_UNSPEC;
	hints.ai_socktype = SOCK_STREAM;
	if (getaddrinfo(host, ports, &hints, &res) != 0) {
		char msg[320];
		snprintf(msg, sizeof msg, "failed to resolve host address %s", host);
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
			c->gen++; /* new lifecycle — invalidate old objects */
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
			snprintf(msg, sizeof msg, "failed to connect to %.200s:%" PRId64 " (timed out after %d ms)",
				 host, port, (int)timeout);
		else
			snprintf(msg, sizeof msg, "failed to connect to %.200s:%" PRId64 ": %s",
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
		api->throw(ctx, "gne_abi", 500, "ABI mismatch");
		return 1;
	}
#ifdef _WIN32
	{
		static int wsa_done;
		if (!wsa_done) {
			WSADATA wsa;
			if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) {
				api->throw(ctx, "redis_error", 502, "WSAStartup failed");
				return 1;
			}
			wsa_done = 1;
		}
	}
#endif
	st = calloc(1, sizeof(*st));
	if (!st) {
		api->throw(ctx, "redis_oom", 500, "failed to allocate connection state");
		return 1;
	}
	for (i = 0; i < REDIS_MAX_CONN; i++)
		st->conns[i].fd = -1;
	api->set_data(ctx, st);

	api->define_fn(ctx, "connect", 2, 3, redis_connect);
	*out = api->module(ctx);
	return 0;
}
