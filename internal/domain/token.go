// Package domain holds the core, dependency-free model of the Garurda language:
// tokens, syntax tree nodes and runtime values.
package domain

// TokenType categorises a lexical token.
type TokenType string

const (
	TokenEOF     TokenType = "eof"
	TokenNewline TokenType = "newline"
	TokenIdent   TokenType = "identifier"
	TokenNumber  TokenType = "number"
	TokenKeyword TokenType = "keyword"

	// A string literal is a run of chunks and interpolations delimited by
	// TokenStringStart and TokenStringEnd.
	TokenStringStart TokenType = "string-start"
	TokenString      TokenType = "string-chunk"
	TokenStringEnd   TokenType = "string-end"

	// Interpolation boundaries inside a template string.
	TokenInterpBeg TokenType = "${"
	TokenInterpEnd TokenType = "}"

	// Logical operators. They are keywords (`and`, `or`, `not`) but still
	// have token types so the parser can rank them by precedence.
	TokenOr  TokenType = "or"
	TokenAnd TokenType = "and"
	TokenNot TokenType = "not"

	// Operators
	TokenPlus    TokenType = "+"
	TokenMinus   TokenType = "-"
	TokenStar    TokenType = "*"
	TokenSlash   TokenType = "/"
	TokenPercent TokenType = "%"

	TokenAssign  TokenType = "="
	TokenPlusEq  TokenType = "+="
	TokenMinusEq TokenType = "-="
	TokenStarEq  TokenType = "*="
	TokenSlashEq TokenType = "/="

	TokenEq   TokenType = "=="
	TokenNeq  TokenType = "!="
	TokenLt   TokenType = "<"
	TokenGt   TokenType = ">"
	TokenLtEq TokenType = "<="
	TokenGtEq TokenType = ">="
	// TokenArrow introduces the short lambda body: fn(x) => x * 2
	TokenArrow TokenType = "=>"

	// TokenIn is synthesised by the parser for the `x in xs` operator; the
	// `in` keyword itself stays a keyword for `for x in xs`.
	TokenIn TokenType = "in"

	TokenQuestion TokenType = "?"
	TokenColon    TokenType = ":"
	TokenComma    TokenType = ","
	TokenDot      TokenType = "."
	TokenSemi     TokenType = ";"

	TokenLParen   TokenType = "("
	TokenRParen   TokenType = ")"
	TokenLBrace   TokenType = "{"
	TokenRBrace   TokenType = "}"
	TokenLBracket TokenType = "["
	TokenRBracket TokenType = "]"
	TokenRange    TokenType = ".."
)

// Token is a single lexical unit with its 1-based source position.
type Token struct {
	Type   TokenType
	Text   string // raw text for identifiers, keywords and operators
	Number float64
	IsInt  bool
	Str    string // decoded contents of a string literal
	Line   int
	Col    int
	// Dollar records that the name was written with the optional `$` sigil,
	// which allows reserved words such as $string as identifiers.
	Dollar bool
}

// String renders the token the way it appears in source, for error messages.
func (t Token) String() string {
	switch t.Type {
	case TokenEOF:
		return "end of file"
	case TokenNewline:
		return "end of line"
	case TokenIdent, TokenKeyword:
		return "'" + t.Text + "'"
	case TokenNumber:
		return "number " + t.Text
	case TokenString:
		return "string " + t.Str
	case TokenStringStart:
		return "string"
	case TokenStringEnd:
		return "end of string"
	default:
		return "'" + string(t.Type) + "'"
	}
}

// Keywords is the reserved word table. With the `$` sigil a reserved word may
// still be used as an identifier, e.g. $string.
var Keywords = map[string]bool{
	"fn": true, "return": true, "if": true, "else": true, "for": true,
	"in": true, "while": true, "break": true, "continue": true,
	"print": true, "println": true, "and": true, "or": true, "not": true,
	"true": true, "false": true, "null": true, "throw": true, "try": true,
	"catch": true, "use": true, "var": true,

	// Type names usable in declarations and annotations.
	"int": true, "float": true, "number": true, "string": true, "bool": true,
	"array": true, "object": true, "any": true, "function": true, "error": true,
	"request": true, "response": true, "connection": true, "session": true,
	"upload": true, "rows": true, "module": true, "mailer": true,
}

// IsTypeName reports whether a keyword may start a type annotation.
func IsTypeName(name string) bool {
	switch name {
	case "int", "float", "number", "string", "bool", "array", "object", "any",
		"function", "error", "request", "response", "connection", "session",
		"upload", "rows", "module", "mailer":
		return true
	}
	return false
}
