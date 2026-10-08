package interp

import (
	"errors"

	"garurda/internal/domain"
)

// This file implements a second, specialised compiler for functions whose
// every value is an integer.
//
// Compiling to closures removed node dispatch, but a call still went through
// boxed values: an argument slice, an interface per argument, a type check on
// the way out. For numeric code that is the whole cost, and it is why
// call-heavy recursion (the fib benchmark) stayed behind PHP.
//
// A function is eligible when its parameters and return type are integers and
// its body contains nothing but integer work: arithmetic, comparisons, integer
// locals, loops, and calls. Anything else makes the compiler give up and the
// function keeps using the general path. Eligibility is decided by trying, so
// there is no separate rule set to maintain.
//
// The specialised body talks to the interpreter only to reach globals and to
// call other functions, so it is a handful of instructions per call.

// errNotIntFn aborts specialised compilation.
var errNotIntFn = errors.New("not an int function")

// intCompFn is a compiled integer-only function.
type intCompFn struct {
	name    string
	nparams int
	// nints is the number of integer slots this body needs. It may exceed the
	// general compilation's count; the frame is sized for the larger of the two.
	nints int
	// retSlot is the frame slot that receives the return value.
	retSlot int
	body    intStmt
}

// intStmt is a compiled statement of an integer function.
type intStmt func(in *Interp, f *frame) (ctrl, error)

// intExpr is a compiled expression of an integer function.
type intExpr func(in *Interp, f *frame) (int64, error)

// iscope is a compile-time scope of the integer compiler. Every name is an
// integer: either a slot in the frame or an integer slot in the globals.
type iscope struct {
	parent  *iscope
	names   map[string]*islot
	nints   int
	fnScope bool
	// fn shares the layout counter with the enclosing function, so a nested
	// block does not restart numbering.
	counter *int
}

// islot is a resolved integer variable.
type islot struct {
	idx    int
	global bool
	// builtin marks a name that holds a builtin function at compile time. A
	// call to one of these is not integer work, so compilation gives up.
	builtin bool
	// callSite marks a name holding a plain value; calls through it are
	// resolved at run time.
	value bool
}

type intCompiler struct {
	in *Interp
	// retSlot is reserved before the body is compiled so `return` knows where
	// to put the value.
	retSlot int
}

// compileIntFn tries to build the integer version of a function. It returns
// nil when the function is not eligible.
func (c *intCompiler) compileIntFn(x *domain.FnExpr) *intCompFn {
	// Every parameter must be an annotated integer.
	if x.Ret == nil || x.Ret.Name != "int" || x.Ret.Nullable {
		return nil
	}
	for _, p := range x.Params {
		if p.Type == nil || p.Type.Name != "int" || p.Type.Nullable || p.Default != nil {
			return nil
		}
	}
	sc := &iscope{names: map[string]*islot{}}
	counter := 0
	sc.counter = &counter
	sc.fnScope = true
	// Globals are reachable from the body.
	for name, slot := range c.in.gscope.names {
		if slot.kind == slotInt {
			sc.names[name] = &islot{idx: slot.idx, global: true, value: true}
			continue
		}
		if v := c.in.globals.getVal(slot.idx); v != nil {
			if _, isBuiltin := v.(*domain.Builtin); isBuiltin {
				sc.names[name] = &islot{builtin: true}
				continue
			}
		}
		sc.names[name] = &islot{value: true}
	}

	fn := &intCompFn{name: x.Name, nparams: len(x.Params)}
	// Slot 0 is the return channel; parameters and locals follow.
	fn.retSlot = sc.add()
	c.retSlot = fn.retSlot
	for _, p := range x.Params {
		sc.names[p.Name] = &islot{idx: sc.add()}
	}
	body, err := c.block(x.Body, sc)
	if err != nil {
		return nil
	}
	fn.body = body
	// One extra slot holds the return value.
	fn.nints = counter
	return fn
}

// add reserves one integer slot.
func (s *iscope) add() int {
	idx := *s.counter
	*s.counter++
	return idx
}

func (s *iscope) child() *iscope {
	return &iscope{parent: s, names: map[string]*islot{}, counter: s.counter}
}

// lookup resolves a name outward.
func (s *iscope) lookup(name string) *islot {
	for sc := s; sc != nil; sc = sc.parent {
		if sl, ok := sc.names[name]; ok {
			return sl
		}
	}
	return nil
}

// ---- statements ----

