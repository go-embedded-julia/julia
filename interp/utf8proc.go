package interp

import (
	"encoding/binary"
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/internal/utf8proc"
)

// The utf8proc calls of Base.Unicode (base/strings/unicode.jl), over
// internal/utf8proc, a port of utf8proc v2.10.0 (the release julia 1.12.6 pins,
// deps/utf8proc.version): case mappings (toupper, tolower, totitle, islower,
// isupper), category, charwidth, the grapheme breaks, and normalization
// through utf8proc_decompose(_custom) and utf8proc_reencode. The Unicode
// stdlib's utf8proc_decompose_char and utf8proc_get_property are not here: it is
// not one of the seven stdlibs of the image.

func (vm *VM) bootUtf8proc() {
	// UInt32 -> UInt32 and UInt32 -> Cint, as Base declares them
	toUInt32 := func(name string, f func(int32) int32) shim {
		return func(vm *VM, a []any) (any, error) {
			c, err := vm.utf8procChar(name, a[0])
			return uint32(f(c)), err
		}
	}
	toCint := func(name string, f func(int32) int) shim {
		return func(vm *VM, a []any) (any, error) {
			c, err := vm.utf8procChar(name, a[0])
			return int32(f(c)), err
		}
	}
	vm.shims["utf8proc_toupper"] = toUInt32("utf8proc_toupper", utf8proc.ToUpper)
	vm.shims["utf8proc_tolower"] = toUInt32("utf8proc_tolower", utf8proc.ToLower)
	vm.shims["utf8proc_totitle"] = toUInt32("utf8proc_totitle", utf8proc.ToTitle)
	vm.shims["utf8proc_charwidth"] = toCint("utf8proc_charwidth", utf8proc.CharWidth)
	vm.shims["utf8proc_category"] = toCint("utf8proc_category", utf8proc.Category)
	vm.shims["utf8proc_islower"] = toCint("utf8proc_islower", func(c int32) int { return cbi(utf8proc.IsLower(c)) })
	vm.shims["utf8proc_isupper"] = toCint("utf8proc_isupper", func(c int32) int { return cbi(utf8proc.IsUpper(c)) })
	vm.shims["utf8proc_category_string"] = func(vm *VM, a []any) (any, error) {
		c, err := vm.utf8procChar("utf8proc_category_string", a[0])
		return vm.stringPtrOf(utf8proc.CategoryString(c)), err
	}
	vm.shims["utf8proc_errmsg"] = func(vm *VM, a []any) (any, error) {
		code, ok := a[0].(int64)
		if !ok {
			return nil, vm.typeError("ccall", "", core.Int64Type, a[0])
		}
		return vm.stringPtrOf(utf8proc.Errmsg(int(code))), nil
	}
	vm.shims["utf8proc_grapheme_break"] = func(vm *VM, a []any) (any, error) {
		c1, err := vm.utf8procChar("utf8proc_grapheme_break", a[0])
		if err != nil {
			return nil, err
		}
		c2, err := vm.utf8procChar("utf8proc_grapheme_break", a[1])
		return utf8proc.GraphemeBreak(c1, c2), err
	}
	vm.shims["utf8proc_grapheme_break_stateful"] = func(vm *VM, a []any) (any, error) { return vm.graphemeBreakStateful(a) }
	vm.shims["utf8proc_decompose"] = func(vm *VM, a []any) (any, error) { return vm.utf8procDecompose("utf8proc_decompose", a, nil) }
	vm.shims["utf8proc_decompose_custom"] = func(vm *VM, a []any) (any, error) {
		// the custom function is a cfunction pointer, called with the code point
		// and the Ref the caller passed
		var cerr error
		custom := func(c int32) int32 {
			if cerr != nil {
				return c
			}
			r, err := vm.callCFunction(a[5], uint32(c), a[6])
			if err != nil {
				cerr = err
				return c
			}
			u, ok := r.(uint32)
			if !ok {
				cerr = vm.typeError("ccall", "", core.UInt32Type, r)
				return c
			}
			return int32(u)
		}
		n, err := vm.utf8procDecompose("utf8proc_decompose_custom", a, custom)
		if cerr != nil {
			return nil, cerr
		}
		return n, err
	}
	vm.shims["utf8proc_reencode"] = func(vm *VM, a []any) (any, error) { return vm.utf8procReencode(a) }
}

func cbi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// utf8procChar is a UInt32 code point argument, as the C function takes an
// int32: a value above 2^31 is as C would read it, outside the Unicode range.
func (vm *VM) utf8procChar(name string, v any) (int32, error) {
	switch x := v.(type) {
	case uint32:
		return int32(x), nil
	case int32:
		return x, nil
	}
	return 0, vm.typeError("ccall", "", core.UInt32Type, v)
}

// graphemeBreakStateful is utf8proc_grapheme_break_stateful(c1, c2, Ref{Int32}):
// the state is read from, and written back to, the Ref.
func (vm *VM) graphemeBreakStateful(a []any) (any, error) {
	c1, err := vm.utf8procChar("utf8proc_grapheme_break_stateful", a[0])
	if err != nil {
		return nil, err
	}
	c2, err := vm.utf8procChar("utf8proc_grapheme_break_stateful", a[1])
	if err != nil {
		return nil, err
	}
	p, ok := a[2].(*Pointer)
	if !ok {
		return nil, fmt.Errorf("%w: utf8proc_grapheme_break_stateful of %s", ErrUnsupported, showValue(vm.newTuple(a)))
	}
	sv, err := vm.ptrLoad("utf8proc_grapheme_break_stateful", p, 1)
	if err != nil {
		return nil, err
	}
	state, _ := sv.(int32)
	brk := utf8proc.GraphemeBreakStateful(c1, c2, &state)
	if err := vm.ptrStore("utf8proc_grapheme_break_stateful", p, state, 1); err != nil {
		return nil, err
	}
	return brk, nil
}

