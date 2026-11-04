package interp

import (
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The jl_ir_* accessors, a TypeName's field masks, jl_argument_datatype and
// the code-location ccalls, each want julia 1.12.6's (testdata/irflags.jl).
func TestIRFlagsAgainstJulia(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	tt, err := s.Eval("f(x) = x + 1; Tuple{typeof(f), Int}")
	if err != nil {
		t.Fatal(err)
	}
	null := &Pointer{Type: vm.ptrType(core.UInt64Type)}
	match, _ := vm.shims["jl_gf_invoke_lookup_worlds"](vm, []any{tt, nil, vm.World(), null, null})
	src := match.(*Struct).Fields[2].(*Struct).Fields[12]
	call := func(name expr.Symbol, a ...any) (any, error) { return vm.shims[name](vm, a) }
	for _, c := range []struct {
		name expr.Symbol
		args []any
		want string
	}{
		{"jl_ir_flag_inlining", []any{src}, "0x00"},
		{"jl_ir_flag_has_fcall", []any{src}, "0x00"},
		{"jl_ir_inlining_cost", []any{src}, "0xffff"},
		{"jl_ir_nslots", []any{src}, "2"},
		{"jl_ir_slotflag", []any{src, uint64(0)}, "0x00"},
		{"jl_ir_slotflag", []any{src, uint64(1)}, "0x08"},
		// refusals: compressed IR the VM never writes, not a CodeInfo, a
		// slot out of range
		{"jl_ir_flag_inlining", []any{"\x00"}, `!ErrorException("jl_ir_flag_inlining: malformed compressed IR")`},
		{"jl_ir_nslots", []any{int64(1)}, "!TypeError(:ccall"},
		{"jl_ir_slotflag", []any{int64(1), uint64(0)}, "!TypeError(:ccall"},
		{"jl_ir_slotflag", []any{src, uint64(2)}, `!ErrorException("jl_ir_slotflag: slot 0x0000000000000002 out of range")`},
	} {
		v, err := call(c.name, c.args...)
		check(t, string(c.name), v, err, c.want)
	}
	// julia's lowering sets has_image_globalref for f; this lowering's flag
	// is what it read in
	if v, err := call("jl_ir_flag_has_image_globalref", src); err != nil || (v != uint8(0) && v != uint8(1)) {
		t.Errorf("jl_ir_flag_has_image_globalref: %v %v", v, err)
	}
	// a flag that is not a Bool or UInt8, a cost that is not a UInt16
	bad := &Struct{Type: vm.tCodeInfo, Fields: append([]any(nil), src.(*Struct).Fields...)}
	for i, n := range vm.tCodeInfo.FieldNames() {
		if n == "inlining" || n == "inlining_cost" {
			bad.Fields[i] = int64(1)
		}
	}
	if _, err := call("jl_ir_flag_inlining", bad); err == nil {
		t.Error("an Int inlining flag: no error")
	}
	if _, err := call("jl_ir_inlining_cost", bad); err == nil {
		t.Error("an Int inlining cost: no error")
	}
	if _, err := call("jl_ir_inlining_cost", int64(1)); err == nil {
		t.Error("jl_ir_inlining_cost of an Int: no error")
	}
	// a Bool flag that is set
	set := &Struct{Type: vm.tCodeInfo, Fields: append([]any(nil), src.(*Struct).Fields...)}
	for i, n := range vm.tCodeInfo.FieldNames() {
		if n == "has_fcall" {
			set.Fields[i] = true
		}
	}
	if v, err := call("jl_ir_flag_has_fcall", set); err != nil || v != uint8(1) {
		t.Errorf("has_fcall set: %v %v", v, err)
	}
	// a field CodeInfo does not have is the VM's defect, a panic
	func() {
		defer func() {
			if recover() == nil {
				t.Error("irField of no field: no panic")
			}
		}()
		vm.irField("test", src, "no_such_field")
	}()
	// a DebugInfo's edges are DebugInfos too
	di := vm.juliaDebugInfo(&expr.DebugInfo{Def: expr.Symbol("f"), Edges: expr.Vector{&expr.DebugInfo{Def: expr.Symbol("g")}}})
	if e := di.(*Struct).Fields[2].(SVec); len(e) != 1 || e[0].(*Struct).Fields[0] != any(expr.Symbol("g")) {
		t.Errorf("juliaDebugInfo's edges: %s", showValue(di))
	}
	// an exception's trace names each statement it unwound, shortened
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'a'
	}
	_, err = s.Eval(`raiser() = Core.throw("` + string(long) + `"); raiser()`)
	if th, ok := err.(*Thrown); !ok || len(th.Trace) == 0 || !strings.HasSuffix(th.Trace[0], "…") {
		t.Errorf("trace of a long statement: %v", err)
	}

	// atomicfields and constfields, the words julia loads from them
	for _, c := range []struct {
		tn    *core.TypeName
		field int64
		want  any
	}{
		{vm.tBinding.Name, 5, uint32(22)},
		{vm.tBinding.Name, 6, uint32(1)},
		{core.Int64Type.Name, 5, nil},
	} {
		v, err := vm.kindField(c.tn, c.field)
		p, ok := v.(*Pointer)
		if err != nil || !ok {
			t.Fatalf("field %d of %s: %v %v", c.field, c.tn.Name, v, err)
		}
		var got any
		if m, ok := p.Target.(*Memory); ok {
			got = m.Elems[0]
		}
		if got != c.want {
			t.Errorf("field %d of %s: %v, julia %v", c.field, c.tn.Name, got, c.want)
		}
	}

	// jl_argument_datatype
	ty := func(src string) any {
		v, err := s.Eval(src)
		if err != nil {
			t.Fatal(src, err)
		}
		return v
	}
	for _, c := range []struct{ t, want string }{
		{"Int", "Int64"},
		{"Union{Array{Int, 1}, Array{Float64, 1}}", "Vector{Float64}"},
		{"Union{Int, String}", "nothing"},
		{"T where T<:Integer", "Integer"},
		{"Union{Int, Array{Int, 1}}", "nothing"},
		{"Union{}", "nothing"},
	} {
		v, err := call("jl_argument_datatype", ty(c.t))
		check(t, "jl_argument_datatype of "+c.t, v, err, c.want)
	}
	// julia answers Array{T, 1}, the body itself (julia shows it so, this
	// VM as Vector{T}: compared by identity)
	vecT := ty("Array{T, 1} where T")
	if v, err := call("jl_argument_datatype", vecT); err != nil || v != core.UnwrapUnionAll(vecT) {
		t.Errorf("jl_argument_datatype of Array{T, 1} where T: %v %v", v, err)
	}
	// the n-th element, as jl_nth_argument_datatype asks it
	tup := ty("Tuple{Int, Array{Int, 1}}")
	if nthArgDatatype(tup, 2) != ty("Array{Int, 1}") || nthArgDatatype(tup, 3) != nil || nthArgDatatype(core.Int64Type, 1) != nil {
		t.Error("nthArgDatatype of a tuple's elements")
	}
	// a variable answers for its upper bound; a union whose first member
	// has none answers nothing (rtutils.c:653-662)
	if nthArgDatatype(&core.TypeVar{Name: "T", LB: core.Bottom, UB: core.Int64Type}, 0) != core.Int64Type ||
		nthArgDatatype(&core.Union{A: int64(1), B: core.Int64Type}, 0) != nil {
		t.Error("nthArgDatatype of a variable or a union")
	}

	// the code-location ccalls take lowering's encoding (lower's test checks
	// it against julia); what they refuse
	locs := vm.newVector(core.Int32Type, []any{int32(5), int32(0), int32(0)})
	cl, err := call("jl_compress_codelocs", int32(3), locs, int64(1))
	if err != nil {
		t.Fatal(err)
	}
	v, err := call("jl_uncompress1_codeloc", cl, int64(1))
	check(t, "jl_uncompress1_codeloc", v, err, "(5, 0, 0)")
	for _, c := range []struct {
		name expr.Symbol
		args []any
		want string
	}{
		{"jl_compress_codelocs", []any{int64(3), locs, int64(1)}, "!not ported yet: jl_compress_codelocs of"},
		{"jl_compress_codelocs", []any{int32(3), vm.newVector(core.AnyType, []any{int64(1)}), int64(1)}, "!TypeError(:ccall"},
		{"jl_compress_codelocs", []any{int32(3), locs, int64(2)}, `!ErrorException("jl_compress_codelocs: 2 statements for 3 locations")`},
		{"jl_uncompress1_codeloc", []any{int64(1), int64(1)}, "!not ported yet: jl_uncompress1_codeloc of"},
	} {
		v, err := call(c.name, c.args...)
		check(t, string(c.name), v, err, c.want)
	}
}

