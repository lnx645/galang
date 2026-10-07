package interp

import (
	"garurda/internal/domain"
)

// A bytecode virtual machine for integer functions.
//
// Compiling to Go closures was a large win, but a call still walked a chain of
// closures. This VM replaces that chain with one flat loop: instructions live
// in a contiguous array, the interpreter keeps an explicit frame stack and an
// explicit value stack, and a call is two slice operations plus a jump. No Go
// recursion, no closure calls, no allocation once the stacks are warm.
//
// It also makes an opcode cache possible later: instructions are plain data, so
// they can be written to disk and reloaded instead of being recompiled, which
// is what PHP calls opcache.

type opCode uint8

const (
	opConst       opCode = iota // push a
	opLoadLocal                 // push locals[base+a]
	opStoreLocal                // locals[base+a] = pop
	opLoadGlobal                // push globals.ints[a]
	opStoreGlobal               // globals.ints[a] = pop
	opAdd                       // push a + pop
	opSub
	opMul
	opDiv
	opMod
	opNeg
	opNot
	opLt // push 1 or 0
	opGt
	opLe
	opGe
	opEq
	opNe
	opAnd // operands are 0 or 1, so bitwise and is logical and
	opOr
	opJump        // pc = a
	opJumpIfFalse // if pop == 0 then pc = a
	opJumpIfTrue  // if pop != 0 then pc = a
	opCall        // call the function in global value slot b with a arguments
	opReturn      // the result is locals[base]
)

// instr is one instruction. Being a flat value type keeps the instruction array
// a plain memory block.
type instr struct {
	op opCode
	a  int64
	b  int64
}

// intCode is a compiled integer function.
type intCode struct {
	name string
	code []instr
	// callees caches the function each opCall resolves, indexed by the
	// instruction's own position. Without it every call paid an interface
	// lookup to turn the stored value into a closure, which the profile put
	// at 30% of the total.
	callees []*closure
	// nlocals counts every local slot; slot 0 carries the return value.
	nlocals int
	nparams int
}

// vmFrame is one entry of the explicit call stack.
type vmFrame struct {
	code      *intCode
	pc        int
	localBase int
}

// runInt executes a compiled function from Go code and returns its value.
func (in *Interp) runInt(code *intCode, args []int64, pos domain.Position) (int64, error) {
	in.vstack = in.vstack[:0]
	in.locals = in.locals[:0]
	in.vframes = in.vframes[:0]

	in.locals = growLocals(in.locals, code.nlocals)
	copy(in.locals[1:], args)
	in.vframes = append(in.vframes, vmFrame{code: code, localBase: 0})

	if err := in.runLoop(pos); err != nil {
		return 0, err
	}
	// runInt stores the result of the outermost frame in in.retVal.
	return in.retVal, nil
}

// growLocals extends the local stack, zeroing the new slots.
func growLocals(locals []int64, n int) []int64 {
	need := len(locals) + n
	if cap(locals) < need {
		grown := make([]int64, need)
		copy(grown, locals)
		return grown
	}
	out := locals[:need]
	for i := len(locals); i < need; i++ {
		out[i] = 0
	}
	return out
}