// utf8procDecompose is utf8proc_decompose(str, len, buffer, nwords, options) and
// the custom variant (whose two last arguments custom stands for): the code
// points of the string into buffer (C_NULL for none), how many there are or a
// negative error. The words are written as the bytes of an Int32 each, as the
// caller's Vector{UInt8} holds them.
func (vm *VM) utf8procDecompose(name string, a []any, custom func(int32) int32) (any, error) {
	n, ok1 := a[1].(int64)
	nwords, ok2 := a[3].(int64)
	opts, ok3 := a[4].(int32)
	if !ok1 || !ok2 || !ok3 || n < 0 || nwords < 0 {
		return nil, fmt.Errorf("%w: %s of %s", ErrUnsupported, name, showValue(vm.newTuple(a)))
	}
	if n > int64(vm.MaxMemoryElems) || nwords > int64(vm.MaxMemoryElems) {
		return nil, fmt.Errorf("%w: %s of %d bytes into %d words (VM.MaxMemoryElems is %d)", ErrMemoryLimit, name, n, nwords, vm.MaxMemoryElems)
	}
	var str []byte
	if n > 0 {
		p, isPtr := a[0].(*Pointer)
		if !isPtr {
			return nil, fmt.Errorf("%w: %s of the string %s", ErrUnsupported, name, showValue(a[0]))
		}
		b, ok := vm.bytesAt(p, uint64(n))
		if !ok {
			return nil, fmt.Errorf("%w: %s of the string %s", ErrUnsupported, name, showValue(a[0]))
		}
		str = b
	}
	var buf []int32
	if dst, isPtr := a[2].(*Pointer); isPtr && (dst.Target != nil || dst.Off != 0) {
		buf = make([]int32, nwords)
	}
	got := utf8proc.DecomposeCustom(str, buf, utf8proc.Option(opts), custom)
	if got > 0 && len(buf) > 0 {
		words := min(got, len(buf))
		raw := make([]byte, 4*words)
		for i := 0; i < words; i++ {
			binary.LittleEndian.PutUint32(raw[4*i:], uint32(buf[i]))
		}
		if err := vm.putBytes8(a[2], raw); err != nil {
			return nil, err
		}
	}
	return int64(got), nil
}

// utf8procReencode is utf8proc_reencode(buffer, nwords, options): the words in
// the buffer, normalized and encoded as UTF-8 over the buffer, and a NUL after
// them; how many bytes.
func (vm *VM) utf8procReencode(a []any) (any, error) {
	nwords, ok1 := a[1].(int64)
	opts, ok2 := a[2].(int32)
	p, ok3 := a[0].(*Pointer)
	if !ok1 || !ok2 || !ok3 || nwords < 0 {
		return nil, fmt.Errorf("%w: utf8proc_reencode of %s", ErrUnsupported, showValue(vm.newTuple(a)))
	}
	if nwords > int64(vm.MaxMemoryElems) {
		return nil, fmt.Errorf("%w: utf8proc_reencode of %d words (VM.MaxMemoryElems is %d)", ErrMemoryLimit, nwords, vm.MaxMemoryElems)
	}
	raw, ok := vm.bytesAt(p, uint64(4*nwords))
	if !ok {
		return nil, fmt.Errorf("%w: utf8proc_reencode of the buffer %s", ErrUnsupported, showValue(a[0]))
	}
	words := make([]int32, nwords)
	for i := range words {
		words[i] = int32(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	out := utf8proc.Reencode(words, len(words), utf8proc.Option(opts))
	if err := vm.putBytes8(p, out); err != nil {
		return nil, err
	}
	// the NUL after the bytes (utf8proc.c:749) goes in the byte the caller
	// leaves free: julia's buffer is exactly nwords*4 bytes, and a string whose
	// characters all take four leaves none, where C writes past it into the
	// memory's slack; here it is written when there is room
	end := &Pointer{Type: p.Type, Target: p.Target, Off: p.Off + int64(len(out))}
	vm.putBytes8(end, []byte{0})
	return int64(len(out)), nil
}

// putBytes8 is writeBytes, with the common case done at once: a pointer into a
// memory of bytes (a Vector{UInt8}, the buffer Base.Unicode makes) is filled
// element by element, where writeBytes stores each byte through a pointer of
// its own, which for the megabytes of a normalization is most of its time.
func (vm *VM) putBytes8(dst any, b []byte) error {
	if p, ok := dst.(*Pointer); ok {
		if m, ok := p.Target.(*Memory); ok && m.Type == memUInt8Type && p.Off >= 0 && p.Off+int64(len(b)) <= int64(m.Len()) {
			dst8, _ := m.WrittenBytes(int(p.Off), int(p.Off)+len(b))
			copy(dst8, b)
			return nil
		}
	}
	return vm.writeBytes(dst, b)
}
