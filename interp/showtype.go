package interp

import (
	"strings"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// writeType is julia's show of a type (base/show.jl:969-1200, without the
// IOContext): a DataType by its name and parameters, a UnionAll as its body
// and a where clause, a type wrapper by its name. Of show_typealias
// (show.jl:615-814) it keeps the aliases of the array and memory types of
// Core and Base, matched by shape (Vector{Int64}, Memory, Matrix{T} where
// T<:Integer); the general search for an alias needs type intersection.
func writeType(b *strings.Builder, t any) { writeTypeIn(b, t, nil) }

// writeTypeIn is writeType inside the where clauses of an enclosing
// UnionAll: a variable they bind is shown by its name alone, as julia's
// :unionall_env makes it.
func writeTypeIn(b *strings.Builder, t any, bound []*core.TypeVar) {
	switch x := t.(type) {
	case *core.DataType:
		writeDataType(b, x, nil, bound)
	case *core.UnionAll:
		writeUnionAll(b, x, bound)
	case *core.Union:
		b.WriteString("Union{")
		for i, u := range unionMembers(x, nil) {
			if i > 0 {
				b.WriteString(", ")
			}
			writeParam(b, u, bound)
		}
		b.WriteByte('}')
	case *core.Vararg:
		b.WriteString("Vararg")
		if x.T != nil {
			b.WriteByte('{')
			writeParam(b, x.T, bound)
			if x.N != nil {
				b.WriteString(", ")
				writeParam(b, x.N, bound)
			}
			b.WriteByte('}')
		}
	}
}

// writeParam shows a type parameter: a bound variable by its name, a type
// in the same where context, any other value as itself.
func writeParam(b *strings.Builder, p any, bound []*core.TypeVar) {
	switch x := p.(type) {
	case *core.TypeVar:
		for _, v := range bound {
			if v == x {
				b.WriteString(x.Name)
				return
			}
		}
		writeTypeVar(b, x)
	case *core.DataType, *core.UnionAll, *core.Union, *core.Vararg:
		writeTypeIn(b, x, bound)
	default:
		writeValue(b, p)
	}
}

func unionMembers(u any, acc []any) []any {
	if x, ok := u.(*core.Union); ok {
		return unionMembers(x.B, unionMembers(x.A, acc))
	}
	return append(acc, u)
}

// typeAlias is one alias of show_typealias: name{params} for a DataType of
// the wrapper whose leading parameters are fixed.
type typeAlias struct {
	name   string
	tn     *core.TypeName
	fixed  map[int]any // parameter index → the value the alias fixes
	params []int       // the parameters the alias shows, in order
}

func primaryName(ua *core.UnionAll) *core.TypeName {
	return core.UnwrapUnionAll(ua).(*core.DataType).Name
}

var typeAliases = []typeAlias{
	{"Vector", core.ArrayType.Body.(*core.UnionAll).Body.(*core.DataType).Name, map[int]any{1: int64(1)}, []int{0}},
	{"Matrix", core.ArrayType.Body.(*core.UnionAll).Body.(*core.DataType).Name, map[int]any{1: int64(2)}, []int{0}},
	{"AbstractVector", primaryName(core.AbstractArrayType), map[int]any{1: int64(1)}, []int{0}},
	{"AbstractMatrix", primaryName(core.AbstractArrayType), map[int]any{1: int64(2)}, []int{0}},
	{"DenseVector", primaryName(core.DenseArrayType), map[int]any{1: int64(1)}, []int{0}},
	{"DenseMatrix", primaryName(core.DenseArrayType), map[int]any{1: int64(2)}, []int{0}},
	{"Memory", primaryName(core.GenericMemoryType), map[int]any{0: expr.Symbol("not_atomic"), 2: core.CPU}, []int{1}},
	{"AtomicMemory", primaryName(core.GenericMemoryType), map[int]any{0: expr.Symbol("atomic"), 2: core.CPU}, []int{1}},
	{"MemoryRef", primaryName(core.GenericMemoryRefType), map[int]any{0: expr.Symbol("not_atomic"), 2: core.CPU}, []int{1}},
	{"AtomicMemoryRef", primaryName(core.GenericMemoryRefType), map[int]any{0: expr.Symbol("atomic"), 2: core.CPU}, []int{1}},
}

// aliasOf is the alias d is shown by, if any.
func aliasOf(d *core.DataType) *typeAlias {
	for i := range typeAliases {
		a := &typeAliases[i]
		if a.tn != d.Name {
			continue
		}
		ok := true
		for j, v := range a.fixed {
			if d.Parameters[j] != v {
				ok = false
			}
		}
		if ok {
			return a
		}
	}
	return nil
}

// writeDataType is show_datatype (show.jl:1126-1180) with the alias check
// in front. elide is the where-clause variables the caller prints after:
// a trailing parameter that is the last of them, with its declared bounds,
// is left out (show_typeparams, show.jl:689-733), and the caller drops it
// from its clause.
func writeDataType(b *strings.Builder, d *core.DataType, elide *[]*core.TypeVar, bound []*core.TypeVar) {
	params := d.Parameters
	var orig []*core.TypeVar
	name := modulePrefix(d)
	if d.Name.Module == "Core" && (d.Name.Name == "TypeName" || d.Name.Name == "SimpleVector" ||
		d.Name.Name == "TypeofBottom" || d.Name.Name == "TypeofVararg" || d.Name.Name == "BFloat16") {
		name += "Core."
	}
	// as the body of a where clause, an alias whose shown parameter is a type
	// over the clause's variables does not match back (make_typealias's
	// applied === x fails): Array{Vector{T}, 1} where T
	a := aliasOf(d)
	if a != nil && elide != nil {
		for _, j := range a.params {
			if _, isVar := params[j].(*core.TypeVar); !isVar && core.HasFreeTypeVars(params[j]) {
				a = nil
				break
			}
		}
	}
	if a != nil {
		name = a.name
		shown := make([]any, len(a.params))
		for i, j := range a.params {
			shown[i] = params[j]
		}
		params = shown
		orig = []*core.TypeVar{wrapperVar(d.Name, a.params[len(a.params)-1])}
	} else {
		name += d.Name.Name
		for i := range params {
			orig = append(orig, wrapperVar(d.Name, i))
		}
	}
	b.WriteString(name)
	if d.Name == core.TupleTypeName && len(params) == 0 {
		b.WriteString("{}")
		return
	}
	n := len(params)
	if elide != nil && n > 0 && len(*elide) > 0 {
		last := (*elide)[len(*elide)-1]
		if tv, ok := params[n-1].(*core.TypeVar); ok && tv == last && orig[len(orig)-1] != nil &&
			core.Egal(tv.UB, orig[len(orig)-1].UB) && core.Egal(tv.LB, orig[len(orig)-1].LB) && !usedElsewhere(tv, params[:n-1]) {
			n--
			*elide = (*elide)[:len(*elide)-1]
		}
	}
	if n == 0 || d == core.AnyTupleType {
		return
	}
	b.WriteByte('{')
	for i, p := range params[:n] {
		if i > 0 {
			b.WriteString(", ")
		}
		writeParam(b, p, bound)
	}
	b.WriteByte('}')
}

// wrapperVar is the i-th type variable of tn's wrapper, nil past them.
func wrapperVar(tn *core.TypeName, i int) *core.TypeVar {
	w := tn.Wrapper
	for ; i > 0; i-- {
		ua, ok := w.(*core.UnionAll)
		if !ok {
			return nil
		}
		w = ua.Body
	}
	if ua, ok := w.(*core.UnionAll); ok {
		return ua.Var
	}
	return nil
}

func usedElsewhere(tv *core.TypeVar, params []any) bool {
	for _, p := range params {
		if p == any(tv) {
			return true
		}
	}
	return false
}

// writeUnionAll is show(io, ::UnionAll) (show.jl:969-1010): a wrapper by its
// name (an alias's wrapper by the alias), any other as its body and a where
// clause over the variables the body does not elide.
func writeUnionAll(b *strings.Builder, x *core.UnionAll, bound []*core.TypeVar) {
	if d, ok := core.UnwrapUnionAll(x).(*core.DataType); ok && d.Name.Wrapper == any(x) {
		b.WriteString(modulePrefix(d) + d.Name.Name)
		return
	}
	var vars []*core.TypeVar
	var body any = x
	for {
		ua, ok := body.(*core.UnionAll)
		if !ok {
			break
		}
		vars = append(vars, ua.Var)
		body = ua.Body
	}
	// an alias over exactly its own variables is its name: Vector, Memory
	if d, ok := body.(*core.DataType); ok {
		if a := aliasOf(d); a != nil && len(vars) == len(a.params) {
			own := true
			for i, j := range a.params {
				if d.Parameters[j] != any(vars[i]) || !core.Egal(vars[i].UB, core.AnyType) || vars[i].LB != any(core.Bottom) {
					own = false
				}
			}
			if own {
				b.WriteString(a.name)
				return
			}
		}
	}
	var inner strings.Builder
	inScope := append(append([]*core.TypeVar{}, bound...), vars...)
	if d, ok := body.(*core.DataType); ok {
		writeDataType(&inner, d, &vars, inScope)
	} else {
		writeParam(&inner, body, inScope)
	}
	b.WriteString(inner.String())
	for i, v := range vars {
		switch {
		case len(vars) == 1:
			b.WriteString(" where ")
		case i == 0:
			b.WriteString(" where {")
		default:
			b.WriteString(", ")
		}
		writeTypeVar(b, v)
	}
	if len(vars) > 1 {
		b.WriteByte('}')
	}
}
