package interp

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// pcreFixture calls the libpcre2-8 shims as Base's pcre.jl does: Refs are
// pointers into a mutable struct's first field, strings Ptr{UInt8} into a
// String.
type pcreFixture struct {
	t    *testing.T
	vm   *VM
	s    *Session
	null *Pointer
	u8p  *core.DataType
}

func newPCREFixture(t *testing.T) *pcreFixture {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval(`mutable struct RefI32; x::Int32; end
mutable struct RefU32; x::UInt32; end
mutable struct RefU64; x::UInt64; end
mutable struct RefP8; x::Ptr{UInt8}; end
mutable struct RefI8; x::Int8; end
mutable struct RefAny; x::Any; end
primitive type PCStr 64 end`); err != nil {
		t.Fatal(err)
	}
	return &pcreFixture{t: t, vm: vm, s: s, null: &Pointer{Type: vm.ptrType(core.NothingType)}, u8p: vm.ptrType(core.UInt8Type)}
}

func (f *pcreFixture) call(name string, args ...any) (any, error) {
	f.t.Helper()
	sh := f.vm.libShims["libpcre2-8"][expr.Symbol(name)]
	if sh == nil {
		f.t.Fatalf("no shim %s", name)
	}
	return sh(f.vm, args)
}

func (f *pcreFixture) must(name string, args ...any) any {
	f.t.Helper()
	v, err := f.call(name, args...)
	if err != nil {
		f.t.Fatalf("%s: %v", name, err)
	}
	return v
}

func (f *pcreFixture) str(s string) *Pointer { return &Pointer{Type: f.u8p, Target: s} }

// ref is a Ref of the struct type, holding v.
func (f *pcreFixture) ref(typ string, v any) (*Pointer, *Struct) {
	st := must(f.vm.newStruct(must(f.s.Eval(typ)), []any{v})).(*Struct)
	ft, _ := st.Type.FieldTypes()
	return &Pointer{Type: f.vm.ptrType(ft[0]), Target: st}, st
}

func (f *pcreFixture) buf(n int, fill uint8) (*Pointer, *Memory) {
	m := &Memory{Type: memUInt8Type, Elems: make([]any, n)}
	for i := range m.Elems {
		m.Elems[i] = fill
	}
	return &Pointer{Type: f.u8p, Target: m}, m
}

func joinBytes(m *Memory) string {
	var parts []string
	for _, e := range m.Elems {
		parts = append(parts, fmt.Sprint(e))
	}
	return strings.Join(parts, ",")
}

// ovector is the ovector's words through pcre2_get_ovector_pointer.
func (f *pcreFixture) ovector(md any) (uint32, string) {
	n := f.must("pcre2_get_ovector_count_8", md).(uint32)
	p := f.must("pcre2_get_ovector_pointer_8", md)
	var parts []string
	for i := int64(1); i <= 2*int64(n); i++ {
		v, err := biPointerref(f.vm, []any{p, i, int64(1)})
		if err != nil {
			f.t.Fatal(err)
		}
		parts = append(parts, fmt.Sprint(v))
	}
	return n, strings.Join(parts, ",")
}

func (f *pcreFixture) compile(pat string, opts uint32) (*Pointer, int32, uint64) {
	ec, ecs := f.ref("RefI32", int32(0))
	eo, eos := f.ref("RefU64", uint64(0))
	re := f.must("pcre2_compile_8", f.str(pat), uint64(len(pat)), opts, ec, eo, f.null).(*Pointer)
	return re, ecs.Fields[0].(int32), eos.Fields[0].(uint64)
}

func (f *pcreFixture) match(re *Pointer, subject string, offset uint64, opts uint32) (int32, any) {
	md := f.must("pcre2_match_data_create_from_pattern_8", re, f.null)
	rc := f.must("pcre2_match_8", re, f.str(subject), uint64(len(subject)), offset, opts, md, f.null).(int32)
	return rc, md
}

func (f *pcreFixture) errmsg(code int32, size int) string {
	buf, m := f.buf(size, 0)
	rc := f.must("pcre2_get_error_message_8", code, buf, uint64(size)).(int32)
	if size > 16 {
		var text []byte
		for _, e := range m.Elems {
			if e.(uint8) == 0 {
				break
			}
			text = append(text, e.(uint8))
		}
		return fmt.Sprintf("(%d, %s)", rc, strconv.Quote(string(text)))
	}
	return fmt.Sprintf("(%d, %q)", rc, joinBytes(m))
}

