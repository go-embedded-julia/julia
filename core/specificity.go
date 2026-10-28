package core

// Method specificity: a port of jl_type_morespecific and its helpers,
// src/subtype.c:4909-5552 of julia 1.12.6. Dispatch picks, among the
// methods whose signatures match a call, the most specific one.
//
// The environment maps each type variable entered so far to the number of
// times it occurs in its body (count_occurs), as julia stores it in the
// val field of jl_typeenv_t.

// countMissingWrap is count_missing_wrap (subtype.c:4909-4935).
func countMissingWrap(x any, env *typeEnv) int {
	if !HasFreeTypeVars(x) {
		return 0
	}
	var wrapped *typeEnv
	count := 0
	for env2 := env; env2 != nil; env2 = env2.prev {
		needWrap := 0
		for env3 := wrapped; env3 != nil && needWrap == 0; env3 = env3.prev {
			if env3.v == env2.v {
				needWrap = -1
			} else if HasTypeVar(env3.v.LB, env2.v) || HasTypeVar(env3.v.UB, env2.v) {
				needWrap = 1
			}
		}
		switch needWrap {
		case 0:
			needWrap = boolBit(HasTypeVar(x, env2.v))
		case -1:
			needWrap = 0
		}
		if needWrap != 0 {
			count++
			wrapped = &typeEnv{env2.v, nil, wrapped}
		}
	}
	return count
}

// obviousSubtypeMsp is obvious_subtype_msp (subtype.c:4937-4958).
func obviousSubtypeMsp(x, y, y0 any, sub *bool, wrapx, wrapy int) bool {
	if wrapx != 0 || wrapy != 0 {
		n := wrapx - wrapy
		for ; n > 0; n-- {
			u, ok := y.(*UnionAll)
			if !ok {
				break
			}
			y = u.Body
		}
		for ; n < 0; n++ {
			u, ok := x.(*UnionAll)
			if !ok {
				break
			}
			x = u.Body
		}
		if n > 0 {
			return obviousSubtype(UnwrapUnionAll(x), y, y0, sub) && !*sub
		}
	}
	return obviousSubtype(x, y, y0, sub)
}

// rewrapEnv wraps t in a UnionAll for every variable of env, innermost
// first (the loops of eq_msp and sub_msp).
func rewrapEnv(t any, env *typeEnv, onlyTypes bool) any {
	for ; env != nil; env = env.prev {
		if !onlyTypes || IsType(t) || isTypeVar(t) {
			t = TypeUnionAll(env.v, t)
		}
	}
	return t
}

// eqMsp is eq_msp (subtype.c:4960-5053), release build.
func eqMsp(a, b, a0, b0 any, env *typeEnv) bool {
	if !(IsType(a) || isTypeVar(a)) || !(IsType(b) || isTypeVar(b)) {
		return Egal(a, b)
	}
	if a == b {
		return true
	}
	if TypeOf(a) == TypeOf(b) && TypesEgal(a, b) {
		return true
	}
	if obviouslyUnequal(a, b) {
		return false
	}
	if _, ok := a.(*DataType); ok && !IsConcrete(b) {
		a, b = b, a
	}
	wrapa, wrapb := countMissingWrap(a, env), countMissingWrap(b, env)
	ab, abKnown := false, false
	if b == any(AnyType) || a == any(Bottom) {
		ab, abKnown = true, true
	} else if obviousSubtypeMsp(a, b, b0, &ab, wrapa, wrapb) {
		if !ab {
			return false
		}
		abKnown = true
	}
	ba, baKnown := false, false
	if a == any(AnyType) || b == any(Bottom) {
		ba, baKnown = true, true
	} else if obviousSubtypeMsp(b, a, a0, &ba, wrapb, wrapa) {
		if !ba {
			return false
		}
		baKnown = true
	}
	a, b = rewrapEnv(a, env, false), rewrapEnv(b, env, false)
	if !abKnown && !(&stenv{}).forallExistsSubtype(a, b, 0) {
		return false
	}
	if !baKnown {
		ba = (&stenv{}).forallExistsSubtype(b, a, 0)
	}
	return ba
}

