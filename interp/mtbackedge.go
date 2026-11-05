package interp

import (
	"github.com/go-embedded-julia/julia/core"
)

// The method table's backedges: the edges from a signature no method
// matched to the code instances inferred against it, keyed by the top type
// names the signature's argument types reach (jl_method_table->backedges).
// They are kept as julia keeps them; nothing is invalidated from them yet.

// The facts foreach_top_nth_typename records (gf.c:793-802).
const (
	haveType = 1 << iota
	exactlyType
	haveFunction
	exactlyFunction
	haveKwcall
	exactlyKwcall
	exactlyAny
	shortTuple
)

// foreachTopNthTypename is foreach_top_nth_typename (gf.c:804-863), f called
// for each name with explct true: the false calls are for all_subtypes, which
// jl_method_table_add_backedge does not ask.
func (vm *VM) foreachTopNthTypename(f func(*core.TypeName), a any, n int, facts *uint) {
	switch x := a.(type) {
	case *core.DataType:
		if n <= 0 {
			kwcall := vm.typeOf(vm.Core.own("kwcall").Value)
			switch {
			case x.Name == typeNameOf(core.TypeType):
				// key Type{T} on T instead of Type
				*facts |= haveType
				vm.foreachTopNthTypename(f, x.Parameters[0], -1, facts)
			case x == core.FunctionType:
				if n == -1 {
					*facts |= exactlyType
				} else {
					*facts |= haveFunction | exactlyFunction
				}
			case x == core.AnyType:
				if n == -1 {
					*facts |= exactlyType
				} else {
					*facts |= exactlyAny
				}
			case any(x) == kwcall:
				if n == -1 {
					*facts |= exactlyKwcall
				} else {
					*facts |= haveKwcall
				}
			default:
				dt := x
				for {
					super := dt.Super
					if super == core.FunctionType {
						*facts |= haveFunction
						break
					}
					if super == core.AnyType || super.Super == dt {
						break
					}
					dt = super
				}
				f(dt.Name)
			}
		} else if core.IsTupleType(x) {
			if len(x.Parameters) >= n {
				vm.foreachTopNthTypename(f, x.Parameters[n-1], 0, facts)
			} else {
				*facts |= shortTuple
			}
		}
	case *core.TypeVar:
		vm.foreachTopNthTypename(f, x.UB, n, facts)
	case *core.UnionAll:
		vm.foreachTopNthTypename(f, x.Body, n, facts)
	case *core.Union:
		vm.foreachTopNthTypename(f, x.A, n, facts)
		vm.foreachTopNthTypename(f, x.B, n, facts)
	}
}

// foreachTopTypenameFor is jl_foreach_top_typename_for (gf.c:875-893)
// without all_subtypes, as jl_method_table_add_backedge calls it.
func (vm *VM) foreachTopTypenameFor(f func(*core.TypeName), argtypes any) {
	var facts uint
	vm.foreachTopNthTypename(f, argtypes, 1, &facts)
	if facts&haveKwcall != 0 {
		// split kwcall on the 3rd argument instead
		var kwfacts uint
		vm.foreachTopNthTypename(f, argtypes, 3, &kwfacts)
	}
}

// methodTableAddBackedge is jl_method_table_add_backedge(typ, caller)
// (gf.c:2271-2331): the edge under each top type name of typ, once, a type
// equal to one already recorded reused.
func (vm *VM) methodTableAddBackedge(a []any) (any, error) {
	typ, caller := a[0], a[1]
	vm.foreachTopTypenameFor(func(tn *core.TypeName) {
		if vm.mtBackedges == nil {
			vm.mtBackedges = map[*core.TypeName][]any{}
		}
		edges := vm.mtBackedges[tn]
		for i := 1; i < len(edges); i += 2 {
			if edges[i] == caller && core.TypesEqual(edges[i-1], typ) {
				typ = edges[i-1]
				return // this edge already recorded
			}
		}
		for i := 1; i < len(edges); i += 2 {
			if edges[i] != caller && core.TypesEqual(edges[i-1], typ) {
				typ = edges[i-1]
				break
			}
		}
		vm.mtBackedges[tn] = append(edges, typ, caller)
	}, typ)
	return nil, nil
}
