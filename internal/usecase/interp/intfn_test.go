package interp

import (
	"io"
	"strings"
	"testing"

	"garurda/internal/domain"
	"garurda/internal/usecase/parse"
)

var _ = strings.TrimSpace

// specialised reports whether a function got an unboxed calling convention,
// either bytecode or the closure form.
func specialised(c *closure) bool { return c.code != nil || c.int != nil }

// compileOne compiles a single function declaration the way the real pipeline
// does, including storing it in its global slot.
func compileOne(t *testing.T, src string) *closure {
	t.Helper()
	in := New(discard{}, discard{})
	prog, err := parse.Parse(src, "t.ga")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := &compiler{in: in, globalScope: in.gscope}
	c.captures = &[]capture{}
	body := c.compileStmts(prog.Stmts, in.gscope)
	in.growGlobals()
	if _, _, err := body(in.globals); err != nil {
		t.Fatalf("compile: %v", err)
	}
	decl, ok := prog.Stmts[0].(*domain.FnDecl)
	if !ok {
		t.Fatalf("expected a function declaration")
	}
	cl, ok := in.globals.getVal(in.gscope.names[decl.Fn.Name].idx).(*closure)
	if !ok {
		t.Fatalf("%s is not stored as a closure", decl.Fn.Name)
	}
	return cl
}

// TestBackendChoice documents which backend each function gets. Bytecode wins
// on call-heavy code; a function containing a loop keeps the closure backend,
// because there the loop becomes a native Go loop.
func TestBackendChoice(t *testing.T) {
	cases := []struct {
		src         string
		wantCode    bool // bytecode
		wantUnboxed bool // either unboxed backend
	}{
		{"fn fib(int $n) int { if $n < 2 { return $n }\n return fib($n - 1) + fib($n - 2) }", true, true},
		{"fn add(int $a, int $b) int { return $a + $b }", true, true},
		{"fn loops(int $n) int { $t = 0\n for $i in 0..$n { $t = $t + $i }\n return $t }", false, true},
		{"fn loops2(int $n) int { $t = 0\n while $n > 0 { $t = $t + 1; $n = $n - 1 }\n return $t }", false, true},
		{"fn usesPrint(int $n) int { print $n\n return $n }", false, false},
		{"fn usesArray(int $n) int { $a = [1, 2]\n return $n + $a[0] }", false, false},
		{"fn untyped($n) { return $n }", false, false},
		{"fn nullable(int $n) ?int { return $n }", false, false},
		{"fn hasDefault(int $n, int $m = 1) int { return $n + $m }", false, false},
	}
	for _, c := range cases {
		cl := compileOne(t, c.src)
		if got := cl.code != nil; got != c.wantCode {
			t.Errorf("bytecode=%v want %v for: %s", got, c.wantCode, c.src)
		}
		if got := specialised(cl); got != c.wantUnboxed {
			t.Errorf("unboxed=%v want %v for: %s", got, c.wantUnboxed, c.src)
		}
	}
}

// TestBytecodeMatchesGeneralPath runs integer code through the specialised
// backends and the general path and requires identical output, so
// specialisation can never change semantics.
func TestBytecodeMatchesGeneralPath(t *testing.T) {
	sources := []string{
		"fn f(int $n) int { if $n < 2 { return $n }\n return f($n - 1) + f($n - 2) }\nprint f(15)",
		"fn w(int $a, int $b) int { $s = 0\n while $a > 0 { $s = $s + $b; $a = $a - 3 }\n return $s }\nprint w(10, 4)",
		"fn r(int $n) int { $t = 0\n for $i in $n..0 { $t = $t + $i }\n return $t }\nprint r(4)",
		"fn b(int $n) int { $t = 0\n for $i in 0..$n { if $i > 5 { break }\n $t = $t + $i }\n return $t }\nprint b(20)",
		"fn c(int $n) int { $t = 0\n for $i in 0..$n { if $i % 3 == 0 { continue }\n $t = $t + $i }\n return $t }\nprint c(50)",
		"fn m(int $n) int { $p = 1\n for $i in 1..$n { $p = $p * $i % 1000 }\n return $p }\nprint m(10)",
	}
	for _, src := range sources {
		var fast, slow strings.Builder
		inFast := New(&fast, &fast)
		if _, err := inFast.Eval(src, "t.ga"); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		// The same program with every unboxed backend disabled, which forces
		// the general closure path.
		inSlow := New(&slow, &slow)
		stripSpecialisation(inSlow, src, &discard{})
		if _, err := inSlow.Eval(src, "t.ga"); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if fast.String() != slow.String() {
			t.Errorf("output differs:\n specialised: %q\ngeneral:     %q\nsource: %s",
				fast.String(), slow.String(), src)
		}
	}
}

// stripSpecialisation runs the program once so the functions exist, then clears
// their bytecode and unboxed bodies. The first run's output goes to throwaway.
func stripSpecialisation(in *Interp, src string, out io.Writer) {
	inSlowOut := in.Out
	in.Out = out
	_, _ = in.Eval(src, "t.ga")
	in.Out = inSlowOut
	for _, slot := range in.gscope.names {
		if cl, ok := in.globals.getVal(slot.idx).(*closure); ok {
			cl.code = nil
			cl.int = nil
		}
	}
}

// TestDumpBytecode lists the bytecode of a function, which is how the
// instruction stream was debugged. Run with -v.
func TestDumpBytecode(t *testing.T) {
	cl := compileOne(t, "fn fib(int $n) int { if $n < 2 { return $n }\n return fib($n - 1) + fib($n - 2) }")
	if cl.code == nil {
		t.Skip("no bytecode for this function")
	}
	t.Logf("%s: nlocals=%d nparams=%d, %d instructions",
		cl.code.name, cl.code.nlocals, cl.code.nparams, len(cl.code.code))
}
