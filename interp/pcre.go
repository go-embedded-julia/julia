package interp

import (
	"fmt"
	"unsafe"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-regexp/pcre2"
)

// libpcre2-8: the PCRE2 functions Base's PCRE module calls (pcre.jl),
// through go-regexp/pcre2, a Go port of PCRE2 10.44, the version julia
// 1.12.6 pins (deps/pcre.version). A compiled pattern, match data, a match
// context and a JIT stack are Ptr{Cvoid} handles holding the Go object;
// freeing one drops its address region (addrspace.go). The ovector is read
// through a Ptr{Csize_t} into the match data's own, the name table through
// a Ptr{UInt8} into its bytes.

const pcreLib = "libpcre2-8"

// ovecView is what pcre2_get_ovector_pointer's Ptr{Csize_t} points at.
type ovecView struct{ md *pcre2.MatchData }

func (vm *VM) bootPCRE() {
	voidPtr := vm.ptrType(core.NothingType)
	handle := func(h any) *Pointer { return &Pointer{Type: voidPtr, Target: h} }
	free := func(a []any) (any, error) {
		if p, ok := a[0].(*Pointer); ok && p.Target != nil {
			vm.dropRegion(addrKey(p.Target))
		}
		return nil, nil
	}
	if vm.libShims == nil {
		vm.libShims = map[string]map[expr.Symbol]shim{}
	}
	vm.libShims[pcreLib] = map[expr.Symbol]shim{
		"pcre2_jit_stack_create_8": func(vm *VM, a []any) (any, error) {
			start, _ := a[0].(uint64)
			max, _ := a[1].(uint64)
			return handle(pcre2.JITStackCreate(start, max)), nil
		},
		"pcre2_match_context_create_8": func(vm *VM, a []any) (any, error) {
			return handle(pcre2.NewMatchContext()), nil
		},
		"pcre2_jit_stack_assign_8": func(vm *VM, a []any) (any, error) {
			mc, err := pcreHandle[*pcre2.MatchContext](vm, a[0])
			if err != nil {
				return nil, err
			}
			js, err := pcreHandle[*pcre2.JITStack](vm, a[2])
			if err != nil {
				return nil, err
			}
			mc.JITStackAssign(js)
			return nil, nil
		},
		"pcre2_compile_8": func(vm *VM, a []any) (any, error) { return vm.pcreCompile(a) },
		"pcre2_jit_compile_8": func(vm *VM, a []any) (any, error) {
			code, err := pcreHandle[*pcre2.Code](vm, a[0])
			if err != nil {
				return nil, err
			}
			opts, _ := a[1].(uint32)
			return int32(code.JITCompile(opts)), nil
		},
		"pcre2_pattern_info_8": func(vm *VM, a []any) (any, error) { return vm.pcrePatternInfo(a) },
		"pcre2_match_data_create_from_pattern_8": func(vm *VM, a []any) (any, error) {
			code, err := pcreHandle[*pcre2.Code](vm, a[0])
			if err != nil {
				return nil, err
			}
			return handle(pcre2.NewMatchData(code)), nil
		},
		"pcre2_get_ovector_count_8": func(vm *VM, a []any) (any, error) {
			md, err := pcreHandle[*pcre2.MatchData](vm, a[0])
			if err != nil {
				return nil, err
			}
			return md.OvectorCount(), nil
		},
		"pcre2_get_ovector_pointer_8": func(vm *VM, a []any) (any, error) {
			md, err := pcreHandle[*pcre2.MatchData](vm, a[0])
			if err != nil {
				return nil, err
			}
			return &Pointer{Type: vm.ptrType(core.UInt64Type), Target: ovecView{md}}, nil
		},
		"pcre2_match_8": func(vm *VM, a []any) (any, error) { return vm.pcreMatch(a) },
		"pcre2_get_error_message_8": func(vm *VM, a []any) (any, error) {
			n, _ := a[0].(int32)
			size, _ := a[2].(uint64)
			if size > uint64(vm.MaxMemoryElems) {
				size = uint64(vm.MaxMemoryElems)
			}
			buf := make([]byte, size)
			rc := pcre2.GetErrorMessageBuf(int(n), buf)
			if rc < 0 && rc != pcre2.PCRE2_ERROR_NOMEMORY {
				return int32(rc), nil
			}
			// the message and its NUL, as far as the buffer holds them
			m := 0
			for m < len(buf) && buf[m] != 0 {
				m++
			}
			if err := vm.writeBytes(a[1], buf[:min(m+1, len(buf))]); err != nil {
				return nil, err
			}
			return int32(rc), nil
		},
		"pcre2_substring_number_from_name_8": func(vm *VM, a []any) (any, error) {
			code, err := pcreHandle[*pcre2.Code](vm, a[0])
			if err != nil {
				return nil, err
			}
			name, ok := vm.cstringArg(a[1])
			if !ok {
				return nil, fmt.Errorf("%w: pcre2_substring_number_from_name of %s", ErrUnsupported, showValue(a[1]))
			}
			return int32(pcre2.SubstringNumberFromName(code, name)), nil
		},
		"pcre2_substring_length_bynumber_8": func(vm *VM, a []any) (any, error) {
			md, err := pcreHandle[*pcre2.MatchData](vm, a[0])
			if err != nil {
				return nil, err
			}
			n, _ := a[1].(int32)
			length, rc := pcre2.SubstringLengthByNumber(md, uint32(n))
			if rc == 0 {
				if err := vm.storeAs("pcre2_substring_length_bynumber", a[2], length); err != nil {
					return nil, err
				}
			}
			return int32(rc), nil
		},
		"pcre2_substring_copy_bynumber_8": func(vm *VM, a []any) (any, error) {
			md, err := pcreHandle[*pcre2.MatchData](vm, a[0])
			if err != nil {
				return nil, err
			}
			n, _ := a[1].(uint32)
			sp, ok := a[3].(*Pointer)
			if !ok {
				return nil, vm.typeError("ccall", "", vm.ptrType(core.UInt64Type), a[3])
			}
			sv, err := vm.ptrLoad("pcre2_substring_copy_bynumber", sp, 1)
			if err != nil {
				return nil, err
			}
			size, _ := sv.(uint64)
			if size > uint64(vm.MaxMemoryElems) {
				size = uint64(vm.MaxMemoryElems)
			}
			buf := make([]byte, size)
			length, rc := pcre2.SubstringCopyByNumber(md, n, buf)
			if rc != 0 {
				return int32(rc), nil
			}
			// the substring and the NUL C writes after it
			if err := vm.writeBytes(a[2], buf[:min(length+1, size)]); err != nil {
				return nil, err
			}
			return int32(rc), vm.storeAs("pcre2_substring_copy_bynumber", sp, length)
		},
		"pcre2_match_data_free_8":    func(vm *VM, a []any) (any, error) { return free(a) },
		"pcre2_code_free_8":          func(vm *VM, a []any) (any, error) { return free(a) },
		"pcre2_jit_stack_free_8":     func(vm *VM, a []any) (any, error) { return free(a) },
		"pcre2_match_context_free_8": func(vm *VM, a []any) (any, error) { return free(a) },
	}
}

