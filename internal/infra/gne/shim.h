//go:build cgo

/* shim.h — deklarasi internal host GNE (bukan kontrak publik; kontrak
 * publik ada di include/gne.h). Satu sumber kebenaran untuk signature
 * ekspor Go ↔ pemanggilan C, supaya tidak ada drift. */
#ifndef GNE_SHIM_H
#define GNE_SHIM_H

#include "gne.h"

/* Diekspor oleh Go (exports.go) — dijadikan fungsi pada tabel host API. */
extern gne_handle gne_host_null(gne_ctx *ctx);
extern gne_handle gne_host_bool_new(gne_ctx *ctx, int v);
extern gne_handle gne_host_int_new(gne_ctx *ctx, int64_t v);
extern gne_handle gne_host_float_new(gne_ctx *ctx, double v);
extern gne_handle gne_host_string(gne_ctx *ctx, char *s, size_t len);
extern gne_handle gne_host_array(gne_ctx *ctx);
extern gne_handle gne_host_object(gne_ctx *ctx);
extern int gne_host_type_of(gne_ctx *ctx, uint64_t h, int32_t *out);
extern int gne_host_get_bool(gne_ctx *ctx, uint64_t h, int *out);
extern int gne_host_get_int(gne_ctx *ctx, uint64_t h, int64_t *out);
extern int gne_host_get_float(gne_ctx *ctx, uint64_t h, double *out);
extern int gne_host_str_len(gne_ctx *ctx, uint64_t h, size_t *out);
extern int gne_host_str_copy(gne_ctx *ctx, uint64_t h, char *buf, size_t cap);
extern int gne_host_len(gne_ctx *ctx, uint64_t h, size_t *out);
extern int gne_host_arr_push(gne_ctx *ctx, uint64_t arr, uint64_t val);
extern int gne_host_arr_get(gne_ctx *ctx, uint64_t arr, size_t idx,
			    uint64_t *out);
extern int gne_host_arr_set(gne_ctx *ctx, uint64_t arr, size_t idx,
			    uint64_t val);
extern int gne_host_obj_set(gne_ctx *ctx, uint64_t obj, char *key,
			    uint64_t val);
extern int gne_host_obj_get(gne_ctx *ctx, uint64_t obj, char *key,
			    uint64_t *out);
extern int gne_host_obj_has(gne_ctx *ctx, uint64_t obj, char *key,
			    int *out);
extern int gne_host_define_fn(gne_ctx *ctx, char *name, int32_t min,
			      int32_t max, void *fn);
extern int gne_host_define_method(gne_ctx *ctx, uint64_t obj, char *name,
				  int32_t min, int32_t max, void *fn,
				  uint64_t self);
extern gne_handle gne_host_module(gne_ctx *ctx);
extern int gne_host_call(gne_ctx *ctx, uint64_t fn, int argc,
			 uint64_t *argv, uint64_t *ret);
extern void gne_host_throw(gne_ctx *ctx, char *code, int32_t status,
			   char *msg);
extern int gne_host_failed(gne_ctx *ctx);
extern void gne_host_set_data(gne_ctx *ctx, void *data);
extern void *gne_host_get_data(gne_ctx *ctx);
extern void gne_host_retain(gne_ctx *ctx, uint64_t h);
extern void gne_host_release(gne_ctx *ctx, uint64_t h);

/* Trampoline & utilitas yang dijalankan dari Go. */
int gne_invoke_fn(void *fn, gne_ctx *ctx, int argc, uint64_t *argv,
		  uint64_t *ret);
int gne_invoke_init(void *fn, const gne_host_api *api, gne_ctx *ctx,
		    uint64_t *out);
const gne_host_api *gne_get_host_api(void);
void *gne_dl_open(const char *path);
void *gne_dl_sym(void *h, const char *name);
const char *gne_dl_error(void);

#endif /* GNE_SHIM_H */
