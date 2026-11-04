package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// A CodeInstance's fields, at jltypes.c:3645-3693's indices.
const (
	ciDef = iota
	ciOwner
	ciNext
	ciMinWorld
	ciMaxWorld
	ciRettype
	ciExctype
	ciRettypeConst
	ciInferred
	ciDebuginfo
	ciEdges
	ciAnalysisResults
	ciIpoPurityBits
	ciTimeInferTotal
	ciTimeInferCacheSaved
	ciTimeInferSelf
	ciTimeCompile
	ciSpecsigflags
	ciPrecompile
	ciInvoke
	ciSpecptr
	ciNumFields
)

// The MethodInstance's cache field (jltypes.c:3618-3626).
const miCache = 4

// cfunc is the target of a C function pointer the runtime stores in a
// CodeInstance's invoke field.
type cfunc string

// fptrConstReturn is jl_fptr_const_return (gf.c:665): a call returns
// rettype_const.
const fptrConstReturn cfunc = "jl_fptr_const_return"

// bootCodeInstanceType defines CodeInstance with julia's layout, its next
// field a CodeInstance and its const and atomic fields marked (jltypes.c:
// 3645-3700).
func (vm *VM) bootCodeInstanceType() {
	t := core.NewIncompleteType("Core", "CodeInstance", nil, false, true, []expr.Symbol{"def", "owner",
		"next", "min_world", "max_world", "rettype", "exctype", "rettype_const", "inferred", "debuginfo",
		"edges", "analysis_results", "ipo_purity_bits", "time_infer_total", "time_infer_cache_saved",
		"time_infer_self", "time_compile", "specsigflags", "precompile", "invoke", "specptr"})
	must(0, t.SetSuper(core.AnyType))
	any_, ulong := core.AnyType, core.UInt64Type
	must(0, t.SetFieldTypes([]any{any_, any_, t, ulong, ulong, any_, any_, any_, any_,
		vm.Core.own("DebugInfo").Value, core.SimpleVectorType, any_, core.UInt32Type,
		core.UInt16Type, core.UInt16Type, core.UInt16Type, core.UInt16Type, core.UInt8Type,
		core.BoolType, any_, any_}))
	t.Name.ConstFields = marked(ciNumFields, 0, 1, 5, 6, 7, 11, 13, 14, 15)
	t.Name.AtomicFields = marked(ciNumFields, 2, 3, 4, 8, 9, 10, 12, 16, 17, 18, 19, 20)
	vm.tCodeInstance = t
	vm.setCore("CodeInstance", t)
}

// reservation is one of jl_engine_reserve's leases (engine.cpp:51).
type reservation struct {
	mi    *Struct
	owner any
	ci    *Struct
}

// newCodeinst is jl_new_codeinst (gf.c:636-676), Core.CodeInstance: mi
// and the C NULLs of its arguments are undef.
func (vm *VM) newCodeinst(a []any) (any, error) {
	if err := vm.isCodeDef(a[0]); err != nil {
		return nil, err
	}
	flags, ok1 := a[6].(int32)
	minW, ok2 := a[7].(uint64)
	maxW, ok3 := a[8].(uint64)
	effects, ok4 := a[9].(uint32)
	if !ok1 || !ok2 || !ok3 || !ok4 || minW > maxW {
		return nil, fmt.Errorf("%w: jl_new_codeinst of %s", ErrUnsupported, vm.argTypes(a))
	}
	inferredConst := a[4]
	if flags&2 == 0 {
		inferredConst = undef
	}
	di := a[11]
	if di == nil {
		di = undef
	}
	ci := &Struct{Type: vm.tCodeInstance, Fields: []any{a[0], a[1], undef, minW, maxW, a[2], a[3],
		inferredConst, a[5], di, a[12], a[10], effects, uint16(0), uint16(0), uint16(0), uint16(0),
		uint8(0), false, undef, undef}}
	if flags&1 != 0 {
		if flags&2 == 0 {
			return nil, fmt.Errorf("%w: jl_new_codeinst of a constant return without its constant", ErrUnsupported)
		}
		ci.Fields[ciInvoke] = fptrConstReturn
	}
	return ci, nil
}

