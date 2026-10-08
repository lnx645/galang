package interp

import (
	"fmt"
	"math"
	"strings"

	"garurda/internal/domain"
)

// Value operations shared by the compiled code. These stay interpreter-aware
// because builtins and the standard library are implemented in terms of them.

// callValue invokes any callable value.
func (in *Interp) callValue(callee domain.Value, args []domain.Value, pos domain.Position) (domain.Value, error) {
	switch c := callee.(type) {
	case *closure:
		// An async call returns a promise; the body runs when awaited.
		if c.fn.async {
			argsCopy := append([]domain.Value(nil), args...)
			return in.newPromise(pos, func() (domain.Value, error) {
				return in.callClosure(c, argsCopy, pos)
			}), nil
		}
		// Bytecode first: one flat loop, no boxed values.
		if c.code != nil && len(args) == c.code.nparams {
			unboxed := true
			for _, a := range args {
				if _, ok := a.(domain.Int); !ok {
					unboxed = false
					break
				}
			}
			if unboxed {
				vals := make([]int64, len(args))
				for i, a := range args {
					vals[i] = int64(a.(domain.Int))
				}
				v, err := in.runInt(c.code, vals, pos)
				if err != nil {
					return nil, err
				}
				return domain.Int(v), nil
			}
		}
		// An integer-only function entered from the general path takes the
		// unboxed route, so the whole call tree stays unboxed.
		if c.int != nil && len(args) == c.int.nparams {
			unboxed := make([]int64, len(args))
			allInt := true
			for i, a := range args {
				n, ok := a.(domain.Int)
				if !ok {
					allInt = false
					break
				}
				unboxed[i] = int64(n)
			}
			if allInt {
				v, err := in.callInt(c, unboxed, pos)
				if err != nil {
					return nil, err
				}
				return domain.Int(v), nil
			}
		}
		return in.callClosure(c, args, pos)
	case *domain.Builtin:
		if c.MinArgs > 0 && len(args) < c.MinArgs {
			return nil, in.errf(pos, "%s() expects at least %d argument(s), got %d", c.Name, c.MinArgs, len(args))
		}
		return c.Fn(args, pos)
	}
	return nil, in.errf(pos, "%s is not callable", domain.TypeName(callee))
}

// callClosure runs a compiled function.
func (in *Interp) callClosure(c *closure, args []domain.Value, pos domain.Position) (domain.Value, error) {
	in.depth++
	if in.depth > in.maxDepth {
		in.depth--
		return nil, in.errf(pos, "maximum call depth exceeded (%d): recursion too deep?", in.maxDepth)
	}
	f, err := in.newFrame(c.fn, c.def, args, pos)
	if err != nil {
		in.depth--
		return nil, err
	}
	// Record the call site: 24 bytes into a fixed array, no allocation.
	if in.traceDepth < len(in.tracePos) {
		in.tracePos[in.traceDepth] = pos
		in.traceFn[in.traceDepth] = c.fn
	}
	in.traceDepth++

	k, ret, runErr := c.fn.body(f)
	f.release(&in.frames)
	in.traceDepth--
	in.depth--

	if runErr != nil {
		if re, ok := runErr.(*Error); ok {
			return nil, re.Frame(StackFrame{Func: displayName(c.fn.name), File: in.File, Pos: pos})
		}
		return nil, runErr
	}
	if k == ctrlReturn {
		if c.fn.retType != nil {
			if err := in.checkType(c.fn.retType, ret, pos); err != nil {
				return nil, err
			}
			if c.fn.elem != nil {
				if a, ok := ret.(*domain.Arr); ok {
					a.Elem = c.fn.elem
				}
			}
		}
		return ret, nil
	}
	return domain.Null{}, nil
}

// callMethodOn dispatches `recv.name(args)` where args already starts with recv.
func (in *Interp) callMethodOn(recv domain.Value, name string, args []domain.Value, pos domain.Position) (domain.Value, error) {
	if obj, ok := recv.(*domain.Obj); ok {
		if v, found := obj.Get(name); found {
			switch v.(type) {
			case *domain.Builtin, *closure:
				return in.callValue(v, args[1:], pos)
			}
		}
		return nil, in.errf(pos, "%s has no function '%s'", domain.TypeName(recv), name)
	}
	if m, ok := builtinMethod(recv, name); ok {
		return m(in, args, pos)
	}
	if m, ok := valueMethod(recv, name); ok {
		return m(in, args, pos)
	}
	return nil, in.errf(pos, "%s has no method '%s'", domain.TypeName(recv), name)
}

