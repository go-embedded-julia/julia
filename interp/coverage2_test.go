package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

func memType(et any) *core.DataType {
	return must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), et, core.CPU)).(*core.DataType)
}

// A Binding's backedges: kept by the first edge from another module, not
// repeated for the same edge twice in a row, read through getfield and
// isdefined; an edge from the binding's own module marks the method.
func TestBindingBackedges(t *testing.T) {
	f := newCodeinstFixture(t)
	vm := f.vm
	b := f.s.Mod.own("f")
	if b == nil {
		t.Fatal("no binding of f")
	}
	v, err := biGetfield(vm, []any{b, expr.Symbol("backedges")})
	check(t, "no backedges yet", v, err, "!UndefRefError()")
	if d, _ := biIsdefined(vm, []any{b, expr.Symbol("backedges")}); d != false {
		t.Error("isdefined backedges before the first")
	}
	vm.addBindingBackedge(b, int64(1))
	vm.addBindingBackedge(b, int64(1)) // the last one again
	vm.addBindingBackedge(b, int64(2))
	vm.addBindingBackedge(b, int64(1))
	if _, _, es, _ := arrayOf(b.backedges); len(es) != 3 {
		t.Errorf("%d backedges, want 3", len(es))
	}
	v, err = biGetfield(vm, []any{b, expr.Symbol("backedges")})
	if err != nil || v != any(b.backedges) {
		t.Errorf("getfield backedges: %v %v", v, err)
	}
	if d, _ := biIsdefined(vm, []any{b, expr.Symbol("backedges")}); d != true {
		t.Error("isdefined backedges after")
	}

	other := vm.Core.own("Int")
	if other == nil {
		t.Fatal("no binding of Int")
	}
	sh := vm.shims["jl_maybe_add_binding_backedge"]
	for _, c := range []struct {
		args []any
		want string
	}{
		{[]any{int64(1), int64(1), f.meth}, "!TypeError(:ccall"},
		{[]any{b, int64(1), int64(1)}, "!TypeError(:ccall"},
		{[]any{b, int64(1), b}, "!TypeError(:ccall"},
		{[]any{b, nil, f.meth}, "0"},
		{[]any{b, undef, f.meth}, "0"},
		{[]any{other, int64(7), f.meth}, "0"},
		{[]any{b, int64(8), f.meth}, "1"},
	} {
		v, err := sh(vm, c.args)
		check(t, "jl_maybe_add_binding_backedge", v, err, c.want)
	}
	if b.flags&bindingFlagAnyImplicitEdges == 0 || f.meth.Fields[methodDidScanSource].(uint8)&2 == 0 {
		t.Errorf("an edge from the binding's own module did not mark it: %d %v", b.flags, f.meth.Fields[methodDidScanSource])
	}
	if _, _, es, _ := arrayOf(other.backedges); len(es) != 1 {
		t.Errorf("the other module's binding: %d backedges", len(es))
	}
}

// An exported, implicit binding that no module declares resolves to nothing,
// and two modules that use each other for it do not loop.
func TestLookupThroughUsingsCycle(t *testing.T) {
	a, b, m := newModule("A", nil), newModule("B", nil), newModule("M", nil)
	a.usings, b.usings, m.usings = []*Module{b}, []*Module{a}, []*Module{a}
	for _, mod := range []*Module{a, b} {
		x := mod.declare("x", 0)
		x.implicit, x.Exports = true, true
	}
	if got := m.lookup("x", 10); got != nil {
		t.Errorf("a name no module declares: %v", got)
	}
	// declared in B: found through the cycle
	bx := b.own("x")
	bx.Value, bx.implicit = int64(1), false
	if got := m.lookup("x", 10); got != bx {
		t.Errorf("a name B declares: %v", got)
	}
}

// fieldsFree of a type without a field list: a singleton has no fields to
// restrict it.
func TestFieldsFreeOfSingletons(t *testing.T) {
	if !fieldsFree(core.NothingType, map[*core.DataType]bool{}, false) {
		t.Error("Nothing is identity-free")
	}
	// a concrete type that records no field list (made by hand: core gives
	// every type one now): free only when it is a singleton
	inc := core.NewIncompleteType("Main", "NoFields", nil, false, false, nil)
	inc.IsConcrete = true
	if fieldsFree(inc, map[*core.DataType]bool{}, false) {
		t.Error("a type without a field list is free")
	}
	inc.Name.Singleton = true
	if !fieldsFree(inc, map[*core.DataType]bool{}, true) {
		t.Error("a singleton without a field list is not free")
	}
}

