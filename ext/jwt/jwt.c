/* jwt.c — official GNE extension: JSON Web Token (RFC 7519/7515/8725)
 * with HMAC (HS256/HS384/HS512), pure C with no crypto dependencies.
 *
 * Usage style:
 *
 *   use "jwt"
 *   $t = jwt.sign({"sub": "123", "iat": time.now(), "exp": time.now() + 3600},
 *                 "strong-secret")                  // HS256 (default)
 *   $t = jwt.sign({"sub": "123"}, "strong-secret", "HS512")
 *
 *   $claims = jwt.verify($t, "strong-secret")        // → object; throws on failure
 *   print($claims.sub)
 *
 *   $raw = jwt.decode($t)                           // object, WITHOUT verification!
 *
 * Scope & security decisions:
 *   - HMAC algorithms only (HS256/HS384/HS512). Any other header "alg"
 *     (none, RS256, ES256, ...) is REJECTED — prevents alg-confusion
 *     attacks.
 *   - verify() checks the signature (constant-time comparison), then
 *     the time claims: "exp" (expiry) and "nbf" (not before) when
 *     present — both must be numbers (NumericDate).
 *   - decode() deliberately does NOT verify the signature: inspection
 *     only (displaying the contents of an untrusted token = a trap).
 *   - Claims: an OBJECT is accepted directly and serialized internally
 *     (key enumeration via api->obj_keys, ABI 2); a string is treated
 *     as raw JSON text and must be a valid JSON object.
 *   - verify()/decode() RETURN an object (the claims JSON parsed into
 *     GaLang values) — no manual json_decode() needed.
 *   - Limits: claims ≤1 MiB, token ≤2 MiB, JSON depth64 levels.
 *
 * Errors (catchable with try/catch):
 *   - wrong argument type / empty secret / limit exceeded → "type_error" 500
 *   - broken token/header/payload structure or claims that are not a
 *     JSON object → "jwt_error" 400
 *   - rejected alg, invalid signature, expired, not yet valid →
 *     "jwt_error" 401
 */
#include "gne.h"

#include <ctype.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static const gne_host_api *api;

#define JWT_MAX_CLAIMS (1u << 20) /*1 MiB */
#define JWT_MAX_TOKEN (2u << 20)  /*2 MiB */
#define JWT_MAX_JSON_DEPTH 64

/* ================================================================
 * SHA-2 (FIPS 180-4) — self-contained implementation.
 * The K/IV tables are derived from the prime square/cube roots
 * (full integer precision) and verified against test vectors.
 * ================================================================ */

/* ---- SHA-256 ---- */

typedef struct {
	uint32_t h[8];
	unsigned long long bits;
	unsigned char buf[64];
	size_t n;
} sha256_ctx;

static const uint32_t K256[64] = {
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
	0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
	0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
	0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
	0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
	0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
	0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
	0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
	0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
};

static const uint32_t IV256[8] = {
	0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
	0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
};

#define ROR32(x, n) (((x) >> (n)) | ((x) << (32 - (n))))

static void sha256_block(uint32_t h[8], const unsigned char *p)
{
	uint32_t w[64], a, b, c, d, e, f, g, hh, t1, t2;
	int i;
	for (i = 0; i < 16; i++)
		w[i] = ((uint32_t)p[4 * i] << 24) | ((uint32_t)p[4 * i + 1] << 16) |
		       ((uint32_t)p[4 * i + 2] << 8) | (uint32_t)p[4 * i + 3];
	for (; i < 64; i++) {
		uint32_t s0 = ROR32(w[i - 15], 7) ^ ROR32(w[i - 15], 18) ^ (w[i - 15] >> 3);
		uint32_t s1 = ROR32(w[i - 2], 17) ^ ROR32(w[i - 2], 19) ^ (w[i - 2] >> 10);
		w[i] = w[i - 16] + s0 + w[i - 7] + s1;
	}
	a = h[0]; b = h[1]; c = h[2]; d = h[3];
	e = h[4]; f = h[5]; g = h[6]; hh = h[7];
	for (i = 0; i < 64; i++) {
		uint32_t S1 = ROR32(e, 6) ^ ROR32(e, 11) ^ ROR32(e, 25);
		uint32_t ch = (e & f) ^ (~e & g);
		t1 = hh + S1 + ch + K256[i] + w[i];
		{
			uint32_t S0 = ROR32(a, 2) ^ ROR32(a, 13) ^ ROR32(a, 22);
			uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
			t2 = S0 + maj;
		}
		hh = g; g = f; f = e; e = d + t1;
		d = c; c = b; b = a; a = t1 + t2;
	}
	h[0] += a; h[1] += b; h[2] += c; h[3] += d;
	h[4] += e; h[5] += f; h[6] += g; h[7] += hh;
}

static void sha256_init(sha256_ctx *c)
{
	memcpy(c->h, IV256, sizeof IV256);
	c->bits = 0;
	c->n = 0;
}

static void sha256_update(sha256_ctx *c, const unsigned char *p, size_t len)
{
	c->bits += (unsigned long long)len * 8;
	while (len > 0) {
		size_t take = 64 - c->n;
		if (take > len)
			take = len;
		memcpy(c->buf + c->n, p, take);
		c->n += take;
		p += take;
		len -= take;
		if (c->n == 64) {
			sha256_block(c->h, c->buf);
			c->n = 0;
		}
	}
}

static void sha256_final(sha256_ctx *c, unsigned char out[32])
{
	unsigned char pad[72];
	unsigned long long bits = c->bits;
	size_t plen = (c->n < 56) ? (56 - c->n) : (120 - c->n);
	int i;
	pad[0] = 0x80;
	memset(pad + 1, 0, plen - 1);
	for (i = 0; i < 8; i++)
		pad[plen + i] = (unsigned char)(bits >> (56 - 8 * i));
	sha256_update(c, pad, plen + 8);
	for (i = 0; i < 8; i++) {
		out[4 * i] = (unsigned char)(c->h[i] >> 24);
		out[4 * i + 1] = (unsigned char)(c->h[i] >> 16);
		out[4 * i + 2] = (unsigned char)(c->h[i] >> 8);
		out[4 * i + 3] = (unsigned char)(c->h[i]);
	}
}

/* ---- SHA-512 / SHA-384 (one code path, different IV and length) ---- */

typedef struct {
	uint64_t h[8];
	uint64_t bits_lo, bits_hi;
	unsigned char buf[128];
	size_t n;
} sha512_ctx;

