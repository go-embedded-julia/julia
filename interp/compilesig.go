package interp

import (
	"github.com/go-embedded-julia/julia/core"
)

// The compilation signature of a call (gf.c:963-1468, julia 1.12.6): the
// argument types julia compiles and caches a method for, widened from the
// call's by its specialization heuristics, and the test of whether a
// signature is one. Inference asks for both (jl_normalize_to_compilable_sig).

// Method's fields these read (methodFields).
const (
	methodSig           = 7
	methodUnspecialized = 14
	methodCalled        = 23
	methodNospecialize  = 24
	methodIsva          = 26
	methodMaxVarargs    = 31
)

// veryGeneralType is very_general_type (gf.c:963-966).
func veryGeneralType(t any) bool {
	return t == any(core.AnyType) || core.TypesEqual(t, core.TypeType)
}

// nthSlotType is jl_nth_slot_type (gf.c:968-978).
func nthSlotType(sig any, i int) any {
	ps := core.UnwrapUnionAll(sig).(*core.DataType).Parameters
	if i < len(ps)-1 {
		return ps[i]
	}
	p := ps[len(ps)-1]
	if va, ok := p.(*core.Vararg); ok {
		return varargT(va)
	}
	return p
}

// varargT is jl_unwrap_vararg: T, or Any when absent.
func varargT(va *core.Vararg) any {
	if va.T == nil {
		return core.AnyType
	}
	return va.T
}

// vaTupleKind is jl_va_tuple_kind (julia.h): the Vararg kind of a
// signature's last parameter, through its UnionAlls (jl_vararg_kind: no
// Vararg, an Int length, another length, or none).
type vaKind int

const (
	vaNone vaKind = iota
	vaInt
	vaBound
	vaUnbound
)

func vaTupleKind(sig any) vaKind {
	ps := core.UnwrapUnionAll(sig).(*core.DataType).Parameters
	if len(ps) == 0 {
		return vaNone
	}
	va, ok := ps[len(ps)-1].(*core.Vararg)
	switch {
	case !ok:
		return vaNone
	case va.N == nil:
		return vaUnbound
	}
	if _, ok := va.N.(int64); ok {
		return vaInt
	}
	return vaBound
}

// instVarargpInEnv is inst_varargp_in_env (gf.c:998-1035): the
// signature's Vararg with sparams substituted, variables left unbound
// rewrapped around its element type.
func instVarargpInEnv(decl any, sparams SVec) any {
	ps := core.UnwrapUnionAll(decl).(*core.DataType).Parameters
	vm := ps[len(ps)-1]
	if len(sparams) == 0 || !core.HasFreeTypeVars(vm) {
		return vm
	}
	vm = core.InstantiateTypeInEnv(vm, decl, sparams)
	va, ok := vm.(*core.Vararg)
	if !ok {
		return vm
	}
	i := 0
	for u, isU := decl.(*core.UnionAll); isU && i < len(sparams); u, isU = u.Body.(*core.UnionAll) {
		if v, ok := sparams[i].(*core.TypeVar); ok {
			T, N := va.T, va.N
			var nt any = T
			if T != nil && core.HasTypeVar(T, v) {
				nt = core.TypeUnionAll(v, T)
			}
			if N != nil && core.HasTypeVar(N, v) {
				N = nil
			}
			if w, err := core.WrapVararg(nt, N); err == nil {
				va = w
			}
		}
		i++
	}
	return va
}

// maxVarargs is get_max_varargs (gf.c:51-83): how many times a Vararg is
// unrolled, from the method's max_varargs or the most arguments any
// method of its function takes; mayIncrease when that heuristic decided.
func (vm *VM) maxVarargs(m *Struct) (n int, mayIncrease bool) {
	if mv, _ := m.Fields[methodMaxVarargs].(uint8); mv != 0xff {
		return int(mv), false
	}
	sig := m.Fields[methodSig]
	dt := nthArgDatatype(sig, 1)
	if kw := vm.kwcallType(); kw != nil && dt == kw {
		dt = nthArgDatatype(sig, 3)
	}
	if dt == nil || core.IsTypeType(dt) || dt == vm.kwcallType() {
		return 1, false
	}
	n = int(dt.Name.MaxArgs.Load()) + 2
	if kw := vm.kwcallType(); kw != nil && nthArgDatatype(sig, 1) == kw {
		n += 2
	}
	nargs, _ := m.Fields[methodNargs].(int32)
	if n > int(nargs) {
		n -= int(nargs)
	} else {
		n = 0
	}
	return n, true
}