// sizeof of a union memory, and of a value whose type has no layout.
func TestSizeofCorners(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	um := must(biMemorynew(vm, []any{memType(core.TypeUnion(core.Int8Type, core.NothingType)), int64(3)}))
	v, err := biSizeof(vm, []any{um})
	check(t, "sizeof a union memory", v, err, "6")
	fn, err := s.Eval("h() = 1; h")
	if err != nil {
		t.Fatal(err)
	}
	v, err = biSizeof(vm, []any{fn})
	check(t, "sizeof a function", v, err, "0")
	_, err = biSizeof(vm, []any{vm.Core})
	check(t, "sizeof a module", nil, err, "!sizeof of a Module")
}

// jl_cstr_to_string of a String and of a memory of bytes.
func TestCstrToString(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	u8p := vm.ptrType(core.UInt8Type)
	m := &Memory{Type: memType(core.UInt8Type), Elems: []any{uint8('a'), uint8('b'), uint8(0), uint8('c')}}
	short := &Memory{Type: memType(core.UInt8Type), Elems: []any{uint8('a')}}
	i32 := &Memory{Type: memType(core.Int32Type), Elems: []any{int32(1)}}
	for _, c := range []struct {
		name string
		p    any
		want string
	}{
		{"a String", &Pointer{Type: u8p, Target: "hello\x00world"}, `"hello"`},
		{"a String past its NUL", &Pointer{Type: u8p, Target: "hello", Off: 5}, `""`},
		{"a String's offset", &Pointer{Type: u8p, Target: "hello", Off: 2}, `"llo"`},
		{"a String's bad offset", &Pointer{Type: u8p, Target: "hello", Off: 6}, "!invalid pointer"},
		{"a String's negative offset", &Pointer{Type: u8p, Target: "hello", Off: -1}, "!invalid pointer"},
		{"a memory", &Pointer{Type: u8p, Target: m}, `"ab"`},
		{"a memory from its middle", &Pointer{Type: u8p, Target: m, Off: 3}, "!jl_cstr_to_string of"},
		{"a memory without NUL", &Pointer{Type: u8p, Target: short}, "!jl_cstr_to_string of"},
		{"a negative offset", &Pointer{Type: u8p, Target: m, Off: -1}, "!jl_cstr_to_string of"},
		{"a memory of Int32", &Pointer{Type: u8p, Target: i32}, "!jl_cstr_to_string of"},
		{"another target", &Pointer{Type: u8p}, "!jl_cstr_to_string of"},
		{"no pointer", int64(1), "!TypeError(:ccall"},
	} {
		v, err := vm.cstrToString([]any{c.p})
		check(t, c.name, v, err, c.want)
	}
}

