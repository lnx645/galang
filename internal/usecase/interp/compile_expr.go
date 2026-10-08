package interp

import (
	"strings"

	"garurda/internal/domain"
)

// compileExpr turns an expression into a closure.
func (c *compiler) compileExpr(e domain.Expr, s *cscope) exprFn {
	switch x := e.(type) {
	case *domain.IntLit:
		v := domain.Int(x.Value)
		if cv, ok := domain.CacheInt(x.Value); ok {
			return func(f *frame) (domain.Value, error) { return cv, nil }
		}
		return func(f *frame) (domain.Value, error) { return v, nil }

	case *domain.FloatLit:
		v := domain.Float(x.Value)
		return func(f *frame) (domain.Value, error) { return v, nil }

	case *domain.StrLit:
		if len(x.Parts) == 1 && !x.Parts[0].IsExpr {
			v := domain.Str(x.Parts[0].Lit)
			return func(f *frame) (domain.Value, error) { return v, nil }
		}
		return c.compileString(x, s)

	case *domain.BoolLit:
		v := domain.Bool(x.Value)
		return func(f *frame) (domain.Value, error) { return v, nil }

	case *domain.NullLit:
		return func(f *frame) (domain.Value, error) { return domain.Null{}, nil }

	case *domain.Ident:
		return c.compileIdent(x, s)

	case *domain.ListLit:
		return c.compileList(x, s)

	case *domain.ObjectLit:
		return c.compileObject(x, s)

	case *domain.GroupExpr:
		return c.compileExpr(x.Inner, s)

	case *domain.FnExpr:
		factory, _ := c.compileFnExpr(x, s)
		return func(f *frame) (domain.Value, error) { return factory(f), nil }

	case *domain.IndexExpr:
		left := c.compileExpr(x.Left, s)
		idx := c.compileExpr(x.Index, s)
		pos := x.P
		return func(f *frame) (domain.Value, error) {
			lv, err := left(f)
			if err != nil {
				return nil, err
			}
			iv, err := idx(f)
			if err != nil {
				return nil, err
			}
			return c.in.index(lv, iv, pos)
		}

	case *domain.SliceExpr:
		return c.compileSlice(x, s)

	case *domain.PropExpr:
		recv := c.compileExpr(x.Left, s)
		return func(f *frame) (domain.Value, error) {
			rv, err := recv(f)
			if err != nil {
				return nil, err
			}
			return c.in.prop(rv, x.Name, x.P)
		}

	case *domain.CallExpr:
		return c.compileCall(x, s)

	case *domain.MethodExpr:
		return c.compileMethod(x, s)

	case *domain.PrefixExpr:
		return c.compilePrefix(x, s)

	case *domain.InfixExpr:
		return c.compileInfix(x, s)

	case *domain.TernaryExpr:
		cond := c.compileExpr(x.Cond, s)
		yes := c.compileExpr(x.Conseq, s)
		no := c.compileExpr(x.Alt, s)
		return func(f *frame) (domain.Value, error) {
			cv, err := cond(f)
			if err != nil {
				return nil, err
			}
			if domain.Truthy(cv) {
				return yes(f)
			}
			return no(f)
		}

	case *domain.RangeExpr:
		return c.compileRange(x, s)
	}
	in := c.in
	pos := e.Pos()
	return func(f *frame) (domain.Value, error) {
		return nil, in.errf(pos, "unsupported expression %T", e)
	}
}

