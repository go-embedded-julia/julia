package core

// WidenDiagonal is jl_widen_diagonal (subtype.c:4903-4906): t with the
// diagonal type variables of ua, those occurring only covariantly and more
// than once, made non-diagonal, as the Compiler asks it of a method's
// signature.
func WidenDiagonal(t any, ua *UnionAll) any {
	return widenDiagonal(t, ua, nil)
}

// widenDiagonal is widen_diagonal (subtype.c:4884-4901).
func widenDiagonal(t any, u *UnionAll, troot *varBinding) any {
	inner := []*TypeVar{}
	vb := &varBinding{v: u.Var, right: true, innervars: &inner, prev: troot}
	var nt any
	if body, ok := u.Body.(*UnionAll); ok {
		nt = widenDiagonal(t, body, vb)
	} else {
		nt = widenDiagonalRoot(t, vb)
	}
	for _, v := range inner {
		nt = TypeUnionAll(v, nt)
	}
	return nt
}

// widenDiagonalRoot is _widen_diagonal (subtype.c:4874-4882).
func widenDiagonalRoot(t any, troot *varBinding) any {
	checkDiagonal(t, troot, 0)
	anyConcrete := false
	for v := troot; v != nil; v = v.prev {
		anyConcrete = anyConcrete || (v.occursCov > 1 && v.occursInv == 0)
	}
	if !anyConcrete {
		return t // no diagonal
	}
	return insertNondiagonal(t, troot, 0)
}

// checkDiagonal is check_diagonal (subtype.c:4703-4770): the occurrences
// of troot's variables in t, covariant (param 1) or invariant (param 2).
func checkDiagonal(t any, troot *varBinding, param int) {
	switch x := t.(type) {
	case *Union:
		var saved []int8
		for v := troot; v != nil; v = v.prev {
			saved = append(saved, v.occursInv|v.occursCov<<2)
		}
		checkDiagonal(x.A, troot, param)
		i := 0
		for v := troot; v != nil; v = v.prev {
			inv, cov := saved[i]&3, saved[i]>>2
			saved[i] = v.occursInv | v.occursCov<<2
			v.occursInv, v.occursCov = inv, cov
			i++
		}
		checkDiagonal(x.B, troot, param)
		i = 0
		for v := troot; v != nil; v = v.prev {
			if v.occursInv < saved[i]&3 {
				v.occursInv = saved[i] & 3
			}
			if v.occursCov < saved[i]>>2 {
				v.occursCov = saved[i] >> 2
			}
			i++
		}
	case *UnionAll:
		// the variable it binds is not troot's while in its body
		v1, v2 := troot, troot.prev
		for v2 != nil {
			if v2.v == x.Var {
				v1.prev = v2.prev
				break
			}
			v1, v2 = v2, v2.prev
		}
		checkDiagonal(x.Body, troot, param)
		v1.prev = v2
	case *DataType:
		nparam := 2
		if IsTupleType(x) {
			nparam = 1
		}
		nparam = max(nparam, param)
		for _, p := range x.Parameters {
			checkDiagonal(p, troot, nparam)
		}
	case *Vararg:
		tt := unwrapVararg(x)
		n := int64(2)
		if k, ok := x.N.(int64); ok {
			n = k
		}
		if n > 0 {
			checkDiagonal(tt, troot, param)
		}
		if n > 1 {
			checkDiagonal(tt, troot, param)
		}
		if x.N != nil {
			checkDiagonal(x.N, troot, 2)
		}
	case *TypeVar:
		v := troot
		for ; v != nil; v = v.prev {
			if v.v == x {
				if param == 1 && v.occursCov < 2 {
					v.occursCov++
				}
				if param == 2 && v.occursInv < 2 {
					v.occursInv++
				}
				break
			}
		}
		if v == nil {
			checkDiagonal(x.UB, troot, 0)
		}
	}
}

// insertNondiagonal is insert_nondiagonal (subtype.c:4772-4872).
func insertNondiagonal(t any, troot *varBinding, widen2ub int) any {
	switch x := t.(type) {
	case *TypeVar:
		concretekind := int8(1)
		if widen2ub > 1 {
			concretekind = 0
		}
		v := troot
		for ; v != nil; v = v.prev {
			if v.occursInv == 0 && v.occursCov > concretekind && v.v == x {
				break
			}
		}
		if v != nil {
			if widen2ub != 0 {
				return insertNondiagonal(x.UB, troot, 2)
			}
			// each covariant occurrence gets its own newvar <: var (the
			// diagonal rule)
			nv := &TypeVar{Name: v.v.Name, LB: v.v.LB, UB: v.v}
			*v.innervars = append(*v.innervars, nv)
			return nv
		}
	case *UnionAll:
		body, tv := x.Body, x.Var
		v := troot
		for ; v != nil; v = v.prev {
			if v.v == tv {
				break
			}
		}
		if v != nil {
			v.v = nil // the body's own binding, not troot's
		}
		newbody := insertNondiagonal(body, troot, widen2ub)
		if v != nil {
			v.v = tv
		}
		if body == newbody || HasTypeVar(newbody, tv) {
			if body != newbody {
				t = &UnionAll{Var: tv, Body: newbody}
			}
			// the lower bound is not widened, the wrong direction
			newub := insertNondiagonal(tv.UB, troot, widen2ub)
			if newub != tv.UB {
				nv := &TypeVar{Name: tv.Name, LB: tv.LB, UB: newub}
				t = TypeUnionAll(nv, applyType(t, []any{nv}))
			}
			return t
		}
		return newbody
	case *Union:
		na := insertNondiagonal(x.A, troot, widen2ub)
		nb := insertNondiagonal(x.B, troot, widen2ub)
		if na != x.A || nb != x.B {
			return simpleUnion(na, nb)
		}
	case *Vararg:
		// a Vararg's variable is widened to its upper bound, else it stays
		// diagonal
		tt := unwrapVararg(x)
		if widen2ub == 0 {
			if k, ok := x.N.(int64); !ok || k > 1 {
				widen2ub = 1
			}
		}
		if nt := insertNondiagonal(tt, troot, widen2ub); nt != tt {
			return wrapVararg(nt, x.N, false, 0)
		}
	case *DataType:
		if IsTupleType(x) {
			var params []any
			for i, elt := range x.Parameters {
				if ne := insertNondiagonal(elt, troot, widen2ub); ne != elt {
					if params == nil {
						params = append([]any(nil), x.Parameters...)
					}
					params[i] = ne
				}
			}
			if params != nil {
				return applyType(AnyTupleType, params)
			}
		}
	}
	return t
}
