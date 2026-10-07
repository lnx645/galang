// Package interp evaluates a Garurda syntax tree. It is a tree-walking
// interpreter: correctness first, with measured optimisations to follow.
package interp

import (
	"fmt"
	"io"
	"strings"

	"garurda/internal/domain"
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
	fmt.Fprintf(&b, "%s:%d:%d: %s", e.File, e.Pos.Line, e.Pos.Col, e.Msg)
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

// Frame captures the call stack while an error unwinds.
func (e *Error) Frame(f StackFrame) *Error {
	e.Stack = append(e.Stack, f)
	return e
}

// Thrown reports whether the error was raised by throw and is catchable.
func (e *Error) Thrown() bool { return e.Value != nil }

// env is a lexical scope.
type scope struct {
	vars   map[string]domain.Value
	types  map[string]*domain.TypeExpr
	parent *scope
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent}
}

// lookup resolves a name through the scope chain. A scope with no variables
// holds no map at all, which keeps loop-body scopes allocation free.
func (e *scope) lookup(name string) (domain.Value, *domain.TypeExpr, bool) {
	for s := e; s != nil; s = s.parent {
		if s.vars == nil {
			continue
		}
		if v, ok := s.vars[name]; ok {
			return v, s.types[name], true
		}
	}
	return nil, nil, false
}

func (e *scope) assign(name string, v domain.Value) {
	for s := e; s != nil; s = s.parent {
		if s.vars == nil {
			continue
		}
		if _, ok := s.vars[name]; ok {
			s.vars[name] = v
			return
		}
	}
	e.define(name, v, nil)
}

func (e *scope) define(name string, v domain.Value, t *domain.TypeExpr) {
	if e.vars == nil {
		e.vars = make(map[string]domain.Value, 4)
	}
	e.vars[name] = v
	if t != nil {
		if e.types == nil {
			e.types = make(map[string]*domain.TypeExpr, 4)
		}
		e.types[name] = t
	}
}

// Interp evaluates Garurda programs.
type Interp struct {
	// Out receives print/println output.
	Out io.Writer
	// Err receives runtime diagnostics.
	Err io.Writer

	global *scope
	// calls tracks the active call stack for error traces.
	calls []StackFrame
	// depth guards against runaway recursion.
	depth    int
	maxDepth int
	// Modules are the builtin module factories, e.g. "strings" and "math".
	Modules map[string]func() *domain.Obj
	// File is the name used in diagnostics for the program being run.
	File string
}

// New creates an interpreter writing to out.
func New(out, errOut io.Writer) *Interp {
	in := &Interp{
		Out:      out,
		Err:      errOut,
		global:   newScope(nil),
		maxDepth: 2000,
		File:     "<stdin>",
	}
	in.installGlobals()
	return in
}

// Global returns the top-level scope.
func (in *Interp) Global() *scope { return in.global }

func (in *Interp) errf(pos domain.Position, format string, args ...interface{}) *Error {
	return &Error{Msg: fmt.Sprintf(format, args...), Pos: pos, File: in.File, Stack: in.snapshot()}
}

// Throw builds a catchable error value.
func (in *Interp) Throw(msg string, code string, status int, pos domain.Position) *Error {
	ev := &domain.ErrorValue{Message: msg, Code: code, Status: status}
	return &Error{Msg: msg, Pos: pos, File: in.File, Value: ev, Stack: in.snapshot()}
}

func (in *Interp) snapshot() []StackFrame {
	out := make([]StackFrame, len(in.calls))
	copy(out, in.calls)
	return out
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

// CompileFile parses a file into a program.
func (in *Interp) CompileFile(path string) (*parse.Program, error) {
	src, err := readFile(path)
	if err != nil {
		return nil, err
	}
	prog, err := parse.Parse(src, path)
	if err != nil {
		return nil, err
	}
	return prog, nil
}

// Eval parses and runs source text in the global scope, returning the value of
// the last evaluated statement, if any.
func (in *Interp) Eval(src, file string) (domain.Value, error) {
	prog, err := parse.Parse(src, file)
	if err != nil {
		return nil, err
	}
	return in.Run(prog)
}

// EvalDiscard runs source text and ignores the resulting value.
func (in *Interp) EvalDiscard(src, file string) error {
	_, err := in.Eval(src, file)
	return err
}

// Run executes statements, returning the value of the last expression
// statement, if any. It is used by the REPL.
func (in *Interp) Run(prog *parse.Program) (domain.Value, error) {
	prev := in.File
	in.File = prog.File
	defer func() { in.File = prev }()
	_, last, err := in.execBlockStmts(prog.Stmts, in.global)
	return last, err
}

// control is the reason a statement list finished early.
type control int

const (
	ctrlNone control = iota
	ctrlBreak
	ctrlContinue
	ctrlReturn
)

// execBlockStmts runs statements, returning how the block ended.
func (in *Interp) execBlockStmts(stmts []domain.Stmt, e *scope) (control, domain.Value, error) {
	var last domain.Value
	for _, st := range stmts {
		c, v, err := in.execStmt(st, e)
		if err != nil {
			return ctrlNone, nil, err
		}
		if c != ctrlNone {
			return c, v, nil
		}
		if v != nil {
			last = v
		}
	}
	return ctrlNone, last, nil
}

func (in *Interp) execBlock(b *domain.Block, e *scope) (control, domain.Value, error) {
	if b == nil {
		return ctrlNone, nil, nil
	}
	return in.execBlockStmts(b.Stmts, newScope(e))
}
