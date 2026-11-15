package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// A frame that a call has finished with is cleared and reused: whatever the
// last use left in it (locals, static parameters, the argument buffer, flags)
// is gone when the next call gets it, and a recursion that nests frames gets
// distinct ones.
func TestFramePoolGivesCleanFrames(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	must(s.Eval("function pool_f(a)\n    a\nend"))
	f0 := s.Mod.own("pool_f").Value.(*Function)
	lam := f0.Table.Methods(f0.Table.World())[0].Def.(*lambda)
	for i := 0; i < 20; i++ {
		f := vm.newFrame(lam.c, lam.mod)
		f.locals[0] = int64(i)
		f.sparams, f.spenv = []any{int64(1)}, "env"
		f.preevaluation, f.continueAt, f.ip, f.argBusy = true, 5, 7, true
		f.argbuf[0] = "stale"
		freeFrame(f)
		g := vm.newFrame(lam.c, lam.mod)
		if g.sparams != nil || g.spenv != nil || g.preevaluation || g.continueAt != 0 || g.ip != 0 || g.argBusy || g.argbuf[0] != nil || g.vm != vm || g.c != lam.c {
			t.Fatalf("a reused frame is not clean: %+v", g)
		}
		for j, v := range g.locals {
			if v != undef {
				t.Fatalf("local %d of a reused frame is %v", j, v)
			}
		}
		freeFrame(g)
	}
	// nested calls hold distinct frames
	must(s.Eval("function pool_rec(n, acc)\n    Core.Intrinsics.slt_int(n, 1) ? acc : pool_rec(Core.Intrinsics.sub_int(n, 1), Core.Intrinsics.add_int(acc, n))\nend"))
	for i := 0; i < 3; i++ {
		v, err := s.Eval("pool_rec(50, 0)")
		check(t, "pool_rec", v, err, "1275")
	}
}

// A frame holds its locals inline when it is small, and in a slice of its own
// when it is not; both are undefined until written, and a call builds the
// locals from its arguments without aliasing the caller's slice.
func TestFrameLocalsAndArguments(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	lamOf := func(name string) (*Function, *lambda) {
		f := s.Mod.own(expr.Symbol(name)).Value.(*Function)
		return f, f.Table.Methods(f.Table.World())[0].Def.(*lambda)
	}
	// small and large frames: the number of locals decides where they live
	must(s.Eval("function fr_small(a)\n    a\nend"))
	_, small := lamOf("fr_small")
	if n := small.c.nslots + small.c.nssa; n > frameInline {
		t.Fatalf("the small method has %d locals", n)
	}
	body := "a"
	for i := 0; i < frameInline+2; i++ {
		body = "Core.tuple(" + body + ", a)"
	}
	must(s.Eval("function fr_large(a)\n    " + body + "\nend"))
	_, large := lamOf("fr_large")
	if n := large.c.nslots + large.c.nssa; n <= frameInline {
		t.Fatalf("the large method has %d locals", n)
	}
	for name, l := range map[string]*lambda{"small": small, "large": large} {
		f := vm.newFrame(l.c, l.mod)
		if len(f.locals) != l.c.nslots+l.c.nssa {
			t.Errorf("%s: %d locals, want %d", name, len(f.locals), l.c.nslots+l.c.nssa)
		}
		for i, v := range f.locals {
			if v != undef {
				t.Errorf("%s: local %d is %v, not undefined", name, i, v)
			}
		}
		inline := len(f.locals) > 0 && &f.locals[0] == &f.inline[0]
		if inline != (name == "small") {
			t.Errorf("%s: locals inline = %v", name, inline)
		}
	}
	// arguments: the function in slot 1, the arguments after it, a vararg
	// method's rest as a tuple that does not alias the caller's slice
	must(s.Eval("function fr_va(a, rest...)\n    rest\nend"))
	fn, va := lamOf("fr_va")
	args := []any{int64(1), int64(2), int64(3)}
	got, err := vm.callLambda(va, fn, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	args[1], args[2] = int64(99), int64(99)
	if showValue(got) != "(2, 3)" {
		t.Errorf("the rest of a vararg call: %s (the caller's slice was aliased?)", showValue(got))
	}
	for _, c := range []struct{ src, want string }{
		{"fr_va(1)", "()"}, {"fr_va(1, 2)", "(2,)"}, {"fr_small(7)", "7"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
}
