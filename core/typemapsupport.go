package core

// What julia's typemap (src/typemap.c) asks of the type system.

// HasIntersectTypeNotKind is jl_has_intersect_type_not_kind
// (subtype.c:2444-2459): the intersection of t and Type is non-empty and
// not a kind.
func HasIntersectTypeNotKind(t any) bool { return hasIntersectTypeNotKind(t) }

// HasIntersectKindNotType is jl_has_intersect_kind_not_type
// (subtype.c:2462-2474): DataType <: t, Union <: t, UnionAll <: t and the
// like.
func HasIntersectKindNotType(t any) bool {
	t = UnwrapUnionAll(t)
	if t == any(AnyType) || IsKind(t) {
		return true
	}
	switch x := t.(type) {
	case *Union:
		return HasIntersectKindNotType(x.A) || HasIntersectKindNotType(x.B)
	case *TypeVar:
		return HasIntersectKindNotType(x.UB)
	}
	return false
}
