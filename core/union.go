package core

import "strings"

// Union construction: jl_type_union, simple_union and simple_intersect of
// jltypes.c:401-903.

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// datatypeNameCmp is datatype_name_cmp (jltypes.c:419-457).
func datatypeNameCmp(a, b any) int {
	ad, aok := a.(*DataType)
	bd, bok := b.(*DataType)
	if !aok {
		if bok {
			return 1
		}
		return 0
	}
	if !bok {
		return -1
	}
	if c := strings.Compare(ad.Name.Module, bd.Name.Module); c != 0 {
		return c
	}
	if c := strings.Compare(ad.Name.Name, bd.Name.Name); c != 0 {
		return c
	}
	if c := cmpInt(len(ad.Parameters), len(bd.Parameters)); c != 0 {
		return c
	}
	c := 0
	for i := 0; i < 3 && i < len(ad.Parameters); i++ {
		ap, bp := ad.Parameters[i], bd.Parameters[i]
		if ap == bp {
			continue
		}
		_, apd := ap.(*DataType)
		_, bpd := bp.(*DataType)
		_, apu := ap.(*UnionAll)
		_, bpu := bp.(*UnionAll)
		if apd && bpd {
			c = datatypeNameCmp(ap, bp)
			if c != 0 {
				return c
			}
		} else if apu && bpu {
			c = datatypeNameCmp(UnwrapUnionAll(ap), UnwrapUnionAll(bp))
			if c != 0 {
				return c
			}
		} else {
			c = 0
		}
	}
	return c
}

// unionSortCmp is union_sort_cmp (jltypes.c:461-495): singletons first,
// then isbits DataTypes, then other DataTypes, then UnionAlls.
func unionSortCmp(a, b any) int {
	if a == nil {
		if b == nil {
			return 0
		}
		return 1
	}
	if b == nil {
		return -1
	}
	ad, ok := a.(*DataType)
	if ok {
		bd, ok := b.(*DataType)
		if !ok {
			return -1
		}
		switch {
		case ad.isSingleton():
			if bd.isSingleton() {
				return datatypeNameCmp(a, b)
			}
			return -1
		case bd.isSingleton():
			return 1
		case ad.isbits():
			if bd.isbits() {
				return datatypeNameCmp(a, b)
			}
			return -1
		case bd.isbits():
			return 1
		}
		return datatypeNameCmp(a, b)
	}
	if _, ok := b.(*DataType); ok {
		return 1
	}
	return datatypeNameCmp(UnwrapUnionAll(a), UnwrapUnionAll(b))
}

// isSingleton is jl_is_datatype_singleton for the types this package
// knows: a concrete type the boot table marks singleton, or Type{T} of a
// concrete T (whose instance is T itself and has no fields).
//
// A type is a singleton when it is immutable and of size zero: no fields
// (Nothing, Val{x}, Tuple{}), or only fields whose types are singletons
// themselves (Tuple{Missing}, Pair{Nothing,Missing}), which is when julia
// gives it an instance.
func (d *DataType) isSingleton() bool {
	if !d.IsConcrete || d.Name.Mutable {
		return false
	}
	if d.Name.Singleton {
		return true
	}
	fields, ok := d.fieldTypes()
	if !ok {
		return false
	}
	for _, f := range fields {
		fd, ok := f.(*DataType)
		if !ok || !fd.isSingleton() {
			return false
		}
	}
	return true
}

// fieldTypes returns the field types of a tuple or of a struct whose fields
// the boot table records.
func (d *DataType) fieldTypes() ([]any, bool) {
	if d.Name == TupleTypeName {
		return d.Parameters, true
	}
	if d.Name.Fields == nil {
		return nil, false
	}
	var env *typeEnv
	for i, p := range d.Name.Primary.Parameters {
		env = &typeEnv{p.(*TypeVar), d.Parameters[i], env}
	}
	fields := make([]any, len(d.Name.Fields))
	for i, f := range d.Name.Fields {
		fields[i] = instType(f, env, nil, true)
	}
	return fields, true
}

// isbits is jl_isbits: a concrete immutable type whose instances are plain
// bits — a primitive type, a singleton, or a struct or tuple whose field
// types are all isbits (datatype.c, jl_compute_field_offsets).
func (d *DataType) isbits() bool {
	if !d.IsConcrete || d.Name.Mutable {
		return false
	}
	if d.Name.Isbits || d.isSingleton() {
		return true
	}
	fields, ok := d.fieldTypes()
	if !ok {
		return false
	}
	for _, f := range fields {
		fd, ok := f.(*DataType)
		if !ok || !fd.isbits() {
			return false
		}
	}
	return true
}

