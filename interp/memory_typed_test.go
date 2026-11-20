package interp

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The typed stores of Memory (#81): the table says which element types have one,
// and a typed memory reads and writes as a boxed one does.

func memTypeOf(et any) *core.DataType {
	return must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), et, core.CPU)).(*core.DataType)
}

// UInt8 is typed and every other element type is not.
func TestTypedKinds(t *testing.T) {
	for _, c := range []struct {
		name string
		et   any
	}{
		{"UInt8", core.UInt8Type}, {"Int8", core.Int8Type}, {"UInt16", core.UInt16Type}, {"Int16", core.Int16Type},
		{"UInt32", core.UInt32Type}, {"Int32", core.Int32Type}, {"UInt64", core.UInt64Type}, {"Int64", core.Int64Type},
		{"Float16", core.Float16Type}, {"Float32", core.Float32Type}, {"Float64", core.Float64Type}, {"Bool", core.BoolType},
		{"Char", core.CharType}, {"Any", core.AnyType}, {"String", core.StringType}, {"Nothing", core.NothingType},
		{"Symbol", core.SymbolType},
	} {
		m := newMemoryN(memTypeOf(c.et), 3)
		want := kindAny
		switch c.name {
		case "UInt8":
			want = kindU8
		case "Float64":
			want = kindF64
		}
		if m.kind != want || memKindOf(m.Type) != want || m.Len() != 3 {
			t.Errorf("%s: kind %d, want %d", c.name, m.kind, want)
		}
		if boxed, bytes, floats := m.stores(); bytes != (want == kindU8) || floats != (want == kindF64) || boxed != (want == kindAny) {
			t.Errorf("%s: stores boxed %v, bytes %v and floats %v", c.name, boxed, bytes, floats)
		}
	}
	if len(typedKinds) != 2 {
		t.Errorf("%d typed kinds, want 2: UInt8 and Float64", len(typedKinds))
	}
	// a type that is no memory type, and no type
	if memKindOf(core.Int64Type) != kindAny || memKindOf(nil) != kindAny {
		t.Error("a type that is no memory type is typed")
	}
}

func TestTypedMemoryAccessors(t *testing.T) {
	m := newMemory(memUInt8Type, []any{uint8(1), undef, uint8(3)})
	if m.Len() != 3 || m.Get(0) != any(uint8(1)) || m.Get(1) != any(uint8(0)) || m.Get(2) != any(uint8(3)) {
		t.Fatalf("a typed memory made from boxed values: %v", m.All())
	}
	m.Set(1, uint8(9))
	if m.Get(1) != any(uint8(9)) {
		t.Error("Set")
	}
	// Slice and All are copies: a write through them is not a write of the memory
	s := m.Slice(1, 3)
	s[0] = uint8(77)
	if len(s) != 2 || m.Get(1) != any(uint8(9)) || len(m.All()) != 3 {
		t.Errorf("Slice: %v, memory %v", s, m.All())
	}
	// Bytes shares the store
	b, ok := m.Bytes(0, 3)
	if !ok || !bytes.Equal(b, []byte{1, 9, 3}) {
		t.Fatalf("Bytes: %v %v", b, ok)
	}
	b[0] = 5
	if m.Get(0) != any(uint8(5)) {
		t.Error("Bytes does not share the store")
	}
	if w, ok := m.WrittenBytes(1, 2); !ok || len(w) != 1 {
		t.Errorf("WrittenBytes: %v %v", w, ok)
	}
	// CopyFrom, overlapping, as memmove
	m.CopyFrom(1, m, 0, 2)
	if got := m.All(); got[0] != any(uint8(5)) || got[1] != any(uint8(5)) || got[2] != any(uint8(9)) {
		t.Errorf("CopyFrom: %v", got)
	}
	m.SetAll([]any{uint8(7), "not a byte"})
	if m.Len() != 2 || m.Get(0) != any(uint8(7)) || m.Get(1) != any(uint8(0)) {
		t.Errorf("SetAll: %v", m.All())
	}
	m.SetAll(nil)
	if _, bytes, _ := m.stores(); m.Len() != 0 || !bytes {
		t.Error("SetAll of nothing")
	}
	// a boxed memory has no bytes, and Written is for boxed memories
	a := newMemory(memAnyType, []any{int64(1)})
	if _, ok := a.Bytes(0, 1); ok {
		t.Error("Bytes of a boxed memory")
	}
	if _, ok := a.WrittenBytes(0, 1); ok {
		t.Error("WrittenBytes of a boxed memory")
	}
	func() {
		defer func() {
			if r := recover(); r == nil || !strings.Contains(r.(string), "typed Memory") {
				t.Errorf("Written of a typed memory: %v", r)
			}
		}()
		newMemoryN(memUInt8Type, 1).Written(0, 1)
	}()
	// a UInt8 memory made empty is typed, with a store of its own
	if e := newMemory(memUInt8Type, nil); e.kind != kindU8 || e.Len() != 0 {
		t.Errorf("an empty typed memory: kind %d, %d elements", e.kind, e.Len())
	} else if _, bytes, _ := e.stores(); !bytes {
		t.Error("an empty typed memory has no store of bytes")
	}
}

