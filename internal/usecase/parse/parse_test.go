package parse

import (
	"strings"
	"testing"

	"galang/internal/domain"
	"galang/internal/usecase/lex"
)

// tokenSummary renders a token stream compactly for assertions.
func tokenSummary(t *testing.T, src string) string {
	t.Helper()
	toks, err := lex.Tokenize(src, "t.ga")
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	var parts []string
	for _, tk := range toks {
		switch tk.Type {
		case domain.TokenNewline:
			continue
		case domain.TokenEOF:
			parts = append(parts, "EOF")
		case domain.TokenString:
			parts = append(parts, `"`+tk.Str+`"`)
		case domain.TokenNumber:
			parts = append(parts, tk.Text)
		default:
			parts = append(parts, string(tk.Type))
		}
	}
	return strings.Join(parts, " ")
}

func TestLexerTokens(t *testing.T) {
	// Numbers and identifiers both render through their text form, so the
	// expectations below read like the source itself.
	cases := []struct{ src, want string }{
		{`$a = 1`, "identifier = 1 EOF"},
		{`$a = 1 + 2 * 3`, "identifier = 1 + 2 * 3 EOF"},
		{`fn f(int $x) { return $x }`, "keyword identifier ( keyword identifier ) { keyword identifier } EOF"},
		{`print "halo"`, `keyword string-start "halo" string-end EOF`},
		{`$s = "a ${1 + 1} b"`, `identifier = string-start "a " ${ 1 + 1 } " b" string-end EOF`},
		{`// hanya komentar`, "EOF"},
		{`/* blok */ 1`, "1 EOF"},
		{`0xFF`, "255 EOF"},
		{`1_000_000`, "1000000 EOF"},
		{`3.14`, "3.14 EOF"},
		{`$a[0]`, "identifier [ 0 ] EOF"},
		{`$o.kunci`, "identifier . identifier EOF"},
		{`$a[1:3]`, "identifier [ 1 : 3 ] EOF"},
		{`a and b or not c`, "identifier and identifier or not identifier EOF"},
		{`for i in 1..3`, "keyword identifier keyword 1 .. 3 EOF"},
		{`$x ? 1 : 2`, "identifier ? 1 : 2 EOF"},
		{`a.b("x")`, `identifier . identifier ( string-start "x" string-end ) EOF`},
		{`fn(x) => x`, "keyword ( identifier ) => identifier EOF"},
		{"\n\nprint 1", "keyword 1 EOF"},
		{`$s = "a"`, `identifier = string-start "a" string-end EOF`},
		{`$s = "a${"b"}c"`, `identifier = string-start "a" ${ string-start "b" string-end } "c" string-end EOF`},
	}
	for _, c := range cases {
		if got := tokenSummary(t, c.src); got != c.want {
			t.Errorf("tokens(%q):\n got: %s\nwant: %s", c.src, got, c.want)
		}
	}
}

func TestLexerStringModes(t *testing.T) {
	// A newline escape is decoded inside a double-quoted string.
	toks, err := lex.Tokenize(`"a\nb"`, "t.ga")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got := onlyChunks(toks); got != "a\nb" {
		t.Errorf("escaped newline: got %q", got)
	}

	// Backtick strings keep backslashes literal.
	toks, _ = lex.Tokenize("`a\\nb`", "t.ga")
	if got := onlyChunks(toks); got != `a\nb` {
		t.Errorf("raw string: got %q", got)
	}

	// \u escapes decode to the right rune.
	toks, _ = lex.Tokenize(`"garúda"`, "t.ga")
	if got := onlyChunks(toks); got != "garúda" {
		t.Errorf("unicode escape: got %q", got)
	}
}

// onlyChunks joins the literal chunks of a token stream.
func onlyChunks(toks []domain.Token) string {
	var b strings.Builder
	for _, tk := range toks {
		if tk.Type == domain.TokenString {
			b.WriteString(tk.Str)
		}
	}
	return b.String()
}

func TestLexerErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{`"tidak ditutup`, "unterminated string"},
		{"`multi\ngaris", "unterminated string"},
		{`"a` + "\n" + `b"`, "newline in string"},
		{`/* belum selesai`, "unterminated block comment"},
		{`"salah \q escape"`, "unknown escape"},
		{`$`, "'$' must be followed"},
		{`$1abc`, "'$' must be followed"},
		{`@`, "unexpected character"},
		{`&`, "use '&&'"},
	}
	for _, c := range cases {
		_, err := lex.Tokenize(c.src, "t.ga")
		if err == nil {
			t.Errorf("expected error for %q", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("lex(%q): got %v, want %q", c.src, err, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{`$a =`, "unexpected end of file"},
		{`if $a {`, "missing '}'"},
		{`fn f( {`, "expected variable name"},
		{`$a = [1, 2`, "expected ']'"},
		{`$o = {a: }`, "unexpected '}'"},
		{`$a = (1 + 2`, "expected ')'"},
		{`$a = $b[1`, "expected ']'"},
		{`while { }`, "expected '{'"},
		{`while $a`, "expected '{'"},
		{`for $x [1] {}`, "expected 'in'"},
		{`$a = "x${"y"`, "unterminated interpolation"},
	}
	for _, c := range cases {
		_, err := Parse(c.src, "t.ga")
		if err == nil {
			t.Errorf("expected parse error for %q", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("parse(%q):\n got: %v\nwant: %q", c.src, err, c.want)
		}
	}
}

// parseSrc parses without evaluating, so parser-only behaviour is testable.
func parseSrc(t *testing.T, src string) *Program {
	t.Helper()
	prog, err := Parse(src, "t.ga")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return prog
}

func TestParseStructure(t *testing.T) {
	// Typed declarations record their annotation.
	prog := parseSrc(t, `array<int> $ids = [1, 2]`)
	if len(prog.Stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(prog.Stmts))
	}
	d, ok := prog.Stmts[0].(*domain.VarDecl)
	if !ok {
		t.Fatalf("expected VarDecl, got %T", prog.Stmts[0])
	}
	if d.Name != "ids" {
		t.Errorf("name = %q", d.Name)
	}
	if d.Type == nil || d.Type.Name != "array" || len(d.Type.Args) != 1 || d.Type.Args[0].Name != "int" {
		t.Errorf("type annotation = %v", d.Type)
	}

	// Nullable annotations.
	d = parseSrc(t, `?string $s = null`).Stmts[0].(*domain.VarDecl)
	if !d.Type.Nullable || d.Type.Name != "string" {
		t.Errorf("nullable annotation = %v", d.Type)
	}

	// Function return annotation and parameters.
	fd := parseSrc(t, `fn hitung(int $a, $b = 2) int { return $a }`).Stmts[0].(*domain.FnDecl)
	if fd.Fn.Ret == nil || fd.Fn.Ret.Name != "int" {
		t.Errorf("return annotation = %v", fd.Fn.Ret)
	}
	if len(fd.Fn.Params) != 2 {
		t.Fatalf("params = %d", len(fd.Fn.Params))
	}
	if fd.Fn.Params[0].Type == nil || fd.Fn.Params[0].Type.Name != "int" {
		t.Errorf("param type = %v", fd.Fn.Params[0].Type)
	}
	if fd.Fn.Params[1].Type != nil {
		t.Errorf("untyped param should have nil type")
	}
	if fd.Fn.Params[1].Default == nil {
		t.Errorf("default value not parsed")
	}

	// else-if chains nest.
	ifStmt := parseSrc(t, `if $a { } else if $b { } else { }`).Stmts[0].(*domain.IfStmt)
	if _, ok := ifStmt.Else.(*domain.IfStmt); !ok {
		t.Errorf("else branch should be an IfStmt, got %T", ifStmt.Else)
	}

	// Comprehension fields.
	comp := parseSrc(t, `[$k for $k, $v in $o if $k != ""]`).Stmts[0].(*domain.ExprStmt)
	list := comp.X.(*domain.ListLit)
	if list.CompVar != "k" || list.CompIdx != "v" || list.CompCond == nil {
		t.Errorf("comprehension = %+v", list)
	}

	// Slices remember which bounds were present.
	sl := parseSrc(t, `$a[1:]`).Stmts[0].(*domain.ExprStmt).X.(*domain.SliceExpr)
	if !sl.HasLow || sl.HasHigh {
		t.Errorf("slice bounds: low=%v high=%v", sl.HasLow, sl.HasHigh)
	}

	// A type name before '(' is a constructor call, not a declaration.
	es := parseSrc(t, `error("x")`).Stmts[0].(*domain.ExprStmt)
	if _, ok := es.X.(*domain.CallExpr); !ok {
		t.Errorf("expected CallExpr, got %T", es.X)
	}

	// try/catch records the binding.
	ts := parseSrc(t, `try { } catch e { }`).Stmts[0].(*domain.TryStmt)
	if !ts.HasVar || ts.Var != "e" {
		t.Errorf("catch binding = %q (hasVar=%v)", ts.Var, ts.HasVar)
	}
}
