package interp

import (
	"fmt"
	"os"
	"strings"

	"garurda/internal/domain"
)

// ---- statement execution ----

func (in *Interp) execStmt(st domain.Stmt, e *scope) (control, domain.Value, error) {
	switch s := st.(type) {
	case *domain.VarDecl:
		return ctrlNone, nil, in.execVarDecl(s, e)

	case *domain.AssignStmt:
		return ctrlNone, nil, in.execAssign(s, e)

	case *domain.ExprStmt:
		v, err := in.eval(s.X, e)
		return ctrlNone, v, err

	case *domain.PrintStmt:
		return ctrlNone, nil, in.execPrint(s, e)

	case *domain.ReturnStmt:
		if s.Value == nil {
			return ctrlReturn, domain.Null{}, nil
		}
		v, err := in.eval(s.Value, e)
		if err != nil {
			return ctrlNone, nil, err
		}
		return ctrlReturn, v, nil

	case *domain.IfStmt:
		return in.execIf(s, e)

	case *domain.WhileStmt:
		return in.execWhile(s, e)

	case *domain.ForInStmt:
		return in.execForIn(s, e)

	case *domain.FnDecl:
		fn := &Fn{Name: s.Fn.Name, Params: s.Fn.Params, Body: s.Fn.Body, Ret: s.Fn.Ret, Elem: retElemType(s.Fn), Env: e}
		e.define(s.Fn.Name, fn, &domain.TypeExpr{Name: "function", Nullable: true})
		return ctrlNone, nil, nil

	case *domain.BreakStmt:
		return ctrlBreak, nil, nil

	case *domain.ContinueStmt:
		return ctrlContinue, nil, nil

	case *domain.ThrowStmt:
		return ctrlNone, nil, in.execThrow(s, e)

	case *domain.TryStmt:
		return in.execTry(s, e)

	case *domain.UseStmt:
		return ctrlNone, nil, in.execUse(s, e)

	case *domain.Block:
		inner := newScope(e)
		c, v, err := in.execBlockStmts(s.Stmts, inner)
		return c, v, err
	}
	return ctrlNone, nil, in.errf(st.Pos(), "unsupported statement %T", st)
}

// retElemType reads the element type from an `array<int>` return annotation,
// so that `fn ids() array<int>` produces a typed array.
func retElemType(fn *domain.FnExpr) *domain.TypeExpr {
	if fn.Ret == nil {
		return nil
	}
	return elemOf(fn.Ret)
}

func (in *Interp) execVarDecl(s *domain.VarDecl, e *scope) error {
	var v domain.Value = domain.Null{}
	if s.Value != nil {
		val, err := in.eval(s.Value, e)
		if err != nil {
			return err
		}
		v = val
	} else if s.Type != nil {
		v = zeroValue(s.Type)
	}
	if s.Type != nil {
		if err := in.checkType(s.Type, v, s.P); err != nil {
			return err
		}
	}
	e.define(s.Name, v, s.Type)
	return nil
}

func (in *Interp) execAssign(s *domain.AssignStmt, e *scope) error {
	val, err := in.eval(s.Value, e)
	if err != nil {
		return err
	}
	switch s.Op {
	case domain.TokenAssign, domain.TokenPlusEq, domain.TokenMinusEq, domain.TokenStarEq, domain.TokenSlashEq:
	default:
		return in.errf(s.P, "unsupported assignment operator %q", string(s.Op))
	}
	if s.Op != domain.TokenAssign {
		cur, err := in.eval(s.Target, e)
		if err != nil {
			return err
		}
		val, err = in.binaryOp(cur, s.Op, val, s.P)
		if err != nil {
			return err
		}
	}

	switch tgt := s.Target.(type) {
	case *domain.Ident:
		_, declared, ok := e.lookup(tgt.Name)
		if !ok {
			// `$x = 1` declares a dynamically typed variable. A declared type
			// still guards every later assignment.
			e.define(tgt.Name, val, nil)
			return nil
		}
		if declared != nil {
			if err := in.checkType(declared, val, s.P); err != nil {
				return err
			}
		}
		e.assign(tgt.Name, val)
		return nil

	case *domain.IndexExpr:
		recv, err := in.eval(tgt.Left, e)
		if err != nil {
			return err
		}
		idx, err := in.eval(tgt.Index, e)
		if err != nil {
			return err
		}
		return in.setIndex(recv, idx, val, s.P)

	case *domain.PropExpr:
		recv, err := in.eval(tgt.Left, e)
		if err != nil {
			return err
		}
		return in.setProp(recv, tgt.Name, val, s.P)
	}
	return in.errf(s.P, "invalid assignment target")
}