// kwcallType is typeof(Core.kwcall), nil before boot.jl defines it.
func (vm *VM) kwcallType() *core.DataType {
	b := vm.Core.own("kwcall")
	if b == nil || b.Value == undef {
		return nil
	}
	t, _ := vm.typeOf(b.Value).(*core.DataType)
	return t
}

// updateMaxArgs is update_max_args (gf.c:1989-2003): a function's
// max_args raised to the arguments of a method inserted into its table.
func (vm *VM) updateMaxArgs(sig any) {
	t, ok := core.UnwrapUnionAll(sig).(*core.DataType)
	if !ok {
		return
	}
	dt := nthArgDatatype(t, 1)
	if dt == nil || core.IsTypeType(dt) || dt == vm.kwcallType() {
		return
	}
	na := int32(len(t.Parameters))
	if vaTupleKind(t) == vaUnbound {
		na--
	}
	if na > dt.Name.MaxArgs.Load() {
		dt.Name.MaxArgs.Store(na)
	}
}

func boolBit32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func (vm *VM) argBit(m *Struct, field int, iArg int) bool {
	bits, _ := m.Fields[field].(int32)
	return iArg > 0 && iArg <= 32 && bits&(1<<(iArg-1)) != 0
}

// compilationSig is jl_compilation_sig (gf.c:1043-1284): the widened
// parameters of tt for definition, nil when tt's own are kept.
func (vm *VM) compilationSig(tt *core.DataType, sparams SVec, def *Struct, maxVarargs int) []any {
	decl := def.Fields[methodSig]
	n32, _ := def.Fields[methodNargs].(int32)
	nargs := int(n32)
	nspec := maxVarargs + nargs
	if def.Fields[methodGenerator] != undef {
		return nil // staged functions are not optimized
	}
	if decl == any(core.AnyTupleType) && def.Fields[methodUnspecialized] != undef {
		return append([]any(nil), core.AnyTupleType.Parameters...) // builtin methods
	}
	np := len(tt.Parameters)
	switch vaTupleKind(decl) {
	case vaNone:
		if core.IsVaTuple(tt) || np != nargs {
			return nil
		}
	case vaInt, vaBound:
		if core.IsVaTuple(tt) || np < nargs-1 {
			return nil
		}
	case vaUnbound:
		if np < nspec && core.IsVaTuple(tt) {
			return nil
		}
	}
	var newparams []any
	set := func(i int, v any) {
		if newparams == nil {
			newparams = append([]any(nil), tt.Parameters...)
		}
		newparams[i] = v
	}
	for i := 0; i < np; i++ {
		elt := tt.Parameters[i]
		if va, ok := elt.(*core.Vararg); ok {
			elt = varargT(va)
		}
		declI := nthSlotType(decl, i)
		typeI := core.RewrapUnionAll(declI, decl)
		iArg := min(i, nargs-1)
		switch {
		case core.IsKind(typeI):
			// the match was against the kind, not a Type: cache that
			elt = typeI
			set(i, elt)
		case core.IsTypeType(elt):
			kind := vm.typeOf(elt.(*core.DataType).Parameters[0])
			if core.Subtype(kind, typeI) && !core.Subtype(core.TypeType, typeI) {
				elt = kind
				set(i, elt)
			}
		case core.IsKind(elt):
			// a kind where one is not expected: widened to Type{T}
			if !(core.Subtype(elt, typeI) && !core.Subtype(core.TypeType, typeI)) {
				elt = core.TypeType
				set(i, elt)
			}
		}
		if core.IsKind(elt) {
			continue // kind slots always need guard entries
		}
		if vm.argBit(def, methodNospecialize, iArg) && !core.HasFreeTypeVars(declI) && !core.IsKind(declI) {
			if declI != elt {
				set(i, declI)
			}
			continue
		}
		_, eltIsDT := elt.(*core.DataType)
		switch {
		case core.TypesEqual(elt, core.TypeType):
		case !eltIsDT && core.Subtype(elt, core.TypeType):
			set(i, core.TypeType)
		case core.IsTypeType(elt):
			p0 := elt.(*core.DataType).Parameters[0]
			if !core.HasFreeTypeVars(declI) && veryGeneralType(typeI) {
				// an Any or Type slot is not specialized for every Type
				// passed, unless the argument is called (issue #36783)
				if !(iArg <= 8 && vm.argBit(def, methodCalled, iArg)) {
					set(i, core.TypeType)
				}
			} else if core.IsTypeType(p0) && (core.IsTypeType(p0.(*core.DataType).Parameters[0]) || !core.HasFreeTypeVars(declI)) {
				// Type{Type{...}}: compiled as Type{T} unless something more
				// specific was declared
				if isva, _ := def.Fields[methodIsva].(bool); i < nargs || !isva {
					// julia widens a kind di to Type (gf.c:1210); di is none here:
					// a kind holding di holds elt, so typeof(p0) is under it
					// and the kind case above made elt that kind already
					di, _ := core.TypeIntersection(typeI, core.TypeType)
					set(i, di)
				} else {
					set(i, core.TypeType)
				}
			}
		}
		notcalledFunc := iArg > 0 && iArg <= 8 && !vm.argBit(def, methodCalled, iArg) &&
			!core.HasFreeTypeVars(declI) && core.Subtype(elt, core.FunctionType)
		if notcalledFunc && core.Subtype(core.FunctionType, typeI) {
			// a supertype of Function despecialized when not called
			set(i, core.FunctionType)
		}
	}
	// a varargs method is specialized up to max_args
	if np >= nspec && vaTupleKind(decl) == vaUnbound {
		if newparams == nil {
			newparams = append([]any(nil), tt.Parameters...)
		}
		var typeI any = core.Bottom
		if maxVarargs > 0 && nspec >= 2 {
			typeI = newparams[nspec-2]
		}
		all := true
		for j := nspec - 1; j < len(newparams); j++ {
			pj := newparams[j]
			if va, ok := pj.(*core.Vararg); ok {
				pj = varargT(va)
			}
			if !core.Subtype(pj, typeI) {
				all = false
				break
			}
		}
		if all {
			if core.IsTypeType(typeI) && core.IsTypeType(typeI.(*core.DataType).Parameters[0]) {
				typeI = core.TypeType
			}
			va, _ := core.WrapVararg(typeI, nil)
			typeI = va
		} else {
			typeI = instVarargpInEnv(decl, sparams)
		}
		limited := append(append([]any(nil), newparams[:nspec-1]...), typeI)
		newparams = limited
	}
	return newparams
}

