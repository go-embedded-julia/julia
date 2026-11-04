package interp

import (
	"runtime"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Pointers into memories and to layouts. Each layout want is julia
// 1.12.6's unsafe_load(convert(Ptr{Base.DataTypeLayout}, T.layout)), read
// here through a struct of the same six fields.
func TestMemoryPointers(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.CoreSession()
	if _, err := s.Eval(`struct L1; size::UInt32; nfields::UInt32; npointers::UInt32; firstptr::Int32; alignment::UInt16; flags::UInt16; end
struct P2; a::Int8; b::Any; c::Int64; end
lay(T) = Core.Intrinsics.pointerref(Core.Intrinsics.bitcast(Ptr{L1}, getfield(T, :layout)), 1, 1)`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		{"lay(Memory{Int64})", "L1(0x00000008, 0x00000000, 0x00000001, -1, 0x0008, 0x0005)"},
		{"lay(Memory{Any})", "L1(0x00000008, 0x00000000, 0x00000001, -1, 0x0008, 0x000d)"},
		{"lay(Memory{Union{Int8, Nothing}})", "L1(0x00000001, 0x00000000, 0x00000001, -1, 0x0001, 0x0015)"},
		{"lay(Memory{Int32})", "L1(0x00000004, 0x00000000, 0x00000001, -1, 0x0004, 0x0005)"},
		{"lay(Int64)", "L1(0x00000008, 0x00000000, 0x00000000, -1, 0x0008, 0x0080)"},
		{"lay(P2)", "L1(0x00000018, 0x00000003, 0x00000001, 1, 0x0008, 0x0001)"},
		{"lay(Tuple{Int8, Int64})", "L1(0x00000010, 0x00000002, 0x00000000, -1, 0x0008, 0x0081)"},
		// julia: Number.layout == C_NULL
		{"Core.Intrinsics.bitcast(UInt, getfield(Number, :layout))", "0x0000000000000000"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}

	// reading and writing elements through pointers, moving them
	i32 := must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.Int32Type, core.CPU)).(*core.DataType)
	m := must(biMemorynew(vm, []any{i32, int64(3)})).(*Memory)
	p := &Pointer{Type: vm.ptrType(core.Int32Type), Target: m}
	for _, c := range []struct {
		name string
		f    func(*VM, []any) (any, error)
		args []any
		want string
	}{
		{"pointerset", biPointerset, []any{p, int32(7), int64(2), int64(1)}, ""},
		{"pointerref", biPointerref, []any{p, int64(2), int64(1)}, "7"},
		{"pointerref past the end", biPointerref, []any{p, int64(4), int64(1)}, "!pointerref: invalid pointer"},
		// the bytes of Int32[0, 7], little-endian: 7 << 32
		{"pointerref as another type", biPointerref, []any{&Pointer{Type: vm.ptrType(core.Int64Type), Target: m}, int64(1), int64(1)}, "30064771072"},
		{"pointerref misaligned", biPointerref, []any{&Pointer{Type: p.Type, Target: m, Off: 2}, int64(1), int64(1)}, "!pointerref: invalid pointer"},
		{"pointerset of another type", biPointerset, []any{p, int64(7), int64(1), int64(1)}, "!TypeError(:pointerset, \"\", Int32, 7)"},
		{"pointerset of a non-pointer", biPointerset, []any{int64(1), int32(1), int64(1), int64(1)}, "!TypeError(:pointerset"},
		{"pointerset of a non-Int index", biPointerset, []any{p, int32(1), "1", int64(1)}, "!TypeError(:pointerset"},
		{"pointerset of a non-Int align", biPointerset, []any{p, int32(1), int64(1), "1"}, "!TypeError(:pointerset"},
		{"pointerset past the end", biPointerset, []any{p, int32(1), int64(9), int64(1)}, "!pointerset: invalid pointer"},
		{"pointerset of no memory", biPointerset, []any{&Pointer{Type: p.Type}, int32(1), int64(1), int64(1)}, "!pointerset: invalid pointer"},
		{"add_ptr", ptrArith("add_ptr", 1), []any{p, uint64(4)}, "Ptr{Int32}(pointer(Memory{Int32}) + 4)"},
		{"sub_ptr", ptrArith("sub_ptr", -1), []any{&Pointer{Type: p.Type, Target: m, Off: 8}, uint64(4)}, "Ptr{Int32}(pointer(Memory{Int32}) + 4)"},
		{"add_ptr of a non-pointer", ptrArith("add_ptr", 1), []any{int64(1), uint64(4)}, "!TypeError(:add_ptr"},
		{"add_ptr of a non-UInt", ptrArith("add_ptr", 1), []any{p, int64(4)}, "!TypeError(:add_ptr"},
	} {
		v, err := c.f(vm, c.args)
		check(t, c.name, v, err, c.want)
	}

	// jl_get_fenv_consts writes julia's arm64 values (Base.Rounding.JL_FE_*)
	fe := must(biMemorynew(vm, []any{i32, int64(9)})).(*Memory)
	if _, err := vm.fenvConsts([]any{&Pointer{Type: p.Type, Target: fe}}); err != nil {
		t.Fatal(err)
	}
	if got := Repr(fe); got != "Int32[16, 8, 4, 2, 1, 0, 4194304, 8388608, 12582912]" {
		t.Errorf("fenv consts: %s", got)
	}
	for _, args := range [][]any{{int64(1)}, {&Pointer{Type: p.Type}}, {&Pointer{Type: p.Type, Target: m}}} {
		if _, err := vm.fenvConsts(args); err == nil {
			t.Errorf("jl_get_fenv_consts of %v", args)
		}
	}

	// ptr_or_offset of an isbits-Union memory is the index; addresses
	// round-trip through integers
	u := must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.TypeUnion(core.Int8Type, core.NothingType), core.CPU)).(*core.DataType)
	um := must(biMemorynew(vm, []any{u, int64(3)})).(*Memory)
	ur := &MemoryRef{Type: u, Mem: um, Off: 2}
	if got := Repr(vm.refPtrOrOffset(ur)); got != "Ptr{Nothing}(0x0000000000000002)" {
		t.Errorf("ptr_or_offset of a Union memory: %s", got)
	}
	_, err := biPointerref(vm, []any{&Pointer{Type: vm.ptrType(core.Int8Type), Target: um}, int64(1), int64(1)})
	check(t, "pointerref into a Union memory", nil, err, "!not ported yet: pointerref into a Memory")
	addr, err := biBitcast(vm, []any{core.UInt64Type, &Pointer{Type: p.Type, Target: m, Off: 4}})
	if err != nil {
		t.Fatal(err)
	}
	back, err := biBitcast(vm, []any{p.Type, addr})
	if err != nil || !vm.egal(back, &Pointer{Type: p.Type, Target: m, Off: 4}) {
		t.Errorf("an address back to a pointer: %v, %v", back, err)
	}
	if again, _ := biBitcast(vm, []any{core.UInt64Type, &Pointer{Type: p.Type, Target: m}}); again == addr || again == uint64(0) {
		t.Errorf("addresses: %v and %v", again, addr)
	}
	_, err = vm.addressOf(&Pointer{Type: p.Type, Target: m, Off: -1})
	check(t, "the address before an object", nil, err, "!not ported yet: the address of")
	if _, _, ok := vm.targetAt(uint64(99) << regionBits); ok {
		t.Error("an address in no region")
	}
	_, err = biBitcast(vm, []any{core.UInt32Type, p})
	check(t, "a pointer to a UInt32", nil, err, "!bitcast: argument size does not match")
	// an undefined boxed element and a layout read as the wrong struct
	anyMem := must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.AnyType, core.CPU)).(*core.DataType)
	am := must(biMemorynew(vm, []any{anyMem, int64(1)})).(*Memory)
	_, err = biPointerref(vm, []any{&Pointer{Type: vm.ptrType(core.AnyType), Target: am}, int64(1), int64(1)})
	check(t, "an undefined element", nil, err, "!UndefRefError()")
	_, err = biPointerref(vm, []any{&Pointer{Type: vm.ptrType(core.Int64Type), Target: layoutOf{core.Int64Type}}, int64(1), int64(1)})
	check(t, "a layout as Int64", nil, err, "!not ported yet: pointerref of a layout")
	if got := Repr(vm.typeLayoutPtr(core.Int64Type)); got != "Ptr{Nothing}(Int64.layout + 0)" {
		t.Errorf("show of a layout pointer: %s", got)
	}
	l1 := coreValue(t, vm, "L1")
	_, err = biPointerref(vm, []any{&Pointer{Type: vm.ptrType(l1), Target: layoutOf{core.NumberType}}, int64(1), int64(1)})
	check(t, "the layout of an abstract type", nil, err, "!not ported yet: the layout of the abstract type Number")

	// a string's address round-trips; the address space is bounded; a
	// memory's region goes with it
	sp := &Pointer{Type: vm.ptrType(core.UInt8Type), Target: "abc", Off: 1}
	a1, err := vm.addressOf(sp)
	if err != nil {
		t.Fatal(err)
	}
	if target, off, ok := vm.targetAt(a1); !ok || target != any("abc") || off != 1 {
		t.Errorf("a string's address: %v %v %v", target, off, ok)
	}
	full := New()
	full.addrs.next = maxRegions
	_, err = full.addressOf(&Pointer{Type: sp.Type, Target: "x"})
	check(t, "past the last region", nil, err, "!memory limit exceeded")
	gone := func() any {
		m := must(biMemorynew(vm, []any{i32, int64(1)})).(*Memory)
		return addrKey(m)
	}()
	for i := 0; i < 50 && gone.(interface{ Value() *Memory }).Value() != nil; i++ {
		runtime.GC()
	}
	vm.addrs.mu.Lock() // the cleanups write the table too
	vm.addrs.byID[maxRegions] = gone
	vm.addrs.mu.Unlock()
	if _, _, ok := vm.targetAt(uint64(maxRegions) << regionBits); ok {
		t.Error("the region of a collected memory")
	}
}