// compileIdent compiles a variable read.
func (c *compiler) compileIdent(x *domain.Ident, s *cscope) exprFn {
	in := c.in
	pos := x.P
	slot := c.resolve(s, x.Name)
	if slot == nil {
		return func(f *frame) (domain.Value, error) {
			return nil, in.errf(pos, "undefined variable '%s'", x.Name)
		}
	}
	kind, idx := slot.kind, slot.idx
	name := x.Name
	isGlobal := s.isGlobal || slot.global

	// One closure per slot kind. A single closure with a switch inside cost a
	// branch on every variable read, which is the most frequent operation in
	// a program.
	switch {
	case kind == slotInt && isGlobal:
		return func(f *frame) (domain.Value, error) {
			g := f.glob
			if g.isInt[idx] {
				return domain.Int(g.ints[idx]), nil
			}
			if v := g.getIval(idx); v != nil {
				return v, nil
			}
			return nil, in.errf(pos, "undefined variable '%s'", name)
		}
	case kind == slotInt:
		return func(f *frame) (domain.Value, error) {
			if f.isInt[idx] {
				return domain.Int(f.ints[idx]), nil
			}
			if v := f.getIval(idx); v != nil {
				return v, nil
			}
			return nil, in.errf(pos, "undefined variable '%s'", name)
		}
	case kind == slotCell && isGlobal:
		return func(f *frame) (domain.Value, error) {
			g := f.glob
			if cl := g.cellAt(idx); cl != nil && cl.v != nil {
				return cl.v, nil
			}
			if v := g.getVal(idx); v != nil {
				return v, nil
			}
			return nil, in.errf(pos, "undefined variable '%s'", name)
		}
	case kind == slotCell:
		defIdx := slot.defIdx
		return func(f *frame) (domain.Value, error) {
			if cl := f.cellAt(idx); cl != nil && cl.v != nil {
				return cl.v, nil
			}
			if f.def != nil && defIdx < len(f.def.vals) {
				if cl, _ := f.def.vals[defIdx].(*cell); cl != nil && cl.v != nil {
					return cl.v, nil
				}
			}
			return nil, in.errf(pos, "undefined variable '%s'", name)
		}
	case kind == slotVal && isGlobal:
		return func(f *frame) (domain.Value, error) {
			if v := f.glob.getVal(idx); v != nil {
				return v, nil
			}
			return nil, in.errf(pos, "undefined variable '%s'", name)
		}
	case kind == slotVal:
		return func(f *frame) (domain.Value, error) {
			if v := f.getVal(idx); v != nil {
				return v, nil
			}
			return nil, in.errf(pos, "undefined variable '%s'", name)
		}
	default:
		return func(f *frame) (domain.Value, error) {
			if v, ok := f.dyn[name]; ok {
				return v, nil
			}
			if v, ok := f.glob.dyn[name]; ok {
				return v, nil
			}
			return nil, in.errf(pos, "undefined variable '%s'", name)
		}
	}
}

// compileIntExpr produces the unboxed fast path for an expression, or nil when
// the expression cannot be proven to be an int.
func (c *compiler) compileIntExpr(e domain.Expr, s *cscope) intFn {
	switch x := e.(type) {
	case *domain.IntLit:
		v := x.Value
		return func(f *frame) (int64, bool, error) { return v, true, nil }

	case *domain.Ident:
		slot := c.resolve(s, x.Name)
		if slot == nil || slot.kind != slotInt {
			return nil
		}
		idx := slot.idx
		if s.isGlobal || slot.global {
			return func(f *frame) (int64, bool, error) {
				if f.glob.isInt[idx] {
					return f.glob.ints[idx], true, nil
				}
				if v := f.glob.getIval(idx); v != nil {
					if n, isInt := v.(domain.Int); isInt {
						return int64(n), true, nil
					}
				}
				return 0, false, nil
			}
		}
		return func(f *frame) (int64, bool, error) {
			if f.isInt[idx] {
				return f.ints[idx], true, nil
			}
			if v := f.getIval(idx); v != nil {
				if n, isInt := v.(domain.Int); isInt {
					return int64(n), true, nil
				}
			}
			return 0, false, nil
		}

	case *domain.InfixExpr:
		switch x.Op {
		case domain.TokenPlus, domain.TokenMinus, domain.TokenStar, domain.TokenPercent, domain.TokenSlash:
		default:
			return nil
		}
		left := c.compileIntExpr(x.Left, s)
		if left == nil {
			return nil
		}
		right := c.compileIntExpr(x.Right, s)
		if right == nil {
			return nil
		}
		op := x.Op
		in := c.in
		pos := x.P
		if op == domain.TokenSlash {
			// Division can widen to float, so it keeps a guard.
			return func(f *frame) (int64, bool, error) {
				a, ok1, err := left(f)
				if err != nil || !ok1 {
					return 0, false, err
				}
				b, ok2, err := right(f)
				if err != nil || !ok2 {
					return 0, false, err
				}
				if b == 0 {
					return 0, false, in.errf(pos, "division by zero")
				}
				if a%b != 0 {
					return 0, false, nil
				}
				return a / b, true, nil
			}
		}
		return func(f *frame) (int64, bool, error) {
			a, ok1, err := left(f)
			if err != nil || !ok1 {
				return 0, false, err
			}
			b, ok2, err := right(f)
			if err != nil || !ok2 {
				return 0, false, err
			}
			switch op {
			case domain.TokenPlus:
				return a + b, true, nil
			case domain.TokenMinus:
				return a - b, true, nil
			case domain.TokenStar:
				return a * b, true, nil
			default: // percent
				if b == 0 {
					return 0, false, in.errf(pos, "modulo by zero")
				}
				return a % b, true, nil
			}
		}

	case *domain.PrefixExpr:
		if x.Op != domain.TokenMinus {
			return nil
		}
		inner := c.compileIntExpr(x.Right, s)
		if inner == nil {
			return nil
		}
		return func(f *frame) (int64, bool, error) {
			v, ok, err := inner(f)
			if err != nil || !ok {
				return 0, false, err
			}
			return -v, true, nil
		}
	}
	return nil
}