static const uint64_t K512[80] = {
	0x428a2f98d728ae22ULL, 0x7137449123ef65cdULL, 0xb5c0fbcfec4d3b2fULL,
	0xe9b5dba58189dbbcULL, 0x3956c25bf348b538ULL, 0x59f111f1b605d019ULL,
	0x923f82a4af194f9bULL, 0xab1c5ed5da6d8118ULL, 0xd807aa98a3030242ULL,
	0x12835b0145706fbeULL, 0x243185be4ee4b28cULL, 0x550c7dc3d5ffb4e2ULL,
	0x72be5d74f27b896fULL, 0x80deb1fe3b1696b1ULL, 0x9bdc06a725c71235ULL,
	0xc19bf174cf692694ULL, 0xe49b69c19ef14ad2ULL, 0xefbe4786384f25e3ULL,
	0x0fc19dc68b8cd5b5ULL, 0x240ca1cc77ac9c65ULL, 0x2de92c6f592b0275ULL,
	0x4a7484aa6ea6e483ULL, 0x5cb0a9dcbd41fbd4ULL, 0x76f988da831153b5ULL,
	0x983e5152ee66dfabULL, 0xa831c66d2db43210ULL, 0xb00327c898fb213fULL,
	0xbf597fc7beef0ee4ULL, 0xc6e00bf33da88fc2ULL, 0xd5a79147930aa725ULL,
	0x06ca6351e003826fULL, 0x142929670a0e6e70ULL, 0x27b70a8546d22ffcULL,
	0x2e1b21385c26c926ULL, 0x4d2c6dfc5ac42aedULL, 0x53380d139d95b3dfULL,
	0x650a73548baf63deULL, 0x766a0abb3c77b2a8ULL, 0x81c2c92e47edaee6ULL,
	0x92722c851482353bULL, 0xa2bfe8a14cf10364ULL, 0xa81a664bbc423001ULL,
	0xc24b8b70d0f89791ULL, 0xc76c51a30654be30ULL, 0xd192e819d6ef5218ULL,
	0xd69906245565a910ULL, 0xf40e35855771202aULL, 0x106aa07032bbd1b8ULL,
	0x19a4c116b8d2d0c8ULL, 0x1e376c085141ab53ULL, 0x2748774cdf8eeb99ULL,
	0x34b0bcb5e19b48a8ULL, 0x391c0cb3c5c95a63ULL, 0x4ed8aa4ae3418acbULL,
	0x5b9cca4f7763e373ULL, 0x682e6ff3d6b2b8a3ULL, 0x748f82ee5defb2fcULL,
	0x78a5636f43172f60ULL, 0x84c87814a1f0ab72ULL, 0x8cc702081a6439ecULL,
	0x90befffa23631e28ULL, 0xa4506cebde82bde9ULL, 0xbef9a3f7b2c67915ULL,
	0xc67178f2e372532bULL, 0xca273eceea26619cULL, 0xd186b8c721c0c207ULL,
	0xeada7dd6cde0eb1eULL, 0xf57d4f7fee6ed178ULL, 0x06f067aa72176fbaULL,
	0x0a637dc5a2c898a6ULL, 0x113f9804bef90daeULL, 0x1b710b35131c471bULL,
	0x28db77f523047d84ULL, 0x32caab7b40c72493ULL, 0x3c9ebe0a15c9bebcULL,
	0x431d67c49c100d4cULL, 0x4cc5d4becb3e42b6ULL, 0x597f299cfc657e2aULL,
	0x5fcb6fab3ad6faecULL, 0x6c44198c4a475817ULL,
};

static const uint64_t IV512[8] = {
	0x6a09e667f3bcc908ULL, 0xbb67ae8584caa73bULL, 0x3c6ef372fe94f82bULL,
	0xa54ff53a5f1d36f1ULL, 0x510e527fade682d1ULL, 0x9b05688c2b3e6c1fULL,
	0x1f83d9abfb41bd6bULL, 0x5be0cd19137e2179ULL,
};

static const uint64_t IV384[8] = {
	0xcbbb9d5dc1059ed8ULL, 0x629a292a367cd507ULL, 0x9159015a3070dd17ULL,
	0x152fecd8f70e5939ULL, 0x67332667ffc00b31ULL, 0x8eb44a8768581511ULL,
	0xdb0c2e0d64f98fa7ULL, 0x47b5481dbefa4fa4ULL,
};

#define ROR64(x, n) (((x) >> (n)) | ((x) << (64 - (n))))

static uint64_t be64(const unsigned char *p)
{
	return ((uint64_t)p[0] << 56) | ((uint64_t)p[1] << 48) |
	       ((uint64_t)p[2] << 40) | ((uint64_t)p[3] << 32) |
	       ((uint64_t)p[4] << 24) | ((uint64_t)p[5] << 16) |
	       ((uint64_t)p[6] << 8) | (uint64_t)p[7];
}

static void sha512_block(uint64_t h[8], const unsigned char *p)
{
	uint64_t w[80], a, b, c, d, e, f, g, hh, t1, t2;
	int i;
	for (i = 0; i < 16; i++)
		w[i] = be64(p + 8 * i);
	for (; i < 80; i++) {
		uint64_t s0 = ROR64(w[i - 15], 1) ^ ROR64(w[i - 15], 8) ^ (w[i - 15] >> 7);
		uint64_t s1 = ROR64(w[i - 2], 19) ^ ROR64(w[i - 2], 61) ^ (w[i - 2] >> 6);
		w[i] = w[i - 16] + s0 + w[i - 7] + s1;
	}
	a = h[0]; b = h[1]; c = h[2]; d = h[3];
	e = h[4]; f = h[5]; g = h[6]; hh = h[7];
	for (i = 0; i < 80; i++) {
		uint64_t S1 = ROR64(e, 14) ^ ROR64(e, 18) ^ ROR64(e, 41);
		uint64_t ch = (e & f) ^ (~e & g);
		t1 = hh + S1 + ch + K512[i] + w[i];
		{
			uint64_t S0 = ROR64(a, 28) ^ ROR64(a, 34) ^ ROR64(a, 39);
			uint64_t maj = (a & b) ^ (a & c) ^ (b & c);
			t2 = S0 + maj;
		}
		hh = g; g = f; f = e; e = d + t1;
		d = c; c = b; b = a; a = t1 + t2;
	}
	h[0] += a; h[1] += b; h[2] += c; h[3] += d;
	h[4] += e; h[5] += f; h[6] += g; h[7] += hh;
}

static void sha512_init_v(sha512_ctx *c, const uint64_t iv[8])
{
	memcpy(c->h, iv, sizeof c->h);
	c->bits_lo = c->bits_hi = 0;
	c->n = 0;
}

static void sha512_update(sha512_ctx *c, const unsigned char *p, size_t len)
{
	uint64_t add = (uint64_t)len * 8;
	uint64_t old = c->bits_lo;
	c->bits_lo += add;
	if (c->bits_lo < old)
		c->bits_hi++;
	while (len > 0) {
		size_t take = 128 - c->n;
		if (take > len)
			take = len;
		memcpy(c->buf + c->n, p, take);
		c->n += take;
		p += take;
		len -= take;
		if (c->n == 128) {
			sha512_block(c->h, c->buf);
			c->n = 0;
		}
	}
}

/* Writes the first outlen bytes of the hash result (48 for SHA-384). */
static void sha512_final(sha512_ctx *c, unsigned char *out, size_t outlen)
{
	unsigned char pad[144], lenb[16], tmp[64];
	size_t plen = (c->n < 112) ? (112 - c->n) : (240 - c->n);
	int i;
	pad[0] = 0x80;
	memset(pad + 1, 0, plen - 1);
	for (i = 0; i < 8; i++) {
		lenb[i] = (unsigned char)(c->bits_hi >> (56 - 8 * i));
		lenb[8 + i] = (unsigned char)(c->bits_lo >> (56 - 8 * i));
	}
	sha512_update(c, pad, plen);
	sha512_update(c, lenb, 16);
	for (i = 0; i < 8; i++) {
		tmp[8 * i] = (unsigned char)(c->h[i] >> 56);
		tmp[8 * i + 1] = (unsigned char)(c->h[i] >> 48);
		tmp[8 * i + 2] = (unsigned char)(c->h[i] >> 40);
		tmp[8 * i + 3] = (unsigned char)(c->h[i] >> 32);
		tmp[8 * i + 4] = (unsigned char)(c->h[i] >> 24);
		tmp[8 * i + 5] = (unsigned char)(c->h[i] >> 16);
		tmp[8 * i + 6] = (unsigned char)(c->h[i] >> 8);
		tmp[8 * i + 7] = (unsigned char)(c->h[i]);
	}
	memcpy(out, tmp, outlen);
}

/* ---- HMAC (RFC 2104) ---- */

