package interp

import (
	"fmt"
	"io"
	"strings"

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
	return vm.ptrLoad("pointerref", p, i)
}

// ptrLoad is the load of jl_pointerref and jl_atomic_pointerref, named as
// the intrinsic fname: element i (1-based) of what p points into.
func (vm *VM) ptrLoad(fname string, p *Pointer, i int64) (any, error) {
	switch t := p.Target.(type) {
	case *Memory:
		if at, ok := reinterpreted(p, t); ok {
			return vm.memLoadBytes(fname, t, p.Off+(i-1)*int64(at.Name.Bits/8), at)
		}
		k, err := vm.memIndex(fname, p, t, p.Type.Parameters[0], i)
		if err != nil {
			return nil, err
		}
		if t.Elems[k] == undef {
			return nil, vm.exception(vm.tUndefRef)
		}
		return t.Elems[k], nil
	case layoutOf:
		st, ok := p.Type.Parameters[0].(*core.DataType)
		if !ok || i != 1 || p.Off != 0 || len(st.Name.FieldNames) != 6 {
			return nil, fmt.Errorf("%w: %s of a layout as %s", ErrUnsupported, fname, showValue(p.Type))
		}
		return vm.layoutStruct(t.t, st)
	case *ptrSlot:
		if i != 1 || p.Off != 0 {
			return nil, vm.errorf("%s: invalid pointer", fname)
		}
		return t.val, nil
	case string:
		if p.Type.Parameters[0] != any(core.UInt8Type) {
			return nil, fmt.Errorf("%w: %s of a String as %s", ErrUnsupported, fname, showValue(p.Type))
		}
		at := p.Off + i - 1
		if at < 0 || at >= int64(len(t)) {
			return nil, vm.errorf("%s: invalid pointer", fname)
		}
		return t[at], nil
	}
	return nil, vm.errorf("%s: invalid pointer", fname)
}

// stringPtr is jl_string_ptr: a Ptr{UInt8} to the bytes of s.
func (vm *VM) stringPtr(a []any) (any, error) {
	s, ok := a[0].(string)
	if !ok {
		return nil, vm.typeError("ccall", "", core.StringType, a[0])
	}
	return vm.stringPtrOf(s), nil
}

// stringPtrOf is a Ptr{UInt8} to s's first byte.
func (vm *VM) stringPtrOf(s string) *Pointer {
	return &Pointer{Type: vm.ptrType(core.UInt8Type), Target: s}
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
	str, ok := stringBytes(a[1], a[2])
	if !ok {
		return nil, fmt.Errorf("%w: jl_uv_puts of %s bytes at %s", ErrUnsupported, showValue(a[2]), showValue(a[1]))
	}
	_, err = io.WriteString(s.w(), str)
	return nil, err
}

// cstrToString is jl_cstr_to_string (array.c): a String of the bytes from p
// to the first NUL. A String's bytes end with an implied NUL (julia's
// strings and symbol names are NUL-terminated); a Memory{UInt8} must hold
// one, as C would read past it.
func (vm *VM) cstrToString(a []any) (any, error) {
	p, ok := a[0].(*Pointer)
	if !ok {
		return nil, vm.typeError("ccall", "", vm.ptrType(core.UInt8Type), a[0])
	}
	switch t := p.Target.(type) {
	case string:
		if p.Off < 0 || p.Off > int64(len(t)) {
			return nil, vm.errorf("jl_cstr_to_string: invalid pointer")
		}
		s := t[p.Off:]
		if i := strings.IndexByte(s, 0); i >= 0 {
			s = s[:i]
		}
		return s, nil
	case *Memory:
		if t.Type.Parameters[1] == any(core.UInt8Type) && p.Off >= 0 {
			var b []byte
			for i := p.Off; i < int64(len(t.Elems)); i++ {
				c, _ := t.Elems[i].(uint8)
				if c == 0 {
					return string(b), nil
				}
				b = append(b, c)
			}
		}
	}
	return nil, fmt.Errorf("%w: jl_cstr_to_string of %s", ErrUnsupported, showValue(a[0]))
}