// index reads `recv[idx]`.
func (in *Interp) index(recv, idx domain.Value, p domain.Position) (domain.Value, error) {
	switch r := recv.(type) {
	case *domain.Arr:
		n, err := arrIndex(idx, p, "array", len(r.Items))
		if err != nil {
			return nil, err
		}
		return r.Items[n], nil
	case *domain.Obj:
		key, ok := indexKey(idx)
		if !ok {
			return nil, in.errf(p, "object key must be a string, got %s", domain.TypeName(idx))
		}
		v, found := r.Get(key)
		if !found {
			// Missing payload fields read as null rather than raising, so
			// templates and JSON handling stay forgiving.
			return domain.Null{}, nil
		}
		return v, nil
	case domain.Str:
		runes := []rune(string(r))
		n, err := arrIndex(idx, p, "string", len(runes))
		if err != nil {
			return nil, err
		}
		return domain.Str(string(runes[n])), nil
	}
	return nil, in.errf(p, "cannot index %s", domain.TypeName(recv))
}

// arrIndex normalises an index value, rejecting out-of-range access.
func arrIndex(idx domain.Value, p domain.Position, what string, length int) (int, error) {
	i, ok := domain.AsInt(idx)
	if !ok {
		return 0, &Error{Msg: what + " index must be a number, got " + domain.TypeName(idx), Pos: p}
	}
	n := int(i)
	if n < 0 {
		n += length
	}
	if n < 0 || n >= length {
		return 0, &Error{
			Msg: fmt.Sprintf("index %d out of range (%s has %d items)", i, what, length),
			Pos: p,
		}
	}
	return n, nil
}

// prop reads `recv.field`.
func (in *Interp) prop(recv domain.Value, name string, p domain.Position) (domain.Value, error) {
	if obj, ok := recv.(*domain.Obj); ok {
		if v, found := obj.Get(name); found {
			return v, nil
		}
		return domain.Null{}, nil
	}
	if s, ok := recv.(domain.Str); ok {
		if v, found := stringProp(string(s), name); found {
			return v, nil
		}
	}
	if ev, ok := recv.(*domain.ErrorValue); ok {
		switch name {
		case "message":
			return domain.Str(ev.Message), nil
		case "code":
			return domain.Str(ev.Code), nil
		case "status":
			return domain.Int(ev.Status), nil
		case "stack":
			out := &domain.Arr{}
			for _, line := range ev.Stack {
				out.Append(domain.Str(line))
			}
			return out, nil
		}
	}
	if a, ok := recv.(*domain.Arr); ok {
		if v, found := arrayProp(a, name); found {
			return v, nil
		}
	}
	if recv == nil {
		return nil, in.errf(p, "cannot read '%s' of null", name)
	}
	return nil, in.errf(p, "%s has no field '%s'", domain.TypeName(recv), name)
}

// slice implements `recv[low:high]` with already compiled bounds.
func (in *Interp) slice(recv domain.Value, low, high exprFn, p domain.Position, f *frame) (domain.Value, error) {
	var src []domain.Value
	isStr := false
	switch r := recv.(type) {
	case *domain.Arr:
		src = r.Items
	case domain.Str:
		isStr = true
		for _, ru := range []rune(string(r)) {
			src = append(src, domain.Str(string(ru)))
		}
	default:
		return nil, in.errf(p, "cannot slice %s", domain.TypeName(recv))
	}
	length := len(src)
	lo := 0
	hi := length
	if low != nil {
		lv, err := low(f)
		if err != nil {
			return nil, err
		}
		v, ok := domain.AsInt(lv)
		if !ok {
			return nil, in.errf(p, "slice bound must be a number")
		}
		lo = clampIndex(int(v), length)
	}
	if high != nil {
		hv, err := high(f)
		if err != nil {
			return nil, err
		}
		v, ok := domain.AsInt(hv)
		if !ok {
			return nil, in.errf(p, "slice bound must be a number")
		}
		hi = clampIndex(int(v), length)
	}
	if hi < lo {
		hi = lo
	}
	if isStr {
		var b strings.Builder
		for _, v := range src[lo:hi] {
			b.WriteString(v.String())
		}
		return domain.Str(b.String()), nil
	}
	out := make([]domain.Value, hi-lo)
	copy(out, src[lo:hi])
	return &domain.Arr{Items: out}, nil
}

