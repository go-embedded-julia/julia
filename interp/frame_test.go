package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

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
