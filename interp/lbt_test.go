package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// libblastrampoline's configuration, read the way lbt.jl reads it (its
// structs mirrored here without Base): before a forward no library, after
// lbt_forward of OpenBLAS the one julia 1.12.6 reports (BLAS.get_config():
// [ILP64] libopenblas64_.dylib, build flags 0x1a, no exported symbol yet);
// the thread count is what was last set.
func TestLBT(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	setup := `
primitive type CStr 64 end
struct lbt_library_info_t
    libname::CStr
    handle::Ptr{Nothing}
    suffix::CStr
    active_forwards::Ptr{UInt8}
    interface::Int32
    complex_retstyle::Int32
    f2c::Int32
    cblas::Int32
end
struct lbt_config_t
    loaded_libs::Ptr{Ptr{lbt_library_info_t}}
    build_flags::UInt32
    exported_symbols::Ptr{CStr}
    num_exported_symbols::UInt32
end
const LBT = "@rpath/libblastrampoline.5.dylib"
cfg() = Core.Intrinsics.pointerref(ccall((:lbt_get_config, LBT), Ptr{lbt_config_t}, ()), 1, 1)
libp(i) = Core.Intrinsics.pointerref(getfield(cfg(), :loaded_libs), i, 1)
lib(i) = Core.Intrinsics.pointerref(libp(i), 1, 1)
isnull(p) = Core.Intrinsics.bitcast(UInt, p) === UInt(0)
str(c) = ccall(:jl_cstr_to_string, Ref{String}, (Ptr{UInt8},), Core.Intrinsics.bitcast(Ptr{UInt8}, c))
fwd(p, clear) = ccall((:lbt_forward, LBT), Int32, (Ptr{UInt8}, Int32, Int32, Ptr{UInt8}), ccall(:jl_string_ptr, Ptr{UInt8}, (Any,), p), clear, Int32(0), Core.Intrinsics.bitcast(Ptr{UInt8}, 0))
nothing`
	if _, err := s.Eval(setup); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		{"getfield(cfg(), :build_flags)", "0x0000001a"},
		{"getfield(cfg(), :num_exported_symbols)", "0x00000000"},
		{"isnull(libp(1))", "true"},
		{`fwd("/julia/lib/julia/libopenblas64_.dylib", Int32(1))`, "0"},
		{"str(getfield(lib(1), :libname))", `"/julia/lib/julia/libopenblas64_.dylib"`},
		{"str(getfield(lib(1), :suffix))", `"64_"`},
		{"getfield(lib(1), :interface)", "64"},
		{"getfield(lib(1), :complex_retstyle)", "0"},
		{"getfield(lib(1), :f2c)", "0"},
		{"getfield(lib(1), :cblas)", "-1"},
		{"Core.Intrinsics.pointerref(getfield(lib(1), :active_forwards), 1, 1)", "0x00"},
		{"isnull(libp(2))", "true"},
		{"isnull(libp(1))", "false"},
		// a second forward without clear appends; a forward of no library
		// forwards nothing; with clear the list starts over
		{`fwd("@rpath/libgfortran.5.dylib", Int32(0))`, "0"},
		{`str(getfield(lib(2), :libname))`, `"/julia/lib/julia/libgfortran.5.dylib"`},
		{`fwd("libnope", Int32(1))`, "0"},
		{`fwd("libgfortran", Int32(1))`, "0"},
		{`str(getfield(lib(1), :libname))`, `"/julia/lib/julia/libgfortran.5.dylib"`},
		{"isnull(libp(2))", "true"},
		// a library with Go functions forwards them
		{`fwd("libdSFMT", Int32(1))`, "1"}, {"ccall((:lbt_get_num_threads, LBT), Int32, ())", "1"},
		{"ccall((:lbt_set_num_threads, LBT), Nothing, (Int32,), Int32(4)); ccall((:lbt_get_num_threads, LBT), Int32, ())", "4"},
		{"ccall((:lbt_set_num_threads, LBT), Nothing, (Int64,), 9); ccall((:lbt_get_num_threads, LBT), Int32, ())", "4"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
}

// What cValue cannot build is refused.
func TestCValueRefusals(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		t, g any
	}{
		{"a type that is no DataType", core.Bottom, int32(1)},
		{"fields for a struct of another arity", vm.ptrType(core.Int32Type), cStruct{int32(1)}},
		{"an array as no pointer", core.Int32Type, cArray{}},
		{"NULL as no pointer", core.Int32Type, nil},
		{"a bad element", vm.ptrType(vm.ptrType(core.Int32Type)), cArray{cArray{"x"}}},
	} {
		_, err := vm.cValue(c.t, c.g)
		if err == nil {
			t.Errorf("%s: no error", c.name)
		}
	}
}

// jl_ptr_to_array_1d wraps the elements a pointer into a memory reaches,
// sharing them; any other pointer is refused.
func TestPtrToArray1d(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	mem := &Memory{Type: memType(core.UInt8Type), Elems: []any{uint8(1), uint8(2), uint8(3)}}
	vec := must(core.Apply(core.ArrayType, core.UInt8Type, int64(1)))
	p := &Pointer{Type: vm.ptrType(core.UInt8Type), Target: mem, Off: 1}
	v, err := vm.ptrToArray1d([]any{vec, p, uint64(2), int32(0)})
	check(t, "two bytes from the second", v, err, "UInt8[0x02, 0x03]")
	mem.Elems[2] = uint8(9)
	check(t, "sharing them", v, nil, "UInt8[0x02, 0x09]")
	for _, c := range []struct {
		name string
		a    []any
		want string
	}{
		{"past the end", []any{vec, p, uint64(3), int32(0)}, "!jl_ptr_to_array_1d: invalid pointer"},
		{"a huge count", []any{vec, p, uint64(1) << 63, int32(0)}, "!jl_ptr_to_array_1d: invalid pointer"},
		{"another element type", []any{must(core.Apply(core.ArrayType, core.Int8Type, int64(1))), p, uint64(1), int32(0)}, "!not ported yet: jl_ptr_to_array_1d"},
		{"no memory", []any{vec, vm.stringPtrOf("ab"), uint64(1), int32(0)}, "!not ported yet: jl_ptr_to_array_1d"},
		{"no array type", []any{core.Int8Type, p, uint64(1), int32(0)}, "!not ported yet: jl_ptr_to_array_1d"},
		{"a signed count", []any{vec, p, int64(1), int32(0)}, "!not ported yet: jl_ptr_to_array_1d"},
	} {
		v, err := vm.shims["jl_ptr_to_array_1d"](vm, c.a)
		check(t, c.name, v, err, c.want)
	}
	// an offset between elements
	w := &Pointer{Type: vm.ptrType(core.UInt16Type), Target: &Memory{Type: memType(core.UInt16Type), Elems: []any{uint16(1)}}, Off: 1}
	_, err = vm.ptrToArray1d([]any{must(core.Apply(core.ArrayType, core.UInt16Type, int64(1))), w, uint64(0), int32(0)})
	check(t, "between elements", nil, err, "!jl_ptr_to_array_1d: invalid pointer")
}