// pcreHandle is the Go object of type T a Ptr{Cvoid} handle holds.
func pcreHandle[T any](vm *VM, v any) (T, error) {
	var zero T
	if p, ok := v.(*Pointer); ok {
		if h, ok := p.Target.(T); ok {
			return h, nil
		}
	}
	return zero, fmt.Errorf("%w: a PCRE2 call on %s", ErrUnsupported, showValue(v))
}

// pcreCompile is pcre2_compile(pattern, length, options, &errorcode,
// &erroroffset, ccontext): a handle, or C_NULL with the error stored.
func (vm *VM) pcreCompile(a []any) (any, error) {
	pat, ok := stringBytes(a[0], a[1])
	if !ok {
		return nil, fmt.Errorf("%w: pcre2_compile of a pattern at %s", ErrUnsupported, showValue(a[0]))
	}
	opts, _ := a[2].(uint32)
	code, errcode, erroff := pcre2.Compile([]byte(pat), opts, nil)
	// the error code and offset are stored either way: ERROR_NOERROR (100)
	// and 0 on success, as pcre2_compile does (pcre2_compile.c: *errorptr =
	// ERROR_NOERROR at the start)
	stored := int32(errcode)
	if code != nil {
		stored = 100 // ERROR_NOERROR
	}
	if err := vm.storeAs("pcre2_compile", a[3], stored); err != nil {
		return nil, err
	}
	if err := vm.storeAs("pcre2_compile", a[4], erroff); err != nil {
		return nil, err
	}
	if code == nil {
		return &Pointer{Type: vm.ptrType(core.NothingType)}, nil
	}
	return &Pointer{Type: vm.ptrType(core.NothingType), Target: code}, nil
}