func (c *intCompiler) block(b *domain.Block, sc *iscope) (intStmt, error) {
	if b == nil {
		return func(in *Interp, f *frame) (ctrl, error) { return ctrlNone, nil }, nil
	}
	fns := make([]intStmt, 0, len(b.Stmts))
	for _, st := range b.Stmts {
		fn, err := c.stmt(st, sc)
		if err != nil {
			return nil, err
		}
		fns = append(fns, fn)
	}
	switch len(fns) {
	case 0:
		return func(in *Interp, f *frame) (ctrl, error) { return ctrlNone, nil }, nil
	case 1:
		return fns[0], nil
	}
	return func(in *Interp, f *frame) (ctrl, error) {
		for _, fn := range fns {
			k, err := fn(in, f)
			if err != nil {
				return ctrlNone, err
			}
			if k != ctrlNone {
				return k, nil
			}
		}
		return ctrlNone, nil
	}, nil
}

func (c *intCompiler) stmt(st domain.Stmt, sc *iscope) (intStmt, error) {
	switch x := st.(type) {
	case *domain.Block:
		return c.block(x, sc.child())

	case *domain.VarDecl:
		return c.assign(x.Name, x.Value, sc)

	case *domain.AssignStmt:
		if x.Op != domain.TokenAssign {
			// Compound assignment on integers: compile as a read-modify-write.
			return c.compoundAssign(x, sc)
		}
		tgt, ok := x.Target.(*domain.Ident)
		if !ok {
			return nil, errNotIntFn
		}
		return c.assign(tgt.Name, x.Value, sc)

	case *domain.ReturnStmt:
		if x.Value == nil {
			return nil, errNotIntFn
		}
		ex, err := c.expr(x.Value, sc)
		if err != nil {
			return nil, err
		}
		return func(in *Interp, f *frame) (ctrl, error) {
			v, err := ex(in, f)
			if err != nil {
				return ctrlNone, err
			}
			f.ints[c.retSlot] = v
			return ctrlReturn, nil
		}, nil

	case *domain.IfStmt:
		cond, err := c.expr(x.Cond, sc)
		if err != nil {
			return nil, err
		}
		then, err := c.block(x.Then, sc.child())
		if err != nil {
			return nil, err
		}
		var els intStmt
		if x.Else != nil {
			els, err = c.stmt(x.Else, sc)
			if err != nil {
				return nil, err
			}
		}
		return func(in *Interp, f *frame) (ctrl, error) {
			cv, err := cond(in, f)
			if err != nil {
				return ctrlNone, err
			}
			if cv != 0 {
				return then(in, f)
			}
			if els != nil {
				return els(in, f)
			}
			return ctrlNone, nil
		}, nil

	case *domain.WhileStmt:
		cond, err := c.expr(x.Cond, sc)
		if err != nil {
			return nil, err
		}
		body, err := c.block(x.Body, sc.child())
		if err != nil {
			return nil, err
		}
		return func(in *Interp, f *frame) (ctrl, error) {
			for {
				cv, err := cond(in, f)
				if err != nil {
					return ctrlNone, err
				}
				if cv == 0 {
					return ctrlNone, nil
				}
				k, err := body(in, f)
				if err != nil {
					return ctrlNone, err
				}
				if k == ctrlBreak {
					return ctrlNone, nil
				}
			}
		}, nil

	case *domain.ForInStmt:
		if !x.Spec.IsRange {
			return nil, errNotIntFn
		}
		low, err := c.expr(x.Spec.Low, sc)
		if err != nil {
			return nil, err
		}
		high, err := c.expr(x.Spec.High, sc)
		if err != nil {
			return nil, err
		}
		body := sc.child()
		slotIdx := body.add()
		body.names[x.Var] = &islot{idx: slotIdx}
		loop, err := c.block(x.Body, body)
		if err != nil {
			return nil, err
		}
		var stepFn intExpr
		if x.Spec.Step != nil {
			if stepFn, err = c.expr(x.Spec.Step, sc); err != nil {
				return nil, err
			}
		}
		pos := x.P
		return func(in *Interp, f *frame) (ctrl, error) {
			lo, err := low(in, f)
			if err != nil {
				return ctrlNone, err
			}
			hi, err := high(in, f)
			if err != nil {
				return ctrlNone, err
			}
			step := int64(1)
			if lo > hi {
				step = -1
			}
			if stepFn != nil {
				sv, err := stepFn(in, f)
				if err != nil {
					return ctrlNone, err
				}
				if sv == 0 {
					return ctrlNone, in.errf(pos, "range step cannot be zero")
				}
				step = sv
			}
			for i := lo; ; i += step {
				if (step > 0 && i > hi) || (step < 0 && i < hi) {
					break
				}
				f.ints[slotIdx] = i
				f.isInt[slotIdx] = true
				k, err := loop(in, f)
				if err != nil {
					return ctrlNone, err
				}
				if k == ctrlBreak {
					return ctrlNone, nil
				}
			}
			return ctrlNone, nil
		}, nil

	case *domain.BreakStmt:
		return func(in *Interp, f *frame) (ctrl, error) { return ctrlBreak, nil }, nil

	case *domain.ContinueStmt:
		return func(in *Interp, f *frame) (ctrl, error) { return ctrlContinue, nil }, nil

	case *domain.ExprStmt:
		// Only a call may appear as a bare statement.
		call, ok := x.X.(*domain.CallExpr)
		if !ok {
			return nil, errNotIntFn
		}
		ex, err := c.expr(call, sc)
		if err != nil {
			return nil, err
		}
		return func(in *Interp, f *frame) (ctrl, error) {
			_, err := ex(in, f)
			return ctrlNone, err
		}, nil
	}
	return nil, errNotIntFn
}

