package interp

import (
	"strings"

	"garurda/internal/domain"
)

// builtinFn is the Go signature shared by every builtin implementation. `in`
// may be nil for builtins that do not need interpreter state.
type builtinFn = func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error)

// typeFn wraps a Go function as a builtin value with a variable argument
// count. The interpreter is captured so builtins can call back into user code.
func (in *Interp) typeFn(name string, minArgs int, fn builtinFn) *domain.Builtin {
	self := in
	return &domain.Builtin{
		Name:    name,
		MinArgs: minArgs,
		VarArgs: true,
		Fn: func(args []domain.Value, pos domain.Position) (domain.Value, error) {
			return fn(self, args, pos)
		},
	}
}

// strictFn is a builtin with a bounded argument count.
func (in *Interp) strictFn(name string, min, max int, fn builtinFn) *domain.Builtin {
	self := in
	return &domain.Builtin{
		Name:    name,
		MinArgs: min,
		VarArgs: false,
		Fn: func(args []domain.Value, pos domain.Position) (domain.Value, error) {
			if max >= 0 && len(args) > max {
				return nil, self.errf(pos, "%s() takes at most %d argument(s), got %d", name, max, len(args))
			}
			return fn(self, args, pos)
		},
	}
}

// installGlobals registers the builtin namespace in the global scope.
func (in *Interp) installGlobals() {
	names := in.builtinNames()
	// Reserve every slot first, then grow once, then fill: growing between
	// assignments would discard the values already stored.
	for name := range names {
		in.gscope.names[name] = &cslot{kind: slotVal, idx: in.gscope.layout.addVal()}
	}
	in.globals.grow(in.gscope.layout)
	for name, val := range names {
		in.globals.vals[in.gscope.names[name].idx] = val
	}
	in.Modules = map[string]func() *domain.Obj{
		"strings":  in.newStringsModule,
		"math":     in.newMathModule,
		"time":     in.newTimeModule,
		"http":     in.newHttpModule,
		"database": in.newDatabaseModule,
		"file":     in.newFileModule,
	}
}