// subMsp is sub_msp (subtype.c:5055-5094), release build.
func subMsp(x, y, y0 any, env *typeEnv) bool {
	if y == any(AnyType) || x == any(Bottom) {
		return true
	}
	_, yua := y.(*UnionAll)
	_, yu := y.(*Union)
	if x == y || (TypeOf(x) == TypeOf(y) && (yua || yu) && TypesEgal(x, y)) {
		return true
	}
	var sub bool
	if obviousSubtypeMsp(x, y, y0, &sub, countMissingWrap(x, env), countMissingWrap(y, env)) {
		return sub
	}
	x, y = rewrapEnv(x, env, true), rewrapEnv(y, env, true)
	return (&stenv{}).forallExistsSubtype(x, y, 0)
}

// nthTupleElt is nth_tuple_elt (subtype.c:5100-5117); nil when the tuple
// has no i-th element.
func nthTupleElt(t *DataType, i int) any {
	n := len(t.Parameters)
	if n == 0 {
		return nil
	}
	if i < n-1 {
		return t.Parameters[i]
	}
	if last, ok := UnwrapUnionAll(t.Parameters[n-1]).(*Vararg); ok {
		if k, ok := last.N.(int64); ok && int64(i) >= int64(n-1)+k {
			return nil
		}
		return unwrapVararg(last)
	}
	if i == n-1 {
		return t.Parameters[i]
	}
	return nil
}

// numOccurs is num_occurs (subtype.c:5267-5275).
func numOccurs(v *TypeVar, env *typeEnv) int {
	for ; env != nil; env = env.prev {
		if env.v == v {
			n, _ := env.val.(int)
			return n
		}
	}
	return 0
}

// countOccurs is count_occurs (subtype.c:5238-5265).
func countOccurs(t any, v *TypeVar) int {
	if t == any(v) {
		return 1
	}
	switch x := t.(type) {
	case *Union:
		return max(countOccurs(x.A, v), countOccurs(x.B, v))
	case *UnionAll:
		if x.Var == v {
			return 0
		}
		return countOccurs(x.Body, v)
	case *Vararg:
		if x.T != nil {
			n := countOccurs(x.T, v)
			if x.N != nil {
				n += countOccurs(x.N, v)
			}
			return n
		}
	case *DataType:
		c := 0
		for _, p := range x.Parameters {
			c += countOccurs(p, v)
		}
		return c
	}
	return 0
}

// tupleMorespecific is tuple_morespecific (subtype.c:5119-5190).
func tupleMorespecific(cdt, pdt *DataType, c0, p0 any, invariant bool, env *typeEnv) bool {
	plen := len(pdt.Parameters)
	if plen == 0 {
		return false
	}
	clen := len(cdt.Parameters)
	if clen == 0 {
		return true
	}
	clast := cdt.Parameters[clen-1]
	ckind := kindOfVararg(clast)
	cva := ckind > varargInt
	pva := kindOfVararg(pdt.Parameters[plen-1]) > varargInt
	cdiag, pdiag, someMorespecific := false, false, false
	for i := 0; ; i++ {
		if cva && pva && i >= clen && i >= plen {
			break
		}
		ce, pe := nthTupleElt(cdt, i), nthTupleElt(pdt, i)
		if ce == nil {
			if pe == nil {
				break
			}
			return true
		}
		if pe == nil {
			if !cva && !someMorespecific {
				return false
			}
			break
		}
		if typeMorespecific(pe, ce, p0, c0, invariant, env) {
			return false
		}
		if cv, ok := ce.(*TypeVar); ok && !cdiag && numOccurs(cv, env) > 1 {
			cdiag = true
		}
		if pv, ok := pe.(*TypeVar); ok && !pdiag && numOccurs(pv, env) > 1 {
			pdiag = true
		}
		if cva && pva && i >= clen-1 && i >= plen-1 && (someMorespecific || (cdiag && !pdiag)) {
			return true
		}
		cms := typeMorespecific(ce, pe, c0, p0, invariant, env)
		if !cms && !subMsp(ce, pe, p0, env) {
			// a bound vararg tuple can be more specific despite disjoint
			// elements, to keep transitivity (subtype.c:5166-5173)
			if !someMorespecific || !cva || ckind != varargBound {
				return false
			}
			return numOccurs(clast.(*Vararg).N.(*TypeVar), env) > 1
		}
		if !cms && i == clen-1 && clen == plen && !cva && pva && eqMsp(ce, pe, c0, p0, env) &&
			isTypeVar(ce) && isTypeVar(pe) && !cdiag && pdiag {
			return false
		}
		if cms {
			someMorespecific = true
		}
	}
	if cva && pva && clen > plen && (!pdiag || cdiag) {
		return true
	}
	if cva && !pva && !someMorespecific {
		return false
	}
	return someMorespecific || (cdiag && !pdiag)
}

