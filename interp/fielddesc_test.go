package interp

import (
	"fmt"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// The field descriptors after a type's layout, as Base.DataTypeFieldDesc
// reads them, against julia 1.12.6: the descriptor width and the first and
// last fields' isptr, size and offset.
func TestFieldDescAgainstJulia(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval(`struct FieldDescStorage{T}; ptrsize::T; offset::T; end
struct S1; a::Int8; b::Int64; c::Int16; end
struct S2; x::S1; y::Int8; end
struct S3; a::Int8; b::Any; end
mutable struct M1; a::Int32; s::String; end`); err != nil {
		t.Fatal(err)
	}
	eval := func(src string) any {
		v, err := s.Eval(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		return v
	}
	for _, c := range []struct{ src, want string }{
		// julia 1.12.6's answers
		{"Tuple{Int8,Int64}", "0\t0,1,0;0,8,8"},
		{"S1", "0\t0,1,0;0,2,16"},
		{"S2", "0\t0,24,0;0,1,24"},
		{"S3", "0\t0,1,0;1,8,8"},
		{"M1", "0\t0,4,0;1,8,8"},
		{"NTuple{300,UInt8}", "1\t0,1,0;0,1,299"},
		{"Tuple{NTuple{200,Int64},Int8}", "1\t0,1600,0;0,1,1600"},
	} {
		d := eval(c.src).(*core.DataType)
		l, err := structLayout(d)
		if err != nil {
			t.Fatal(err)
		}
		fdt := fielddescType(l)
		elt := [...]string{"UInt8", "UInt16", "UInt32"}[fdt]
		st := eval("FieldDescStorage{" + elt + "}").(*core.DataType)
		p := &Pointer{Type: vm.ptrType(st), Target: layoutOf{d}, Off: 20}
		got := fmt.Sprint(fdt, "\t")
		for k, i := range []int64{1, int64(len(l.offsets))} {
			v, err := vm.ptrLoad("pointerref", p, i)
			if err != nil {
				t.Fatalf("%s field %d: %v", c.src, i, err)
			}
			f := v.(*Struct).Fields
			ps, _, _ := primBits(f[0])
			off, _, _ := primBits(f[1])
			if k > 0 {
				got += ";"
			}
			got += fmt.Sprintf("%d,%d,%d", ps.lo&1, ps.lo>>1, off.lo)
		}
		if got != c.want {
			t.Errorf("%s: %q, julia %q", c.src, got, c.want)
		}
	}
	// a descriptor of the wrong width, or past the fields, is refused
	d := eval("S1").(*core.DataType)
	wide := &Pointer{Type: vm.ptrType(eval("FieldDescStorage{UInt16}")), Target: layoutOf{d}, Off: 20}
	_, err := vm.ptrLoad("pointerref", wide, 1)
	check(t, "a descriptor of the wrong width", nil, err, "!not ported yet: the field descriptors")
	narrow := &Pointer{Type: vm.ptrType(eval("FieldDescStorage{UInt8}")), Target: layoutOf{d}, Off: 20}
	_, err = vm.ptrLoad("pointerref", narrow, 4)
	check(t, "a descriptor past the fields", nil, err, "!pointerref: invalid pointer")
	u := eval("struct U1; a::Union{Int8,Int16}; end; U1").(*core.DataType)
	_, err = vm.fieldDesc(u, eval("FieldDescStorage{UInt8}").(*core.DataType), 1)
	check(t, "the descriptors of a layout not laid out here", nil, err, "!isbits Union field")
}
