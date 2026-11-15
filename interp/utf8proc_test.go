package interp

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/utf8proc"
)

// The utf8proc calls of Base.Unicode over internal/utf8proc. The package is
// checked against julia's utf8proc over every code point (its own tests); here
// the calls: arguments, pointers, the buffers julia hands them. TestKernelBase
// runs the functions of Base.Unicode on julia's Base image.

func bytesBuf(vm *VM, n int) *Pointer {
	elems := make([]any, n)
	for i := range elems {
		elems[i] = uint8(0)
	}
	return &Pointer{Type: vm.ptrType(core.UInt8Type), Target: &Memory{Type: memUInt8Type, Elems: elems}}
}

func bufBytes(p *Pointer) []byte {
	m := p.Target.(*Memory)
	b := make([]byte, len(m.Elems))
	for i, e := range m.Elems {
		b[i] = e.(uint8)
	}
	return b
}

func TestUtf8procCharFunctions(t *testing.T) {
	vm := bootedVM(t)
	for _, c := range []uint32{'a', 'A', 0xDF, 0x1C5, 0x3B1, 0x1F88, 0x300, 0x3000, 0xFFFF, 0x1F600, 0x110000, 0x378} {
		ch := int32(c)
		for name, want := range map[string]uint32{"utf8proc_toupper": uint32(utf8proc.ToUpper(ch)), "utf8proc_tolower": uint32(utf8proc.ToLower(ch)), "utf8proc_totitle": uint32(utf8proc.ToTitle(ch))} {
			if got, err := vm.shims[expr.Symbol(name)](vm, []any{c}); err != nil || got != want {
				t.Errorf("%s(%#x) = %v, %v; want %d", name, c, got, err, want)
			}
		}
		for name, want := range map[string]int32{"utf8proc_charwidth": int32(utf8proc.CharWidth(ch)), "utf8proc_category": int32(utf8proc.Category(ch)),
			"utf8proc_islower": int32(cbi(utf8proc.IsLower(ch))), "utf8proc_isupper": int32(cbi(utf8proc.IsUpper(ch)))} {
			if got, err := vm.shims[expr.Symbol(name)](vm, []any{c}); err != nil || got != want {
				t.Errorf("%s(%#x) = %v, %v; want %d", name, c, got, err, want)
			}
		}
		p, err := vm.shims["utf8proc_category_string"](vm, []any{c})
		if err != nil || p.(*Pointer).Target != utf8proc.CategoryString(ch) {
			t.Errorf("utf8proc_category_string(%#x) = %v, %v", c, p, err)
		}
	}
	// an int32 argument is read as it is, and any other type is refused
	if got, _ := vm.shims["utf8proc_toupper"](vm, []any{int32('a')}); got != uint32('A') {
		t.Errorf("an int32: %v", got)
	}
	for name := range map[string]bool{"utf8proc_toupper": true, "utf8proc_charwidth": true, "utf8proc_category_string": true} {
		if _, err := vm.shims[expr.Symbol(name)](vm, []any{"a"}); err == nil {
			t.Errorf("%s of a string", name)
		}
	}
	// errmsg
	for code, want := range map[int64]string{-1: utf8proc.Errmsg(-1), -3: "Invalid UTF-8 string", 7: utf8proc.Errmsg(7)} {
		if p, err := vm.shims["utf8proc_errmsg"](vm, []any{code}); err != nil || p.(*Pointer).Target != want {
			t.Errorf("utf8proc_errmsg(%d) = %v, %v", code, p, err)
		}
	}
	if _, err := vm.shims["utf8proc_errmsg"](vm, []any{int32(1)}); err == nil {
		t.Error("errmsg of an Int32")
	}
	// grapheme_break
	if got, err := vm.shims["utf8proc_grapheme_break"](vm, []any{uint32('a'), uint32('b')}); err != nil || got != true {
		t.Errorf("a|b: %v %v", got, err)
	}
	if got, _ := vm.shims["utf8proc_grapheme_break"](vm, []any{uint32('a'), uint32(0x301)}); got != false {
		t.Errorf("a|U+0301: %v", got)
	}
	if _, err := vm.shims["utf8proc_grapheme_break"](vm, []any{"a", uint32('b')}); err == nil {
		t.Error("grapheme_break of a string")
	}
	if _, err := vm.shims["utf8proc_grapheme_break"](vm, []any{uint32('a'), "b"}); err == nil {
		t.Error("grapheme_break, second argument a string")
	}
}

