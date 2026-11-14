package interp

import (
	"os"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// shType prints a type with every type variable's bounds, as
// testdata/widendiagonal.golden's printer does: widen_diagonal's new
// variables keep their name, and only their bounds tell them apart.
func shType(x any) string {
	switch t := x.(type) {
	case *core.UnionAll:
		return "(A " + t.Var.Name + "[" + shType(t.Var.LB) + "," + shType(t.Var.UB) + "]." + shType(t.Body) + ")"
	case *core.TypeVar:
		return t.Name
	case *core.DataType:
		s := string(t.Name.Name)
		if len(t.Parameters) > 0 {
			ps := make([]string, len(t.Parameters))
			for i, p := range t.Parameters {
				ps[i] = shType(p)
			}
			s += "{" + strings.Join(ps, ",") + "}"
		}
		return s
	case *core.Union:
		return "U(" + shType(t.A) + "," + shType(t.B) + ")"
	case *core.Vararg:
		s := "Va("
		if t.T != nil {
			s += shType(t.T)
		}
		s += ","
		if t.N != nil {
			s += shType(t.N)
		}
		return s + ")"
	}
	return Repr(x)
}

// jl_widen_diagonal against julia 1.12.6 (testdata/widendiagonal.golden:
// the UnionAll; whether it is widened whole or its body, whose variables
// are its own, as the Compiler passes a signature; the result).
func TestWidenDiagonalAgainstJulia(t *testing.T) {
	b, err := os.ReadFile("testdata/widendiagonal.golden")
	if err != nil {
		t.Fatal(err)
	}
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	eval := func(src string) any {
		v, err := s.Eval(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		return v
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 42 {
		t.Fatalf("%d golden lines", len(lines))
	}
	for _, l := range lines {
		f := strings.Split(l, "\t")
		ua := eval(f[0]).(*core.UnionAll)
		var ty any = ua
		if f[1] == "body" {
			ty = core.UnwrapUnionAll(ua)
		}
		v, err := vm.shims["jl_widen_diagonal"](vm, []any{ty, ua})
		if err != nil {
			t.Fatal(err)
		}
		if got := shType(v); got != f[2] {
			t.Errorf("widen_diagonal of %s (%s) = %s, julia %s", f[0], f[1], got, f[2])
		}
	}
	_, err = vm.shims["jl_widen_diagonal"](vm, []any{core.Int64Type, core.Int64Type})
	check(t, "widen_diagonal of no UnionAll", nil, err, "!TypeError")
}