// argTypes names a ccall's argument types, for a refusal: the values a
// code-cache call takes refer to one another (a MethodInstance's cache
// holds CodeInstances whose def is the MethodInstance).
func (vm *VM) argTypes(a []any) string {
	ts := make([]any, len(a))
	for i, v := range a {
		ts[i] = vm.typeOf(v)
	}
	return showValue(vm.newTuple(ts))
}

// isCodeDef checks a CodeInstance's def: a MethodInstance or an
// ABIOverride (julia_internal.h:708-715).
func (vm *VM) isCodeDef(def any) error {
	if s, ok := def.(*Struct); ok && (s.Type == vm.tMethodInstance || any(s.Type) == vm.Core.own("ABIOverride").Value) {
		return nil
	}
	return vm.typeError("ccall", "", vm.tMethodInstance, def)
}

// codeInstanceArg is a ccall's CodeInstance argument.
func (vm *VM) codeInstanceArg(v any) (*Struct, error) {
	if ci, ok := v.(*Struct); ok && ci.Type == vm.tCodeInstance {
		return ci, nil
	}
	return nil, vm.typeError("ccall", "", vm.tCodeInstance, v)
}

// methodInstanceArg is a ccall's MethodInstance argument.
func (vm *VM) methodInstanceArg(v any) (*Struct, error) {
	if mi, ok := v.(*Struct); ok && mi.Type == vm.tMethodInstance {
		return mi, nil
	}
	return nil, vm.typeError("ccall", "", vm.tMethodInstance, v)
}

// miCacheInsert is jl_mi_cache_insert (gf.c:752-768): ci at the head of
// mi's cache list.
func (vm *VM) miCacheInsert(a []any) (any, error) {
	mi, err := vm.methodInstanceArg(a[0])
	if err != nil {
		return nil, err
	}
	ci, err := vm.codeInstanceArg(a[1])
	if err != nil {
		return nil, err
	}
	ci.Fields[ciNext] = mi.Fields[miCache]
	mi.Fields[miCache] = ci
	return nil, nil
}

// rettypeInferred is jl_rettype_inferred (gf.c:3150-3170): the first of
// mi's CodeInstances of this owner valid over the whole world range and
// inferred, else nothing.
func (vm *VM) rettypeInferred(a []any) (any, error) {
	mi, err := vm.methodInstanceArg(a[1])
	if err != nil {
		return nil, err
	}
	minW, ok1 := a[2].(uint64)
	maxW, ok2 := a[3].(uint64)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("%w: jl_rettype_inferred of %s", ErrUnsupported, vm.argTypes(a))
	}
	for ci, ok := mi.Fields[miCache].(*Struct); ok; ci, ok = ci.Fields[ciNext].(*Struct) {
		if ci.Fields[ciMinWorld].(uint64) <= minW && maxW <= ci.Fields[ciMaxWorld].(uint64) &&
			vm.egal(ci.Fields[ciOwner], a[0]) && ci.Fields[ciInferred] != undef {
			return ci, nil
		}
	}
	return nil, nil
}

// engineReserve is jl_engine_reserve (engine.cpp:60-104): an uninferred
// CodeInstance (jl_new_codeinst_uninit, gf.c:745-750), leased for
// inference of mi by owner. The VM runs one task, so a lease already held
// is its own: the cycle, and no new lease.
func (vm *VM) engineReserve(a []any) (any, error) {
	mi, err := vm.methodInstanceArg(a[0])
	if err != nil {
		return nil, err
	}
	ci := &Struct{Type: vm.tCodeInstance, Fields: []any{mi, a[1], undef, uint64(1), uint64(0), undef, undef,
		undef, undef, undef, undef, undef, uint32(0), uint16(0), uint16(0), uint16(0), uint16(0),
		uint8(0), false, undef, undef}}
	for _, r := range vm.reservations {
		if r.mi == mi && vm.egal(r.owner, a[1]) {
			return ci, nil
		}
	}
	vm.reservations = append(vm.reservations, reservation{mi, a[1], ci})
	return ci, nil
}

