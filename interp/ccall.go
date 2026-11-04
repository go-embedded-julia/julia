package interp

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/lower"
)

// hrtimeOrigin is jl_hrtime's origin.
var hrtimeOrigin = time.Now()

// foreigncall is where julia's interpreter gives up ("`ccall` requires the
// compiler", interpreter.c:356-358): julia never meets it because Base is
// compiled. Here Core and Base run in the interpreter, so every ccall they
// make must reach Go: a shim per C function, keyed by the literal name the
// call names (ast.md: args[1] of :foreigncall), with that function's
// semantics. A name without a shim is ErrUnsupported when the call runs,
// never earlier, so that methods holding calls the program does not make
// still load.

// shim is the Go side of one C function a ccall names.
type shim func(vm *VM, args []any) (any, error)

func (vm *VM) bootShims() {
	vm.shims = map[expr.Symbol]shim{
		// jl_toplevel_eval_in (toplevel.c): evaluate ex in module m
		"jl_toplevel_eval_in": func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Module)
			if !ok {
				return nil, vm.typeError("eval", "", core.ModuleType, a[0])
			}
			return vm.sessionFor(m).evalForm(a[1])
		},
		// jl_load_ (toplevel.c:1194-1201), boot.jl's include
		"jl_load_": func(vm *VM, a []any) (any, error) { return vm.jlLoad(a) },
		// jl_set_istopmod (module.c:743-749): a top module, and for the
		// primary one the module every other falls back to; the primary
		// Base becomes the VM's Base
		"jl_set_istopmod": func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Module)
			if !ok {
				return nil, vm.typeError("ccall", "", core.ModuleType, a[0])
			}
			m.istopmod = true
			if primary, _ := a[1].(bool); primary {
				vm.topModule = m
			}
			return nil, nil
		},
		// jl_set_module_uuid (module.c:2041), m's package UUID as two
		// UInt64s, high half first
		"jl_set_module_uuid": func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Module)
			if !ok {
				return nil, vm.typeError("ccall", "", core.ModuleType, a[0])
			}
			t, ok := a[1].(*Tuple)
			if !ok || len(t.Elems) != 2 {
				return nil, fmt.Errorf("%w: jl_set_module_uuid of %s", ErrUnsupported, showValue(a[1]))
			}
			hi, ok1 := t.Elems[0].(uint64)
			lo, ok2 := t.Elems[1].(uint64)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("%w: jl_set_module_uuid of %s", ErrUnsupported, showValue(a[1]))
			}
			m.uuid = [2]uint64{hi, lo}
			return nil, nil
		},
		// jl_field_index (datatype.c:1781-1806), fieldindex: the 0-based
		// index of a field by name, -1 or a FieldError when there is none
		"jl_field_index": func(vm *VM, a []any) (any, error) {
			t, ok := a[0].(*core.DataType)
			if !ok {
				return nil, vm.typeError("ccall", "", core.DataTypeType, a[0])
			}
			name, ok := a[1].(expr.Symbol)
			if !ok {
				return nil, vm.typeError("ccall", "", core.SymbolType, a[1])
			}
			for i, n := range t.FieldNames() {
				if n == name {
					return int32(i), nil
				}
			}
			if a[2] != int32(0) {
				return nil, vm.noFieldError(t, name)
			}
			return int32(-1), nil
		},
		// jl_base_relative_to (toplevel.c:356-366): the top module above m
		"jl_base_relative_to": func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Module)
			if !ok {
				return nil, vm.typeError("ccall", "", core.ModuleType, a[0])
			}
			return vm.baseRelativeTo(m), nil
		},
		// jl_prepend_cwd (init.c): a path made absolute against the working
		// directory; include's paths are VM.Sources', which has none
		"jl_prepend_cwd": func(vm *VM, a []any) (any, error) { return a[0], nil },
		// jl_uv_flush (jl_uv.c): output is written through as it comes
		"jl_uv_flush": func(vm *VM, a []any) (any, error) { return nil, nil },
		// output to VM.Stdout / VM.Stderr and pointers to String bytes
		// (vptr.go)
		"jl_string_ptr":  func(vm *VM, a []any) (any, error) { return vm.stringPtr(a) },
		"jl_uv_puts":     func(vm *VM, a []any) (any, error) { return vm.uvPuts(a) },
		"jl_uv_putb":     func(vm *VM, a []any) (any, error) { return vm.uvPutb(a) },
		"jl_uv_putc":     func(vm *VM, a []any) (any, error) { return vm.uvPutc(a) },
		"jl_static_show": func(vm *VM, a []any) (any, error) { return vm.staticShow(a) },
		// jl_module_globalref (module.c): boot.jl's GlobalRef(m, s)
		"jl_module_globalref": func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Module)
			s, ok2 := a[1].(expr.Symbol)
			if !ok || !ok2 {
				return nil, vm.typeError("GlobalRef", "", core.ModuleType, a[0])
			}
			return expr.GlobalRef{Mod: m.Name, Name: s}, nil
		},
		// jl_sizeof_jl_options (jloptions.c): the size of the C struct that
		// Base.JLOptions mirrors; there is no C struct here, so the size of
		// that mirror, laid out as C lays its own out
		"jl_sizeof_jl_options": func(vm *VM, a []any) (any, error) {
			var t *core.DataType
			if b := vm.Base.own("JLOptions"); b != nil {
				t, _ = b.Value.(*core.DataType)
			}
			if t == nil {
				return nil, vm.errorf("jl_sizeof_jl_options: Base.JLOptions is not defined")
			}
			size, _, err := dataLayout(t)
			return int64(size), err
		},
		// jl_get_LLVM_VERSION (intrinsics.cpp:120-126): 10000 major + 100
		// minor + patch of the LLVM julia 1.12.6 pins (deps/llvm.version:
		// 18.1.7), which Base reads as checked.jl's llvm_version; there is no
		// LLVM here, so the reference's answer
		"jl_get_LLVM_VERSION": func(vm *VM, a []any) (any, error) { return uint32(180107), nil },
		// jl_types_equal (subtype.c:2319), operators.jl's ==(::Type, ::Type),
		// as a Cint
		"jl_types_equal": func(vm *VM, a []any) (any, error) {
			if core.TypesEqual(a[0], a[1]) {
				return int32(1), nil
			}
			return int32(0), nil
		},
		// type intersection (subtype.c:4430-4677)
		"jl_type_intersection": func(vm *VM, a []any) (any, error) { return core.TypeIntersection(a[0], a[1]) },
		"jl_intersect_types":   func(vm *VM, a []any) (any, error) { return core.IntersectTypes(a[0], a[1]) },
		"jl_type_intersection_with_env": func(vm *VM, a []any) (any, error) {
			ti, env, _, err := core.TypeIntersectionEnv(a[0], a[1])
			if err != nil {
				return nil, err
			}
			return SVec{ti, SVec(env)}, nil
		},
		"jl_has_empty_intersection": func(vm *VM, a []any) (any, error) {
			empty, err := core.HasEmptyIntersection(a[0], a[1])
			if err != nil || !empty {
				return int32(0), err
			}
			return int32(1), nil
		},
		// jl_symbol_n (symbol.c:113-118), boot.jl's Symbol(::String): the
		// bytes a pointer into a String points at
		"jl_symbol_n": func(vm *VM, a []any) (any, error) { return vm.symbolN(a) },
		// jl_alloc_string (array.c:290-305) and the memmove that fills it
		// (strbuf.go)
		"jl_alloc_string":   func(vm *VM, a []any) (any, error) { return vm.allocString(a) },
		"jl_cstr_to_string": func(vm *VM, a []any) (any, error) { return vm.cstrToString(a) },
		"memmove":           func(vm *VM, a []any) (any, error) { return vm.memmove(a) },
		"memset":            func(vm *VM, a []any) (any, error) { return vm.memset(a) },
		// jl_stored_inline (datatype.c:430-434), array.jl's allocatedinline
		"jl_stored_inline": func(vm *VM, a []any) (any, error) {
			n, err := storedInline(a[0])
			return int32(n), err
		},
		// jl_genericmemory_copyto (genericmemory.c:225-300), unsafe_copyto!
		// of memories of boxed elements (mempointer.go)
		"jl_genericmemory_copyto": func(vm *VM, a []any) (any, error) { return nil, vm.memoryCopyto(a) },
		// c.jl's platform queries (sys.c:66, 568-571, 762-770), answered as
		// the reference julia 1.12.6 answers on darwin/arm64, which the VM
		// models (as fenvConsts and the division intrinsics do): Apple's
		// arm64 char is signed, mode_t is 2 bytes
		"jl_is_char_signed": func(vm *VM, a []any) (any, error) { return true, nil },
		"jl_get_UNAME":      func(vm *VM, a []any) (any, error) { return expr.Symbol("Darwin"), nil },
		"jl_get_ARCH":       func(vm *VM, a []any) (any, error) { return expr.Symbol("aarch64"), nil },
		"jl_sizeof_mode_t":  func(vm *VM, a []any) (any, error) { return int32(2), nil },
		// jl_ver_* (jlapi.c:610-643) as Cints: the version the VM ports
		// (VERSION: 1.12.6, a release); ordering.jl reads the major
		"jl_ver_major":      func(vm *VM, a []any) (any, error) { return int32(1), nil },
		"jl_ver_minor":      func(vm *VM, a []any) (any, error) { return int32(12), nil },
		"jl_ver_patch":      func(vm *VM, a []any) (any, error) { return int32(6), nil },
		"jl_ver_is_release": func(vm *VM, a []any) (any, error) { return int32(1), nil },
		// jl_generating_output (precompile.c:19-22): whether an output
		// file is set in jl_options; the VM's are all NULL (defaultOptions),
		// it writes no image
		"jl_generating_output": func(vm *VM, a []any) (any, error) { return int32(0), nil },
		// jl_get_fenv_consts (jl_uv.c), rounding.jl's first call
		"jl_get_fenv_consts": func(vm *VM, a []any) (any, error) { return vm.fenvConsts(a) },
		// jl_module_name (module.c)
		"jl_module_name": func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Module)
			if !ok {
				return nil, vm.typeError("ccall", "", core.ModuleType, a[0])
			}
			return m.Name, nil
		},
		// jl_module_parent (module.c)
		"jl_module_parent": func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Module)
			if !ok {
				return nil, vm.typeError("ccall", "", core.ModuleType, a[0])
			}
			if m.Parent == nil {
				return m, nil // a root module is its own parent
			}
			return m.Parent, nil
		},
		// jl_type_unionall (jltypes.c:907-948); boot.jl's UnionAll asserts
		// v::TypeVar before the call
		"jl_type_unionall": func(vm *VM, a []any) (any, error) {
			v, ok := a[0].(*core.TypeVar)
			if !ok {
				return nil, vm.typeError("typeassert", "", core.TypeVarType, a[0])
			}
			return vm.typeUnionAll(v, a[1])
		},
		// jl_has_free_typevars (jltypes.c)
		"jl_has_free_typevars": func(vm *VM, a []any) (any, error) { return core.HasFreeTypeVars(a[0]), nil },
		// jl_get_tls_world_age (task.c): the task's world, a UInt
		"jl_get_tls_world_age": func(vm *VM, a []any) (any, error) { return vm.taskWorld, nil },
		// jl_prepend_string (toplevel.c:1252-1264): prefix and str as C
		// strings, each to its first NUL, in a 1024-byte buffer
		"jl_prepend_string": func(vm *VM, a []any) (any, error) {
			prefix, ok1 := a[0].(string)
			str, ok2 := a[1].(string)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("%w: jl_prepend_string of %s", ErrUnsupported, showValue(vm.newTuple(a)))
			}
			prefix, _, _ = strings.Cut(prefix, "\x00")
			str, _, _ = strings.Cut(str, "\x00")
			if len(prefix)+len(str) >= 1024 {
				return nil, vm.errorf("use a bigger buffer for jl_fullpath")
			}
			return prefix + str, nil
		},
		// jl_gensym and jl_tagged_gensym (symbol.c:129-163), Base.gensym
		"jl_gensym":        func(vm *VM, a []any) (any, error) { return vm.gensym(nil) },
		"jl_tagged_gensym": func(vm *VM, a []any) (any, error) { return vm.gensym(a) },
		// jl_symbol_name (symbol.c): a pointer to the name's bytes, read only
		"jl_symbol_name": func(vm *VM, a []any) (any, error) {
			s, ok := a[0].(expr.Symbol)
			if !ok {
				return nil, vm.typeError("ccall", "", core.SymbolType, a[0])
			}
			return vm.stringPtrOf(string(s)), nil
		},
		// jl_hrtime (sys.c, uv_hrtime): a monotonic clock in nanoseconds
		// from an arbitrary origin, here the process's start
		"jl_hrtime": func(vm *VM, a []any) (any, error) { return uint64(time.Since(hrtimeOrigin)), nil },
		// jl_typeinf_timing_begin and _end (gf.c:4944-4966): the task's
		// reentrancy flag; compile time is not measured (it is off unless
		// enabled), so the clock is only read
		"jl_typeinf_timing_begin": func(vm *VM, a []any) (any, error) {
			if vm.typeinfTiming {
				return uint64(0), nil
			}
			vm.typeinfTiming = true
			return uint64(time.Since(hrtimeOrigin)) | 1, nil
		},
		"jl_typeinf_timing_end": func(vm *VM, a []any) (any, error) {
			if a[0] != uint64(0) {
				vm.typeinfTiming = false
			}
			return nil, nil
		},
		// jl_set_typeinf_func (gf.c:957-961): the inference entry point and
		// the world it runs in, the task's now
		"jl_set_typeinf_func": func(vm *VM, a []any) (any, error) {
			vm.typeinfFunc, vm.typeinfWorld = a[0], vm.taskWorld
			return nil, nil
		},
		// jl_call_in_typeinf_world (gf.c:552-563): args[1](args[2:]...) in
		// the inference world, the args a Ptr{Any} into a Vector's memory
		"jl_call_in_typeinf_world": func(vm *VM, a []any) (any, error) {
			args, err := vm.ptrArgs("jl_call_in_typeinf_world", a[0], a[1])
			if err != nil {
				return nil, err
			}
			last := vm.taskWorld
			vm.taskWorld = vm.typeinfWorld
			defer func() { vm.taskWorld = last }()
			return vm.apply(args[0], args[1:])
		},
		// method reflection (reflect.go)
		"jl_matching_methods":          func(vm *VM, a []any) (any, error) { return vm.matchingMethods(a) },
		"jl_specializations_get_linfo": func(vm *VM, a []any) (any, error) { return vm.specializationsGetLinfo(a) },
		"jl_specializations_lookup":    func(vm *VM, a []any) (any, error) { return vm.specializationsLookup(a) },
		"jl_gf_invoke_lookup_worlds":   func(vm *VM, a []any) (any, error) { return vm.gfInvokeLookupWorlds(a) },
		"jl_gf_invoke_lookup":          func(vm *VM, a []any) (any, error) { return vm.gfInvokeLookupMethod(a) },
		"jl_argument_datatype":         argumentDatatype,
		"jl_normalize_to_compilable_sig": func(vm *VM, a []any) (any, error) {
			return vm.normalizeToCompilableSig(a)
		},
		"jl_isa_compileable_sig": func(vm *VM, a []any) (any, error) {
			m, ok1 := a[2].(*Struct)
			sp, ok2 := a[1].(SVec)
			if !ok1 || m.Type != vm.tMethod || !ok2 {
				return nil, fmt.Errorf("%w: jl_isa_compileable_sig of %s", ErrUnsupported, vm.argTypes(a))
			}
			return boolBit32(vm.isaCompileableSig(a[0], sp, m)), nil
		},
		"jl_method_table_disable": func(vm *VM, a []any) (any, error) { return vm.methodTableDisable(a) },
		// jl_get_fieldtypes (julia.h:1498): a DataType's field types, which
		// core instantiates as it reads them (jl_compute_fieldtypes,
		// jltypes.c:2847-2870); a struct before _typebody! has none
		"jl_get_fieldtypes": func(vm *VM, a []any) (any, error) {
			d, ok := a[0].(*core.DataType)
			if !ok {
				return nil, vm.typeError("ccall", "", core.DataTypeType, a[0])
			}
			if ft, ok := d.FieldTypes(); ok {
				return SVec(ft), nil
			}
			if d.Name.Abstract || d.Name.Bits > 0 {
				return SVec{}, nil
			}
			return nil, vm.errorf("cannot determine field types of incomplete type %s", d.Name.Name)
		},
		// jl_is_assertsbuild (jlapi.c:554-560): the official build is
		// built without assertions (JL_NDEBUG)
		"jl_is_assertsbuild": func(vm *VM, a []any) (any, error) { return int8(0), nil },
		"jl_copy_code_info":  func(vm *VM, a []any) (any, error) { return vm.copyCodeInfo(a) },
		// a DebugInfo's code locations (ircode.c:1425-1566), lowering's
		// encoding
		"jl_compress_codelocs": func(vm *VM, a []any) (any, error) {
			first, ok1 := a[0].(int32)
			n, ok2 := a[2].(int64)
			_, _, elems, ok3 := arrayOf(a[1])
			if !ok1 || !ok2 || !ok3 {
				return nil, fmt.Errorf("%w: jl_compress_codelocs of %s", ErrUnsupported, showValue(vm.newTuple(a)))
			}
			locs := make([]int32, len(elems))
			for i, e := range elems {
				v, ok := e.(int32)
				if !ok {
					return nil, vm.typeError("ccall", "", core.Int32Type, e)
				}
				locs[i] = v
			}
			if len(locs) != 0 && n*3 != int64(len(locs)) {
				return nil, vm.errorf("jl_compress_codelocs: %d statements for %d locations", n, len(locs))
			}
			return lower.CompressCodelocs(first, locs), nil
		},
		"jl_uncompress1_codeloc": func(vm *VM, a []any) (any, error) {
			cl, ok1 := a[0].(string)
			pc, ok2 := a[1].(int64)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("%w: jl_uncompress1_codeloc of %s", ErrUnsupported, showValue(vm.newTuple(a)))
			}
			line, to, at := lower.UncompressCodeloc(cl, int(min(max(pc, -1), 1<<31)))
			return vm.newTuple([]any{line, to, at}), nil
		},
		// compressed IR (ircode.go)
		"jl_compress_ir": func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Struct)
			if !ok || m.Type != vm.tMethod {
				return nil, vm.typeError("ccall", "", vm.tMethod, a[0])
			}
			return vm.compressIR(m, a[1])
		},
		"jl_uncompress_ir": func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Struct)
			if !ok || m.Type != vm.tMethod {
				return nil, vm.typeError("ccall", "", vm.tMethod, a[0])
			}
			return vm.uncompressIR(m, a[1], a[2])
		},
		"jl_uncompress_argnames": func(vm *VM, a []any) (any, error) { return vm.uncompressArgnames(a[0]) },
		// a source's flags and slots (ircode.c:1213-1303)
		"jl_ir_flag_inlining":            irFlag("jl_ir_flag_inlining", "inlining"),
		"jl_ir_flag_has_fcall":           irFlag("jl_ir_flag_has_fcall", "has_fcall"),
		"jl_ir_flag_has_image_globalref": irFlag("jl_ir_flag_has_image_globalref", "has_image_globalref"),
		"jl_ir_inlining_cost":            func(vm *VM, a []any) (any, error) { return vm.irInliningCost(a) },
		"jl_ir_nslots":                   func(vm *VM, a []any) (any, error) { return vm.irNslots(a) },
		"jl_ir_slotflag":                 func(vm *VM, a []any) (any, error) { return vm.irSlotflag(a) },
		"jl_scan_method_source_now":      func(vm *VM, a []any) (any, error) { return vm.scanMethodSourceNow(a) },
		// the code cache (codeinst.go)
		"jl_new_codeinst":          func(vm *VM, a []any) (any, error) { return vm.newCodeinst(a) },
		"jl_mi_cache_insert":       func(vm *VM, a []any) (any, error) { return vm.miCacheInsert(a) },
		"jl_rettype_inferred":      func(vm *VM, a []any) (any, error) { return vm.rettypeInferred(a) },
		"jl_engine_reserve":        func(vm *VM, a []any) (any, error) { return vm.engineReserve(a) },
		"jl_engine_fulfill":        func(vm *VM, a []any) (any, error) { return vm.engineFulfill(a) },
		"jl_promote_ci_to_current": func(vm *VM, a []any) (any, error) { return vm.promoteCIToCurrent(a) },
		"jl_invoke_api":            func(vm *VM, a []any) (any, error) { return vm.invokeAPI(a) },
		"jl_maybe_add_binding_backedge": func(vm *VM, a []any) (any, error) {
			return vm.maybeAddBindingBackedge(a)
		},
		"jl_method_instance_add_backedge": func(vm *VM, a []any) (any, error) {
			return vm.methodInstanceAddBackedge(a)
		},
		"jl_promote_mi_to_current": func(vm *VM, a []any) (any, error) { return vm.promoteMIToCurrent(a) },
		"jl_fill_codeinst":         func(vm *VM, a []any) (any, error) { return vm.fillCodeinst(a) },
		"jl_update_codeinst":       func(vm *VM, a []any) (any, error) { return vm.updateCodeinst(a) },
		// jl_push_newly_inferred (staticdata_utils.c:117-131): a no-op
		// unless the image being written collects them (jl_set_newly_inferred,
		// which is not ported)
		"jl_push_newly_inferred": func(vm *VM, a []any) (any, error) { return nil, nil },
		// jl_get_world_counter (gf.c:32-38): the newest world, a UInt;
		// there are no pure callbacks here
		"jl_get_world_counter": func(vm *VM, a []any) (any, error) { return vm.World(), nil },
	}
	vm.bootEqtableShims()
	vm.bootModuleOptShims()
	vm.bootBindingShims()
}