func clampIndex(i, length int) int {
	if i < 0 {
		i += length
	}
	if i < 0 {
		return 0
	}
	if i > length {
		return length
	}
	return i
}

// setIndex writes `recv[idx] = val`.
func (in *Interp) setIndex(recv, idx, val domain.Value, p domain.Position) error {
	switch r := recv.(type) {
	case *domain.Arr:
		n, err := arrIndex(idx, p, "array", len(r.Items))
		if err != nil {
			return err
		}
		if r.Elem != nil {
			if err := in.checkType(r.Elem, val, p); err != nil {
				return err
			}
		}
		r.Set(n, val)
		return nil
	case *domain.Obj:
		key, ok := indexKey(idx)
		if !ok {
			return in.errf(p, "object key must be a string, got %s", domain.TypeName(idx))
		}
		r.Set(key, val)
		return nil
	}
	return in.errf(p, "cannot index %s for assignment", domain.TypeName(recv))
}

// setProp writes `recv.name = val`.
func (in *Interp) setProp(recv domain.Value, name string, val domain.Value, p domain.Position) error {
	switch r := recv.(type) {
	case *domain.Obj:
		r.Set(name, val)
		return nil
	case *domain.ErrorValue:
		switch name {
		case "message":
			r.Message = val.String()
			return nil
		case "code":
			r.Code = val.String()
			return nil
		case "status":
			if n, ok := domain.AsInt(val); ok {
				r.Status = int(n)
			}
			return nil
		}
	}
	return in.errf(p, "cannot set property '%s' on %s", name, domain.TypeName(recv))
}

// readSlot resolves a variable for a runtime helper.
func (in *Interp) readSlot(f *frame, slot *cslot, name string, pos domain.Position) (domain.Value, error) {
	if slot == nil {
		if v, ok := f.dyn[name]; ok {
			return v, nil
		}
		if v, ok := f.glob.dyn[name]; ok {
			return v, nil
		}
		return nil, in.errf(pos, "undefined variable '%s'", name)
	}
	v, ok := f.load(slot.kind, slot.idx, name)
	if !ok || v == nil {
		return nil, in.errf(pos, "undefined variable '%s'", name)
	}
	return v, nil
}

// binaryOp applies a binary operator without coercion.
func (in *Interp) binaryOp(l domain.Value, op domain.TokenType, r domain.Value, p domain.Position) (domain.Value, error) {
	switch op {
	case domain.TokenPlus:
		return in.opPlus(l, r, p)
	case domain.TokenMinus, domain.TokenStar, domain.TokenSlash, domain.TokenPercent:
		return in.opArith(l, op, r, p)
	case domain.TokenEq, domain.TokenNeq:
		eq := valuesEqual(l, r)
		if op == domain.TokenNeq {
			return domain.Bool(!eq), nil
		}
		return domain.Bool(eq), nil
	case domain.TokenLt, domain.TokenLtEq, domain.TokenGt, domain.TokenGtEq:
		return in.opCompare(l, op, r, p)
	case domain.TokenIn:
		found, err := in.membership(l, r, p)
		if err != nil {
			return nil, err
		}
		return domain.Bool(found), nil
	}
	return nil, in.errf(p, "unsupported operator %q", string(op))
}

func (in *Interp) opPlus(l, r domain.Value, p domain.Position) (domain.Value, error) {
	ls, lIsStr := l.(domain.Str)
	rs, rIsStr := r.(domain.Str)
	if lIsStr || rIsStr {
		if !lIsStr {
			return nil, in.errf(p, "cannot add %s to string (use str(%s))", domain.TypeName(l), domain.TypeName(l))
		}
		if !rIsStr {
			return nil, in.errf(p, "cannot add %s to string (use str(%s))", domain.TypeName(r), domain.TypeName(r))
		}
		return domain.Str(string(ls) + string(rs)), nil
	}
	if la, ok := l.(*domain.Arr); ok {
		if ra, ok := r.(*domain.Arr); ok {
			out := &domain.Arr{Items: make([]domain.Value, 0, len(la.Items)+len(ra.Items))}
			out.Items = append(out.Items, la.Items...)
			out.Items = append(out.Items, ra.Items...)
			return out, nil
		}
	}
	return in.opArith(l, domain.TokenPlus, r, p)
}

