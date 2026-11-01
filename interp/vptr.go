package interp

import (
	"fmt"
	"io"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Pointers without addresses. Julia code reaches C through Ptr values: the
// address of a C global (cglobal), of a String's bytes (jl_string_ptr), of
// an output stream. Here a Ptr is a Go value that names what it points
// into and a byte offset, never a host address: a program can read and
// write only what a pointer was made from, and a pointer made from an
// integer (C_NULL) points at nothing.

// Pointer is a Ptr{T} value.
type Pointer struct {
	Type   *core.DataType
	Target any // a ptrSlot, a *outStream, a string (its bytes), or nil
	Off    int64
}

// ptrSlot is a C global variable cglobal names: a cell holding a value.
type ptrSlot struct {
	name expr.Symbol
	val  any
}

// outStream is one of the VM's output streams (jl_uv_stdout,
// jl_uv_stderr), writing to VM.Stdout or VM.Stderr.
type outStream struct {
	name string
	w    func() io.Writer
}

func (vm *VM) ptrType(t any) *core.DataType {
	return must(core.Apply(core.PtrType, t)).(*core.DataType)
}

// bootPointers makes the C globals cglobal can name.
func (vm *VM) bootPointers() {
	voidPtr := vm.ptrType(core.NothingType)
	vm.cglobals = map[expr.Symbol]*ptrSlot{}
	for _, s := range []*outStream{
		{"stdout", func() io.Writer { return vm.Stdout }},
		{"stderr", func() io.Writer { return vm.Stderr }},
	} {
		name := expr.Symbol("jl_uv_" + s.name)
		vm.cglobals[name] = &ptrSlot{name: name, val: &Pointer{Type: voidPtr, Target: s}}
	}
}

// biCglobal is jl_cglobal (runtime_intrinsics.c:634-679) for the C globals
// the VM has: a pointer to the slot, as a Ptr{ty}.
func biCglobal(vm *VM, a []any) (any, error) {
	if !core.IsType(a[1]) {
		return nil, vm.typeError("cglobal", "", core.TypeType, a[1])
	}
	var rt *core.DataType
	if a[1] == any(core.NothingType) {
		rt = vm.ptrType(core.NothingType)
	} else {
		rt = vm.ptrType(a[1])
	}
	if !rt.IsConcrete {
		return nil, vm.errorf("cglobal: type argument not concrete")
	}
	name, ok := a[0].(expr.Symbol)
	if !ok {
		return nil, vm.typeError("cglobal", "", core.SymbolType, a[0])
	}
	slot := vm.cglobals[name]
	if slot == nil && name == "jl_options" {
		// the command-line options, read through the struct Base mirrors
		// them with (options.jl)
		d, ok := a[1].(*core.DataType)
		if !ok {
			return nil, fmt.Errorf("%w: jl_options as %s", ErrUnsupported, showValue(a[1]))
		}
		opts, err := vm.defaultOptions(d)
		if err != nil {
			return nil, err
		}
		slot = &ptrSlot{name: name, val: opts}
		vm.cglobals[name] = slot
	}
	if slot == nil {
		return nil, fmt.Errorf("%w: cglobal(:%s)", ErrUnsupported, name)
	}
	return &Pointer{Type: rt, Target: slot}, nil
}

// biPointerref is jl_pointerref (runtime_intrinsics.c:412-429) for what a
// pointer here can address: a C global's value, a String's bytes.
func biPointerref(vm *VM, a []any) (any, error) {
	p, ok := a[0].(*Pointer)
	if err := vm.typechk("pointerref", core.PtrType, a[0], ok); err != nil {
		return nil, err
	}
	i, ok := a[1].(int64)
	if err := vm.typechk("pointerref", core.Int64Type, a[1], ok); err != nil {
		return nil, err
	}
	if _, ok := a[2].(int64); !ok {
		return nil, vm.typeError("pointerref", "", core.Int64Type, a[2])
	}
	switch t := p.Target.(type) {
	case *ptrSlot:
		if i != 1 || p.Off != 0 {
			return nil, vm.errorf("pointerref: invalid pointer")
		}
		return t.val, nil
	case string:
		if p.Type.Parameters[0] != any(core.UInt8Type) {
			return nil, fmt.Errorf("%w: pointerref of a String as %s", ErrUnsupported, showValue(p.Type))
		}
		at := p.Off + i - 1
		if at < 0 || at >= int64(len(t)) {
			return nil, vm.errorf("pointerref: invalid pointer")
		}
		return t[at], nil
	}
	return nil, vm.errorf("pointerref: invalid pointer")
}

// stringPtr is jl_string_ptr: a Ptr{UInt8} to the bytes of s.
func (vm *VM) stringPtr(a []any) (any, error) {
	s, ok := a[0].(string)
	if !ok {
		return nil, vm.typeError("ccall", "", core.StringType, a[0])
	}
	return &Pointer{Type: vm.ptrType(core.UInt8Type), Target: s}, nil
}

// streamOf is the output stream a Ptr{Cvoid} points at.
func (vm *VM) streamOf(v any) (*outStream, error) {
	if p, ok := v.(*Pointer); ok {
		if s, ok := p.Target.(*outStream); ok {
			return s, nil
		}
	}
	return nil, vm.errorf("ccall: not an output stream: %s", showValue(v))
}

// uvPuts is jl_uv_puts(stream, bytes, n): n bytes from a String pointer.
func (vm *VM) uvPuts(a []any) (any, error) {
	s, err := vm.streamOf(a[0])
	if err != nil {
		return nil, err
	}
	p, ok := a[1].(*Pointer)
	str, ok2 := p.Target.(string)
	if !ok || !ok2 {
		return nil, fmt.Errorf("%w: jl_uv_puts of %s", ErrUnsupported, showValue(a[1]))
	}
	var n int64
	switch x := a[2].(type) {
	case int64:
		n = x
	case uint64:
		n = int64(x)
	}
	if n < 0 || p.Off < 0 || p.Off+n > int64(len(str)) {
		return nil, vm.errorf("jl_uv_puts: %d bytes past the string", n)
	}
	_, err = io.WriteString(s.w(), str[p.Off:p.Off+n])
	return nil, err
}

// uvPutb is jl_uv_putb(stream, byte).
func (vm *VM) uvPutb(a []any) (any, error) {
	s, err := vm.streamOf(a[0])
	if err != nil {
		return nil, err
	}
	_, err = s.w().Write([]byte{a[1].(uint8)})
	return nil, err
}

// uvPutc is jl_uv_putc(stream, char): the Char's UTF-8 bytes, as julia
// stores them.
func (vm *VM) uvPutc(a []any) (any, error) {
	s, err := vm.streamOf(a[0])
	if err != nil {
		return nil, err
	}
	u := uint32(a[1].(expr.Char))
	b := []byte{byte(u >> 24)}
	for u <<= 8; u != 0; u <<= 8 {
		b = append(b, byte(u>>24))
	}
	_, err = s.w().Write(b)
	return nil, err
}

// staticShow is jl_static_show(stream, x): C's show of any value, here
// the interpreter's repr.
func (vm *VM) staticShow(a []any) (any, error) {
	s, err := vm.streamOf(a[0])
	if err != nil {
		return nil, err
	}
	_, err = io.WriteString(s.w(), showValue(a[1]))
	return nil, err
}

// biSizeof is jl_f_sizeof (builtins.c:530-570) for what has a size
// without a struct layout: strings, symbols, simple vectors, primitive
// values and types, memories of primitive elements.
func biSizeof(vm *VM, args []any) (any, error) {
	if err := vm.nargs("sizeof", args, 1, 1); err != nil {
		return nil, err
	}
	switch x := args[0].(type) {
	case string:
		return int64(len(x)), nil
	case expr.Symbol:
		return int64(len(x)), nil
	case SVec:
		return int64(1+len(x)) * 8, nil
	case *Memory:
		if bits := x.Type.Parameters[1]; bits != nil {
			if d, ok := bits.(*core.DataType); ok && d.Name.Bits > 0 {
				return int64(d.Name.Bits/8) * int64(len(x.Elems)), nil
			}
		}
		return nil, fmt.Errorf("%w: sizeof of a %s", ErrUnsupported, showValue(x.Type))
	case *core.DataType:
		if x.Name.Abstract {
			return nil, vm.errorf("Abstract type %s does not have a definite size.", x.Name.Name)
		}
		size, _, err := dataLayout(x)
		if err != nil {
			return nil, err
		}
		return int64(size), nil
	}
	if args[0] == any(core.Bottom) {
		return nil, vm.errorf("The empty type does not have a definite size since it does not have instances.")
	}
	if _, w, ok := primBits(args[0]); ok {
		return int64(w / 8), nil
	}
	return nil, fmt.Errorf("%w: sizeof of a %s", ErrUnsupported, showValue(vm.typeOf(args[0])))
}