// builtinNames returns the name/value pairs registered as globals.
func (in *Interp) builtinNames() map[string]domain.Value {
	b := map[string]domain.Value{}
	reg := func(name string, min int, fn builtinFn) {
		b[name] = in.typeFn(name, min, fn)
	}
	regs := func(name string, min, max int, fn builtinFn) {
		b[name] = in.strictFn(name, min, max, fn)
	}

	// ---- output ----
	reg("print", 0, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		parts := make([]string, 0, len(args))
		for _, a := range args {
			parts = append(parts, a.String())
		}
		in.printLine(strings.Join(parts, " "))
		return domain.Null{}, nil
	})

	// ---- type inspection ----
	regs("type", 1, 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		return domain.Str(domain.TypeName(args[0])), nil
	})
	for _, t := range []string{"int", "float", "string", "bool", "array", "object", "null", "error", "function", "promise"} {
		want := t
		regs("is_"+want, 1, 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
			return domain.Bool(string(domain.TypeName(args[0])) == want), nil
		})
	}

	// ---- conversions ----
	regs("int", 1, 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		switch v := args[0].(type) {
		case domain.Int:
			return v, nil
		case domain.Float:
			return domain.Int(int64(v)), nil
		case domain.Str:
			n, ok := parseIntStr(string(v))
			if !ok {
				return nil, in.errf(pos, "cannot convert %q to int", string(v))
			}
			return domain.Int(n), nil
		case domain.Bool:
			if v {
				return domain.Int(1), nil
			}
			return domain.Int(0), nil
		}
		return nil, in.errf(pos, "cannot convert %s to int", domain.TypeName(args[0]))
	})
	regs("float", 1, 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		f, ok := domain.AsFloat(args[0])
		if !ok {
			if s, isStr := args[0].(domain.Str); isStr {
				if pf, ok2 := parseFloatStr(string(s)); ok2 {
					return domain.Float(pf), nil
				}
			}
			return nil, in.errf(pos, "cannot convert %s to float", domain.TypeName(args[0]))
		}
		return domain.Float(f), nil
	})
	regs("str", 1, 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		return domain.Str(args[0].String()), nil
	})
	regs("bool", 1, 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		return domain.Bool(domain.Truthy(args[0])), nil
	})

	// ---- async ----
	// gather resolves every argument (promises run now) and returns an
	// array of results; the first rejection propagates.
	reg("gather", 0, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		out := make([]domain.Value, 0, len(args))
		for _, a := range args {
			v, err := in.awaitValue(a, pos)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return &domain.Arr{Items: out}, nil
	})
	// spawn queues a call as a fire-and-forget promise; it runs at the
	// next await/drain.
	reg("spawn", 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		if c, ok := args[0].(*closure); ok {
			// newPromiseClosure copies the argument slice.
			return in.newPromiseClosure(pos, c, args[1:]), nil
		}
		rest := append([]domain.Value(nil), args[1:]...)
		target := args[0]
		return in.newPromise(pos, func() (domain.Value, error) {
			return in.callValue(target, rest, pos)
		}), nil
	})

	// ---- errors ----
	reg("error", 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		msg, _ := args[0].(domain.Str)
		ev := &domain.ErrorValue{Message: string(msg), Code: "error", Status: 500}
		if len(args) > 1 {
			if o, ok := args[1].(*domain.Obj); ok {
				if c, found := o.Get("code"); found {
					ev.Code = c.String()
				}
				if s, found := o.Get("status"); found {
					if si, ok := domain.AsInt(s); ok {
						ev.Status = int(si)
					}
				}
			}
		}
		return ev, nil
	})
	for name, status := range map[string]int{
		"bad_request":  400,
		"unauthorized": 401,
		"forbidden":    403,
		"not_found":    404,
		"conflict":     409,
		"server_error": 500,
	} {
		st := status
		reg(name, 0, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
			msg := "error"
			if len(args) > 0 {
				msg = args[0].String()
			}
			return &domain.ErrorValue{Message: msg, Code: "http_error", Status: st}, nil
		})
	}

	// ---- collections: length, membership, index ----
	regs("len", 1, 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		switch v := args[0].(type) {
		case domain.Str:
			return domain.Int(len([]rune(string(v)))), nil
		case *domain.Arr:
			return domain.Int(len(v.Items)), nil
		case *domain.Obj:
			return domain.Int(len(v.Keys())), nil
		}
		return nil, in.errf(pos, "len() is not defined for %s", domain.TypeName(args[0]))
	})
	regs("has", 2, 2, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		switch coll := args[0].(type) {
		case *domain.Arr:
			for _, it := range coll.Items {
				if valuesEqual(it, args[1]) {
					return domain.Bool(true), nil
				}
			}
			return domain.Bool(false), nil
		case *domain.Obj:
			key, ok := indexKey(args[1])
			if !ok {
				return domain.Bool(false), nil
			}
			_, found := coll.Get(key)
			return domain.Bool(found), nil
		case domain.Str:
			return domain.Bool(strings.Contains(string(coll), args[1].String())), nil
		}
		return domain.Bool(false), nil
	})
	// `in` as a function: has(needle, haystack)
	reg("contains", 2, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		s, ok := args[0].(domain.Str)
		if !ok {
			return nil, in.errf(pos, "contains() expects a string")
		}
		return domain.Bool(strings.Contains(string(s), args[1].String())), nil
	})

	// ---- array mutation ----
	// append() and push() are the same operation; push() reads better on
	// builder-style code.
	appendArr := func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			return nil, in.errf(pos, "expects an array, got %s", domain.TypeName(args[0]))
		}
		// Value semantics: mutate a copy so `$b = append($a, x)` never
		// changes `$a` behind the caller's back.
		out := a.Copy()
		for _, extra := range args[1:] {
			if out.Elem != nil {
				if err := in.checkType(out.Elem, extra, pos); err != nil {
					return nil, err
				}
			}
			out.Append(extra)
		}
		return out, nil
	}
	reg("append", 2, appendArr)
	reg("push", 2, appendArr)
	reg("pop", 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			return nil, in.errf(pos, "pop() expects an array, got %s", domain.TypeName(args[0]))
		}
		if len(a.Items) == 0 {
			return domain.Null{}, nil
		}
		return a.Copy().Pop(), nil
	})
	reg("at", 2, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			return nil, in.errf(pos, "at() expects an array, got %s", domain.TypeName(args[0]))
		}
		i, ok := domain.AsInt(args[1])
		if !ok {
			return nil, in.errf(pos, "at() expects a numeric index")
		}
		n := int(i)
		if n < 0 {
			n += len(a.Items)
		}
		if n < 0 || n >= len(a.Items) {
			if len(args) > 2 {
				return args[2], nil
			}
			return domain.Null{}, nil
		}
		return a.Items[n], nil
	})
	regs("first", 1, 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			if s, isStr := args[0].(domain.Str); isStr {
				r := []rune(string(s))
				if len(r) == 0 {
					return domain.Str(""), nil
				}
				return domain.Str(string(r[0])), nil
			}
			return nil, in.errf(pos, "first() expects an array, got %s", domain.TypeName(args[0]))
		}
		if len(a.Items) == 0 {
			return domain.Null{}, nil
		}
		return a.Items[0], nil
	})
	regs("last", 1, 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			return nil, in.errf(pos, "last() expects an array, got %s", domain.TypeName(args[0]))
		}
		if len(a.Items) == 0 {
			return domain.Null{}, nil
		}
		return a.Items[len(a.Items)-1], nil
	})
	reg("slice", 2, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			if s, isStr := args[0].(domain.Str); isStr {
				r := []rune(string(s))
				lo, hi, err := sliceBounds(in, args[1:], len(r), pos)
				if err != nil {
					return nil, err
				}
				return domain.Str(string(r[lo:hi])), nil
			}
			return nil, in.errf(pos, "slice() expects an array or string")
		}
		lo, hi, err := sliceBounds(in, args[1:], len(a.Items), pos)
		if err != nil {
			return nil, err
		}
		out := make([]domain.Value, hi-lo)
		copy(out, a.Items[lo:hi])
		return &domain.Arr{Items: out, Elem: a.Elem}, nil
	})
	regs("join", 1, 2, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			return nil, in.errf(pos, "join() expects an array")
		}
		sep := ""
		if len(args) > 1 {
			sep = args[1].String()
		}
		parts := make([]string, 0, len(a.Items))
		for _, it := range a.Items {
			parts = append(parts, it.String())
		}
		return domain.Str(strings.Join(parts, sep)), nil
	})
	reg("reverse", 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			if s, isStr := args[0].(domain.Str); isStr {
				r := []rune(string(s))
				for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
					r[i], r[j] = r[j], r[i]
				}
				return domain.Str(string(r)), nil
			}
			return nil, in.errf(pos, "reverse() expects an array or string")
		}
		out := &domain.Arr{Items: make([]domain.Value, len(a.Items)), Elem: a.Elem}
		for i, it := range a.Items {
			out.Items[len(a.Items)-1-i] = it
		}
		return out, nil
	})
	reg("unique", 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			return nil, in.errf(pos, "unique() expects an array")
		}
		out := &domain.Arr{Elem: a.Elem}
		for _, it := range a.Items {
			dup := false
			for _, seen := range out.Items {
				if valuesEqual(seen, it) {
					dup = true
					break
				}
			}
			if !dup {
				out.Append(it)
			}
		}
		return out, nil
	})

	// ---- transformation ----
	reg("map", 2, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			return nil, in.errf(pos, "map() expects an array, got %s", domain.TypeName(args[0]))
		}
		out := &domain.Arr{}
		for i, it := range a.Items {
			// map(f, array) argument order is also accepted.
			res, err := in.callFnHelper(args[1], []domain.Value{it, domain.Int(i)}, pos)
			if err != nil {
				return nil, err
			}
			out.Append(res)
		}
		return out, nil
	})
	reg("filter", 2, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			return nil, in.errf(pos, "filter() expects an array, got %s", domain.TypeName(args[0]))
		}
		out := &domain.Arr{Elem: a.Elem}
		for i, it := range a.Items {
			keep, err := in.callFnHelper(args[1], []domain.Value{it, domain.Int(i)}, pos)
			if err != nil {
				return nil, err
			}
			if domain.Truthy(keep) {
				out.Append(it)
			}
		}
		return out, nil
	})
	reg("reduce", 2, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			return nil, in.errf(pos, "reduce() expects an array, got %s", domain.TypeName(args[0]))
		}
		var acc domain.Value
		start := 0
		if len(args) >= 3 {
			acc = args[2]
		} else {
			if len(a.Items) == 0 {
				return domain.Null{}, nil
			}
			acc = a.Items[0]
			start = 1
		}
		for i := start; i < len(a.Items); i++ {
			var err error
			acc, err = in.callFnHelper(args[1], []domain.Value{acc, a.Items[i]}, pos)
			if err != nil {
				return nil, err
			}
		}
		return acc, nil
	})
	reg("find", 2, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			return nil, in.errf(pos, "find() expects an array, got %s", domain.TypeName(args[0]))
		}
		for i, it := range a.Items {
			hit, err := in.callFnHelper(args[1], []domain.Value{it, domain.Int(i)}, pos)
			if err != nil {
				return nil, err
			}
			if domain.Truthy(hit) {
				return it, nil
			}
		}
		return domain.Null{}, nil
	})
	reg("sort", 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			return nil, in.errf(pos, "sort() expects an array, got %s", domain.TypeName(args[0]))
		}
		out := a.Copy()
		if len(args) > 1 {
			cmpFn := args[1]
			var serr error
			// Insertion sort keeps the comparator in Go land: stable and short.
			for i := 1; i < len(out.Items) && serr == nil; i++ {
				for j := i; j > 0; j-- {
					less, err := in.callFnHelper(cmpFn, []domain.Value{out.Items[j], out.Items[j-1]}, pos)
					if err != nil {
						serr = err
						break
					}
					if !domain.Truthy(less) {
						break
					}
					out.Items[j], out.Items[j-1] = out.Items[j-1], out.Items[j]
				}
			}
			if serr != nil {
				return nil, serr
			}
			return out, nil
		}
		var serr error
		sortValues(out.Items, func(a, b domain.Value) bool {
			if serr != nil {
				return false
			}
			c, err := compareValues(a, b)
			if err != nil {
				serr = err
				return false
			}
			return c < 0
		})
		if serr != nil {
			return nil, serr
		}
		return out, nil
	})

	// ---- objects ----
	regs("keys", 1, 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		o, ok := args[0].(*domain.Obj)
		if !ok {
			return nil, in.errf(pos, "keys() expects an object")
		}
		out := &domain.Arr{Items: make([]domain.Value, 0, len(o.Keys()))}
		for _, k := range o.Keys() {
			out.Append(domain.Str(k))
		}
		return out, nil
	})
	regs("values", 1, 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		o, ok := args[0].(*domain.Obj)
		if !ok {
			if a, isArr := args[0].(*domain.Arr); isArr {
				out := &domain.Arr{}
				for _, it := range a.Items {
					out.Append(it)
				}
				return out, nil
			}
			return nil, in.errf(pos, "values() expects an object")
		}
		out := &domain.Arr{}
		for _, k := range o.Keys() {
			v, _ := o.Get(k)
			out.Append(v)
		}
		return out, nil
	})
	reg("merge", 2, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		out := domain.NewObj()
		for _, a := range args {
			switch src := a.(type) {
			case *domain.Obj:
				for _, k := range src.Keys() {
					v, _ := src.Get(k)
					out.Set(k, v)
				}
			case *domain.Arr:
				if o, ok := src.Items[0].(*domain.Obj); ok {
					for _, k := range o.Keys() {
						v, _ := o.Get(k)
						out.Set(k, v)
					}
				}
			default:
				return nil, in.errf(pos, "merge() expects objects, got %s", domain.TypeName(a))
			}
		}
		return out, nil
	})
	reg("unset", 2, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		o, ok := args[0].(*domain.Obj)
		if !ok {
			return nil, in.errf(pos, "unset() expects an object")
		}
		key, ok := indexKey(args[1])
		if !ok {
			return nil, in.errf(pos, "unset() expects a string key")
		}
		return domain.Bool(o.Delete(key)), nil
	})
	reg("to_object", 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		a, ok := args[0].(*domain.Arr)
		if !ok {
			if o, isObj := args[0].(*domain.Obj); isObj {
				return o, nil
			}
			return nil, in.errf(pos, "to_object() expects an array")
		}
		out := domain.NewObj()
		for i, it := range a.Items {
			switch kv := it.(type) {
			case *domain.Obj:
				if len(kv.Keys()) >= 2 {
					k0, _ := kv.Get(kv.Keys()[0])
					k1, _ := kv.Get(kv.Keys()[1])
					key, ok := indexKey(k0)
					if ok {
						out.Set(key, k1)
						continue
					}
				}
				out.Set(strconvItoa(i), it)
			default:
				out.Set(strconvItoa(i), it)
			}
		}
		return out, nil
	})

	// ---- numbers ----
	regs("abs", 1, 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		switch v := args[0].(type) {
		case domain.Int:
			if v < 0 {
				return domain.Int(-int64(v)), nil
			}
			return v, nil
		case domain.Float:
			return domain.Float(absFloat(float64(v))), nil
		}
		return nil, in.errf(pos, "abs() expects a number")
	})
	regs("min", 1, -1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		return minMax(in, args, pos, true)
	})
	regs("max", 1, -1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		return minMax(in, args, pos, false)
	})
	regs("sum", 1, 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		return sumArray(in, args[0], pos)
	})

	reg("parse_int", 2, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		s, ok := args[0].(domain.Str)
		if !ok {
			return nil, in.errf(pos, "parse_int() expects a string")
		}
		base := 10
		if bi, ok := domain.AsInt(args[1]); ok {
			base = int(bi)
		}
		n, ok := parseIntBase(string(s), base)
		if !ok {
			return nil, in.errf(pos, "invalid integer %q", string(s))
		}
		return domain.Int(n), nil
	})
	reg("str_repeat", 2, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		s, _ := args[0].(domain.Str)
		n, ok := domain.AsInt(args[1])
		if !ok || n < 0 {
			return domain.Str(""), nil
		}
		return domain.Str(strings.Repeat(string(s), int(n))), nil
	})
	reg("now_ms", 0, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		return domain.Int(nowMillis()), nil
	})

	// ---- json ----
	reg("json_encode", 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		s, err := jsonEncode(args[0])
		if err != nil {
			return nil, in.errf(pos, "%s", err.Error())
		}
		return domain.Str(s), nil
	})
	reg("json_decode", 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		s, ok := args[0].(domain.Str)
		if !ok {
			return nil, in.errf(pos, "json_decode() expects a string")
		}
		v, err := jsonDecode(string(s))
		if err != nil {
			return nil, in.errf(pos, "invalid JSON: %s", err.Error())
		}
		return v, nil
	})
	reg("html_escape", 1, func(in *Interp, args []domain.Value, pos domain.Position) (domain.Value, error) {
		return domain.Str(domain.EscapeHTML(args[0].String())), nil
	})
	return b
}

