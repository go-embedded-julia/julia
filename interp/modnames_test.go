package interp

import (
	"slices"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// jl_module_names lists what julia 1.12.6 lists for the same module
// (names_oracle.jl: [:X, :a, :b]; with all, the globals and constants too,
// #h included; with imported, the imports; with usings, the names brought
// in by `using A: x` and those of used modules' exports, a name twice when
// it comes twice), and a name cannot be both public and exported.
func TestModuleNames(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	src := `module Y
export y1
y1 = 1
y2 = 2
end
module X
import Core: tuple
using ..Y: y2
using ..Y
export a
public b
a = 1
b = 2
c = 3
const d = 4
var"#h" = 5
global e
end
i32(x) = Core.Intrinsics.trunc_int(Int32, x)
names(m, all, imported, usings) = ccall(:jl_module_names, Core.Array{Symbol,1}, (Any, Int32, Int32, Int32), m, i32(all), i32(imported), i32(usings))
nothing`
	if _, err := s.Eval(src); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		{"names(X, 0, 0, 0)", "[:X, :a, :b]"},
		{"names(X, 1, 0, 0)", `[Symbol("#h"), :X, :a, :b, :c, :d, :e]`},
		{"names(X, 0, 1, 0)", "[:X, :a, :b, :tuple]"},
		{"names(Y, 0, 0, 0)", "[:Y, :y1]"},
		{"names(1, 0, 0, 0)", "!TypeError(:ccall"},
		{"Core.eval(X, Expr(:public, :a))", `!cannot declare X.a public; it is already declared exported`},
		{"Core.eval(X, Expr(:export, :b))", `!cannot declare X.b exported; it is already declared public`},
		{"Core.eval(X, Expr(:export, :a)); names(X, 0, 0, 0)", "[:X, :a, :b]"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
	// Main lists its globals as public, eval and include aside; a deprecated
	// or hidden name only with all
	m := vm.Main
	for _, name := range []expr.Symbol{"g1", "eval", "include", "#g2"} {
		m.declare(name, 0).Value = int64(1)
	}
	dep := m.declare("g3", 0)
	dep.Value, dep.deprecations = int64(1), []depChange{{0, bkFlagDeprecated}}
	v, err := vm.moduleNames([]any{m, int32(0), int32(0), int32(0)})
	if err != nil {
		t.Fatal(err)
	}
	_, _, got, _ := arrayOf(v)
	seen := map[any]bool{}
	for _, x := range got {
		seen[x] = true
	}
	if !seen[expr.Symbol("g1")] || seen[expr.Symbol("eval")] || seen[expr.Symbol("include")] || seen[expr.Symbol("#g2")] || seen[expr.Symbol("g3")] {
		t.Errorf("names(Main): %v", got)
	}
	// with usings: the explicit y2, then the used modules' exports, the last
	// used first (Y, then Core)
	x := must(s.Eval("X")).(*Module)
	v, _ = vm.moduleNames([]any{x, int32(0), int32(0), int32(1)})
	_, _, us, _ := arrayOf(v)
	want := []any{expr.Symbol("X"), expr.Symbol("a"), expr.Symbol("b"), expr.Symbol("y2"), expr.Symbol("Y"), expr.Symbol("y1")}
	if len(us) < 6 || !slices.Equal(us[:6], want) {
		t.Errorf("names with usings: %v", us)
	}
	// import A binds A as an import; using A binds it in Main only
	for _, c := range []struct{ src, want string }{
		{"module Z; import ..Y; end; names(Z, 1, 0, 0)", "[:Z]"},
		{"names(Z, 0, 1, 0)", "[:Y, :Z]"},
		{"Core.eval(Main, Expr(:using, Expr(:., :L, :Y))); names(Main, 0, 1, 0)", "[:L, :Main, :Y, :g1]"},
		{"names(Main, 0, 0, 0)", "[:L, :Main, :g1]"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
	// a deprecated export of a used module is listed with all only
	ym := must(s.Eval("Y")).(*Module)
	ym.own("y1").deprecations = []depChange{{0, bkFlagDeprecated}}
	for _, all := range []int32{0, 1} {
		v, _ := vm.moduleNames([]any{x, all, int32(0), int32(1)})
		_, _, names, _ := arrayOf(v)
		has := false
		for _, n := range names {
			has = has || n == any(expr.Symbol("y1"))
		}
		if has != (all == 1) {
			t.Errorf("all=%d: y1 listed %v", all, has)
		}
	}
}
