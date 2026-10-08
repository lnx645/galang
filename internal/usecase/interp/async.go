package interp

import (
	"garurda/internal/domain"
)

// Async execution is cooperative: an `async fn` call builds a promise with a
// thunk (the real call, run later). `await` runs the thunk right where it
// stands, `gather` runs several in order and collects results, `spawn` makes
// a fire-and-forget promise. Everything runs on the caller's goroutine, so
// the interpreter's shared state (frames, argStack, trace) is never touched
// by two goroutines at once.

// Promise lifecycle states packed into one byte.
const (
	promPending byte = iota
	promRunning
	promDone
)

// boxNargs marks a promise whose arguments did not fit inline and live in an
// *argBox stored in arr[0].
const boxNargs = 0xff

// argBox holds an argument list longer than the two inline slots.
type argBox struct {
	vals []domain.Value
}

func (*argBox) Type() domain.TypeTag { return domain.TypeArray }
func (*argBox) Truthy() bool         { return true }
func (b *argBox) String() string     { return "<args>" }

// errBox stores a rejected promise's error in the result slot. The error is
// never exposed as a value; await unwraps it back into a Go error.
type errBox struct {
	e error
}

func (*errBox) Type() domain.TypeTag { return domain.TypeError }
func (*errBox) Truthy() bool         { return false }
func (b *errBox) String() string     { return b.e.Error() }

// promise carries a deferred call. It implements domain.Value so it can be
// stored in variables and passed around like any other value.
//
// The struct is deliberately compact (target <= 64 bytes): a `spawn` loop can
// create a hundred thousand of them before the drain, and they all stay live
// until then. arr is overlaid:
//
//	before run: arr[:nargs] are the arguments (or arr[0] is *argBox)
//	after run:  arr[0] is the result, arr[1] is *errBox when rejected
type promise struct {
	// fn is set for a compiled closure, call for any other callable.
	fn   *closure
	call func() (domain.Value, error)

	arr   [2]domain.Value
	line  int32
	col   int32
	nargs uint8 // inline argument count, boxNargs, or unused after run
	st    byte  // promPending / promRunning / promDone
}

func (p *promise) Type() domain.TypeTag { return domain.TypePromise }
func (p *promise) Truthy() bool         { return true }
func (p *promise) String() string {
	switch {
	case p.st != promDone:
		return "<promise pending>"
	case p.err() != nil:
		return "<promise rejected>"
	default:
		return "<promise resolved>"
	}
}

// pos is the position where the promise was created, kept as int32 pairs
// because a Position of two machine ints would cost 16 bytes.
func (p *promise) pos() domain.Position {
	return domain.Position{Line: int(p.line), Col: int(p.col)}
}

// argsBuf resolves the argument list captured before the run.
func (p *promise) argsBuf() []domain.Value {
	if p.nargs == boxNargs {
		if b, ok := p.arr[0].(*argBox); ok {
			return b.vals
		}
		return nil
	}
	return p.arr[:p.nargs]
}

// result reads the recorded outcome. It is only valid after promDone.
func (p *promise) result() (domain.Value, error) {
	if b, ok := p.arr[1].(*errBox); ok {
		return nil, b.e
	}
	return p.arr[0], nil
}

func (p *promise) err() error {
	if b, ok := p.arr[1].(*errBox); ok {
		return b.e
	}
	return nil
}

// store records the outcome and switches to promDone.
func (p *promise) store(v domain.Value, err error) {
	p.arr[0] = v
	if err != nil {
		p.arr[1] = &errBox{e: err}
	} else {
		p.arr[1] = nil
	}
	p.st = promDone
}

// newPromiseClosure builds a promise for a deferred closure call and
// registers it so an unawaited spawn still runs at the next drain. Arguments
// are copied into the struct itself (up to two) so the common case costs a
// single allocation.
func (in *Interp) newPromiseClosure(pos domain.Position, c *closure, args []domain.Value) *promise {
	p := &promise{fn: c, line: int32(pos.Line), col: int32(pos.Col)}
	if len(args) <= len(p.arr) {
		copy(p.arr[:], args)
		p.nargs = uint8(len(args))
	} else {
		p.arr[0] = &argBox{vals: append([]domain.Value(nil), args...)}
		p.nargs = boxNargs
	}
	in.pending = append(in.pending, p)
	return p
}

// newPromise builds a promise from a generic callable and registers it. The
// callable captures its own arguments, so no slots are needed here.
func (in *Interp) newPromise(pos domain.Position, call func() (domain.Value, error)) *promise {
	p := &promise{call: call, line: int32(pos.Line), col: int32(pos.Col)}
	in.pending = append(in.pending, p)
	return p
}

// run executes the deferred call once and records the outcome.
func (in *Interp) runPromise(p *promise) (domain.Value, error) {
	if p.st == promDone {
		return p.result()
	}
	p.st = promRunning
	var v domain.Value
	var err error
	if p.fn != nil {
		v, err = in.callBody(p.fn, p.argsBuf(), p.pos())
	} else {
		v, err = p.call()
	}
	p.store(v, err)
	return v, err
}

// awaitValue resolves a promise by running its thunk inline. Any other value
// passes through unchanged, so `await` is safe on already-resolved data.
func (in *Interp) awaitValue(v domain.Value, pos domain.Position) (domain.Value, error) {
	p, ok := v.(*promise)
	if !ok {
		return v, nil
	}
	if p.st == promRunning {
		return nil, in.errf(pos, "await: cyclic await on a promise that is still running")
	}
	v, err := in.runPromise(p)
	// Drop the promise from the pending queue when it is the newest entry
	// (the common LIFO case), so an awaited loop does not accumulate a
	// million dead promises until the next drain.
	if n := len(in.pending); n > 0 && in.pending[n-1] == p {
		in.pending = in.pending[:n-1]
	}
	return v, err
}

// drainPending runs every promise that has not run yet, including promises
// created while draining. Unawaited spawns therefore still take effect.
func (in *Interp) drainPending() error {
	for guard := 0; len(in.pending) > 0; guard++ {
		if guard > 10000 {
			in.pending = in.pending[:0]
			return in.errf(domain.Position{}, "async: drain did not settle (promise loop?)")
		}
		batch := in.pending
		in.pending = nil
		for _, p := range batch {
			if p.st == promDone {
				continue
			}
			// The error sticks to the promise; await re-raises it, and an
			// unawaited spawn reports it at drain time.
			_, _ = in.runPromise(p)
		}
	}
	return nil
}