func (c *compiler) compileString(x *domain.StrLit, s *cscope) exprFn {
	in := c.in
	// A string with a single interpolation of one identifier is the common
	// case in templates and logging; build it without extra allocations.
	parts := x.Parts
	if len(parts) == 3 && parts[1].IsExpr && !parts[0].IsExpr && !parts[2].IsExpr {
		if id, ok := parts[1].Expr.(*domain.Ident); ok {
			prefix, suffix := parts[0].Lit, parts[2].Lit
			slot := c.resolve(s, id.Name)
			if slot != nil {
				kind, idx := slot.kind, slot.idx
				isGlobal := s.isGlobal || slot.global
				return func(f *frame) (domain.Value, error) {
					g := f.glob
					var v domain.Value
					switch kind {
					case slotInt:
						if isGlobal {
							if g.isInt[idx] {
								v = domain.Int(g.ints[idx])
							} else {
								v = g.getIval(idx)
							}
						} else if f.isInt[idx] {
							v = domain.Int(f.ints[idx])
						} else {
							v = f.getIval(idx)
						}
					case slotCell:
						if isGlobal {
							if cl := g.cellAt(idx); cl != nil {
								v = cl.v
							}
						} else if cl := f.cellAt(idx); cl != nil {
							v = cl.v
						}
					case slotVal:
						if isGlobal {
							v = g.getVal(idx)
						} else {
							v = f.getVal(idx)
						}
					default:
						v = f.dyn[id.Name]
					}
					if v == nil {
						return nil, in.errf(x.P, "undefined variable '%s'", id.Name)
					}
					return domain.Str(prefix + v.String() + suffix), nil
				}
			}
		}
	}
	evs := make([]exprFn, 0, len(parts))
	for _, p := range parts {
		if p.IsExpr {
			evs = append(evs, c.compileExpr(p.Expr, s))
			continue
		}
		lit := domain.Str(p.Lit)
		evs = append(evs, func(f *frame) (domain.Value, error) { return lit, nil })
	}
	return func(f *frame) (domain.Value, error) {
		var b strings.Builder
		for _, ev := range evs {
			v, err := ev(f)
			if err != nil {
				return nil, err
			}
			b.WriteString(v.String())
		}
		return domain.Str(b.String()), nil
	}
}

func (c *compiler) compileList(x *domain.ListLit, s *cscope) exprFn {
	in := c.in
	if x.CompSrc != nil {
		return c.compileComprehension(x, s)
	}
	elems := make([]exprFn, 0, len(x.Elems))
	hasRange := false
	for _, el := range x.Elems {
		if _, ok := el.(*domain.RangeExpr); ok {
			hasRange = true
		}
		elems = append(elems, c.compileExpr(el, s))
	}
	elemType := s.listElem
	return func(f *frame) (domain.Value, error) {
		out := &domain.Arr{Elem: elemType}
		out.Items = make([]domain.Value, 0, len(elems))
		for _, ev := range elems {
			v, err := ev(f)
			if err != nil {
				return nil, err
			}
			if rng, ok := v.(*domain.Arr); ok && hasRange {
				out.Items = append(out.Items, rng.Items...)
				continue
			}
			if elemType != nil {
				if err := in.checkType(elemType, v, domain.Position{}); err != nil {
					return nil, err
				}
			}
			out.Items = append(out.Items, v)
		}
		return out, nil
	}
}

