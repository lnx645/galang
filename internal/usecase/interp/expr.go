package interp

import (
	"math"
	"strings"
	"unicode/utf8"

	"garurda/internal/domain"
)

// ---- expression evaluation ----

func (in *Interp) eval(e domain.Expr, scope *scope) (domain.Value, error) {
	switch x := e.(type) {
	case *domain.IntLit:
		if v, ok := domain.CacheInt(x.Value); ok {
			return v, nil
		}
		return domain.Int(x.Value), nil
	case *domain.FloatLit:
		return domain.Float(x.Value), nil
	case *domain.BoolLit:
		return domain.Bool(x.Value), nil
	case *domain.NullLit:
		return domain.Null{}, nil
	case *domain.StrLit:
		return in.evalString(x, scope)
	case *domain.Ident:
		v, _, ok := scope.lookup(x.Name)
		if !ok {
			return nil, in.errf(x.P, "undefined variable '%s'", x.Name)
		}
		return v, nil
	case *domain.ListLit:
		return in.evalList(x, scope)
	case *domain.ObjectLit:
		return in.evalObject(x, scope)
	case *domain.GroupExpr:
		return in.eval(x.Inner, scope)
	case *domain.FnExpr:
		return &Fn{Name: x.Name, Params: x.Params, Body: x.Body, Ret: x.Ret, Elem: retElemType(x), Env: scope}, nil
	case *domain.IndexExpr:
		return in.evalIndex(x, scope)
	case *domain.SliceExpr:
		return in.evalSlice(x, scope)
	case *domain.PropExpr:
		return in.evalProp(x, scope)
	case *domain.CallExpr:
		return in.evalCall(x, scope)
	case *domain.MethodExpr:
		return in.evalMethod(x, scope)
	case *domain.PrefixExpr:
		return in.evalPrefix(x, scope)
	case *domain.InfixExpr:
		return in.evalInfix(x, scope)
	case *domain.TernaryExpr:
		cond, err := in.eval(x.Cond, scope)
		if err != nil {
			return nil, err
		}
		if domain.Truthy(cond) {
			return in.eval(x.Conseq, scope)
		}
		return in.eval(x.Alt, scope)
	case *domain.RangeExpr:
		return in.evalRange(x, scope)
	}
	return nil, in.errf(e.Pos(), "unsupported expression %T", e)
}

func (in *Interp) evalString(x *domain.StrLit, env *scope) (domain.Value, error) {
	if len(x.Parts) == 1 && !x.Parts[0].IsExpr {
		return domain.Str(x.Parts[0].Lit), nil
	}
	var b strings.Builder
	for _, part := range x.Parts {
		if !part.IsExpr {
			b.WriteString(part.Lit)
			continue
		}
		v, err := in.eval(part.Expr, env)
		if err != nil {
			return nil, err
		}
		b.WriteString(v.String())
	}
	return domain.Str(b.String()), nil
}

