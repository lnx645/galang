// Package lex turns Garurda source text into a token stream.
package lex

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"garurda/internal/domain"
)

// Error is a lexer error with a source position.
type Error struct {
	Msg  string
	Line int
	Col  int
	File string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Col, e.Msg)
}

// Lexer tokenises one source file.
type Lexer struct {
	src  string
	file string
	i    int
	line int
	col  int

	// stack holds the scanning modes so a string nested inside an
	// interpolation returns to the right place when it ends.
	stack []frame

	out []domain.Token
}

// frame is one level of the lexer's mode stack.
type frame struct {
	mode  int
	quote byte
	raw   bool
	// implicit marks the short `$nama` form, which ends at the first `}`
	// or at the end of the line rather than tracking nested braces.
	implicit   bool
	braceDepth int
}

const (
	modeNormal = iota
	modeString
	modeInterp
)

// Tokenize scans src and returns the full token list, terminated by EOF.
func Tokenize(src, file string) ([]domain.Token, error) {
	l := &Lexer{src: src, file: file, line: 1, col: 1}
	if err := l.run(); err != nil {
		return nil, err
	}
	return l.out, nil
}

func (l *Lexer) pos() domain.Position { return domain.Position{Line: l.line, Col: l.col} }

func (l *Lexer) errorf(p domain.Position, format string, args ...interface{}) error {
	return &Error{Msg: fmt.Sprintf(format, args...), Line: p.Line, Col: p.Col, File: l.file}
}

// peekByte returns the byte at the current offset, or 0 at end of input.
func (l *Lexer) peekByte() byte {
	if l.i >= len(l.src) {
		return 0
	}
	return l.src[l.i]
}

// peekByteAt returns the byte n positions ahead of the cursor.
func (l *Lexer) peekByteAt(n int) byte {
	if l.i+n >= len(l.src) {
		return 0
	}
	return l.src[l.i+n]
}

func (l *Lexer) advance() byte {
	c := l.src[l.i]
	l.i++
	if c == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return c
}

func (l *Lexer) emit(t domain.TokenType, text string, p domain.Position) {
	tok := domain.Token{Type: t, Text: text, Line: p.Line, Col: p.Col}
	l.out = append(l.out, tok)
}

// push enters a nested scanning mode.
func (l *Lexer) push(f frame) { l.stack = append(l.stack, f) }

// pop leaves the current mode and restores the enclosing one.
func (l *Lexer) pop() {
	l.stack = l.stack[:len(l.stack)-1]
}

// top returns the current frame, which always exists while scanning.
func (l *Lexer) top() frame { return l.stack[len(l.stack)-1] }

func (l *Lexer) setTop(f frame) { l.stack[len(l.stack)-1] = f }

func (l *Lexer) mode() int { return l.stack[len(l.stack)-1].mode }

func (l *Lexer) run() error {
	l.push(frame{mode: modeNormal})
	for l.i < len(l.src) {
		switch l.mode() {
		case modeNormal:
			if err := l.scanNormal(); err != nil {
				return err
			}
		case modeString:
			if err := l.scanString(); err != nil {
				return err
			}
		default:
			if err := l.scanInterp(); err != nil {
				return err
			}
		}
	}
	if l.mode() == modeInterp {
		return l.errorf(l.pos(), "unterminated interpolation")
	}
	p := l.pos()
	l.emit(domain.TokenEOF, "", p)
	return nil
}