static void hmac_sha256(const unsigned char *key, size_t klen,
			const unsigned char *msg, size_t mlen,
			unsigned char out[32])
{
	unsigned char k[64], inner[32];
	sha256_ctx c;
	size_t i;
	memset(k, 0, sizeof k);
	if (klen > 64) {
		sha256_init(&c);
		sha256_update(&c, key, klen);
		sha256_final(&c, k);
	} else {
		memcpy(k, key, klen);
	}
	sha256_init(&c);
	for (i = 0; i < 64; i++) {
		unsigned char b = k[i] ^ 0x36;
		sha256_update(&c, &b, 1);
	}
	sha256_update(&c, msg, mlen);
	sha256_final(&c, inner);
	sha256_init(&c);
	for (i = 0; i < 64; i++) {
		unsigned char b = k[i] ^ 0x5c;
		sha256_update(&c, &b, 1);
	}
	sha256_update(&c, inner, 32);
	sha256_final(&c, out);
}

static void hmac_sha512v(const unsigned char *key, size_t klen,
			 const unsigned char *msg, size_t mlen,
			 const uint64_t iv[8], unsigned char *out, size_t outlen)
{
	unsigned char k[128], inner[64];
	sha512_ctx c;
	size_t i;
	memset(k, 0, sizeof k);
	if (klen > 128) {
		sha512_init_v(&c, iv);
		sha512_update(&c, key, klen);
		sha512_final(&c, k, outlen);
		memset(k + outlen, 0, 128 - outlen);
	} else {
		memcpy(k, key, klen);
	}
	sha512_init_v(&c, iv);
	for (i = 0; i < 128; i++) {
		unsigned char b = k[i] ^ 0x36;
		sha512_update(&c, &b, 1);
	}
	sha512_update(&c, msg, mlen);
	sha512_final(&c, inner, outlen);
	sha512_init_v(&c, iv);
	for (i = 0; i < 128; i++) {
		unsigned char b = k[i] ^ 0x5c;
		sha512_update(&c, &b, 1);
	}
	sha512_update(&c, inner, outlen);
	sha512_final(&c, out, outlen);
}

/* ================================================================
 * base64url without padding (RFC 4648 §5 — used by JWS)
 * ================================================================ */

static int b64url_encode(const unsigned char *in, size_t n, char **out,
			 size_t *outlen)
{
	static const char T[] =
		"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
	size_t len = (n / 3) * 4 + (n % 3 ? n % 3 + 1 : 0);
	char *o = malloc(len + 1);
	size_t i = 0, j = 0;
	if (!o)
		return -1;
	while (i + 3 <= n) {
		unsigned v = ((unsigned)in[i] << 16) | ((unsigned)in[i + 1] << 8) |
			     in[i + 2];
		o[j++] = T[(v >> 18) & 63];
		o[j++] = T[(v >> 12) & 63];
		o[j++] = T[(v >> 6) & 63];
		o[j++] = T[v & 63];
		i += 3;
	}
	if (n - i == 1) {
		unsigned v = (unsigned)in[i] << 16;
		o[j++] = T[(v >> 18) & 63];
		o[j++] = T[(v >> 12) & 63];
	} else if (n - i == 2) {
		unsigned v = ((unsigned)in[i] << 16) | ((unsigned)in[i + 1] << 8);
		o[j++] = T[(v >> 18) & 63];
		o[j++] = T[(v >> 12) & 63];
		o[j++] = T[(v >> 6) & 63];
	}
	o[len] = '\0';
	*out = o;
	*outlen = len;
	return 0;
}

/* Strict-decode, lenient-accept: characters outside the alphabet → -1.
 * Up to2 trailing '=' padding is accepted (some JWT libraries send
 * it); non-zero leftover bits are ignored. */
static int b64url_decode(const char *in, size_t n, unsigned char **out,
			 size_t *outlen)
{
	unsigned char *o;
	size_t cap, j = 0, i;
	unsigned acc = 0;
	int bits = 0;
	while (n > 0 && in[n - 1] == '=')
		n--;
	if (n % 4 == 1)
		return -1;
	cap = (n / 4) * 3 + 3;
	o = malloc(cap ? cap : 1);
	if (!o)
		return -1;
	for (i = 0; i < n; i++) {
		char c = in[i];
		int v;
		if (c >= 'A' && c <= 'Z')
			v = c - 'A';
		else if (c >= 'a' && c <= 'z')
			v = c - 'a' + 26;
		else if (c >= '0' && c <= '9')
			v = c - '0' + 52;
		else if (c == '-')
			v = 62;
		else if (c == '_')
			v = 63;
		else {
			free(o);
			return -1;
		}
		acc = (acc << 6) | (unsigned)v;
		bits += 6;
		if (bits >= 8) {
			bits -= 8;
			o[j++] = (unsigned char)((acc >> bits) & 0xff);
		}
	}
	*out = o;
	*outlen = j;
	return 0;
}

/* ================================================================
 * Mini JSON parser — full validation + top-level claim extraction.
 * Enough for JWT's needs: confirming a valid object, fetching numbers
 * (exp/nbf) and strings (alg) at the top level without building a
 * tree. (verify()/decode() additionally run the full parser below to
 * build the returned object.)
 * ================================================================ */

static const char *json_ws(const char *p, const char *e)
{
	while (p < e && (*p == ' ' || *p == '\t' || *p == '\n' || *p == '\r'))
		p++;
	return p;
}

/* p points at the opening quote; returns AFTER the closing quote. */
static const char *json_string_end(const char *p, const char *e)
{
	p++;
	while (p < e) {
		unsigned char c = (unsigned char)*p;
		if (c == '"')
			return p + 1;
		if (c < 0x20)
			return NULL;
		if (c == '\\') {
			p++;
			if (p >= e)
				return NULL;
			if (*p == '"' || *p == '\\' || *p == '/' || *p == 'b' ||
			    *p == 'f' || *p == 'n' || *p == 'r' || *p == 't') {
				p++;
			} else if (*p == 'u') {
				int i;
				p++;
				for (i = 0; i < 4; i++, p++) {
					if (p >= e ||
					    !isxdigit((unsigned char)*p))
						return NULL;
				}
			} else {
				return NULL;
			}
		} else {
			p++;
		}
	}
	return NULL;
}

/* Validate one JSON value → pointer after the value, or NULL. */
static const char *json_nilai(const char *p, const char *e, int depth)
{
	p = json_ws(p, e);
	if (depth > JWT_MAX_JSON_DEPTH || p >= e)
		return NULL;
	if (*p == '{') {
		p = json_ws(p + 1, e);
		if (p < e && *p == '}')
			return p + 1;
		for (;;) {
			p = json_ws(p, e);
			if (p >= e || *p != '"')
				return NULL;
			p = json_string_end(p, e);
			if (!p)
				return NULL;
			p = json_ws(p, e);
			if (p >= e || *p != ':')
				return NULL;
			p = json_nilai(p + 1, e, depth + 1);
			if (!p)
				return NULL;
			p = json_ws(p, e);
			if (p >= e)
				return NULL;
			if (*p == ',') {
				p++;
				continue;
			}
			if (*p == '}')
				return p + 1;
			return NULL;
		}
	}
	if (*p == '[') {
		p = json_ws(p + 1, e);
		if (p < e && *p == ']')
			return p + 1;
		for (;;) {
			p = json_nilai(p, e, depth + 1);
			if (!p)
				return NULL;
			p = json_ws(p, e);
			if (p >= e)
				return NULL;
			if (*p == ',') {
				p++;
				continue;
			}
			if (*p == ']')
				return p + 1;
			return NULL;
		}
	}
	if (*p == '"')
		return json_string_end(p, e);
	if (p + 4 <= e && memcmp(p, "true", 4) == 0)
		return p + 4;
	if (p + 5 <= e && memcmp(p, "false", 5) == 0)
		return p + 5;
	if (p + 4 <= e && memcmp(p, "null", 4) == 0)
		return p + 4;
	/* number */
	if (*p == '-')
		p++;
	if (p >= e)
		return NULL;
	if (*p == '0') {
		p++;
	} else if (*p >= '1' && *p <= '9') {
		while (p < e && *p >= '0' && *p <= '9')
			p++;
	} else {
		return NULL;
	}
	if (p < e && *p == '.') {
		p++;
		if (p >= e || *p < '0' || *p > '9')
			return NULL;
		while (p < e && *p >= '0' && *p <= '9')
			p++;
	}
	if (p < e && (*p == 'e' || *p == 'E')) {
		p++;
		if (p < e && (*p == '+' || *p == '-'))
			p++;
		if (p >= e || *p < '0' || *p > '9')
			return NULL;
		while (p < e && *p >= '0' && *p <= '9')
			p++;
	}
	return p;
}