// runLoop is the interpreter: one switch over the instruction array.
//
// Everything the loop touches lives in the interpreter, so a frame push or pop
// can never leave a stale copy behind. That was a real bug in the first
// version, which cached the stacks in locals and lost values on every call.
func (in *Interp) runLoop(pos domain.Position) error {
	for {
		fi := len(in.vframes) - 1
		if fi < 0 {
			return nil
		}
		cur := in.vframes[fi].code
		code := cur.code
		callees := cur.callees
		base := in.vframes[fi].localBase
		pc := in.vframes[fi].pc
		var ret int64

		for pc < len(code) {
			ins := code[pc]
			pc++
			switch ins.op {
			case opConst:
				in.vstack = append(in.vstack, ins.a)

			case opLoadLocal:
				in.vstack = append(in.vstack, in.locals[base+int(ins.a)])

			case opStoreLocal:
				n := len(in.vstack) - 1
				in.locals[base+int(ins.a)] = in.vstack[n]
				in.vstack = in.vstack[:n]

			case opLoadGlobal:
				in.vstack = append(in.vstack, in.globals.ints[ins.a])

			case opStoreGlobal:
				n := len(in.vstack) - 1
				in.globals.ints[ins.a] = in.vstack[n]
				in.globals.isInt[ins.a] = true
				in.vstack = in.vstack[:n]

			case opAdd, opSub, opMul, opDiv, opMod:
				b := in.vstack[len(in.vstack)-1]
				a := in.vstack[len(in.vstack)-2]
				in.vstack = in.vstack[:len(in.vstack)-2]
				var r int64
				switch ins.op {
				case opAdd:
					r = a + b
				case opSub:
					r = a - b
				case opMul:
					r = a * b
				case opDiv:
					if b == 0 {
						return in.errf(pos, "division by zero")
					}
					r = a / b
				default:
					if b == 0 {
						return in.errf(pos, "modulo by zero")
					}
					r = a % b
				}
				in.vstack = append(in.vstack, r)

			case opAnd, opOr:
				b := in.vstack[len(in.vstack)-1]
				a := in.vstack[len(in.vstack)-2]
				in.vstack = in.vstack[:len(in.vstack)-2]
				if ins.op == opAnd {
					in.vstack = append(in.vstack, a&b)
				} else {
					in.vstack = append(in.vstack, a|b)
				}

			case opNeg:
				in.vstack[len(in.vstack)-1] = -in.vstack[len(in.vstack)-1]

			case opNot:
				if in.vstack[len(in.vstack)-1] == 0 {
					in.vstack[len(in.vstack)-1] = 1
				} else {
					in.vstack[len(in.vstack)-1] = 0
				}

			case opLt, opGt, opLe, opGe, opEq, opNe:
				b := in.vstack[len(in.vstack)-1]
				a := in.vstack[len(in.vstack)-2]
				in.vstack = in.vstack[:len(in.vstack)-2]
				var res bool
				switch ins.op {
				case opLt:
					res = a < b
				case opGt:
					res = a > b
				case opLe:
					res = a <= b
				case opGe:
					res = a >= b
				case opEq:
					res = a == b
				default:
					res = a != b
				}
				if res {
					in.vstack = append(in.vstack, 1)
				} else {
					in.vstack = append(in.vstack, 0)
				}

			case opJump:
				pc = int(ins.a)

			case opJumpIfFalse:
				n := len(in.vstack) - 1
				v := in.vstack[n]
				in.vstack = in.vstack[:n]
				if v == 0 {
					pc = int(ins.a)
				}

			case opJumpIfTrue:
				n := len(in.vstack) - 1
				v := in.vstack[n]
				in.vstack = in.vstack[:n]
				if v != 0 {
					pc = int(ins.a)
				}

			case opCall:
				nargs := int(ins.a)
				target := int(ins.b)
				argsStart := len(in.vstack) - nargs

				// Resolve the callee, reusing the cached one when the global slot
				// still holds it. Comparing interface values needs no runtime
				// type lookup, unlike converting interface{} to domain.Value.
				slotVal := in.globals.vals[target]
				var cl *closure
				if cached := callees[pc-1]; cached != nil && slotVal == cached {
					cl = cached
				} else {
					gv, _ := slotVal.(domain.Value)
					var ok bool
					cl, ok = gv.(*closure)
					if !ok {
						return in.errf(pos, "value in slot %d is not a function", target)
					}
					callees[pc-1] = cl
				}

				if cl.code != nil && cl.code.nparams == nargs {
					if len(in.vframes) >= in.maxDepth {
						return in.errf(pos, "maximum call depth exceeded (%d): recursion too deep?", in.maxDepth)
					}
					newBase := len(in.locals)
					in.locals = growLocals(in.locals, cl.code.nlocals)
					copy(in.locals[newBase+1:], in.vstack[argsStart:])
					in.vstack = in.vstack[:argsStart]
					in.vframes[fi].pc = pc
					in.vframes = append(in.vframes, vmFrame{code: cl.code, localBase: newBase})
					goto nextFrame
				}

				// The callee has no integer code: use the general path with
				// boxed arguments.
				gv, _ := slotVal.(domain.Value)
				boxed := make([]domain.Value, nargs)
				for i := 0; i < nargs; i++ {
					boxed[i] = domain.Int(in.vstack[argsStart+i])
				}
				in.vstack = in.vstack[:argsStart]
				res, err := in.callValue(gv, boxed, pos)
				if err != nil {
					return err
				}
				iv, isInt := res.(domain.Int)
				if !isInt {
					return in.errf(pos, "function returned %s where an integer was required", domain.TypeName(res))
				}
				in.vstack = append(in.vstack, int64(iv))

			case opReturn:
				// The frame's locals are released on return; leaving them in
				// place made the local stack grow with every call, which turned
				// deep recursion quadratic.
				ret = in.locals[base]
				in.locals = in.locals[:base]
				in.vframes[fi].pc = pc
				in.vframes = in.vframes[:fi]
				if len(in.vframes) > 0 {
					in.vstack = append(in.vstack, ret)
				} else {
					in.retVal = ret
					in.vstack = in.vstack[:0]
				}
				goto nextFrame
			}
		}
		// Falling off the end of the code behaves like a bare return.
		ret = in.locals[base]
		in.locals = in.locals[:base]
		in.vframes[fi].pc = pc
		in.vframes = in.vframes[:fi]
		if len(in.vframes) > 0 {
			in.vstack = append(in.vstack, ret)
		} else {
			in.retVal = ret
			in.vstack = in.vstack[:0]
		}
	nextFrame:
	}
}