// A memory of primitive elements read and written as bytes: through a
// pointer of another primitive type, and by memset.
func TestMemoryBytes(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval("primitive type P16 16 end"); err != nil {
		t.Fatal(err)
	}
	p16 := must(s.Eval("P16")).(*core.DataType)
	u32 := memType(core.UInt32Type)
	newMem := func(elems ...any) *Memory { return &Memory{Type: u32, Elems: elems} }
	u8p := vm.ptrType(core.UInt8Type)

	// stores and loads of bytes, through UInt8 pointers into UInt32s
	m := newMem(uint32(0x04030201), uint32(0x08070605))
	for _, c := range []struct {
		name string
		f    func(*VM, []any) (any, error)
		args []any
		want string
	}{
		{"load a byte", biPointerref, []any{&Pointer{Type: u8p, Target: m}, int64(3), int64(1)}, "0x03"},
		{"store a byte", biPointerset, []any{&Pointer{Type: u8p, Target: m}, uint8(0xff), int64(2), int64(1)}, ""},
		{"load a byte across", biPointerref, []any{&Pointer{Type: u8p, Target: m, Off: 3}, int64(1), int64(1)}, "0x04"},
		{"store past the end", biPointerset, []any{&Pointer{Type: u8p, Target: m}, uint8(1), int64(9), int64(1)}, "!pointerset: invalid pointer"},
		{"load past the end", biPointerref, []any{&Pointer{Type: u8p, Target: m}, int64(9), int64(1)}, "!pointerref: invalid pointer"},
		{"store of another type", biPointerset, []any{&Pointer{Type: u8p, Target: m}, int64(1), int64(1), int64(1)}, "!TypeError(:pointerset"},
	} {
		v, err := c.f(vm, c.args)
		check(t, c.name, v, err, c.want)
	}
	if m.Elems[0] != uint32(0x0403ff01) {
		t.Errorf("after the byte store: %#x", m.Elems[0])
	}
	// an element that is no value: uninitialized bits read as zeros, bytes
	// the VM cannot take are refused
	m.Elems[1] = undef
	v, err := biPointerref(vm, []any{&Pointer{Type: u8p, Target: m, Off: 4}, int64(1), int64(1)})
	check(t, "an uninitialized element", v, err, "0x00")
	m.Elems[1] = "not bits"
	_, err = biPointerref(vm, []any{&Pointer{Type: u8p, Target: m, Off: 4}, int64(1), int64(1)})
	check(t, "an element that is not bits", nil, err, "!the bits of a")
	_, err = biPointerset(vm, []any{&Pointer{Type: u8p, Target: m, Off: 4}, uint8(1), int64(1), int64(1)})
	check(t, "a store over an element that is not bits", nil, err, "!the bits of a")
	// a load of a primitive type the VM cannot make from bytes
	pm := &Memory{Type: memType(core.UInt8Type), Elems: []any{uint8(1), uint8(2)}}
	_, err = vm.memLoadBytes("pointerref", pm, 0, p16)
	check(t, "a load of a custom primitive", nil, err, "!as ")
	_, err = vm.memLoadBytes("pointerref", pm, 1, p16)
	check(t, "a load past the end", nil, err, "!invalid pointer")
	// the stores' refusals
	err = vm.memStoreBytes("pointerset", m, 0, "x")
	check(t, "a store of a string", nil, err, "!pointerset of a")
	err = vm.memStoreBytes("pointerset", newMem(uint32(0)), 2, uint32(1))
	check(t, "a store past the end", nil, err, "!invalid pointer")
	pe := &Memory{Type: memType(p16), Elems: []any{undef, undef}}
	err = vm.memStoreBytes("pointerset", pe, 0, uint16(1))
	check(t, "a store into a custom primitive", nil, err, "!into a Memory")
	// memIndex of a pointer whose type is not the memory's
	_, err = vm.memIndex("pointerref", &Pointer{Type: u8p, Target: m}, m, core.Int64Type, 1)
	check(t, "memIndex of another type", nil, err, "!pointer into a Memory")

	// memset: whole elements, a range from the middle of one, bytes of a
	// memory of a primitive that cannot be made from bytes, a String, and
	// what it refuses
	m = newMem(uint32(0), uint32(0), uint32(0))
	p := &Pointer{Type: u8p, Target: m}
	_, err = vm.memset([]any{p, int32(0xab), uint64(12)})
	check(t, "memset whole", nil, err, "")
	if m.Elems[2] != uint32(0xabababab) {
		t.Errorf("memset whole: %#x", m.Elems[2])
	}
	m.Elems[0], m.Elems[1] = uint32(0), uint32(0)
	_, err = vm.memset([]any{&Pointer{Type: u8p, Target: m, Off: 1}, int32(0x11), uint64(6)})
	check(t, "memset partial", nil, err, "")
	if m.Elems[0] != uint32(0x11111100) || m.Elems[1] != uint32(0x00111111) {
		t.Errorf("memset partial: %#x %#x", m.Elems[0], m.Elems[1])
	}
	_, err = vm.memset([]any{p, int32(1), uint64(13)})
	check(t, "memset past the end", nil, err, "!invalid pointer")
	_, err = vm.memset([]any{&Pointer{Type: u8p, Target: pe}, int32(1), uint64(2)})
	check(t, "memset of a custom primitive", nil, err, "!into a Memory")
	str, err := vm.allocString([]any{int64(4)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = vm.memset([]any{&Pointer{Type: u8p, Target: str.(string)}, int32('A'), uint64(4)})
	check(t, "memset of a String", nil, err, "")
	if str != any("AAAA") {
		t.Errorf("memset of a String: %q", str)
	}
	_, err = vm.memset([]any{&Pointer{Type: u8p, Target: "literal"}, int32('A'), uint64(2)})
	check(t, "memset of a String the VM did not make", nil, err, "!memset of")
	_, err = vm.memset([]any{int64(1), int32(1), uint64(1)})
	check(t, "memset of no pointer", nil, err, "!memset of")
}

// A module made after the bootstrap defines its own include and eval; a type
// for include that cannot be made from the module is an error.
func TestEvalModuleWithABrokenIncludeInto(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	// the VM has no Base after Boot: Core stands in for it
	vm.baseModule = vm.Core
	base := vm.baseRelativeTo(vm.Main)
	if _, err := vm.sessionFor(base).Eval("struct IncludeInto\na\nb\nend"); err != nil {
		t.Fatal(err)
	}
	_, err := vm.NewSession().Eval("module Fresh\nend")
	check(t, "a module with a broken IncludeInto", nil, err, "!invalid struct allocation")
}
