// Package parse builds a Garurda syntax tree from a token stream.
package parse

import (
	"fmt"

	"garurda/internal/domain"
	"garurda/internal/usecase/lex"
)

// Error is a parse error with a source position.
type Error struct {
	Msg  string
	Line int
	Col  int
	File string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Col, e.Msg)
}

// Program is a parsed source file.
type Program struct {
	Stmts []domain.Stmt
	File  string
}

// Parser is a recursive descent parser with precedence climbing for
// expressions.
type Parser struct {
	toks []domain.Token
	i    int
	file string
}

// Parse tokenises and parses src into a Program.
func Parse(src, file string) (*Program, error) {
	toks, err := lex.Tokenize(src, file)
	if err != nil {
		return nil, err
	}
	p := &Parser{toks: toks, file: file}
	prog := &Program{File: file}
	for {
		p.skipSeparators()
		if p.at(domain.TokenEOF) {
			break
		}
		st, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		prog.Stmts = append(prog.Stmts, st)
		// Optional statement terminator.
		if p.at(domain.TokenSemi) {
			p.next()
		}
	}
	return prog, nil
}

// ---- token helpers ----

func (p *Parser) cur() domain.Token { return p.toks[p.i] }

func (p *Parser) at(t domain.TokenType) bool { return p.toks[p.i].Type == t }

func (p *Parser) peekIs(n int, t domain.TokenType) bool {
	j := p.i + n
	if j >= len(p.toks) {
		return false
	}
	return p.toks[j].Type == t
}

func (p *Parser) next() domain.Token {
	t := p.toks[p.i]
	if t.Type != domain.TokenEOF {
		p.i++
	}
	return t
}

func (p *Parser) errf(t domain.Token, format string, args ...interface{}) error {
	return &Error{Msg: fmt.Sprintf(format, args...), Line: t.Line, Col: t.Col, File: p.file}
}

func (p *Parser) errAt(format string, args ...interface{}) error {
	return p.errf(p.cur(), format, args...)
}

func (p *Parser) expect(t domain.TokenType, what string) (domain.Token, error) {
	if p.at(t) {
		return p.next(), nil
	}
	return domain.Token{}, p.errAt("expected %s, found %s", what, p.cur().String())
}

// skipSeparators consumes newlines and stray semicolons.
func (p *Parser) skipSeparators() {
	for p.at(domain.TokenNewline) || p.at(domain.TokenSemi) {
		p.next()
	}
}

// skipNewlines consumes newlines only, used where a statement may continue.
func (p *Parser) skipNewlines() {
	for p.at(domain.TokenNewline) {
		p.next()
	}
}

func (p *Parser) isKeyword(kw string) bool {
	t := p.cur()
	return t.Type == domain.TokenKeyword && t.Text == kw
}

func (p *Parser) expectKeyword(kw string) (domain.Token, error) {
	if p.isKeyword(kw) {
		return p.next(), nil
	}
	return domain.Token{}, p.errAt("expected '%s', found %s", kw, p.cur().String())
}

// ---- statements ----

func (p *Parser) parseStmt() (domain.Stmt, error) {
	t := p.cur()

	if t.Type == domain.TokenKeyword {
		switch t.Text {
		case "fn":
			return p.parseFnStmt()
		case "if":
			return p.parseIf()
		case "while":
			return p.parseWhile()
		case "for":
			return p.parseFor()
		case "print":
			return p.parsePrint(false)
		case "println":
			return p.parsePrint(true)
		case "return":
			p.next()
			if p.atNewlineOrEnd() {
				return &domain.ReturnStmt{P: pos(t)}, nil
			}
			x, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			return &domain.ReturnStmt{Value: x, P: pos(t)}, nil
		case "break":
			p.next()
			return &domain.BreakStmt{P: pos(t)}, nil
		case "continue":
			p.next()
			return &domain.ContinueStmt{P: pos(t)}, nil
		case "throw":
			return p.parseThrow()
		case "try":
			return p.parseTry()
		case "async":
			return p.parseAsyncStmt()
		case "use":
			return p.parseUse()
		case "var":
			p.next()
			return p.parseVarDecl(nil, pos(t))
		}
	}

	// Typed declaration: int $n = 72, ?string $s = null, array<int> $ids = []
	// A type name followed by `(` is a builtin constructor call, not a
	// declaration: object(x), error("msg").
	nextIsLParen := p.peekIs(1, domain.TokenLParen)
	if t.Type == domain.TokenKeyword && domain.IsTypeName(t.Text) && !nextIsLParen {
		return p.parseTypedDecl()
	}
	if t.Type == domain.TokenQuestion && p.i+1 < len(p.toks) &&
		domain.IsTypeName(p.toks[p.i+1].Text) && !p.peekIs(2, domain.TokenLParen) {
		return p.parseTypedDecl()
	}

	// `print`/`println` may also appear without a keyword prefix? No.

	x, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	switch p.cur().Type {
	case domain.TokenAssign, domain.TokenPlusEq, domain.TokenMinusEq, domain.TokenStarEq, domain.TokenSlashEq:
		op := p.next()
		p.skipNewlines()
		rhs, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &domain.AssignStmt{Target: x, Op: op.Type, Value: rhs, P: pos(t)}, nil
	}
	return &domain.ExprStmt{X: x, P: pos(t)}, nil
}