// sessionFor is the session that evaluates code in module m: its lowering
// state, made on first use. Its top module is asked again for every form
// (baseRelativeTo).
func (vm *VM) sessionFor(m *Module) *Session {
	if s := vm.sessions[m]; s != nil {
		return s
	}
	s := &Session{VM: vm, Mod: m, low: lower.NewStateIn(exprModule(m), vm.baseRelativeTo(m).Name)}
	vm.sessions[m] = s
	return s
}

// baseRelativeTo is jl_base_relative_to (toplevel.c:356-366): the nearest
// enclosing top module, else the primary one (Core until Base claims it).
func (vm *VM) baseRelativeTo(m *Module) *Module {
	for ; m != nil; m = m.Parent {
		if m.istopmod {
			return m
		}
	}
	return vm.topModule
}

// replaceBase makes m, the Base that Base_compiler.jl defines, the VM's
// Base in place of the empty one New made: the module Main and the
// standard imports name.
func (vm *VM) replaceBase(m *Module) {
	old := vm.Base
	vm.Base = m
	vm.modules["Base"] = m
	vm.Main.mu.Lock()
	for i, u := range vm.Main.usings {
		if u == old {
			vm.Main.usings[i] = m
		}
	}
	vm.Main.mu.Unlock()
}

// oForeign is a prepared :foreigncall: the callee's literal name and, for
// a library-qualified one, its library, the return and argument types, and
// the arguments (julia's GC roots after the declared arguments are left
// out).
type oForeign struct {
	name   expr.Symbol
	lib    op // nil when the callee names no library
	ret    op
	atypes op
	args   []op
}