// scanNormal handles code outside strings.
func (l *Lexer) scanNormal() error {
	c := l.peekByte()

	switch {
	case c == ' ' || c == '\t' || c == '\r':
		l.advance()
		return nil

	case c == '\n':
		p := l.pos()
		l.advance()
		l.emit(domain.TokenNewline, "\n", p)
		return nil

	case c == '/' && l.peekByteAt(1) == '/':
		for l.i < len(l.src) && l.peekByte() != '\n' {
			l.advance()
		}
		return nil

	case c == '/' && l.peekByteAt(1) == '*':
		start := l.pos()
		l.advance()
		l.advance()
		for {
			if l.i >= len(l.src) {
				return l.errorf(start, "unterminated block comment")
			}
			if l.peekByte() == '*' && l.peekByteAt(1) == '/' {
				l.advance()
				l.advance()
				return nil
			}
			if l.peekByte() == '\n' {
				// Keep newlines as statement separators inside comments.
				p := l.pos()
				l.advance()
				l.emit(domain.TokenNewline, "\n", p)
				continue
			}
			l.advance()
		}

	case c == '"' || c == '\'':
		p := l.pos()
		l.advance()
		l.push(frame{mode: modeString, quote: c})
		l.emitStringStart(p)
		return nil

	case c == '`':
		p := l.pos()
		l.advance()
		l.push(frame{mode: modeString, quote: '`', raw: true})
		l.emitStringStart(p)
		return nil
	}

	if isDigit(c) || (c == '.' && isDigit(l.peekByteAt(1))) {
		return l.scanNumber()
	}

	// `$` is an optional sigil in front of an identifier.
	if c == '$' {
		start := l.pos()
		l.advance()
		nc := l.peekByte()
		if nc == '_' || (nc >= 'a' && nc <= 'z') || (nc >= 'A' && nc <= 'Z') {
			name, err := l.scanIdentBody(start)
			if err != nil {
				return err
			}
			l.emitName(name, start, true)
			return nil
		}
		return l.errorf(start, "'$' must be followed by an identifier")
	}

	if c == '_' || isAlpha(c) {
		start := l.pos()
		name, err := l.scanIdentBody(start)
		if err != nil {
			return err
		}
		l.emitName(name, start, false)
		return nil
	}

	if c >= utf8.RuneSelf {
		return l.errorf(l.pos(), "unexpected character %q", string(rune(c)))
	}

	return l.scanOperator()
}

func (l *Lexer) scanIdentBody(start domain.Position) (string, error) {
	var b strings.Builder
	for l.i < len(l.src) {
		c := l.peekByte()
		if c == '_' || isAlpha(c) || isDigit(c) {
			b.WriteByte(l.advance())
			continue
		}
		break
	}
	return b.String(), nil
}

// emitName classifies an identifier as keyword or identifier. A name written
// with `$` is never a keyword, so `$string` is a legal variable.
func (l *Lexer) emitName(name string, p domain.Position, dollar bool) {
	if !dollar && domain.Keywords[name] {
		switch name {
		case "or":
			l.emit(domain.TokenOr, name, p)
			return
		case "and":
			l.emit(domain.TokenAnd, name, p)
			return
		case "not":
			l.emit(domain.TokenNot, name, p)
			return
		}
		l.emit(domain.TokenKeyword, name, p)
		return
	}
	tok := domain.Token{Type: domain.TokenIdent, Text: name, Line: p.Line, Col: p.Col, Dollar: dollar}
	l.out = append(l.out, tok)
}

// scanString collects literal chunks of a string and switches into
// interpolation mode when it meets `${`.
func (l *Lexer) scanString() error {
	f := l.top()
	var lit strings.Builder

	flush := func() {
		if lit.Len() == 0 {
			return
		}
		p := l.pos()
		l.out = append(l.out, domain.Token{Type: domain.TokenString, Str: lit.String(), Line: p.Line, Col: p.Col})
		lit.Reset()
	}

	for l.i < len(l.src) {
		c := l.peekByte()
		switch {
		case c == f.quote:
			l.advance()
			flush()
			l.emit(domain.TokenStringEnd, "", l.pos())
			l.pop()
			return nil

		case c == '\n' && f.quote != '`':
			return l.errorf(l.pos(), "unterminated string (newline in string)")

		case c == '\\' && !f.raw:
			l.advance()
			if l.i >= len(l.src) {
				return l.errorf(l.pos(), "unterminated escape sequence")
			}
			e := l.advance()
			switch e {
			case 'n':
				lit.WriteByte('\n')
			case 't':
				lit.WriteByte('\t')
			case 'r':
				lit.WriteByte('\r')
			case '0':
				lit.WriteByte(0)
			case '\\':
				lit.WriteByte('\\')
			case '"':
				lit.WriteByte('"')
			case '\'':
				lit.WriteByte('\'')
			case '`':
				lit.WriteByte('`')
			case '$':
				lit.WriteByte('$')
			case 'u':
				if l.i >= len(l.src) || l.peekByte() != '{' {
					return l.errorf(l.pos(), "expected '{' after \\u")
				}
				l.advance()
				var hex strings.Builder
				for l.i < len(l.src) && l.peekByte() != '}' && hex.Len() < 6 {
					h := l.peekByte()
					if !isHex(h) {
						return l.errorf(l.pos(), "invalid hex digit in \\u escape")
					}
					hex.WriteByte(l.advance())
				}
				if l.i >= len(l.src) || l.peekByte() != '}' {
					return l.errorf(l.pos(), "unterminated \\u escape")
				}
				l.advance()
				var cp int64
				for i := 0; i < hex.Len(); i++ {
					cp = cp*16 + int64(hexVal(hex.String()[i]))
				}
				lit.WriteRune(rune(cp))
			default:
				return l.errorf(l.pos(), "unknown escape \\%c", e)
			}

		case c == '$' && l.peekByteAt(1) == '{' && !f.raw:
			flush()
			p := l.pos()
			l.advance()
			l.advance()
			l.emit(domain.TokenInterpBeg, "${", p)
			// Suspend the string while the interpolation is tokenised.
			l.push(frame{mode: modeInterp, braceDepth: 1})
			return nil

		case c == '$' && isIdentStart(l.peekByteAt(1)) && !f.raw:
			// Short form: `$nama`. It accepts a plain identifier only, so
			// complex expressions must use `${...}` and `$` in front of a
			// digit or punctuation stays literal.
			flush()
			p := l.pos()
			l.advance()
			name, err := l.scanIdentBody(p)
			if err != nil {
				return err
			}
			l.emit(domain.TokenInterpBeg, "${", p)
			l.emitName(name, p, true)
			l.emit(domain.TokenInterpEnd, "}", p)
			return nil

		default:
			lit.WriteByte(l.advance())
		}
	}
	return l.errorf(l.pos(), "unterminated string")
}