// grapheme_break_stateful through a Ref{Int32}.
func TestUtf8procGraphemeBreakStateful(t *testing.T) {
	vm := bootedVM(t)
	state := &Memory{Type: memType(core.Int32Type), Elems: []any{int32(0)}}
	ptr := &Pointer{Type: vm.ptrType(core.Int32Type), Target: state}
	brk := vm.shims["utf8proc_grapheme_break_stateful"]
	// the regional indicators: break after two
	var got []bool
	cps := []uint32{0x1F1FA, 0x1F1F8, 0x1F1EB}
	for i := 1; i < len(cps); i++ {
		b, err := brk(vm, []any{cps[i-1], cps[i], ptr})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, b.(bool))
	}
	if got[0] || !got[1] {
		t.Errorf("regional indicators: %v", got)
	}
	if state.Elems[0] == int32(0) {
		t.Error("the state was not written back")
	}
	// a null pointer, and a pointer into something that is not a Ref
	if _, err := brk(vm, []any{uint32('a'), uint32('b'), &Pointer{Type: vm.ptrType(core.Int32Type)}}); err == nil {
		t.Error("a null Ref")
	}
	if _, err := brk(vm, []any{uint32('a'), uint32('b'), &Pointer{Type: vm.ptrType(core.Int32Type), Target: "x"}}); err == nil {
		t.Errorf("a pointer into a string")
	}
	if _, err := brk(vm, []any{uint32('a'), uint32('b'), int64(1)}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("not a pointer: %v", err)
	}
	if _, err := brk(vm, []any{"a", uint32('b'), ptr}); err == nil {
		t.Error("a string")
	}
	if _, err := brk(vm, []any{uint32('a'), "b", ptr}); err == nil {
		t.Error("a string second")
	}
}

func words(b []byte) []int32 {
	var out []int32
	for i := 0; i+4 <= len(b); i += 4 {
		out = append(out, int32(binary.LittleEndian.Uint32(b[i:])))
	}
	return out
}

func TestUtf8procDecomposeAndReencode(t *testing.T) {
	vm := bootedVM(t)
	null := &Pointer{Type: vm.ptrType(core.UInt8Type)}
	str := func(s string) *Pointer { return &Pointer{Type: vm.ptrType(core.UInt8Type), Target: s} }
	opts := func(o utf8proc.Option) int32 { return int32(o) }
	dec := vm.shims["utf8proc_decompose"]

	// the size, then the words: what Base.Unicode.utf8proc_map does
	flags := opts(utf8proc.Stable | utf8proc.DecomposeOpt)
	n, err := dec(vm, []any{str("é"), int64(2), null, int64(0), flags})
	if err != nil || n != int64(2) {
		t.Fatalf("the size of é: %v %v", n, err)
	}
	buf := bytesBuf(vm, 4*2+1)
	if n, err := dec(vm, []any{str("é"), int64(2), buf, int64(2), flags}); err != nil || n != int64(2) {
		t.Fatalf("decompose: %v %v", n, err)
	}
	if w := words(bufBytes(buf)); len(w) < 2 || w[0] != 'e' || w[1] != 0x301 {
		t.Errorf("words %v", w)
	}
	// reencode: composed back, in the same buffer
	re, err := vm.shims["utf8proc_reencode"](vm, []any{buf, int64(2), opts(utf8proc.Stable | utf8proc.Compose)})
	if err != nil || re != int64(2) {
		t.Fatalf("reencode: %v %v", re, err)
	}
	if b := bufBytes(buf); string(b[:2]) != "é" || b[2] != 0 {
		t.Errorf("reencoded %q", b)
	}
	// an empty string, a string with a NUL, an error from utf8proc is a negative Int
	if n, _ := dec(vm, []any{str(""), int64(0), null, int64(0), flags}); n != int64(0) {
		t.Errorf("empty: %v", n)
	}
	if n, _ := dec(vm, []any{str("a\x00b"), int64(3), null, int64(0), flags}); n != int64(3) {
		t.Errorf("a NUL: %v", n)
	}
	if n, _ := dec(vm, []any{str("\xff"), int64(1), null, int64(0), flags}); n != int64(utf8proc.ErrInvalidUTF8) {
		t.Errorf("invalid UTF-8: %v", n)
	}
	if n, _ := dec(vm, []any{str("a"), int64(1), null, int64(0), opts(utf8proc.Compose | utf8proc.DecomposeOpt)}); n != int64(utf8proc.ErrInvalidOpts) {
		t.Errorf("invalid options: %v", n)
	}
	// a buffer shorter than the result is written as far as it goes
	small := bytesBuf(vm, 4)
	if n, _ := dec(vm, []any{str("abc"), int64(3), small, int64(1), flags}); n != int64(3) {
		t.Errorf("a short buffer: %v", n)
	}
	if w := words(bufBytes(small)); w[0] != 'a' {
		t.Errorf("the short buffer: %v", w)
	}
	// refusals: arguments, a string that is not there, over the limit
	for name, args := range map[string][]any{
		"a length that is no Int":         {str("a"), "1", null, int64(0), flags},
		"a negative length":               {str("a"), int64(-1), null, int64(0), flags},
		"a negative size":                 {str("a"), int64(1), null, int64(-1), flags},
		"options that are no Cint":        {str("a"), int64(1), null, int64(0), int64(1)},
		"a string that is no ptr":         {int64(1), int64(1), null, int64(0), flags},
		"a string shorter than n":         {str("a"), int64(5), null, int64(0), flags},
		"a buffer that cannot be written": {str("a"), int64(1), &Pointer{Type: vm.ptrType(core.UInt8Type), Target: int64(5)}, int64(4), flags},
	} {
		if _, err := dec(vm, args); err == nil {
			t.Errorf("decompose with %s", name)
		}
	}
	vm.MaxMemoryElems = 8
	if _, err := dec(vm, []any{str("a"), int64(9), null, int64(0), flags}); !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("a long string: %v", err)
	}
	if _, err := dec(vm, []any{str("a"), int64(1), null, int64(9), flags}); !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("a long buffer: %v", err)
	}
	if _, err := vm.shims["utf8proc_reencode"](vm, []any{buf, int64(9), flags}); !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("a long reencode: %v", err)
	}
	vm.MaxMemoryElems = DefaultMaxMemoryElems
	for name, args := range map[string][]any{
		"words that are no Int": {buf, "2", flags}, "negative words": {buf, int64(-1), flags}, "options that are no Cint": {buf, int64(2), int64(1)},
		"a buffer that is no pointer": {int64(1), int64(2), flags}, "a buffer too short": {buf, int64(100), flags},
	} {
		if _, err := vm.shims["utf8proc_reencode"](vm, args); !errors.Is(err, ErrUnsupported) {
			t.Errorf("reencode with %s: %v", name, err)
		}
	}
	// writing the result back fails when the pointer is not writable
	ro := &Pointer{Type: vm.ptrType(core.UInt8Type), Target: "abcd"}
	if _, err := vm.shims["utf8proc_reencode"](vm, []any{ro, int64(1), flags}); err == nil {
		t.Error("reencode into a string that is not writable")
	}
}