// assign compiles `name = value` for an integer target.
func (c *intCompiler) assign(name string, value domain.Expr, sc *iscope) (intStmt, error) {
	slot := sc.lookup(name)
	if slot == nil || slot.builtin {
		// A new local, or a name that currently holds a function: not integer
		// work from the specialised compiler's point of view.
		if slot != nil {
			return nil, errNotIntFn
		}
		idx := sc.add()
		sc.names[name] = &islot{idx: idx}
		slot = sc.names[name]
	}
	if slot.value && !slot.global {
		// A plain-value global lives in the val index space; the unboxed
		// local slots cannot represent it, and writing idx 0 would clobber
		// the return channel. Give up and let the general path run.
		return nil, errNotIntFn
	}
	ex, err := c.expr(value, sc)
	if err != nil {
		return nil, err
	}
	slotIdx, global := slot.idx, slot.global
	return func(in *Interp, f *frame) (ctrl, error) {
		v, err := ex(in, f)
		if err != nil {
			return ctrlNone, err
		}
		if global {
			g := f.glob
			g.ints[slotIdx] = v
			g.isInt[slotIdx] = true
			return ctrlNone, nil
		}
		f.ints[slotIdx] = v
		f.isInt[slotIdx] = true
		return ctrlNone, nil
	}, nil
}

func (c *intCompiler) compoundAssign(x *domain.AssignStmt, sc *iscope) (intStmt, error) {
	tgt, ok := x.Target.(*domain.Ident)
	if !ok {
		return nil, errNotIntFn
	}
	slot := sc.lookup(tgt.Name)
	if slot == nil || slot.builtin {
		return nil, errNotIntFn
	}
	if slot.value && !slot.global {
		return nil, errNotIntFn
	}
	rhs, err := c.expr(x.Value, sc)
	if err != nil {
		return nil, err
	}
	slotIdx, global := slot.idx, slot.global
	op := x.Op
	pos := x.P
	return func(in *Interp, f *frame) (ctrl, error) {
		base := f
		if global {
			base = f.glob
			// The general path may have demoted this global after we
			// specialised; ints[idx] would then be a stale integer.
			if !base.isInt[slotIdx] {
				return ctrlNone, in.errf(pos, "variable '%s' is not an integer here", tgt.Name)
			}
		}
		cur := base.ints[slotIdx]
		v, err := rhs(in, f)
		if err != nil {
			return ctrlNone, err
		}
		var out int64
		switch op {
		case domain.TokenPlusEq:
			out = cur + v
		case domain.TokenMinusEq:
			out = cur - v
		case domain.TokenStarEq:
			out = cur * v
		case domain.TokenSlashEq:
			if v == 0 {
				return ctrlNone, in.errf(pos, "division by zero")
			}
			out = cur / v
		default:
			return ctrlNone, errNotIntFn
		}
		base.ints[slotIdx] = out
		base.isInt[slotIdx] = true
		return ctrlNone, nil
	}, nil
}

// ---- expressions ----

