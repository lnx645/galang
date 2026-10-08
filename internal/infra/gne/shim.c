//go:build cgo

/* shim.c — jembatan C GNE: tabel host API (berisi fungsi Go yang diexport),
 * trampoline pemanggilan fungsi native, dan pembungkus dlopen/LoadLibrary.
 * Pilihan loading: RTLD_NOW | RTLD_LOCAL (simbol ekstensi tidak bocor ke
 * global namespace proses). */
#include "shim.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(_WIN32)
#include <windows.h>
#else
#include <dlfcn.h>
#endif

/* ---- trampoline: Go tidak bisa memanggil pointer fungsi C secara langsung ---- */

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

/* ---- pembungkus define_*: public API memakai gne_cfunc ber-TYPE,
 *      ekspor Go menerima void* (cgo tidak mendukung pointer fungsi) ---- */

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

/* ---- loader platform ---- */

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
	return e != NULL ? e : "dlerror tidak diketahui";
#endif
}

/* ---- tabel host API (satu instance proses; semua state lewat ctx) ----
 * Cast eksplisit pada field ber-const: cgo menurunkan const pada ekspor
 * Go (char* vs const char*), sedangkan kontrak publik di gne.h tetap
 * memakai const — konversi pointer fungsi ini aman dan disengaja. */

static const gne_host_api gne_api_v1 = {
	.abi = GNE_ABI,
	.null = gne_host_null,
	.bool_new = gne_host_bool_new,
	.int_new = gne_host_int_new,
	.float_new = gne_host_float_new,
	.string = (gne_handle (*)(gne_ctx *, const char *, size_t))gne_host_string,
	.array = gne_host_array,
	.object = gne_host_object,
	.type_of = gne_host_type_of,
	.get_bool = gne_host_get_bool,
	.get_int = gne_host_get_int,
	.get_float = gne_host_get_float,
	.str_len = gne_host_str_len,
	.str_copy = gne_host_str_copy,
	.len = gne_host_len,
	.arr_push = gne_host_arr_push,
	.arr_get = gne_host_arr_get,
	.arr_set = gne_host_arr_set,
	.obj_set = (int (*)(gne_ctx *, gne_handle, const char *, gne_handle))gne_host_obj_set,
	.obj_get = (int (*)(gne_ctx *, gne_handle, const char *, uint64_t *))gne_host_obj_get,
	.obj_has = (int (*)(gne_ctx *, gne_handle, const char *, int *))gne_host_obj_has,
	.define_fn = wrap_define_fn,
	.define_method = wrap_define_method,
	.module = gne_host_module,
	.call = (int (*)(gne_ctx *, gne_handle, int, const gne_handle *, gne_handle *))gne_host_call,
	.throw = (void (*)(gne_ctx *, const char *, int32_t, const char *))gne_host_throw,
	.failed = gne_host_failed,
	.set_data = gne_host_set_data,
	.get_data = gne_host_get_data,
	.retain = gne_host_retain,
	.release = gne_host_release,
};

const gne_host_api *gne_get_host_api(void)
{
	return &gne_api_v1;
}
