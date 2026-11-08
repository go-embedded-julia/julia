package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
)

// compileHint is jl_compile_hint (gf.c:3897-3908), precompile(argt)
// (loading.jl:4228-4234): 1 when jl_get_compile_hint_specialization
// (gf.c:3765-3824) finds the method instance to compile, 0 when it finds
// none. Compiling it is nothing for an interpreter; the answer is what
// Base reads (loading.jl:4257-4260 assert it). The search: no answer for a
// query with free type variables or no concrete subtype, else the methods
// matching it in the newest world; one is the answer. Among several, julia
// keeps those the query is a compileable signature of
// (jl_isa_compileable_sig) and the least specific; that is not ported, and
// such a query is refused.
func (vm *VM) compileHint(a []any) (any, error) {
	tt := a[0]
	if core.HasFreeTypeVars(tt) || !hasConcreteSubtype(tt) {
		return int32(0), nil
	}
	unw, ok := core.UnwrapUnionAll(tt).(*core.DataType)
	if !ok || !core.IsTupleType(unw) || len(unw.Parameters) == 0 {
		return int32(0), nil
	}
	tab, _, _, ok := vm.methodTableOf(unw.Parameters[0])
	if !ok {
		return nil, fmt.Errorf("%w: jl_compile_hint of the methods of %s", ErrUnsupported, showValue(unw.Parameters[0]))
	}
	res, err := tab.MlMatches(tt, -1, false, vm.World(), 0, ^uint64(0))
	if err != nil {
		return nil, err
	}
	switch len(res.List) {
	case 0:
		return int32(0), nil
	case 1:
		return int32(1), nil
	}
	return nil, fmt.Errorf("%w: jl_compile_hint of %s, which %d methods match (jl_isa_compileable_sig)",
		ErrUnsupported, showValue(tt), len(res.List))
}

// hasConcreteSubtype is jl_has_concrete_subtype (gf.c:4932-4942): false
// for Union{}, a DataType's flag (a Vararg's element type's), true for any
// other type.
func hasConcreteSubtype(t any) bool {
	if t == any(core.Bottom) {
		return false
	}
	t = core.UnwrapUnionAll(t)
	if va, ok := t.(*core.Vararg); ok {
		t = va.T
		if t == nil {
			t = core.AnyType
		}
	}
	d, ok := t.(*core.DataType)
	return !ok || d.HasConcreteSubtype
}
