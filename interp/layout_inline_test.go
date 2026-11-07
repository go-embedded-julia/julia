package interp

import (
	"bytes"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// A struct holding a struct with references stores it inline, its pointers
// within it (datatype.c:828-839): TAG_GENERAL's image of the outer one lists
// the inner's pointer at the outer's offset, and reads it back.
func TestIRInlineStructWithPointers(t *testing.T) {
	f := newIRFixture(t)
	f.vm.baseModule = f.b
	defer func() { f.vm.baseModule = nil }()
	f.typ("struct Inner; x::Int; s::String; end; struct Outer; a::Int8; i::Inner; b::Int8; end; Outer")
	outer := f.typ("Outer").(*core.DataType)
	v := f.new(outer, int8(1), f.new(f.typ("Inner").(*core.DataType), int64(2), "s"), int8(3))

	raw, ptrs, err := f.vm.image(outer, v)
	if err != nil {
		t.Fatal(err)
	}
	// a at 0, the inline Inner at 8 (x at 8, s at 16), b at 24
	want := make([]byte, 32)
	want[0], want[8], want[24] = 1, 2, 3
	if !bytes.Equal(raw, want) || len(ptrs) != 1 || ptrs[0].off != 16 || ptrs[0].val != any("s") {
		t.Errorf("the image of an Outer: %v, %v", raw, ptrs)
	}
	f.roundTrip("an inline struct with a pointer", f.code([]any{int64(1)}, v), nil)

	// a struct whose layout cannot be computed has no image, nor as a field
	bad := core.NewIncompleteType("Main", "Bad", nil, false, false, []expr.Symbol{"u"})
	if _, _, err := f.vm.image(bad, &Struct{Type: bad}); err == nil {
		t.Error("the image of a struct without a layout")
	}
}
