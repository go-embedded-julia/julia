package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// The PCRE2 shims' handles, frees, refusals and clamps.
func TestPCREHandlesAndRefusals(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	re, _, _ := f.compile("(?<a>b)", 0)
	_, md := f.match(re, "xb", 0, 0)
	js := f.must("pcre2_jit_stack_create_8", uint64(32768), uint64(1<<20), f.null)
	mc := f.must("pcre2_match_context_create_8", f.null)
	if _, err := f.call("pcre2_jit_stack_assign_8", mc, f.null, js); err != nil {
		t.Fatal(err)
	}
	// a match with the context, the way Base does
	rc := f.must("pcre2_match_8", re, f.str("xb"), uint64(2), uint64(0), uint32(0), md, mc).(int32)
	if rc != 2 {
		t.Errorf("a match with a context: %d", rc)
	}
	bad := int64(1)
	wrongHandle := md // a match data where a code is wanted, and the other way
	for _, c := range []struct {
		f    string
		args []any
	}{
		{"pcre2_jit_stack_assign_8", []any{bad, f.null, js}},
		{"pcre2_jit_stack_assign_8", []any{mc, f.null, bad}},
		{"pcre2_jit_compile_8", []any{bad, uint32(1)}},
		{"pcre2_pattern_info_8", []any{bad, uint32(4), f.null}},
		{"pcre2_match_data_create_from_pattern_8", []any{wrongHandle, f.null}},
		{"pcre2_get_ovector_count_8", []any{bad}},
		{"pcre2_get_ovector_pointer_8", []any{re}},
		{"pcre2_match_8", []any{bad, f.str("x"), uint64(1), uint64(0), uint32(0), md, f.null}},
		{"pcre2_match_8", []any{re, f.str("x"), uint64(1), uint64(0), uint32(0), bad, f.null}},
		{"pcre2_match_8", []any{re, f.str("x"), uint64(1), uint64(0), uint32(0), md, md}},
		{"pcre2_match_8", []any{re, f.null, uint64(1), uint64(0), uint32(0), md, f.null}},
		{"pcre2_match_8", []any{re, f.str("x"), uint64(5), uint64(0), uint32(0), md, f.null}},
		{"pcre2_substring_number_from_name_8", []any{bad, f.str("a")}},
		{"pcre2_substring_number_from_name_8", []any{re, bad}},
		{"pcre2_substring_length_bynumber_8", []any{bad, int32(0), f.null}},
		{"pcre2_substring_copy_bynumber_8", []any{bad, uint32(0), f.null, f.null}},
		{"pcre2_compile_8", []any{f.null, uint64(1), uint32(0), f.null, f.null, f.null}},
		{"pcre2_compile_8", []any{f.str("a"), uint64(1), uint32(0), bad, f.null, f.null}},
		{"pcre2_compile_8", []any{f.str("("), uint64(1), uint32(0), f.null, bad, f.null}},
		{"pcre2_compile_8", []any{f.str("("), uint64(1), uint32(0), bad, f.null, f.null}},
	} {
		v, err := f.call(c.f, c.args...)
		check(t, c.f, v, err, "!")
	}
	// an errorcode through a Ref of the wrong type, and through C_NULL
	pref, _ := f.ref("RefP8", &Pointer{Type: f.u8p})
	_, err := f.call("pcre2_compile_8", f.str("a"), uint64(1), uint32(0), pref, f.null, f.null)
	check(t, "a Ref of a pointer for an int", nil, err, "!as Ptr{UInt8}")
	if v, err := f.call("pcre2_compile_8", f.str("a"), uint64(1), uint32(0), f.null, f.null, f.null); err != nil || v.(*Pointer).Target == nil {
		t.Errorf("a compile with no error outputs: %v %v", err, v)
	}
	// the ovector through a pointer: out of range and misaligned
	ov := f.must("pcre2_get_ovector_pointer_8", md).(*Pointer)
	for _, c := range []struct {
		name string
		p    *Pointer
		i    int64
	}{
		{"past the ovector", ov, 99},
		{"before it", ov, 0},
		{"misaligned", &Pointer{Type: ov.Type, Target: ov.Target, Off: 4}, 1},
		{"as another type", &Pointer{Type: f.vm.ptrType(core.Int8Type), Target: ov.Target}, 1},
	} {
		_, err := biPointerref(vm, []any{c.p, c.i, int64(1)})
		check(t, c.name, nil, err, "!invalid pointer")
	}

	// frees: of a handle, of C_NULL, of what is no pointer
	for _, h := range []any{md, re, js, mc, f.null, bad} {
		for _, name := range []string{"pcre2_match_data_free_8", "pcre2_code_free_8", "pcre2_jit_stack_free_8", "pcre2_match_context_free_8"} {
			if _, err := f.call(name, h); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	}

	// the error message into a String the VM made, a literal, a bad pointer,
	// a pointer past its memory; a buffer clamped by the memory limit
	re2, _, _ := f.compile("a", 0)
	str, err := vm.allocString([]any{int64(32)})
	if err != nil {
		t.Fatal(err)
	}
	if rc := f.must("pcre2_get_error_message_8", int32(101), &Pointer{Type: f.u8p, Target: str.(string)}, uint64(32)); rc != int32(19) {
		t.Errorf("a message into a String: %v", rc)
	}
	if got := str.(string)[:19]; got != `\ at end of pattern` {
		t.Errorf("the message in the String: %q", got)
	}
	for _, c := range []struct {
		name string
		dst  any
	}{
		{"a literal", f.str("literal literal literal literal")},
		{"no pointer", bad},
		{"past its memory", &Pointer{Type: f.u8p, Target: &Memory{Type: memUInt8Type, Elems: make([]any, 4)}, Off: 100}},
	} {
		_, err := f.call("pcre2_get_error_message_8", int32(101), c.dst, uint64(32))
		check(t, "a message into "+c.name, nil, err, "!")
	}
	vm.MaxMemoryElems = 4
	buf, m := f.buf(4, 0xaa)
	if rc := f.must("pcre2_get_error_message_8", int32(101), buf, uint64(1000)); rc != int32(-48) {
		t.Errorf("a clamped message: %v", rc)
	}
	if joinBytes(m) != "92,32,97,0" {
		t.Errorf("the clamped message: %s", joinBytes(m))
	}
	// a substring copy whose size is clamped, into a bad destination, through
	// a bad Ref
	vm.MaxMemoryElems = 1 << 20
	re3, _, _ := f.compile("(b)", 0)
	_, md3 := f.match(re3, "xb", 0, 0)
	big, _ := f.ref("RefU64", uint64(1<<40))
	vm.MaxMemoryElems = 8
	buf, m = f.buf(8, 0xaa)
	if rc := f.must("pcre2_substring_copy_bynumber_8", md3, uint32(1), buf, big); rc != int32(0) || joinBytes(m)[:4] != "98,0" {
		t.Errorf("a clamped copy: %v %s", rc, joinBytes(m))
	}
	vm.MaxMemoryElems = 1 << 20
	one, _ := f.ref("RefU64", uint64(8))
	for _, c := range []struct {
		name string
		dst  any
		ref  any
	}{
		{"a bad destination", bad, one},
		{"a literal", f.str("literal"), one},
		{"a Ref that is no pointer", buf, bad},
		{"a Ref into nothing", buf, &Pointer{Type: vm.ptrType(core.UInt64Type)}},
	} {
		_, err := f.call("pcre2_substring_copy_bynumber_8", md3, uint32(1), c.dst, c.ref)
		check(t, "a copy into "+c.name, nil, err, "!")
	}
	_ = re2
	_, err = f.call("pcre2_substring_length_bynumber_8", md3, int32(1), bad)
	check(t, "a length into no Ref", nil, err, "!result through")

	// a Cstring that is no address, one that is an address of nothing, one
	// that has no NUL, and the name table of a pattern without names
	cs := must(f.s.Eval("PCStr")).(*core.DataType)
	noNUL := &Memory{Type: memUInt8Type, Elems: []any{uint8('a')}}
	for _, name := range []any{PrimValue{cs, from64(12345)}, &Pointer{Type: f.u8p, Target: noNUL}, "x", float64(1)} {
		_, err := f.call("pcre2_substring_number_from_name_8", re3, name)
		check(t, "a bad Cstring", nil, err, "!pcre2_substring_number_from_name of")
	}
	r, st := f.ref("RefP8", &Pointer{Type: f.u8p})
	if rc := f.must("pcre2_pattern_info_8", re3, uint32(19), r); rc != int32(0) {
		t.Errorf("the name table of a pattern: %v", rc)
	}
	if p, ok := st.Fields[0].(*Pointer); !ok || p.Target == nil {
		t.Errorf("the name table: %v", st.Fields[0])
	}
}
