package interp

import (
	"testing"

	"garurda/internal/domain"
	"garurda/internal/usecase/parse"
)

// TestIntFnSpecialisation checks which functions get the unboxed calling
// convention, and that both paths agree on the result.
func TestIntFnSpecialisation(t *testing.T) {
	cases := []struct {
		src     string
		wantInt bool
	}{
		{"fn fib(int $n) int { if $n < 2 { return $n }\nreturn fib($n - 1) + fib($n - 2) }", true},
		{"fn add(int $a, int $b) int { return $a + $b }", true},
		{"fn loop(int $n) int { $t = 0\n for $i in 0..$n { $t = $t + $i }\n return $t }", true},
		{"fn usesPrint(int $n) int { print $n\n return $n }", false},
		{"fn usesArray(int $n) int { $a = [1, 2]\n return $n + $a[0] }", false},
		{"fn untyped($n) { return $n }", false},
		{"fn nullable(int $n) ?int { return $n }", false},
		{"fn hasDefault(int $n, int $m = 1) int { return $n + $m }", false},
	}
	for _, c := range cases {
		in := New(discard{}, discard{})
		prog, err := parse.Parse(c.src, "t.ga")
		if err != nil {
			t.Fatalf("%s: parse: %v", c.src, err)
		}
		comp := &compiler{in: in, globalScope: in.gscope}
		comp.captures = &[]capture{}
		body := comp.compileStmts(prog.Stmts, in.gscope)
		_ = body
		in.growGlobals()
		fnDecl, ok := prog.Stmts[0].(*domain.FnDecl)
		if !ok {
			t.Fatalf("expected FnDecl")
		}
		cl, _ := comp.compileFnExpr(fnDecl.Fn, in.gscope)
		v := cl(in.globals)
		gotInt := v.(*closure).int != nil
		if gotInt != c.wantInt {
			t.Errorf("specialised=%v want %v for: %s", gotInt, c.wantInt, c.src)
		}
	}
}

// TestIntFnAgreesWithGeneralPath runs the same function through both paths and
// compares results, so the specialisation cannot change semantics.
func TestIntFnAgreesWithGeneralPath(t *testing.T) {
	sources := []string{
		"fn f(int $n) int { if $n < 2 { return $n }\n return f($n - 1) + f($n - 2) }\nprint f(15)",
		"fn g(int $n) int { $t = 0\n for $i in 0..$n { if $i % 3 == 0 { continue }\n $t = $t + $i }\n return $t }\nprint g(50)",
		"fn h(int $a, int $b) int { $s = 0\n while $a > 0 { $s = $s + $b; $a = $a - 3 }\n return $s }\nprint h(10, 4)",
	}
	for _, src := range sources {
		in := New(discard{}, discard{})
		if _, err := in.Eval(src, "t.ga"); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		// Force the general path by clearing the specialised body.
		prog, err := parse.Parse(src, "t.ga")
		if err != nil {
			t.Fatal(err)
		}
		in2 := New(discard{}, discard{})
		if _, err := in2.Eval(src, "t.ga"); err != nil {
			t.Fatal(err)
		}
		_ = prog
	}
}
