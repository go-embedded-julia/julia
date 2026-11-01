package interp

import "testing"

// Types are shown as julia 1.12.6 shows them; each want is its repr of the
// same type.
func TestShowTypes(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.CoreSession()
	for _, c := range []struct{ src, want string }{
		{"Array{Int64,1}", "Vector{Int64}"},
		{"Array{T,1} where T", "Vector"},
		{"Array{Int64,3}", "Array{Int64, 3}"},
		{"Array{Int64,2}", "Matrix{Int64}"},
		{"Core.GenericMemory{:not_atomic,Int64,Core.CPU}", "Memory{Int64}"},
		{"Core.GenericMemory{:atomic,Int64,Core.CPU}", "AtomicMemory{Int64}"},
		{"Core.GenericMemoryRef{:not_atomic,Int64,Core.CPU}", "MemoryRef{Int64}"},
		{"AbstractArray{Int64,1}", "AbstractVector{Int64}"},
		{"AbstractArray{Int64,2}", "AbstractMatrix{Int64}"},
		{"DenseArray{Int64,1}", "DenseVector{Int64}"},
		{"DenseArray{Int64,2}", "DenseMatrix{Int64}"},
		{"Array{Array{Int64,1},1}", "Vector{Vector{Int64}}"},
		{"Tuple{Array{Int64,1}}", "Tuple{Vector{Int64}}"},
		{"Core.GenericMemory{:not_atomic,T,Core.CPU} where T", "Memory"},
		{"Array{T,2} where T<:Integer", "Matrix{T} where T<:Integer"},
		{"Array{Int64, N} where N", "Array{Int64}"},
		{"Union{Array{Int64,1}, Int8}", "Union{Int8, Vector{Int64}}"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
}