// tupleFullLength is tuple_full_length (subtype.c:5192-5203).
func tupleFullLength(t *DataType) int {
	n := len(t.Parameters)
	if n == 0 {
		return 0
	}
	if last, ok := UnwrapUnionAll(t.Parameters[n-1]).(*Vararg); ok {
		if k, ok := last.N.(int64); ok {
			n += int(k) - 1
		}
	}
	return n
}

// argsMorespecificFix1 is args_morespecific_fix1 (subtype.c:5207-5236):
// a is a bound-vararg tuple, b is not; fix a's length to b's when that
// changes one of a's earlier arguments. -1 means "no decision".
func argsMorespecificFix1(a, b *DataType, a0, b0 any, swap bool, env *typeEnv) int {
	n := len(a.Parameters)
	taillen := tupleFullLength(b) - n + 1
	if taillen <= 0 {
		return -1
	}
	N := UnwrapUnionAll(a.Parameters[n-1]).(*Vararg).N.(*TypeVar)
	// instantiating a tuple type gives a tuple type
	newA := instType(a, &typeEnv{N, int64(taillen), nil}, nil, true).(*DataType)
	changed := false
	for i := 0; i < n-1 && i < len(newA.Parameters); i++ {
		if a.Parameters[i] != newA.Parameters[i] {
			changed = true
			break
		}
	}
	if !changed {
		return -1
	}
	switch {
	case eqMsp(b, newA, b0, a0, env):
		return boolBit(swap)
	case swap:
		return boolBit(typeMorespecific(b, newA, b0, a0, false, env))
	}
	return boolBit(typeMorespecific(newA, b, a0, b0, false, env))
}

// tupleCmpTypeofBottom is tuple_cmp_typeofbottom (subtype.c:5277-5290).
func tupleCmpTypeofBottom(a, b *DataType) int {
	la, lb := len(a.Parameters), len(b.Parameters)
	for i := 0; i < la || i < lb; i++ {
		var pa, pb any
		if i < la {
			pa = a.Parameters[i]
		}
		if i < lb {
			pb = b.Parameters[i]
		}
		xa := boolBit(pa == any(TypeofBottomType) || pa == any(TypeofBottomType.Super))
		xb := boolBit(pb == any(TypeofBottomType) || pb == any(TypeofBottomType.Super))
		if xa != xb {
			return xa - xb
		}
	}
	return 0
}

// vaTupleKind is jl_va_tuple_kind (julia_internal.h:1180-1188).
func vaTupleKind(t *DataType) varargKind {
	if len(t.Parameters) == 0 {
		return varargNone
	}
	return kindOfVararg(t.Parameters[len(t.Parameters)-1])
}

// enterUnionAll is HANDLE_UNIONALL_A/B (subtype.c:5293-5303).
func enterUnionAll(u *UnionAll, env *typeEnv) *typeEnv {
	return &typeEnv{u.Var, countOccurs(u.Body, u.Var), env}
}