func (c *compiler) compileComprehension(x *domain.ListLit, s *cscope) exprFn {
	in := c.in
	inner := c.scopeForBlock(s)
	slot := c.declare(inner, x.CompVar, nil, false)
	var slot2 *cslot
	if x.CompIdx != "" {
		slot2 = c.declare(inner, x.CompIdx, nil, false)
	}
	src := c.compileExpr(x.CompSrc, s)
	var cond exprFn
	if x.CompCond != nil {
		cond = c.compileExpr(x.CompCond, inner)
	}
	elem := c.compileExpr(x.CompElem, inner)
	kind, idx, name := slot.kind, slot.idx, x.CompVar
	var kind2 slotKind
	var idx2 int
	var name2 string
	if slot2 != nil {
		kind2, idx2, name2 = slot2.kind, slot2.idx, x.CompIdx
	}
	return func(f *frame) (domain.Value, error) {
		sv, err := src(f)
		if err != nil {
			return nil, err
		}
		out := &domain.Arr{}
		emit := func() error {
			if cond != nil {
				cv, err := cond(f)
				if err != nil {
					return err
				}
				if !domain.Truthy(cv) {
					return nil
				}
			}
			v, err := elem(f)
			if err != nil {
				return err
			}
			out.Append(v)
			return nil
		}
		switch coll := sv.(type) {
		case *domain.Arr:
			for i, item := range coll.Items {
				f.store(kind, idx, name, item)
				if slot2 != nil {
					f.store(kind2, idx2, name2, domain.Int(i))
				}
				if err := emit(); err != nil {
					return nil, err
				}
			}
		case *domain.Obj:
			for _, kk := range coll.Keys() {
				v, _ := coll.Get(kk)
				f.store(kind, idx, name, domain.Str(kk))
				if slot2 != nil {
					f.store(kind2, idx2, name2, v)
				}
				if err := emit(); err != nil {
					return nil, err
				}
			}
		case domain.Str:
			for _, r := range string(coll) {
				f.store(kind, idx, name, domain.Str(string(r)))
				if err := emit(); err != nil {
					return nil, err
				}
			}
		default:
			return nil, in.errf(x.P, "cannot iterate over %s in comprehension", domain.TypeName(sv))
		}
		return out, nil
	}
}

func (c *compiler) compileObject(x *domain.ObjectLit, s *cscope) exprFn {
	type pair struct {
		key string
		val exprFn
	}
	pairs := make([]pair, 0, len(x.Pairs))
	for _, p := range x.Pairs {
		pairs = append(pairs, pair{key: p.Key, val: c.compileExpr(p.Value, s)})
	}
	return func(f *frame) (domain.Value, error) {
		obj := domain.NewObj()
		for _, p := range pairs {
			v, err := p.val(f)
			if err != nil {
				return nil, err
			}
			obj.Set(p.key, v)
		}
		return obj, nil
	}
}

func (c *compiler) compileSlice(x *domain.SliceExpr, s *cscope) exprFn {
	in := c.in
	recv := c.compileExpr(x.Left, s)
	var low, high exprFn
	if x.Low != nil {
		low = c.compileExpr(x.Low, s)
	}
	if x.Hi != nil {
		high = c.compileExpr(x.Hi, s)
	}
	pos := x.P
	return func(f *frame) (domain.Value, error) {
		rv, err := recv(f)
		if err != nil {
			return nil, err
		}
		return in.slice(rv, low, high, pos, f)
	}
}

func (c *compiler) compileRange(x *domain.RangeExpr, s *cscope) exprFn {
	in := c.in
	low := c.compileExpr(x.Low, s)
	high := c.compileExpr(x.High, s)
	var step exprFn
	if x.Step != nil {
		step = c.compileExpr(x.Step, s)
	}
	pos := x.P
	return func(f *frame) (domain.Value, error) {
		lv, err := low(f)
		if err != nil {
			return nil, err
		}
		hv, err := high(f)
		if err != nil {
			return nil, err
		}
		start, ok1 := domain.AsInt(lv)
		end, ok2 := domain.AsInt(hv)
		if !ok1 || !ok2 {
			return nil, in.errf(pos, "range bounds must be numbers")
		}
		st := int64(1)
		if start > end {
			st = -1
		}
		if step != nil {
			sv, err := step(f)
			if err != nil {
				return nil, err
			}
			si, ok := domain.AsInt(sv)
			if !ok {
				return nil, in.errf(pos, "range step must be a number")
			}
			if si == 0 {
				return nil, in.errf(pos, "range step cannot be zero")
			}
			st = si
		}
		n := 0
		if st > 0 && end >= start {
			n = int((end-start)/st) + 1
		} else if st < 0 && end <= start {
			n = int((start-end)/(-st)) + 1
		}
		out := &domain.Arr{Items: make([]domain.Value, 0, n)}
		for i := start; ; i += st {
			if (st > 0 && i > end) || (st < 0 && i < end) {
				break
			}
			out.Items = append(out.Items, domain.Int(i))
		}
		return out, nil
	}
}