/* The whole text must be exactly ONE JSON object value (no trailing
 * garbage). */
static int json_obj(const char *js, size_t n)
{
	const char *p = json_ws(js, js + n);
	if (p >= js + n || *p != '{')
		return 0;
	p = json_nilai(p, js + n, 0);
	if (!p)
		return 0;
	return json_ws(p, js + n) == js + n;
}

/* Unescape a JSON string fragment into dst (capacity cap including
 * NUL). \u escapes above ASCII become '?' (JWT keys & alg are ASCII).
 * 0 = success, -1 = failure. */
static int json_unescape(const char *src, size_t n, char *dst, size_t cap)
{
	size_t i = 0, j = 0;
	if (cap == 0)
		return -1;
	while (i < n) {
		char c = src[i];
		if (c != '\\') {
			if (j + 1 >= cap)
				return -1;
			dst[j++] = c;
			i++;
			continue;
		}
		i++;
		if (i >= n)
			return -1;
		switch (src[i]) {
		case '"': case '\\': case '/':
			if (j + 1 >= cap) return -1;
			dst[j++] = src[i];
			i++;
			break;
		case 'b': if (j + 1 >= cap) return -1; dst[j++] = '\b'; i++; break;
		case 'f': if (j + 1 >= cap) return -1; dst[j++] = '\f'; i++; break;
		case 'n': if (j + 1 >= cap) return -1; dst[j++] = '\n'; i++; break;
		case 'r': if (j + 1 >= cap) return -1; dst[j++] = '\r'; i++; break;
		case 't': if (j + 1 >= cap) return -1; dst[j++] = '\t'; i++; break;
		case 'u': {
			unsigned v = 0;
			int k;
			if (i + 4 >= n)
				return -1;
			for (k = 0; k < 4; k++) {
				char h = src[i + 1 + k];
				v <<= 4;
				if (h >= '0' && h <= '9') v += (unsigned)(h - '0');
				else if (h >= 'a' && h <= 'f') v += (unsigned)(h - 'a' + 10);
				else if (h >= 'A' && h <= 'F') v += (unsigned)(h - 'A' + 10);
				else return -1;
			}
			if (j + 1 >= cap)
				return -1;
			dst[j++] = v < 0x80 ? (char)v : '?';
			i += 5;
			break;
		}
		default:
			return -1;
		}
	}
	dst[j] = '\0';
	return 0;
}

/* Look up a top-level key in a JSON object that was ALREADY validated
 * by json_obj. Fills *num (number type) or strbuf (string type).
 * 1 = found & type matches, 0 = not found, -1 = corrupt, -2 = found
 * but the type is not the requested one. */
static int json_cari(const char *js, size_t n, const char *key, double *num,
		     char *strbuf, size_t cap)
{
	const char *e = js + n;
	const char *p = json_ws(js, e);
	if (p >= e || *p != '{')
		return -1;
	p = json_ws(p + 1, e);
	if (p < e && *p == '}')
		return 0;
	for (;;) {
		const char *kend, *v, *ve;
		char kbuf[64];
		int cocok;
		p = json_ws(p, e);
		if (p >= e || *p != '"')
			return -1;
		kend = json_string_end(p, e);
		if (!kend)
			return -1;
		if (json_unescape(p + 1, (size_t)(kend - 1 - (p + 1)), kbuf,
				  sizeof kbuf) != 0)
			return -1;
		p = json_ws(kend, e);
		if (p >= e || *p != ':')
			return -1;
		v = json_ws(p + 1, e);
		if (v >= e)
			return -1;
		ve = json_nilai(v, e, 1);
		if (!ve)
			return -1;
		cocok = strcmp(kbuf, key) == 0;
		if (cocok) {
			if (*v == '-' || (*v >= '0' && *v <= '9')) {
				if (!num)
					return -2;
				{
					char tmp[64];
					size_t L = (size_t)(ve - v);
					if (L >= sizeof tmp)
						return -2;
					memcpy(tmp, v, L);
					tmp[L] = '\0';
					*num = strtod(tmp, NULL);
				}
				return 1;
			}
			if (*v == '"') {
				if (!strbuf)
					return -2;
				if (json_unescape(v + 1,
						  (size_t)(ve - 1 - (v + 1)),
						  strbuf, cap) != 0)
					return -2;
				return 1;
			}
			return -2;
		}
		p = json_ws(ve, e);
		if (p < e && *p == ',') {
			p++;
			continue;
		}
		if (p < e && *p == '}')
			return 0;
		return -1;
	}
}

/* ================================================================
 * JSON builder — GaLang value → JSON text (used by sign(object)).
 * Growable buffer; every helper returns -1 on allocation failure.
 * ================================================================ */

typedef struct {
	char *p;
	size_t len, cap;
} jbuf;

static int jb_grow(jbuf *b, size_t need)
{
	size_t ncap;
	char *np;
	if (b->len + need + 1 <= b->cap)
		return 0;
	ncap = b->cap ? b->cap : 256;
	while (ncap < b->len + need + 1)
		ncap *= 2;
	np = realloc(b->p, ncap);
	if (!np)
		return -1;
	b->p = np;
	b->cap = ncap;
	return 0;
}

static int jb_add(jbuf *b, const char *s, size_t n)
{
	if (jb_grow(b, n) != 0)
		return -1;
	if (n)
		memcpy(b->p + b->len, s, n);
	b->len += n;
	b->p[b->len] = '\0';
	return 0;
}

static int jb_addc(jbuf *b, char c)
{
	return jb_add(b, &c, 1);
}

/* Emit a JSON string literal (quotes + escaping). */
static int jb_string(jbuf *b, const char *s, size_t n)
{
	static const char hex[] = "0123456789abcdef";
	size_t i;
	if (jb_addc(b, '"') != 0)
		return -1;
	for (i = 0; i < n; i++) {
		unsigned char c = (unsigned char)s[i];
		const char *esc = NULL;
		char u[6];
		switch (c) {
		case '"': esc = "\\\""; break;
		case '\\': esc = "\\\\"; break;
		case '\b': esc = "\\b"; break;
		case '\f': esc = "\\f"; break;
		case '\n': esc = "\\n"; break;
		case '\r': esc = "\\r"; break;
		case '\t': esc = "\\t"; break;
		default: break;
		}
		if (esc) {
			if (jb_add(b, esc, 2) != 0)
				return -1;
		} else if (c < 0x20) {
			/* other control characters as \u00XX */
			u[0] = '\\'; u[1] = 'u'; u[2] = '0'; u[3] = '0';
			u[4] = hex[(c >> 4) & 0xf];
			u[5] = hex[c & 0xf];
			if (jb_add(b, u, 6) != 0)
				return -1;
		} else if (jb_addc(b, (char)c) != 0) {
			return -1;
		}
	}
	return jb_addc(b, '"');
}