// stringBytes is the n bytes a pointer into a String points at, false for
// any other pointer, a count that is not an integer or a range past the
// string.
func stringBytes(ptr, count any) (string, bool) {
	p, ok := ptr.(*Pointer)
	if !ok {
		return "", false
	}
	str, ok := p.Target.(string)
	if !ok {
		return "", false
	}
	var n int64
	switch x := count.(type) {
	case int64:
		n = x
	case uint64:
		if x > uint64(len(str)) {
			return "", false
		}
		n = int64(x)
	default:
		return "", false
	}
	if n < 0 || p.Off < 0 || p.Off > int64(len(str)) || n > int64(len(str))-p.Off {
		return "", false
	}
	return str[p.Off : p.Off+n], true
}

// symbolN is jl_symbol_n(str, len) (symbol.c:113-118).
func (vm *VM) symbolN(a []any) (any, error) {
	s, ok := stringBytes(a[0], a[1])
	if !ok {
		return nil, fmt.Errorf("%w: jl_symbol_n of %s bytes at %s", ErrUnsupported, showValue(a[1]), showValue(a[0]))
	}
	if strings.IndexByte(s, 0) >= 0 {
		return nil, vm.exception(vm.tArgError, "Symbol name may not contain \\0")
	}
	return expr.Symbol(s), nil
}

