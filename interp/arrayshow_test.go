package interp

import (
	"strings"
	"testing"
)

// Arrays are shown as julia 1.12.6 shows them: each want is its repr of the
// same array. Core has no setindex! before Base, so a helper fills them
// through memoryrefset!.
func TestShowArrays(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.CoreSession()
	if _, err := s.Eval(`
function fillv!(a, i, xs)
    Core.Intrinsics.slt_int(Core.nfields(xs), i) && return a
    Core.memoryrefset!(Core.memoryrefnew(getfield(a, :ref), i, true), getfield(xs, i), :not_atomic, true)
    fillv!(a, Core.Intrinsics.add_int(i, 1), xs)
end
function fillm!(m, i, xs)
    Core.Intrinsics.slt_int(Core.nfields(xs), i) && return m
    Core.memoryrefset!(Core.memoryrefnew(Core.memoryrefnew(m), i, true), getfield(xs, i), :not_atomic, true)
    fillm!(m, Core.Intrinsics.add_int(i, 1), xs)
end
v(T, xs...) = fillv!(Array{T,1}(undef, Core.nfields(xs)), 1, xs)
`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		{"fillm!(Memory{Int64}(undef, 2), 1, (1, 2))", "[1, 2]"},
		{"Memory{Int64}(undef, 0)", "Int64[]"},
		{"a = Array{Any,1}(undef, 3); Core.memoryrefset!(Core.memoryrefnew(getfield(a, :ref), 2, true), 1, :not_atomic, true); a", "Any[#undef, 1, #undef]"},
		{"v(Int8, Int8(1), Int8(2))", "Int8[1, 2]"},
		{"v(Float32, 1f0, 2.5f0)", "Float32[1.0, 2.5]"},
		{"v(Float64, 1.5, 2.0)", "[1.5, 2.0]"},
		{"fillv!(Array{Int64,2}(undef, 2, 3), 1, (1, 4, 2, 5, 3, 6))", "[1 2 3; 4 5 6]"},
		{"v(Int64)", "Int64[]"},
		{"Array{Int64,2}(undef, 0, 3)", "Matrix{Int64}(undef, 0, 3)"},
		{"v(Array{Int64,1}, v(Int64, 1, 2), v(Int64, 3))", "[[1, 2], [3]]"},
		{`v(Any, 1, "a")`, `Any[1, "a"]`},
		{"v(UInt8, UInt8(1), UInt8(255))", "UInt8[0x01, 0xff]"},
		{"v(Symbol, :a, :b)", "[:a, :b]"},
		{"v(Char, 'a', 'b')", "['a', 'b']"},
		{`v(String, "x", "y")`, `["x", "y"]`},
		{"v(Union{Int64, Nothing}, 1, nothing)", "Union{Nothing, Int64}[1, nothing]"},
		{"v(Nothing, nothing, nothing)", "[nothing, nothing]"},
		{"v(Tuple{Int64,Int64}, (1, 2), (3, 4))", "[(1, 2), (3, 4)]"},
		{"v(Bool, true, false)", "Bool[1, 0]"},
		{"v(Real, 1, 2.0)", "Real[1, 2.0]"},
		{"fillv!(Array{Int64,3}(undef, 2, 2, 2), 1, (1, 2, 3, 4, 5, 6, 7, 8))", "[1 3; 2 4;;; 5 7; 6 8]"},
		{"fillv!(Array{Int64,0}(undef, ()), 1, (7,))", "fill(7)"},
		{"Array{Any,0}(undef, ())", "Array{Any, 0}(UndefInitializer())"},
		{"v(Array{Int8,1}, v(Int8, Int8(1)))", "Vector{Int8}[[1]]"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
	// julia shows a reference's address; the index stands in for it here
	v, err := s.Eval("Core.memoryrefnew(Core.memoryrefnew(fillm!(Memory{Int64}(undef, 2), 1, (1, 2))), 2, true)")
	check(t, "a MemoryRef", v, err, "MemoryRef{Int64}(2, [1, 2])")
	// julia: Float32[1.0f32, NaN, Inf]
	v, err = s.Eval("v(Float32, 1f32, Core.Intrinsics.div_float(0f0, 0f0), Core.Intrinsics.div_float(1f0, 0f0))")
	check(t, "Float32 specials", v, err, "Float32[1.0f32, NaN, Inf]")
	if !strings.HasPrefix(Repr(SVec{}), "svec") {
		t.Error("svec")
	}
}
