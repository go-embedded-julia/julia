package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// irFixture is a VM, a session with a method to compress into, and the
// types the hand-built IR needs.
type irFixture struct {
	t    testing.TB
	vm   *VM
	s    *Session
	m    *Struct // a method of the session's module
	src  *Struct // its lowered source
	b    *Module // a module that is neither Core nor the method's
	ci   *Struct // a CodeInstance of the method, for its edges
	args map[string]any
}

func newIRFixture(t testing.TB) *irFixture {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval(`const Vector = Array{T,1} where {T}
module B
end
struct Single end
struct Two
a::Int
b::Int
end
struct Mixed
a::Int8
p::Any
c::Int32
end
mutable struct Mut
a::Any
end
f1(x) = x
f2(x, y) = tuple(x, y)`); err != nil {
		t.Fatal(err)
	}
	f := &irFixture{t: t, vm: vm, s: s, b: vm.modules["B"], args: map[string]any{}}
	tt, err := s.Eval("Tuple{typeof(f1), Int}")
	if err != nil {
		t.Fatal(err)
	}
	null := &Pointer{Type: vm.ptrType(core.UInt64Type)}
	match, err := vm.shims["jl_gf_invoke_lookup_worlds"](vm, []any{tt, nil, vm.World(), null, null})
	if err != nil || match == nil {
		t.Fatalf("no method: %v", err)
	}
	f.m = match.(*Struct).Fields[2].(*Struct)
	f.src = f.m.Fields[methodSource].(*Struct)
	mi, err := vm.specializationsGetLinfo([]any{f.m, tt, SVec{}})
	if err != nil {
		t.Fatal(err)
	}
	ci, err := vm.shims["jl_new_codeinst"](vm, []any{mi, nil, core.AnyType, core.Bottom, nil, nil, int32(0),
		uint64(1), ^uint64(0), uint32(0), nil, nil, SVec{}})
	if err != nil {
		t.Fatal(err)
	}
	f.ci = ci.(*Struct)
	return f
}

func (f *irFixture) typ(src string) any {
	f.t.Helper()
	v, err := f.s.Eval(src)
	if err != nil {
		f.t.Fatalf("%s: %v", src, err)
	}
	return v
}

func (f *irFixture) core(name string) *core.DataType { return f.vm.coreType(name).(*core.DataType) }

func (f *irFixture) new(t any, fields ...any) any {
	f.t.Helper()
	v, err := f.vm.newStruct(t, fields)
	if err != nil {
		f.t.Fatalf("new %s: %v", showValue(t), err)
	}
	return v
}

func (f *irFixture) setCI(ci *Struct, name expr.Symbol, v any) {
	for i, n := range f.vm.tCodeInfo.FieldNames() {
		if n == name {
			ci.Fields[i] = v
			return
		}
	}
	f.t.Fatalf("no CodeInfo field %s", name)
}

// code is a CodeInfo of the fixture's method with these statements, one
// ssaflag each, and the value as ssavaluetypes.
func (f *irFixture) code(stmts []any, types any) *Struct {
	ci := &Struct{Type: f.vm.tCodeInfo, Fields: append([]any(nil), f.src.Fields...)}
	flags := make([]any, len(stmts))
	for i := range flags {
		flags[i] = uint32(0)
	}
	f.setCI(ci, "code", f.vm.newVector(core.AnyType, stmts))
	f.setCI(ci, "ssaflags", f.vm.newVector(core.UInt32Type, flags))
	f.setCI(ci, "ssavaluetypes", types)
	return ci
}

