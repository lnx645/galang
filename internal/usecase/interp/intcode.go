package interp

import (
	"garurda/internal/domain"
)

// The bytecode compiler for integer functions.
//
// It shares the eligibility rules of the closure-based integer compiler in
// intfn.go: only parameters and return values that are annotated `int`, and a
// body made of integer work. When anything else shows up, compilation gives up
// and the function keeps the general closure path.
//
// The output is plain data, which is what makes an opcode cache possible.

// codeBuilder accumulates instructions and records jump targets to patch.
type codeBuilder struct {
	code []instr
	// breaks and continues collect the jump instructions emitted inside the
	// loop currently being compiled; the loop patches them when its own
	// targets are known.
	breaks    []int
	continues []int
}

// emit appends an instruction and returns its index.
func (b *codeBuilder) emit(i instr) int {
	b.code = append(b.code, i)
	return len(b.code) - 1
}

// jump emits a jump with a placeholder target and returns its index.
func (b *codeBuilder) jump(op opCode) int {
	return b.emit(instr{op: op, a: -1})
}

// here returns the address of the next instruction.
func (b *codeBuilder) here() int { return len(b.code) }

// patch points a jump at the current address.
func (b *codeBuilder) patch(idx int) {
	b.code[idx].a = int64(b.here())
}

// bcScope is a compile-time scope of the bytecode compiler.
type bcScope struct {
	parent  *bcScope
	names   map[string]*bcSlot
	counter *int
	nlocals *int
}

// bcSlot is a resolved variable: either a local slot or a global integer slot.
type bcSlot struct {
	idx    int
	global bool
	// builtin marks a name holding a builtin function, which is not integer work.
	builtin bool
	// value marks a plain value that is not an integer slot.
	value bool
}

type bcCompiler struct {
	in    *Interp
	owner *codeBuilder
	// fnName is used only for diagnostics.
	fnName string
	// why records why compilation gave up, for diagnostics and tests.
	why string
	// hasLoop records a loop in the body. The closure backend turns a loop
	// into a native Go loop, which beats a bytecode loop whose condition costs
	// several instructions, so a looping function keeps the closure backend.
	hasLoop bool
}

// compileIntCode tries to build bytecode for an integer-only function.
func (c *bcCompiler) compileIntCode(x *domain.FnExpr) *intCode {
	if x.Ret == nil || x.Ret.Name != "int" || x.Ret.Nullable {
		return nil
	}
	for _, p := range x.Params {
		if p.Type == nil || p.Type.Name != "int" || p.Type.Nullable || p.Default != nil {
			return nil
		}
	}

	nlocals := 0
	sc := &bcScope{names: map[string]*bcSlot{}, counter: new(int), nlocals: &nlocals}
	// Globals are reachable by name.
	for name, slot := range c.in.gscope.names {
		if slot.kind == slotInt {
			sc.names[name] = &bcSlot{idx: slot.idx, global: true, value: true}
			continue
		}
		if v := c.in.globals.getVal(slot.idx); v != nil {
			if _, isBuiltin := v.(*domain.Builtin); isBuiltin {
				sc.names[name] = &bcSlot{builtin: true}
				continue
			}
		}
		sc.names[name] = &bcSlot{value: true}
	}

	code := &intCode{name: x.Name, nparams: len(x.Params)}
	c.owner = &codeBuilder{}
	c.fnName = x.Name

	// Slot 0 receives the return value.
	sc.add()
	for _, p := range x.Params {
		sc.names[p.Name] = &bcSlot{idx: sc.add()}
	}
	if !c.block(x.Body, sc) {
		return nil
	}
	c.emit(instr{op: opReturn})
	code.code = c.owner.code
	code.nlocals = nlocals
	code.callees = make([]*closure, len(code.code))
	return code
}

func (s *bcScope) add() int {
	idx := *s.counter
	*s.counter++
	*s.nlocals++
	return idx
}

func (s *bcScope) child() *bcScope {
	return &bcScope{parent: s, names: map[string]*bcSlot{}, counter: s.counter, nlocals: s.nlocals}
}

