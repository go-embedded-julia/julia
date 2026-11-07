package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The shims of String, memory and Cstring arguments that ccall.go registers.
func TestStringMemoryShims(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	sh := func(name string, args ...any) (any, error) { return vm.shims[expr.Symbol(name)](vm, args) }
	u8mem := func(elems ...any) *Memory { return &Memory{Type: memUInt8Type, Elems: elems} }

	// jl_string_to_genericmemory, and back
	v, err := sh("jl_string_to_genericmemory", "ab")
	check(t, "string to memory", v, err, "UInt8[0x61, 0x62]")
	_, err = sh("jl_string_to_genericmemory", int64(1))
	check(t, "string to memory of an Int", nil, err, "!TypeError(:ccall")
	m := u8mem(uint8('a'), uint8('b'), uint8('c'))
	v, err = sh("jl_genericmemory_to_string", m, int64(2))
	check(t, "memory to string", v, err, `"ab"`)
	if len(m.Elems) != 0 {
		t.Error("jl_genericmemory_to_string leaves the memory its elements")
	}
	v, err = sh("jl_genericmemory_to_string", u8mem(uint8(1)), int64(0))
	check(t, "memory to string of nothing", v, err, `""`)
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"an Int", []any{int64(1), int64(1)}, "!jl_genericmemory_to_string of"},
		{"a memory of another type", []any{&Memory{Type: memUInt16Type}, int64(0)}, "!jl_genericmemory_to_string of"},
		{"a negative length", []any{u8mem(uint8(1)), int64(-1)}, "!jl_genericmemory_to_string of"},
		{"a length past the memory", []any{u8mem(uint8(1)), int64(2)}, "!jl_genericmemory_to_string of"},
		{"a length that is no Int", []any{u8mem(uint8(1)), "1"}, "!jl_genericmemory_to_string of"},
		{"an element left undefined", []any{u8mem(uint8(1), undef), int64(2)}, "!UndefRefError"},
	} {
		v, err := sh("jl_genericmemory_to_string", c.args...)
		check(t, c.name, v, err, c.want)
	}
	// jl_pchar_to_string of a String and of a memory of bytes
	v, err = sh("jl_pchar_to_string", f.str("abc"), int64(2))
	check(t, "pchar of a String", v, err, `"ab"`)
	mem := u8mem(uint8('x'), uint8('y'), uint8('z'))
	v, err = sh("jl_pchar_to_string", &Pointer{Type: f.u8p, Target: mem, Off: 1}, int64(2))
	check(t, "pchar of a memory", v, err, `"yz"`)
	for _, c := range []struct {
		name string
		args []any
	}{
		{"past the memory", []any{&Pointer{Type: f.u8p, Target: mem, Off: 2}, int64(2)}},
		{"a negative length", []any{&Pointer{Type: f.u8p, Target: mem}, int64(-1)}},
		{"a memory of another type", []any{&Pointer{Type: f.u8p, Target: &Memory{Type: memUInt16Type}}, int64(0)}},
		{"no pointer", []any{int64(1), int64(1)}},
		{"a length that is no Int", []any{&Pointer{Type: f.u8p, Target: mem}, "1"}},
	} {
		v, err := sh("jl_pchar_to_string", c.args...)
		check(t, "pchar of "+c.name, v, err, "!jl_pchar_to_string of")
	}
	if v, _ := sh("jl_genericmemory_owner", mem); v != any(mem) {
		t.Error("jl_genericmemory_owner is the memory")
	}
	if v, _ := sh("jl_object_id", int64(7)); v != vm.objectID(int64(7)) {
		t.Error("jl_object_id is objectID")
	}

	// the parser's character classes, the operator tables, strlen: each takes
	// a Cstring, as a pointer or as the address bitcast from one
	cs := must(f.s.Eval("PCStr")).(*core.DataType)
	cstr := func(s string) any {
		addr, err := vm.addressOf(f.str(s + "\x00"))
		if err != nil {
			t.Fatal(err)
		}
		return PrimValue{cs, from64(addr)}
	}
	for _, c := range []struct {
		name string
		arg  any
		want string
	}{
		{"jl_is_operator", f.str("+"), "1"},
		{"jl_is_operator", cstr("+"), "1"},
		{"jl_is_operator", cstr("abc"), "0"},
		{"jl_is_unary_operator", cstr("-"), "1"},
		{"jl_is_unary_and_binary_operator", cstr("-"), "1"},
		{"jl_is_unary_and_binary_operator", cstr("*"), "0"},
		{"jl_is_syntactic_operator", cstr("="), "1"},
		{"jl_is_syntactic_operator", cstr("+"), "0"},
		{"jl_operator_precedence", cstr("+"), "11"},
		{"jl_operator_precedence", cstr("abc"), "0"},
		{"strlen", cstr("abc"), "0x0000000000000003"},
		{"strlen", f.str("abcd"), "0x0000000000000004"},
		{"strlen", f.str(""), "0x0000000000000000"},
		{"strlen", int64(1), "!strlen of"},
		{"jl_is_operator", int64(1), "!ccall(:jl_is_operator) of"},
		{"jl_is_unary_operator", PrimValue{cs, from64(12345)}, "!ccall(:jl_is_unary_operator) of"},
		{"jl_is_syntactic_operator", "x", "!ccall(:jl_is_syntactic_operator) of"},
		{"jl_operator_precedence", float64(1), "!ccall(:jl_operator_precedence) of"},
	} {
		v, err := sh(c.name, c.arg)
		check(t, c.name, v, err, c.want)
	}
	// strcmp's sign, the bytes compared unsigned as C does ("\xe9" > "z")
	for _, c := range []struct {
		a, b any
		want string
	}{
		{cstr("abc"), cstr("abd"), "-1"}, {cstr("abc"), cstr("abc"), "0"}, {cstr("b"), cstr("abc"), "1"},
		{cstr("\xe9"), cstr("z"), "1"}, {f.str("ab"), cstr("a"), "1"},
		{cstr("a"), int64(1), "!strcmp of"},
	} {
		v, err := sh("strcmp", c.a, c.b)
		check(t, "strcmp", v, err, c.want)
	}
	for _, c := range []struct {
		name string
		arg  any
		want string
	}{
		{"jl_id_start_char", uint32('a'), "1"}, {"jl_id_start_char", uint32('1'), "0"},
		{"jl_id_char", uint32('1'), "1"}, {"jl_id_char", uint32(' '), "0"},
		{"jl_op_suffix_char", uint32(0x2032), "1"}, {"jl_op_suffix_char", uint32('a'), "0"},
		{"jl_id_char", int64(1), "!TypeError(:ccall"},
		{"jl_alignment", uint64(0), "8"}, {"jl_alignment", uint64(1), "16"},
		{"jl_threadid", nil, "0"},
	} {
		v, err := sh(c.name, c.arg)
		check(t, c.name, v, err, c.want)
	}
	if v, err := sh("jl_get_julia_bindir"); err != nil || v != any(vm.BinDir) {
		t.Errorf("jl_get_julia_bindir: %v %v", v, err)
	}
	v, err = sh("jl_gc_active_impl")
	check(t, "jl_gc_active_impl", v, err, "Ptr{UInt8}(\"Built with stock GC\" + 0)")
}