// pcreMatch is pcre2_match(code, subject, length, startoffset, options,
// match_data, mcontext).
func (vm *VM) pcreMatch(a []any) (any, error) {
	code, err := pcreHandle[*pcre2.Code](vm, a[0])
	if err != nil {
		return nil, err
	}
	subj, ok := stringBytes(a[1], a[2])
	if !ok {
		return nil, fmt.Errorf("%w: pcre2_match of a subject at %s", ErrUnsupported, showValue(a[1]))
	}
	md, err := pcreHandle[*pcre2.MatchData](vm, a[5])
	if err != nil {
		return nil, err
	}
	var mc *pcre2.MatchContext
	if p, ok := a[6].(*Pointer); ok && p.Target != nil {
		if mc, err = pcreHandle[*pcre2.MatchContext](vm, a[6]); err != nil {
			return nil, err
		}
	}
	start, _ := a[3].(uint64)
	opts, _ := a[4].(uint32)
	// the String's own bytes, not a copy: PCRE2 only reads the subject, and
	// eachmatch calls this once per match over the same String
	b := unsafe.Slice(unsafe.StringData(subj), len(subj))
	return int32(pcre2.Match(code, b, start, opts, md, mc)), nil
}

// pcrePatternInfo is pcre2_pattern_info(code, what, where): the item
// stored through where, a Ref of the item's C type (pcre.jl's info), the
// name table as a Ptr{UInt8} to its bytes.
func (vm *VM) pcrePatternInfo(a []any) (any, error) {
	code, err := pcreHandle[*pcre2.Code](vm, a[0])
	if err != nil {
		return nil, err
	}
	what, _ := a[1].(uint32)
	if what == pcre2.PCRE2_INFO_NAMETABLE {
		table, _, _ := code.NameTable()
		return int32(0), vm.storeAs("pcre2_pattern_info", a[2], vm.stringPtrOf(string(table)))
	}
	v, rc := code.PatternInfo(what)
	if rc != 0 {
		return int32(rc), nil
	}
	return int32(0), vm.storeAs("pcre2_pattern_info", a[2], v)
}

// storeAs is storeThrough of v converted to the C type the Ref argument
// was made of (pcre.jl's info passes a RefValue{T} of the item's type):
// an integer result is truncated or widened to it.
func (vm *VM) storeAs(fname string, ref any, v any) error {
	p, ok := ref.(*Pointer)
	var st *Struct
	if ok {
		st, ok = p.Target.(*Struct)
	}
	if !ok || len(st.Fields) == 0 {
		return vm.storeThrough(fname, ref, nil, v) // refused, or C_NULL
	}
	ft, _ := st.Type.FieldTypes()
	d, _ := ft[0].(*core.DataType)
	if _, isPtr := v.(*Pointer); !isPtr {
		bits, _, _ := primBits(v)
		x, ok := bitsValue(d, bits)
		if !ok {
			return fmt.Errorf("%w: %s's result as %s", ErrUnsupported, fname, showValue(ft[0]))
		}
		v = x
	}
	return vm.storeThrough(fname, ref, d, v)
}

// writeBytes writes b where a Ptr{UInt8} points: into a memory of bytes, or
// a String allocString made.
func (vm *VM) writeBytes(dst any, b []byte) error {
	p, ok := dst.(*Pointer)
	if !ok {
		return vm.typeError("ccall", "", vm.ptrType(core.UInt8Type), dst)
	}
	if s, isStr := p.Target.(string); isStr {
		w, ok := vm.writableBytes(s)
		if ok && p.Off >= 0 && p.Off+int64(len(b)) <= int64(len(w)) {
			copy(w[p.Off:], b)
			return nil
		}
		return vm.errorf("ccall: invalid pointer")
	}
	for i, c := range b {
		q := &Pointer{Type: vm.ptrType(core.UInt8Type), Target: p.Target, Off: p.Off + int64(i)}
		if err := vm.ptrStore("ccall", q, c, 1); err != nil {
			return err
		}
	}
	return nil
}

// cstringArg is the string a Cstring argument points at: a Cstring is the
// address of its bytes (strings/cstring.jl:31), in a known region.
func (vm *VM) cstringArg(v any) (string, bool) {
	if p, ok := v.(*Pointer); ok {
		s, err := vm.cstrToString([]any{p})
		str, _ := s.(string)
		return str, err == nil
	}
	bits, _, ok := primBits(v)
	if !ok {
		return "", false
	}
	target, off, ok := vm.targetAt(bits.lo)
	if !ok {
		return "", false
	}
	s, err := vm.cstrToString([]any{&Pointer{Type: vm.ptrType(core.UInt8Type), Target: target, Off: off}})
	str, _ := s.(string)
	return str, err == nil
}