func (c *intCompiler) expr(e domain.Expr, sc *iscope) (intExpr, error) {
	switch x := e.(type) {
	case *domain.IntLit:
		v := x.Value
		return func(in *Interp, f *frame) (int64, error) { return v, nil }, nil

	case *domain.PrefixExpr:
		if x.Op != domain.TokenMinus {
			return nil, errNotIntFn
		}
		inner, err := c.expr(x.Right, sc)
		if err != nil {
			return nil, err
		}
		return func(in *Interp, f *frame) (int64, error) {
			v, err := inner(in, f)
			return -v, err
		}, nil

	case *domain.Ident:
		slot := sc.lookup(x.Name)
		if slot == nil || slot.builtin {
			return nil, errNotIntFn
		}
		if slot.value && !slot.global {
			// A plain-value global is boxed and lives in the val index space.
			// Falling through to the local reader would return f.ints[0] —
			// the return channel — as if it were the variable.
			return nil, errNotIntFn
		}
		idx, global := slot.idx, slot.global
		if global {
			// A global may also be written by the general path, so its flag
			// has to be checked.
			name := x.Name
			p0 := x.P
			return func(in *Interp, f *frame) (int64, error) {
				g := f.glob
				if !g.isInt[idx] {
					return 0, in.errf(p0, "variable '%s' is not an integer here", name)
				}
				return g.ints[idx], nil
			}, nil
		}
		// Locals and parameters only ever hold integers here, so the flag is
		// not consulted.
		return func(in *Interp, f *frame) (int64, error) {
			return f.ints[idx], nil
		}, nil

	case *domain.GroupExpr:
		return c.expr(x.Inner, sc)

	case *domain.TernaryExpr:
		cond, err := c.expr(x.Cond, sc)
		if err != nil {
			return nil, err
		}
		yes, err := c.expr(x.Conseq, sc)
		if err != nil {
			return nil, err
		}
		no, err := c.expr(x.Alt, sc)
		if err != nil {
			return nil, err
		}
		return func(in *Interp, f *frame) (int64, error) {
			cv, err := cond(in, f)
			if err != nil {
				return 0, err
			}
			if cv != 0 {
				return yes(in, f)
			}
			return no(in, f)
		}, nil

	case *domain.InfixExpr:
		return c.infix(x, sc)

	case *domain.CallExpr:
		return c.call(x, sc)
	}
	return nil, errNotIntFn
}