// parseTypedDecl parses `int $n = 1` and `?string $s`.
func (p *Parser) parseTypedDecl() (domain.Stmt, error) {
	start := p.cur()
	te, err := p.parseTypeExpr()
	if err != nil {
		return nil, err
	}
	name, err := p.parseIdentName()
	if err != nil {
		return nil, err
	}
	var val domain.Expr
	if p.at(domain.TokenAssign) {
		p.next()
		p.skipNewlines()
		val, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}
	return &domain.VarDecl{Name: name, Type: te, Value: val, P: pos(start)}, nil
}

// parseVarDecl parses `var a = 1, b = 2` after the optional `var` keyword.
// Multiple declarators are returned as a block of declarations.
func (p *Parser) parseVarDecl(te *domain.TypeExpr, p0 domain.Position) (domain.Stmt, error) {
	if p.at(domain.TokenEOF) || p.atNewlineOrEnd() {
		return &domain.Block{}, nil
	}
	blk := &domain.Block{P: p0}
	for {
		name, err := p.parseIdentName()
		if err != nil {
			return nil, err
		}
		d := &domain.VarDecl{Name: name, Type: te, P: p0}
		if p.at(domain.TokenAssign) {
			p.next()
			p.skipNewlines()
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			d.Value = val
		}
		blk.Stmts = append(blk.Stmts, d)
		if p.at(domain.TokenComma) {
			p.next()
			p.skipNewlines()
			continue
		}
		break
	}
	if len(blk.Stmts) == 1 {
		return blk.Stmts[0], nil
	}
	return blk, nil
}

func (p *Parser) parseIdentName() (string, error) {
	t := p.cur()
	if t.Type != domain.TokenIdent {
		return "", p.errAt("expected variable name, found %s", t.String())
	}
	p.next()
	return t.Text, nil
}

// parseTypeExpr parses `int`, `?string`, `array<int>`, `?array<string>`.
func (p *Parser) parseTypeExpr() (*domain.TypeExpr, error) {
	start := p.cur()
	nullable := false
	if p.at(domain.TokenQuestion) {
		p.next()
		nullable = true
	}
	t := p.cur()
	if t.Type != domain.TokenKeyword || !domain.IsTypeName(t.Text) {
		return nil, p.errAt("expected type name, found %s", t.String())
	}
	p.next()
	te := &domain.TypeExpr{Name: t.Text, Nullable: nullable, P: pos(t)}
	if p.at(domain.TokenLt) {
		p.next()
		for {
			arg, err := p.parseTypeExpr()
			if err != nil {
				return nil, err
			}
			te.Args = append(te.Args, arg)
			if p.at(domain.TokenComma) {
				p.next()
				continue
			}
			break
		}
		if _, err := p.expect(domain.TokenGt, "'>'"); err != nil {
			return nil, err
		}
	}
	_ = start
	return te, nil
}

func (p *Parser) parsePrint(newline bool) (domain.Stmt, error) {
	t := p.next() // print / println
	st := &domain.PrintStmt{Newline: newline, P: pos(t)}
	p.skipNewlines()
	// A call-style list `println(a, b)` is accepted for familiarity. The
	// opening paren is only treated that way when a comma follows the first
	// argument; otherwise it rewinds so `print (2 + 3) * 4` still reads the
	// paren as part of the expression.
	if p.at(domain.TokenLParen) {
		save := p.i
		p.next() // (
		p.skipNewlines()
		if p.at(domain.TokenRParen) {
			p.next()
			return st, nil // println()
		}
		first, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipNewlines()
		if p.at(domain.TokenComma) {
			st.Values = append(st.Values, first)
			for p.at(domain.TokenComma) {
				p.next()
				p.skipNewlines()
				x, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				st.Values = append(st.Values, x)
				p.skipNewlines()
			}
			if _, err := p.expect(domain.TokenRParen, "')'"); err != nil {
				return nil, err
			}
			return st, nil
		}
		p.i = save
	}
	for {
		p.skipNewlines()
		if p.atNewlineOrEnd() || p.at(domain.TokenSemi) {
			break
		}
		x, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		st.Values = append(st.Values, x)
		if p.at(domain.TokenComma) {
			p.next()
			continue
		}
		break
	}
	return st, nil
}