/* Serialize one GaLang value as JSON text.
 * Returns 0 = success, -1 = not serializable (function/promise/error
 * value, non-finite float, or deeper than JWT_MAX_JSON_DEPTH),
 * -2 = out of memory. */
static int json_dari(gne_ctx *ctx, gne_handle h, jbuf *b, int depth)
{
	int32_t t;
	if (depth > JWT_MAX_JSON_DEPTH)
		return -1;
	if (api->type_of(ctx, h, &t) != 0)
		return -1;
	switch (t) {
	case GNE_NULL:
		return jb_add(b, "null", 4);
	case GNE_BOOL: {
		int v = 0;
		if (api->get_bool(ctx, h, &v) != 0)
			return -1;
		return v ? jb_add(b, "true", 4) : jb_add(b, "false", 5);
	}
	case GNE_INT: {
		int64_t v = 0;
		char tmp[24];
		int n;
		if (api->get_int(ctx, h, &v) != 0)
			return -1;
		n = snprintf(tmp, sizeof tmp, "%lld", (long long)v);
		if (n < 0 || (size_t)n >= sizeof tmp)
			return -1;
		return jb_add(b, tmp, (size_t)n);
	}
	case GNE_FLOAT: {
		double v = 0;
		char tmp[40];
		int n;
		if (api->get_float(ctx, h, &v) != 0)
			return -1;
		/* NaN/±inf are not representable in JSON */
		if (!(v == v && v - v == 0.0))
			return -1;
		/* shortest representation that round-trips */
		n = snprintf(tmp, sizeof tmp, "%.15g", v);
		if (n < 0 || (size_t)n >= sizeof tmp || strtod(tmp, NULL) != v) {
			n = snprintf(tmp, sizeof tmp, "%.16g", v);
			if (n < 0 || (size_t)n >= sizeof tmp ||
			    strtod(tmp, NULL) != v)
				n = snprintf(tmp, sizeof tmp, "%.17g", v);
		}
		if (n < 0 || (size_t)n >= sizeof tmp)
			return -1;
		return jb_add(b, tmp, (size_t)n);
	}
	case GNE_STRING: {
		size_t n = 0;
		char *s;
		int rc;
		if (api->str_len(ctx, h, &n) != 0)
			return -1;
		s = malloc(n + 1);
		if (!s)
			return -2;
		if (n > 0 && api->str_copy(ctx, h, s, n + 1) < 0) {
			free(s);
			return -1;
		}
		s[n] = '\0';
		rc = jb_string(b, s, n);
		free(s);
		return rc;
	}
	case GNE_ARRAY: {
		size_t n = 0, i;
		if (api->len(ctx, h, &n) != 0)
			return -1;
		if (jb_addc(b, '[') != 0)
			return -2;
		for (i = 0; i < n; i++) {
			gne_handle el;
			int rc;
			if (i && jb_addc(b, ',') != 0)
				return -2;
			if (api->arr_get(ctx, h, i, &el) != 0)
				return -1;
			rc = json_dari(ctx, el, b, depth + 1);
			if (rc != 0)
				return rc;
		}
		return jb_addc(b, ']');
	}
	case GNE_OBJECT: {
		gne_handle keys;
		size_t n = 0, i;
		int first = 1, rc = 0;
		if (api->obj_keys(ctx, h, &keys) != 0)
			return -1;
		if (api->len(ctx, keys, &n) != 0)
			return -1;
		if (jb_addc(b, '{') != 0)
			return -2;
		for (i = 0; i < n; i++) {
			gne_handle kh, val;
			size_t kn = 0;
			char *k;
			if (first) {
				first = 0;
			} else if (jb_addc(b, ',') != 0) {
				rc = -2;
				break;
			}
			if (api->arr_get(ctx, keys, i, &kh) != 0 ||
			    api->str_len(ctx, kh, &kn) != 0) {
				rc = -1;
				break;
			}
			k = malloc(kn + 1);
			if (!k) {
				rc = -2;
				break;
			}
			if (kn > 0 && api->str_copy(ctx, kh, k, kn + 1) < 0) {
				free(k);
				rc = -1;
				break;
			}
			k[kn] = '\0';
			rc = jb_string(b, k, kn);
			if (rc == 0 && jb_addc(b, ':') != 0)
				rc = -2;
			if (rc == 0 && api->obj_get(ctx, h, k, &val) != 0)
				rc = -1;
			free(k);
			if (rc != 0)
				break;
			rc = json_dari(ctx, val, b, depth + 1);
			if (rc != 0)
				break;
		}
		if (rc != 0)
			return rc;
		return jb_addc(b, '}');
	}
	default:
		/* function, promise, error — no JSON representation */
		return -1;
	}
}

/* ================================================================
 * JSON reader — JSON text → GaLang value (used by verify/decode).
 * Errors are reported through the caller's JWT-level messages; this
 * layer only signals success/failure.
 * ================================================================ */

/* Write the UTF-8 encoding of cp into o, returning the end pointer. */
static char *utf8_put(char *o, unsigned cp)
{
	if (cp < 0x80) {
		*o++ = (char)cp;
	} else if (cp < 0x800) {
		*o++ = (char)(0xc0 | (cp >> 6));
		*o++ = (char)(0x80 | (cp & 0x3f));
	} else if (cp < 0x10000) {
		*o++ = (char)(0xe0 | (cp >> 12));
		*o++ = (char)(0x80 | ((cp >> 6) & 0x3f));
		*o++ = (char)(0x80 | (cp & 0x3f));
	} else {
		*o++ = (char)(0xf0 | (cp >> 18));
		*o++ = (char)(0x80 | ((cp >> 12) & 0x3f));
		*o++ = (char)(0x80 | ((cp >> 6) & 0x3f));
		*o++ = (char)(0x80 | (cp & 0x3f));
	}
	return o;
}

/* Decode a JSON string body (src = bytes after the opening quote, n =
 * length without the closing quote) into a malloc'd NUL-terminated
 * buffer with *outlen set. Full \u handling: UTF-8 output, surrogate
 * pairs combined, lone surrogates → U+FFFD. NULL on failure (corrupt
 * escape or allocation error). Output never exceeds n bytes. */
