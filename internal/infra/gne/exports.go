//go:build cgo

package gne

/*
#cgo CFLAGS: -I${SRCDIR}/../../../include
#include "shim.h"
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"fmt"
	"unsafe"

	"galang/internal/domain"
)

// gneGuardH/gneGuardI wrap every export with panic recovery: a Go panic
// must not cross a C frame (it would kill the process), so it is turned
// into a deferred error on ctx.
func gneGuardH(ctx *C.gne_ctx, fn func() C.gne_handle) (res C.gne_handle) {
	defer func() {
		if r := recover(); r != nil {
			gneFail(ctx, "gne_panic", 500, fmt.Sprintf("host panic: %v", r))
			res = 0
		}
	}()
	return fn()
}

func gneGuardI(ctx *C.gne_ctx, fn func() C.int) (res C.int) {
	defer func() {
		if r := recover(); r != nil {
			gneFail(ctx, "gne_panic", 500, fmt.Sprintf("host panic: %v", r))
			res = -1
		}
	}()
	return fn()
}

func gneGuardV(ctx *C.gne_ctx, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			gneFail(ctx, "gne_panic", 500, fmt.Sprintf("host panic: %v", r))
		}
	}()
	fn()
}

// ---- constructors ----

//export gne_host_null
func gne_host_null(ctx *C.gne_ctx) C.gne_handle {
	return gneGuardH(ctx, func() C.gne_handle {
		m := modFor(ctx)
		if m == nil {
			return 0
		}
		return C.gne_handle(m.newTemp(domain.Null{}))
	})
}

//export gne_host_bool_new
func gne_host_bool_new(ctx *C.gne_ctx, v C.int) C.gne_handle {
	return gneGuardH(ctx, func() C.gne_handle {
		m := modFor(ctx)
		if m == nil {
			return 0
		}
		return C.gne_handle(m.newTemp(domain.Bool(v != 0)))
	})
}

//export gne_host_int_new
func gne_host_int_new(ctx *C.gne_ctx, v C.int64_t) C.gne_handle {
	return gneGuardH(ctx, func() C.gne_handle {
		m := modFor(ctx)
		if m == nil {
			return 0
		}
		return C.gne_handle(m.newTemp(domain.Int(v)))
	})
}

//export gne_host_float_new
func gne_host_float_new(ctx *C.gne_ctx, v C.double) C.gne_handle {
	return gneGuardH(ctx, func() C.gne_handle {
		m := modFor(ctx)
		if m == nil {
			return 0
		}
		return C.gne_handle(m.newTemp(domain.Float(v)))
	})
}

//export gne_host_string
func gne_host_string(ctx *C.gne_ctx, s *C.char, n C.size_t) C.gne_handle {
	return gneGuardH(ctx, func() C.gne_handle {
		m := modFor(ctx)
		if m == nil {
			return 0
		}
		b := C.GoBytes(unsafe.Pointer(s), C.int(n))
		return C.gne_handle(m.newTemp(domain.Str(string(b))))
	})
}

//export gne_host_array
func gne_host_array(ctx *C.gne_ctx) C.gne_handle {
	return gneGuardH(ctx, func() C.gne_handle {
		m := modFor(ctx)
		if m == nil {
			return 0
		}
		return C.gne_handle(m.newTemp(&domain.Arr{}))
	})
}

//export gne_host_object
func gne_host_object(ctx *C.gne_ctx) C.gne_handle {
	return gneGuardH(ctx, func() C.gne_handle {
		m := modFor(ctx)
		if m == nil {
			return 0
		}
		return C.gne_handle(m.newTemp(domain.NewObj()))
	})
}

// ---- accessors ----

func gneTag(t domain.TypeTag) C.int32_t {
	switch t {
	case domain.TypeNull:
		return C.GNE_NULL
	case domain.TypeBool:
		return C.GNE_BOOL
	case domain.TypeInt:
		return C.GNE_INT
	case domain.TypeFloat:
		return C.GNE_FLOAT
	case domain.TypeString:
		return C.GNE_STRING
	case domain.TypeArray:
		return C.GNE_ARRAY
	case domain.TypeObject, domain.TypeCallObject:
		return C.GNE_OBJECT
	case domain.TypeFunc:
		return C.GNE_FUNCTION
	case domain.TypePromise:
		return C.GNE_PROMISE
	case domain.TypeError:
		return C.GNE_ERROR
	}
	return C.GNE_NULL
}

//export gne_host_type_of
func gne_host_type_of(ctx *C.gne_ctx, h C.uint64_t, out *C.int32_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || out == nil {
			return -1
		}
		v, ok := m.reg.lookup(uint64(h))
		if !ok {
			return -1
		}
		*out = gneTag(v.Type())
		return 0
	})
}

//export gne_host_get_bool
func gne_host_get_bool(ctx *C.gne_ctx, h C.uint64_t, out *C.int) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || out == nil {
			return -1
		}
		v, ok := m.reg.lookup(uint64(h))
		if !ok {
			return -1
		}
		b, ok := v.(domain.Bool)
		if !ok {
			return -1
		}
		if b {
			*out = 1
		} else {
			*out = 0
		}
		return 0
	})
}

//export gne_host_get_int
func gne_host_get_int(ctx *C.gne_ctx, h C.uint64_t, out *C.int64_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || out == nil {
			return -1
		}
		v, ok := m.reg.lookup(uint64(h))
		if !ok {
			return -1
		}
		i, ok := v.(domain.Int)
		if !ok {
			return -1
		}
		*out = C.int64_t(i)
		return 0
	})
}

//export gne_host_get_float
func gne_host_get_float(ctx *C.gne_ctx, h C.uint64_t, out *C.double) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || out == nil {
			return -1
		}
		v, ok := m.reg.lookup(uint64(h))
		if !ok {
			return -1
		}
		switch f := v.(type) {
		case domain.Float:
			*out = C.double(f)
		case domain.Int:
			*out = C.double(f)
		default:
			return -1
		}
		return 0
	})
}

//export gne_host_str_len
func gne_host_str_len(ctx *C.gne_ctx, h C.uint64_t, out *C.size_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || out == nil {
			return -1
		}
		v, ok := m.reg.lookup(uint64(h))
		if !ok {
			return -1
		}
		s, ok := v.(domain.Str)
		if !ok {
			return -1
		}
		*out = C.size_t(len(s))
		return 0
	})
}

//export gne_host_str_copy
func gne_host_str_copy(ctx *C.gne_ctx, h C.uint64_t, buf *C.char, bufCap C.size_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || buf == nil {
			return -1
		}
		v, ok := m.reg.lookup(uint64(h))
		if !ok {
			return -1
		}
		s, ok := v.(domain.Str)
		if !ok {
			return -1
		}
		b := []byte(s)
		if C.size_t(len(b))+1 > bufCap {
			return -1
		}
		if len(b) > 0 {
			C.memcpy(unsafe.Pointer(buf), unsafe.Pointer(&b[0]), C.size_t(len(b)))
		}
		unsafe.Slice(buf, len(b)+1)[len(b)] = 0
		return C.int(len(b))
	})
}

// ---- containers ----

//export gne_host_len
func gne_host_len(ctx *C.gne_ctx, h C.uint64_t, out *C.size_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || out == nil {
			return -1
		}
		v, ok := m.reg.lookup(uint64(h))
		if !ok {
			return -1
		}
		switch c := v.(type) {
		case *domain.Arr:
			*out = C.size_t(len(c.Items))
		case domain.Str:
			*out = C.size_t(len(c))
		default:
			return -1
		}
		return 0
	})
}

//export gne_host_arr_push
func gne_host_arr_push(ctx *C.gne_ctx, arr, val C.uint64_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil {
			return -1
		}
		av, ok := m.reg.lookup(uint64(arr))
		if !ok {
			return -1
		}
		a, ok := av.(*domain.Arr)
		if !ok {
			return -1
		}
		vv, ok := m.reg.lookup(uint64(val))
		if !ok {
			return -1
		}
		a.Append(vv)
		return 0
	})
}

//export gne_host_arr_get
func gne_host_arr_get(ctx *C.gne_ctx, arr C.uint64_t, idx C.size_t, out *C.uint64_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || out == nil {
			return -1
		}
		av, ok := m.reg.lookup(uint64(arr))
		if !ok {
			return -1
		}
		a, ok := av.(*domain.Arr)
		if !ok || int(idx) >= len(a.Items) {
			return -1
		}
		*out = C.uint64_t(m.newTemp(a.Items[int(idx)]))
		return 0
	})
}

//export gne_host_arr_set
func gne_host_arr_set(ctx *C.gne_ctx, arr C.uint64_t, idx C.size_t, val C.uint64_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil {
			return -1
		}
		av, ok := m.reg.lookup(uint64(arr))
		if !ok {
			return -1
		}
		a, ok := av.(*domain.Arr)
		if !ok || int(idx) >= len(a.Items) {
			return -1
		}
		vv, ok := m.reg.lookup(uint64(val))
		if !ok {
			return -1
		}
		a.Set(int(idx), vv)
		return 0
	})
}

//export gne_host_obj_set
func gne_host_obj_set(ctx *C.gne_ctx, obj C.uint64_t, key *C.char, val C.uint64_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || key == nil {
			return -1
		}
		ov, ok := m.reg.lookup(uint64(obj))
		if !ok {
			return -1
		}
		o, ok := ov.(*domain.Obj)
		if !ok {
			return -1
		}
		vv, ok := m.reg.lookup(uint64(val))
		if !ok {
			return -1
		}
		o.Set(C.GoString(key), vv)
		return 0
	})
}

//export gne_host_obj_get
func gne_host_obj_get(ctx *C.gne_ctx, obj C.uint64_t, key *C.char, out *C.uint64_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || key == nil || out == nil {
			return -1
		}
		ov, ok := m.reg.lookup(uint64(obj))
		if !ok {
			return -1
		}
		o, ok := ov.(*domain.Obj)
		if !ok {
			return -1
		}
		v, ok := o.Get(C.GoString(key))
		if !ok {
			return -1
		}
		*out = C.uint64_t(m.newTemp(v))
		return 0
	})
}

//export gne_host_obj_has
func gne_host_obj_has(ctx *C.gne_ctx, obj C.uint64_t, key *C.char, out *C.int) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || key == nil || out == nil {
			return -1
		}
		ov, ok := m.reg.lookup(uint64(obj))
		if !ok {
			return -1
		}
		o, ok := ov.(*domain.Obj)
		if !ok {
			return -1
		}
		if _, ok := o.Get(C.GoString(key)); ok {
			*out = 1
		} else {
			*out = 0
		}
		return 0
	})
}

// gne_host_obj_keys returns an array of string handles holding the
// object's keys in insertion order (ABI 2). C-owned temporary like the
// other accessors; -1 when the handle is not an object. Never throws.
//
//export gne_host_obj_keys
func gne_host_obj_keys(ctx *C.gne_ctx, obj C.uint64_t, out *C.uint64_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || out == nil {
			return -1
		}
		ov, ok := m.reg.lookup(uint64(obj))
		if !ok {
			return -1
		}
		o, ok := ov.(*domain.Obj)
		if !ok {
			return -1
		}
		keys := o.Keys()
		arr := &domain.Arr{}
		for _, k := range keys {
			arr.Append(domain.Str(k))
		}
		*out = C.uint64_t(m.newTemp(arr))
		return 0
	})
}

// ---- function registration ----

//export gne_host_define_fn
func gne_host_define_fn(ctx *C.gne_ctx, name *C.char, min, max C.int32_t, fn unsafe.Pointer) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || name == nil || fn == nil {
			return -1
		}
		gn := C.GoString(name)
		if gn == "" {
			return -1
		}
		full := m.name + "." + gn
		v := m.reg.hooks.WrapNative(full, int(min), int(max), fn, 0, m)
		m.ns.Set(gn, v)
		return 0
	})
}

//export gne_host_define_method
func gne_host_define_method(ctx *C.gne_ctx, obj C.uint64_t, name *C.char, min, max C.int32_t, fn unsafe.Pointer, self C.uint64_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || name == nil || fn == nil {
			return -1
		}
		ov, ok := m.reg.lookup(uint64(obj))
		if !ok {
			return -1
		}
		o, ok := ov.(*domain.Obj)
		if !ok {
			return -1
		}
		if _, ok := m.reg.lookup(uint64(self)); !ok {
			return -1
		}
		gn := C.GoString(name)
		if gn == "" {
			return -1
		}
		// self is pinned by the host: the method thunk must stay valid for
		// the lifetime of the module.
		m.reg.retain(uint64(self))
		full := m.name + "." + gn
		v := m.reg.hooks.WrapNative(full, int(min), int(max), fn, uint64(self), m)
		o.Set(gn, v)
		return 0
	})
}

//export gne_host_module
func gne_host_module(ctx *C.gne_ctx) C.gne_handle {
	return gneGuardH(ctx, func() C.gne_handle {
		m := modFor(ctx)
		if m == nil {
			return 0
		}
		return C.gne_handle(m.newTemp(m.ns))
	})
}

// ---- callbacks ----

//export gne_host_call
func gne_host_call(ctx *C.gne_ctx, fn C.uint64_t, argc C.int, argv *C.uint64_t, out *C.uint64_t) C.int {
	return gneGuardI(ctx, func() C.int {
		m := modFor(ctx)
		if m == nil || out == nil {
			return -1
		}
		fv, ok := m.reg.lookup(uint64(fn))
		if !ok {
			return -1
		}
		pos := domain.Position{Line: int(ctx.line), Col: int(ctx.col)}
		args := make([]domain.Value, 0, int(argc))
		if argc > 0 && argv == nil {
			return -1
		}
		hargv := unsafe.Slice(argv, int(argc))
		for _, h := range hargv {
			v, ok := m.reg.lookup(uint64(h))
			if !ok {
				return -1
			}
			args = append(args, v)
		}
		res, err := m.reg.hooks.CallValue(fv, args, pos)
		if err != nil {
			code, status, msg := m.reg.hooks.DescribeError(err)
			gneFail(ctx, code, status, msg)
			return -1
		}
		*out = C.uint64_t(m.newTemp(res))
		return 0
	})
}

// ---- errors, instance data, handle lifetime ----

//export gne_host_throw
func gne_host_throw(ctx *C.gne_ctx, code *C.char, status C.int32_t, msg *C.char) {
	gneGuardV(ctx, func() {
		if ctx == nil {
			return
		}
		gneFail(ctx, C.GoString(code), int(status), C.GoString(msg))
	})
}

//export gne_host_failed
func gne_host_failed(ctx *C.gne_ctx) C.int {
	if ctx == nil {
		return 0
	}
	return ctx.failed
}

//export gne_host_set_data
func gne_host_set_data(ctx *C.gne_ctx, data unsafe.Pointer) {
	if ctx != nil {
		ctx.data = data
	}
}

//export gne_host_get_data
func gne_host_get_data(ctx *C.gne_ctx) unsafe.Pointer {
	if ctx == nil {
		return nil
	}
	return ctx.data
}

//export gne_host_retain
func gne_host_retain(ctx *C.gne_ctx, h C.uint64_t) {
	gneGuardV(ctx, func() {
		if m := modFor(ctx); m != nil {
			m.reg.retain(uint64(h))
		}
	})
}

//export gne_host_release
func gne_host_release(ctx *C.gne_ctx, h C.uint64_t) {
	gneGuardV(ctx, func() {
		if m := modFor(ctx); m != nil {
			m.reg.release(uint64(h))
		}
	})
}