func (in *Interp) opArith(l domain.Value, op domain.TokenType, r domain.Value, p domain.Position) (domain.Value, error) {
	if li, ok := l.(domain.Int); ok {
		if ri, ok := r.(domain.Int); ok {
			return intArith(int64(li), op, int64(ri), func(_ domain.Position, format string, args ...interface{}) error {
				return in.errf(p, format, args...)
			})
		}
	}
	lf, ok1 := domain.AsFloat(l)
	rf, ok2 := domain.AsFloat(r)
	if !ok1 || !ok2 {
		return nil, in.errf(p, "cannot apply '%s' to %s and %s", string(op), domain.TypeName(l), domain.TypeName(r))
	}
	switch op {
	case domain.TokenPlus:
		return domain.Float(lf + rf), nil
	case domain.TokenMinus:
		return domain.Float(lf - rf), nil
	case domain.TokenStar:
		return domain.Float(lf * rf), nil
	case domain.TokenSlash:
		if rf == 0 {
			return nil, in.errf(p, "division by zero")
		}
		return domain.Float(lf / rf), nil
	default:
		if rf == 0 {
			return nil, in.errf(p, "modulo by zero")
		}
		return domain.Float(math.Mod(lf, rf)), nil
	}
}

// intArith keeps integer arithmetic integral.
func intArith(a int64, op domain.TokenType, b int64, errf func(pos domain.Position, format string, args ...interface{}) error) (domain.Value, error) {
	switch op {
	case domain.TokenPlus:
		return domain.Int(a + b), nil
	case domain.TokenMinus:
		return domain.Int(a - b), nil
	case domain.TokenStar:
		return domain.Int(a * b), nil
	case domain.TokenSlash:
		if b == 0 {
			return nil, errf(domain.Position{}, "division by zero")
		}
		if a%b == 0 {
			return domain.Int(a / b), nil
		}
		return domain.Float(float64(a) / float64(b)), nil
	default:
		if b == 0 {
			return nil, errf(domain.Position{}, "modulo by zero")
		}
		return domain.Int(a % b), nil
	}
}

// membership implements `needle in haystack`.
func (in *Interp) membership(needle, haystack domain.Value, p domain.Position) (bool, error) {
	switch coll := haystack.(type) {
	case *domain.Arr:
		for _, it := range coll.Items {
			if valuesEqual(it, needle) {
				return true, nil
			}
		}
		return false, nil
	case *domain.Obj:
		key, ok := indexKey(needle)
		if !ok {
			return false, in.errf(p, "'in' on an object needs a string key, got %s", domain.TypeName(needle))
		}
		_, found := coll.Get(key)
		return found, nil
	case domain.Str:
		return strings.Contains(string(coll), needle.String()), nil
	}
	return false, in.errf(p, "'in' expects an array, object or string, got %s", domain.TypeName(haystack))
}

func (in *Interp) opCompare(l domain.Value, op domain.TokenType, r domain.Value, p domain.Position) (domain.Value, error) {
	cmp, err := compareValues(l, r)
	if err != nil {
		return nil, in.errf(p, "cannot compare %s with %s", domain.TypeName(l), domain.TypeName(r))
	}
	switch op {
	case domain.TokenLt:
		return domain.Bool(cmp < 0), nil
	case domain.TokenLtEq:
		return domain.Bool(cmp <= 0), nil
	case domain.TokenGt:
		return domain.Bool(cmp > 0), nil
	default:
		return domain.Bool(cmp >= 0), nil
	}
}