func (s *bcScope) lookup(name string) *bcSlot {
	for sc := s; sc != nil; sc = sc.parent {
		if sl, ok := sc.names[name]; ok {
			return sl
		}
	}
	return nil
}

func (c *bcCompiler) emit(i instr) int { return c.owner.emit(i) }

// beginLoop records where the body starts so breaks and continues emitted
// inside it can be patched later.
func (c *bcCompiler) beginLoop() (breaksAt, continuesAt int) {
	return len(c.owner.breaks), len(c.owner.continues)
}

// patchLoop fills in every break and continue of the body just compiled.
func (c *bcCompiler) patchLoop(breaksAt, continuesAt, brkTarget, contTarget int) {
	for _, idx := range c.owner.breaks[breaksAt:] {
		c.owner.code[idx].a = int64(brkTarget)
	}
	for _, idx := range c.owner.continues[continuesAt:] {
		c.owner.code[idx].a = int64(contTarget)
	}
	c.owner.breaks = c.owner.breaks[:breaksAt]
	c.owner.continues = c.owner.continues[:continuesAt]
}

// block compiles statements and reports success.
func (c *bcCompiler) block(b *domain.Block, sc *bcScope) bool {
	if b == nil {
		return true
	}
	for _, st := range b.Stmts {
		if !c.stmt(st, sc) {
			return false
		}
	}
	return true
}