func (c *intCompiler) infix(x *domain.InfixExpr, sc *iscope) (intExpr, error) {
	left, err := c.expr(x.Left, sc)
	if err != nil {
		return nil, err
	}
	op := x.Op
	pos := x.P

	switch op {
	case domain.TokenLt, domain.TokenGt, domain.TokenLtEq, domain.TokenGtEq, domain.TokenEq, domain.TokenNeq:
		right, err := c.expr(x.Right, sc)
		if err != nil {
			return nil, err
		}
		return func(in *Interp, f *frame) (int64, error) {
			a, err := left(in, f)
			if err != nil {
				return 0, err
			}
			b, err := right(in, f)
			if err != nil {
				return 0, err
			}
			var res bool
			switch op {
			case domain.TokenLt:
				res = a < b
			case domain.TokenGt:
				res = a > b
			case domain.TokenLtEq:
				res = a <= b
			case domain.TokenGtEq:
				res = a >= b
			case domain.TokenEq:
				res = a == b
			default:
				res = a != b
			}
			if res {
				return 1, nil
			}
			return 0, nil
		}, nil
	}

	right, err := c.expr(x.Right, sc)
	if err != nil {
		return nil, err
	}
	return func(in *Interp, f *frame) (int64, error) {
		a, err := left(in, f)
		if err != nil {
			return 0, err
		}
		b, err := right(in, f)
		if err != nil {
			return 0, err
		}
		switch op {
		case domain.TokenPlus:
			return a + b, nil
		case domain.TokenMinus:
			return a - b, nil
		case domain.TokenStar:
			return a * b, nil
		case domain.TokenSlash:
			if b == 0 {
				return 0, in.errf(pos, "division by zero")
			}
			return a / b, nil
		case domain.TokenPercent:
			if b == 0 {
				return 0, in.errf(pos, "modulo by zero")
			}
			return a % b, nil
		case domain.TokenAnd:
			if a == 0 {
				return 0, nil
			}
			return boolInt(b != 0), nil
		case domain.TokenOr:
			if a != 0 {
				return 1, nil
			}
			return boolInt(b != 0), nil
		}
		return 0, errNotIntFn
	}, nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// call compiles a call to a function held in a variable. The callee is
// resolved at run time: if it has a specialised integer body the arguments
// never become boxed values, otherwise the general path runs.
func (c *intCompiler) call(x *domain.CallExpr, sc *iscope) (intExpr, error) {
	callee, ok := x.Callee.(*domain.Ident)
	if !ok {
		return nil, errNotIntFn
	}
	slot := sc.lookup(callee.Name)
	// A builtin is known to be a builtin: no integer work.
	if slot != nil && slot.builtin {
		return nil, errNotIntFn
	}
	// Only global callees are supported; a local closure cannot be an int
	// function because defining one would have aborted compilation.
	globalSlot, ok2 := c.in.gscope.names[callee.Name]
	if !ok2 {
		return nil, errNotIntFn
	}
	if globalSlot.kind != slotVal {
		// The callee is looked up with getVal, which uses the val index
		// space; an int or cell slot there would read an unrelated value.
		return nil, errNotIntFn
	}
	args := make([]intExpr, 0, len(x.Args))
	for _, a := range x.Args {
		ex, err := c.expr(a, sc)
		if err != nil {
			return nil, err
		}
		args = append(args, ex)
	}
	n := len(args)
	idx := globalSlot.idx
	pos := x.P

	return func(in *Interp, f *frame) (int64, error) {
		g := f.glob
		gv := g.getVal(idx)
		if _, ok := gv.(*closure); !ok {
			return 0, in.errf(pos, "'%s' is not a function", callee.Name)
		}
		// Evaluate every argument once, unboxed.
		if n == 1 {
			a0, err := args[0](in, f)
			if err != nil {
				return 0, err
			}
			return in.dispatchInt(gv, []int64{a0}, callee.Name, pos)
		}
		vals := make([]int64, n)
		for i, ex := range args {
			v, err := ex(in, f)
			if err != nil {
				return 0, err
			}
			vals[i] = v
		}
		return in.dispatchInt(gv, vals, callee.Name, pos)
	}, nil
}

// dispatchInt runs a callee reached from the unboxed calling convention,
// choosing the backend it actually has: bytecode, the integer fast path, or
// the general closures. The old code demanded cl.int and failed at runtime,
// which made a specialised function with a loop unable to call an ordinary
// function — a valid program — at all.
func (in *Interp) dispatchInt(gv domain.Value, vals []int64, name string, pos domain.Position) (int64, error) {
	cl := gv.(*closure)
	if cl.code != nil && cl.code.nparams == len(vals) {
		return in.callCode(cl.code, vals, pos)
	}
	if cl.int != nil && cl.int.nparams == len(vals) {
		return in.callInt(cl, vals, pos)
	}
	// General path: box the arguments and require an integer back.
	boxed := make([]domain.Value, len(vals))
	for i, v := range vals {
		boxed[i] = domain.Int(v)
	}
	res, err := in.callValue(gv, boxed, pos)
	if err != nil {
		return 0, err
	}
	iv, isInt := res.(domain.Int)
	if !isInt {
		return 0, in.errf(pos, "'%s' returned %s where an integer was required", name, domain.TypeName(res))
	}
	return int64(iv), nil
}

// callInt runs a specialised integer function.
func (in *Interp) callInt(c *closure, args []int64, pos domain.Position) (int64, error) {
	in.depth++
	if in.depth > in.maxDepth {
		in.depth--
		return 0, in.errf(pos, "maximum call depth exceeded (%d): recursion too deep?", in.maxDepth)
	}
	f := in.frames.get()
	// Size the frame for the integer body, which may need more integer slots
	// than the general compilation of the same function.
	n := c.fn.nints
	if cap(f.ints) < n {
		f.ints = make([]int64, n)
		f.isInt = make([]bool, n)
	}
	f.ints = f.ints[:n]
	f.isInt = f.isInt[:n]
	for i := range f.ints {
		f.ints[i] = 0
		f.isInt[i] = false
	}
	for i := range f.vals {
		f.vals[i] = nil
	}
	f.vals = f.vals[:0]
	f.dyn = nil
	f.def = c.def
	f.glob = c.def.glob
	f.refs = 0
	// Parameters occupy the slots right after the return slot, and they are
	// valid integers by construction.
	copy(f.ints[1:], args)
	for i := 0; i < len(args); i++ {
		f.isInt[1+i] = true
	}

	// The return value is parked in slot 0.
	k, err := c.int.body(in, f)
	v := f.ints[c.int.retSlot]
	f.release(&in.frames)
	in.depth--
	if err != nil {
		if re, ok := err.(*Error); ok {
			return 0, re.Frame(StackFrame{Func: displayName(c.int.name), File: in.File, Pos: pos})
		}
		return 0, err
	}
	if k != ctrlReturn {
		return 0, nil
	}
	return v, nil
}