// compareValues returns -1, 0 or 1 for orderable operands.
func compareValues(l, r domain.Value) (int, error) {
	switch lv := l.(type) {
	case domain.Int:
		if rv, ok := r.(domain.Int); ok {
			return cmpInt(int64(lv), int64(rv)), nil
		}
		if rf, ok := domain.AsFloat(r); ok {
			return cmpFloat(float64(lv), rf), nil
		}
	case domain.Float:
		if rf, ok := domain.AsFloat(r); ok {
			return cmpFloat(float64(lv), rf), nil
		}
	case domain.Str:
		if rs, ok := r.(domain.Str); ok {
			return strings.Compare(string(lv), string(rs)), nil
		}
	case domain.Bool:
		if rv, ok := r.(domain.Bool); ok {
			a, b := 0, 0
			if lv {
				a = 1
			}
			if rv {
				b = 1
			}
			return a - b, nil
		}
	}
	return 0, errNotOrderable
}

var errNotOrderable = errStr("values are not orderable")

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// valuesEqual compares without coercion: "1" != 1.
func valuesEqual(a, b domain.Value) bool {
	if domain.TypeName(a) != domain.TypeName(b) {
		// int and float compare numerically.
		_, aIsStr := a.(domain.Str)
		_, bIsStr := b.(domain.Str)
		_, aIsBool := a.(domain.Bool)
		_, bIsBool := b.(domain.Bool)
		if aIsStr || bIsStr || aIsBool || bIsBool {
			return false
		}
		af, ok1 := domain.AsFloat(a)
		bf, ok2 := domain.AsFloat(b)
		return ok1 && ok2 && af == bf
	}
	switch av := a.(type) {
	case domain.Null:
		return true
	case domain.Bool:
		return bool(av) == bool(b.(domain.Bool))
	case domain.Int:
		return int64(av) == int64(b.(domain.Int))
	case domain.Float:
		return float64(av) == float64(b.(domain.Float))
	case domain.Str:
		return string(av) == string(b.(domain.Str))
	case *domain.Arr:
		bv := b.(*domain.Arr)
		if len(av.Items) != len(bv.Items) {
			return false
		}
		for i := range av.Items {
			if !valuesEqual(av.Items[i], bv.Items[i]) {
				return false
			}
		}
		return true
	case *domain.Obj:
		bv := b.(*domain.Obj)
		for _, k := range av.Keys() {
			av2, ok := av.Get(k)
			if !ok {
				return false
			}
			bv2, ok := bv.Get(k)
			if !ok || !valuesEqual(av2, bv2) {
				return false
			}
		}
		return true
	case *closure:
		return av == b.(*closure)
	case *domain.Builtin:
		return av == b.(*domain.Builtin)
	case *domain.ErrorValue:
		bv := b.(*domain.ErrorValue)
		return av.Message == bv.Message && av.Code == bv.Code
	}
	return false
}

// indexKey normalises an object key to a string.
func indexKey(idx domain.Value) (string, bool) {
	if s, ok := idx.(domain.Str); ok {
		return string(s), true
	}
	return "", false
}

// stringProp exposes read-only string properties such as `s.len`.
func stringProp(s, name string) (domain.Value, bool) {
	switch name {
	case "len":
		return domain.Int(len([]rune(s))), true
	case "upper":
		return domain.Str(strings.ToUpper(s)), true
	case "lower":
		return domain.Str(strings.ToLower(s)), true
	case "trim":
		return domain.Str(strings.TrimSpace(s)), true
	case "reversed":
		rev := make([]rune, 0, len(s))
		for _, r := range []rune(s) {
			rev = append([]rune{r}, rev...)
		}
		return domain.Str(string(rev)), true
	}
	return nil, false
}

// arrayProp exposes read-only array properties such as `a.len`.
func arrayProp(a *domain.Arr, name string) (domain.Value, bool) {
	switch name {
	case "len":
		return domain.Int(len(a.Items)), true
	case "first":
		if len(a.Items) == 0 {
			return domain.Null{}, true
		}
		return a.Items[0], true
	case "last":
		if len(a.Items) == 0 {
			return domain.Null{}, true
		}
		return a.Items[len(a.Items)-1], true
	case "joined":
		parts := make([]string, 0, len(a.Items))
		for _, it := range a.Items {
			parts = append(parts, it.String())
		}
		return domain.Str(strings.Join(parts, ",")), true
	}
	return nil, false
}
