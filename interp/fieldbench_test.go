package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// The cost of getfield by name and by index and of setfield! on a mutable
// struct, the interpreter's commonest builtins after the intrinsics.
func BenchmarkFieldAccess(b *testing.B) {
	vm := New()
	s := vm.NewSession()
	if _, err := s.Eval("mutable struct FB; a::Int; b::Any; end"); err != nil {
		b.Fatal(err)
	}
	x, err := s.Eval("FB(1, 2)")
	if err != nil {
		b.Fatal(err)
	}
	get, set := vm.Core.own("getfield").Value, vm.Core.own("setfield!").Value
	byName := []any{x, expr.Symbol("b")}
	byIndex := []any{x, int64(1)}
	store := []any{x, expr.Symbol("a"), int64(3)}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vm.apply(get, byName); err != nil {
			b.Fatal(err)
		}
		if _, err := vm.apply(get, byIndex); err != nil {
			b.Fatal(err)
		}
		if _, err := vm.apply(set, store); err != nil {
			b.Fatal(err)
		}
	}
}