// emitStringStart marks the beginning of a string literal.
func (l *Lexer) emitStringStart(p domain.Position) {
	l.emit(domain.TokenStringStart, "", p)
}

// scanInterp tokenises code inside `${ ... }` and returns to the suspended
// string mode at the matching closing brace.
func (l *Lexer) scanInterp() error {
	c := l.peekByte()
	switch {
	case c == ' ' || c == '\t' || c == '\r':
		l.advance()
		return nil
	case c == '\n':
		p := l.pos()
		l.advance()
		l.emit(domain.TokenNewline, "\n", p)
		return nil
	case c == '{':
		p := l.pos()
		l.advance()
		f := l.top()
		f.braceDepth++
		l.setTop(f)
		l.emit(domain.TokenLBrace, "{", p)
		return nil
	case c == '}':
		p := l.pos()
		l.advance()
		f := l.top()
		f.braceDepth--
		if f.braceDepth == 0 {
			l.emit(domain.TokenInterpEnd, "}", p)
			l.pop()
		} else {
			l.setTop(f)
			l.emit(domain.TokenRBrace, "}", p)
		}
		return nil
	}
	return l.scanNormal()
}

// scanNumber reads an integer, float, hexadecimal or underscored literal.
func (l *Lexer) scanNumber() error {
	start := l.pos()
	var b strings.Builder

	if l.peekByte() == '0' && (l.peekByteAt(1) == 'x' || l.peekByteAt(1) == 'X') {
		l.advance()
		l.advance()
		digits := 0
		for l.i < len(l.src) && isHex(l.peekByte()) {
			b.WriteByte(l.advance())
			digits++
		}
		if digits == 0 {
			return l.errorf(start, "hexadecimal literal needs at least one digit")
		}
		v := int64(0)
		for i := 0; i < b.Len(); i++ {
			v = v*16 + int64(hexVal(b.String()[i]))
		}
		l.emitNumber(domain.Int(v), start)
		return nil
	}

	isFloat := false
	for l.i < len(l.src) {
		c := l.peekByte()
		if isDigit(c) {
			b.WriteByte(l.advance())
			continue
		}
		if c == '_' {
			// Digit separator, only valid between digits.
			l.advance()
			continue
		}
		if c == '.' && l.peekByteAt(1) != '.' {
			nxt := l.peekByteAt(1)
			if isDigit(nxt) || nxt == 'e' || nxt == 'E' {
				isFloat = true
				b.WriteByte(l.advance())
				continue
			}
			if b.Len() == 0 {
				break
			}
			isFloat = true
			b.WriteByte(l.advance())
			continue
		}
		if (c == 'e' || c == 'E') && (b.Len() > 0) {
			isFloat = true
			b.WriteByte(l.advance())
			if l.peekByte() == '+' || l.peekByte() == '-' {
				b.WriteByte(l.advance())
			}
			continue
		}
		break
	}

	text := b.String()
	if text == "" {
		return l.errorf(start, "invalid number")
	}
	if isFloat {
		f, ok := parseFloat(text)
		if !ok {
			return l.errorf(start, "invalid float literal '%s'", text)
		}
		l.emitNumber(domain.Float(f), start)
		return nil
	}
	i, ok := parseInt(text)
	if !ok {
		return l.errorf(start, "invalid integer literal '%s'", text)
	}
	l.emitNumber(domain.Int(i), start)
	return nil
}