func (in *Interp) setIndex(recv, idx, val domain.Value, p domain.Position) error {
	switch r := recv.(type) {
	case *domain.Arr:
		i, ok := domain.AsInt(idx)
		if !ok {
			return in.errf(p, "array index must be a number, got %s", domain.TypeName(idx))
		}
		n := int(i)
		if n < 0 {
			n += len(r.Items)
		}
		if n < 0 || n >= len(r.Items) {
			return in.errf(p, "index %d out of range (array has %d items)", i, len(r.Items))
		}
		if r.Elem != nil {
			if err := in.checkType(r.Elem, val, p); err != nil {
				return err
			}
		}
		r.Set(n, val)
		return nil
	case *domain.Obj:
		key, ok := indexKey(idx)
		if !ok {
			return in.errf(p, "object key must be a string, got %s", domain.TypeName(idx))
		}
		r.Set(key, val)
		return nil
	}
	return in.errf(p, "cannot index %s for assignment", domain.TypeName(recv))
}

func (in *Interp) setProp(recv domain.Value, name string, val domain.Value, p domain.Position) error {
	switch r := recv.(type) {
	case *domain.Obj:
		r.Set(name, val)
		return nil
	case *domain.Arr:
		// `arr.push(x)` is spelled as a builtin call, but allow arr.field
		// assignment for builtin objects only.
	}
	return in.errf(p, "cannot set property '%s' on %s", name, domain.TypeName(recv))
}

func (in *Interp) execPrint(s *domain.PrintStmt, e *scope) error {
	parts := make([]string, 0, len(s.Values))
	for _, ex := range s.Values {
		v, err := in.eval(ex, e)
		if err != nil {
			return err
		}
		parts = append(parts, v.String())
	}
	// print and println both terminate the line: values are joined with a space,
	// then a newline is written. The two names are kept separate because
	// `println` reads better when the intent is "log this line".
	line := strings.Join(parts, " ")
	return in.printLine(line)
}

// printLine writes one line plus a newline.
func (in *Interp) printLine(text string) error {
	_, err := io_WriteString(in.Out, text+"\n")
	return err
}

func (in *Interp) execIf(s *domain.IfStmt, e *scope) (control, domain.Value, error) {
	cond, err := in.eval(s.Cond, e)
	if err != nil {
		return ctrlNone, nil, err
	}
	if domain.Truthy(cond) {
		return in.execBlock(s.Then, e)
	}
	if s.Else != nil {
		return in.execStmt(s.Else, e)
	}
	return ctrlNone, nil, nil
}

func (in *Interp) execWhile(s *domain.WhileStmt, e *scope) (control, domain.Value, error) {
	for {
		cond, err := in.eval(s.Cond, e)
		if err != nil {
			return ctrlNone, nil, err
		}
		if !domain.Truthy(cond) {
			return ctrlNone, nil, nil
		}
		c, _, err := in.execBlock(s.Body, e)
		if err != nil {
			return ctrlNone, nil, err
		}
		if c == ctrlBreak {
			return ctrlNone, nil, nil
		}
	}
}