// isaCompileableSig is jl_isa_compileable_sig (gf.c:1287-1468): whether
// type is a signature jl_compilation_sig can return for a concrete call.
func (vm *VM) isaCompileableSig(typ any, sparams SVec, def *Struct) bool {
	decl := def.Fields[methodSig]
	t, ok := typ.(*core.DataType)
	if !ok || core.HasFreeTypeVars(typ) {
		return false
	}
	if decl == any(core.AnyTupleType) && def.Fields[methodUnspecialized] != undef {
		return vm.egal(typ, decl) // builtin methods
	}
	np := len(t.Parameters)
	n32, _ := def.Fields[methodNargs].(int32)
	nargs := int(n32)
	if np == 0 {
		return nargs == 0
	}
	isva, _ := def.Fields[methodIsva].(bool)
	if def.Fields[methodGenerator] != undef {
		// staged functions: the caller is assumed intelligent
		return (isva && np >= nargs-1 || !isva && np == nargs) && t.IsDispatchTuple
	}
	lastVa := isVararg(t.Parameters[np-1])
	if isva {
		mv, heuristic := vm.maxVarargs(def)
		nspecMin := nargs + mv
		nspecMax := nspecMin
		if heuristic {
			nspecMax = 1<<31 - 1 // new methods may raise nspec_min later
		}
		isunbound := vaTupleKind(decl) == vaUnbound
		if lastVa {
			if !isunbound || np < nspecMin || np > nspecMax {
				return false
			}
		} else if np < nargs-1 || (isunbound && np >= nspecMax) {
			return false
		}
	} else if np != nargs || lastVa {
		return false
	}
	for i := 0; i < np; i++ {
		elt := t.Parameters[i]
		iArg := min(i, nargs-1)
		if va, ok := elt.(*core.Vararg); ok {
			typeI := instVarargpInEnv(decl, sparams)
			if core.HasFreeTypeVars(typeI) {
				return false
			}
			if vm.egal(elt, typeI) {
				continue
			}
			elt = varargT(va)
			if core.IsTypeType(elt) && core.IsTypeType(elt.(*core.DataType).Parameters[0]) {
				return false // elt would be set to Type instead
			}
		}
		declI := nthSlotType(decl, i)
		typeI := core.RewrapUnionAll(declI, decl)
		if vm.argBit(def, methodNospecialize, iArg) && !core.HasFreeTypeVars(declI) && !core.IsKind(declI) {
			if vm.egal(elt, declI) {
				continue
			}
			return false
		}
		if core.IsKind(elt) {
			// kind slots always get guard entries
			if core.Subtype(elt, typeI) && !core.Subtype(core.TypeType, typeI) {
				continue
			}
			return false
		} else if core.IsKind(typeI) {
			return false
		}
		if core.IsTypeType(core.UnwrapUnionAll(elt)) {
			iscalled := (iArg > 0 && iArg <= 8 && vm.argBit(def, methodCalled, iArg)) || core.HasFreeTypeVars(declI)
			if core.TypesEqual(elt, core.TypeType) {
				if !iscalled && veryGeneralType(typeI) {
					continue
				}
				if i >= nargs && isva {
					continue
				}
				return false
			}
			if !iscalled && veryGeneralType(typeI) {
				return false
			}
			ed, ok := elt.(*core.DataType)
			if !ok {
				return false
			}
			p0 := ed.Parameters[0]
			// julia first refuses a kind of Union{} (gf.c:1409), which no value's
			// type is: Type{Union{}} is typeof(Union{}), never a Type{T} here
			kind := vm.typeOf(p0)
			if core.Subtype(kind, typeI) && !core.Subtype(core.TypeType, typeI) {
				return false // gets turned into a kind
			}
			if core.IsTypeType(p0) && (core.IsTypeType(p0.(*core.DataType).Parameters[0]) || !core.HasFreeTypeVars(declI)) {
				if i < nargs || !isva {
					ti, _ := core.TypeIntersection(typeI, core.TypeType)
					if core.IsKind(ti) || !core.TypesEqual(ti, elt) {
						return false
					}
					continue
				}
				return false
			}
			continue
		}
		notcalledFunc := iArg > 0 && iArg <= 8 && !vm.argBit(def, methodCalled, iArg) &&
			!core.HasFreeTypeVars(declI) && core.Subtype(elt, core.FunctionType)
		if notcalledFunc && core.Subtype(core.FunctionType, typeI) {
			if elt == any(core.FunctionType) {
				continue
			}
			return false
		}
		if !core.IsConcrete(elt) {
			return false
		}
	}
	return true
}

