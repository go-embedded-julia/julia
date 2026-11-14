package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// A method of a global function defined inside let (stmtinfo.jl:215-218):
// lowering builds its body at run time, the captured value quoted in, and
// jl_method_def makes a CodeInfo of that lambda (method.c:1246-1251).
func TestLetGlobalMethod(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	for _, c := range []struct{ src, want string }{
		{"struct MRP; x::Int; end\nlet instance = MRP(1)\n    global MRP\n    MRP() = instance\nend\nMRP()", "Main.L.MRP(1)"},
		{"MRP() === MRP()", "true"},
		{"let a = 1, b = 2.5\n    global g2\n    g2(x) = (a, b, x)\nend\ng2(:c)", "(1, 2.5, :c)"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}

	// malformed lambdas are refused, as C would read past them
	vec := func(xs ...any) expr.Vector { return expr.Vector(xs) }
	slot := vec(expr.Symbol("#self#"), nil, int64(0))
	good := func() []any {
		return []any{vec(expr.Symbol("#self#")), vec(vec(slot), vec(), int64(0), vec()),
			expr.New("block", expr.GlobalRef{Mod: "Base", Name: "x"}, expr.New("return", int64(1))), vec(int64(1), int64(1)),
			vec(expr.LineInfoNode{File: expr.Symbol("f"), Line: 1})}
	}
	if _, ok := lambdaCodeInfo(expr.New("lambda", good()...)); !ok {
		t.Fatal("a well-formed lambda is refused")
	}
	for i, bad := range []func(a []any) []any{
		func(a []any) []any { return a[:4] },
		func(a []any) []any { a[0] = int64(1); return a },
		func(a []any) []any { a[2] = expr.New("call"); return a },
		func(a []any) []any { a[1] = vec(vec(slot)); return a },
		func(a []any) []any { a[1] = vec(int64(1), vec(), int64(0)); return a },
		func(a []any) []any { a[1] = vec(vec(slot), vec(), 1.0); return a },
		func(a []any) []any { a[1] = vec(vec(int64(1)), vec(), int64(0)); return a },
		func(a []any) []any { a[1] = vec(vec(vec(int64(1), nil, int64(0))), vec(), int64(0)); return a },
		func(a []any) []any { a[3] = vec(int64(2), int64(1)); return a },
		func(a []any) []any { a[3] = vec(); return a },
		func(a []any) []any { a[4] = vec(); return a },
		func(a []any) []any { a[4] = vec(int64(1)); return a },
		func(a []any) []any { a[4] = vec(expr.LineInfoNode{File: expr.Symbol("f"), InlinedAt: 2}); return a },
	} {
		if _, ok := lambdaCodeInfo(expr.New("lambda", bad(good())...)); ok {
			t.Errorf("malformed lambda %d accepted", i)
		}
	}
}