func (p *Parser) parseIf() (domain.Stmt, error) {
	t := p.next() // if / else
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	then, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	st := &domain.IfStmt{Cond: cond, Then: then, P: pos(t)}
	// Look ahead for else / else if.
	save := p.i
	p.skipSeparators()
	if p.isKeyword("else") {
		p.next()
		p.skipNewlines()
		if p.isKeyword("if") {
			els, err := p.parseIf()
			if err != nil {
				return nil, err
			}
			st.Else = els
			return st, nil
		}
		blk, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		st.Else = blk
		return st, nil
	}
	p.i = save
	return st, nil
}

func (p *Parser) parseWhile() (domain.Stmt, error) {
	t := p.next()
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &domain.WhileStmt{Cond: cond, Body: body, P: pos(t)}, nil
}

// parseFor handles `for x in src`, `for k, v in obj` and `for x in 0..10`.
// parseFor handles `for x in src`, `for k, v in obj` and `for x in 0..10`.
func (p *Parser) parseFor() (domain.Stmt, error) {
	t := p.next() // for
	varName, err := p.parseIdentName()
	if err != nil {
		return nil, err
	}
	var2 := ""
	if p.at(domain.TokenComma) {
		p.next()
		var2, err = p.parseIdentName()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expectKeyword("in"); err != nil {
		return nil, err
	}
	spec, err := p.parseForSpec()
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &domain.ForInStmt{Var: varName, Var2: var2, Spec: spec, Body: body, P: pos(t)}, nil
}

func (p *Parser) parseForSpec() (domain.ForSpec, error) {
	var spec domain.ForSpec
	first, err := p.parseExpr()
	if err != nil {
		return spec, err
	}
	if rng, ok := first.(*domain.RangeExpr); ok {
		spec.IsRange = true
		spec.Low, spec.High, spec.Step = rng.Low, rng.High, rng.Step
		return spec, nil
	}
	spec.Src = first
	return spec, nil
}

func (p *Parser) parseThrow() (domain.Stmt, error) {
	t := p.next()
	x, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &domain.ThrowStmt{Value: x, P: pos(t)}, nil
}

func (p *Parser) parseTry() (domain.Stmt, error) {
	t := p.next()
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	st := &domain.TryStmt{Body: body, P: pos(t)}
	// catch is optional; a bare try simply propagates errors.
	save := p.i
	p.skipSeparators()
	if p.isKeyword("catch") {
		p.next()
		if p.at(domain.TokenIdent) {
			st.Var = p.next().Text
			st.HasVar = true
		}
		blk, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		st.Catch = blk
	} else {
		p.i = save
	}
	// optional finally clause
	save2 := p.i
	p.skipSeparators()
	if p.isKeyword("finally") {
		p.next()
		blk, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		st.Finally = blk
	} else {
		p.i = save2
	}
	return st, nil
}

func (p *Parser) parseUse() (domain.Stmt, error) {
	t := p.next()
	name, err := p.parseStringLiteral()
	if err != nil {
		return nil, err
	}
	return &domain.UseStmt{Path: name, P: pos(t)}, nil
}

// parseStringLiteral reads a plain string literal without interpolation.
func (p *Parser) parseStringLiteral() (string, error) {
	t, err := p.expect(domain.TokenStringStart, "string")
	if err != nil {
		return "", err
	}
	_ = t
	if p.at(domain.TokenString) {
		s := p.next().Str
		if _, err := p.expect(domain.TokenStringEnd, "end of string"); err != nil {
			return "", err
		}
		return s, nil
	}
	if _, err := p.expect(domain.TokenStringEnd, "end of string"); err != nil {
		return "", err
	}
	return "", nil
}

func (p *Parser) parseBlock() (*domain.Block, error) {
	t, err := p.expect(domain.TokenLBrace, "'{'")
	if err != nil {
		return nil, err
	}
	blk := &domain.Block{P: pos(t)}
	for {
		p.skipSeparators()
		if p.at(domain.TokenRBrace) {
			p.next()
			return blk, nil
		}
		if p.at(domain.TokenEOF) {
			return nil, p.errAt("unexpected end of file, missing '}'")
		}
		st, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		blk.Stmts = append(blk.Stmts, st)
		if p.at(domain.TokenSemi) {
			p.next()
		}
	}
}

func (p *Parser) parseFnStmt() (domain.Stmt, error) {
	start := p.cur()
	save := p.i
	p.next() // fn
	if p.at(domain.TokenIdent) {
		name := p.next().Text
		fn, err := p.parseFnRest(name, pos(start))
		if err != nil {
			return nil, err
		}
		return &domain.FnDecl{Fn: fn, P: pos(start)}, nil
	}
	// Anonymous function used as an expression statement.
	p.i = save
	x, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &domain.ExprStmt{X: x, P: pos(start)}, nil
}

// parseAsyncStmt parses `async fn name(...) { ... }` and the anonymous form
// `async fn(...) { ... }` used as an expression statement.
func (p *Parser) parseAsyncStmt() (domain.Stmt, error) {
	start := p.cur() // async
	save := p.i
	p.next() // async
	if p.at(domain.TokenKeyword) && p.cur().Text == "fn" {
		p.next() // fn
		if p.at(domain.TokenIdent) {
			name := p.next().Text
			fn, err := p.parseFnRest(name, pos(start))
			if err != nil {
				return nil, err
			}
			fn.Async = true
			return &domain.FnDecl{Fn: fn, P: pos(start)}, nil
		}
	}
	// Anonymous async function (or a plain expression that happens to start
	// with `async fn`): reparse from `async` as an expression.
	p.i = save
	x, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &domain.ExprStmt{X: x, P: pos(start)}, nil
}

func (p *Parser) parseFnRest(name string, p0 domain.Position) (*domain.FnExpr, error) {
	fn := &domain.FnExpr{Name: name, P: p0}
	params, err := p.parseParams()
	if err != nil {
		return nil, err
	}
	fn.Params = params
	// Optional return type annotation: `fn f() int { ... }`. It is checked
	// against the returned value but carries no runtime cost.
	p.skipNewlines()
	if (p.at(domain.TokenKeyword) && domain.IsTypeName(p.cur().Text)) || p.at(domain.TokenQuestion) {
		te, err := p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
		fn.Ret = te
	}
	p.skipNewlines()
	if p.at(domain.TokenArrow) {
		p.next()
		p.skipNewlines()
		body, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		fn.Arrow = true
		fn.Body = &domain.Block{Stmts: []domain.Stmt{&domain.ReturnStmt{Value: body, P: bodyPos(body)}}}
		return fn, nil
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	fn.Body = body
	return fn, nil
}

func bodyPos(e domain.Expr) domain.Position {
	if e == nil {
		return domain.Position{}
	}
	return e.Pos()
}

func (p *Parser) parseParams() ([]domain.Param, error) {
	if _, err := p.expect(domain.TokenLParen, "'('"); err != nil {
		return nil, err
	}
	var params []domain.Param
	p.skipNewlines()
	for !p.at(domain.TokenRParen) {
		if p.at(domain.TokenEOF) {
			return nil, p.errAt("unexpected end of file in parameter list")
		}
		var prm domain.Param
		// Optional type annotation.
		if (p.at(domain.TokenKeyword) && domain.IsTypeName(p.cur().Text)) || p.at(domain.TokenQuestion) {
			te, err := p.parseTypeExpr()
			if err != nil {
				return nil, err
			}
			prm.Type = te
		}
		name, err := p.parseIdentName()
		if err != nil {
			return nil, err
		}
		prm.Name = name
		if p.at(domain.TokenAssign) {
			p.next()
			p.skipNewlines()
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			prm.Default = val
		}
		params = append(params, prm)
		if p.at(domain.TokenComma) {
			p.next()
			p.skipNewlines()
			continue
		}
		break
	}
	p.skipNewlines()
	if _, err := p.expect(domain.TokenRParen, "')'"); err != nil {
		return nil, err
	}
	return params, nil
}

func (p *Parser) atNewlineOrEnd() bool {
	return p.at(domain.TokenNewline) || p.at(domain.TokenEOF) || p.at(domain.TokenSemi) ||
		p.at(domain.TokenRBrace)
}

func pos(t domain.Token) domain.Position {
	return domain.Position{Line: t.Line, Col: t.Col}
}