func (l *Lexer) emitNumber(v domain.Value, p domain.Position) {
	tok := domain.Token{Type: domain.TokenNumber, Line: p.Line, Col: p.Col}
	switch n := v.(type) {
	case domain.Int:
		tok.IsInt = true
		tok.Number = float64(n)
		tok.Text = n.String()
	case domain.Float:
		tok.Number = float64(n)
		tok.Text = n.String()
	}
	l.out = append(l.out, tok)
}

// scanOperator reads punctuation and multi-character operators.
func (l *Lexer) scanOperator() error {
	p := l.pos()
	c := l.advance()
	switch c {
	case '+':
		if l.peekByte() == '=' {
			l.advance()
			l.emit(domain.TokenPlusEq, "+=", p)
		} else {
			l.emit(domain.TokenPlus, "+", p)
		}
	case '-':
		if l.peekByte() == '=' {
			l.advance()
			l.emit(domain.TokenMinusEq, "-=", p)
		} else {
			l.emit(domain.TokenMinus, "-", p)
		}
	case '*':
		if l.peekByte() == '=' {
			l.advance()
			l.emit(domain.TokenStarEq, "*=", p)
		} else {
			l.emit(domain.TokenStar, "*", p)
		}
	case '/':
		if l.peekByte() == '=' {
			l.advance()
			l.emit(domain.TokenSlashEq, "/=", p)
		} else {
			l.emit(domain.TokenSlash, "/", p)
		}
	case '%':
		l.emit(domain.TokenPercent, "%", p)
	case '=':
		switch l.peekByte() {
		case '=':
			l.advance()
			l.emit(domain.TokenEq, "==", p)
		case '>':
			l.advance()
			l.emit(domain.TokenArrow, "=>", p)
		default:
			l.emit(domain.TokenAssign, "=", p)
		}
	case '!':
		if l.peekByte() == '=' {
			l.advance()
			l.emit(domain.TokenNeq, "!=", p)
			return nil
		}
		return l.errorf(p, "unexpected '!'; use 'not' for negation")
	case '<':
		if l.peekByte() == '=' {
			l.advance()
			l.emit(domain.TokenLtEq, "<=", p)
		} else {
			l.emit(domain.TokenLt, "<", p)
		}
	case '>':
		if l.peekByte() == '=' {
			l.advance()
			l.emit(domain.TokenGtEq, ">=", p)
		} else {
			l.emit(domain.TokenGt, ">", p)
		}
	case '.':
		if l.peekByte() == '.' {
			l.advance()
			l.emit(domain.TokenRange, "..", p)
		} else {
			l.emit(domain.TokenDot, ".", p)
		}
	case '?':
		l.emit(domain.TokenQuestion, "?", p)
	case ':':
		l.emit(domain.TokenColon, ":", p)
	case ',':
		l.emit(domain.TokenComma, ",", p)
	case ';':
		l.emit(domain.TokenSemi, ";", p)
	case '(':
		l.emit(domain.TokenLParen, "(", p)
	case ')':
		l.emit(domain.TokenRParen, ")", p)
	case '{':
		l.emit(domain.TokenLBrace, "{", p)
	case '}':
		l.emit(domain.TokenRBrace, "}", p)
	case '[':
		l.emit(domain.TokenLBracket, "[", p)
	case ']':
		l.emit(domain.TokenRBracket, "]", p)
	default:
		return l.errorf(p, "unexpected character %q", string(rune(c)))
	}
	return nil
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= utf8.RuneSelf
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// isIdentStart reports whether c may begin an identifier.
func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}

// parseInt parses an integer literal body, ignoring digit separators.
func parseInt(s string) (int64, bool) {
	clean := strings.ReplaceAll(s, "_", "")
	if clean == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(clean, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseFloat parses a float literal body, ignoring digit separators.
func parseFloat(s string) (float64, bool) {
	clean := strings.ReplaceAll(s, "_", "")
	if clean == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