func (f *irFixture) compress(ci any) (string, error) {
	f.m.Fields[methodRoots] = undef
	v, err := f.vm.shims["jl_compress_ir"](f.vm, []any{f.m, ci})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func (f *irFixture) uncompress(data string, ci any) (*Struct, error) {
	v, err := f.vm.shims["jl_uncompress_ir"](f.vm, []any{f.m, ci, data})
	if err != nil {
		return nil, err
	}
	return v.(*Struct), nil
}

// roundTrip compresses ci and uncompresses it again, the statements, the
// types and the ssaflags are what they were.
func (f *irFixture) roundTrip(name string, ci *Struct, meta any) (string, *Struct) {
	f.t.Helper()
	data, err := f.compress(ci)
	if err != nil {
		f.t.Fatalf("%s: compress: %v", name, err)
	}
	back, err := f.uncompress(data, meta)
	if err != nil {
		f.t.Fatalf("%s: uncompress: %v", name, err)
	}
	for _, field := range []expr.Symbol{"code", "ssavaluetypes", "ssaflags", "slotflags", "nargs", "slotnames"} {
		if g, w := showValue(f.vm.ciField(back, field)), showValue(f.vm.ciField(ci, field)); g != w {
			f.t.Errorf("%s: %s round trip:\n got  %.400s\n want %.400s", name, field, g, w)
		}
	}
	return data, back
}

func ssa(i int) expr.SSAValue { return expr.SSAValue(i) }

// A CodeInfo of every kind of statement and value: through jl_compress_ir
// and jl_uncompress_ir, each one is what it was.
func TestIRRoundTripKinds(t *testing.T) {
	f := newIRFixture(t)
	f.vm.baseModule = f.b
	defer func() { f.vm.baseModule = nil }()
	data, _ := f.roundTrip("kinds", f.kindsCode(), nil)
	if len(data) == 0 {
		t.Error("nothing compressed")
	}
}

// kindsCode is a CodeInfo with a statement of each kind (Base is the
// fixture's module B).
func (f *irFixture) kindsCode() *Struct {
	vm := f.vm
	phi := func(edges []any, vals []any) any {
		return f.new(f.core("PhiNode"), vm.newVector(core.Int32Type, edges), vm.newVector(core.AnyType, vals))
	}
	phic := func(vals []any) any { return f.new(f.core("PhiCNode"), vm.newVector(core.AnyType, vals)) }
	var many, edges300, vals300 []any
	for i := 0; i < 300; i++ {
		many = append(many, int64(i))
		edges300 = append(edges300, int32(i))
		vals300 = append(vals300, ssa(i%5))
	}
	vecInt := f.typ("Vector{Int}")
	stmts := []any{
		// SSA values: nearby, far, long
		&expr.Expr{Head: "call", Args: []any{ssa(0), ssa(0)}},
		&expr.Expr{Head: "call", Args: []any{ssa(0), ssa(1), ssa(1)}},
		expr.SlotNumber(3), expr.SlotNumber(70000), ssa(-1),
		// calls and Exprs, short and long
		&expr.Expr{Head: "call", Args: []any{expr.GlobalRef{Mod: "Core", Name: "tuple"}}},
		&expr.Expr{Head: "foreigncall", Args: []any{expr.Symbol("x"), int64(1)}},
		&expr.Expr{Head: "new", Args: many},
		// control flow
		expr.GotoNode(2), expr.GotoIfNot{Cond: ssa(0), Dest: 3},
		expr.EnterNode{Catch: 4}, expr.EnterNode{Catch: 4, Scope: int64(7), HasScope: true},
		expr.ReturnNode{Val: ssa(2)}, expr.Unreachable{}, expr.Argument(2),
		expr.NewvarNode{Slot: expr.SlotNumber(2)}, expr.LineNumberNode{Line: 5, File: expr.Symbol("f.jl")},
		// the SSA-only nodes
		phi([]any{int32(1), int32(-3), int32(4)}, []any{ssa(0), int64(1), nil}),
		phi(edges300, vals300),
		phi([]any{int32(1)}, []any{ssa(0), ssa(1)}),
		phic([]any{ssa(0), ssa(1)}), phic(vals300),
		f.new(f.core("PiNode"), ssa(1), core.Int64Type), f.new(f.core("UpsilonNode"), ssa(1)),
		expr.QuoteNode{Value: phi([]any{int32(1)}, []any{ssa(0)})},
		expr.QuoteNode{Value: &expr.Expr{Head: "call", Args: []any{expr.Symbol("zz")}}},
		expr.QuoteNode{Value: int64(5)}, expr.QuoteNode{Value: expr.Symbol("sym")},
		// integers and other bits
		int64(5), int64(-5), int64(300), int64(-70000), int64(5000000000), int64(-5000000000),
		int32(7), int32(-7), int32(300), int32(70000), int32(-70000), uint8(9), uint8(200),
		float64(2.5), float32(1.5), 'x', true, false, nil,
		uint16(3), uint32(3), uint64(3), int8(-3), int16(-3),
		// values
		f.new(f.typ("Single").(*core.DataType)), f.new(f.typ("Two").(*core.DataType), int64(1), int64(2)),
		f.new(f.typ("Mixed").(*core.DataType), int8(1), "s", int32(3)),
		core.Int64Type, core.AnyType, vecInt, f.typ("Ptr{Int}"), f.typ("Vector{Vector{Int}}"),
		SVec{}, SVec{int64(1), expr.Symbol("q")}, vm.newTuple(nil),
		vm.Core, f.b, f.s.Mod, "a string", expr.Symbol("not common"), expr.Symbol("getproperty"),
		expr.GlobalRef{Mod: "Core", Name: "x"}, expr.GlobalRef{Mod: f.s.Mod.Name, Name: "y"}, expr.GlobalRef{Mod: "B", Name: "z"},
		&core.Union{A: core.Int64Type, B: core.Float64Type}, f.typ("Union{Int,Float64}"),
		f.new(f.typ("Mut").(*core.DataType), int64(1)),
		vm.newVector(core.AnyType, []any{int64(1), "a"}),
	}
	return f.code(stmts, int64(len(stmts)))
}

// ssavaluetypes is written as a literal (as_literal): strings, arrays and
// memories by value, any struct as its memory.
func TestIRRoundTripLiterals(t *testing.T) {
	f := newIRFixture(t)
	vm := f.vm
	mem := func(elt any, elems []any) *Memory {
		return &Memory{Type: must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), elt, core.CPU)).(*core.DataType), Elems: elems}
	}
	stmts := []any{expr.ReturnNode{Val: int64(1)}}
	for _, c := range []struct {
		name string
		v    any
	}{
		{"string", "ssa types"},
		{"nothing", nil},
		{"vector of Any", vm.newVector(core.AnyType, []any{int64(1), core.Int64Type, "x", expr.Symbol("s")})},
		{"vector of Int", vm.newVector(core.Int64Type, []any{int64(1), int64(-2), int64(3)})},
		{"vector of UInt8", vm.newVector(core.UInt8Type, []any{uint8(1), uint8(255)})},
		{"empty vector", vm.newVector(core.Int64Type, nil)},
		{"vector of structs", vm.newVector(f.typ("Two"), []any{f.new(f.typ("Two"), int64(1), int64(2))})},
		{"memory of Any", mem(core.AnyType, []any{int64(1), "x"})},
		{"memory of UInt8", mem(core.UInt8Type, []any{uint8(1), uint8(2)})},
		{"struct", f.new(f.typ("Two"), int64(8), int64(9))},
		{"struct with a pointer", f.new(f.typ("Mixed"), int8(1), expr.Symbol("p"), int32(3))},
		{"mutable struct", f.new(f.typ("Mut"), int64(1))},
		{"union", &core.Union{A: core.Int64Type, B: core.Int8Type}},
		{"float", float64(1.25)},
		{"char", 'é'},
		{"int64", int64(1 << 40)},
	} {
		ci := f.code(stmts, c.v)
		f.roundTrip(c.name, ci, nil)
	}
	// what the encoder refuses
	for _, c := range []struct {
		name string
		v    any
		want string
	}{
		{"memory of a union", mem(f.typ("Union{Int, Nothing}"), []any{int64(1)}), "!jl_compress_ir of a Memory"},
		{"vector of zero-size", vm.newVector(f.typ("Single"), []any{f.new(f.typ("Single"))}), "!jl_compress_ir of a Memory"},
		{"vector of structs with pointers", vm.newVector(f.typ("Tuple{Int,String}"), []any{vm.newTuple([]any{int64(1), "s"})}), "!jl_compress_ir of inline elements with pointers"},
	} {
		_, err := f.compress(f.code(stmts, c.v))
		check(t, c.name, nil, err, c.want)
	}
	// a GlobalRef into a module the VM does not name
	_, err := f.compress(f.code([]any{expr.GlobalRef{Mod: "NoSuch", Name: "x"}}, int64(1)))
	check(t, "a GlobalRef", nil, err, "!a module this VM does not name")
	// a non-CodeInfo
	_, err = vm.shims["jl_compress_ir"](vm, []any{f.m, int64(1)})
	check(t, "compress of an Int", nil, err, "!TypeError(:ccall")
	_, err = vm.shims["jl_compress_ir"](vm, []any{int64(1), f.src})
	check(t, "compress of an Int method", nil, err, "!TypeError(:ccall")
}