// gensym is jl_gensym and jl_tagged_gensym(str, len) (symbol.c:129-163):
// ##ctr, or ##str#ctr, from one counter starting at 0 (gs_ctr); str is a
// pointer into a String and len its length, or -1 for a NUL-ended one.
func (vm *VM) gensym(a []any) (any, error) {
	ctr := vm.gsCtr
	vm.gsCtr++
	if len(a) == 0 {
		return expr.Symbol(fmt.Sprintf("##%d", ctr)), nil
	}
	var s string
	ok := false
	if n, isU := a[1].(uint64); isU && n == ^uint64(0) {
		// strlen: to the first NUL from where the pointer points
		if s, ok = stringBytes(a[0], int64(0)); ok {
			p := a[0].(*Pointer)
			rest := p.Target.(string)[p.Off:]
			s, _, _ = strings.Cut(rest, "\x00")
		}
	} else {
		s, ok = stringBytes(a[0], a[1])
		if ok && strings.IndexByte(s, 0) >= 0 {
			vm.gsCtr--
			return nil, vm.exception(vm.tArgError, "Symbol name may not contain \\0")
		}
	}
	if !ok {
		vm.gsCtr--
		return nil, fmt.Errorf("%w: jl_tagged_gensym of %s", ErrUnsupported, showValue(vm.newTuple(a)))
	}
	return expr.Symbol(fmt.Sprintf("##%s#%d", s, ctr)), nil
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

// hasFixedLayout is jl_has_fixed_layout (jltypes.c:293-319): whether a type
// that may have free type variables has a layout all the same. Memory types
// are left to the caller, which has its own answer for them, and so is a
// concrete type (julia's first case): d is the body of a UnionAll.
func hasFixedLayout(d *core.DataType) bool {
	switch {
	case d.Name.Abstract, d.Name == core.TupleTypeName:
		return false
	case d.Name == core.NamedTupleTypeName:
		return !layoutUsesFreeTypevars(d.Parameters[0], nil) && !layoutUsesFreeTypevars(d.Parameters[1], nil)
	}
	ft, _ := d.FieldTypes()
	for _, f := range ft {
		if layoutUsesFreeTypevars(f, nil) {
			return false
		}
	}
	return true
}

// layoutUsesFreeTypevars is layout_uses_free_typevars (jltypes.c:52-106),
// env the variables bound around v. julia's mayinlinealloc is taken as
// immutability, which TypeName records; julia also excludes a type that
// may reference itself, which it does not.
func layoutUsesFreeTypevars(v any, env []*core.TypeVar) bool {
	for {
		if tv, ok := v.(*core.TypeVar); ok {
			for _, b := range env {
				if b == tv {
					return false
				}
			}
			return true
		}
		for {
			ua, ok := v.(*core.UnionAll)
			if !ok {
				break
			}
			env = append(env, ua.Var)
			v = ua.Body
		}
		switch x := v.(type) {
		case *core.DataType:
			switch {
			case x.IsConcrete, x.Name.Mutable:
				return false
			case x.Name == core.NamedTupleTypeName:
				return layoutUsesFreeTypevars(x.Parameters[0], env) || layoutUsesFreeTypevars(x.Parameters[1], env)
			case x.Name == core.TupleTypeName:
				return true // conservative, as julia
			}
			ft, _ := x.FieldTypes()
			for _, f := range ft {
				if layoutUsesFreeTypevars(f, env) {
					return true
				}
			}
			return false
		case *core.Union:
			if layoutUsesFreeTypevars(x.A, env) {
				return true
			}
			v = x.B
		default:
			// julia also follows a Vararg's T and N; a Vararg is only ever
			// a Tuple's last parameter, and a Tuple is answered above
			return false
		}
	}
}

// biSizeof is jl_f_sizeof (builtins.c:530-570) for what has a size
// without a struct layout: strings, symbols, simple vectors, primitive
// values and types, memories of primitive elements.
func biSizeof(vm *VM, args []any) (any, error) {
	if err := vm.nargs("sizeof", args, 1, 1); err != nil {
		return nil, err
	}
	// a Union or UnionAll: its isbits union's size, else its body's
	// (builtins.c:534-542; jl_uniontype_size, datatype.c:392-422)
	switch args[0].(type) {
	case *core.Union, *core.UnionAll:
		x := core.UnwrapUnionAll(args[0])
		if u, ok := x.(*core.Union); ok && unionIsbits(u) {
			size := 0
			for _, m := range unionMembers(u, nil) {
				s, _, _ := dataLayout(m.(*core.DataType))
				size = max(size, s)
			}
			return int64(size), nil
		}
		d, ok := x.(*core.DataType)
		if !ok {
			return nil, vm.errorf("Argument is an abstract type and does not have a definite size.")
		}
		if !d.Name.Abstract && !hasFixedLayout(d) {
			return nil, vm.errorf("Argument is an incomplete %s type and does not have a definite size.", d.Name.Name)
		}
		args = []any{d}
	}
	switch x := args[0].(type) {
	case *core.Vararg:
		return int64(16), nil // jl_vararg_t: T and N
	case string:
		return int64(len(x)), nil
	case expr.Symbol:
		return int64(len(x)), nil
	case SVec:
		return int64(1+len(x)) * 8, nil
	case *Memory:
		// the element's size, a pointer's when boxed, plus a selector byte
		// for an isbits union, times the length (builtins.c:566-568)
		size, _, boxed, union := memElem(x.Type.Parameters[1])
		if boxed {
			size = 8
		}
		if union {
			size++
		}
		return int64(size) * int64(len(x.Elems)), nil
	case *Struct, *Tuple:
		// any other value's size is its type's layout size (builtins.c:563-569)
		return biSizeof(vm, []any{vm.typeOf(x)})
	case *Module:
		// Module's layout is opaque, of size 0 (builtins.c:566): julia gives
		// 0 for a module, and refuses the type itself
		return int64(0), nil
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
	// any other value's size is its type's (builtins.c:563-569): a
	// function's singleton type has none
	if t, ok := vm.typeOf(args[0]).(*core.DataType); ok {
		if size, _, err := dataLayout(t); err == nil {
			return int64(size), nil
		}
	}
	return nil, fmt.Errorf("%w: sizeof of a %s", ErrUnsupported, showValue(vm.typeOf(args[0])))
}