// The shims against julia 1.12.6's libpcre2-8 (testdata/pcre.jl): compile
// errors, matches and ovectors, capture names, substring lengths and copies,
// error messages.
func TestPCREAgainstJulia(t *testing.T) {
	f := newPCREFixture(t)
	var got []string
	emit := func(format string, args ...any) { got = append(got, fmt.Sprintf(format, args...)) }
	for _, c := range []struct {
		name, pat string
		opts      uint32
	}{{"open", "(", 0}, {"class", "[b-a]", 0}, {"quant", "a**", 0}, {"badopt", "a", 0xffffffff}, {"ok", "(?<a>b)(c)?", 0},
		{"empty", "", 0}, {"utf", "\xff", 0x00080000}} {
		re, ec, eo := f.compile(c.pat, c.opts)
		emit("compile %s %v %d %d", c.name, re.Target != nil, ec, eo)
	}
	for _, code := range []int32{101, 102, 115, 0, -1, -48, -49, 9999, -9999} {
		emit("message %d %s", code, f.errmsg(code, 256))
	}
	emit("message small %s", f.errmsg(101, 5))
	emit("message one %s", f.errmsg(101, 1))
	emit("message zero %s", f.errmsg(101, 0))

	re, _, _ := f.compile("(?<a>b)(c)?(?<zz>d)?", 0)
	for _, what := range []uint32{4, 17, 18, 1, 0xffff} {
		r, st := f.ref("RefU32", uint32(0))
		rc := f.must("pcre2_pattern_info_8", re, what, r).(int32)
		if rc == 0 {
			emit("info %d (0, 0x%08x)", what, st.Fields[0])
		} else {
			emit("info %d (%d, nothing)", what, rc)
		}
	}
	r, st := f.ref("RefP8", &Pointer{Type: f.u8p})
	rc := f.must("pcre2_pattern_info_8", re, uint32(19), r).(int32)
	var table []string
	for i := int64(1); i <= 10; i++ {
		v, err := biPointerref(f.vm, []any{st.Fields[0], i, int64(1)})
		if err != nil {
			t.Fatal(err)
		}
		table = append(table, fmt.Sprint(v))
	}
	emit("nametable %d %s", rc, strings.Join(table, ","))
	var nums []string
	cs := must(f.s.Eval("PCStr")).(*core.DataType)
	for _, n := range []string{"a", "zz", "nope", "", "aa"} {
		// as a Pointer, and as the Cstring's address
		v1 := f.must("pcre2_substring_number_from_name_8", re, f.str(n)).(int32)
		addr, err := f.vm.addressOf(f.str(n + "\x00"))
		if err != nil {
			t.Fatal(err)
		}
		v2 := f.must("pcre2_substring_number_from_name_8", re, PrimValue{cs, from64(addr)}).(int32)
		if v1 != v2 {
			t.Errorf("name %q: %d through a pointer, %d through its address", n, v1, v2)
		}
		nums = append(nums, fmt.Sprint(v1))
	}
	emit("number_from_name Int32[%s]", strings.Join(nums, ", "))
	emit("jit %d", f.must("pcre2_jit_compile_8", re, uint32(1)))
	emit("jit badopt %d", f.must("pcre2_jit_compile_8", re, uint32(0xffff)))

	for _, c := range []struct {
		name, subject string
		offset        uint64
		opts          uint32
	}{{"xbc", "xbc", 0, 0}, {"xb", "xb", 0, 0}, {"xyz", "xyz", 0, 0}, {"past", "xbc", 9, 0}, {"notempty", "", 0, 4},
		{"again", "bcbc", 2, 0}, {"bad opts", "xbc", 0, 0xffffffff}} {
		rc, md := f.match(re, c.subject, c.offset, c.opts)
		n, ov := f.ovector(md)
		if rc > 0 {
			emit("match %s %d (0x%08x, %q)", c.name, rc, n, ov)
		} else {
			emit("match %s %d %d", c.name, rc, n)
		}
	}

	_, md := f.match(re, "xbc", 0, 0)
	for _, n := range []int32{0, 1, 2, 3, 4, 100} {
		s, ss := f.ref("RefU64", uint64(0))
		r := f.must("pcre2_substring_length_bynumber_8", md, n, s).(int32)
		if r == 0 {
			emit("length %d %d %d", n, r, ss.Fields[0])
		} else {
			emit("length %d %d nothing", n, r)
		}
	}
	for _, c := range []struct {
		n    uint32
		size uint64
	}{{0, 16}, {1, 16}, {2, 16}, {1, 1}, {1, 2}, {1, 0}, {3, 16}, {100, 16}} {
		buf, m := f.buf(16, 0xaa)
		s, ss := f.ref("RefU64", c.size)
		r := f.must("pcre2_substring_copy_bynumber_8", md, c.n, buf, s).(int32)
		emit("copy %d %d %d %d %s", c.n, c.size, r, ss.Fields[0], joinBytes(m))
	}
	_, md = f.match(re, "xb", 0, 0)
	for _, n := range []uint32{2, 3} {
		s, _ := f.ref("RefU64", uint64(0))
		emit("unset length %d %d", n, f.must("pcre2_substring_length_bynumber_8", md, int32(n), s))
		buf, m := f.buf(4, 0xaa)
		s, ss := f.ref("RefU64", uint64(4))
		r := f.must("pcre2_substring_copy_bynumber_8", md, n, buf, s).(int32)
		emit("unset copy %d %d %d %s", n, r, ss.Fields[0], joinBytes(m))
	}

	golden, err := os.ReadFile("testdata/pcre.golden")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Split(strings.TrimSuffix(string(golden), "\n"), "\n")
	for i := 0; i < max(len(got), len(want)); i++ {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g == "jit -45" && w == "jit 0" {
			// go-regexp/pcre2 has no JIT: ERROR_JIT_BADOPTION, which pcre.jl
			// takes as "no JIT" (PCRE.jit_compile) and matches by interpretation
			continue
		}
		if g != w {
			t.Errorf("line %d:\n go    %s\n julia %s", i+1, g, w)
		}
	}
}