func (c *code) prepForeign(x *expr.Expr) op {
	// (foreigncall name rettype argtypes nreq cc args… roots…)
	if len(x.Args) < 5 {
		return oRaise{"Invalid IR: malformed :foreigncall"}
	}
	f := oForeign{ret: c.prepValue(x.Args[1]), atypes: c.prepValue(x.Args[2]), args: c.prepArgs(x.Args[5:])}
	// a literal name, or (name, lib) as lowering leaves ccall((:f, LIB), …):
	// a call of Core.tuple on the quoted name and the library's value
	callee := x.Args[0]
	if t, ok := callee.(*expr.Expr); ok && t.Head == "call" && len(t.Args) == 3 && t.Args[0] == any(expr.GlobalRef{Mod: "Core", Name: "tuple"}) {
		callee, f.lib = t.Args[1], c.prepValue(t.Args[2])
	}
	q, ok := callee.(expr.QuoteNode)
	name, ok2 := q.Value.(expr.Symbol)
	if !ok || !ok2 {
		return oUnsupported{"a ccall to a computed function: " + showValue(x.Args[0])}
	}
	f.name = name
	return f
}

// refArg is whether v is a Ptr{T} passed for a Ref{T} argument.
func refArg(at, v any) bool {
	d, ok1 := at.(*core.DataType)
	p, ok2 := v.(*Pointer)
	return ok1 && ok2 && d.Name == typeNameOf(core.RefType) && p.Type.Parameters[0] == d.Parameters[0]
}