// normalizeToCompilableSig is jl_normalize_to_compilable_sig(ti, env, m,
// return_if_compileable) (gf.c:3660-3685).
func (vm *VM) normalizeToCompilableSig(a []any) (any, error) {
	ti, ok1 := a[0].(*core.DataType)
	env, ok2 := a[1].(SVec)
	m, ok3 := a[2].(*Struct)
	ric, ok4 := a[3].(int32)
	if !ok1 || !ok2 || !ok3 || m.Type != vm.tMethod || !ok4 || !core.IsTupleType(ti) {
		return nil, vm.typeError("ccall", "", core.AnyTupleType, a[0])
	}
	mv, _ := vm.maxVarargs(m)
	newparams := vm.compilationSig(ti, env, m, mv)
	compileable := ti.IsDispatchTuple
	tt := ti
	if newparams != nil {
		t, err := core.Apply(core.AnyTupleType, newparams...)
		if err != nil {
			return nil, err
		}
		tt = t.(*core.DataType)
		if !compileable {
			_, e, _, err := core.TypeIntersectionEnv(tt, m.Fields[methodSig])
			if err != nil {
				return nil, err
			}
			env = SVec(e)
		}
	}
	if !compileable {
		compileable = vm.isaCompileableSig(tt, env, m)
	}
	if ric == 0 || compileable {
		return tt, nil
	}
	return nil, nil
}