func (in *Interp) evalList(x *domain.ListLit, env *scope) (domain.Value, error) {
	if x.CompSrc != nil {
		return in.evalComprehension(x, env)
	}
	items := make([]domain.Value, 0, len(x.Elems))
	for _, el := range x.Elems {
		// `[0..10]` expands a range into an array literal.
		if rng, ok := el.(*domain.RangeExpr); ok {
			v, err := in.evalRange(rng, env)
			if err != nil {
				return nil, err
			}
			items = append(items, v.(*domain.Arr).Items...)
			continue
		}
		v, err := in.eval(el, env)
		if err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return &domain.Arr{Items: items}, nil
}

// evalComprehension evaluates `[expr for x in src if cond]`.
func (in *Interp) evalComprehension(x *domain.ListLit, env *scope) (domain.Value, error) {
	src, err := in.eval(x.CompSrc, env)
	if err != nil {
		return nil, err
	}
	out := &domain.Arr{}
	emit := func(child *scope) error {
		if x.CompCond != nil {
			c, err := in.eval(x.CompCond, child)
			if err != nil {
				return err
			}
			if !domain.Truthy(c) {
				return nil
			}
		}
		v, err := in.eval(x.CompElem, child)
		if err != nil {
			return err
		}
		out.Append(v)
		return nil
	}

	switch coll := src.(type) {
	case *domain.Arr:
		for i, item := range coll.Items {
			child := newScope(env)
			child.define(x.CompVar, item, nil)
			if x.CompIdx != "" {
				child.define(x.CompIdx, domain.Int(i), nil)
			}
			if err := emit(child); err != nil {
				return nil, err
			}
		}
	case *domain.Obj:
		for _, k := range coll.Keys() {
			v, _ := coll.Get(k)
			child := newScope(env)
			child.define(x.CompVar, domain.Str(k), nil)
			if x.CompIdx != "" {
				child.define(x.CompIdx, v, nil)
			}
			if err := emit(child); err != nil {
				return nil, err
			}
		}
	case domain.Str:
		for _, r := range string(coll) {
			child := newScope(env)
			child.define(x.CompVar, domain.Str(string(r)), nil)
			if err := emit(child); err != nil {
				return nil, err
			}
		}
	default:
		return nil, in.errf(x.P, "cannot iterate over %s in comprehension", domain.TypeName(src))
	}
	return out, nil
}

func (in *Interp) evalObject(x *domain.ObjectLit, env *scope) (domain.Value, error) {
	obj := domain.NewObj()
	for _, pair := range x.Pairs {
		v, err := in.eval(pair.Value, env)
		if err != nil {
			return nil, err
		}
		obj.Set(pair.Key, v)
	}
	return obj, nil
}

func (in *Interp) evalRange(x *domain.RangeExpr, env *scope) (domain.Value, error) {
	lo, err := in.eval(x.Low, env)
	if err != nil {
		return nil, err
	}
	hi, err := in.eval(x.High, env)
	if err != nil {
		return nil, err
	}
	start, ok1 := domain.AsInt(lo)
	end, ok2 := domain.AsInt(hi)
	if !ok1 || !ok2 {
		return nil, in.errf(x.P, "range bounds must be numbers")
	}
	// The default step follows the direction, so `3..1` counts down.
	step := int64(1)
	if start > end {
		step = -1
	}
	if x.Step != nil {
		sv, err := in.eval(x.Step, env)
		if err != nil {
			return nil, err
		}
		si, ok := domain.AsInt(sv)
		if !ok {
			return nil, in.errf(x.P, "range step must be a number")
		}
		if si == 0 {
			return nil, in.errf(x.P, "range step cannot be zero")
		}
		step = si
	}
	out := &domain.Arr{}
	// Both bounds are inclusive: 0..3 has four items.
	if step > 0 {
		for i := start; i <= end; i += step {
			out.Append(domain.Int(i))
		}
	} else {
		for i := start; i >= end; i += step {
			out.Append(domain.Int(i))
		}
	}
	return out, nil
}

func (in *Interp) evalIndex(x *domain.IndexExpr, env *scope) (domain.Value, error) {
	recv, err := in.eval(x.Left, env)
	if err != nil {
		return nil, err
	}
	idx, err := in.eval(x.Index, env)
	if err != nil {
		return nil, err
	}
	return in.index(recv, idx, x.P)
}

func (in *Interp) index(recv, idx domain.Value, p domain.Position) (domain.Value, error) {
	switch r := recv.(type) {
	case *domain.Arr:
		i, ok := domain.AsInt(idx)
		if !ok {
			return nil, in.errf(p, "array index must be a number, got %s", domain.TypeName(idx))
		}
		n := int(i)
		if n < 0 {
			n += len(r.Items)
		}
		if n < 0 || n >= len(r.Items) {
			return nil, in.errf(p, "index %d out of range (array has %d items)", i, len(r.Items))
		}
		return r.Items[n], nil

	case *domain.Obj:
		key, ok := indexKey(idx)
		if !ok {
			return nil, in.errf(p, "object key must be a string, got %s", domain.TypeName(idx))
		}
		v, found := r.Get(key)
		if !found {
			// Reading a missing field yields null, matching web payloads.
			return domain.Null{}, nil
		}
		return v, nil

	case domain.Str:
		runes := []rune(string(r))
		i, ok := domain.AsInt(idx)
		if !ok {
			return nil, in.errf(p, "string index must be a number, got %s", domain.TypeName(idx))
		}
		n := int(i)
		if n < 0 {
			n += len(runes)
		}
		if n < 0 || n >= len(runes) {
			return nil, in.errf(p, "index %d out of range (string has %d characters)", i, len(runes))
		}
		return domain.Str(string(runes[n])), nil
	}
	return nil, in.errf(p, "cannot index %s", domain.TypeName(recv))
}

// indexKey normalises an object key to a string.
func indexKey(idx domain.Value) (string, bool) {
	if s, ok := idx.(domain.Str); ok {
		return string(s), true
	}
	return "", false
}

func (in *Interp) evalSlice(x *domain.SliceExpr, env *scope) (domain.Value, error) {
	recv, err := in.eval(x.Left, env)
	if err != nil {
		return nil, err
	}
	lo, hi := 0, -1
	length := 0
	var src []domain.Value

	switch r := recv.(type) {
	case *domain.Arr:
		src = r.Items
	case domain.Str:
		for _, ru := range []rune(string(r)) {
			src = append(src, domain.Str(string(ru)))
		}
	default:
		return nil, in.errf(x.P, "cannot slice %s", domain.TypeName(recv))
	}
	length = len(src)

	if x.Low != nil {
		lv, err := in.eval(x.Low, env)
		if err != nil {
			return nil, err
		}
		i, ok := domain.AsInt(lv)
		if !ok {
			return nil, in.errf(x.P, "slice bound must be a number")
		}
		lo = clampIndex(int(i), length)
	}
	if x.HasHigh {
		hv, err := in.eval(x.Hi, env)
		if err != nil {
			return nil, err
		}
		i, ok := domain.AsInt(hv)
		if !ok {
			return nil, in.errf(x.P, "slice bound must be a number")
		}
		hi = clampIndex(int(i), length)
	} else {
		hi = length
	}
	if hi < lo {
		hi = lo
	}
	out := make([]domain.Value, hi-lo)
	copy(out, src[lo:hi])
	if _, isStr := recv.(domain.Str); isStr {
		var b strings.Builder
		for _, v := range out {
			b.WriteString(v.String())
		}
		return domain.Str(b.String()), nil
	}
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

func (in *Interp) evalProp(x *domain.PropExpr, env *scope) (domain.Value, error) {
	recv, err := in.eval(x.Left, env)
	if err != nil {
		return nil, err
	}
	if obj, ok := recv.(*domain.Obj); ok {
		if v, found := obj.Get(x.Name); found {
			return v, nil
		}
		// Unknown field reads yield null, so payload access never panics.
		return domain.Null{}, nil
	}
	if s, ok := recv.(domain.Str); ok {
		if v, found := stringProp(string(s), x.Name); found {
			return v, nil
		}
	}
	if errv, ok := recv.(*domain.ErrorValue); ok {
		if x.Name == "message" {
			return domain.Str(errv.Message), nil
		}
		if x.Name == "code" {
			return domain.Str(errv.Code), nil
		}
		if x.Name == "status" {
			return domain.Int(errv.Status), nil
		}
	}
	if a, ok := recv.(*domain.Arr); ok {
		if v, found := arrayProp(a, x.Name); found {
			return v, nil
		}
	}
	return nil, in.errf(x.P, "%s has no field '%s'", domain.TypeName(recv), x.Name)
}

func (in *Interp) evalCall(x *domain.CallExpr, env *scope) (domain.Value, error) {
	callee, err := in.eval(x.Callee, env)
	if err != nil {
		return nil, err
	}
	args, err := in.evalArgs(x.Args, env)
	if err != nil {
		return nil, err
	}
	return in.callValue(callee, args, x.P)
}

func (in *Interp) evalArgs(exprs []domain.Expr, env *scope) ([]domain.Value, error) {
	if len(exprs) == 0 {
		return nil, nil
	}
	out := make([]domain.Value, 0, len(exprs))
	for _, ex := range exprs {
		v, err := in.eval(ex, env)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// callValue invokes any callable value.
func (in *Interp) callValue(callee domain.Value, args []domain.Value, p domain.Position) (domain.Value, error) {
	switch c := callee.(type) {
	case *Fn:
		return in.callFn(c, args, p)
	case *domain.Builtin:
		if c.MinArgs > 0 && len(args) < c.MinArgs {
			return nil, in.errf(p, "%s() expects at least %d argument(s), got %d", c.Name, c.MinArgs, len(args))
		}
		return c.Fn(args, p)
	}
	return nil, in.errf(p, "%s is not callable", domain.TypeName(callee))
}

// Fn is a user-defined function or closure.
type Fn struct {
	Name   string
	Params []domain.Param
	Body   *domain.Block
	// Ret is the declared return type, if any.
	Ret *domain.TypeExpr
	// Elem is the element type of an `array<T>` return annotation.
	Elem *domain.TypeExpr
	Env  *scope
}

func (f *Fn) Type() domain.TypeTag { return domain.TypeFunc }
func (f *Fn) Truthy() bool         { return true }
func (f *Fn) String() string {
	if f.Name == "" {
		return "<function>"
	}
	return "<function " + f.Name + ">"
}

// callFn binds parameters and runs the body.
func (in *Interp) callFn(fn *Fn, args []domain.Value, p domain.Position) (domain.Value, error) {
	required := 0
	for _, prm := range fn.Params {
		if prm.Default == nil {
			required++
		}
	}
	if len(args) < required {
		return nil, in.errf(p, "%s() missing argument(s): needs %d, got %d", fn.display(), required, len(args))
	}
	if len(args) > len(fn.Params) {
		return nil, in.errf(p, "%s() takes %d argument(s), got %d", fn.display(), len(fn.Params), len(args))
	}

	in.depth++
	if in.depth > in.maxDepth {
		in.depth--
		return nil, in.errf(p, "maximum call depth exceeded (%d): recursion too deep?", in.maxDepth)
	}
	in.calls = append(in.calls, StackFrame{Func: fn.display(), File: in.File, Pos: p})

	calleeScope := newScope(fn.Env)
	for i, prm := range fn.Params {
		var v domain.Value
		if i < len(args) {
			v = args[i]
		} else {
			dv, err := in.eval(prm.Default, fn.Env)
			if err != nil {
				in.depth--
				in.calls = in.calls[:len(in.calls)-1]
				return nil, err
			}
			v = dv
		}
		if prm.Type != nil {
			if err := in.checkType(prm.Type, v, p); err != nil {
				in.depth--
				in.calls = in.calls[:len(in.calls)-1]
				return nil, err
			}
		}
		calleeScope.define(prm.Name, v, prm.Type)
	}

	ctrl, ret, err := in.execBlockStmts(fn.Body.Stmts, calleeScope)
	in.depth--
	in.calls = in.calls[:len(in.calls)-1]
	if err != nil {
		if re, ok := err.(*Error); ok {
			return nil, re.Frame(StackFrame{Func: fn.display(), File: in.File, Pos: p})
		}
		return nil, err
	}
	if ctrl == ctrlReturn {
		// Return annotations are checked but erased at runtime.
		if fn.Ret != nil {
			if err := in.checkType(fn.Ret, ret, p); err != nil {
				return nil, err
			}
			if fn.Ret.Name == "array" && fn.Elem != nil {
				if a, ok := ret.(*domain.Arr); ok {
					a.Elem = fn.Elem
				}
			}
		}
		return ret, nil
	}
	return domain.Null{}, nil
}

func (f *Fn) display() string {
	if f.Name == "" {
		return "<anonymous function>"
	}
	return f.Name
}

// evalMethod dispatches `recv.name(args)`.
func (in *Interp) evalMethod(x *domain.MethodExpr, env *scope) (domain.Value, error) {
	recv, err := in.eval(x.Receiver, env)
	if err != nil {
		return nil, err
	}
	// `evalMethod` uses `scope` as the parameter name; the signature above
	// keeps `env` for the rest of the file.
	scope := env

	extra, err := in.evalArgs(x.Args, scope)
	if err != nil {
		return nil, err
	}
	// Value methods receive the receiver as the first argument, matching the
	// signature their implementations use.
	withRecv := make([]domain.Value, 0, len(extra)+1)
	withRecv = append(withRecv, recv)
	withRecv = append(withRecv, extra...)

	// Modules and objects expose builtin functions as fields.
	if obj, ok := recv.(*domain.Obj); ok {
		if v, found := obj.Get(x.Name); found {
			switch v.(type) {
			case *domain.Builtin, *Fn:
				return in.callValue(v, extra, x.P)
			}
		}
		return nil, in.errf(x.P, "%s has no function '%s'", domain.TypeName(recv), x.Name)
	}
	if bi, ok := builtinMethod(recv, x.Name); ok {
		return bi(in, withRecv, x.P)
	}
	// `"abc".upper()` style: methods on builtin value types.
	if m, ok := valueMethod(recv, x.Name); ok {
		return m(in, withRecv, x.P)
	}
	return nil, in.errf(x.P, "%s has no method '%s'", domain.TypeName(recv), x.Name)
}

func (in *Interp) evalPrefix(x *domain.PrefixExpr, env *scope) (domain.Value, error) {
	v, err := in.eval(x.Right, env)
	if err != nil {
		return nil, err
	}
	switch x.Op {
	case domain.TokenMinus:
		switch n := v.(type) {
		case domain.Int:
			return domain.Int(-int64(n)), nil
		case domain.Float:
			return domain.Float(-float64(n)), nil
		}
		return nil, in.errf(x.P, "cannot negate %s", domain.TypeName(v))
	case domain.TokenNot:
		return domain.Bool(!domain.Truthy(v)), nil
	}
	return nil, in.errf(x.P, "unsupported unary operator")
}

// evalInfix evaluates binary operators and the `in` membership test.
func (in *Interp) evalInfix(x *domain.InfixExpr, env *scope) (domain.Value, error) {
	// Short-circuit evaluation for and/or.
	switch x.Op {
	case domain.TokenAnd:
		l, err := in.eval(x.Left, env)
		if err != nil {
			return nil, err
		}
		if !domain.Truthy(l) {
			return l, nil
		}
		return in.eval(x.Right, env)
	case domain.TokenOr:
		l, err := in.eval(x.Left, env)
		if err != nil {
			return nil, err
		}
		if domain.Truthy(l) {
			return l, nil
		}
		return in.eval(x.Right, env)
	}

	l, err := in.eval(x.Left, env)
	if err != nil {
		return nil, err
	}
	r, err := in.eval(x.Right, env)
	if err != nil {
		return nil, err
	}
	return in.binaryOp(l, x.Op, r, x.P)
}

// binaryOp applies a binary operator with no coercion.
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
	case domain.TokenIn:
		// `needle in haystack`: the left operand is the needle.
		found, err := in.membership(l, r, p)
		if err != nil {
			return nil, err
		}
		return domain.Bool(found), nil
	case domain.TokenLt, domain.TokenLtEq, domain.TokenGt, domain.TokenGtEq:
		return in.opCompare(l, op, r, p)
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
	// Array concatenation.
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
	// Integer arithmetic stays integral to avoid pointless float allocation.
	if li, ok := l.(domain.Int); ok {
		if ri, ok := r.(domain.Int); ok {
			return intArith(int64(li), op, int64(ri), func(pos domain.Position, format string, args ...interface{}) error {
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
	case domain.TokenPercent:
		if rf == 0 {
			return nil, in.errf(p, "modulo by zero")
		}
		return domain.Float(math.Mod(lf, rf)), nil
	}
	return nil, in.errf(p, "unsupported arithmetic operator %q", string(op))
}

// intValue boxes an integer, reusing the shared cache for small values so that
// arithmetic in loops does not allocate.
func intValue(i int64) domain.Value {
	if v, ok := domain.CacheInt(i); ok {
		return v
	}
	return domain.Int(i)
}

// intArith keeps integer arithmetic integral; the caller supplies the error
// helper so this stays free of interpreter state.
func intArith(a int64, op domain.TokenType, b int64, errf func(pos domain.Position, format string, args ...interface{}) error) (domain.Value, error) {
	switch op {
	case domain.TokenPlus:
		return intValue(a + b), nil
	case domain.TokenMinus:
		return intValue(a - b), nil
	case domain.TokenStar:
		return intValue(a * b), nil
	case domain.TokenSlash:
		if b == 0 {
			return nil, errf(domain.Position{}, "division by zero")
		}
		if a%b == 0 {
			return intValue(a / b), nil
		}
		return domain.Float(float64(a) / float64(b)), nil
	case domain.TokenPercent:
		if b == 0 {
			return nil, errf(domain.Position{}, "modulo by zero")
		}
		return intValue(a % b), nil
	}
	return nil, errf(domain.Position{}, "unsupported arithmetic operator %q", string(op))
}

// membership implements the `needle in haystack` operator.
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
	cmp, err := compareValues(l, r, p)
	if err != nil {
		return nil, err
	}
	switch op {
	case domain.TokenLt:
		return domain.Bool(cmp < 0), nil
	case domain.TokenLtEq:
		return domain.Bool(cmp <= 0), nil
	case domain.TokenGt:
		return domain.Bool(cmp > 0), nil
	case domain.TokenGtEq:
		return domain.Bool(cmp >= 0), nil
	}
	return nil, in.errf(p, "unsupported comparison")
}

// compareValues returns -1, 0 or 1 for orderable operands.
func compareValues(l, r domain.Value, p domain.Position) (int, error) {
	_ = p
	switch lv := l.(type) {
	case domain.Int:
		if rv, ok := r.(domain.Int); ok {
			return cmpInt(int64(lv), int64(rv)), nil
		}
		rf, ok := domain.AsFloat(r)
		if !ok {
			break
		}
		return cmpFloat(float64(lv), rf), nil
	case domain.Float:
		rf, ok := domain.AsFloat(r)
		if !ok {
			break
		}
		return cmpFloat(float64(lv), rf), nil
	case domain.Str:
		rs, ok := r.(domain.Str)
		if !ok {
			break
		}
		return strings.Compare(string(lv), string(rs)), nil
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
	return 0, &Error{Msg: "cannot compare " + domain.TypeName(l) + " with " + domain.TypeName(r), Pos: p}
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// valuesEqual compares values without coercion: "1" != 1.
func valuesEqual(a, b domain.Value) bool {
	if domain.TypeName(a) != domain.TypeName(b) {
		// int and float compare numerically.
		if af, ok := domain.AsFloat(a); ok {
			if bf, ok2 := domain.AsFloat(b); ok2 {
				if _, aIsStr := a.(domain.Str); !aIsStr {
					if _, bIsStr := b.(domain.Str); !bIsStr {
						if _, aIsBool := a.(domain.Bool); !aIsBool {
							if _, bIsBool := b.(domain.Bool); !bIsBool {
								return af == bf
							}
						}
					}
				}
			}
		}
		return false
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
		ak, bk := av.Keys(), bv.Keys()
		if len(ak) != len(bk) {
			return false
		}
		for _, k := range ak {
			av2, ok := av.Get(k)
			if !ok {
				return false
			}
			bv2, ok := bv.Get(k)
			if !ok {
				return false
			}
			if !valuesEqual(av2, bv2) {
				return false
			}
		}
		return true
	case *Fn:
		return av == b.(*Fn)
	case *domain.Builtin:
		return av == b.(*domain.Builtin)
	case *domain.ErrorValue:
		bv := b.(*domain.ErrorValue)
		return av.Message == bv.Message && av.Code == bv.Code
	}
	return false
}

// ---- builtin value properties ----

func stringProp(s, name string) (domain.Value, bool) {
	r := []rune(s)
	switch name {
	case "len":
		return domain.Int(len(r)), true
	case "upper":
		return domain.Str(strings.ToUpper(s)), true
	case "lower":
		return domain.Str(strings.ToLower(s)), true
	case "trim":
		return domain.Str(strings.TrimSpace(s)), true
	case "reversed":
		rev := make([]rune, len(r))
		for i, ru := range r {
			rev[len(r)-1-i] = ru
		}
		return domain.Str(string(rev)), true
	}
	return nil, false
}

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

var _ = utf8.RuneLen
