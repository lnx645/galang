/* gne.h — GaLang Native Extension (GNE) host ABI, version 2.
 *
 * Quick contract for extension authors:
 *
 *   1. GaLang values are passed as handles (uint64_t). 0 is ALWAYS
 *      invalid; GaLang null has its own handle (api->null()).
 *   2. Host functions may only be called from the single goroutine that
 *      invoked the extension; ctx is valid for one call only — do NOT
 *      store it or pass it to another thread.
 *   3. Never longjmp/abort/exit from inside an extension (it kills the
 *      Go runtime). Report errors with api->throw and return a
 *      non-zero code instead.
 *   4. Keep api (valid for the process lifetime) and per-instance state
 *      via set_data/get_data — avoid bare global variables when one
 *      .so is used by several interpreters.
 *   5. Strings crossing the Go↔C boundary are copied (str_copy /
 *      constructors) — no host pointer may be retained.
 *
 * Handle ownership (reference counting):
 *   - EVERY handle created during one call — arguments, constructors,
 *     get_* results, call results — is TEMPORARILY owned by the host:
 *     the host releases them all when the cfunc returns. Do NOT release
 *     them yourself (that could break pinning before it finishes);
 *     just retain() when a handle must survive the call (e.g. stored
 *     in state).
 *   - A handle returned through *ret: ownership TRANSFERS to the host.
 *     Do not release() it afterwards; if C also keeps a copy,
 *     retain() it BEFORE returning.
 *   - release() is only for handles you retained yourself; the entry
 *     is dropped once rc reaches 0, so a stale handle reused later
 *     yields a clear error (instead of reading someone else's value).
 *   - define_method() pins its own self (the host already retained
 *     it), so method thunks stay valid for the module's lifetime.
 *
 * How to build:
 *   gcc -shared -fPIC -I<path to gne.h> -o redis.so redis.c
 *   (macOS: -dynamiclib; Windows mingw: -shared)
 *   Put the result in ./gne/ or ~/.galang/gne, then: use "redis".
 *
 * ABI evolution rules (why extensions keep working across upgrades):
 *   - The gne_host_api struct is APPEND-ONLY: fields are only ever
 *     added at the end — never reordered, removed, or retyped.
 *   - An extension declares the ABI it was compiled with through
 *     GNE_ABI; it must reject a host table with a different number
 *     (api->abi != GNE_ABI) so mismatches fail loudly instead of
 *     reading the wrong offsets.
 *   - A newer host serves a LEGACY VIEW of the table to an extension
 *     built for an older ABI: identical layout, only api->abi reports
 *     the older number (the host reads the declared ABI from the
 *     package sidecar at load time). Extensions built for an ABI newer
 *     than the host are rejected at install time by `gar gne install`.
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

/* ABI version. The host rejects extensions with a different number
 * (legacy numbers are served via the compatibility view described
 * above). */
#define GNE_ABI 2

/* Call context — HOST INTERNAL FIELDS, do not access them directly from
 * an extension (they may change without notice in the next ABI version).
 * Valid for one call only; do not store it. */
struct gne_ctx {
	uint64_t mod;      /* module id (host-internal) */
	uint64_t self;     /* method self, 0 when not a method */
	int32_t  line, col; /* position of the running call */
	int32_t  failed;   /* != 0 when api->throw has been called */
	int32_t  status;
	char    *code, *msg; /* pending error (host-owned) */
	void    *data;       /* per-module instance data (api->set_data) */
};

typedef struct gne_ctx gne_ctx;
typedef uint64_t gne_handle;

/* Value types (compact enum). */
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

/* Native function registered with GaLang.
 * For methods (define_method), argv[0] is self; min/max do not count
 * self. Return 0 with *ret set = success. */
typedef int (*gne_cfunc)(gne_ctx *ctx, int argc, const gne_handle *argv,
			 gne_handle *ret);

/* Host API table — filled in by the GaLang runtime, used for the life of
 * the process. Extensions store this pointer in a static variable from
 * gne_module_init(). Every function needs the ctx of the running call. */
