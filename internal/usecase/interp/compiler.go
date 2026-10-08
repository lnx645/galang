package interp

import (
	"strings"

	"garurda/internal/domain"
)

// The compiler walks the syntax tree once and produces closures. Scope
// resolution, slot allocation and int inference all happen here, which is why
// the running program needs no name lookups.

// stmtFn is a compiled statement.
type stmtFn func(f *frame) (ctrl, domain.Value, error)

// exprFn is a compiled expression.
type exprFn func(f *frame) (domain.Value, error)

// intFn is the unboxed integer fast path. ok reports whether the value was
// really an int, letting the caller fall back to the generic path.
type intFn func(f *frame) (int64, bool, error)

// cslot is a resolved variable slot.
type cslot struct {
	kind slotKind
	idx  int
	typ  *domain.TypeExpr
	// defIdx is the index in the defining frame of a captured cell.
	defIdx int
	// isInt records the compile-time inference used by the arithmetic fast path.
	isInt bool
	// global marks a slot that lives in the globals frame rather than the
	// caller's frame.
	global bool
}

// cscope is a compile-time scope.
type cscope struct {
	parent *cscope
	names  map[string]*cslot
	layout *frameLayout
	// fn marks the scope that starts a function body; crossing it turns a
	// variable into a capture.
	fn bool
	// isGlobal marks the top-level scope, whose slots live in globals.vals.
	isGlobal bool
	// intOnly lists names known to hold ints, used to specialise arithmetic.
	intOnly map[string]bool

	// forceCell makes every declaration in this scope a shared box, which is
	// what a body containing a nested function needs.
	forceCell bool

	// Return annotation of the function this scope belongs to.
	retType *domain.TypeExpr
	retElem *domain.TypeExpr
	// listElem is the element type of the enclosing `array<T>` annotation.
	listElem *domain.TypeExpr
}

func newCScope(parent *cscope, fn bool) *cscope {
	return &cscope{
		parent:  parent,
		names:   map[string]*cslot{},
		layout:  &frameLayout{},
		fn:      fn,
		intOnly: map[string]bool{},
	}
}

// compiler holds the state of one compilation.
type compiler struct {
	in  *Interp
	err error
	// captures accumulates cell bindings for the function being compiled.
	captures *[]capture
	// globalScope is the top-level scope, used when compiling nested functions.
	globalScope *cscope
	// depth guards against pathological nesting while compiling.
	depth int
}