func (c *bcCompiler) stmt(st domain.Stmt, sc *bcScope) bool {
	switch x := st.(type) {
	case *domain.Block:
		return c.block(x, sc.child())

	case *domain.VarDecl:
		return c.assign(x.Name, x.Value, sc)

	case *domain.AssignStmt:
		tgt, ok := x.Target.(*domain.Ident)
		if !ok {
			c.why = "assignment target is not a name"
			return false
		}
		if x.Op == domain.TokenAssign {
			return c.assign(tgt.Name, x.Value, sc)
		}
		slot := sc.lookup(tgt.Name)
		if slot == nil || slot.builtin {
			c.why = "compound assignment target is not an integer local"
			return false
		}
		if slot.value && !slot.global {
			c.why = "compound assignment target is a plain-value global"
			return false
		}
		// The left operand goes first: the VM computes `a op b` with a on top
		// minus one, so `$x -= 3` must push $x before 3.
		if slot.global {
			c.emit(instr{op: opLoadGlobal, a: int64(slot.idx)})
		} else {
			c.emit(instr{op: opLoadLocal, a: int64(slot.idx)})
		}
		if !c.expr(x.Value, sc) {
			return false
		}
		op := opAdd
		switch x.Op {
		case domain.TokenPlusEq:
			op = opAdd
		case domain.TokenMinusEq:
			op = opSub
		case domain.TokenStarEq:
			op = opMul
		case domain.TokenSlashEq:
			op = opDiv
		default:
			return false
		}
		c.emit(instr{op: op})
		c.store(slot)
		return true

	case *domain.ReturnStmt:
		if x.Value == nil || !c.expr(x.Value, sc) {
			return false
		}
		c.emit(instr{op: opStoreLocal, a: 0})
		c.emit(instr{op: opReturn})
		return true

	case *domain.IfStmt:
		if !c.expr(x.Cond, sc) {
			return false
		}
		jf := c.emit(instr{op: opJumpIfFalse, a: -1})
		if !c.block(x.Then, sc.child()) {
			return false
		}
		if x.Else == nil {
			c.owner.patch(jf)
			return true
		}
		jend := c.emit(instr{op: opJump, a: -1})
		c.owner.patch(jf)
		if !c.stmt(x.Else, sc) {
			return false
		}
		c.owner.patch(jend)
		return true

	case *domain.WhileStmt:
		c.hasLoop = true
		// The condition is part of the loop head: the backward jump has to
		// re-evaluate it.
		start := c.owner.here()
		if !c.expr(x.Cond, sc) {
			return false
		}
		jf := c.emit(instr{op: opJumpIfFalse, a: -1})
		breaksAt, continuesAt := c.beginLoop()
		if !c.block(x.Body, sc.child()) {
			return false
		}
		contTarget := c.owner.here()
		c.emit(instr{op: opJump, a: int64(start)})
		c.owner.patch(jf)
		c.patchLoop(breaksAt, continuesAt, c.owner.here(), contTarget)
		return true

	case *domain.ForInStmt:
		if !x.Spec.IsRange {
			return false
		}
		c.hasLoop = true
		curSlot := sc.add()
		limitSlot := sc.add()
		stepSlot := sc.add()
		loopSlot := sc.add()

		if !c.expr(x.Spec.Low, sc) {
			return false
		}
		c.emit(instr{op: opStoreLocal, a: int64(curSlot)})
		if !c.expr(x.Spec.High, sc) {
			return false
		}
		c.emit(instr{op: opStoreLocal, a: int64(limitSlot)})

		if x.Spec.Step != nil {
			if !c.expr(x.Spec.Step, sc) {
				return false
			}
		} else {
			// The default step follows the direction of the range:
			// 1 - 2*(cur > limit), which is 1 ascending or -1 descending.
			c.emit(instr{op: opConst, a: 1})
			c.emit(instr{op: opLoadLocal, a: int64(curSlot)})
			c.emit(instr{op: opLoadLocal, a: int64(limitSlot)})
			c.emit(instr{op: opGt})
			c.emit(instr{op: opConst, a: 2})
			c.emit(instr{op: opMul})
			c.emit(instr{op: opSub})
		}
		c.emit(instr{op: opStoreLocal, a: int64(stepSlot)})

		body := sc.child()
		body.names[x.Var] = &bcSlot{idx: loopSlot}

		start := c.owner.here()
		// (step > 0 && cur <= limit) || (step < 0 && cur >= limit)
		c.emit(instr{op: opLoadLocal, a: int64(stepSlot)})
		c.emit(instr{op: opConst, a: 0})
		c.emit(instr{op: opGt})
		c.emit(instr{op: opLoadLocal, a: int64(curSlot)})
		c.emit(instr{op: opLoadLocal, a: int64(limitSlot)})
		c.emit(instr{op: opLe})
		c.emit(instr{op: opAnd})
		c.emit(instr{op: opLoadLocal, a: int64(stepSlot)})
		c.emit(instr{op: opConst, a: 0})
		c.emit(instr{op: opLt})
		c.emit(instr{op: opLoadLocal, a: int64(curSlot)})
		c.emit(instr{op: opLoadLocal, a: int64(limitSlot)})
		c.emit(instr{op: opGe})
		c.emit(instr{op: opAnd})
		c.emit(instr{op: opOr})
		jf := c.emit(instr{op: opJumpIfFalse, a: -1})

		c.emit(instr{op: opLoadLocal, a: int64(curSlot)})
		c.emit(instr{op: opStoreLocal, a: int64(loopSlot)})
		breaksAt, continuesAt := c.beginLoop()
		if !c.block(x.Body, body) {
			return false
		}
		contTarget := c.owner.here()
		c.emit(instr{op: opLoadLocal, a: int64(curSlot)})
		c.emit(instr{op: opLoadLocal, a: int64(stepSlot)})
		c.emit(instr{op: opAdd})
		c.emit(instr{op: opStoreLocal, a: int64(curSlot)})
		c.emit(instr{op: opJump, a: int64(start)})
		c.owner.patch(jf)
		c.patchLoop(breaksAt, continuesAt, c.owner.here(), contTarget)
		return true

	case *domain.BreakStmt:
		c.owner.breaks = append(c.owner.breaks, c.emit(instr{op: opJump, a: -1}))
		return true

	case *domain.ContinueStmt:
		c.owner.continues = append(c.owner.continues, c.emit(instr{op: opJump, a: -1}))
		return true

	case *domain.ExprStmt:
		call, ok := x.X.(*domain.CallExpr)
		if !ok {
			c.why = "bare expression statement is not a call"
			return false
		}
		return c.call(call, sc)
	}
	return false
}

// store emits the store instruction for a slot.
func (c *bcCompiler) store(slot *bcSlot) {
	if slot.global {
		c.emit(instr{op: opStoreGlobal, a: int64(slot.idx)})
		return
	}
	c.emit(instr{op: opStoreLocal, a: int64(slot.idx)})
}