// engineFulfill is jl_engine_fulfill (engine.cpp:138-149): the lease of
// ci ends.
func (vm *VM) engineFulfill(a []any) (any, error) {
	ci, err := vm.codeInstanceArg(a[0])
	if err != nil {
		return nil, err
	}
	for i, r := range vm.reservations {
		if r.ci == ci {
			vm.reservations = append(vm.reservations[:i], vm.reservations[i+1:]...)
			break
		}
	}
	return nil, nil
}

// promoteCIToCurrent is jl_promote_ci_to_current (gf.c:1776-1809): a
// CodeInstance validated in the current world, and the CodeInstances among
// its edges, valid in every later world.
func (vm *VM) promoteCIToCurrent(a []any) (any, error) {
	ci, err := vm.codeInstanceArg(a[0])
	if err != nil {
		return nil, err
	}
	w, ok := a[1].(uint64)
	if !ok {
		return nil, vm.typeError("ccall", "", core.UInt64Type, a[1])
	}
	if vm.World() == w {
		vm.promoteCI(ci, w)
	}
	return nil, nil
}

func (vm *VM) promoteCI(ci *Struct, w uint64) {
	if ci.Fields[ciMaxWorld] != any(w) {
		return
	}
	ci.Fields[ciMaxWorld] = ^uint64(0)
	edges, _ := ci.Fields[ciEdges].(SVec)
	for _, e := range edges {
		if s, ok := e.(*Struct); ok && s.Type == vm.tCodeInstance {
			vm.promoteCI(s, w)
		}
	}
}

// invokeAPI is jl_invoke_api (gf.c:3643-3658): which of the runtime's
// calling conventions a CodeInstance's invoke pointer is, 0 for none, -1
// for one it does not name.
func (vm *VM) invokeAPI(a []any) (any, error) {
	ci, err := vm.codeInstanceArg(a[0])
	if err != nil {
		return nil, err
	}
	switch f := ci.Fields[ciInvoke]; f {
	case nil, undef:
		return int32(0), nil
	case cfunc("jl_fptr_args"):
		return int32(1), nil
	case fptrConstReturn:
		return int32(2), nil
	case cfunc("jl_fptr_sparam"):
		return int32(3), nil
	case cfunc("jl_fptr_interpret_call"):
		return int32(4), nil
	default:
		if p, ok := f.(*Pointer); ok && p.Target == nil && p.Off == 0 {
			return int32(0), nil
		}
	}
	return int32(-1), nil
}

// methodInstanceAddBackedge is jl_method_instance_add_backedge(callee,
// invokesig, caller) (gf.c:2239-2261, push_edge, method.c:1068-1074): the
// caller recorded on the callee's backedges, after its invoke signature
// when it has one. The VM keeps them; it invalidates nothing yet.
func (vm *VM) methodInstanceAddBackedge(a []any) (any, error) {
	callee, err := vm.methodInstanceArg(a[0])
	if err != nil {
		return nil, err
	}
	caller, err := vm.codeInstanceArg(a[2])
	if err != nil {
		return nil, err
	}
	if a[1] != nil && !core.IsType(a[1]) {
		return nil, vm.typeError("ccall", "", core.TypeType, a[1])
	}
	if callee.Fields[3] == undef {
		callee.Fields[3] = vm.newVector(core.AnyType, nil)
	}
	backedges := callee.Fields[3].(*Struct)
	if a[1] != nil {
		vm.vectorPush(backedges, a[1])
	}
	vm.vectorPush(backedges, caller)
	return nil, nil
}

// methodSigLatestOnly is METHOD_SIG_LATEST_ONLY (julia_internal.h:678).
const methodSigLatestOnly = 0b0010