func (f *frame) evalForeign(x oForeign) (any, error) {
	vm := f.vm
	if _, err := f.evalValue(x.ret); err != nil {
		return nil, err
	}
	at, err := f.evalValue(x.atypes)
	if err != nil {
		return nil, err
	}
	atypes, ok := at.(SVec)
	if !ok || len(atypes) > len(x.args) {
		return nil, vm.errorf("Invalid IR: malformed :foreigncall")
	}
	args := make([]any, len(atypes))
	for i := range args {
		v, err := f.evalValue(x.args[i])
		if err != nil {
			return nil, err
		}
		// an Any argument is passed boxed; a primitive one by value, which
		// lowering has already converted (cconvert/unsafe_convert) to it; a
		// Ref{T} one as the Ptr{T} unsafe_convert(Ref{T}, x) made
		// (refpointer.jl:110-111)
		if atypes[i] != any(core.AnyType) && atypes[i] != any(vm.typeOf(v)) && !refArg(atypes[i], v) {
			return nil, fmt.Errorf("%w: a ccall argument of type %s", ErrUnsupported, showValue(atypes[i]))
		}
		args[i] = v
	}
	if x.lib != nil {
		lib, err := f.evalValue(x.lib)
		if err != nil {
			return nil, err
		}
		name, _ := lib.(string)
		if sh := vm.libShims[name][x.name]; sh != nil {
			return sh(vm, args)
		}
		return nil, fmt.Errorf("%w: ccall((:%s, %s))", ErrUnsupported, x.name, showValue(lib))
	}
	sh := vm.shims[x.name]
	if sh == nil {
		return nil, fmt.Errorf("%w: ccall(:%s)", ErrUnsupported, x.name)
	}
	return sh(vm, args)
}
