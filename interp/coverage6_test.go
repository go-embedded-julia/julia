package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// foreach_top_nth_typename (gf.c:804-863): the top type names a signature's
// nth argument reaches, and what it learns on the way. The wants follow the C.
func TestForeachTopNthTypename(t *testing.T) {
	f := newCodeinstFixture(t)
	vm := f.vm
	typ := func(src string) any {
		v, err := f.s.Eval(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		return v
	}
	fn := vm.typeOf(typ("f")).(*core.DataType)
	kwcallT := vm.typeOf(vm.Core.own("kwcall").Value).(*core.DataType)
	numberName := core.NumberType.Name
	names := func(a any, n int) ([]*core.TypeName, uint) {
		var got []*core.TypeName
		var facts uint
		vm.foreachTopNthTypename(func(tn *core.TypeName) { got = append(got, tn) }, a, n, &facts)
		return got, facts
	}
	for _, c := range []struct {
		name  string
		a     any
		n     int
		names []*core.TypeName
		facts uint
	}{
		{"a function's type", typ("Tuple{typeof(f), Int}"), 1, []*core.TypeName{fn.Name}, haveFunction},
		{"an Int, up to Number", typ("Tuple{typeof(f), Int}"), 2, []*core.TypeName{numberName}, 0},
		{"Type{Int}", typ("Tuple{typeof(f), Type{Int}}"), 2, []*core.TypeName{numberName}, haveType},
		{"Type{Function}", typ("Tuple{Type{Function}}"), 1, nil, haveType | exactlyType},
		{"Type{Any}", typ("Tuple{Type{Any}}"), 1, nil, haveType | exactlyType},
		{"Function", typ("Tuple{Function}"), 1, nil, haveFunction | exactlyFunction},
		{"Any", typ("Tuple{Any}"), 1, nil, exactlyAny},
		{"typeof(kwcall)", core.TupleType(kwcallT), 1, nil, haveKwcall},
		{"Type{typeof(kwcall)}", typ("Tuple{Type{typeof(Core.kwcall)}}"), 1, nil, haveType | exactlyKwcall},
		{"a short tuple", typ("Tuple{}"), 1, nil, shortTuple},
		{"a short tuple, the second", typ("Tuple{Int}"), 2, nil, shortTuple},
		{"a TypeVar's bound", must(core.NewTypeVar("T", core.Bottom, core.Int64Type)), 0, []*core.TypeName{numberName}, 0},
		{"a UnionAll's body", typ("Tuple{Int,T} where T"), 1, []*core.TypeName{numberName}, 0},
		{"a Union's members", typ("Tuple{Union{Int,String}}"), 1, []*core.TypeName{numberName, core.AbstractStringType.Name}, 0},
		{"what is no type", int64(1), 1, nil, 0},
		{"a tuple type as an argument", typ("Tuple{Tuple{Int}}"), 1, []*core.TypeName{core.AnyTupleType.Name}, 0},
		{"a function's subtype's walk", typ("Tuple{Core.Builtin}"), 1, []*core.TypeName{vm.coreType("Builtin").(*core.DataType).Name}, haveFunction},
	} {
		got, facts := names(c.a, c.n)
		if facts != c.facts || len(got) != len(c.names) {
			t.Errorf("%s: %d names, facts %b; want %d, %b", c.name, len(got), facts, len(c.names), c.facts)
			continue
		}
		for i := range got {
			if got[i] != c.names[i] {
				t.Errorf("%s: name %d is %s, want %s", c.name, i, got[i].Name, c.names[i].Name)
			}
		}
	}
	// a kwcall signature is keyed on its third argument too
	var keyed []*core.TypeName
	vm.foreachTopTypenameFor(func(tn *core.TypeName) { keyed = append(keyed, tn) },
		core.TupleType(kwcallT, core.Int64Type, fn))
	if len(keyed) != 1 || keyed[0] != fn.Name {
		t.Errorf("a kwcall signature: %v", keyed)
	}
}

// jl_method_table_add_backedge: an edge under the top type name of a
// signature, once, a type equal to one already recorded reused.
func TestMethodTableAddBackedge(t *testing.T) {
	f := newCodeinstFixture(t)
	vm := f.vm
	typ := func(src string) any {
		v, err := f.s.Eval(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		return v
	}
	fn := vm.typeOf(typ("f")).(*core.DataType)
	ci2 := f.newCI(vm.World(), nil)
	ti := typ("Tuple{typeof(f), Int}")
	add := func(ty, ci any) {
		t.Helper()
		if _, err := f.sh("jl_method_table_add_backedge", ty, ci); err != nil {
			t.Fatal(err)
		}
	}
	add(ti, f.ci)
	add(ti, f.ci) // the same edge again: once
	if e := vm.mtBackedges[fn.Name]; len(e) != 2 || e[0] != ti || e[1] != any(f.ci) {
		t.Fatalf("one edge: %d entries", len(e))
	}
	// another caller of an equal type reuses the type
	equal := typ("Tuple{typeof(f), Int} where {T}")
	add(equal, ci2)
	e := vm.mtBackedges[fn.Name]
	if len(e) != 4 || e[2] != ti || e[3] != any(ci2) {
		t.Fatalf("a second caller: %d entries", len(e))
	}
	// the same caller, the same type again: still once
	add(equal, ci2)
	if len(vm.mtBackedges[fn.Name]) != 4 {
		t.Errorf("a repeated edge was added: %d", len(vm.mtBackedges[fn.Name]))
	}
	// another type is a new edge
	add(typ("Tuple{typeof(f), Int8}"), f.ci)
	if len(vm.mtBackedges[fn.Name]) != 6 {
		t.Errorf("another type: %d entries", len(vm.mtBackedges[fn.Name]))
	}
}

// The shims the Base load reaches: jl_new_structv, the rounding mode,
// _dyld_image_count, jl_widen_diagonal and uv_cwd. Each want is julia
// 1.12.6's where julia answers.
func TestLoaderShims(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	if _, err := f.s.Eval(`struct Two; a::Int; b::Int; end
struct One; a::Int; end
struct Zero; end
mutable struct MPart; a::Int; b::Int; MPart() = new(); end`); err != nil {
		t.Fatal(err)
	}
	mem := &Memory{Type: memAnyType, Elems: []any{int64(1), int64(2)}}
	args := &Pointer{Type: vm.ptrType(core.AnyType), Target: mem}
	null := &Pointer{Type: vm.ptrType(core.AnyType)}
	for _, c := range []struct {
		t    string
		args any
		n    any
		want string
	}{
		{"Two", args, uint32(2), "Main.L.Two(1, 2)"},
		{"Zero", null, uint32(0), "Main.L.Zero()"},
		{"One", args, uint32(1), "Main.L.One(1)"},
		{"Two", args, uint32(1), "!invalid struct allocation"},
		{"Two", args, uint32(3), "!invalid struct allocation"},
		{"One", args, uint32(2), "!invalid struct allocation"},
		{"Int", args, uint32(1), "!invalid struct allocation"}, // as julia 1.12.6
		{"Integer", args, uint32(0), "!TypeError(:new"},
		{"Two", args, int64(2), "!TypeError(:ccall"},
		{"Two", args, uint32(1 << 31), "!TypeError(:ccall"},
		{"Two", int64(1), uint32(2), "!jl_new_structv of"},
	} {
		v, err := f.sh("jl_new_structv", must(f.s.Eval(c.t)), c.args, c.n)
		if err == nil {
			if got := showValue(v); got != c.want && "Main.L."+got != c.want {
				t.Errorf("new %s: %s, julia %s", c.t, got, c.want)
			}
			continue
		}
		check(t, c.t, v, err, c.want)
	}
	// the rounding mode: to nearest only
	v, err := f.sh("jl_get_fenv_rounding")
	check(t, "jl_get_fenv_rounding", v, err, "0")
	v, err = f.sh("jl_set_fenv_rounding", int32(0))
	check(t, "jl_set_fenv_rounding of 0", v, err, "0")
	_, err = f.sh("jl_set_fenv_rounding", int32(1))
	check(t, "jl_set_fenv_rounding of 1", nil, err, "!the rounding mode")
	// no host library is loaded (julia answers a positive count)
	v, err = f.sh("_dyld_image_count")
	check(t, "_dyld_image_count", v, err, "0x00000000")
	_, err = f.sh("jl_widen_diagonal", core.Int64Type, int64(1))
	check(t, "jl_widen_diagonal of no UnionAll", nil, err, "!TypeError(:ccall")
	ua := must(f.s.Eval("Tuple{T,T} where T")).(*core.UnionAll)
	w, err := f.sh("jl_widen_diagonal", core.UnwrapUnionAll(ua), ua)
	if err != nil || w == any(ua) {
		t.Errorf("jl_widen_diagonal: %v %v", w, err)
	}
	// uv_cwd: the working directory, with the size it needs
	vm.WorkDir = "/work"
	ref, st := f.ref("RefU64", uint64(16))
	buf, m := f.buf(16, 0xaa)
	v, err = f.sh("uv_cwd", buf, ref)
	if err != nil || v != any(int32(0)) || st.Fields[0] != any(uint64(5)) || joinBytes(m)[:13] != "47,119,111,114,107,0"[:13] {
		t.Errorf("uv_cwd: %v %v %v %s", v, err, st.Fields[0], joinBytes(m))
	}
	ref, st = f.ref("RefU64", uint64(2))
	v, err = f.sh("uv_cwd", buf, ref)
	if err != nil || v != any(int32(-55)) || st.Fields[0] != any(uint64(6)) {
		t.Errorf("uv_cwd with no room: %v %v %v", v, err, st.Fields[0])
	}
}

// A pointer in compressed IR is its address in the VM's address space,
// decoded back to what it points into; C_NULL stays C_NULL.
func TestIRPointerImages(t *testing.T) {
	f := newIRFixture(t)
	vm := f.vm
	u8p := vm.ptrType(core.UInt8Type)
	mem := &Memory{Type: memUInt8Type, Elems: []any{uint8(1), uint8(2), uint8(3)}}
	stmts := []any{
		&Pointer{Type: u8p},                                                // C_NULL
		&Pointer{Type: u8p, Off: 12},                                       // an integer made a pointer
		&Pointer{Type: u8p, Target: mem, Off: 1},                           // into a memory
		&Pointer{Type: vm.ptrType(core.Int64Type), Target: "text", Off: 2}, // into a String
	}
	_, back := f.roundTrip("pointers", f.code(stmts, int64(len(stmts))), nil)
	_, _, got, _ := arrayOf(vm.ciField(back, "code"))
	if p := got[0].(*Pointer); p.Target != nil || p.Off != 0 {
		t.Errorf("C_NULL: %v", p)
	}
	if p := got[1].(*Pointer); p.Target != nil || p.Off != 12 {
		t.Errorf("an integer: %v", p)
	}
	if p := got[2].(*Pointer); p.Target != any(mem) || p.Off != 1 {
		t.Errorf("into a memory: %v", showValue(p))
	}
	if p := got[3].(*Pointer); p.Target != any("text") || p.Off != 2 {
		t.Errorf("into a String: %v", showValue(p))
	}
	// a pointer before its object has no address
	_, err := f.compress(f.code([]any{&Pointer{Type: u8p, Target: mem, Off: -1}}, int64(1)))
	check(t, "a pointer before its object", nil, err, "!not ported yet: the address of")
	// and one that is no pointer of its type
	_, err = f.compress(f.code([]any{int64(1)}, &Pointer{Type: u8p}))
	check(t, "a pointer as ssavaluetypes", nil, err, "")
}

// The field descriptor kinds of a layout: 8, 16 and 32 bits wide, julia
// 1.12.6's Base.datatype_fielddesc_type (0, 1, 2).
func TestFielddescKinds(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	if _, err := f.s.Eval(`struct L1; size::UInt32; nfields::UInt32; npointers::UInt32; firstptr::Int32; alignment::UInt16; flags::UInt16; end
struct Two; a::Int; b::Int; end
struct Big; a::NTuple{30000,UInt8}; b::NTuple{30000,UInt8}; c::NTuple{30000,UInt8}; d::NTuple{30000,UInt8}; end
struct Mid; a::NTuple{20000,UInt8}; b::NTuple{20000,UInt8}; c::Int8; end
struct Wide; a::NTuple{40000,UInt8}; b::Int8; end`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		kind int
	}{{"Two", 0}, {"Mid", 1}, {"Wide", 2}, {"Big", 2}} {
		v, err := f.s.Eval("Core.Intrinsics.pointerref(Core.Intrinsics.bitcast(Ptr{L1}, getfield(" + c.name + ", :layout)), 1, 1)")
		if err != nil {
			t.Fatal(err)
		}
		flags := v.(*Struct).Fields[5].(uint16)
		if got := int(flags>>1) & 3; got != c.kind {
			t.Errorf("%s: field descriptor kind %d, julia %d", c.name, got, c.kind)
		}
	}
	_ = vm
	_ = expr.Symbol("")
}