// promoteMIToCurrent is jl_promote_mi_to_current (gf.c:1811-1829): a
// MethodInstance whose method has no other signature in force, marked so
// in the world it was validated in.
func (vm *VM) promoteMIToCurrent(a []any) (any, error) {
	mi, err := vm.methodInstanceArg(a[0])
	if err != nil {
		return nil, err
	}
	minW, ok1 := a[1].(uint64)
	w, ok2 := a[2].(uint64)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("%w: jl_promote_mi_to_current of %s", ErrUnsupported, vm.argTypes(a))
	}
	if vm.World() != w {
		return nil, nil
	}
	m, ok := mi.Fields[0].(*Struct)
	if !ok || m.Type != vm.tMethod {
		return nil, nil
	}
	mstatus, _ := m.Fields[4].(uint8)
	mistatus, _ := mi.Fields[7].(uint8)
	if mstatus&methodSigLatestOnly != 0 || m.Fields[6] != any(minW) || mistatus&methodSigLatestOnly != 0 {
		return nil, nil
	}
	mi.Fields[7] = uint8(methodSigLatestOnly)
	return nil, nil
}

// fillCodeinst is jl_fill_codeinst (gf.c:707-743): an uninferred
// CodeInstance's results, its inferred field nothing.
func (vm *VM) fillCodeinst(a []any) (any, error) {
	ci, err := vm.codeInstanceArg(a[0])
	if err != nil {
		return nil, err
	}
	flags, ok1 := a[4].(int32)
	minW, ok2 := a[5].(uint64)
	maxW, ok3 := a[6].(uint64)
	effects, ok4 := a[7].(uint32)
	if !ok1 || !ok2 || !ok3 || !ok4 || minW > maxW || flags&3 == 1 ||
		ci.Fields[ciMinWorld] != uint64(1) || ci.Fields[ciMaxWorld] != uint64(0) {
		return nil, fmt.Errorf("%w: jl_fill_codeinst of %s", ErrUnsupported, vm.argTypes(a))
	}
	ci.Fields[ciRettype], ci.Fields[ciExctype] = a[1], a[2]
	if flags&2 != 0 {
		ci.Fields[ciRettypeConst] = a[3]
	}
	ci.Fields[ciEdges] = a[10]
	if a[9] != nil {
		ci.Fields[ciDebuginfo] = a[9]
	}
	if flags&1 != 0 {
		ci.Fields[ciInvoke] = fptrConstReturn
	}
	ci.Fields[ciIpoPurityBits], ci.Fields[ciAnalysisResults] = effects, a[8]
	ci.Fields[ciInferred], ci.Fields[ciMinWorld], ci.Fields[ciMaxWorld] = nil, minW, maxW
	return nil, nil
}

// updateCodeinst is jl_update_codeinst (gf.c:678-705): a CodeInstance's
// inference results. The three times arrive as Float64 and are kept as
// julia's Float16 bits (julia_double_to_half).
func (vm *VM) updateCodeinst(a []any) (any, error) {
	ci, err := vm.codeInstanceArg(a[0])
	if err != nil {
		return nil, err
	}
	flags, ok1 := a[2].(int32)
	minW, ok2 := a[3].(uint64)
	maxW, ok3 := a[4].(uint64)
	effects, ok4 := a[5].(uint32)
	var times [3]uint16
	ok5 := true
	for i := range times {
		f, ok := a[7+i].(float64)
		ok5 = ok5 && ok
		times[i] = doubleToHalf(f)
	}
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || minW > maxW || flags&1 != 0 && ci.Fields[ciRettypeConst] == undef {
		return nil, fmt.Errorf("%w: jl_update_codeinst of %s", ErrUnsupported, vm.argTypes(a))
	}
	ci.Fields[ciAnalysisResults] = a[6]
	ci.Fields[ciTimeInferTotal], ci.Fields[ciTimeInferCacheSaved], ci.Fields[ciTimeInferSelf] = times[0], times[1], times[2]
	ci.Fields[ciIpoPurityBits], ci.Fields[ciDebuginfo], ci.Fields[ciEdges] = effects, a[10], a[11]
	if flags&1 != 0 {
		ci.Fields[ciInvoke] = fptrConstReturn
	}
	ci.Fields[ciInferred], ci.Fields[ciMinWorld], ci.Fields[ciMaxWorld] = a[1], minW, maxW
	return nil, nil
}