func (in *Interp) execForIn(s *domain.ForInStmt, e *scope) (control, domain.Value, error) {
	if s.Spec.IsRange {
		lo, err := in.eval(s.Spec.Low, e)
		if err != nil {
			return ctrlNone, nil, err
		}
		hi, err := in.eval(s.Spec.High, e)
		if err != nil {
			return ctrlNone, nil, err
		}
		start, ok1 := domain.AsInt(lo)
		end, ok2 := domain.AsInt(hi)
		if !ok1 || !ok2 {
			return ctrlNone, nil, in.errf(s.P, "range bounds must be numbers")
		}
		// The default step follows the direction: `for i in 3..1` counts down.
		step := int64(1)
		if start > end {
			step = -1
		}
		if s.Spec.Step != nil {
			sv, err := in.eval(s.Spec.Step, e)
			if err != nil {
				return ctrlNone, nil, err
			}
			si, ok := domain.AsInt(sv)
			if !ok {
				return ctrlNone, nil, in.errf(s.P, "range step must be a number")
			}
			if si == 0 {
				return ctrlNone, nil, in.errf(s.P, "range step cannot be zero")
			}
			step = si
		}
		// One scope for the whole loop: the loop variable is rebound each
		// iteration instead of allocating a fresh scope. Like Go, a closure
		// created inside the body observes the final value.
		loopScope := newScope(e)
		for i := start; ; i += step {
			if step > 0 && i > end {
				break
			}
			if step < 0 && i < end {
				break
			}
			loopScope.define(s.Var, intValue(i), nil)
			c, _, err := in.execBlockStmts(s.Body.Stmts, loopScope)
			if err != nil {
				return ctrlNone, nil, err
			}
			if c == ctrlBreak {
				return ctrlNone, nil, nil
			}
		}
		return ctrlNone, nil, nil
	}

	src, err := in.eval(s.Spec.Src, e)
	if err != nil {
		return ctrlNone, nil, err
	}
	switch coll := src.(type) {
	case *domain.Arr:
		for i := 0; i < len(coll.Items); i++ {
			scope := newScope(e)
			scope.define(s.Var, coll.Items[i], nil)
			if s.Var2 != "" {
				scope.define(s.Var2, domain.Int(i), nil)
			}
			c, _, err := in.execBlockStmts(s.Body.Stmts, scope)
			if err != nil {
				return ctrlNone, nil, err
			}
			if c == ctrlBreak {
				return ctrlNone, nil, nil
			}
		}
	case *domain.Obj:
		for _, k := range coll.Keys() {
			v, _ := coll.Get(k)
			scope := newScope(e)
			scope.define(s.Var, domain.Str(k), nil)
			if s.Var2 != "" {
				scope.define(s.Var2, v, nil)
			}
			c, _, err := in.execBlockStmts(s.Body.Stmts, scope)
			if err != nil {
				return ctrlNone, nil, err
			}
			if c == ctrlBreak {
				return ctrlNone, nil, nil
			}
		}
	case domain.Str:
		for _, r := range string(coll) {
			scope := newScope(e)
			scope.define(s.Var, domain.Str(string(r)), nil)
			c, _, err := in.execBlockStmts(s.Body.Stmts, scope)
			if err != nil {
				return ctrlNone, nil, err
			}
			if c == ctrlBreak {
				return ctrlNone, nil, nil
			}
		}
	default:
		return ctrlNone, nil, in.errf(s.P, "cannot iterate over %s", domain.TypeName(src))
	}
	return ctrlNone, nil, nil
}

func (in *Interp) execThrow(s *domain.ThrowStmt, e *scope) error {
	v, err := in.eval(s.Value, e)
	if err != nil {
		return err
	}
	switch t := v.(type) {
	case *domain.ErrorValue:
		return in.wrapThrown(t, s.P)
	case domain.Str:
		return in.Throw(string(t), "error", 500, s.P)
	}
	return in.errf(s.P, "throw expects a string or error, got %s", domain.TypeName(v))
}

// wrapThrown converts an error value into a catchable runtime error.
func (in *Interp) wrapThrown(ev *domain.ErrorValue, p domain.Position) error {
	return &Error{Msg: ev.Message, Pos: p, File: in.File, Value: ev, Stack: in.snapshot()}
}

func (in *Interp) execTry(s *domain.TryStmt, e *scope) (control, domain.Value, error) {
	c, v, err := in.execBlock(s.Body, e)
	if err == nil {
		return c, v, nil
	}
	if s.Catch == nil {
		return ctrlNone, nil, err
	}
	re, ok := err.(*Error)
	if !ok || !re.Thrown() {
		// Internal interpreter errors are not catchable by user code.
		return ctrlNone, nil, err
	}
	scope := newScope(e)
	if s.HasVar {
		scope.define(s.Var, re.Value, nil)
	} else {
		scope.define("error", re.Value, nil)
	}
	return in.execBlockStmts(s.Catch.Stmts, scope)
}

func (in *Interp) execUse(s *domain.UseStmt, e *scope) error {
	factory, ok := in.Modules[s.Path]
	if !ok {
		return in.errf(s.P, "unknown module '%s'", s.Path)
	}
	e.define(s.Path, factory(), nil)
	return nil
}

// readFile is a tiny indirection so tests can stub file access.
var readFile = func(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// io_WriteString writes to w without pulling in extra helpers.
func io_WriteString(w interface{ Write([]byte) (int, error) }, s string) (int, error) {
	return w.Write([]byte(s))
}

var _ = fmt.Sprintf
