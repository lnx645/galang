//go:build cgo

/* shim.c — the GNE C bridge: the host API table (holding the Go
 * functions exported by the host), trampolines for calling native
 * functions, and the dlopen/LoadLibrary wrappers.
 * Loading choices: RTLD_NOW | RTLD_LOCAL (extension symbols do not leak
 * into the process global namespace). */
#include "shim.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(_WIN32)
#include <windows.h>
#else
#include <dlfcn.h>
#endif

/* ---- trampolines: Go cannot call C function pointers directly ---- */

int gne_invoke_fn(void *fn, gne_ctx *ctx, int argc, uint64_t *argv,
		  uint64_t *ret)
{
	gne_cfunc f = (gne_cfunc)fn;
	return f(ctx, argc, argv, ret);
}

int gne_invoke_init(void *fn, const gne_host_api *api, gne_ctx *ctx,
		    uint64_t *out)
{
	int (*initfn)(const gne_host_api *, gne_ctx *, uint64_t *) =
		(int (*)(const gne_host_api *, gne_ctx *, uint64_t *))fn;
	return initfn(api, ctx, out);
}

/* ---- define_* wrappers: the public API uses a typed gne_cfunc,
 *      while the Go exports receive void* (cgo has no function
 *      pointers) ---- */

static int wrap_define_fn(gne_ctx *ctx, const char *name, int32_t min,
			  int32_t max, gne_cfunc fn)
{
	return gne_host_define_fn(ctx, (char *)name, min, max, (void *)fn);
}

static int wrap_define_method(gne_ctx *ctx, gne_handle obj, const char *name,
			      int32_t min, int32_t max, gne_cfunc fn,
			      gne_handle self)
{
	return gne_host_define_method(ctx, obj, (char *)name, min, max,
				      (void *)fn, self);
}

/* ---- platform loader ---- */

void *gne_dl_open(const char *path)
{
#if defined(_WIN32)
	return (void *)LoadLibraryA(path);
#else
	return dlopen(path, RTLD_NOW | RTLD_LOCAL);
#endif
}

void *gne_dl_sym(void *h, const char *name)
{
#if defined(_WIN32)
	return (void *)GetProcAddress((HMODULE)h, name);
#else
	return dlsym(h, name);
#endif
}

const char *gne_dl_error(void)
{
#if defined(_WIN32)
	static char buf[64];
	snprintf(buf, sizeof(buf), "GetLastError=%lu",
		 (unsigned long)GetLastError());
	return buf;
#else
	const char *e = dlerror();
	return e != NULL ? e : "unknown dlerror";
#endif
}

/* ---- host API table (one set of instances per process; all state
 * goes through ctx) ----
 * Explicit casts on const fields: cgo lowers const on Go exports
 * (char* vs const char*), while the public contract in gne.h keeps
 * const — this function-pointer conversion is safe and intentional. */

#define GNE_API_BODY                                                       \
	.null = gne_host_null,                                             \
	.bool_new = gne_host_bool_new,                                     \
	.int_new = gne_host_int_new,                                       \
	.float_new = gne_host_float_new,                                   \
	.string = (gne_handle (*)(gne_ctx *, const char *, size_t))gne_host_string, \
	.array = gne_host_array,                                           \
	.object = gne_host_object,                                         \
	.type_of = gne_host_type_of,                                       \
	.get_bool = gne_host_get_bool,                                     \
	.get_int = gne_host_get_int,                                       \
	.get_float = gne_host_get_float,                                   \
	.str_len = gne_host_str_len,                                       \
	.str_copy = gne_host_str_copy,                                     \
	.len = gne_host_len,                                               \
	.arr_push = gne_host_arr_push,                                     \
	.arr_get = gne_host_arr_get,                                       \
	.arr_set = gne_host_arr_set,                                       \
	.obj_set = (int (*)(gne_ctx *, gne_handle, const char *, gne_handle))gne_host_obj_set, \
	.obj_get = (int (*)(gne_ctx *, gne_handle, const char *, uint64_t *))gne_host_obj_get, \
	.obj_has = (int (*)(gne_ctx *, gne_handle, const char *, int *))gne_host_obj_has, \
	.define_fn = wrap_define_fn,                                       \
	.define_method = wrap_define_method,                               \
	.module = gne_host_module,                                         \
	.call = (int (*)(gne_ctx *, gne_handle, int, const gne_handle *, gne_handle *))gne_host_call, \
	.throw = (void (*)(gne_ctx *, const char *, int32_t, const char *))gne_host_throw, \
	.failed = gne_host_failed,                                         \
	.set_data = gne_host_set_data,                                     \
	.get_data = gne_host_get_data,                                     \
	.retain = gne_host_retain,                                         \
	.release = gne_host_release,                                       \
	.obj_keys = (int (*)(gne_ctx *, gne_handle, uint64_t *))gne_host_obj_keys, \
	.tls_wrap = (int (*)(gne_ctx *, uintptr_t, const char *,          \
			     const char *, int32_t, int32_t, char *,      \
			     size_t, gne_handle *))gne_host_tls_wrap,     \
	.tls_read = gne_host_tls_read,                                    \
	.tls_write = (int (*)(gne_ctx *, gne_handle, const char *,        \
			      size_t, int32_t))gne_host_tls_write,        \
	.tls_close = gne_host_tls_close,

/* One entry per served ABI: index = abi-1, counting up to GNE_ABI. The
 * struct is append-only, so every view is layout-identical — only the
 * reported abi number differs. When GNE_ABI is bumped, insert an entry
 * for each ABI introduced along the way (the last entry must always
 * use GNE_ABI). Serving a legacy view is what keeps already-installed
 * extensions loading after a `gar` upgrade. */
static const gne_host_api gne_api_by_abi[] = {
	{ .abi = 1, GNE_API_BODY },
	{ .abi = 2, GNE_API_BODY },
	{ .abi = GNE_ABI, GNE_API_BODY },
};

/* gne_get_host_api_abi returns the table view for an extension's
 * declared ABI. declared <= 0 (no sidecar hint) or an unknown/newer
 * value selects the current view; the extension's own equality check
 * then rejects a real mismatch with a clear error. */
const gne_host_api *gne_get_host_api_abi(int32_t declared)
{
	size_t count = sizeof gne_api_by_abi / sizeof gne_api_by_abi[0];
	if (declared >= 1 && (size_t)declared <= count &&
	    gne_api_by_abi[declared - 1].abi == declared)
		return &gne_api_by_abi[declared - 1];
	return &gne_api_by_abi[count - 1];
}