// IsBits is jl_isbits of d (isbits above).
func (d *DataType) IsBits() bool { return d.isbits() }

// countUnionComponents is count_union_components (jltypes.c:497-516).
func countUnionComponents(types []any, widen bool) int {
	c := 0
	for _, e := range types {
		for {
			u, ok := e.(*Union)
			if !ok {
				break
			}
			c += countUnionComponents([]any{u.A}, widen)
			e = u.B
		}
		if u, ok := UnwrapUnionAll(e).(*Union); widen && e != UnwrapUnionAll(e) && ok {
			c += countUnionComponents([]any{u.A, u.B}, widen)
		} else {
			c++
		}
	}
	return c
}

// flattenTypeUnion is flatten_type_union (jltypes.c:518-541).
func flattenTypeUnion(types []any, out *[]any, widen bool) {
	for _, e := range types {
		for {
			u, ok := e.(*Union)
			if !ok {
				break
			}
			flattenTypeUnion([]any{u.A}, out, widen)
			e = u.B
		}
		if u, ok := UnwrapUnionAll(e).(*Union); widen && e != UnwrapUnionAll(e) && ok {
			// julia rewraps every element collected so far, from index 0:
			// old_idx starts at 0, not at the index this union started at
			flattenTypeUnion([]any{u.A, u.B}, out, widen)
			for i := range *out {
				(*out)[i] = RewrapUnionAll((*out)[i], e)
			}
		} else {
			*out = append(*out, e)
		}
	}
}

// isortUnion is isort_union (jltypes.c:544-557).
func isortUnion(a []any) {
	for i := 1; i < len(a); i++ {
		x := a[i]
		j := i
		for ; j > 0; j-- {
			y := a[j-1]
			if !(unionSortCmp(x, y) < 0) {
				break
			}
			a[j] = y
		}
		a[j] = x
	}
}

func boolBit(b bool) int {
	if b {
		return 1
	}
	return 0
}

// simpleSubtype is simple_subtype (jltypes.c:559-595).
func simpleSubtype(a, b any, hasfree int, isUnion bool) bool {
	if a == any(Bottom) || b == any(AnyType) {
		return true
	}
	if Egal(a, b) {
		return true
	}
	if hasfree == 0 {
		mergeable := isUnion
		if !mergeable {
			mergeable = !(IsTypeType(a) && IsTypeType(b) &&
				TypeOf(a.(*DataType).TParam0()) != TypeOf(b.(*DataType).TParam0()))
		}
		return mergeable && Subtype(a, b)
	}
	if tv, ok := a.(*TypeVar); ok {
		na := tv.UB
		hasfree &= boolBit(HasFreeTypeVars(na)) | 2
		return simpleSubtype(na, b, hasfree, isUnion)
	}
	if tv, ok := b.(*TypeVar); ok {
		nb := tv.LB
		if isLeafBound(nb) {
			return false
		}
		hasfree &= (boolBit(HasFreeTypeVars(nb)) << 1) | 1
		return simpleSubtype(a, nb, hasfree, isUnion)
	}
	if b == any(DataTypeType) || b == any(TypeofBottomType) {
		return IsTypeType(a) && TypeOf(a.(*DataType).TParam0()) == b
	}
	return false
}