func (c *bcCompiler) assign(name string, value domain.Expr, sc *bcScope) bool {
	slot := sc.lookup(name)
	if slot == nil {
		slot = &bcSlot{idx: sc.add()}
		sc.names[name] = slot
	}
	if slot.builtin {
		return false
	}
	if slot.value && !slot.global {
		// A global holding a plain value lives in the val index space; the
		// VM's unboxed local slots cannot represent it. Writing it would hit
		// local slot 0 (the return channel) and silently corrupt the result.
		c.why = "assignment target '" + name + "' is a plain-value global"
		return false
	}
	if !c.expr(value, sc) {
		return false
	}
	c.store(slot)
	return true
}

func (c *bcCompiler) expr(e domain.Expr, sc *bcScope) bool {
	switch x := e.(type) {
	case *domain.IntLit:
		c.emit(instr{op: opConst, a: x.Value})
		return true

	case *domain.PrefixExpr:
		if x.Op != domain.TokenMinus {
			return false
		}
		if !c.expr(x.Right, sc) {
			return false
		}
		c.emit(instr{op: opNeg})
		return true

	case *domain.Ident:
		slot := sc.lookup(x.Name)
		if slot == nil || slot.builtin {
			return false
		}
		if slot.value && !slot.global {
			// Plain-value globals are boxed values in the val index space;
			// emitting opLoadLocal for one would read local slot 0 (the
			// return channel) and produce garbage. Give up instead.
			c.why = "global '" + x.Name + "' is not an integer slot"
			return false
		}
		if slot.global {
			c.emit(instr{op: opLoadGlobal, a: int64(slot.idx)})
			return true
		}
		c.emit(instr{op: opLoadLocal, a: int64(slot.idx)})
		return true

	case *domain.GroupExpr:
		return c.expr(x.Inner, sc)

	case *domain.InfixExpr:
		if !c.expr(x.Left, sc) {
			return false
		}
		if !c.expr(x.Right, sc) {
			return false
		}
		switch x.Op {
		case domain.TokenPlus:
			c.emit(instr{op: opAdd})
		case domain.TokenMinus:
			c.emit(instr{op: opSub})
		case domain.TokenStar:
			c.emit(instr{op: opMul})
		case domain.TokenSlash:
			c.emit(instr{op: opDiv})
		case domain.TokenPercent:
			c.emit(instr{op: opMod})
		case domain.TokenLt:
			c.emit(instr{op: opLt})
		case domain.TokenGt:
			c.emit(instr{op: opGt})
		case domain.TokenLtEq:
			c.emit(instr{op: opLe})
		case domain.TokenGtEq:
			c.emit(instr{op: opGe})
		case domain.TokenEq:
			c.emit(instr{op: opEq})
		case domain.TokenNeq:
			c.emit(instr{op: opNe})
		case domain.TokenAnd:
			c.emit(instr{op: opAnd})
		case domain.TokenOr:
			c.emit(instr{op: opOr})
		default:
			return false
		}
		return true

	case *domain.CallExpr:
		return c.call(x, sc)
	}
	return false
}

// call compiles a call. The callee must be a global name holding a function.
func (c *bcCompiler) call(x *domain.CallExpr, sc *bcScope) bool {
	callee, ok := x.Callee.(*domain.Ident)
	if !ok {
		c.why = "callee is not a name"
		return false
	}
	slot := sc.lookup(callee.Name)
	if slot != nil && slot.builtin {
		c.why = "callee is a builtin: " + callee.Name
		return false
	}
	target, ok := c.in.gscope.names[callee.Name]
	if !ok {
		c.why = "callee is not a global: " + callee.Name
		return false
	}
	// opCall reads globals.vals[target.idx]; only a slotVal global lives
	// there. An int or cell slot would resolve to the wrong value.
	if target.kind != slotVal {
		c.why = "callee global is not a value slot: " + callee.Name
		return false
	}
	for _, a := range x.Args {
		if !c.expr(a, sc) {
			return false
		}
	}
	_ = target
	c.emit(instr{op: opCall, a: int64(len(x.Args)), b: int64(target.idx)})
	return true
}
