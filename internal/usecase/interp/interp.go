// Package interp evaluates Garurda programs.
//
// The engine compiles the syntax tree into Go closures and runs those, so the
// hot loop contains no node-type dispatch, no name lookups, and (for integers)
// no boxing. See compiler.go and frame.go.
package interp

import (
	"fmt"
	"io"
	"os"
	"strings"

	"garurda/internal/domain"
	"garurda/internal/infra/template"
	"garurda/internal/usecase/parse"
)

// Error is a runtime error carrying the source position where it happened.
type Error struct {
	Msg   string
	Pos   domain.Position
	File  string
	Value *domain.ErrorValue // non-nil for thrown errors
	Stack []StackFrame
}

// StackFrame is one entry of an error stack trace.
type StackFrame struct {
	Func string
	File string
	Pos  domain.Position
}

func (e *Error) Error() string {
	var b strings.Builder
	file := e.File
	if file == "" {
		file = "<garurda>"
	}
	fmt.Fprintf(&b, "%s:%d:%d: %s", file, e.Pos.Line, e.Pos.Col, e.Msg)
	if len(e.Stack) > 0 {
		b.WriteString("\n  at ")
		frames := make([]string, 0, len(e.Stack))
		for _, f := range e.Stack {
			frames = append(frames, fmt.Sprintf("%s (%s:%d)", f.Func, f.File, f.Pos.Line))
		}
		b.WriteString(strings.Join(frames, "\n  at "))
	}
	return b.String()
}

// Frame appends a call site to the trace.
func (e *Error) Frame(f StackFrame) *Error {
	e.Stack = append(e.Stack, f)
	return e
}

// Thrown reports whether the error came from throw and is catchable.
func (e *Error) Thrown() bool { return e.Value != nil }

// Interp evaluates Garurda programs.
type Interp struct {
	// Out receives print/println output.
	Out io.Writer
	// Err receives runtime diagnostics.
	Err io.Writer

	// globals is the compiled top-level frame plus its layout.
	globals *frame
	// gscope is the compile-time scope for top-level names.
	gscope *cscope
	// frames is the activation record cache.
	frames frameCache
	// trace holds the call stack for error messages. Only a position and a
	// function pointer are recorded per call; the human readable text is built
	// when an error is actually formatted. Recording a whole StackFrame per
	// call cost 13% of fib's CPU time.
	tracePos   [64]domain.Position
	traceFn    [64]*compFn
	traceDepth int
	// The bytecode VM's stacks. They are reused across calls, so a running
	// program stops allocating once they are warm.
	vstack  []int64
	locals  []int64
	vframes []vmFrame
	// retVal receives the result of the outermost bytecode frame.
	retVal int64

	// argStack is the shared argument stack. Calls push and pop instead of
	// allocating a slice per call, which is what makes recursion allocation
	// free.
	argStack []domain.Value
	// pending holds promises whose thunk has not run yet; drained after
	// Run and after each HTTP handler so unawaited spawns still execute.
	pending []*promise
	// depth guards against runaway recursion.
	depth    int
	maxDepth int
	// Modules are the builtin module factories, e.g. "strings" and "math".
	Modules map[string]func() *domain.Obj
	// webRoutes holds HTTP routes registered from Garurda code via the
	// http module. It is nil until the http module is first loaded.
	webRoutes *webRoutes
	// engine renders Blade templates for the http module.
	engine *template.Engine
	// File is the name used in diagnostics for the program being run.
	File string
	// parser obtains programs from source. It exists so the caller can plug in a
	// syntax tree cache without the engine depending on any storage.
	parser func(src, file string) (*parse.Program, error)
}

// SetViewsDir sets the directory used by http.render() for Blade templates.
func (in *Interp) SetViewsDir(dir string) {
	in.engine = template.NewEngine(dir)
}

// UseParser installs a source-to-program function, such as a cache.
func (in *Interp) UseParser(f func(src, file string) (*parse.Program, error)) {
	in.parser = f
}

// programFrom parses src, using the installed parser when there is one.
func (in *Interp) programFrom(src, file string) (*parse.Program, error) {
	if in.parser != nil {
		return in.parser(src, file)
	}
	return parse.Parse(src, file)
}

// New creates an interpreter writing to out.
func New(out, errOut io.Writer) *Interp {
	in := &Interp{
		Out:      out,
		Err:      errOut,
		maxDepth: 1500,
		File:     "<stdin>",
	}
	gs := newCScope(nil, true)
	gs.isGlobal = true
	in.gscope = gs
	in.globals = &frame{}
	in.globals.glob = in.globals
	in.argStack = make([]domain.Value, 0, 64)
	in.vstack = make([]int64, 0, 256)
	in.locals = make([]int64, 0, 256)
	in.vframes = make([]vmFrame, 0, 64)
	in.webRoutes = &webRoutes{}
	in.engine = template.NewEngine("views")
	in.installGlobals()
	return in
}

