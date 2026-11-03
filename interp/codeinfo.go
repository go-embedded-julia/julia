package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// CodeInfo as julia code reads and writes it. Lowering hands the VM a
// *expr.CodeInfo, which it evaluates; type inference takes a method's
// source as julia's mutable CodeInfo (jltypes.c:3483-3535), copies it
// (base/expr.jl:79-92) and assigns its fields, so reflection gives it as a
// Struct of that layout, its vectors julia's.

// codeInfoFieldTypes are CodeInfo's field types (jltypes.c:3510-3533).
func (vm *VM) codeInfoFieldTypes() []any {
	vec := func(t any) any { return must(core.Apply(core.ArrayType, t, int64(1))) }
	a, b, u64 := core.AnyType, core.BoolType, core.UInt64Type
	return []any{vecAnyType, vm.Core.own("DebugInfo").Value, a, vec(core.UInt32Type), vec(core.SymbolType),
		vec(core.UInt8Type), a, a, a, a, u64, u64, a, u64, b, b, b, b, b, core.UInt8Type, core.UInt8Type,
		core.UInt16Type, core.UInt16Type}
}

// bootCodeInfoType defines CodeInfo, a mutable struct of julia's fields.
func (vm *VM) bootCodeInfoType() {
	vm.tCodeInfo = must(core.NewType("Core", "CodeInfo", core.AnyType, false, true,
		[]expr.Symbol{"code", "debuginfo", "ssavaluetypes", "ssaflags", "slotnames", "slotflags",
			"slottypes", "rettype", "parent", "edges", "min_world", "max_world",
			"method_for_inference_limit_heuristics", "nargs", "propagate_inbounds", "has_fcall",
			"has_image_globalref", "nospecializeinfer", "isva", "inlining", "constprop", "purity",
			"inlining_cost"}, vm.codeInfoFieldTypes()))
	vm.setCore("CodeInfo", vm.tCodeInfo)
}

// juliaCodeInfo is src as julia holds it: its statements in a fresh
// Vector{Any}, ssaflags, slotnames and slotflags in vectors of their
// element types, its line table a DebugInfo, and the C NULLs nothing.
// Lowering's return type, julia's Any, is Any here.
func (vm *VM) juliaCodeInfo(src *expr.CodeInfo) *Struct {
	f := src.Values()
	f[0] = vm.newVector(core.AnyType, append([]any(nil), src.Code...))
	f[1] = vm.juliaDebugInfo(src.DebugInfo)
	f[3] = vm.newVector(core.UInt32Type, append([]any(nil), src.SSAFlags...))
	f[4] = vm.newVector(core.SymbolType, append([]any(nil), src.SlotNames...))
	f[5] = vm.newVector(core.UInt8Type, append([]any(nil), src.SlotFlags...))
	if _, ok := src.RetType.(expr.Opaque); ok {
		f[7] = core.AnyType
	}
	if e, ok := src.Edges.(expr.Vector); ok {
		f[9] = SVec(append([]any(nil), e...))
	}
	return &Struct{Type: vm.tCodeInfo, Fields: f}
}

// juliaDebugInfo is a line table as julia's DebugInfo(def, linetable,
// edges, codelocs), the edges a svec of DebugInfos.
func (vm *VM) juliaDebugInfo(v any) any {
	d, ok := v.(*expr.DebugInfo)
	if !ok {
		return v
	}
	edges := make(SVec, len(d.Edges))
	for i, e := range d.Edges {
		edges[i] = vm.juliaDebugInfo(e)
	}
	t := vm.Core.own("DebugInfo").Value.(*core.DataType)
	return &Struct{Type: t, Fields: []any{d.Def, vm.juliaDebugInfo(d.Linetable), edges, d.Codelocs}}
}

// copyCodeInfo is jl_copy_code_info (method.c:833-841): a shallow copy.
func (vm *VM) copyCodeInfo(a []any) (any, error) {
	switch src := a[0].(type) {
	case *expr.CodeInfo:
		return vm.juliaCodeInfo(src), nil
	case *Struct:
		if src.Type == vm.tCodeInfo {
			return &Struct{Type: vm.tCodeInfo, Fields: append([]any(nil), src.Fields...)}, nil
		}
	}
	return nil, vm.typeError("ccall", "", vm.tCodeInfo, a[0])
}

// methodDidScanSource is Method's did_scan_source field (jltypes.c:3570).
const methodDidScanSource = 29

// scanMethodSourceNow is jl_scan_method_source_now (method.c:51-77) over
// a CodeInfo: once per method, the flag 0x1, and 0x2 when a statement is
// a GlobalRef into the method's own module (jl_maybe_add_binding_backedge,
// module.c:1667-1683). The VM keeps one value per binding and invalidates
// nothing when a binding changes, so the backedges a GlobalRef into
// another module adds are not kept.
func (vm *VM) scanMethodSourceNow(a []any) (any, error) {
	m, ok := a[0].(*Struct)
	if !ok || m.Type != vm.tMethod {
		return nil, vm.typeError("ccall", "", vm.tMethod, a[0])
	}
	src, ok := a[1].(*Struct)
	if !ok || src.Type != vm.tCodeInfo {
		return nil, fmt.Errorf("%w: jl_scan_method_source_now of %s", ErrUnsupported, showValue(a[1]))
	}
	flags := m.Fields[methodDidScanSource].(uint8)
	if flags&1 != 0 {
		return nil, nil
	}
	flags |= 1
	_, _, code, _ := arrayOf(src.Fields[0])
	for _, st := range code {
		if gr, ok := st.(expr.GlobalRef); ok && vm.modules[gr.Mod] == m.Fields[1] {
			flags |= 2
		}
	}
	m.Fields[methodDidScanSource] = flags
	return nil, nil
}

// irNodeFields are the fields of the lowered form's nodes package expr
// holds as Go values, with the layouts jltypes.c gives them (:3165-3178,
// :3401-3480); a ReturnNode without a value and an EnterNode without a
// scope leave theirs undefined.
func irNodeFields(v any) ([]expr.Symbol, []any, bool) {
	switch x := v.(type) {
	case expr.SSAValue:
		return []expr.Symbol{"id"}, []any{int64(x)}, true
	case expr.SlotNumber:
		return []expr.Symbol{"id"}, []any{int64(x)}, true
	case expr.Argument:
		return []expr.Symbol{"n"}, []any{int64(x)}, true
	case expr.GotoNode:
		return []expr.Symbol{"label"}, []any{int64(x)}, true
	case expr.GotoIfNot:
		return []expr.Symbol{"cond", "dest"}, []any{x.Cond, int64(x.Dest)}, true
	case expr.ReturnNode:
		return []expr.Symbol{"val"}, []any{x.Val}, true
	case expr.Unreachable:
		return []expr.Symbol{"val"}, []any{undef}, true
	case expr.NewvarNode:
		return []expr.Symbol{"slot"}, []any{x.Slot}, true
	case expr.EnterNode:
		var scope any = undef
		if x.HasScope {
			scope = x.Scope
		}
		return []expr.Symbol{"catch_dest", "scope"}, []any{int64(x.Catch), scope}, true
	}
	return nil, nil, false
}

// irNodeField is getfield of such a node.
func (vm *VM) irNodeField(v, arg any) (any, error) {
	names, fields, _ := irNodeFields(v)
	i, err := vm.fieldIndex("getfield", vm.typeOf(v).(*core.DataType), names, len(names), v, arg)
	if err != nil {
		return nil, err
	}
	if fields[i] == undef {
		return nil, vm.exception(vm.tUndefRef)
	}
	return fields[i], nil
}