// typeMorespecific is type_morespecific_ (subtype.c:5305-5514).
func typeMorespecific(a, b, a0, b0 any, invariant bool, env *typeEnv) bool {
	if a == b {
		return false
	}
	ad, aIsDT := a.(*DataType)
	bd, bIsDT := b.(*DataType)
	if aIsDT && bIsDT && ad.Name == TupleTypeName && bd.Name == TupleTypeName {
		if msp := tupleCmpTypeofBottom(ad, bd); msp != 0 {
			return msp > 0
		}
		akind, bkind := vaTupleKind(ad), vaTupleKind(bd)
		if akind == varargBound && bkind < varargBound {
			if argsMorespecificFix1(ad, bd, a0, b0, false, env) == 1 {
				return true
			}
		}
		if bkind == varargBound && akind < varargBound {
			if argsMorespecificFix1(bd, ad, b0, a0, true, env) == 0 {
				return false
			}
		}
		return tupleMorespecific(ad, bd, a0, b0, invariant, env)
	}
	if !invariant {
		if a == any(AnyType) {
			return false
		}
		if b == any(AnyType) && !isTypeVar(a) {
			return true
		}
	}
	if u, ok := a.(*Union); ok {
		if ub, ok := b.(*UnionAll); ok {
			return typeMorespecific(a, ub.Body, a0, b0, invariant, enterUnionAll(ub, env))
		}
		if subMsp(b, a, a0, env) {
			return false
		}
		if typeMorespecific(u.A, b, a0, b0, invariant, env) || typeMorespecific(u.B, b, a0, b0, invariant, env) {
			if v, ok := b.(*Union); ok {
				if typeMorespecific(v.A, a, b0, a0, invariant, env) || typeMorespecific(v.B, a, b0, a0, invariant, env) {
					return false
				}
			}
			return true
		}
		return false
	}
	if IsTypeType(a) && !invariant {
		if b == any(TypeofBottomType) {
			return false
		}
		switch tp0a := ad.TParam0().(type) {
		case *TypeVar:
			if IsKind(b) && !subMsp(AnyType, tp0a.UB, b0, env) {
				return true
			}
		case *bottomType:
			if subMsp(b, TypeType, TypeType, env) {
				return true
			}
		default:
			if b == any(DataTypeType) || b == any(UnionAllType) || b == any(UnionType) {
				return true
			}
		}
	}
	if u, ok := b.(*Union); ok {
		if ua, ok := a.(*UnionAll); ok {
			return typeMorespecific(ua.Body, b, a0, b0, invariant, enterUnionAll(ua, env))
		}
		if typeMorespecific(a, u.A, a0, b0, invariant, env) || typeMorespecific(a, u.B, a0, b0, invariant, env) {
			return !typeMorespecific(b, a, b0, a0, invariant, env)
		}
		return false
	}
	if aIsDT && bIsDT {
		return datatypeMorespecific(ad, bd, a0, b0, env)
	}
	if av, ok := a.(*TypeVar); ok {
		if bv, ok := b.(*TypeVar); ok {
			return (typeMorespecific(av.UB, bv.UB, a0, b0, false, env) &&
				!typeMorespecific(av.LB, bv.LB, a0, b0, false, env)) ||
				(typeMorespecific(bv.LB, av.LB, b0, a0, false, env) &&
					!typeMorespecific(bv.UB, av.UB, b0, a0, false, env))
		}
		if !IsType(b) {
			return false
		}
		if invariant {
			if av.UB == any(Bottom) {
				return true
			}
			if !HasFreeTypeVars(b) {
				return false
			}
			if eqMsp(av.UB, b, a0, b0, env) {
				return numOccurs(av, env) >= 2
			}
		} else if b == any(AnyType) && av.UB == any(AnyType) && numOccurs(av, env) >= 2 {
			// {T,T} where T must be more specific than {Any,Any}
			return true
		}
		return typeMorespecific(av.UB, b, a0, b0, false, env)
	}
	if bv, ok := b.(*TypeVar); ok {
		if !IsType(a) {
			return true
		}
		if invariant {
			if bv.UB == any(Bottom) {
				return false
			}
			if HasFreeTypeVars(a) {
				if typeMorespecific(a, bv.UB, a0, b0, false, env) {
					return true
				}
				if eqMsp(a, bv.UB, a0, b0, env) {
					return numOccurs(bv, env) < 2
				}
				return false
			}
			if obviouslyDisjoint(a, bv.UB, true) {
				return false
			}
			return !typeMorespecific(bv.UB, a, b0, a0, false, env)
		}
		return typeMorespecific(a, bv.UB, a0, b0, false, env)
	}
	if ua, ok := a.(*UnionAll); ok {
		return typeMorespecific(ua.Body, b, a0, b0, invariant, enterUnionAll(ua, env))
	}
	if ub, ok := b.(*UnionAll); ok {
		return typeMorespecific(a, ub.Body, a0, b0, invariant, enterUnionAll(ub, env))
	}
	return false
}