// compileStmts compiles a statement list into one closure.
func (c *compiler) compileStmts(stmts []domain.Stmt, s *cscope) stmtFn {
	fns := make([]stmtFn, 0, len(stmts))
	for _, st := range stmts {
		if fn := c.compileStmt(st, s); fn != nil {
			fns = append(fns, fn)
		}
	}
	switch len(fns) {
	case 0:
		return func(f *frame) (ctrl, domain.Value, error) { return ctrlNone, nil, nil }
	case 1:
		return fns[0]
	}
	return func(f *frame) (ctrl, domain.Value, error) {
		var last domain.Value
		for _, fn := range fns {
			k, v, err := fn(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			if v != nil {
				last = v
			}
			if k != ctrlNone {
				return k, v, nil
			}
		}
		return ctrlNone, last, nil
	}
}

// ---- scope helpers ----

// resolve finds a name, walking outward across function boundaries by turning
// variables into captured cells.
func (c *compiler) resolve(s *cscope, name string) *cslot {
	for sc := s; sc != nil; sc = sc.parent {
		if slot, ok := sc.names[name]; ok {
			if sc == s || !c.crossesFunction(s, sc) {
				return slot
			}
			// The variable belongs to an enclosing function: capture it.
			return c.makeCapture(s, sc, name, slot)
		}
	}
	return nil
}

// crossesFunction reports whether sc is in an outer function relative to s.
func (c *compiler) crossesFunction(s, sc *cscope) bool {
	for x := s; x != nil && x != sc; x = x.parent {
		if x.fn {
			return true
		}
	}
	return false
}

// makeCapture converts a variable into a cell so an inner function can share
// it by reference, then allocates the local slot that will hold the cell.
func (c *compiler) makeCapture(s *cscope, def *cscope, name string, slot *cslot) *cslot {
	if def.isGlobal {
		// The globals frame is already shared by every function, so a global
		// needs no box; it is only marked so reads and writes go there.
		slot.global = true
		return slot
	}
	// Convert the defining slot into a cell slot once. The box itself is
	// created lazily by the first access.
	if slot.kind != slotCell {
		slot.kind = slotCell
	}
	local := s.layout.addVal()
	*c.captures = append(*c.captures, capture{localIdx: local, defIdx: slot.idx})
	local_ := &cslot{kind: slotCell, idx: local, defIdx: slot.idx, isInt: false}
	// Remember it so sibling statements in this scope reuse the same binding.
	s.names[name] = local_
	return local_
}

// storeSlot writes through a slot, routing global slots to the globals frame.
func storeSlot(f *frame, slot *cslot, name string, v domain.Value) {
	if slot == nil {
		return
	}
	if slot.global {
		f.glob.store(slot.kind, slot.idx, name, v)
		return
	}
	f.store(slot.kind, slot.idx, name, v)
}

// declare allocates a slot for a name in scope s.
func (c *compiler) declare(s *cscope, name string, t *domain.TypeExpr, isInt bool) *cslot {
	if slot, ok := s.names[name]; ok {
		return slot
	}
	slot := &cslot{typ: t, isInt: isInt, global: s.isGlobal}
	switch {
	case s.forceCell:
		slot.kind = slotCell
		slot.idx = s.layout.addVal()
	case isInt:
		// Globals get int slots too: a program-level counter is exactly the
		// case where unboxed arithmetic matters.
		slot.kind = slotInt
		slot.idx = s.layout.addInt()
	default:
		slot.kind = slotVal
		slot.idx = s.layout.addVal()
	}
	s.names[name] = slot
	return slot
}

// isIntExpr reports whether an expression is statically known to be an int.
// Used only to pick the unboxed fast path; the generic path stays correct.
func isIntExpr(e domain.Expr) bool {
	switch x := e.(type) {
	case *domain.IntLit:
		return true
	case *domain.Ident:
		return false // decided by the slot, handled by the caller
	case *domain.InfixExpr:
		switch x.Op {
		case domain.TokenPlus, domain.TokenMinus, domain.TokenStar, domain.TokenPercent:
			return isIntExpr(x.Left) && isIntExpr(x.Right)
		case domain.TokenSlash:
			// Division may widen to float.
			return false
		}
		return false
	case *domain.PrefixExpr:
		return x.Op == domain.TokenMinus && isIntExpr(x.Right)
	}
	return false
}

// ---- statement compilation ----

func (c *compiler) compileStmt(st domain.Stmt, s *cscope) stmtFn {
	switch x := st.(type) {
	case *domain.VarDecl:
		return c.compileVarDecl(x, s)
	case *domain.AssignStmt:
		return c.compileAssign(x, s)
	case *domain.ExprStmt:
		ex := c.compileExpr(x.X, s)
		return func(f *frame) (ctrl, domain.Value, error) {
			v, err := ex(f)
			return ctrlNone, v, err
		}
	case *domain.PrintStmt:
		return c.compilePrint(x, s)
	case *domain.ReturnStmt:
		return c.compileReturn(x, s)
	case *domain.IfStmt:
		return c.compileIf(x, s)
	case *domain.WhileStmt:
		return c.compileWhile(x, s)
	case *domain.ForInStmt:
		return c.compileForIn(x, s)
	case *domain.FnDecl:
		return c.compileFnDecl(x, s)
	case *domain.BreakStmt:
		return func(f *frame) (ctrl, domain.Value, error) { return ctrlBreak, nil, nil }
	case *domain.ContinueStmt:
		return func(f *frame) (ctrl, domain.Value, error) { return ctrlContinue, nil, nil }
	case *domain.ThrowStmt:
		return c.compileThrow(x, s)
	case *domain.TryStmt:
		return c.compileTry(x, s)
	case *domain.UseStmt:
		return c.compileUse(x)
	case *domain.Block:
		inner := c.scopeForBlock(s)
		body := c.compileStmts(x.Stmts, inner)
		return body
	}
	in := c.in
	pos := st.Pos()
	return func(f *frame) (ctrl, domain.Value, error) {
		return ctrlNone, nil, in.errf(pos, "unsupported statement %T", st)
	}
}

// scopeForBlock creates the lexical scope of a nested block.
func (c *compiler) scopeForBlock(parent *cscope) *cscope {
	ns := newCScope(parent, false)
	ns.layout = parent.layout
	ns.isGlobal = parent.isGlobal
	return ns
}

// fnScope creates the scope of a function body, with its own frame layout.
func (c *compiler) fnScope(parent *cscope, global *cscope) *cscope {
	ns := newCScope(parent, true)
	// Slots resolve outward through the global scope.
	ns.parent = global
	return ns
}

func (c *compiler) compileVarDecl(x *domain.VarDecl, s *cscope) stmtFn {
	in := c.in
	pos := x.P
	isInt := x.Type != nil && (x.Type.Name == "int" || x.Type.Name == "number")
	var val exprFn
	var ival intFn
	if x.Value != nil {
		val = c.compileExpr(x.Value, s)
		ival = c.compileIntExpr(x.Value, s)
		if !isInt && isIntExpr(x.Value) {
			isInt = true
		}
	}
	slot := c.declare(s, x.Name, x.Type, isInt)
	slotRef := slot
	// A declared type may turn an existing dynamic slot into a typed one.
	if x.Type != nil {
		slot.typ = x.Type
		if slot.kind == slotVal && !isInt {
			// keep as is
			_ = slot
		}
	}

	switch {
	case ival != nil && slot.kind == slotInt && x.Value != nil:
		name := x.Name
		typ := x.Type
		return func(f *frame) (ctrl, domain.Value, error) {
			n, ok, err := ival(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			if ok {
				if typ != nil {
					if err := in.checkType(typ, domain.Int(n), pos); err != nil {
						return ctrlNone, nil, err
					}
				}
				if slotRef.kind == slotInt {
					storeInt(f, slotRef, name, n)
				} else {
					storeSlot(f, slotRef, name, domain.Int(n))
				}
				return ctrlNone, nil, nil
			}
			v, err := val(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			if typ != nil {
				if err := in.checkType(typ, v, pos); err != nil {
					return ctrlNone, nil, err
				}
			}
			storeSlot(f, slotRef, name, v)
			return ctrlNone, v, nil
		}
	case x.Value != nil:
		return func(f *frame) (ctrl, domain.Value, error) {
			v, err := val(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			if typ := x.Type; typ != nil {
				if err := in.checkType(typ, v, pos); err != nil {
					return ctrlNone, nil, err
				}
			}
			storeSlot(f, slotRef, x.Name, v)
			return ctrlNone, nil, nil
		}
	case x.Type != nil:
		zero := zeroValue(x.Type)
		return func(f *frame) (ctrl, domain.Value, error) {
			storeSlot(f, slotRef, x.Name, zero)
			return ctrlNone, zero, nil
		}
	default:
		return func(f *frame) (ctrl, domain.Value, error) {
			storeSlot(f, slotRef, x.Name, domain.Null{})
			return ctrlNone, nil, nil
		}
	}
}

func (c *compiler) compileAssign(x *domain.AssignStmt, s *cscope) stmtFn {
	in := c.in
	pos := x.P

	// Compound assignment such as `$x += 1`.
	if x.Op != domain.TokenAssign {
		op := x.Op
		rhs := c.compileExpr(x.Value, s)
		switch tgt := x.Target.(type) {
		case *domain.Ident:
			slot := c.assignSlot(tgt.Name, s, pos, c.compileIntExpr(x.Value, s) != nil)
			return func(f *frame) (ctrl, domain.Value, error) {
				cur, err := in.readSlot(f, slot, tgt.Name, pos)
				if err != nil {
					return ctrlNone, nil, err
				}
				rv, err := rhs(f)
				if err != nil {
					return ctrlNone, nil, err
				}
				v, err := in.binaryOp(cur, op, rv, pos)
				if err != nil {
					return ctrlNone, nil, err
				}
				if slot.typ != nil {
					if err := in.checkType(slot.typ, v, pos); err != nil {
						return ctrlNone, nil, err
					}
				}
				storeSlot(f, slot, tgt.Name, v)
				return ctrlNone, v, nil
			}
		case *domain.IndexExpr:
			recv := c.compileExpr(tgt.Left, s)
			idx := c.compileExpr(tgt.Index, s)
			val := c.compileExpr(x.Value, s)
			return func(f *frame) (ctrl, domain.Value, error) {
				rv, err := recv(f)
				if err != nil {
					return ctrlNone, nil, err
				}
				iv, err := idx(f)
				if err != nil {
					return ctrlNone, nil, err
				}
				cur, err := in.index(rv, iv, pos)
				if err != nil {
					return ctrlNone, nil, err
				}
				nv, err := val(f)
				if err != nil {
					return ctrlNone, nil, err
				}
				combined, err := in.binaryOp(cur, op, nv, pos)
				if err != nil {
					return ctrlNone, nil, err
				}
				if err := in.setIndex(rv, iv, combined, pos); err != nil {
					return ctrlNone, nil, err
				}
				return ctrlNone, combined, nil
			}
		case *domain.PropExpr:
			recv := c.compileExpr(tgt.Left, s)
			val := c.compileExpr(x.Value, s)
			return func(f *frame) (ctrl, domain.Value, error) {
				rv, err := recv(f)
				if err != nil {
					return ctrlNone, nil, err
				}
				nv, err := val(f)
				if err != nil {
					return ctrlNone, nil, err
				}
				cur, err := in.prop(rv, tgt.Name, pos)
				if err != nil {
					return ctrlNone, nil, err
				}
				combined, err := in.binaryOp(cur, op, nv, pos)
				if err != nil {
					return ctrlNone, nil, err
				}
				if err := in.setProp(rv, tgt.Name, combined, pos); err != nil {
					return ctrlNone, nil, err
				}
				return ctrlNone, combined, nil
			}
		default:
			return func(f *frame) (ctrl, domain.Value, error) {
				return ctrlNone, nil, in.errf(pos, "invalid compound assignment target")
			}
		}
	}

	switch tgt := x.Target.(type) {
	case *domain.Ident:
		// Compile the source first: an int-typed right hand side lets a fresh
		// variable be given an unboxed int slot, which is what keeps loop
		// counters allocation free.
		rhs := c.compileExpr(x.Value, s)
		ival := c.compileIntExpr(x.Value, s)
		slot := c.assignSlot(tgt.Name, s, pos, ival != nil)
		if ival != nil {
			s.intOnly[tgt.Name] = true
		}
		kind, idx := slot.kind, slot.idx
		typ := slot.typ
		name := tgt.Name
		slotRef := slot
		if ival != nil && kind == slotInt {
			return func(f *frame) (ctrl, domain.Value, error) {
				n, ok, err := ival(f)
				if err != nil {
					return ctrlNone, nil, err
				}
				if !ok {
					// Not an int after all: take the generic route and demote
					// the slot.
					v, err := rhs(f)
					if err != nil {
						return ctrlNone, nil, err
					}
					if typ != nil {
						if err := in.checkType(typ, v, pos); err != nil {
							return ctrlNone, nil, err
						}
					}
					f.store(kind, idx, name, v)
					return ctrlNone, nil, nil
				}
				if typ != nil {
					if err := in.checkType(typ, domain.Int(n), pos); err != nil {
						return ctrlNone, nil, err
					}
				}
				// Unboxed store: no interface value is created. An assignment
				// yields no value, matching PHP, so nothing is boxed here.
				storeInt(f, slotRef, name, n)
				return ctrlNone, nil, nil
			}
		}
		return func(f *frame) (ctrl, domain.Value, error) {
			v, err := rhs(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			if typ != nil {
				if err := in.checkType(typ, v, pos); err != nil {
					return ctrlNone, nil, err
				}
			}
			storeSlot(f, slotRef, name, v)
			return ctrlNone, v, nil
		}

	case *domain.IndexExpr:
		recv := c.compileExpr(tgt.Left, s)
		idx := c.compileExpr(tgt.Index, s)
		val := c.compileExpr(x.Value, s)
		return func(f *frame) (ctrl, domain.Value, error) {
			rv, err := recv(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			iv, err := idx(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			v, err := val(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			if err := in.setIndex(rv, iv, v, pos); err != nil {
				return ctrlNone, nil, err
			}
			return ctrlNone, v, nil
		}

	case *domain.PropExpr:
		recv := c.compileExpr(tgt.Left, s)
		val := c.compileExpr(x.Value, s)
		return func(f *frame) (ctrl, domain.Value, error) {
			rv, err := recv(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			v, err := val(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			if err := in.setProp(rv, tgt.Name, v, pos); err != nil {
				return ctrlNone, nil, err
			}
			return ctrlNone, v, nil
		}
	}
	return func(f *frame) (ctrl, domain.Value, error) {
		return ctrlNone, nil, in.errf(pos, "invalid assignment target")
	}
}

// assignSlot resolves or creates the slot an assignment writes to. intSource
// says the right hand side is statically an int, which buys an unboxed slot.
func (c *compiler) assignSlot(name string, s *cscope, pos domain.Position, intSource bool) *cslot {
	if slot := c.resolve(s, name); slot != nil {
		return slot
	}
	// Assignment to a new name declares it. Inheriting intOnly lets a variable
	// declared in an inner block keep its int-ness.
	return c.declare(s, name, nil, s.intOnly[name] || intSource)
}

func (c *compiler) compilePrint(x *domain.PrintStmt, s *cscope) stmtFn {
	in := c.in
	vals := make([]exprFn, 0, len(x.Values))
	for _, v := range x.Values {
		vals = append(vals, c.compileExpr(v, s))
	}
	switch len(vals) {
	case 0:
		return func(f *frame) (ctrl, domain.Value, error) {
			return ctrlNone, nil, in.printLine("")
		}
	case 1:
		one := vals[0]
		return func(f *frame) (ctrl, domain.Value, error) {
			v, err := one(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			return ctrlNone, v, in.printLine(v.String())
		}
	}
	return func(f *frame) (ctrl, domain.Value, error) {
		parts := make([]string, 0, len(vals))
		for _, ev := range vals {
			v, err := ev(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			parts = append(parts, v.String())
		}
		return ctrlNone, nil, in.printLine(strings.Join(parts, " "))
	}
}

func (c *compiler) compileReturn(x *domain.ReturnStmt, s *cscope) stmtFn {
	in := c.in
	pos := x.P
	if x.Value == nil {
		return func(f *frame) (ctrl, domain.Value, error) {
			return ctrlReturn, domain.Null{}, nil
		}
	}
	val := c.compileExpr(x.Value, s)
	retType := s.retType
	return func(f *frame) (ctrl, domain.Value, error) {
		v, err := val(f)
		if err != nil {
			return ctrlNone, nil, err
		}
		if retType != nil {
			if err := in.checkType(retType, v, pos); err != nil {
				return ctrlNone, nil, err
			}
			if retType.Name == "array" && s.retElem != nil {
				if a, ok := v.(*domain.Arr); ok {
					a.Elem = s.retElem
				}
			}
		}
		return ctrlReturn, v, nil
	}
}

func (c *compiler) compileIf(x *domain.IfStmt, s *cscope) stmtFn {
	cond := c.compileExpr(x.Cond, s)
	thenScope := c.scopeForBlock(s)
	then := c.compileStmts(x.Then.Stmts, thenScope)
	var els stmtFn
	if x.Else != nil {
		els = c.compileStmt(x.Else, s)
	}
	return func(f *frame) (ctrl, domain.Value, error) {
		cv, err := cond(f)
		if err != nil {
			return ctrlNone, nil, err
		}
		if domain.Truthy(cv) {
			return then(f)
		}
		if els != nil {
			return els(f)
		}
		return ctrlNone, nil, nil
	}
}

func (c *compiler) compileWhile(x *domain.WhileStmt, s *cscope) stmtFn {
	cond := c.compileExpr(x.Cond, s)
	bodyScope := c.scopeForBlock(s)
	body := c.compileStmts(x.Body.Stmts, bodyScope)
	return func(f *frame) (ctrl, domain.Value, error) {
		for {
			cv, err := cond(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			if !domain.Truthy(cv) {
				return ctrlNone, nil, nil
			}
			k, _, err := body(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			if k == ctrlBreak {
				return ctrlNone, nil, nil
			}
		}
	}
}

func (c *compiler) compileForIn(x *domain.ForInStmt, s *cscope) stmtFn {
	in := c.in
	pos := x.P
	bodyScope := c.scopeForBlock(s)
	// The loop variable is an int slot when it comes from a range.
	isIntLoop := x.Spec.IsRange
	slot := c.declare(bodyScope, x.Var, nil, isIntLoop)
	var slot2 *cslot
	if x.Var2 != "" {
		slot2 = c.declare(bodyScope, x.Var2, nil, false)
	}
	body := c.compileStmts(x.Body.Stmts, bodyScope)
	name := x.Var
	slotRef := slot

	if x.Spec.IsRange {
		low := c.compileExpr(x.Spec.Low, s)
		high := c.compileExpr(x.Spec.High, s)
		var stepFn exprFn
		if x.Spec.Step != nil {
			stepFn = c.compileExpr(x.Spec.Step, s)
		}
		return func(f *frame) (ctrl, domain.Value, error) {
			lv, err := low(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			hv, err := high(f)
			if err != nil {
				return ctrlNone, nil, err
			}
			start, ok1 := domain.AsInt(lv)
			end, ok2 := domain.AsInt(hv)
			if !ok1 || !ok2 {
				return ctrlNone, nil, in.errf(pos, "range bounds must be numbers")
			}
			step := int64(1)
			if start > end {
				step = -1
			}
			if stepFn != nil {
				sv, err := stepFn(f)
				if err != nil {
					return ctrlNone, nil, err
				}
				si, ok := domain.AsInt(sv)
				if !ok {
					return ctrlNone, nil, in.errf(pos, "range step must be a number")
				}
				if si == 0 {
					return ctrlNone, nil, in.errf(pos, "range step cannot be zero")
				}
				step = si
			}
			for i := start; ; i += step {
				if (step > 0 && i > end) || (step < 0 && i < end) {
					break
				}
				if slotRef.kind == slotInt {
					storeInt(f, slotRef, name, i)
				} else {
					storeSlot(f, slotRef, name, domain.Int(i))
				}
				k, _, err := body(f)
				if err != nil {
					return ctrlNone, nil, err
				}
				if k == ctrlBreak {
					return ctrlNone, nil, nil
				}
			}
			return ctrlNone, nil, nil
		}
	}

	src := c.compileExpr(x.Spec.Src, s)
	return func(f *frame) (ctrl, domain.Value, error) {
		sv, err := src(f)
		if err != nil {
			return ctrlNone, nil, err
		}
		bind := func(v domain.Value) { storeSlot(f, slotRef, name, v) }
		switch coll := sv.(type) {
		case *domain.Arr:
			for i, item := range coll.Items {
				bind(item)
				if slot2 != nil {
					storeSlot(f, slot2, x.Var2, domain.Int(i))
				}
				k, _, err := body(f)
				if err != nil {
					return ctrlNone, nil, err
				}
				if k == ctrlBreak {
					return ctrlNone, nil, nil
				}
			}
		case *domain.Obj:
			for _, kk := range coll.Keys() {
				v, _ := coll.Get(kk)
				bind(domain.Str(kk))
				if slot2 != nil {
					storeSlot(f, slot2, x.Var2, v)
				}
				k, _, err := body(f)
				if err != nil {
					return ctrlNone, nil, err
				}
				if k == ctrlBreak {
					return ctrlNone, nil, nil
				}
			}
		case domain.Str:
			for _, r := range string(coll) {
				bind(domain.Str(string(r)))
				k, _, err := body(f)
				if err != nil {
					return ctrlNone, nil, err
				}
				if k == ctrlBreak {
					return ctrlNone, nil, nil
				}
			}
		default:
			return ctrlNone, nil, in.errf(pos, "cannot iterate over %s", domain.TypeName(sv))
		}
		return ctrlNone, nil, nil
	}
}

func (c *compiler) compileThrow(x *domain.ThrowStmt, s *cscope) stmtFn {
	in := c.in
	pos := x.P
	val := c.compileExpr(x.Value, s)
	return func(f *frame) (ctrl, domain.Value, error) {
		v, err := val(f)
		if err != nil {
			return ctrlNone, nil, err
		}
		switch t := v.(type) {
		case *domain.ErrorValue:
			return ctrlNone, nil, in.wrapThrown(t, pos)
		case domain.Str:
			return ctrlNone, nil, in.Throw(string(t), "error", 500, pos)
		}
		return ctrlNone, nil, in.errf(pos, "throw expects a string or error, got %s", domain.TypeName(v))
	}
}

func (c *compiler) compileTry(x *domain.TryStmt, s *cscope) stmtFn {
	body := c.compileStmts(x.Body.Stmts, c.scopeForBlock(s))

	var final stmtFn
	if x.Finally != nil {
		final = c.compileStmts(x.Finally.Stmts, c.scopeForBlock(s))
	}
	runFinally := func(f *frame) error {
		if final == nil {
			return nil
		}
		_, _, ferr := final(f)
		return ferr
	}

	if x.Catch == nil {
		if final == nil {
			return func(f *frame) (ctrl, domain.Value, error) { return body(f) }
		}
		return func(f *frame) (ctrl, domain.Value, error) {
			k, v, err := body(f)
			if ferr := runFinally(f); ferr != nil {
				return ctrlNone, nil, ferr
			}
			return k, v, err
		}
	}

	catchScope := c.scopeForBlock(s)
	var catchErrSlot *cslot
	if x.HasVar {
		catchErrSlot = c.declare(catchScope, x.Var, nil, false)
	} else {
		catchErrSlot = c.declare(catchScope, "error", nil, false)
	}
	handler := c.compileStmts(x.Catch.Stmts, catchScope)
	name := "error"
	if x.HasVar {
		name = x.Var
	}
	return func(f *frame) (ctrl, domain.Value, error) {
		k, v, err := body(f)
		if err == nil {
			if ferr := runFinally(f); ferr != nil {
				return ctrlNone, nil, ferr
			}
			return k, v, nil
		}
		re, ok := err.(*Error)
		if !ok || !re.Thrown() {
			// Interpreter bugs are never swallowed by user code,
			// but finally still runs for cleanup.
			if ferr := runFinally(f); ferr != nil {
				return ctrlNone, nil, ferr
			}
			return ctrlNone, nil, err
		}
		storeSlot(f, catchErrSlot, name, re.Value)
		ck, cv, cerr := handler(f)
		if ferr := runFinally(f); ferr != nil {
			return ctrlNone, nil, ferr
		}
		return ck, cv, cerr
	}
}

func (c *compiler) compileUse(x *domain.UseStmt) stmtFn {
	in := c.in
	path := x.Path
	pos := x.P
	// Modul bawaan menang lebih dulu; selain itu dianggap path file
	// relatif yang mengikat namespace sesuai nama berkasnya.
	bind := path
	_, builtin := in.Modules[path]
	if !builtin {
		bind = fileModuleName(path)
	}
	slot := c.declare(c.globalScope, bind, nil, false)
	slot.global = true
	return func(f *frame) (ctrl, domain.Value, error) {
		if builtin {
			storeSlot(f, slot, bind, in.Modules[path]())
			return ctrlNone, nil, nil
		}
		v, err := in.loadFileModule(path, pos)
		if err != nil {
			return ctrlNone, nil, err
		}
		storeSlot(f, slot, bind, v)
		return ctrlNone, nil, nil
	}
}

func (c *compiler) compileFnDecl(x *domain.FnDecl, s *cscope) stmtFn {
	// The name is declared before the body is compiled so a recursive call
	// resolves to this very function.
	slot := c.declare(s, x.Fn.Name, &domain.TypeExpr{Name: "function", Nullable: true}, false)
	cl, _ := c.compileFnExpr(x.Fn, s)
	name := x.Fn.Name
	return func(f *frame) (ctrl, domain.Value, error) {
		storeSlot(f, slot, name, cl(f))
		return ctrlNone, nil, nil
	}
}

// compileFnExpr compiles a function expression into a closure factory plus its
// compiled body.
func (c *compiler) compileFnExpr(x *domain.FnExpr, s *cscope) (func(f *frame) domain.Value, *compFn) {
	// The body chains to the enclosing scope so resolve() can reach the
	// variables a closure shares with its parent.
	body := newCScope(s, true)
	body.retType = x.Ret
	body.retElem = elemOf(x.Ret)

	caps := make([]capture, 0, 2)
	savedCaptures := c.captures
	c.captures = &caps
	c.depth++
	defer func() {
		c.depth--
		c.captures = savedCaptures
	}()

	// A body that defines a nested function needs every one of its locals to
	// be shareable, so those slots become boxes up front.
	body.forceCell = hasFnLiteral(x.Body.Stmts)

	// c.in.File names the file being compiled right now (Run and
	// loadFileModule both set it before compiling).
	fn := &compFn{name: x.Name, retType: x.Ret, elem: elemOf(x.Ret), async: x.Async, file: c.in.File, pos: x.P}
	for _, p := range x.Params {
		isInt := p.Type != nil && (p.Type.Name == "int" || p.Type.Name == "number")
		// Parameters join the function scope so the body resolves them.
		ps := paramSlot{typ: p.Type, kind: slotVal}
		if isInt {
			ps.kind = slotInt
			ps.idx = body.layout.addInt()
		} else {
			ps.idx = body.layout.addVal()
		}
		if body.forceCell {
			ps.kind = slotCell
			ps.idx = body.layout.addVal()
		}
		body.names[p.Name] = &cslot{kind: ps.kind, idx: ps.idx, typ: p.Type, isInt: isInt}
		if p.Default != nil {
			ps.defaultFn = c.compileExpr(p.Default, body)
		} else {
			fn.minArgs++
		}
		fn.params = append(fn.params, ps)
	}
	fn.body = c.compileStmts(x.Body.Stmts, body)
	fn.nints = body.layout.nints
	fn.nvals = body.layout.nvals
	fn.captures = caps

	// Specialisation is attempted twice, best first: bytecode for a flat
	// interpreter loop, then the closure form. Failing both is not an error —
	// the function simply keeps the general path.
	// Two unboxed backends, each better at something. Bytecode wins on
	// call-heavy code because a call is a jump instead of a chain of
	// closures; the closure backend wins on loops because a loop becomes a
	// native Go loop. A function with a loop therefore keeps closures.
	// File modules are exempt from both: the backends address globals
	// through the single shared in.globals frame, while a module owns its
	// own frame and its names were compiled against that frame's layout.
	// The general path resolves globals through the frame chain, which is
	// correct for any frame.
	var specialised *intCompFn
	var code *intCode
	if c.globalScope == c.in.gscope {
		bcc := &bcCompiler{in: c.in}
		bc := bcc.compileIntCode(x)
		if bc != nil && !bcc.hasLoop {
			code = bc
			if bc.nlocals > fn.nints {
				fn.nints = bc.nlocals
			}
		} else {
			specialised = (&intCompiler{in: c.in}).compileIntFn(x)
			if specialised != nil && specialised.nints > fn.nints {
				fn.nints = specialised.nints
			}
		}
	}

	factory := func(f *frame) domain.Value {
		// The defining frame must stay alive as long as the closure exists.
		f.refs++
		return &closure{fn: fn, def: f, int: specialised, code: code}
	}
	return factory, fn
}

// hasFnLiteral reports whether the statements contain a nested function
// literal, which forces the enclosing scope to use shareable boxes.
func hasFnLiteral(stmts []domain.Stmt) bool {
	found := false
	var walkStmts func(list []domain.Stmt)
	var walkExpr func(e domain.Expr)
	walkExpr = func(e domain.Expr) {
		if e == nil || found {
			return
		}
		switch x := e.(type) {
		case *domain.FnExpr:
			found = true
		case *domain.GroupExpr:
			walkExpr(x.Inner)
		case *domain.InfixExpr:
			walkExpr(x.Left)
			walkExpr(x.Right)
		case *domain.PrefixExpr:
			walkExpr(x.Right)
		case *domain.TernaryExpr:
			walkExpr(x.Cond)
			walkExpr(x.Conseq)
			walkExpr(x.Alt)
		case *domain.CallExpr:
			walkExpr(x.Callee)
			for _, a := range x.Args {
				walkExpr(a)
			}
		case *domain.MethodExpr:
			walkExpr(x.Receiver)
			for _, a := range x.Args {
				walkExpr(a)
			}
		case *domain.IndexExpr:
			walkExpr(x.Left)
			walkExpr(x.Index)
		case *domain.SliceExpr:
			walkExpr(x.Left)
			walkExpr(x.Low)
			walkExpr(x.Hi)
		case *domain.PropExpr:
			walkExpr(x.Left)
		case *domain.RangeExpr:
			walkExpr(x.Low)
			walkExpr(x.High)
			walkExpr(x.Step)
		case *domain.StrLit:
			for _, part := range x.Parts {
				if part.IsExpr {
					walkExpr(part.Expr)
				}
			}
		case *domain.ListLit:
			for _, el := range x.Elems {
				walkExpr(el)
			}
			walkExpr(x.CompSrc)
			walkExpr(x.CompElem)
			walkExpr(x.CompCond)
		case *domain.ObjectLit:
			for _, p := range x.Pairs {
				walkExpr(p.Value)
			}
		}
	}
	walkStmts = func(list []domain.Stmt) {
		for _, st := range list {
			if found {
				return
			}
			switch x := st.(type) {
			case *domain.VarDecl:
				walkExpr(x.Value)
			case *domain.AssignStmt:
				walkExpr(x.Target)
				walkExpr(x.Value)
			case *domain.ExprStmt:
				walkExpr(x.X)
			case *domain.PrintStmt:
				for _, v := range x.Values {
					walkExpr(v)
				}
			case *domain.ReturnStmt:
				walkExpr(x.Value)
			case *domain.IfStmt:
				walkExpr(x.Cond)
				walkStmts(x.Then.Stmts)
				if x.Else != nil {
					walkStmts([]domain.Stmt{x.Else})
				}
			case *domain.WhileStmt:
				walkExpr(x.Cond)
				walkStmts(x.Body.Stmts)
			case *domain.ForInStmt:
				walkExpr(x.Spec.Low)
				walkExpr(x.Spec.High)
				walkExpr(x.Spec.Step)
				walkExpr(x.Spec.Src)
				walkStmts(x.Body.Stmts)
			case *domain.FnDecl:
				found = true
			case *domain.ThrowStmt:
				walkExpr(x.Value)
			case *domain.TryStmt:
				walkStmts(x.Body.Stmts)
				if x.Catch != nil {
					walkStmts(x.Catch.Stmts)
				}
				if x.Finally != nil {
					walkStmts(x.Finally.Stmts)
				}
			case *domain.Block:
				walkStmts(x.Stmts)
			}
		}
	}
	walkStmts(stmts)
	return found
}