// mergeVarargUnions is merge_vararg_unions (jltypes.c:599-641).
func mergeVarargUnions(temp []any) {
	for i := len(temp) - 1; i > 0; i-- {
		tt, ok := temp[i].(*DataType)
		if !ok || tt.Name != TupleTypeName {
			continue
		}
		nfields := len(tt.Parameters)
		if nfields <= 1 {
			continue
		}
		va := tt.Parameters[nfields-1]
		if kindOfVararg(va) != varargUnbound {
			continue
		}
		t := unwrapVararg(va.(*Vararg))
		same := true
		for j := 0; j < nfields-1; j++ {
			if !Egal(tt.Parameters[j], t) {
				same = false
				break
			}
		}
		if !same {
			continue
		}
		minElements := nfields - 1
		for j := i - 1; j >= 0; j-- {
			ttj, ok := temp[j].(*DataType)
			if !ok || ttj.Name != TupleTypeName {
				break
			}
			nfieldsj := len(ttj.Parameters)
			if nfieldsj >= minElements {
				continue
			}
			if nfieldsj != minElements-1 {
				break
			}
			match := true
			for k := 0; k < nfieldsj; k++ {
				if !Egal(ttj.Parameters[k], t) {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			temp[j] = nil
			minElements--
		}
		if minElements == nfields-1 {
			continue
		}
		params := make([]any, minElements+1)
		for j := 0; j < minElements; j++ {
			params[j] = t
		}
		params[minElements] = va
		temp[i] = applyType(AnyTupleType, params)
	}
}

func buildUnion(temp []any) any {
	var tu any = Bottom
	for k := len(temp) - 1; k >= 0; k-- {
		if temp[k] != nil {
			if tu == any(Bottom) {
				tu = temp[k]
			} else {
				tu = &Union{temp[k], tu}
			}
		}
	}
	return tu
}

// TypeUnion is jl_type_union (jltypes.c:643-690): Union{ts...}.
func TypeUnion(ts ...any) any {
	if len(ts) == 0 {
		return Bottom
	}
	for _, pi := range ts {
		if !(IsType(pi) || isTypeVar(pi)) {
			throwf("TypeError: in Union, expected Type, got %s", Show(pi))
		}
	}
	if len(ts) == 1 {
		return ts[0]
	}
	temp := make([]any, 0, countUnionComponents(ts, true))
	flattenTypeUnion(ts, &temp, true)
	nt := len(temp)
	for i := 0; i < nt; i++ {
		hasFree := temp[i] != nil && HasFreeTypeVars(temp[i])
		for j := 0; j < nt; j++ {
			if j != i && temp[i] != nil && temp[j] != nil {
				hasFree2 := boolBit(hasFree) | boolBit(HasFreeTypeVars(temp[j]))<<1
				if simpleSubtype(temp[i], temp[j], hasFree2, true) {
					temp[i] = nil
				}
			}
		}
	}
	isortUnion(temp)
	mergeVarargUnions(temp)
	return buildUnion(temp)
}

// simpleSubtype2 is simple_subtype2 (jltypes.c:692-721).
func simpleSubtype2(a, b any, hasfree int, isUnion bool) (subab, subba bool) {
	switch {
	case Egal(a, b):
		return true, true
	case a == any(Bottom) || b == any(AnyType):
		return true, false
	case b == any(Bottom) || a == any(AnyType):
		return false, true
	case hasfree != 0:
		return simpleSubtype(a, b, hasfree, isUnion),
			simpleSubtype(b, a, ((hasfree&2)>>1)|((hasfree&1)<<1), isUnion)
	case IsTypeType(a) && IsTypeType(b) &&
		TypeOf(a.(*DataType).TParam0()) != TypeOf(b.(*DataType).TParam0()):
		return false, false
	case TypeOf(a) == TypeOf(b) && TypesEgal(a, b):
		return true, true
	}
	return Subtype(a, b), Subtype(b, a)
}

// simpleUnion is simple_union (jltypes.c:723-795).
func simpleUnion(a, b any) any {
	nta := countUnionComponents([]any{a}, true)
	temp := make([]any, 0, nta+countUnionComponents([]any{b}, true))
	flattenTypeUnion([]any{a}, &temp, true)
	nta = len(temp)
	flattenTypeUnion([]any{b}, &temp, true)
	nt := len(temp)
	count := nt
	ra, rb := nta, nt-nta
	// julia skips a NULL temp[i] here (jltypes.c:738); none can be: flatten
	// yields no NULL and only temp[i] itself is cleared, followed by break
	for i := 0; i < nta; i++ {
		hasFree := HasFreeTypeVars(temp[i])
		for j := nta; j < nt; j++ {
			if temp[j] == nil {
				continue
			}
			hasFree2 := boolBit(hasFree) | boolBit(HasFreeTypeVars(temp[j]))<<1
			subab, subba := simpleSubtype2(temp[i], temp[j], hasFree2, false)
			if subab {
				temp[i] = nil
				if !subba {
					ra = 0
				}
				count--
				break
			} else if subba {
				temp[j] = nil
				rb = 0
				count--
			}
		}
	}
	if count == ra {
		return a
	}
	if count == rb {
		return b
	}
	for i := 0; i < nt; i++ {
		hasFree := temp[i] != nil && HasFreeTypeVars(temp[i])
		jmin, jmax := 0, nta
		if i >= nta {
			jmin, jmax = nta, nt
		}
		for j := jmin; j < jmax; j++ {
			if j != i && temp[i] != nil && temp[j] != nil {
				hasFree2 := boolBit(hasFree) | boolBit(HasFreeTypeVars(temp[j]))<<1
				if simpleSubtype(temp[i], temp[j], hasFree2, false) {
					temp[i] = nil
				}
			}
		}
	}
	isortUnion(temp)
	mergeVarargUnions(temp)
	return buildUnion(temp)
}

// hasEmptyIntersection stands for jl_has_empty_intersection, which needs
// type intersection (subtype.c:2533-4437), not ported yet. It answers with
// obviously_disjoint, which is exact when it says "disjoint" and
// conservative otherwise; simple_intersect then keeps an element julia might
// drop, which over-estimates a variable's upper bound in the subtype path.
// That path already over-estimates by design (subtype.c:654-656), and the
// differential test measures what it costs.
func hasEmptyIntersection(a, b any) bool { return obviouslyDisjoint(a, b, false) }

// simpleIntersect is simple_intersect (jltypes.c:799-903).
func simpleIntersect(a, b any, overesi bool) any {
	temp := make([]any, 0, countUnionComponents([]any{a}, false)+countUnionComponents([]any{b}, false))
	flattenTypeUnion([]any{a}, &temp, false)
	nta := len(temp)
	flattenTypeUnion([]any{b}, &temp, false)
	nt := len(temp)
	stemp := make([]int8, nt)
	for i := 0; i < nta; i++ {
		hasfree := HasFreeTypeVars(temp[i])
		for j := nta; j < nt; j++ {
			if stemp[i] == 0 || stemp[j] == 0 {
				intersect := !hasfree && !HasFreeTypeVars(temp[j])
				var disjoint bool
				if intersect {
					disjoint = hasEmptyIntersection(temp[i], temp[j])
				} else {
					disjoint = obviouslyDisjoint(temp[i], temp[j], false)
				}
				if !disjoint {
					stemp[i], stemp[j] = 1, 1
				}
			}
		}
	}
	for i := 0; i < nt; i++ {
		if stemp[i] == 0 {
			temp[i] = nil
		}
	}
	for i := range stemp {
		stemp[i] = 0
	}
	allDisjoint := true
	subs := [2]bool{true, true}
	rs := [2]bool{true, true}
	for i := 0; i < nta; i++ {
		if temp[i] == nil {
			continue
		}
		allDisjoint = false
		hasFree := HasFreeTypeVars(temp[i])
		for j := nta; j < nt; j++ {
			if temp[j] == nil {
				continue
			}
			hasFree2 := boolBit(hasFree) | boolBit(HasFreeTypeVars(temp[j]))<<1
			subab, subba := simpleSubtype2(temp[i], temp[j], hasFree2, false)
			if subba && !subab {
				stemp[i] = -1
				if stemp[j] >= 0 {
					stemp[j] = 2
				}
			} else if subab && !subba {
				stemp[j] = -1
				if stemp[i] >= 0 {
					stemp[i] = 2
				}
			} else if subab || subba {
				if stemp[i] == 0 {
					stemp[i] = 1
				}
				if stemp[j] == 0 {
					stemp[j] = 1
				}
			}
		}
	}
	if !allDisjoint {
		for i := 0; i < nt; i++ {
			side := boolBit(i >= nta)
			subs[side] = subs[side] && (temp[i] == nil || stemp[i] > 0)
			rs[side] = rs[side] && (temp[i] != nil && stemp[i] > 0)
		}
		if rs[0] {
			return a
		}
		if rs[1] {
			return b
		}
	}
	if allDisjoint || (!overesi && !subs[0] && !subs[1]) {
		return Bottom
	}
	lo, hi := 0, nt
	switch {
	case subs[0]:
		lo, hi = 0, nta
	case subs[1]:
		lo, hi = nta, nt
	}
	if !subs[0] && !subs[1] {
		for j := 0; j < nt; j++ {
			limit := int8(0)
			if j < nta {
				limit = 2
			}
			if stemp[j] < limit {
				temp[j] = nil
			}
		}
	}
	part := temp[lo:hi]
	isortUnion(part)
	return buildUnion(part)
}
