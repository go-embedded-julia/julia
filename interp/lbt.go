package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// libblastrampoline, the library LinearAlgebra calls BLAS and LAPACK
// through (stdlib LinearAlgebra, lbt.jl). Its configuration is the one
// julia 1.12.6 reports on darwin/arm64 after LinearAlgebra.__init__
// forwards it to OpenBLAS (BLAS.get_config(): one library,
// libopenblas64_.dylib, ILP64; build flags symbol_trimming, f2c_capable,
// complex_retstyle; complex return style normal, f2c plain, cblas
// unknown). The functions it forwards are the ones the VM implements in Go
// for the forwarded library: none yet (go-numerics, #33), so a BLAS call
// is refused as not ported and the configuration lists no exported symbol.
const lbtLib = "libblastrampoline"

// lbt_config_t build flags (lbt.jl:101-105)
const lbtBuildFlags = 0x10 | 0x02 | 0x08

// lbtState is what libblastrampoline holds: the libraries forwarded to,
// in order, and the thread count.
type lbtState struct {
	libs     []string
	nthreads int32
}

// cOut is a C value a library function returns through a pointer, which
// ccall builds with the types its signature names (foreignResult): a
// struct as its fields, an array as its elements.
type cOut struct{ v any }

// cStruct is a C struct's fields, in order.
type cStruct []any

// cArray is the elements a C pointer points at.
type cArray []any

// cValue is g as a julia value of type t: a cStruct as the struct t, a
// cArray as a pointer to a memory of t's element type, a string as a
// Cstring to its bytes, nil as a NULL pointer, anything else as is.
func (vm *VM) cValue(t any, g any) (any, error) {
	d, ok := t.(*core.DataType)
	if !ok {
		return nil, fmt.Errorf("%w: a C value of type %s", ErrUnsupported, showValue(t))
	}
	switch x := g.(type) {
	case cStruct:
		ft, _ := d.FieldTypes()
		if len(ft) != len(x) {
			return nil, fmt.Errorf("%w: %d fields for %s", ErrUnsupported, len(x), showValue(t))
		}
		fields := make([]any, len(x))
		for i, f := range x {
			v, err := vm.cValue(ft[i], f)
			if err != nil {
				return nil, err
			}
			fields[i] = v
		}
		return &Struct{Type: d, Fields: fields}, nil
	case cArray:
		if d.Name != typeNameOf(core.PtrType) {
			return nil, fmt.Errorf("%w: an array as %s", ErrUnsupported, showValue(t))
		}
		et := d.Parameters[0]
		elems := make([]any, len(x))
		for i, e := range x {
			v, err := vm.cValue(et, e)
			if err != nil {
				return nil, err
			}
			elems[i] = v
		}
		mem := &Memory{Type: must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), et, core.CPU)).(*core.DataType), Elems: elems}
		return &Pointer{Type: d, Target: mem}, nil
	case string:
		return biBitcast(vm, []any{d, vm.stringPtrOf(x)})
	case nil:
		if d.Name != typeNameOf(core.PtrType) {
			return nil, fmt.Errorf("%w: NULL as %s", ErrUnsupported, showValue(t))
		}
		return &Pointer{Type: d}, nil
	}
	return g, nil
}

// lbtConfig is the lbt_config_t (lbt.jl:95-100) of the forwarded
// libraries, NULL-terminated, each an lbt_library_info_t (lbt.jl:6-15).
func (vm *VM) lbtConfig() cArray {
	libs := cArray{}
	for _, name := range vm.lbt.libs {
		h := vm.dynLibs[name]
		// a bitfield of the forwarded exported symbols: none, one byte
		// (div(num_exported_symbols, 8) + 1, lbt.jl:86)
		libs = append(libs, cArray{cStruct{h.Target.(*dynLib).path, any(h), "64_", cArray{uint8(0)}, int32(64), int32(0), int32(0), int32(-1)}})
	}
	libs = append(libs, nil)
	return cArray{cStruct{libs, uint32(lbtBuildFlags), cArray{}, uint32(0)}}
}

// bootLBT registers libblastrampoline's configuration functions.
func (vm *VM) bootLBT() {
	vm.libShims[lbtLib] = map[expr.Symbol]shim{
		// lbt_forward(path, clear, verbose, suffix_hint): the library at
		// path, which it opens, becomes the one calls go to, after the
		// others unless clear; the count of functions forwarded, which are
		// the VM's (0 for a library that does not open)
		"lbt_forward": func(vm *VM, a []any) (any, error) {
			h, err := vm.loadDynamicLibrary([]any{a[0], uint32(0), int32(0)})
			l, ok := handleLib(h)
			if err != nil || !ok {
				return int32(0), nil
			}
			name := l.name
			if clear, _ := a[1].(int32); clear != 0 {
				vm.lbt.libs = nil
			}
			vm.lbt.libs = append(vm.lbt.libs, name)
			return int32(len(vm.libShims[name])), nil
		},
		"lbt_get_config": func(vm *VM, a []any) (any, error) { return cOut{vm.lbtConfig()}, nil },
		"lbt_get_num_threads": func(vm *VM, a []any) (any, error) {
			return vm.lbt.nthreads, nil
		},
		"lbt_set_num_threads": func(vm *VM, a []any) (any, error) {
			if n, ok := a[0].(int32); ok {
				vm.lbt.nthreads = n
			}
			return nil, nil
		},
	}
	vm.lbt.nthreads = 1
}

// ptrToArray1d is jl_ptr_to_array_1d(atype, data, nel, own) (array.c):
// a Vector over the elements a pointer into a memory reaches, sharing
// them; own is moot, the memory being the VM's.
func (vm *VM) ptrToArray1d(a []any) (any, error) {
	at, ok := a[0].(*core.DataType)
	p, isPtr := a[1].(*Pointer)
	n, isN := a[2].(uint64)
	if !ok || !isPtr || !isN || at.Name != typeNameOf(core.ArrayType) {
		return nil, fmt.Errorf("%w: jl_ptr_to_array_1d of %s", ErrUnsupported, showValue(a[1]))
	}
	m, isMem := p.Target.(*Memory)
	et := at.Parameters[0]
	if !isMem || m.Type.Parameters[1] != et {
		return nil, fmt.Errorf("%w: jl_ptr_to_array_1d of %s", ErrUnsupported, showValue(a[1]))
	}
	size, _, _, _ := memElem(et)
	k := p.Off / int64(size)
	if p.Off%int64(size) != 0 || k+int64(n) > int64(len(m.Elems)) || n > uint64(len(m.Elems)) {
		return nil, vm.errorf("jl_ptr_to_array_1d: invalid pointer")
	}
	v := vm.newVector(et, nil)
	v.Fields[0] = &MemoryRef{Type: v.Fields[0].(*MemoryRef).Type, Mem: m, Off: int(k)}
	v.Fields[1] = vm.newTuple([]any{int64(n)})
	return v, nil
}
