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

// promise carries a deferred call. It implements domain.Value so it can be
// stored in variables and passed around like any other value.
type promise struct {
	in *Interp

	// A promise runs either a compiled closure (async fn, spawn of a
	// closure) or a generic callable. Keeping the closure inline avoids
	// allocating a thunk closure per promise.
	fn   *closure
	args []domain.Value
	call func() (domain.Value, error)
	// Up to two arguments are held inline (arr) so the common
	// one-argument promise costs a single allocation instead of two.
	arr [2]domain.Value

	ran bool
	val domain.Value
	err error
	// running guards against `await p` inside p's own body.
	running bool
	pos     domain.Position
}

func (p *promise) Type() domain.TypeTag { return domain.TypePromise }
func (p *promise) Truthy() bool         { return true }
func (p *promise) String() string {
	switch {
	case !p.ran:
		return "<promise pending>"
	case p.err != nil:
		return "<promise rejected>"
	default:
		return "<promise resolved>"
	}
}

// newPromiseClosure builds a promise for a deferred closure call and
// registers it so an unawaited spawn still runs at the next drain. The
// callee and arguments are stored as fields — no thunk closure per promise —
// and up to two arguments are copied into the struct itself so the common
// case costs a single allocation.
func (in *Interp) newPromiseClosure(pos domain.Position, c *closure, args []domain.Value) *promise {
	p := &promise{in: in, fn: c, pos: pos}
	if len(args) <= len(p.arr) {
		copy(p.arr[:], args)
		p.args = p.arr[:len(args)]
	} else {
		p.args = append(make([]domain.Value, 0, len(args)), args...)
	}
	in.pending = append(in.pending, p)
	return p
}

// newPromise builds a promise from a generic callable and registers it.
func (in *Interp) newPromise(pos domain.Position, call func() (domain.Value, error)) *promise {
	p := &promise{in: in, call: call, pos: pos}
	in.pending = append(in.pending, p)
	return p
}

// run executes the deferred call once and records the outcome.
func (p *promise) run() (domain.Value, error) {
	if p.ran {
		return p.val, p.err
	}
	p.running = true
	var v domain.Value
	var err error
	if p.fn != nil {
		v, err = p.in.callBody(p.fn, p.args, p.pos)
	} else {
		v, err = p.call()
	}
	p.running = false
	p.ran = true
	p.val = v
	p.err = err
	return v, err
}

// awaitValue resolves a promise by running its thunk inline. Any other value
// passes through unchanged, so `await` is safe on already-resolved data.
func (in *Interp) awaitValue(v domain.Value, pos domain.Position) (domain.Value, error) {
	p, ok := v.(*promise)
	if !ok {
		return v, nil
	}
	if p.running {
		return nil, in.errf(pos, "await: cyclic await on a promise that is still running")
	}
	v, err := p.run()
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
			if p.ran {
				continue
			}
			// The error sticks to the promise; await re-raises it, and an
			// unawaited spawn reports it at drain time.
			_, _ = p.run()
		}
	}
	return nil
}
