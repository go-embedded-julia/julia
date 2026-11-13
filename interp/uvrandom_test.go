package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// uv_random fills a Ref of a primitive value, or bytes of a primitive
// memory, with fresh randomness; anything else is refused.
func TestUVRandom(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	box, err := s.Eval("mutable struct RU; x::UInt64; end; RU(UInt64(0))")
	if err != nil {
		t.Fatal(err)
	}
	voidp := vm.ptrType(core.NothingType)
	call := func(buf any, n uint64) (any, error) {
		return vm.shims["uv_random"](vm, []any{nil, nil, buf, n, uint32(0), nil})
	}
	// two draws of 64 bits are equal with probability 2^-64
	v, err := call(&Pointer{Type: voidp, Target: box}, 8)
	check(t, "a Ref{UInt64}", v, err, "0")
	first := box.(*Struct).Fields[0]
	if _, err := call(&Pointer{Type: voidp, Target: box}, 8); err != nil || box.(*Struct).Fields[0] == first {
		t.Errorf("two draws: %v %v, %v", first, box.(*Struct).Fields[0], err)
	}
	mem := &Memory{Type: memType(core.UInt32Type), Elems: []any{uint32(0), uint32(0), uint32(0), uint32(0)}}
	v, err = call(&Pointer{Type: voidp, Target: mem, Off: 4}, 8)
	check(t, "8 bytes of a Memory{UInt32}", v, err, "0")
	if mem.Elems[0] != any(uint32(0)) || mem.Elems[3] != any(uint32(0)) || (mem.Elems[1] == any(uint32(0)) && mem.Elems[2] == any(uint32(0))) {
		t.Errorf("Memory after uv_random: %v", mem.Elems)
	}
	for _, c := range []struct {
		name string
		buf  any
		n    any
		want string
	}{
		{"a count of another width", &Pointer{Type: voidp, Target: box}, uint64(4), "!not ported yet: uv_random of 4 bytes"},
		{"an offset into the Ref", &Pointer{Type: voidp, Target: box, Off: 1}, uint64(8), "!not ported yet: uv_random of 8 bytes"},
		{"past the memory", &Pointer{Type: voidp, Target: mem, Off: 12}, uint64(8), "!uv_random: invalid pointer"},
		{"a huge count", &Pointer{Type: voidp, Target: mem}, uint64(1) << 40, "!uv_random: invalid pointer"},
		{"a memory of boxes", &Pointer{Type: voidp, Target: &Memory{Type: memType(core.AnyType), Elems: []any{nil}}}, uint64(1), "!not ported yet: uv_random into a Memory"},
		{"a string", vm.stringPtrOf("ab"), uint64(1), "!not ported yet: uv_random into"},
		{"no pointer", int64(0), uint64(1), "!not ported yet: uv_random into"},
		{"a signed count", &Pointer{Type: voidp, Target: box}, int64(8), "!not ported yet: uv_random into"},
	} {
		v, err := vm.shims["uv_random"](vm, []any{nil, nil, c.buf, c.n, uint32(0), nil})
		check(t, c.name, v, err, c.want)
	}
	two, err := s.Eval("mutable struct R2; x::UInt8; y::UInt8; end; R2(0x00, 0x00)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(&Pointer{Type: voidp, Target: two}, 1)
	check(t, "a struct of two fields", nil, err, "!not ported yet: uv_random of 1 bytes")
}
