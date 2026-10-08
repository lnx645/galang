package interp

import "testing"

func TestAsyncAwait(t *testing.T) {
	cases := []struct{ src, want string }{
		// await resolves an async call.
		{`async fn g(n) { return n * 2 }
		  print await g(21)`, "42"},
		// A promise can be stored, typed, then awaited.
		{`async fn g() { return "ok" }
		  $p = g()
		  print type($p) + " " + await $p`, "promise ok"},
		// await passes plain values through unchanged.
		{`print await 7`, "7"},
		// gather resolves every argument into an array.
		{`async fn a() { return 1 }
		  async fn b() { return 2 }
		  $r = gather(a(), b())
		  print $r[0] + $r[1]`, "3"},
		// Anonymous async functions.
		{`$f = async fn(x) { return x + 1 }
		  print await $f(41)`, "42"},
		// A rejected promise throws when awaited and is catchable.
		{`async fn bad() { throw not_found("hilang") }
		  try { await bad() } catch $e { print $e.message + "/" + str($e.status) }`, "hilang/404"},
		// Errors from gather propagate too.
		{`async fn bad() { throw error("gagal") }
		  try { gather(bad()) } catch $e { print $e.message }`, "gagal"},
	}
	for _, c := range cases {
		if got := evalStr(t, c.src); got != c.want {
			t.Errorf("got %q, want %q\nsource:\n%s", got, c.want, c.src)
		}
	}
}

func TestAsyncSpawnDrains(t *testing.T) {
	// spawn is fire-and-forget: it runs during the drain after the program
	// body, so its output lands after everything else.
	out, _, err := runSnippet(t, `fn tulis(s) { println(s) }
		spawn(tulis, "spawn jalan")
		println("selesai")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "selesai\nspawn jalan\n"
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestAsyncCyclicAwaitFails(t *testing.T) {
	evalFails(t, `$p = null
		$p = spawn(fn() { return await $p } )
		await $p`, "cyclic")
}