var memF64Type = memTypeOf(core.Float64Type)

// A Memory{Float64} keeps every bit of a float: -0.0, a NaN with a payload,
// an infinity; reads and writes through the slice and through Get and Set agree.
func TestTypedMemoryFloat64(t *testing.T) {
	negZero := math.Copysign(0, -1)
	payload := math.Float64frombits(0x7ff8_dead_beef_0001)
	signalling := math.Float64frombits(0xfff0_0000_0000_0001)
	m := newMemory(memF64Type, []any{1.5, negZero, payload, "not a float", signalling})
	if m.Len() != 5 || m.kind != kindF64 {
		t.Fatalf("a typed memory made from boxed values: kind %d, %d elements", m.kind, m.Len())
	}
	bits := func(v any) uint64 { return math.Float64bits(v.(float64)) }
	for i, want := range []uint64{math.Float64bits(1.5), math.Float64bits(negZero), 0x7ff8_dead_beef_0001, 0, 0xfff0_0000_0000_0001} {
		if got := bits(m.Get(i)); got != want {
			t.Errorf("element %d: bits %#x, want %#x", i, got, want)
		}
	}
	m.Set(0, payload)
	if bits(m.Get(0)) != 0x7ff8_dead_beef_0001 {
		t.Error("Set lost the payload of a NaN")
	}
	fs, ok := m.Float64s(1, 4)
	if !ok || len(fs) != 3 || math.Float64bits(fs[0]) != math.Float64bits(negZero) {
		t.Fatalf("Float64s: %v %v", fs, ok)
	}
	fs[2] = 9 // shares the store
	if m.Get(3) != any(9.0) {
		t.Error("Float64s does not share the store")
	}
	// Slice and All are copies of boxed values
	s := m.Slice(0, 2)
	s[0] = "x"
	if len(s) != 2 || bits(m.Get(0)) != 0x7ff8_dead_beef_0001 || len(m.All()) != 5 {
		t.Error("Slice")
	}
	// CopyFrom, overlapping
	m.CopyFrom(1, m, 0, 3)
	if bits(m.Get(1)) != 0x7ff8_dead_beef_0001 || bits(m.Get(2)) != math.Float64bits(negZero) {
		t.Errorf("CopyFrom: %v", m.All())
	}
	m.SetAll([]any{2.0, int64(3)})
	if m.Len() != 2 || m.Get(0) != any(2.0) || m.Get(1) != any(0.0) {
		t.Errorf("SetAll: %v", m.All())
	}
	if w, ok := m.WrittenFloat64s(0, 1); !ok || len(w) != 1 {
		t.Errorf("WrittenFloat64s: %v %v", w, ok)
	}
	// another kind has no floats, and Written is for boxed memories
	if _, ok := newMemoryN(memUInt8Type, 1).Float64s(0, 1); ok {
		t.Error("Float64s of a memory of bytes")
	}
	if _, ok := newMemory(memAnyType, []any{1.0}).WrittenFloat64s(0, 1); ok {
		t.Error("WrittenFloat64s of a boxed memory")
	}
	if _, ok := newMemoryN(memF64Type, 1).Bytes(0, 1); ok {
		t.Error("Bytes of a memory of floats")
	}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Written of a typed memory")
			}
		}()
		newMemoryN(memF64Type, 1).Written(0, 1)
	}()
	if e := newMemory(memF64Type, nil); e.kind != kindF64 || e.Len() != 0 {
		t.Errorf("an empty typed memory: %d %d", e.kind, e.Len())
	} else if _, _, floats := e.stores(); !floats {
		t.Error("an empty typed memory has no store of floats")
	}
}