func (c *compiler) compilePrefix(x *domain.PrefixExpr, s *cscope) exprFn {
	in := c.in
	// The unboxed negation of an int slot.
	if x.Op == domain.TokenMinus {
		if inner := c.compileIntExpr(x.Right, s); inner != nil {
			return func(f *frame) (domain.Value, error) {
				v, ok, err := inner(f)
				if err != nil {
					return nil, err
				}
				if ok {
					return domain.Int(-v), nil
				}
				rv, err := c.compileExpr(x.Right, s)(f)
				if err != nil {
					return nil, err
				}
				switch n := rv.(type) {
				case domain.Int:
					return domain.Int(-int64(n)), nil
				case domain.Float:
					return domain.Float(-float64(n)), nil
				}
				return nil, in.errf(x.P, "cannot negate %s", domain.TypeName(rv))
			}
		}
	}
	val := c.compileExpr(x.Right, s)
	if x.Op == domain.TokenAwait {
		return func(f *frame) (domain.Value, error) {
			v, err := val(f)
			if err != nil {
				return nil, err
			}
			return in.awaitValue(v, x.P)
		}
	}
	if x.Op == domain.TokenNot {
		return func(f *frame) (domain.Value, error) {
			v, err := val(f)
			if err != nil {
				return nil, err
			}
			return domain.Bool(!domain.Truthy(v)), nil
		}
	}
	return func(f *frame) (domain.Value, error) {
		v, err := val(f)
		if err != nil {
			return nil, err
		}
		switch n := v.(type) {
		case domain.Int:
			return domain.Int(-int64(n)), nil
		case domain.Float:
			return domain.Float(-float64(n)), nil
		}
		return nil, in.errf(x.P, "cannot negate %s", domain.TypeName(v))
	}
}

func (c *compiler) compileInfix(x *domain.InfixExpr, s *cscope) exprFn {
	in := c.in
	pos := x.P

	// Short circuit first: it changes the evaluation order.
	switch x.Op {
	case domain.TokenAnd:
		left := c.compileExpr(x.Left, s)
		right := c.compileExpr(x.Right, s)
		return func(f *frame) (domain.Value, error) {
			lv, err := left(f)
			if err != nil {
				return nil, err
			}
			if !domain.Truthy(lv) {
				return lv, nil
			}
			return right(f)
		}
	case domain.TokenOr:
		left := c.compileExpr(x.Left, s)
		right := c.compileExpr(x.Right, s)
		return func(f *frame) (domain.Value, error) {
			lv, err := left(f)
			if err != nil {
				return nil, err
			}
			if domain.Truthy(lv) {
				return lv, nil
			}
			return right(f)
		}
	}

	// Integer fast path: both operands known to be ints.
	switch x.Op {
	case domain.TokenPlus, domain.TokenMinus, domain.TokenStar, domain.TokenPercent, domain.TokenSlash:
		if li := c.compileIntExpr(x.Left, s); li != nil {
			if ri := c.compileIntExpr(x.Right, s); ri != nil {
				op := x.Op
				fast := intArithFn(in, op, li, ri, pos)
				generic := func(f *frame) (domain.Value, error) {
					lv, err := c.compileExpr(x.Left, s)(f)
					if err != nil {
						return nil, err
					}
					rv, err := c.compileExpr(x.Right, s)(f)
					if err != nil {
						return nil, err
					}
					return in.binaryOp(lv, op, rv, pos)
				}
				return func(f *frame) (domain.Value, error) {
					n, ok, err := fast(f)
					if err != nil {
						return nil, err
					}
					if ok {
						return domain.Int(n), nil
					}
					return generic(f)
				}
			}
		}
	}

	left := c.compileExpr(x.Left, s)
	right := c.compileExpr(x.Right, s)
	op := x.Op

	// Comparisons and equality get a specialised closure too, because they are
	// the hottest operations in loops.
	switch op {
	case domain.TokenLt, domain.TokenGt, domain.TokenLtEq, domain.TokenGtEq:
		if li := c.compileIntExpr(x.Left, s); li != nil {
			if ri := c.compileIntExpr(x.Right, s); ri != nil {
				return func(f *frame) (domain.Value, error) {
					a, ok1, err := li(f)
					if err != nil {
						return nil, err
					}
					b, ok2, err := ri(f)
					if err != nil {
						return nil, err
					}
					if ok1 && ok2 {
						switch op {
						case domain.TokenLt:
							return domain.Bool(a < b), nil
						case domain.TokenGt:
							return domain.Bool(a > b), nil
						case domain.TokenLtEq:
							return domain.Bool(a <= b), nil
						default:
							return domain.Bool(a >= b), nil
						}
					}
					lv, err := left(f)
					if err != nil {
						return nil, err
					}
					rv, err := right(f)
					if err != nil {
						return nil, err
					}
					return in.binaryOp(lv, op, rv, pos)
				}
			}
		}
	}

	return func(f *frame) (domain.Value, error) {
		lv, err := left(f)
		if err != nil {
			return nil, err
		}
		rv, err := right(f)
		if err != nil {
			return nil, err
		}
		return in.binaryOp(lv, op, rv, pos)
	}
}