func (in *Interp) errf(pos domain.Position, format string, args ...interface{}) *Error {
	return &Error{Msg: fmt.Sprintf(format, args...), Pos: pos, File: in.File, Stack: in.snapshot()}
}

// Throw builds a catchable error value.
func (in *Interp) Throw(msg string, code string, status int, pos domain.Position) *Error {
	ev := &domain.ErrorValue{Message: msg, Code: code, Status: status}
	return &Error{Msg: msg, Pos: pos, File: in.File, Value: ev, Stack: in.snapshot()}
}

// wrapThrown converts an error value into a catchable runtime error.
func (in *Interp) wrapThrown(ev *domain.ErrorValue, p domain.Position) error {
	return &Error{Msg: ev.Message, Pos: p, File: in.File, Value: ev, Stack: in.snapshot()}
}

func (in *Interp) snapshot() []StackFrame {
	n := in.traceDepth
	if n > len(in.tracePos) {
		n = len(in.tracePos)
	}
	out := make([]StackFrame, 0, n)
	for i := 0; i < n; i++ {
		fr := StackFrame{Pos: in.tracePos[i], File: in.File}
		if in.traceFn[i] != nil {
			fr.Func = displayName(in.traceFn[i].name)
		}
		out = append(out, fr)
	}
	return out
}

// printLine writes one line plus a newline.
func (in *Interp) printLine(text string) error {
	_, err := in.Out.Write([]byte(text + "\n"))
	return err
}

// RunFile parses and executes a file.
func (in *Interp) RunFile(path string) error {
	prog, err := in.CompileFile(path)
	if err != nil {
		return err
	}
	_, err = in.Run(prog)
	return err
}

// CompileFile parses a file into a program, going through the syntax tree
// cache when one is installed.
func (in *Interp) CompileFile(path string) (*parse.Program, error) {
	src, err := readFile(path)
	if err != nil {
		return nil, err
	}
	return in.programFrom(src, path)
}

// Eval parses, compiles and runs source text in the global scope, returning
// the value of the last statement.
func (in *Interp) Eval(src, file string) (domain.Value, error) {
	prog, err := in.programFrom(src, file)
	if err != nil {
		return nil, err
	}
	return in.Run(prog)
}

// Run compiles and executes a program.
func (in *Interp) Run(prog *parse.Program) (domain.Value, error) {
	prev := in.File
	in.File = prog.File
	defer func() { in.File = prev }()

	// Extend the global layout with any names this program introduces.
	c := &compiler{in: in, globalScope: in.gscope, captures: &[]capture{}}
	c.captures = nil
	c.captures = &[]capture{}
	body := c.compileStmts(prog.Stmts, in.gscope)
	if c.err != nil {
		return nil, c.err
	}
	// Grow the globals frame to cover the final layout.
	in.growGlobals()

	_, v, err := body(in.globals)
	// Promises created but never awaited still run, so `spawn` at top level
	// has an effect before the program exits.
	if derr := in.drainPending(); err == nil && derr != nil {
		err = derr
	}
	return v, err
}

// grow resizes a frame to hold a layout. New value slots start as null so a
// REPL keeps values the program did not reassign.
func (f *frame) grow(l *frameLayout) {
	if cap(f.ints) < l.nints {
		old := f.ints
		f.ints = make([]int64, l.nints)
		copy(f.ints, old)
		oldIs := f.isInt
		f.isInt = make([]bool, l.nints)
		copy(f.isInt, oldIs)
	}
	f.ints = f.ints[:l.nints]
	f.isInt = f.isInt[:l.nints]
	if cap(f.vals) < l.nvals {
		old := f.vals
		f.vals = make([]interface{}, l.nvals)
		copy(f.vals, old)
	}
	f.vals = f.vals[:l.nvals]
	if cap(f.ivals) < l.nints {
		old := f.ivals
		f.ivals = make([]interface{}, l.nints)
		copy(f.ivals, old)
	}
	f.ivals = f.ivals[:l.nints]
	// Fresh slots read as null, which is also what makes a REPL keep the
	// values a previous statement stored.
	for i := range f.vals {
		if f.vals[i] == nil {
			f.vals[i] = domain.Null{}
		}
	}
	for i := range f.ivals {
		if f.ivals[i] == nil {
			f.ivals[i] = domain.Null{}
		}
	}
}

// growGlobals resizes the globals frame after compilation.
func (in *Interp) growGlobals() {
	l := in.gscope.layout
	g := in.globals
	g.grow(l)
}

// Global returns the top-level scope (used by tests and tooling).
func (in *Interp) Global() *cscope { return in.gscope }

// readFile loads a source file. It is a variable so tests can stub it.
var readFile = func(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