static char *json_str_decode(const char *src, size_t n, size_t *outlen)
{
	char *o = malloc(n + 1);
	size_t i = 0, j = 0;
	if (!o)
		return NULL;
	while (i < n) {
		char c = src[i];
		if (c != '\\') {
			o[j++] = c;
			i++;
			continue;
		}
		i++;
		if (i >= n)
			goto fail;
		switch (src[i]) {
		case '"': case '\\': case '/':
			o[j++] = src[i];
			i++;
			break;
		case 'b': o[j++] = '\b'; i++; break;
		case 'f': o[j++] = '\f'; i++; break;
		case 'n': o[j++] = '\n'; i++; break;
		case 'r': o[j++] = '\r'; i++; break;
		case 't': o[j++] = '\t'; i++; break;
		case 'u': {
			unsigned v = 0;
			int k;
			if (i + 4 >= n)
				goto fail;
			for (k = 1; k <= 4; k++) {
				char hx = src[i + k];
				v <<= 4;
				if (hx >= '0' && hx <= '9')
					v += (unsigned)(hx - '0');
				else if (hx >= 'a' && hx <= 'f')
					v += (unsigned)(hx - 'a' + 10);
				else if (hx >= 'A' && hx <= 'F')
					v += (unsigned)(hx - 'A' + 10);
				else
					goto fail;
			}
			i += 5;
			if (v >= 0xd800 && v <= 0xdbff) {
				/* high surrogate — needs a low surrogate pair */
				unsigned lo = 0;
				int paired = 0;
				if (i + 6 <= n && src[i] == '\\' &&
				    src[i + 1] == 'u') {
					for (k = 2; k <= 5; k++) {
						char hx = src[i + k];
						lo <<= 4;
						if (hx >= '0' && hx <= '9')
							lo += (unsigned)(hx - '0');
						else if (hx >= 'a' && hx <= 'f')
							lo += (unsigned)(hx - 'a' + 10);
						else if (hx >= 'A' && hx <= 'F')
							lo += (unsigned)(hx - 'A' + 10);
						else
							break;
					}
					if (k == 6 && lo >= 0xdc00 &&
					    lo <= 0xdfff) {
						j = (size_t)(utf8_put(o + j,
							0x10000u +
							((v - 0xd800u) << 10) +
							(lo - 0xdc00u)) - o);
						i += 6;
						paired = 1;
					}
				}
				if (!paired) {
					j = (size_t)(utf8_put(o + j, 0xfffd) - o);
				}
			} else if (v >= 0xdc00 && v <= 0xdfff) {
				/* lone low surrogate */
				j = (size_t)(utf8_put(o + j, 0xfffd) - o);
			} else {
				j = (size_t)(utf8_put(o + j, v) - o);
			}
			break;
		}
		default:
			goto fail;
		}
	}
	o[j] = '\0';
	*outlen = j;
	return o;
fail:
	free(o);
	return NULL;
}

/* Parse exactly one JSON value starting at *pp; on success advance
 * *pp past the value, write the handle to *out, and return 0.
 * Returns -1 on any syntax/type failure or depth overflow. Handles
 * created along the way are call-scoped temps (released by the host
 * if never returned). */
static int json_baca(gne_ctx *ctx, const char **pp, const char *e,
		     int depth, gne_handle *out)
{
	const char *p = json_ws(*pp, e);
	if (depth > JWT_MAX_JSON_DEPTH || p >= e)
		return -1;
	switch (*p) {
	case '{': {
		gne_handle o = api->object(ctx);
		p = json_ws(p + 1, e);
		if (p < e && *p == '}') {
			*pp = p + 1;
			*out = o;
			return 0;
		}
		for (;;) {
			gne_handle val;
			const char *kend;
			char *k;
			size_t kn = 0;
			p = json_ws(p, e);
			if (p >= e || *p != '"')
				return -1;
			kend = json_string_end(p, e);
			if (!kend)
				return -1;
			k = json_str_decode(p + 1,
					    (size_t)(kend - 1 - (p + 1)), &kn);
			if (!k)
				return -1;
			p = json_ws(kend, e);
			if (p >= e || *p != ':') {
				free(k);
				return -1;
			}
			p++;
			if (json_baca(ctx, &p, e, depth + 1, &val) != 0) {
				free(k);
				return -1;
			}
			if (api->obj_set(ctx, o, k, val) != 0) {
				free(k);
				return -1;
			}
			free(k);
			p = json_ws(p, e);
			if (p >= e)
				return -1;
			if (*p == ',') {
				p++;
				continue;
			}
			if (*p == '}') {
				*pp = p + 1;
				*out = o;
				return 0;
			}
			return -1;
		}
	}
	case '[': {
		gne_handle a = api->array(ctx);
		p = json_ws(p + 1, e);
		if (p < e && *p == ']') {
			*pp = p + 1;
			*out = a;
			return 0;
		}
		for (;;) {
			gne_handle val;
			if (json_baca(ctx, &p, e, depth + 1, &val) != 0)
				return -1;
			if (api->arr_push(ctx, a, val) != 0)
				return -1;
			p = json_ws(p, e);
			if (p >= e)
				return -1;
			if (*p == ',') {
				p++;
				continue;
			}
			if (*p == ']') {
				*pp = p + 1;
				*out = a;
				return 0;
			}
			return -1;
		}
	}
	case '"': {
		const char *kend = json_string_end(p, e);
		size_t n = 0;
		char *s;
		if (!kend)
			return -1;
		s = json_str_decode(p + 1, (size_t)(kend - 1 - (p + 1)), &n);
		if (!s)
			return -1;
		*out = api->string(ctx, s, n);
		free(s);
		*pp = kend;
		return 0;
	}
	case 't':
		if (e - p >= 4 && memcmp(p, "true", 4) == 0) {
			*out = api->bool_new(ctx, 1);
			*pp = p + 4;
			return 0;
		}
		return -1;
	case 'f':
		if (e - p >= 5 && memcmp(p, "false", 5) == 0) {
			*out = api->bool_new(ctx, 0);
			*pp = p + 5;
			return 0;
		}
		return -1;
	case 'n':
		if (e - p >= 4 && memcmp(p, "null", 4) == 0) {
			*out = api->null(ctx);
			*pp = p + 4;
			return 0;
		}
		return -1;
	default: {
		/* number: validate with the mini parser, then pick int
		 * vs float from the shape of the text */
		const char *s = p, *ve;
		char stack[64], *tmp = stack;
		size_t L;
		int isfloat = 0, heap = 0;
		ve = json_nilai(s, e, depth);
		if (!ve)
			return -1;
		for (p = s; p < ve; p++) {
			if (*p == '.' || *p == 'e' || *p == 'E') {
				isfloat = 1;
				break;
			}
		}
		L = (size_t)(ve - s);
		if (L >= sizeof stack) {
			tmp = malloc(L + 1);
			if (!tmp)
				return -1;
			heap = 1;
		}
		memcpy(tmp, s, L);
		tmp[L] = '\0';
		if (!isfloat) {
			char *end = NULL;
			long long iv;
			errno = 0;
			iv = strtoll(tmp, &end, 10);
			if (end && *end == '\0' && errno != ERANGE) {
				*out = api->int_new(ctx, (int64_t)iv);
				if (heap)
					free(tmp);
				*pp = ve;
				return 0;
			}
		}
		*out = api->float_new(ctx, strtod(tmp, NULL));
		if (heap)
			free(tmp);
		*pp = ve;
		return 0;
	}
	}
}

/* Parse a whole buffer that must be exactly ONE JSON object value.
 * Returns the object handle, or 0 on any failure. */
static gne_handle json_to_object(gne_ctx *ctx, const char *js, size_t n,
				int *ok)
{
	const char *p = json_ws(js, js + n);
	gne_handle out = 0;
	*ok = 0;
	if (p >= js + n || *p != '{')
		return 0;
	if (json_baca(ctx, &p, js + n, 0, &out) != 0)
		return 0;
	if (json_ws(p, js + n) != js + n)
		return 0;
	*ok = 1;
	return out;
}

/* Read a top-level time claim from a parsed claims object.
 * 0 = absent, 1 = number found, -1 = present but not a number. */
static int claim_number(gne_ctx *ctx, gne_handle obj, const char *key,
		       double *out)
{
	gne_handle v;
	int32_t t;
	if (api->obj_get(ctx, obj, key, &v) != 0)
		return 0;
	if (api->type_of(ctx, v, &t) != 0)
		return -1;
	if (t == GNE_INT) {
		int64_t i = 0;
		if (api->get_int(ctx, v, &i) != 0)
			return -1;
		*out = (double)i;
		return 1;
	}
	if (t == GNE_FLOAT) {
		double d = 0;
		if (api->get_float(ctx, v, &d) != 0)
			return -1;
		*out = d;
		return 1;
	}
	return -1;
}

