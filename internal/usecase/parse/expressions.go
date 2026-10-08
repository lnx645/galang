package parse

import (
	"garurda/internal/domain"
)

// Binary precedence. Higher binds tighter.
const (
	precLowest = iota + 1
	precOr
	precAnd
	precEquality
	precCompare
	precSum
	precProduct
	precUnary
	precRange
)

// infixPrec maps an operator token to its precedence, 0 when not infix.
func infixPrec(t domain.TokenType) int {
	switch t {
	case domain.TokenOr:
		return precOr
	case domain.TokenAnd:
		return precAnd
	case domain.TokenEq, domain.TokenNeq:
		return precEquality
	case domain.TokenIn:
		return precCompare
	case domain.TokenLt, domain.TokenLtEq, domain.TokenGt, domain.TokenGtEq:
		return precCompare
	case domain.TokenPlus, domain.TokenMinus:
		return precSum
	case domain.TokenStar, domain.TokenSlash, domain.TokenPercent:
		return precProduct
	case domain.TokenRange:
		return precRange
	}
	return 0
}

// prefixOp reports whether t starts a unary operator and the minimum
// precedence its operand is parsed with.
//
// `not` binds looser than the comparison operators, so `not a == b` reads as
// `not (a == b)`. `-` binds tightest, so `-a * b` is `(-a) * b`. `await`
// binds like a prefix: `await f() + 1` is `(await f()) + 1`.
func prefixOp(t domain.TokenType) (int, bool) {
	switch t {
	case domain.TokenMinus:
		return precUnary, true
	case domain.TokenNot:
		return precAnd, true
	case domain.TokenAwait:
		return precUnary, true
	}
	return 0, false
}

// parseExpr parses an expression with the given minimum precedence.
func (p *Parser) parseExpr() (domain.Expr, error) {
	return p.parseExprPrec(precLowest)
}

func (p *Parser) parseExprPrec(min int) (domain.Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}

	for {
		t := p.cur()
		prec := infixPrec(t.Type)
		// `in` is a keyword (the for head needs it) and also an infix
		// membership test: `if 2 in [1, 2]`.
		if t.Type == domain.TokenKeyword && t.Text == "in" {
			prec = precCompare
			t = domain.Token{Type: domain.TokenIn, Text: "in", Line: t.Line, Col: t.Col}
		}
		if prec == 0 || prec < min {
			break
		}
		p.next()
		p.skipNewlines()
		if t.Type == domain.TokenRange {
			// Parse the high bound just above range precedence so that
			// `0..10..5` leaves the second `..` for the step below.
			high, err := p.parseExprPrec(precRange + 1)
			if err != nil {
				return nil, err
			}
			var step domain.Expr
			if p.at(domain.TokenRange) {
				p.next()
				step, err = p.parseExprPrec(precRange)
				if err != nil {
					return nil, err
				}
			}
			left = &domain.RangeExpr{Low: left, High: high, Step: step, P: pos(t)}
			continue
		}
		right, err := p.parseExprPrec(prec + 1)
		if err != nil {
			return nil, err
		}
		left = &domain.InfixExpr{Op: t.Type, Left: left, Right: right, P: pos(t)}
	}

	// Ternary: cond ? a : b
	if min <= precOr && p.at(domain.TokenQuestion) {
		t := p.next()
		p.skipNewlines()
		conseq, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(domain.TokenColon, "':'"); err != nil {
			return nil, err
		}
		p.skipNewlines()
		alt, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		left = &domain.TernaryExpr{Cond: left, Conseq: conseq, Alt: alt, P: pos(t)}
	}
	return left, nil
}

// parseUnary handles `not x` and `-x`.
func (p *Parser) parseUnary() (domain.Expr, error) {
	t := p.cur()
	if prec, ok := prefixOp(t.Type); ok {
		p.next()
		p.skipNewlines()
		right, err := p.parseExprPrec(prec)
		if err != nil {
			return nil, err
		}
		return &domain.PrefixExpr{Op: t.Type, Right: right, P: pos(t)}, nil
	}
	return p.parsePostfix()
}

// parsePostfix parses calls, indexing, slicing and property access.
func (p *Parser) parsePostfix() (domain.Expr, error) {
	x, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		switch t.Type {
		case domain.TokenLParen:
			p.next()
			args, err := p.parseArgs()
			if err != nil {
				return nil, err
			}
			if call, ok := x.(*domain.CallExpr); ok {
				// `f()(x)` and `s.trim(...)("x")` style chaining.
				call.Args = append(call.Args, args...)
				x = call
				continue
			}
			x = &domain.CallExpr{Callee: x, Args: args, P: pos(t)}
		case domain.TokenLBracket:
			x, err = p.parseIndexOrSlice(x)
			if err != nil {
				return nil, err
			}
		case domain.TokenDot:
			p.next()
			nameTok := p.cur()
			if nameTok.Type != domain.TokenIdent && nameTok.Type != domain.TokenKeyword {
				return nil, p.errAt("expected field name after '.', found %s", nameTok.String())
			}
			p.next()
			if p.at(domain.TokenLParen) {
				p.next()
				args, err := p.parseArgs()
				if err != nil {
					return nil, err
				}
				x = &domain.MethodExpr{Receiver: x, Name: nameTok.Text, Args: args, P: pos(t)}
				continue
			}
			x = &domain.PropExpr{Left: x, Name: nameTok.Text, P: pos(t)}
		default:
			return x, nil
		}
	}
}