// memmove, memcmp and memset between Strings and memories of bytes, and
// their refusals.
func TestStringMemoryBytes(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	u8mem := func(elems ...any) *Memory { return &Memory{Type: memUInt8Type, Elems: elems} }
	mp := func(m *Memory, off int64) *Pointer { return &Pointer{Type: f.u8p, Target: m, Off: off} }
	// a String into a memory, a memory into a String the VM made, a memory
	// into a memory
	dst := u8mem(uint8(0), uint8(0), uint8(0), uint8(0))
	if _, err := vm.memmove([]any{mp(dst, 1), f.str("xyz"), uint64(3)}); err != nil || joinBytes(dst) != "0,120,121,122" {
		t.Errorf("a String into a memory: %v %s", err, joinBytes(dst))
	}
	str := must(vm.allocString([]any{int64(3)})).(string)
	if _, err := vm.memmove([]any{&Pointer{Type: f.u8p, Target: str}, mp(dst, 1), uint64(3)}); err != nil || str != "xyz" {
		t.Errorf("a memory into a String: %v %q", err, str)
	}
	src := u8mem(uint8('a'), uint8('b'))
	if _, err := vm.memmove([]any{mp(dst, 0), mp(src, 0), uint64(2)}); err != nil || joinBytes(dst) != "97,98,121,122" {
		t.Errorf("a memory into a memory: %v %s", err, joinBytes(dst))
	}
	for _, c := range []struct {
		name string
		args []any
	}{
		{"a String past its end into a memory", []any{mp(dst, 0), f.str("xy"), uint64(3)}},
		{"a memory with an undefined byte", []any{&Pointer{Type: f.u8p, Target: str}, mp(u8mem(uint8(1), undef), 0), uint64(2)}},
		{"a memory past its end", []any{&Pointer{Type: f.u8p, Target: str}, mp(src, 1), uint64(2)}},
		{"a String into a String the VM did not make", []any{f.str("lit"), f.str("abc"), uint64(2)}},
		{"no pointer", []any{int64(1), f.str("a"), uint64(1)}},
	} {
		_, err := vm.memmove(c.args)
		check(t, c.name, nil, err, "!memmove of")
	}
	// the destination fails after the bytes were found
	_, err := vm.memmove([]any{mp(dst, 3), f.str("xyz"), uint64(3)})
	check(t, "a store past the memory", nil, err, "!invalid pointer")
	_, err = vm.memmove([]any{f.str("lit"), mp(src, 0), uint64(2)})
	check(t, "a memory into a String the VM did not make", nil, err, "!invalid pointer")

	// memcmp: memories, Strings, and what it refuses
	for _, c := range []struct {
		name string
		p, q any
		n    uint64
		want string
	}{
		{"equal", mp(src, 0), f.str("ab"), 2, "0"},
		{"less", mp(src, 0), f.str("ac"), 2, "-1"},
		{"greater", f.str("b"), mp(src, 0), 1, "1"},
		{"a memory against itself", mp(src, 1), mp(src, 1), 1, "0"},
		{"past the memory", mp(src, 0), f.str("abc"), 3, "!memcmp of"},
		// a memory of another primitive type is read as its bytes: UInt16[0x6261]
		// is "ab", little-endian
		{"a memory of UInt16", &Pointer{Type: f.u8p, Target: &Memory{Type: memUInt16Type, Elems: []any{uint16(0x6261)}}}, f.str("ab"), 2, "0"},
		{"past a memory of UInt16", &Pointer{Type: f.u8p, Target: &Memory{Type: memUInt16Type, Elems: []any{uint16(0x6261)}}}, f.str("abc"), 3, "!memcmp of"},
		{"a memory of Any", &Pointer{Type: f.u8p, Target: &Memory{Type: memAnyType, Elems: []any{int64(1)}}}, f.str("a"), 1, "!memcmp of"},
		{"a negative offset", mp(src, -1), f.str("a"), 1, "!memcmp of"},
		{"an undefined byte", mp(u8mem(undef), 0), f.str("a"), 1, "!memcmp of"},
		{"no pointer", int64(1), f.str("a"), 1, "!memcmp of"},
		{"another target", &Pointer{Type: f.u8p}, f.str("a"), 1, "!memcmp of"},
	} {
		v, err := vm.memcmp([]any{c.p, c.q, c.n})
		check(t, c.name, v, err, c.want)
	}
	_, err = vm.memcmp([]any{mp(src, 0), mp(src, 0), int64(1)})
	check(t, "a count that is no UInt", nil, err, "!memcmp of")

	// memset over an element the VM cannot read the bytes of
	words := &Memory{Type: memUInt32Type, Elems: []any{"x", uint32(0)}}
	_, err = vm.memset([]any{&Pointer{Type: f.u8p, Target: words, Off: 1}, int32(1), uint64(2)})
	check(t, "memset over a word that is no bits", nil, err, "!the bits of a")
}