// The header's flags, the method's own source, the slot types of an opaque
// closure, a generated function's argument names, ssaflags and the edges and
// metadata of a CodeInstance.
func TestIRRoundTripHeader(t *testing.T) {
	f := newIRFixture(t)
	vm := f.vm
	stmts := []any{&expr.Expr{Head: "call", Args: []any{ssa(0)}}, expr.ReturnNode{Val: ssa(1)}}
	ci := f.code(stmts, int64(2))
	// ssaflags set, a nargs that is not the method's, every flag bit
	flags := vm.newVector(core.UInt32Type, []any{uint32(1), uint32(0x10)})
	f.setCI(ci, "ssaflags", flags)
	f.setCI(ci, "nargs", uint64(7))
	for _, n := range []expr.Symbol{"propagate_inbounds", "has_fcall", "has_image_globalref", "nospecializeinfer", "isva"} {
		f.setCI(ci, n, true)
	}
	f.setCI(ci, "inlining", uint8(2))
	f.setCI(ci, "constprop", uint8(1))
	f.setCI(ci, "purity", uint16(0x123))
	f.setCI(ci, "inlining_cost", uint16(77))
	f.setCI(ci, "slotflags", vm.newVector(core.UInt8Type, []any{uint8(1), uint8(2), uint8(3)}))
	data, back := f.roundTrip("header", ci, nil)
	for _, n := range []expr.Symbol{"propagate_inbounds", "has_fcall", "has_image_globalref", "nospecializeinfer", "isva", "inlining", "constprop", "purity", "inlining_cost"} {
		if g, w := vm.ciField(back, n), vm.ciField(ci, n); g != w {
			t.Errorf("%s: %v, want %v", n, g, w)
		}
	}
	for _, c := range []struct {
		f    string
		want string
	}{
		{"jl_ir_flag_inlining", "0x02"}, {"jl_ir_flag_has_fcall", "0x01"}, {"jl_ir_flag_has_image_globalref", "0x01"},
		{"jl_ir_inlining_cost", "0x004d"}, {"jl_ir_nslots", "3"},
	} {
		v, err := vm.shims[expr.Symbol(c.f)](vm, []any{data})
		check(t, c.f, v, err, c.want)
	}
	// an opaque closure keeps its slot types, a generated function its names
	f.m.Fields[methodIsOpaque] = true
	f.setCI(ci, "slottypes", vm.newVector(core.AnyType, []any{core.Int64Type, core.AnyType}))
	_, back = f.roundTrip("opaque", ci, nil)
	if g, w := showValue(vm.ciField(back, "slottypes")), showValue(vm.ciField(ci, "slottypes")); g != w {
		t.Errorf("slottypes: %s, want %s", g, w)
	}
	f.m.Fields[methodIsOpaque] = false
	saved := f.m.Fields[methodGenerator]
	f.m.Fields[methodGenerator] = true
	f.setCI(ci, "slotnames", vm.newVector(core.SymbolType, []any{expr.Symbol("#self#"), expr.Symbol("x"), expr.Symbol("y")}))
	data, back = f.roundTrip("generated", ci, nil)
	if g, w := showValue(vm.ciField(back, "slotnames")), showValue(vm.ciField(ci, "slotnames")); g != w {
		t.Errorf("slotnames: %s, want %s", g, w)
	}
	f.m.Fields[methodGenerator] = saved

	// a CodeInstance's edges, rettype and worlds; the CodeInfo's own are not kept
	f.ci.Fields[ciRettype] = core.Int64Type
	f.ci.Fields[ciMinWorld] = uint64(4)
	f.ci.Fields[ciMaxWorld] = uint64(9)
	edge := f.new(f.typ("Mut"), int64(1))
	long := make(SVec, 300)
	for i := range long {
		long[i] = f.new(f.typ("Mut"), int64(i))
	}
	f.ci.Fields[ciEdges] = long
	f.setCI(ci, "edges", long)
	statements := []any{edge, long[0], long[299], long[299], &expr.Expr{Head: "call", Args: []any{ssa(0)}}}
	ci2 := f.code(statements, int64(len(statements)))
	f.setCI(ci2, "edges", long)
	data, back = f.roundTrip("edges", ci2, f.ci)
	if got := vm.ciField(back, "rettype"); got != any(core.Int64Type) {
		t.Errorf("rettype: %v", got)
	}
	if got := vm.ciField(back, "max_world"); got != any(uint64(9)) {
		t.Errorf("max_world: %v", got)
	}
	_, _, rs, _ := arrayOf(f.m.Fields[methodRoots])
	if len(rs) != 1 {
		t.Errorf("%d roots, the value that is no edge is one", len(rs))
	}
	// a CodeInstance of an ABIOverride
	abi := f.new(f.typ("Core.ABIOverride"), f.typ("Tuple{Int}"), f.ci.Fields[ciDef])
	f.ci.Fields[ciDef] = abi
	_, back = f.roundTrip("abi override", ci2, f.ci)
	if got := vm.ciField(back, "parent"); got == nil {
		t.Error("parent")
	}
	// the source of the method itself, a CodeInfo of the lowering, a bad metadata
	v, err := vm.shims["jl_compress_ir"](vm, []any{f.m, nil})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.uncompress(v.(string), nil); err != nil {
		t.Error(err)
	}
	low := codeInfo([]any{expr.ReturnNode{Val: int64(1)}})
	if _, err := vm.shims["jl_compress_ir"](vm, []any{f.m, low}); err != nil {
		t.Error(err)
	}
	_, err = vm.shims["jl_uncompress_ir"](vm, []any{f.m, f.m, v})
	check(t, "metadata", nil, err, "!TypeError(:ccall")
	_, err = vm.shims["jl_uncompress_ir"](vm, []any{f.m, nil, int64(1)})
	check(t, "data", nil, err, "!TypeError(:ccall")
	_, err = vm.shims["jl_uncompress_ir"](vm, []any{int64(1), nil, v})
	check(t, "method", nil, err, "!TypeError(:ccall")
	// a CodeInfo uncompresses to itself
	same, err := vm.shims["jl_uncompress_ir"](vm, []any{f.m, nil, ci})
	if err != nil || same != any(ci) {
		t.Errorf("a CodeInfo: %v", err)
	}
}