// intArithFn builds the unboxed integer operator.
func intArithFn(in *Interp, op domain.TokenType, l, r intFn, pos domain.Position) intFn {
	return func(f *frame) (int64, bool, error) {
		a, ok1, err := l(f)
		if err != nil || !ok1 {
			return 0, false, err
		}
		b, ok2, err := r(f)
		if err != nil || !ok2 {
			return 0, false, err
		}
		switch op {
		case domain.TokenPlus:
			return a + b, true, nil
		case domain.TokenMinus:
			return a - b, true, nil
		case domain.TokenStar:
			return a * b, true, nil
		case domain.TokenPercent:
			if b == 0 {
				return 0, false, in.errf(pos, "modulo by zero")
			}
			return a % b, true, nil
		default: // slash
			if b == 0 {
				return 0, false, in.errf(pos, "division by zero")
			}
			if a%b != 0 {
				return 0, false, nil
			}
			return a / b, true, nil
		}
	}
}

func (c *compiler) compileCall(x *domain.CallExpr, s *cscope) exprFn {
	in := c.in
	callee := c.compileExpr(x.Callee, s)
	args := make([]exprFn, 0, len(x.Args))
	for _, a := range x.Args {
		args = append(args, c.compileExpr(a, s))
	}
	pos := x.P
	switch len(args) {
	case 0:
		return func(f *frame) (domain.Value, error) {
			cv, err := callee(f)
			if err != nil {
				return nil, err
			}
			return in.callValue(cv, nil, pos)
		}
	case 1:
		a0 := args[0]
		return func(f *frame) (domain.Value, error) {
			cv, err := callee(f)
			if err != nil {
				return nil, err
			}
			v0, err := a0(f)
			if err != nil {
				return nil, err
			}
			// One argument goes on the shared stack: no slice allocation.
			base := len(in.argStack)
			in.argStack = append(in.argStack, v0)
			res, err := in.callValue(cv, in.argStack[base:], pos)
			in.argStack = in.argStack[:base]
			return res, err
		}
	case 2:
		a0, a1 := args[0], args[1]
		return func(f *frame) (domain.Value, error) {
			cv, err := callee(f)
			if err != nil {
				return nil, err
			}
			v0, err := a0(f)
			if err != nil {
				return nil, err
			}
			v1, err := a1(f)
			if err != nil {
				return nil, err
			}
			base := len(in.argStack)
			in.argStack = append(in.argStack, v0, v1)
			res, err := in.callValue(cv, in.argStack[base:], pos)
			in.argStack = in.argStack[:base]
			return res, err
		}
	}
	return func(f *frame) (domain.Value, error) {
		cv, err := callee(f)
		if err != nil {
			return nil, err
		}
		base := len(in.argStack)
		for _, ev := range args {
			v, err := ev(f)
			if err != nil {
				in.argStack = in.argStack[:base]
				return nil, err
			}
			in.argStack = append(in.argStack, v)
		}
		res, err := in.callValue(cv, in.argStack[base:], pos)
		in.argStack = in.argStack[:base]
		return res, err
	}
}

func (c *compiler) compileMethod(x *domain.MethodExpr, s *cscope) exprFn {
	in := c.in
	recv := c.compileExpr(x.Receiver, s)
	args := make([]exprFn, 0, len(x.Args))
	for _, a := range x.Args {
		args = append(args, c.compileExpr(a, s))
	}
	pos := x.P
	name := x.Name
	return func(f *frame) (domain.Value, error) {
		rv, err := recv(f)
		if err != nil {
			return nil, err
		}
		list := make([]domain.Value, 0, len(args)+1)
		list = append(list, rv)
		for _, ev := range args {
			v, err := ev(f)
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
		return in.callMethodOn(rv, name, list, pos)
	}
}