// A pointer into a C global (cglobal's slot), and reads and writes of
// bytes of memories whose elements are pointers.
func TestPointerSlotsAndBytes(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	i64p := vm.ptrType(core.Int64Type)
	slot := &ptrSlot{name: "g", val: int64(1)}
	p := &Pointer{Type: i64p, Target: slot}
	for _, c := range []struct {
		name string
		p    *Pointer
		x    any
		i    int64
		want string
	}{
		{"the slot", p, int64(5), 1, ""},
		{"a second element", p, int64(5), 2, "!invalid pointer"},
		{"an offset", &Pointer{Type: i64p, Target: slot, Off: 8}, int64(5), 1, "!invalid pointer"},
		{"another type", p, "x", 1, "!TypeError(:pointerset"},
	} {
		_, err := biPointerset(vm, []any{c.p, c.x, c.i, int64(1)})
		check(t, c.name, nil, err, c.want)
	}
	if slot.val != any(int64(5)) {
		t.Errorf("the slot: %v", slot.val)
	}
	// a slot of Any takes any value
	anySlot := &ptrSlot{name: "h"}
	if _, err := biPointerset(vm, []any{&Pointer{Type: vm.ptrType(core.AnyType), Target: anySlot}, "s", int64(1), int64(1)}); err != nil || anySlot.val != any("s") {
		t.Errorf("a slot of Any: %v %v", err, anySlot.val)
	}

	// a memory of pointers read and written as bytes: bytes cannot be made a
	// pointer
	ptrMem := &Memory{Type: memType(i64p), Elems: []any{undef, undef}}
	u8p := vm.ptrType(core.UInt8Type)
	_, err := biPointerset(vm, []any{&Pointer{Type: u8p, Target: ptrMem}, uint8(1), int64(1), int64(1)})
	check(t, "a byte into a memory of pointers", nil, err, "!into a Memory")
	bytes := &Memory{Type: memUInt8Type, Elems: make([]any, 16)}
	for i := range bytes.Elems {
		bytes.Elems[i] = uint8(0)
	}
	_, err = biPointerref(vm, []any{&Pointer{Type: i64p, Target: bytes}, int64(1), int64(1)})
	check(t, "bytes as an Int64", nil, err, "")
	pp := vm.ptrType(i64p)
	_, err = biPointerref(vm, []any{&Pointer{Type: pp, Target: bytes}, int64(1), int64(1)})
	check(t, "bytes as a pointer", nil, err, "!as ")
}

// User primitive types named like the VM's own, and the conversions to them.
func TestConversionsToLookalikeTypes(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval("primitive type IntrinsicFunction 32 end"); err != nil {
		t.Fatal(err)
	}
	lookalike := must(s.Eval("IntrinsicFunction")).(*core.DataType)
	if lookalike == vm.intrinsicType {
		t.Fatal("the user's type is the VM's")
	}
	for _, c := range []struct {
		f    expr.Symbol
		args []any
	}{
		{"bitcast", []any{lookalike, int32(1)}},
		{"zext_int", []any{lookalike, uint8(1)}},
		{"fptosi", []any{lookalike, 1.5}},
	} {
		v, err := vm.apply(intrinsic(t, vm, c.f), c.args)
		check(t, string(c.f), v, err, "!not ported yet")
	}
	// PrimValues are egal and hash alike by their type and bits
	a, b := PrimValue{lookalike, from64(1)}, PrimValue{lookalike, from64(1)}
	if vm.objectID(a) != vm.objectID(b) || vm.objectID(a) == vm.objectID(PrimValue{lookalike, from64(2)}) {
		t.Error("objectID of a PrimValue")
	}
}