func (p *Parser) parseArgs() ([]domain.Expr, error) {
	var args []domain.Expr
	p.skipNewlines()
	for !p.at(domain.TokenRParen) {
		if p.at(domain.TokenEOF) {
			return nil, p.errAt("unexpected end of file in argument list")
		}
		a, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		p.skipNewlines()
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
	return args, nil
}

// parseIndexOrSlice parses `x[i]`, `x[1:3]`, `x[:2]`, `x[1:]`.
func (p *Parser) parseIndexOrSlice(left domain.Expr) (domain.Expr, error) {
	t, err := p.expect(domain.TokenLBracket, "'['")
	if err != nil {
		return nil, err
	}
	p.skipNewlines()
	if p.at(domain.TokenColon) {
		return p.finishSlice(left, nil, pos(t))
	}
	idx, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipNewlines()
	if p.at(domain.TokenColon) {
		return p.finishSlice(left, idx, pos(t))
	}
	if _, err := p.expect(domain.TokenRBracket, "']'"); err != nil {
		return nil, err
	}
	return &domain.IndexExpr{Left: left, Index: idx, P: pos(t)}, nil
}

func (p *Parser) finishSlice(left domain.Expr, low domain.Expr, p0 domain.Position) (domain.Expr, error) {
	if _, err := p.expect(domain.TokenColon, "':'"); err != nil {
		return nil, err
	}
	sl := &domain.SliceExpr{Left: left, Low: low, HasLow: low != nil, P: p0}
	p.skipNewlines()
	if !p.at(domain.TokenRBracket) {
		hi, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		sl.Hi = hi
		sl.HasHigh = true
		p.skipNewlines()
	}
	if _, err := p.expect(domain.TokenRBracket, "']'"); err != nil {
		return nil, err
	}
	return sl, nil
}

// parsePrimary parses literals, identifiers and bracketed expressions.
func (p *Parser) parsePrimary() (domain.Expr, error) {
	t := p.cur()

	switch t.Type {
	case domain.TokenNumber:
		p.next()
		if t.IsInt {
			return &domain.IntLit{Value: int64(t.Number), P: pos(t)}, nil
		}
		return &domain.FloatLit{Value: t.Number, P: pos(t)}, nil

	case domain.TokenStringStart:
		return p.parseStringLiteralExpr()

	case domain.TokenIdent:
		p.next()
		return &domain.Ident{Name: t.Text, P: pos(t)}, nil

	case domain.TokenLParen:
		p.next()
		p.skipNewlines()
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipNewlines()
		if _, err := p.expect(domain.TokenRParen, "')'"); err != nil {
			return nil, err
		}
		return &domain.GroupExpr{Inner: inner, P: pos(t)}, nil

	case domain.TokenLBracket:
		return p.parseListOrComprehension()

	case domain.TokenLBrace:
		return p.parseObjectLiteral()
	}

	if t.Type == domain.TokenKeyword {
		switch t.Text {
		case "true":
			p.next()
			return &domain.BoolLit{Value: true, P: pos(t)}, nil
		case "false":
			p.next()
			return &domain.BoolLit{Value: false, P: pos(t)}, nil
		case "null":
			p.next()
			return &domain.NullLit{P: pos(t)}, nil
		case "fn":
			p.next()
			return p.parseFnRest("", pos(t))
		case "async":
			p.next()
			// `async fn(...)`: the fn keyword must follow.
			if p.at(domain.TokenKeyword) && p.cur().Text == "fn" {
				p.next()
				fn, err := p.parseFnRest("", pos(t))
				if err != nil {
					return nil, err
				}
				fn.Async = true
				return fn, nil
			}
			return nil, p.errAt("expected 'fn' after 'async'")
		}
		// Type names double as builtin constructors, so `error(...)` and
		// `object(...)` work despite being keywords.
		if domain.IsTypeName(t.Text) && p.peekIs(1, domain.TokenLParen) {
			p.next()
			return &domain.Ident{Name: t.Text, P: pos(t)}, nil
		}
	}

	return nil, p.errAt("unexpected %s in expression", t.String())
}

// parseStringLiteralExpr builds a string with optional ${} interpolation.
func (p *Parser) parseStringLiteralExpr() (domain.Expr, error) {
	start, err := p.expect(domain.TokenStringStart, "string")
	if err != nil {
		return nil, err
	}
	lit := &domain.StrLit{P: pos(start)}
	simple := true
	for {
		switch {
		case p.at(domain.TokenString):
			chunk := p.next()
			// Merge into the trailing literal part, but never into an
			// interpolated one.
			if n := len(lit.Parts); n > 0 && !lit.Parts[n-1].IsExpr {
				lit.Parts[n-1].Lit += chunk.Str
			} else {
				lit.Parts = append(lit.Parts, domain.StrPart{Lit: chunk.Str})
			}
		case p.at(domain.TokenInterpBeg):
			simple = false
			p.next()
			p.skipNewlines()
			inner, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			p.skipNewlines()
			if _, err := p.expect(domain.TokenInterpEnd, "'}'"); err != nil {
				return nil, err
			}
			lit.Parts = append(lit.Parts, domain.StrPart{Expr: inner, IsExpr: true})
		default:
			if _, err := p.expect(domain.TokenStringEnd, "end of string"); err != nil {
				return nil, err
			}
			if !simple {
				return lit, nil
			}
			// Plain literal: collapse to a single text part.
			if len(lit.Parts) == 0 {
				lit.Parts = []domain.StrPart{{Lit: ""}}
			}
			return lit, nil
		}
	}
}

// parseListOrComprehension parses `[1, 2]` and `[x * 2 for x in xs if x > 1]`.
func (p *Parser) parseListOrComprehension() (domain.Expr, error) {
	t, err := p.expect(domain.TokenLBracket, "'['")
	if err != nil {
		return nil, err
	}
	p.skipNewlines()
	if p.at(domain.TokenRBracket) {
		p.next()
		return &domain.ListLit{P: pos(t)}, nil
	}

	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipNewlines()

	// Comprehension form.
	if p.isKeyword("for") {
		p.next()
		comp := &domain.ListLit{CompElem: first, P: pos(t)}
		comp.CompVar, err = p.parseIdentName()
		if err != nil {
			return nil, err
		}
		if p.at(domain.TokenComma) {
			p.next()
			comp.CompIdx, err = p.parseIdentName()
			if err != nil {
				return nil, err
			}
		}
		if _, err := p.expectKeyword("in"); err != nil {
			return nil, err
		}
		comp.CompSrc, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipNewlines()
		if p.isKeyword("if") {
			p.next()
			comp.CompCond, err = p.parseExpr()
			if err != nil {
				return nil, err
			}
			p.skipNewlines()
		}
		if _, err := p.expect(domain.TokenRBracket, "']'"); err != nil {
			return nil, err
		}
		return comp, nil
	}

	lit := &domain.ListLit{P: pos(t), Elems: []domain.Expr{first}}
	for {
		if p.at(domain.TokenComma) {
			p.next()
			p.skipNewlines()
			if p.at(domain.TokenRBracket) {
				break
			}
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			lit.Elems = append(lit.Elems, e)
			p.skipNewlines()
			continue
		}
		break
	}
	if _, err := p.expect(domain.TokenRBracket, "']'"); err != nil {
		return nil, err
	}
	return lit, nil
}

// parseObjectLiteral parses `{a: 1, "b": 2}`.
func (p *Parser) parseObjectLiteral() (domain.Expr, error) {
	t, err := p.expect(domain.TokenLBrace, "'{'")
	if err != nil {
		return nil, err
	}
	lit := &domain.ObjectLit{P: pos(t)}
	p.skipNewlines()
	if p.at(domain.TokenRBrace) {
		p.next()
		return lit, nil
	}
	for {
		p.skipNewlines()
		keyTok := p.cur()
		var key string
		switch keyTok.Type {
		case domain.TokenIdent, domain.TokenKeyword:
			key = p.next().Text
		case domain.TokenStringStart:
			key, err = p.parseStringLiteral()
			if err != nil {
				return nil, err
			}
		case domain.TokenNumber:
			p.next()
			key = keyTok.Text
		default:
			return nil, p.errAt("expected object key, found %s", keyTok.String())
		}
		if _, err := p.expect(domain.TokenColon, "':'"); err != nil {
			return nil, err
		}
		p.skipNewlines()
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		lit.Pairs = append(lit.Pairs, domain.Pair{Key: key, Value: val})
		p.skipNewlines()
		if p.at(domain.TokenComma) {
			p.next()
			p.skipNewlines()
			if p.at(domain.TokenRBrace) {
				break
			}
			continue
		}
		break
	}
	p.skipNewlines()
	if _, err := p.expect(domain.TokenRBrace, "'}'"); err != nil {
		return nil, err
	}
	return lit, nil
}