/* ================================================================
 * Argument utilities
 * ================================================================ */

/* Fetch an argument as a strict string (other types → type_error).
 * The result is a malloc'd NUL-terminated buffer the caller must free. */
static char *arg_str(gne_ctx *ctx, gne_handle h, size_t *len)
{
	int32_t t;
	size_t n;
	char *out;
	if (api->type_of(ctx, h, &t) != 0 || t != GNE_STRING ||
	    api->str_len(ctx, h, &n) != 0) {
		api->throw(ctx, "type_error", 500, "argument must be a string");
		return NULL;
	}
	out = malloc(n + 1);
	if (!out) {
		api->throw(ctx, "jwt_oom", 500, "memory allocation failed");
		return NULL;
	}
	if (n > 0 && api->str_copy(ctx, h, out, n + 1) < 0) {
		free(out);
		api->throw(ctx, "type_error", 500,
			   "argument string could not be read");
		return NULL;
	}
	out[n] = '\0';
	*len = n;
	return out;
}

/* Constant-time comparison. */
static int ct_eq(const unsigned char *a, const unsigned char *b, size_t n)
{
	unsigned char d = 0;
	size_t i;
	for (i = 0; i < n; i++)
		d |= (unsigned char)(a[i] ^ b[i]);
	return d == 0;
}

/* True when alg is on the HS256/HS384/HS512 allowlist. */
static int alg_valid(const char *alg)
{
	return strcmp(alg, "HS256") == 0 || strcmp(alg, "HS384") == 0 ||
	       strcmp(alg, "HS512") == 0;
}

/* Pick the HMAC matching alg; -1 = unknown alg. */
static int hmac_pilih(const char *alg, const unsigned char *key, size_t klen,
		      const unsigned char *msg, size_t mlen,
		      unsigned char *out, size_t *outlen)
{
	if (strcmp(alg, "HS256") == 0) {
		hmac_sha256(key, klen, msg, mlen, out);
		*outlen = 32;
		return 0;
	}
	if (strcmp(alg, "HS384") == 0) {
		hmac_sha512v(key, klen, msg, mlen, IV384, out, 48);
		*outlen = 48;
		return 0;
	}
	if (strcmp(alg, "HS512") == 0) {
		hmac_sha512v(key, klen, msg, mlen, IV512, out, 64);
		*outlen = 64;
		return 0;
	}
	return -1;
}

/* Split token "a.b.c" into three segments (pointing into the original
 * text). 0 = success, -1 = wrong structure. */
static int token_plit(const char *tok, size_t tlen, const char **h, size_t *hn,
		      const char **p, size_t *pn, const char **s, size_t *sn)
{
	const char *d1, *d2;
	d1 = memchr(tok, '.', tlen);
	if (!d1)
		return -1;
	d2 = memchr(d1 + 1, '.', (size_t)(tok + tlen - (d1 + 1)));
	if (!d2)
		return -1;
	if (memchr(d2 + 1, '.', (size_t)(tok + tlen - (d2 + 1))))
		return -1;
	*h = tok;
	*hn = (size_t)(d1 - tok);
	*p = d1 + 1;
	*pn = (size_t)(d2 - (d1 + 1));
	*s = d2 + 1;
	*sn = (size_t)(tok + tlen - (d2 + 1));
	if (*hn == 0 || *pn == 0 || *sn == 0)
		return -1;
	return 0;
}

/* ================================================================
 * jwt.sign(claims, secret[, alg]) → token string
 * claims: an OBJECT (serialized internally via api->obj_keys, ABI 2)
 * or a raw JSON object string.
 * ================================================================ */
static int jwt_sign(gne_ctx *ctx, int argc, const gne_handle *argv,
		    gne_handle *ret)
{
	size_t clen = 0, klen = 0, alen = 0;
	char *claims = NULL, *secret = NULL, *algarg = NULL;
	const char *alg = "HS256";
	char header[64], *b64h = NULL, *b64p = NULL, *signing = NULL,
	     *b64s = NULL, *token = NULL;
	size_t b64hlen = 0, b64plen = 0, signlen = 0, b64slen = 0, toklen = 0;
	unsigned char sig[64];
	size_t siglen = 0;
	int rc = -1, from_obj = 0;
	int32_t ctyp = 0;
	jbuf jb = { NULL, 0, 0 };

	/* claims: object → serialized here; string → validated raw JSON */
	if (api->type_of(ctx, argv[0], &ctyp) != 0)
		ctyp = -1;
	if (ctyp == GNE_OBJECT) {
		int src = json_dari(ctx, argv[0], &jb, 0);
		if (src == -2)
			goto oom;
		if (src != 0) {
			api->throw(ctx, "type_error", 500,
				   "claims contain a value that cannot be serialized to JSON (function, promise, or too deep)");
			goto beres;
		}
		from_obj = 1;
		claims = jb.p;
		clen = jb.len;
		if (!claims)
			goto oom;
	} else if (ctyp == GNE_STRING) {
		claims = arg_str(ctx, argv[0], &clen);
		if (!claims)
			goto beres;
	} else {
		api->throw(ctx, "type_error", 500,
			   "claims must be an object or a JSON object string");
		goto beres;
	}
	secret = arg_str(ctx, argv[1], &klen);
	if (!secret)
		goto beres;
	if (argc >= 3) {
		algarg = arg_str(ctx, argv[2], &alen);
		if (!algarg)
			goto beres;
		alg = algarg;
	}
	if (!alg_valid(alg)) {
		api->throw(ctx, "type_error", 500,
			   "alg must be HS256, HS384, or HS512");
		goto beres;
	}
	if (klen == 0) {
		api->throw(ctx, "type_error", 500, "secret must not be empty");
		goto beres;
	}
	if (clen == 0) {
		api->throw(ctx, "type_error", 500, "claims must not be empty");
		goto beres;
	}
	if (clen > JWT_MAX_CLAIMS) {
		api->throw(ctx, "type_error", 500,
			   "claims exceed the 1 MiB limit");
		goto beres;
	}
	/* A serialized object is valid JSON by construction; a raw string
	 * still needs full validation. */
	if (!from_obj && !json_obj(claims, clen)) {
		api->throw(ctx, "jwt_error", 400,
			   "claims string must be a valid JSON object");
		goto beres;
	}

	snprintf(header, sizeof header, "{\"alg\":\"%s\",\"typ\":\"JWT\"}", alg);
	if (b64url_encode((const unsigned char *)header, strlen(header), &b64h,
			  &b64hlen) != 0 ||
	    b64url_encode((const unsigned char *)claims, clen, &b64p,
			  &b64plen) != 0)
		goto oom;
	signlen = b64hlen + 1 + b64plen;
	signing = malloc(signlen + 1);
	if (!signing)
		goto oom;
	memcpy(signing, b64h, b64hlen);
	signing[b64hlen] = '.';
	memcpy(signing + b64hlen + 1, b64p, b64plen);
	signing[signlen] = '\0';

	hmac_pilih(alg, (const unsigned char *)secret, klen,
		   (const unsigned char *)signing, signlen, sig, &siglen);
	if (b64url_encode(sig, siglen, &b64s, &b64slen) != 0)
		goto oom;
	toklen = signlen + 1 + b64slen;
	token = malloc(toklen + 1);
	if (!token)
		goto oom;
	memcpy(token, signing, signlen);
	token[signlen] = '.';
	memcpy(token + signlen + 1, b64s, b64slen);
	token[toklen] = '\0';

	*ret = api->string(ctx, token, toklen);
	rc = 0;
	goto beres;

oom:
	api->throw(ctx, "jwt_oom", 500, "memory allocation failed");
beres:
	free(claims);
	free(secret);
	free(algarg);
	free(b64h);
	free(b64p);
	free(signing);
	free(b64s);
	free(token);
	return rc;
}