// datatypeMorespecific is the DataType-against-DataType case of
// type_morespecific_ (subtype.c:5388-5451).
func datatypeMorespecific(tta, ttb *DataType, a0, b0 any, env *typeEnv) bool {
	if tta == TypeofBottomType && (IsKind(ttb) || IsTypeType(ttb)) {
		return true
	}
	super := false
	for ; tta != AnyType; tta, super = tta.Super, true {
		if tta.Name != ttb.Name {
			continue
		}
		if super {
			if tta.Name != TypeTypeName {
				return true
			}
			if tp0, ok := ttb.TParam0().(*TypeVar); ok && subMsp(AnyType, tp0.UB, b0, env) {
				return true
			}
		}
		var ascore, bscore, ascore1, bscore1, adiag, bdiag int
		for i, apara := range tta.Parameters {
			bpara := ttb.Parameters[i]
			afree, bfree := HasFreeTypeVars(apara), HasFreeTypeVars(bpara)
			if !afree && !bfree && !TypesEqual(apara, bpara) {
				return false
			}
			switch {
			case typeMorespecific(apara, bpara, a0, b0, true, env) && (isTypeVar(apara) || !afree || bfree):
				ascore++
			case typeMorespecific(bpara, apara, b0, a0, true, env) && (isTypeVar(bpara) || !bfree || afree):
				bscore++
			case eqMsp(apara, bpara, a0, b0, env):
				if !afree && bfree {
					ascore++
				} else if afree && !bfree {
					bscore++
				}
			}
			if isTypeVar(bpara) && !isTypeVar(apara) && !IsType(apara) {
				ascore1 = 1
			} else if isTypeVar(apara) && !isTypeVar(bpara) && !IsType(bpara) {
				bscore1 = 1
			}
			if av, ok := apara.(*TypeVar); ok && adiag == 0 {
				for _, later := range tta.Parameters[i+1:] {
					if HasTypeVar(later, av) {
						adiag = 1
						break
					}
				}
			}
			if bv, ok := bpara.(*TypeVar); ok && bdiag == 0 {
				for _, later := range ttb.Parameters[i+1:] {
					if HasTypeVar(later, bv) {
						bdiag = 1
						break
					}
				}
			}
		}
		if ascore1 > bscore1 {
			return true
		}
		if bscore1 > ascore1 || bscore > ascore || bdiag > adiag {
			return false
		}
		return ascore > bscore || adiag > bdiag
	}
	return false
}

// MoreSpecific is jl_type_morespecific (subtype.c:5516-5527): a is more
// specific than b.
func MoreSpecific(a, b any) (more bool, err error) {
	defer catchError(&err)
	if obviouslyDisjoint(a, b, true) {
		return false, nil
	}
	if HasFreeTypeVars(a) || HasFreeTypeVars(b) {
		return false, nil
	}
	if Subtype(b, a) {
		return false, nil
	}
	if Subtype(a, b) {
		return true, nil
	}
	return typeMorespecific(a, b, a, b, false, nil), nil
}
