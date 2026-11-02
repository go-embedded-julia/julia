package interp

import "testing"

// The cost of :new (jl_new_structv) of an immutable struct, a mutable one
// and a singleton, which gives its one instance.
func BenchmarkNewStruct(b *testing.B) {
	vm := New()
	s := vm.NewSession()
	if _, err := s.Eval("struct NI; a::Int; b::Any; end; mutable struct NM; a::Int; end; struct NS end"); err != nil {
		b.Fatal(err)
	}
	var types []any
	for _, name := range []string{"NI", "NM", "NS"} {
		t, err := s.Eval(name)
		if err != nil {
			b.Fatal(err)
		}
		types = append(types, t)
	}
	args := [][]any{{int64(1), int64(2)}, {int64(1)}, nil}
	b.ReportAllocs()
	for b.Loop() {
		for i, t := range types {
			if _, err := vm.newStruct(t, args[i]); err != nil {
				b.Fatal(err)
			}
		}
	}
}