// _unsetindex! through a pointer (genericmemory.jl:78-101): C_NULL over a
// boxed slot, or over a word of an inline element with pointers, leaves it
// #undef; other stores of C_NULL are ordinary stores.
func TestUnsetThroughPointer(t *testing.T) {
	vm := New()
	voidpp := vm.ptrType(vm.ptrType(core.NothingType))
	cnull := &Pointer{Type: vm.ptrType(core.NothingType)}
	mem := func(et any, elems ...any) *Memory {
		return &Memory{Type: must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), et, core.CPU)).(*core.DataType), Elems: elems}
	}
	pair := must(core.Apply(core.AnyTupleType, core.Int64Type, core.StringType)).(*core.DataType)
	boxed := mem(core.AnyType, int64(1), int64(2))
	inline := mem(pair, vm.newTuple([]any{int64(1), "a"}), vm.newTuple([]any{int64(2), "b"}))
	// the second element's second word, as _unsetindex! writes it
	if err := vm.ptrStore("atomic_pointerset", &Pointer{Type: voidpp, Target: inline, Off: 16}, cnull, 2); err != nil || inline.Elems[1] != undef || inline.Elems[0] == undef {
		t.Errorf("inline element: %v %v", inline.Elems, err)
	}
	if err := vm.ptrStore("atomic_pointerset", &Pointer{Type: voidpp, Target: boxed}, cnull, 1); err != nil || boxed.Elems[0] != undef {
		t.Errorf("boxed slot: %v %v", boxed.Elems, err)
	}
	for _, c := range []struct {
		name string
		m    *Memory
		off  int64
	}{
		{"a union", mem(core.TypeUnion(core.Int8Type, core.NothingType), nil), 0},
		{"pointer-free elements", mem(core.Int64Type, int64(1)), 0},
		{"past the end", mem(core.AnyType, int64(1)), 8},
		{"before the start", mem(core.AnyType, int64(1)), -8},
	} {
		if _, ok := vm.unsetThrough(&Pointer{Type: voidpp, Target: c.m, Off: c.off}, c.m, cnull, 1); ok {
			t.Errorf("%s: unset", c.name)
		}
	}
	// a non-null value, and C_NULL into Ptr{Cvoid} elements, are stores
	voids := mem(vm.ptrType(core.NothingType), &Pointer{Type: vm.ptrType(core.NothingType), Off: 1})
	if _, ok := vm.unsetThrough(&Pointer{Type: voidpp, Target: voids}, voids, cnull, 1); ok {
		t.Error("C_NULL into a Ptr{Cvoid} element: unset")
	}
	if _, ok := vm.unsetThrough(&Pointer{Type: voidpp, Target: boxed}, boxed, int64(1), 1); ok {
		t.Error("an Int: unset")
	}
	// a union that is not isbits is stored boxed: its slot is cleared
	u := mem(core.TypeUnion(core.Int64Type, core.StringType), int64(1))
	if k, ok := vm.unsetThrough(&Pointer{Type: voidpp, Target: u}, u, cnull, 1); !ok || k != 0 {
		t.Error("a boxed union slot: not unset")
	}
}
