package interp

import (
	"bytes"
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
		if c.name == "UInt8" {
			want = kindU8
		}
		if m.kind != want || memKindOf(m.Type) != want || m.Len() != 3 {
			t.Errorf("%s: kind %d, want %d", c.name, m.kind, want)
		}
		if boxed, bytes := m.stores(); bytes != (want == kindU8) || boxed != (want == kindAny) {
			t.Errorf("%s: stores boxed %v and bytes %v", c.name, boxed, bytes)
		}
	}
	if len(typedKinds) != 1 {
		t.Errorf("%d typed kinds, want 1: UInt8", len(typedKinds))
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
	if _, bytes := m.stores(); m.Len() != 0 || !bytes {
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
	} else if _, bytes := e.stores(); !bytes {
		t.Error("an empty typed memory has no store of bytes")
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