// Kernel.Set and Get move a []float64 and a Matrix[float64] without boxing an
// element, and every bit comes back: -0.0, NaN payloads, infinities.
func TestKernelFloat64ValuesWithoutBoxing(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	xs := []float64{math.Copysign(0, -1), math.Float64frombits(0x7ff8_dead_beef_0001), math.Inf(-1), math.Float64frombits(0xfff0_0000_0000_0001), 1.0000000000000002, 5e-324}
	same := func(got, want []float64) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
				return false
			}
		}
		return true
	}
	if err := k.Set("v", xs); err != nil {
		t.Fatal(err)
	}
	if err := k.Set("w", Matrix[float64]{Rows: 2, Cols: 3, Data: xs}); err != nil {
		t.Fatal(err)
	}
	g, ok, err := k.Get("v")
	if v, _ := g.([]float64); err != nil || !ok || !same(v, xs) {
		t.Errorf("Get of a vector: %v %v %v", g, ok, err)
	}
	gm, ok, err := k.Get("w")
	if m, _ := gm.(Matrix[float64]); err != nil || !ok || m.Rows != 2 || m.Cols != 3 || !same(m.Data, xs) {
		t.Errorf("Get of a matrix: %v %v %v", gm, ok, err)
	}
	// copies, never aliases: a write to what Get returned is not a write of the kernel's
	g.([]float64)[0] = 99
	if g2, _, _ := k.Get("v"); math.Float64bits(g2.([]float64)[0]) != math.Float64bits(xs[0]) {
		t.Error("Get answers the kernel's own slice")
	}
	xs[1] = 0
	if g2, _, _ := k.Get("v"); math.Float64bits(g2.([]float64)[1]) != 0x7ff8_dead_beef_0001 {
		t.Error("Set kept the caller's slice")
	}
	// the global is a Memory{Float64} of the typed kind
	arr, _ := k.global("v")
	if m := arr.(*Struct).Fields[0].(*MemoryRef).Mem; m.kind != kindF64 {
		t.Errorf("the vector's memory has kind %d", m.kind)
	}
	// and neither direction allocates per element: a few objects for 4096 floats
	big := make([]float64, 4096)
	for i := range big {
		big[i] = float64(i) + 0.5
	}
	if allocs := testing.AllocsPerRun(5, func() {
		if err := k.Set("big", big); err != nil {
			t.Fatal(err)
		}
	}); allocs > 40 {
		t.Errorf("Set of 4096 floats allocates %v times", allocs)
	}
	if allocs := testing.AllocsPerRun(5, func() {
		if _, _, err := k.Get("big"); err != nil {
			t.Fatal(err)
		}
	}); allocs > 40 {
		t.Errorf("Get of 4096 floats allocates %v times", allocs)
	}
}

// The writes to an image's Float64 memory are logged and undone, by Set, by
// WrittenFloat64s, by CopyFrom, by SetAll and by a BLAS call that writes in place.
func TestTypedMemoryFloat64Undo(t *testing.T) {
	vm := New()
	l := vm.undo
	m := newMemory(memF64Type, []any{1.0, 2.0, 3.0, 4.0})
	m.undo = l
	l.on = true
	m.Set(0, 10.0)
	if w, ok := m.WrittenFloat64s(1, 3); ok {
		copy(w, []float64{20, 30})
	}
	m.CopyFrom(2, m, 0, 2)
	m.SetAll([]any{9.0})
	// y := 2x + y on the image's memory, in place: dgemv-free, level 1
	y := newMemory(memF64Type, []any{1.0, 1.0, 1.0})
	y.undo = l
	x, _ := blasArray(vm, []float64{1, 2, 3}, 0)
	py := &Pointer{Type: vm.ptrType(core.Float64Type), Target: y}
	if _, err := blasShim(vm, "daxpy_64_")(vm, []any{blasRef(vm, int64(3)), blasRef(vm, 2.0), x, blasRef(vm, int64(1)), py, blasRef(vm, int64(1))}); err != nil {
		t.Fatal(err)
	}
	if y.Get(0) != any(3.0) || y.Get(2) != any(7.0) {
		t.Fatalf("daxpy wrote %v", y.All())
	}
	n := len(l.entries)
	l.on = false
	for i := n - 1; i >= 0; i-- {
		l.entries[i].undo()
	}
	if got := m.All(); len(got) != 4 || got[0] != any(1.0) || got[1] != any(2.0) || got[2] != any(3.0) || got[3] != any(4.0) {
		t.Errorf("after the undo: %v", got)
	}
	if got := y.All(); got[0] != any(1.0) || got[1] != any(1.0) || got[2] != any(1.0) {
		t.Errorf("the BLAS call was not undone: %v", got)
	}
}

// The writes to a typed memory of the image are logged, and undone, as a boxed
// one's are.
func TestTypedMemoryUndo(t *testing.T) {
	vm := New()
	l := vm.undo
	m := newMemory(memUInt8Type, []any{uint8(1), uint8(2), uint8(3), uint8(4)})
	m.undo = l
	l.on = true
	m.Set(0, uint8(10))
	if w, ok := m.WrittenBytes(1, 3); ok {
		copy(w, []byte{20, 30})
	}
	m.CopyFrom(2, m, 0, 2)
	m.SetAll([]any{uint8(9)})
	if len(l.entries) != 4 {
		t.Fatalf("%d entries", len(l.entries))
	}
	l.on = false
	for i := len(l.entries) - 1; i >= 0; i-- {
		l.entries[i].undo()
	}
	if b, _ := m.Bytes(0, m.Len()); !bytes.Equal(b, []byte{1, 2, 3, 4}) {
		t.Errorf("after the undo: %v", b)
	}
	// a typed memory a cell made has no log
	n := newMemory(memUInt8Type, []any{uint8(1)})
	l.on = true
	n.Set(0, uint8(2))
	if len(l.entries) != 4 {
		t.Errorf("a cell's memory was logged: %d entries", len(l.entries))
	}
}
