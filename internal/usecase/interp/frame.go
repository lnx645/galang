package interp

import "galang/internal/domain"

// The engine compiles the syntax tree once into a tree of Go closures and then
// runs that tree. Three consequences matter:
//
//   - Node dispatch disappears: each node type becomes its own closure, so the
//     per-node type switch that dominated the tree-walker is gone.
//   - Variables live in frame slots resolved at compile time, so there is no
//     name lookup (and no map hashing) while running.
//   - Integer arithmetic between `int` slots stays unboxed in an int64 slice,
//     so a tight loop allocates nothing.
//
// Frames are pooled, which makes a function call allocation free.

// ctrl tells a statement list why it stopped early.
type ctrl int

const (
	ctrlNone ctrl = iota
	ctrlBreak
	ctrlContinue
	ctrlReturn
)

// cell is a captured variable box. Closures share it by pointer, which is what
// gives a counter closure its by-reference behaviour.
type cell struct {
	v domain.Value
}

// slotKind says where a variable lives inside a frame.
type slotKind uint8

const (
	// slotInt: unboxed int64 in frame.ints, valid while frame.isInt[idx].
	slotInt slotKind = iota
	// slotVal: boxed value in frame.vals.
	slotVal
	// slotCell: frame.vals[idx] holds a *cell owned by an enclosing function.
	slotCell
	// slotDyn: name only known at run time, in frame.dyn.
	slotDyn
)

// frame is one activation record.
type frame struct {
	ints  []int64
	vals  []interface{}
	isInt []bool
	// ivals holds the boxed value of an int slot that was demoted (assigned a
	// non-int, or a float). It is indexed in the int index space (nints), the
	// same space as ints/isInt — writing a demoted int slot into vals would
	// collide with the val index space and corrupt unrelated variables or
	// builtins.
	ivals []interface{}
	dyn   map[string]domain.Value
	// def is the frame the running function was defined in. Captured
	// variables are reached through it.
	def *frame
	// glob points at the globals frame, so compiled code can reach top-level
	// variables from inside a function without threading them through frames.
	glob *frame
	// refs counts closures created in this frame. A frame that a closure
	// captured must outlive the call, so it is never recycled.
	refs int
}

// capture links a cell slot in this function to the cell in the defining frame.
type capture struct {
	localIdx int
	defIdx   int
}

// paramSlot describes where a parameter is bound in the frame.
type paramSlot struct {
	kind slotKind
	idx  int
	typ  *domain.TypeExpr
	// defaultFn evaluates the parameter default in the defining scope.
	defaultFn exprFn
}

// compFn is a compiled function body plus its frame layout.
type compFn struct {
	name     string
	nints    int
	nvals    int
	body     stmtFn
	params   []paramSlot
	captures []capture
	// async marks `async fn`: a call returns a promise resolved by await.
	async bool
	// minArgs counts parameters without a default value.
	minArgs int
	// retType is the declared return annotation, if any.
	retType *domain.TypeExpr
	// elem is the element type of an `array<T>` return annotation.
	elem *domain.TypeExpr
	// file is the source file this function was compiled from, so a
	// runtime error inside it is reported against the file the code
	// lives in, not the file whose top-level happens to be running.
	file string
	pos  domain.Position
}

// closure is a compiled function bound to its defining frame. int is set when
// the whole function is integer only, which enables an unboxed calling
// convention with no boxed argument list.
type closure struct {
	fn  *compFn
	def *frame
	int *intCompFn
	// code is set when the function has bytecode; it takes precedence over int.
	code *intCode
}

func (c *closure) Type() domain.TypeTag { return domain.TypeFunc }
func (c *closure) Truthy() bool         { return true }
func (c *closure) String() string {
	if c.fn.name == "" {
		return "<function>"
	}
	return "<function " + c.fn.name + ">"
}

// compile-time frame layout, per function.
type frameLayout struct {
	nints int
	nvals int
}

func (l *frameLayout) addInt() int {
	i := l.nints
	l.nints++
	return i
}

func (l *frameLayout) addVal() int {
	i := l.nvals
	l.nvals++
	return i
}