typedef struct gne_host_api {
	int32_t abi; /* always the host's GNE_ABI */

	/* --- constructors (result: C-owned, see rc rules) --- */
	gne_handle (*null)(gne_ctx *ctx);
	gne_handle (*bool_new)(gne_ctx *ctx, int v);
	gne_handle (*int_new)(gne_ctx *ctx, int64_t v);
	gne_handle (*float_new)(gne_ctx *ctx, double v);
	gne_handle (*string)(gne_ctx *ctx, const char *s, size_t len);
	gne_handle (*array)(gne_ctx *ctx);
	gne_handle (*object)(gne_ctx *ctx);

	/* --- accessors: 0 = ok, -1 = fail (bad type/handle);
	 *     they never throw and never change throw status. --- */
	int (*type_of)(gne_ctx *ctx, gne_handle h, int32_t *out);
	int (*get_bool)(gne_ctx *ctx, gne_handle h, int *out);
	int (*get_int)(gne_ctx *ctx, gne_handle h, int64_t *out);
	int (*get_float)(gne_ctx *ctx, gne_handle h, double *out);
	/* str_len: byte length without NUL.
	 * str_copy: copy + NUL into the receiver's buffer; cap must be
	 * >= str_len+1; returns bytes copied (without NUL) or -1. */
	int (*str_len)(gne_ctx *ctx, gne_handle h, size_t *out);
	int (*str_copy)(gne_ctx *ctx, gne_handle h, char *buf, size_t cap);

	/* --- containers (len applies to arrays & strings) --- */
	int (*len)(gne_ctx *ctx, gne_handle h, size_t *out);
	int (*arr_push)(gne_ctx *ctx, gne_handle arr, gne_handle val);
	int (*arr_get)(gne_ctx *ctx, gne_handle arr, size_t idx, gne_handle *out);
	int (*arr_set)(gne_ctx *ctx, gne_handle arr, size_t idx, gne_handle val);
	int (*obj_set)(gne_ctx *ctx, gne_handle obj, const char *key,
		       gne_handle val);
	int (*obj_get)(gne_ctx *ctx, gne_handle obj, const char *key,
		       gne_handle *out); /* -1 when the key is absent */
	int (*obj_has)(gne_ctx *ctx, gne_handle obj, const char *key, int *out);

	/* --- functions & callbacks ---
	 * define_fn/define_method register into the module namespace of
	 * ctx (allowed during init or during a running call). call
	 * invokes any callable value; async results arrive as a promise —
	 * pass it through as-is. module() returns the module namespace of
	 * ctx (usually written to *out inside gne_module_init). */
	int (*define_fn)(gne_ctx *ctx, const char *name, int32_t min_args,
			 int32_t max_args, gne_cfunc fn);
	int (*define_method)(gne_ctx *ctx, gne_handle obj, const char *name,
			     int32_t min_args, int32_t max_args, gne_cfunc fn,
			     gne_handle self);
	int (*call)(gne_ctx *ctx, gne_handle fn, int argc,
		    const gne_handle *argv, gne_handle *ret);
	gne_handle (*module)(gne_ctx *ctx);

	/* --- error: throw, then return non-zero from the cfunc.
	 * code is free-form (e.g. "redis_error"), status is HTTP-style
	 * (e.g. 500); the error becomes catchable: try/catch e { e.code,
	 * e.status }. --- */
	void (*throw)(gne_ctx *ctx, const char *code, int32_t status,
		      const char *msg);
	int (*failed)(gne_ctx *ctx);

	/* --- per-module instance data (safe with multi-interpreter) --- */
	void (*set_data)(gne_ctx *ctx, void *data);
	void *(*get_data)(gne_ctx *ctx);

	/* --- handle lifetime --- */
	void (*retain)(gne_ctx *ctx, gne_handle h);
	void (*release)(gne_ctx *ctx, gne_handle h);

	/* --- ABI 2: object key enumeration (APPEND-ONLY — new fields
	 * always go last so older header layouts keep their offsets).
	 * obj_keys writes an ARRAY of string handles with the object's
	 * keys in insertion order (C-owned temporary like other accessors:
	 * released by the host after the cfunc returns). Returns 0 on
	 * success, -1 when the handle is not an object; it never throws. */
	int (*obj_keys)(gne_ctx *ctx, gne_handle obj, gne_handle *out);
} gne_host_api;

/* Mandatory entry point exported by every extension.
 * Build the namespace (usually api->object + api->define_fn/define_method)
 * then write its handle to *out. Return 0 = success; on failure:
 * api->throw(...) then return non-zero. */
GNE_EXPORT int gne_module_init(const gne_host_api *api, gne_ctx *ctx,
			       gne_handle *out);

#ifdef __cplusplus
}
#endif

#endif /* GNE_H */