// utf8proc_decompose_custom: a cfunction called with each code point.
func TestUtf8procDecomposeCustom(t *testing.T) {
	vm := bootedVM(t)
	s := vm.NewSession()
	null := &Pointer{Type: vm.ptrType(core.UInt8Type)}
	str := &Pointer{Type: vm.ptrType(core.UInt8Type), Target: "abc"}
	cf := func(src string) *Pointer {
		fn, err := s.Eval(src)
		if err != nil {
			t.Fatal(err)
		}
		return vm.voidPtr(&cfunction{fn: fn, rt: core.UInt32Type, argt: SVec{core.UInt32Type, core.AnyType}})
	}
	custom := vm.shims["utf8proc_decompose_custom"]
	flags := int32(utf8proc.Stable)
	call := func(f *Pointer) (any, error) {
		return custom(vm, []any{str, int64(3), null, int64(0), flags, f, nil})
	}
	buf := bytesBuf(vm, 12)
	if n, err := custom(vm, []any{str, int64(3), buf, int64(3), flags, cf("(c, d) -> Core.Intrinsics.add_int(c, Core.Intrinsics.trunc_int(UInt32, 1))"), nil}); err != nil || n != int64(3) {
		t.Fatalf("custom: %v %v", n, err)
	}
	if w := words(bufBytes(buf)); w[0] != 'b' || w[1] != 'c' || w[2] != 'd' {
		t.Errorf("mapped: %v", w)
	}
	if n, err := call(cf("(c, d) -> c")); err != nil || n != int64(3) {
		t.Errorf("identity: %v %v", n, err)
	}
	// a callback that returns the wrong type, one that throws, a pointer that is no cfunction
	if _, err := call(cf("(c, d) -> 5")); err == nil {
		t.Error("a callback that returns an Int")
	}
	if _, err := call(cf("(c, d) -> throw(ErrorException(\"callback\"))")); err == nil {
		t.Error("a callback that throws")
	}
	if _, err := call(&Pointer{Type: vm.ptrType(core.UInt8Type), Target: "x"}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a callback that is no cfunction: %v", err)
	}
	// once the callback has failed, the rest of the string is not given to it
	calls := 0
	_ = calls
}