// freeFrames is the interpreter's frame cache. An Interp is single goroutine
// by design (it owns the call stack, the depth counter and the argument
// stack), so a plain free list is both safe and far cheaper than sync.Pool,
// which showed up as 24% of fib's CPU time.
type frameCache struct {
	free []*frame
}

func (c *frameCache) get() *frame {
	if n := len(c.free); n > 0 {
		f := c.free[n-1]
		c.free[n-1] = nil
		c.free = c.free[:n-1]
		return f
	}
	return &frame{}
}

func (c *frameCache) put(f *frame) {
	// A bound keeps a pathological call pattern from retaining every frame.
	if len(c.free) < 512 {
		c.free = append(c.free, f)
	}
}

// newFrame prepares a frame for a call and binds the arguments.
func (in *Interp) newFrame(fn *compFn, def *frame, args []domain.Value, pos domain.Position) (*frame, error) {
	if fn.minArgs > 0 && len(args) < fn.minArgs {
		return nil, in.errf(pos, "%s() missing argument(s): needs %d, got %d", displayName(fn.name), fn.minArgs, len(args))
	}
	if len(args) > len(fn.params) {
		return nil, in.errf(pos, "%s() takes %d argument(s), got %d", displayName(fn.name), len(fn.params), len(args))
	}

	f := in.frames.get()
	// One branch for the common case: the cached frame is already big enough.
	if cap(f.ints) < fn.nints || cap(f.isInt) < fn.nints || cap(f.vals) < fn.nvals {
		f.ints = make([]int64, fn.nints)
		f.isInt = make([]bool, fn.nints)
		f.vals = make([]interface{}, fn.nvals)
	}
	if cap(f.ivals) < fn.nints {
		f.ivals = make([]interface{}, fn.nints)
	}
	f.ints = f.ints[:fn.nints]
	f.isInt = f.isInt[:fn.nints]
	f.vals = f.vals[:fn.nvals]
	f.ivals = f.ivals[:fn.nints]
	// Reset: the isInt flag is what distinguishes an unboxed int from a
	// demoted value slot, so it is cleared first.
	for i := range f.isInt {
		f.isInt[i] = false
		f.ints[i] = 0
	}
	for i := range f.vals {
		f.vals[i] = nil
	}
	for i := range f.ivals {
		f.ivals[i] = nil
	}
	f.dyn = nil
	f.def = def
	f.refs = 0
	// Always refresh: a pooled frame may come from another interpreter, and a
	// stale globals pointer would read the wrong variables.
	f.glob = def.glob

	for i, p := range fn.params {
		var v domain.Value
		if i < len(args) {
			v = args[i]
		} else if p.defaultFn != nil {
			dv, err := p.defaultFn(def)
			if err != nil {
				in.frames.put(f)
				return nil, err
			}
			v = dv
		} else {
			v = domain.Null{}
		}
		switch p.kind {
		case slotCell:
			// A parameter that the function shares with a closure gets its own
			// fresh box, so writing to it stays local to the call.
			f.vals[p.idx] = &cell{v: v}
			continue
		case slotInt:
			if n, ok := v.(domain.Int); ok {
				f.ints[p.idx] = int64(n)
				f.isInt[p.idx] = true
				continue
			}
			if err := in.checkType(p.typ, v, pos); err != nil {
				in.frames.put(f)
				return nil, err
			}
			// Anything that is not a plain int demotes the slot: the boxed
			// value lives in ivals (the int index space), and isInt turns
			// false so readers take the boxed path. Storing it in vals would
			// collide with the val index space, and forcing isInt true here
			// would leave readers a stale integer.
			if fv, isFloat := v.(domain.Float); isFloat {
				f.ivals[p.idx] = fv
				f.isInt[p.idx] = false
				continue
			}
			f.ivals[p.idx] = v
			f.isInt[p.idx] = false
			continue
		}
		f.vals[p.idx] = v
	}

	for _, c := range fn.captures {
		if cl := def.cellAt(c.defIdx); cl != nil {
			f.vals[c.localIdx] = cl
		}
	}
	return f, nil
}