// callFnHelper calls a callable value with a fixed argument list, used by the
// higher-order builtins. When the callee accepts a single parameter the extra
// index argument is dropped, so `map(xs, fn(x) ...)` and
// `map(xs, fn(x, i) ...)` both work.
func (in *Interp) callFnHelper(fn domain.Value, args []domain.Value, pos domain.Position) (domain.Value, error) {
	if f, ok := fn.(*closure); ok && len(args) > 1 && len(f.fn.params) == 1 {
		args = args[:1]
	}
	return in.callValue(fn, args, pos)
}

func sliceBounds(in *Interp, rest []domain.Value, length int, pos domain.Position) (int, int, error) {
	lo, hi := 0, length
	if len(rest) > 0 {
		if v, ok := domain.AsInt(rest[0]); ok {
			lo = clampIndex(int(v), length)
		}
	}
	if len(rest) > 1 {
		if v, ok := domain.AsInt(rest[1]); ok {
			hi = clampIndex(int(v), length)
		}
	}
	if hi < lo {
		hi = lo
	}
	return lo, hi, nil
}

// sumArray totals an array of numbers, staying integral when possible.
func sumArray(in *Interp, v domain.Value, pos domain.Position) (domain.Value, error) {
	a, ok := v.(*domain.Arr)
	if !ok {
		return nil, in.errf(pos, "expects an array of numbers, got %s", domain.TypeName(v))
	}
	var isum int64
	var fsum float64
	anyFloat := false
	for _, it := range a.Items {
		switch n := it.(type) {
		case domain.Int:
			isum += int64(n)
			fsum += float64(n)
		case domain.Float:
			fsum += float64(n)
			anyFloat = true
		default:
			return nil, in.errf(pos, "expects numbers, got %s", domain.TypeName(it))
		}
	}
	if anyFloat {
		return domain.Float(fsum), nil
	}
	return domain.Int(isum), nil
}

func minMax(in *Interp, args []domain.Value, pos domain.Position, wantMin bool) (domain.Value, error) {
	// min/max accept either varargs or a single array.
	var list []domain.Value
	if len(args) == 1 {
		if a, ok := args[0].(*domain.Arr); ok {
			list = a.Items
		} else {
			list = args
		}
	} else {
		list = args
	}
	if len(list) == 0 {
		return domain.Null{}, nil
	}
	best := list[0]
	for _, v := range list[1:] {
		c, err := compareValues(v, best)
		if err != nil {
			return nil, err
		}
		if (wantMin && c < 0) || (!wantMin && c > 0) {
			best = v
		}
	}
	return best, nil
}

func sortValues(items []domain.Value, less func(a, b domain.Value) bool) {
	// Simple insertion sort: stable, no reflection, predictable for scripts.
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && less(items[j], items[j-1]); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