/* ================================================================
 * jwt.verify(token, secret) → claims OBJECT (signature + exp/nbf)
 * ================================================================ */
static int jwt_verify(gne_ctx *ctx, int argc, const gne_handle *argv,
		      gne_handle *ret)
{
	size_t tlen = 0, klen = 0;
	char *tok = NULL, *secret = NULL;
	const char *hp, *pp, *sp;
	size_t hn, pn, sn;
	unsigned char *hraw = NULL, *praw = NULL, *sraw = NULL, *mac = NULL;
	size_t hlen = 0, plen = 0, slen = 0, maclen = 0;
	char alg[32];
	double num = 0;
	time_t now;
	int rc = -1, a, ok = 0;
	gne_handle claims = 0;

	(void)argc;
	tok = arg_str(ctx, argv[0], &tlen);
	if (!tok)
		goto beres;
	secret = arg_str(ctx, argv[1], &klen);
	if (!secret)
		goto beres;
	if (klen == 0) {
		api->throw(ctx, "type_error", 500, "secret must not be empty");
		goto beres;
	}
	if (tlen > JWT_MAX_TOKEN) {
		api->throw(ctx, "type_error", 500,
			   "token exceeds the 2 MiB limit");
		goto beres;
	}
	if (token_plit(tok, tlen, &hp, &hn, &pp, &pn, &sp, &sn) != 0) {
		api->throw(ctx, "jwt_error", 400,
			   "token does not have the header.payload.signature format");
		goto beres;
	}
	/* header + alg */
	if (b64url_decode(hp, hn, &hraw, &hlen) != 0 ||
	    !json_obj((const char *)hraw, hlen)) {
		api->throw(ctx, "jwt_error", 400,
			   "token header is not a JSON object");
		goto beres;
	}
	a = json_cari((const char *)hraw, hlen, "alg", NULL, alg, sizeof alg);
	if (a != 1 || alg[0] == '\0') {
		api->throw(ctx, "jwt_error", 400, "token header has no alg");
		goto beres;
	}
	if (strcmp(alg, "HS256") != 0 && strcmp(alg, "HS384") != 0 &&
	    strcmp(alg, "HS512") != 0) {
		char msg[128];
		snprintf(msg, sizeof msg,
			 "alg %s is rejected — only HS256/HS384/HS512 are allowed",
			 alg);
		api->throw(ctx, "jwt_error", 401, msg);
		goto beres;
	}
	/* The payload must be a JSON object, fully parsed BEFORE the
	 * signature check so "broken payload" vs "bad signature" stay
	 * distinguishable; the object is only RETURNED once every check
	 * passes. */
	if (b64url_decode(pp, pn, &praw, &plen) != 0) {
		api->throw(ctx, "jwt_error", 400,
			   "token payload is not a valid base64url segment");
		goto beres;
	}
	claims = json_to_object(ctx, (const char *)praw, plen, &ok);
	if (!ok) {
		api->throw(ctx, "jwt_error", 400,
			   "token payload is not a valid JSON object");
		goto beres;
	}
	/* signature */
	if (b64url_decode(sp, sn, &sraw, &slen) != 0 || slen == 0) {
		api->throw(ctx, "jwt_error", 400,
			   "signature segment is not valid base64url");
		goto beres;
	}
	mac = malloc(64);
	if (!mac) {
		api->throw(ctx, "jwt_oom", 500, "memory allocation failed");
		goto beres;
	}
	hmac_pilih(alg, (const unsigned char *)secret, klen,
		   (const unsigned char *)tok, (size_t)((hp + hn + 1 + pn) - tok),
		   mac, &maclen);
	/* signing input = header "." payload — length hn+1+pn */
	if (slen != maclen || !ct_eq(sraw, mac, slen)) {
		api->throw(ctx, "jwt_error", 401, "invalid signature");
		goto beres;
	}
	/* time claims */
	now = time(NULL);
	a = claim_number(ctx, claims, "exp", &num);
	if (a < 0) {
		api->throw(ctx, "jwt_error", 400, "claim exp is not a number");
		goto beres;
	}
	if (a == 1 && (double)now >= num) {
		api->throw(ctx, "jwt_error", 401, "token has expired (exp)");
		goto beres;
	}
	a = claim_number(ctx, claims, "nbf", &num);
	if (a < 0) {
		api->throw(ctx, "jwt_error", 400, "claim nbf is not a number");
		goto beres;
	}
	if (a == 1 && (double)now < num) {
		api->throw(ctx, "jwt_error", 401,
			   "token is not valid yet (nbf)");
		goto beres;
	}
	*ret = claims;
	rc = 0;

beres:
	free(tok);
	free(secret);
	free(hraw);
	free(praw);
	free(sraw);
	free(mac);
	return rc;
}

/* ================================================================
 * jwt.decode(token) → claims OBJECT WITHOUT signature verification
 * ================================================================ */
static int jwt_decode(gne_ctx *ctx, int argc, const gne_handle *argv,
		      gne_handle *ret)
{
	size_t tlen = 0;
	char *tok = NULL;
	const char *hp, *pp, *sp;
	size_t hn, pn, sn;
	unsigned char *hraw = NULL, *praw = NULL, *sraw = NULL;
	size_t hlen = 0, plen = 0, slen = 0;
	int rc = -1, ok = 0;
	gne_handle claims = 0;

	(void)argc;
	tok = arg_str(ctx, argv[0], &tlen);
	if (!tok)
		goto beres;
	if (tlen > JWT_MAX_TOKEN) {
		api->throw(ctx, "type_error", 500,
			   "token exceeds the 2 MiB limit");
		goto beres;
	}
	if (token_plit(tok, tlen, &hp, &hn, &pp, &pn, &sp, &sn) != 0) {
		api->throw(ctx, "jwt_error", 400,
			   "token does not have the header.payload.signature format");
		goto beres;
	}
	if (b64url_decode(hp, hn, &hraw, &hlen) != 0 ||
	    !json_obj((const char *)hraw, hlen)) {
		api->throw(ctx, "jwt_error", 400,
			   "token header is not a JSON object");
		goto beres;
	}
	if (b64url_decode(pp, pn, &praw, &plen) != 0) {
		api->throw(ctx, "jwt_error", 400,
			   "token payload is not a valid base64url segment");
		goto beres;
	}
	claims = json_to_object(ctx, (const char *)praw, plen, &ok);
	if (!ok) {
		api->throw(ctx, "jwt_error", 400,
			   "token payload is not a valid JSON object");
		goto beres;
	}
	if (b64url_decode(sp, sn, &sraw, &slen) != 0 || slen == 0) {
		api->throw(ctx, "jwt_error", 400,
			   "signature segment is not valid base64url");
		goto beres;
	}
	*ret = claims;
	rc = 0;

beres:
	free(tok);
	free(hraw);
	free(praw);
	free(sraw);
	return rc;
}

int gne_module_init(const gne_host_api *a, gne_ctx *ctx, gne_handle *out)
{
	api = a;
	if (api->abi != GNE_ABI) {
		api->throw(ctx, "gne_abi", 500, "ABI mismatch");
		return 1;
	}
	api->define_fn(ctx, "sign", 2, 3, jwt_sign);
	api->define_fn(ctx, "verify", 2, 2, jwt_verify);
	api->define_fn(ctx, "decode", 1, 1, jwt_decode);
	*out = api->module(ctx);
	return 0;
}