// release ends a frame. Frames captured by a closure are left to the garbage
// collector; recycling them would corrupt live closures, which is exactly the
// bug that made a closure see its own caller's variables.
func (f *frame) release(cache *frameCache) {
	if f.refs > 0 {
		return
	}
	for i := range f.vals {
		f.vals[i] = nil
	}
	for i := range f.ivals {
		f.ivals[i] = nil
	}
	f.dyn = nil
	f.glob = nil
	f.def = nil
	cache.put(f)
}

// displayName renders a function name for diagnostics.
func displayName(name string) string {
	if name == "" {
		return "<anonymous function>"
	}
	return name
}

// getVal reads a value slot, returning nil when the slot was never written.
func (f *frame) getVal(idx int) domain.Value {
	if idx < 0 || idx >= len(f.vals) {
		return nil
	}
	v, _ := f.vals[idx].(domain.Value)
	return v
}

// getIval reads the demoted value of an int slot (the int index space).
func (f *frame) getIval(idx int) domain.Value {
	if idx < 0 || idx >= len(f.ivals) {
		return nil
	}
	v, _ := f.ivals[idx].(domain.Value)
	return v
}

// cellAt returns the cell in a cell slot, creating one lazily so a box always
// exists before a closure reads or writes the variable it shares.
func (f *frame) cellAt(idx int) *cell {
	if idx < 0 || idx >= len(f.vals) {
		return nil
	}
	if cl, ok := f.vals[idx].(*cell); ok {
		return cl
	}
	cl := &cell{}
	if cur := f.getVal(idx); cur != nil {
		cl.v = cur
	}
	f.vals[idx] = cl
	return cl
}

// setInt stores an unboxed int, keeping the validity flag in step.
func (f *frame) setInt(idx int, v int64) {
	f.ints[idx] = v
	f.isInt[idx] = true
}

// store writes a value into a slot of the given kind, demoting an int slot to a
// boxed slot when the value turns out not to be an int.
func (f *frame) store(kind slotKind, idx int, name string, v domain.Value) {
	switch kind {
	case slotInt:
		if n, ok := v.(domain.Int); ok {
			f.ints[idx] = int64(n)
			f.isInt[idx] = true
			return
		}
		// A float must not be squeezed into the int64 slot (that silently
		// truncates $x = 72 → $x = 3.9): the slot demotes and the boxed
		// value lives in ivals, the int index space.
		f.isInt[idx] = false
		f.ivals[idx] = v
	case slotCell:
		if cl := f.cellAt(idx); cl != nil {
			cl.v = v
			return
		}
		// Defensive: a cell slot that lost its box keeps working.
		f.vals[idx] = &cell{v: v}
	case slotVal:
		f.vals[idx] = v
	default:
		if f.dyn == nil {
			f.dyn = make(map[string]domain.Value, 4)
		}
		f.dyn[name] = v
	}
}

// storeInt writes an unboxed int, so the fast path never allocates a boxed
// value just to satisfy the generic store.
func storeInt(f *frame, slot *cslot, name string, n int64) {
	if slot == nil {
		return
	}
	base := f
	if slot.global {
		base = f.glob
	}
	switch slot.kind {
	case slotInt:
		base.ints[slot.idx] = n
		base.isInt[slot.idx] = true
	case slotCell:
		if cl := base.cellAt(slot.idx); cl != nil {
			cl.v = domain.Int(n)
		}
	default:
		base.store(slot.kind, slot.idx, name, domain.Int(n))
	}
}

// load reads a slot.
func (f *frame) load(kind slotKind, idx int, name string) (domain.Value, bool) {
	switch kind {
	case slotInt:
		if f.isInt[idx] {
			return domain.Int(f.ints[idx]), true
		}
		v := f.getIval(idx)
		return v, v != nil
	case slotCell:
		if cl := f.cellAt(idx); cl != nil {
			return cl.v, true
		}
		return nil, false
	case slotVal:
		v := f.getVal(idx)
		return v, v != nil
	default:
		v, ok := f.dyn[name]
		return v, ok
	}
}
