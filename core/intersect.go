package core

// Type intersection: a statement-by-statement port of src/subtype.c
// (julia 1.12.6, lines 2533-4700). It runs in subtyping's environment
// (subtype.go) with intersection and ignore_free set, and calls back into
// the subtype path, whose intersection-only branches are ported there.

// intersectAside is intersect_aside (subtype.c:2538-2574): intersection in
// a nested union environment.
func (e *stenv) intersectAside(x, y any, depth int) any {
	// band-aid for #30335
	if x == any(AnyType) && !isTypeVar(y) {
		return y
	}
	if y == any(AnyType) && !isTypeVar(x) {
		return x
	}
	// band-aid for #46736 #56040
	if obviouslyInUnion(x, y) {
		return y
	}
	if obviouslyInUnion(y, x) {
		return x
	}
	var vars, bbprev, xb, yb *varBinding
	xs, ys := x, y
	if xv, ok := x.(*TypeVar); ok {
		xs = xv.UB
		if xb = e.lookup(xv); xb != nil {
			xs = xb.ub
		}
	}
	if yv, ok := y.(*TypeVar); ok {
		ys = yv.UB
		if yb = e.lookup(yv); yb != nil {
			ys = yb.ub
		}
	}
	if !HasFreeTypeVars(xs) && !HasFreeTypeVars(ys) && (xb == nil || yb == nil) {
		vars = e.vars
		e.vars = xb
		if xb == nil {
			e.vars = yb
		}
		if e.vars != nil {
			bbprev = e.vars.prev
			e.vars.prev = nil
		}
	}
	old := pushUnionState(&e.runions)
	savedepth := e.invdepth
	e.invdepth = depth
	res := e.intersectAll(x, y)
	e.invdepth = savedepth
	popUnionState(&e.runions, old)
	if bbprev != nil {
		e.vars.prev = bbprev
	}
	if vars != nil {
		e.vars = vars
	}
	return res
}

// intersectUnion is intersect_union (subtype.c:2576-2599).
func (e *stenv) intersectUnion(x any, u *Union, R bool, param int) any {
	noFree := !HasFreeTypeVars(x) && !HasFreeTypeVars(u)
	if param == 2 || noFree {
		var vars *varBinding
		if noFree {
			vars = e.vars
			e.vars = nil
		}
		old := pushUnionState(&e.runions)
		var a, b any
		if R {
			a = e.intersectAll(x, u.A)
			b = e.intersectAll(x, u.B)
		} else {
			a = e.intersectAll(u.A, x)
			b = e.intersectAll(u.B, x)
		}
		popUnionState(&e.runions, old)
		if vars != nil {
			e.vars = vars
		}
		return simpleJoin(a, b)
	}
	// try all possible choices in covariant position; union them all
	// together at the top level
	choice := e.pickUnionElement(u, true)
	if R {
		return e.intersect(x, choice, param)
	}
	return e.intersect(choice, x, param)
}

// setVarToConst is set_var_to_const (subtype.c:2602-2629): a variable set
// to a non-type constant.
func (e *stenv) setVarToConst(bb *varBinding, v any, R bool) any {
	offset := e.loffset
	if R {
		offset = -offset
	}
	vi, vlong := v.(int64)
	lbi, lblong := bb.lb.(int64)
	switch {
	case bb.lb == any(Bottom) && bb.ub == any(AnyType):
		if offset == 0 {
			bb.lb, bb.ub = v, v
		} else if vlong {
			v = vi + offset
			bb.lb, bb.ub = v, v
			// the shorter Vararg's length is returned
			if offset > 0 {
				return vi
			}
		} else {
			return Bottom
		}
	case vlong && lblong:
		if vi+offset != lbi {
			return Bottom
		}
		if offset < 0 {
			return bb.lb
		}
	case !Egal(v, bb.lb):
		return Bottom
	}
	return v
}

// boundVarBelow is bound_var_below (subtype.c:2631-2660).
func (e *stenv) boundVarBelow(tv *TypeVar, bb *varBinding, R bool) any {
	if bb == nil {
		return tv
	}
	if bb.depth0 != e.invdepth {
		return Bottom
	}
	e.invdepth++
	e.recordVarOccurrence(bb, 2)
	e.invdepth--
	offset := e.loffset
	if R {
		offset = -offset
	}
	if blb, ok := bb.lb.(int64); ok {
		if blb < offset || blb < 0 {
			return Bottom
		}
		// the shorter Vararg's length is returned
		if offset <= 0 {
			return bb.lb
		}
		return blb - offset
	}
	if offset > 0 {
		if bb.innervars == nil {
			bb.innervars = &[]*TypeVar{}
		}
		ntv := &TypeVar{Name: tv.Name, LB: Bottom, UB: AnyType}
		*bb.innervars = append(*bb.innervars, ntv)
		return ntv
	}
	return tv
}

// trySubtypeByBounds is try_subtype_by_bounds (subtype.c:2665-2683).
func (e *stenv) trySubtypeByBounds(a, b any) bool {
	if u, ok := a.(*Union); ok {
		return e.trySubtypeByBounds(u.A, b) && e.trySubtypeByBounds(u.B, b)
	}
	if u, ok := b.(*Union); ok {
		return e.trySubtypeByBounds(a, u.A) || e.trySubtypeByBounds(a, u.B)
	}
	if a == any(Bottom) || b == any(AnyType) || obviouslyEgal(a, b) {
		return true
	}
	bv, ok := b.(*TypeVar)
	if !ok {
		return false
	}
	if isTypeVar(a) && e.subtypeByBounds(a, b) {
		return true
	}
	// check if `Union{a, ...} <: b`
	blb := bv.LB
	if vb := e.lookup(bv); vb != nil {
		blb = vb.lb
	}
	return obviouslyInUnion(a, blb)
}

// trySubtypeInEnv is try_subtype_in_env (subtype.c:2685-2695).
func (e *stenv) trySubtypeInEnv(a, b any) bool {
	if e.trySubtypeByBounds(a, b) {
		return true
	}
	se := e.newSavedEnv()
	e.saveEnv(se)
	ret := e.subtypeInEnv(a, b)
	e.restoreEnv(se)
	e.freeSavedEnv(se)
	return ret
}

// setBound is set_bound (subtype.c:2697-2709).
func (e *stenv) setBound(bound *any, val any, v *TypeVar) {
	if inUnion(val, v) {
		return
	}
	for b := e.vars; b != nil; b = b.prev {
		if (b.lb == any(v) || b.ub == any(v)) && inUnion(val, b.v) {
			return
		}
	}
	*bound = val
}

// subtypeInEnv is subtype_in_env (subtype.c:2299-2312): a subtype query in
// a fresh environment over the same variables.
func (e *stenv) subtypeInEnv(x, y any) bool {
	e2 := getStenv(e.envout)
	defer putStenv(e2)
	e2.vars, e2.intersection, e2.ignoreFree = e.vars, e.intersection, e.ignoreFree
	e2.invdepth, e2.envidx, e2.loffset = e.invdepth, e.envidx, e.loffset
	e2.calls = e.calls
	sub := e2.forallExistsSubtype(x, y, 0)
	e.calls = e2.calls
	return sub
}

// subtypeInEnvExistential is subtype_in_env_existential
// (subtype.c:2712-2731): subtyping with every variable existential.
func (e *stenv) subtypeInEnvExistential(x, y any) bool {
	if x == any(Bottom) || y == any(AnyType) || obviouslyInUnion(y, x) {
		return true
	}
	var rs []bool
	for v := e.vars; v != nil; v = v.prev {
		rs = append(rs, v.right)
		v.right = true
	}
	issub := e.subtypeInEnv(x, y)
	n := 0
	for v := e.vars; v != nil; v = v.prev {
		v.right = rs[n]
		n++
	}
	return issub
}

// reachableVar is reachable_var (subtype.c:2734-2759): whether variable y
// is reachable from x through bounds, which would make a cycle.
func (e *stenv) reachableVar(x any, y *TypeVar) bool { return e.reachableVarFrom(x, y, nil) }

func (e *stenv) reachableVarFrom(x any, y *TypeVar, log *typeEnv) bool {
	if inUnion(x, y) {
		return true
	}
	if u, ok := x.(*Union); ok {
		return e.reachableVarFrom(u.A, y, log) || e.reachableVarFrom(u.B, y, log)
	}
	xv, ok := x.(*TypeVar)
	if !ok {
		return false
	}
	for t := log; t != nil; t = t.prev {
		if t.v == xv {
			return false
		}
	}
	lb, ub := xv.LB, xv.UB
	if xb := e.lookup(xv); xb != nil {
		lb, ub = xb.lb, xb.ub
	}
	newlog := &typeEnv{xv, nil, log}
	return e.reachableVarFrom(ub, y, newlog) || e.reachableVarFrom(lb, y, newlog)
}

// checkUnsatBound is check_unsat_bound (subtype.c:2762-2774): whether
// setting v == t implies v == SomeType{v}, which is unsatisfiable.
func (e *stenv) checkUnsatBound(t any, v *TypeVar) bool {
	if varOccursInside(t, v, false, false) {
		return true
	}
	for b := e.vars; b != nil; b = b.prev {
		if b.lb == any(v) && b.ub == any(v) && varOccursInside(t, b.v, false, false) {
			return true
		}
	}
	return false
}

// intersectVar is intersect_var (subtype.c:2779-2871).
func (e *stenv) intersectVar(b *TypeVar, a any, R bool, param int) any {
	bb := e.lookup(b)
	if bb == nil {
		if R {
			return e.intersectAside(a, b.UB, 0)
		}
		return e.intersectAside(b.UB, a, 0)
	}
	if e.reachableVar(bb.lb, b) || e.reachableVar(bb.ub, b) {
		return a
	}
	if lbv, ok := bb.lb.(*TypeVar); ok && bb.lb == bb.ub {
		if R {
			return e.intersect(a, lbv, param)
		}
		return e.intersect(lbv, a, param)
	}
	if !IsType(a) && !isTypeVar(a) {
		return e.setVarToConst(bb, a, R)
	}
	if param == 2 {
		var ub any
		if !HasFreeTypeVars(a) {
			if R {
				e.flipOffset()
			}
			ccheck := e.intersectVarCcheckInEnv(bb.lb, bb.ub, a, a, !R)
			if R {
				e.flipOffset()
			}
			if !ccheck {
				return Bottom
			}
			ub = a
		} else {
			e.triangular++
			if R {
				ub = e.intersectAside(a, bb.ub, bb.depth0)
			} else {
				ub = e.intersectAside(bb.ub, a, bb.depth0)
			}
			e.triangular--
			se := e.newSavedEnv()
			e.saveEnv(se)
			issub := e.subtypeInEnvExistential(bb.lb, ub)
			e.restoreEnv(se)
			e.freeSavedEnv(se)
			if !issub {
				return Bottom
			}
		}
		if ub != any(b) {
			if HasFreeTypeVars(ub) && e.checkUnsatBound(ub, b) {
				return Bottom
			}
			bb.ub = ub
			_, ubu := ub.(*Union)
			_, au := a.(*Union)
			_, ubua := ub.(*UnionAll)
			_, aua := a.(*UnionAll)
			if (ubu && !au) || (ubua && !aua) {
				ub = b
			} else {
				bb.lb = ub
			}
		}
		return ub
	}
	var ub any
	if R {
		ub = e.intersectAside(a, bb.ub, bb.depth0)
	} else {
		ub = e.intersectAside(bb.ub, a, bb.depth0)
	}
	if ub == any(Bottom) {
		return Bottom
	}
	if bb.constraintkind == 1 || (e.triangular != 0 && param == 1) {
		if e.triangular != 0 && e.checkUnsatBound(ub, b) {
			return Bottom
		}
		e.setBound(&bb.ub, ub, b)
		return b
	} else if bb.constraintkind == 0 {
		if !isTypeVar(a) && e.trySubtypeInEnv(bb.ub, a) {
			return b
		}
		return ub
	}
	// constraintkind 2
	if ub == a && bb.lb != any(Bottom) {
		return ub
	}
	if Egal(bb.ub, bb.lb) {
		return ub
	}
	if isLeafBound(ub) {
		e.setBound(&bb.lb, ub, b)
	}
	return ub
}

// omitBadUnion is omit_bad_union (subtype.c:2913-2964): u without the
// union members that mention t.
func omitBadUnion(u any, t *TypeVar) any {
	if !HasTypeVar(u, t) {
		return u // u itself when possible, as many checks use ==
	}
	switch x := u.(type) {
	case *UnionAll:
		v := x.Var
		body := omitBadUnion(x.Body, t)
		if !HasTypeVar(body, v) {
			return body
		}
		if HasTypeVar(v.LB, t) {
			return Bottom
		}
		ub := omitBadUnion(v.UB, t)
		if ub == any(Bottom) && v.LB != ub {
			return Bottom
		}
		if obviouslyEgal(v.LB, ub) {
			res := substituteVarNothrow(body, v, ub, 2)
			if res == null {
				return Bottom
			}
			return res
		}
		if ub != v.UB {
			v = newTypeVarChecked(v.Name, v.LB, ub)
			body = substituteVar(body, x.Var, v)
		}
		return &UnionAll{Var: v, Body: body}
	case *Union:
		return simpleJoin(omitBadUnion(x.A, t), omitBadUnion(x.B, t))
	}
	return Bottom
}

// newTypeVarChecked is jl_new_typevar, raising on bounds that are not
// types.
func newTypeVarChecked(name string, lb, ub any) *TypeVar {
	v, err := NewTypeVar(name, lb, ub)
	if err != nil {
		panic(err)
	}
	return v
}

// ivarBinding is jl_ivarbinding_t (subtype.c:93-99): a variable and its
// bounds, as pointers into a binding's own fields or into the storage
// finishUnionAll flattens inner variables into.
type ivarBinding struct {
	v      **TypeVar
	lb, ub *any
	root   *varBinding
	next   *ivarBinding
}

// hasTypeVarViaFlattenEnv is has_typevar_via_flatten_env
// (subtype.c:2967-3008).
func hasTypeVarViaFlattenEnv(x any, t *TypeVar, allvars *ivarBinding, checked []bool) bool {
	switch y := x.(type) {
	case *UnionAll:
		if hasTypeVarViaFlattenEnv(y.Var.LB, t, allvars, checked) || hasTypeVarViaFlattenEnv(y.Var.UB, t, allvars, checked) {
			return true
		}
		return hasTypeVarViaFlattenEnv(y.Body, t, allvars, checked)
	case *Union:
		return hasTypeVarViaFlattenEnv(y.A, t, allvars, checked) || hasTypeVarViaFlattenEnv(y.B, t, allvars, checked)
	case *Vararg:
		return (y.T != nil && hasTypeVarViaFlattenEnv(y.T, t, allvars, checked)) ||
			(y.N != nil && hasTypeVarViaFlattenEnv(y.N, t, allvars, checked))
	case *DataType:
		for _, p := range y.Parameters {
			if hasTypeVarViaFlattenEnv(p, t, allvars, checked) {
				return true
			}
		}
		return false
	case *TypeVar:
		if t == y {
			return true
		}
		ind := 0
		it := allvars
		for it != nil && *it.v != y {
			ind++
			it = it.next
		}
		if it == nil || checked[ind] {
			return false
		}
		checked[ind] = true
		return hasTypeVarViaFlattenEnv(*it.lb, t, allvars, checked) || hasTypeVarViaFlattenEnv(*it.ub, t, allvars, checked)
	}
	return false
}

func (e *stenv) envLength() int {
	n := 0
	for v := e.vars; v != nil; v = v.prev {
		n++
	}
	return n
}

// finishUnionAll is finish_unionall (subtype.c:3011-3359): it reduces vb's
// variable to a value when it can, and moves or rewraps its occurrences in
// the environment and in res.
func (e *stenv) finishUnionAll(res any, vb *varBinding, u *UnionAll) any {
	// null: no single value (a value can be nil, the embedder's nothing)
	varval := null
	newvar := vb.v
	// try to reduce var to a single value
	if _, ok := vb.ub.(int64); ok && isTypeVar(vb.lb) {
		varval = vb.ub
	} else if obviouslyEgal(vb.lb, vb.ub) {
		// given x<:T<:x, substitute x for T
		varval = vb.ub
	} else if vb.occursCov != 0 && isLeafBound(vb.ub) && !HasFreeTypeVars(vb.ub) {
		// replace T<:x with x in covariant position when possible
		varval = vb.ub
	}
	if vb.intValued != 0 {
		_, isLong := varval.(int64)
		// an int-valued variable is an Int, or has Union{}-Any bounds, or
		// equals another variable
		if !(isLong || (vb.lb == any(Bottom) && vb.ub == any(AnyType)) || (isTypeVar(vb.lb) && vb.ub == vb.lb)) {
			return Bottom
		}
	}
	if varval == null && (vb.lb != vb.v.LB || vb.ub != vb.v.UB) {
		newvar = newTypeVarChecked(vb.v.Name, vb.lb, vb.ub)
	}

	// flatten all innervars into a (reversed) list
	icount := 0
	if vb.innervars != nil {
		icount += len(*vb.innervars)
	}
	for b := e.vars; b != nil; b = b.prev {
		if b.innervars != nil {
			icount += len(*b.innervars)
		}
	}
	ivars := make([]*TypeVar, icount)
	ibounds := make([]any, 2*icount)
	n := 0
	var allvars *ivarBinding
	push := func(iv *TypeVar, root *varBinding) {
		ivars[n], ibounds[2*n], ibounds[2*n+1] = iv, iv.LB, iv.UB
		allvars = &ivarBinding{&ivars[n], &ibounds[2*n], &ibounds[2*n+1], root, allvars}
		n++
	}
	if vb.innervars != nil {
		for _, iv := range *vb.innervars {
			push(iv, vb)
		}
	}
	for b := e.vars; b != nil; b = b.prev {
		allvars = &ivarBinding{&b.v, &b.lb, &b.ub, b, allvars}
		if b.innervars != nil {
			for _, iv := range *b.innervars {
				push(iv, b)
			}
		}
	}

	// remove/replace/rewrap free occurrences of this var in the environment
	wrapped := false
	var pwrap, pbtemp *ivarBinding
	vcount := icount + e.envLength()
	checked := make([]bool, vcount)
	for btemp := allvars; btemp != nil; btemp = btemp.next {
		bdepth0 := btemp.root.depth0
		innerflag := 0
		ivar, ilb, iub := *btemp.v, *btemp.lb, *btemp.ub
		if HasTypeVar(ilb, vb.v) {
			if vb.lb == any(ivar) {
				return Bottom
			}
			if varval != null {
				if try(func() { *btemp.lb = substituteVar(ilb, vb.v, varval) }) != nil {
					res = Bottom
				}
			} else if ilb == any(vb.v) {
				*btemp.lb = vb.lb
			} else {
				innerflag |= 1
			}
		}
		if HasTypeVar(iub, vb.v) {
			if vb.ub == any(ivar) {
				*btemp.ub = omitBadUnion(iub, vb.v)
				if *btemp.ub == any(Bottom) && *btemp.ub != *btemp.lb {
					return Bottom
				}
			}
			if varval != null {
				iub = substituteVarNothrow(iub, vb.v, varval, 2)
				if iub == null {
					res = Bottom
				} else {
					*btemp.ub = iub
				}
			} else if iub == any(vb.v) {
				*btemp.ub = vb.ub
			} else {
				innerflag |= 2
			}
			if innerflag != 0 {
				clear(checked)
				if bdepth0 != vb.depth0 ||
					hasTypeVarViaFlattenEnv(vb.lb, ivar, allvars, checked) ||
					hasTypeVarViaFlattenEnv(vb.ub, ivar, allvars, checked) {
					if innerflag&1 != 0 {
						*btemp.lb = &UnionAll{Var: vb.v, Body: ilb}
					}
					if innerflag&2 != 0 {
						*btemp.ub = &UnionAll{Var: vb.v, Body: iub}
					}
				} else {
					// if our variable is T, and some outer variable has
					// constraint S = Ref{T}, move the `where T` outside
					// `where S` instead of putting it here (issue #21243)
					if newvar != vb.v {
						if innerflag&1 != 0 {
							*btemp.lb = substituteVar(ilb, vb.v, newvar)
						}
						if innerflag&2 != 0 {
							*btemp.ub = substituteVar(iub, vb.v, newvar)
						}
					}
					if !wrapped {
						pwrap = pbtemp
					}
					wrapped = true
				}
			}
		}
		pbtemp = btemp
	}

	// insert the newvar into the (reversed) var list if needed
	if wrapped {
		wrap := allvars
		if pwrap != nil {
			wrap = pwrap.next
		}
		inew := &ivarBinding{&newvar, &newvar.LB, &newvar.UB, wrap.root, wrap}
		if pwrap != nil {
			pwrap.next = inew
		} else {
			allvars = inew
		}
		vcount++
	}

	// re-sort the innervars inside the (reversed) var list, HasTypeVar
	// being the partial order; julia only asserts the bound on the moves,
	// a cyclic order would loop forever, so it is a refusal here
	if icount > 0 {
		var pib1 *ivarBinding
		sortCount := 0
		for {
			ib1 := allvars
			if pib1 != nil {
				ib1 = pib1.next
			}
			if ib1 == nil {
				break
			}
			if sortCount++; sortCount > vcount*(vcount+1)/2 {
				throwf("type intersection: the inner variables have a cyclic order")
			}
			lbfree, ubfree := HasFreeTypeVars(*ib1.lb), HasFreeTypeVars(*ib1.ub)
			if lbfree || ubfree {
				changed := false
				pib2, ib2 := ib1, ib1.next
				for ib2 != nil {
					isinnervar := ib2.root.v != *ib2.v
					if isinnervar && ib1.root.depth0 == ib2.root.depth0 &&
						((lbfree && HasTypeVar(*ib1.lb, *ib2.v)) || (ubfree && HasTypeVar(*ib1.ub, *ib2.v))) {
						pib2.next = ib2.next
						ib2.next = ib1
						ib2.root = ib1.root
						if pib1 != nil {
							pib1.next = ib2
						} else {
							allvars = ib2
						}
						changed = true
						break
					}
					pib2, ib2 = ib2, ib2.next
				}
				if changed {
					continue
				}
			}
			pib1 = ib1
		}
	}

	// freeze the innervars' lb/ub and substitute where needed
	for b1 := allvars; b1 != nil; b1 = b1.next {
		ivar, ilb, iub := *b1.v, *b1.lb, *b1.ub
		isinnervar := b1.root.v != ivar
		if isinnervar && (ivar.LB != ilb || ivar.UB != iub) {
			nivar := newTypeVarChecked(ivar.Name, ilb, iub)
			if HasTypeVar(res, ivar) {
				res = substituteVar(res, ivar, nivar)
			}
			for b2 := b1.next; b2 != nil; b2 = b2.next {
				if l := *b2.lb; HasTypeVar(l, ivar) {
					*b2.lb = substituteVar(l, ivar, nivar)
				}
				if h := *b2.ub; HasTypeVar(h, ivar) {
					*b2.ub = substituteVar(h, ivar, nivar)
				}
			}
			if !wrapped && varval == null {
				// newvar also needs bounds substitution
				if HasTypeVar(vb.lb, ivar) {
					vb.lb = substituteVar(vb.lb, ivar, nivar)
				}
				if HasTypeVar(vb.ub, ivar) {
					vb.ub = substituteVar(vb.ub, ivar, nivar)
				}
			}
			*b1.v = nivar
		}
	}

	// switch back the innervars' storage
	for {
		btemp := allvars
		root := vb
		if btemp != nil {
			root = btemp.root
		}
		cnt := 0
		for btemp != nil && btemp.root == root {
			btemp = btemp.next
			cnt++
		}
		if root != vb {
			cnt--
		}
		if root.innervars != nil {
			s := *root.innervars
			if cnt > len(s) {
				s = append(s, make([]*TypeVar, cnt-len(s))...)
			} else {
				s = s[:cnt]
			}
			*root.innervars = s
		} else if cnt > 0 {
			s := make([]*TypeVar, cnt)
			root.innervars = &s
		}
		btemp = allvars
		for i := cnt; i > 0; i-- {
			(*root.innervars)[i-1] = *btemp.v
			btemp = btemp.next
		}
		if root == vb {
			break
		}
		allvars = btemp.next
	}

	// if v still occurs, rewrap body in `UnionAll v` or eliminate the
	// UnionAll
	if HasTypeVar(res, vb.v) {
		if varval != null {
			// T{x} where x can be built though T's parameter is limited:
			// the instantiation may then be invalid
			res = substituteVarNothrow(res, vb.v, varval, 2)
			// simplify chains of UnionAlls where bounds become equal
			for res != null {
				ures, ok := res.(*UnionAll)
				if !ok || !obviouslyEgal(ures.Var.LB, ures.Var.UB) {
					break
				}
				res = substituteVarNothrow(ures.Body, ures.Var, ures.Var.LB, 2)
			}
			if res == null {
				res = Bottom
			}
		} else {
			// refresh newvar if bounds changed
			if vb.lb != newvar.LB || vb.ub != newvar.UB {
				newvar = newTypeVarChecked(newvar.Name, vb.lb, vb.ub)
			}
			if newvar != vb.v {
				res = substituteVar(res, vb.v, newvar)
			}
			varval = newvar
			if !wrapped {
				res = TypeUnionAll(newvar, res)
			}
		}
	}
	if vb.innervars != nil {
		for _, v := range *vb.innervars {
			res = TypeUnionAll(v, res)
		}
	}
	if vb.right && e.envidx < len(e.envout) {
		oldval := e.envout[e.envidx]
		_, vlong := varval.(int64)
		if varval == null || (!isLeafBound(varval) && vb.occursInv == 0) {
			e.envout[e.envidx] = vb.v
		} else if !(oldval != nil && isTypeVar(oldval) && vlong) {
			e.envout[e.envidx] = varval
		}
	}
	return res
}

// intersectUnionAllOnce is intersect_unionall_ (subtype.c:3361-3426).
func (e *stenv) intersectUnionAllOnce(t any, u *UnionAll, R bool, param int, vb *varBinding) any {
	envsize := 0
	for b := e.vars; b != nil; b = b.prev {
		envsize++
		if envsize > 120 {
			vb.limited = true
			return t
		}
	}
	u = e.unaliasUnionAll(u)
	vb.v = u.Var
	e.vars = vb
	var res any
	if R {
		e.envidx++
		res = e.intersect(t, u.Body, param)
		e.envidx--
	} else {
		res = e.intersect(u.Body, t, param)
	}
	vb.concrete = vb.concrete || (vb.occursCov > 1 && isLeafTypeVar(u.Var) && !varOccursInvariant(u.Body, u.Var))
	// the diagonal rule: a variable occurring more than once, and only in
	// covariant position, is concrete; its lower bound must not be abstract
	if res != any(Bottom) && vb.concrete && !isTypeVar(vb.lb) && !isLeafBound(vb.lb) {
		res = Bottom
	}
	e.vars = vb.prev
	if res != any(Bottom) {
		if vb.ub == any(Bottom) && vb.occursCov != 0 {
			// T=Bottom in covariant position
			res = Bottom
		} else if HasTypeVar(vb.lb, u.Var) {
			// fail on circular constraints
			res = Bottom
		} else {
			vb.ub = omitBadUnion(vb.ub, u.Var)
			if vb.ub == any(Bottom) && vb.ub != vb.lb {
				res = Bottom
			}
		}
	}
	if res != any(Bottom) {
		res = e.finishUnionAll(res, vb, u)
	}
	return res
}

// alwaysOccursCov is always_occurs_cov (subtype.c:3428-3458).
func alwaysOccursCov(v any, tv *TypeVar, param int) bool {
	if param > 1 {
		return false
	}
	if v == any(tv) {
		return param == 1
	}
	switch x := v.(type) {
	case *Union:
		return alwaysOccursCov(x.A, tv, param) && alwaysOccursCov(x.B, tv, param)
	case *UnionAll:
		return x.Var != tv && (alwaysOccursCov(x.Var.UB, tv, 0) || alwaysOccursCov(x.Body, tv, param))
	case *Vararg:
		return x.T != nil && alwaysOccursCov(x.T, tv, param)
	case *DataType:
		nparam := param
		if x.Name == TupleTypeName {
			nparam = 1
		}
		for _, p := range x.Parameters {
			if alwaysOccursCov(p, tv, nparam) {
				return true
			}
		}
	}
	return false
}

// intersectUnionAll is intersect_unionall (subtype.c:3460-3500): it
// intersects once, and again under the constraint kind the first pass
// found.
func (e *stenv) intersectUnionAll(t any, u *UnionAll, R bool, param int) any {
	vb := e.newVarBinding()
	defer e.freeVarBinding(vb)
	vb.v, vb.lb, vb.ub, vb.right, vb.depth0, vb.prev = u.Var, u.Var.LB, u.Var.UB, R, e.invdepth, e.vars
	se := e.newSavedEnv()
	e.saveEnv(se)
	noinv := !varOccursInvariant(u.Body, u.Var)
	if isLeafTypeVar(u.Var) && noinv && alwaysOccursCov(u.Body, u.Var, param) {
		vb.constraintkind = 1
	}
	res := e.intersectUnionAllOnce(t, u, R, param, vb)
	vb.intersected = true
	if vb.limited {
		// the environment got too big: avoid tree recursion and propagate
		// the flag
		if e.vars != nil {
			e.vars.limited = true
		}
	} else if res != any(Bottom) {
		constraint1 := vb.constraintkind
		switch {
		case vb.concrete || vb.occursInv > 1 || (vb.occursInv != 0 && vb.occursCov != 0):
			vb.constraintkind = 2
			if vb.concrete {
				vb.constraintkind = 1
			}
		case u.Var.LB != any(Bottom):
			vb.constraintkind = 2
		case vb.occursCov != 0 && noinv:
			vb.constraintkind = 1
		}
		if constraint1 != vb.constraintkind || vb.concrete {
			if constraint1 == 1 {
				vb.lb, vb.ub = vb.v.LB, vb.v.UB
			}
			e.restoreEnvRoot(se, vb.constraintkind == 1)
			vb.occursCov, vb.occursInv = 0, 0
			res = e.intersectUnionAllOnce(t, u, R, param, vb)
		}
	}
	e.freeSavedEnv(se)
	return res
}

// intersectVarargLength is intersect_vararg_length (subtype.c:3505-3518):
// whether n can be the length of Vararg v.
func (e *stenv) intersectVarargLength(v any, n int64, R bool) bool {
	N := v.(*Vararg).N
	// only checked if N is free in the tuple type's last parameter
	if isTypeVar(N) {
		var il any
		if R {
			il = e.intersectInvariant(n, N)
		} else {
			il = e.intersectInvariant(N, n)
		}
		if il == null || il == any(Bottom) {
			return false
		}
	}
	return true
}

// varargLengthVar is the length of a Vararg whose length is not an Int;
// julia asserts it is a TypeVar.
func varargLengthVar(n any) *TypeVar {
	v, ok := n.(*TypeVar)
	if !ok {
		throwf("type intersection: a Vararg length that is neither an Int nor a TypeVar")
	}
	return v
}

// intersectVarargs is intersect_varargs (subtype.c:3520-3600): Vararg is
// covariant in its first parameter, invariant in its second.
func (e *stenv) intersectVarargs(vmx, vmy *Vararg, offset int64, param int) any {
	xp1, xp2 := unwrapVararg(vmx), vmx.N
	yp1, yp2 := unwrapVararg(vmy), vmy.N
	// in Vararg{T1} <: Vararg{T2} subtyping runs twice to simulate several
	// arguments, which the diagonal rule needs
	p := param
	if p == 0 {
		p = 1
	}
	if e.intersect(xp1, yp1, p) == any(Bottom) {
		return Bottom
	}
	ii := e.intersect(xp1, yp1, 1)
	if ii == any(Bottom) {
		return Bottom
	}
	if xp2 == nil && yp2 == nil {
		if obviouslyEgal(xp1, ii) {
			return vmx
		}
		if obviouslyEgal(yp1, ii) {
			return vmy
		}
		return wrapVararg(ii, nil, true, 0)
	}
	var i2 any
	e.loffset = offset
	var xb, yb *varBinding
	var maxOffsetX, maxOffsetY int8
	if xp2 != nil {
		xv := varargLengthVar(xp2)
		if xb = e.lookup(xv); xb != nil {
			xb.intValued = 1
			maxOffsetX = xb.maxOffset
		}
		if yp2 == nil {
			i2 = e.boundVarBelow(xv, xb, false)
		}
	}
	if yp2 != nil {
		yv := varargLengthVar(yp2)
		if yb = e.lookup(yv); yb != nil {
			yb.intValued = 1
			maxOffsetY = yb.maxOffset
		}
		if xp2 == nil {
			i2 = e.boundVarBelow(yv, yb, true)
		}
	}
	if xp2 != nil && yp2 != nil {
		// Vararg{T,N} <: Vararg{T2,N2}; equate N and N2
		i2 = e.intersectInvariant(xp2, yp2)
		k, isLong := i2.(int64)
		tv, isVar := i2.(*TypeVar)
		if i2 == null || i2 == any(Bottom) || (isLong && k < 0) ||
			!((isVar && tv.LB == any(Bottom) && tv.UB == any(AnyType)) || isLong) {
			i2 = Bottom
		}
	}
	e.loffset = 0
	if i2 == any(Bottom) {
		return Bottom
	}
	if xb != nil && !xb.intersected {
		xb.maxOffset = maxOffsetX
		if offset > int64(xb.maxOffset) && xb.maxOffset >= 0 {
			xb.maxOffset = int8(min(offset, 32))
		}
	}
	if yb != nil && !yb.intersected {
		yb.maxOffset = maxOffsetY
		if -offset > int64(yb.maxOffset) && yb.maxOffset >= 0 {
			yb.maxOffset = int8(min(-offset, 32))
		}
	}
	if xp2 != nil && obviouslyEgal(xp1, ii) && obviouslyEgal(xp2, i2) {
		return vmx
	}
	if yp2 != nil && obviouslyEgal(yp1, ii) && obviouslyEgal(yp2, i2) {
		return vmy
	}
	return wrapVararg(ii, i2, true, 0)
}

// intersectTuple is intersect_tuple (subtype.c:3603-3764).
func (e *stenv) intersectTuple(xd, yd *DataType, param int) any {
	lx, ly := len(xd.Parameters), len(yd.Parameters)
	llx, lly := lx, ly
	if lx == 0 && ly == 0 {
		return yd
	}
	vvx, vvy := varargNone, varargNone
	if lx > 0 {
		vvx = kindOfVararg(xd.Parameters[lx-1])
	}
	if ly > 0 {
		vvy = kindOfVararg(yd.Parameters[ly-1])
	}
	if vvx == varargInt {
		llx += int(min(varargLength(xd.Parameters[lx-1]), maxTupleFill+1)) - 1
	}
	if vvy == varargInt {
		lly += int(min(varargLength(yd.Parameters[ly-1]), maxTupleFill+1)) - 1
	}
	// julia allocates the result's parameters; Vararg{T, huge} is a bomb
	if llx > maxTupleFill || lly > maxTupleFill {
		throwf("Vararg length exceeds the limit of %d", maxTupleFill)
	}
	if vvx == varargBound && (vvy == varargBound || vvy == varargUnbound) {
		xb := e.lookup(varargLengthVar(xd.Parameters[lx-1].(*Vararg).N))
		if xb != nil && xb.intersected && xb.maxOffset > 0 {
			llx += int(xb.maxOffset)
		}
	}
	if vvy == varargBound && (vvx == varargBound || vvx == varargUnbound) {
		yb := e.lookup(varargLengthVar(yd.Parameters[ly-1].(*Vararg).N))
		if yb != nil && yb.intersected && yb.maxOffset > 0 {
			lly += int(yb.maxOffset)
		}
	}
	if (vvx == varargNone || vvx == varargInt) && (vvy == varargNone || vvy == varargInt) && llx != lly {
		return Bottom
	}
	np := max(llx, lly)
	params := make([]any, np)
	bottom := false
	i, j := 0, 0
	// x or y itself is the result whenever it can be (e.g. when it is the
	// supertype) instead of a copy
	isx, isy := true, true
	for {
		vx, vy := false, false
		// julia's NULL past the end is hasx/hasy: a parameter can be nil
		var xi, yi any
		hasx, hasy := i < llx, j < lly
		if hasx {
			xi = xd.Parameters[min(i, lx-1)]
		}
		if hasy {
			yi = yd.Parameters[min(j, ly-1)]
		}
		if !hasx && !hasy {
			break
		}
		if _, ok := xi.(*Vararg); ok {
			vx = vvx == varargUnbound || (vvx == varargBound && i == llx-1)
		}
		if _, ok := yi.(*Vararg); ok {
			vy = vvy == varargUnbound || (vvy == varargBound && j == lly-1)
		}
		if !hasx || !hasy {
			if vx && e.intersectVarargLength(xi, int64(lly+1-llx), false) {
				np = j
			} else if vy && e.intersectVarargLength(yi, int64(llx+1-lly), true) {
				np = i
			} else {
				bottom = true
			}
			break
		}
		var ii any
		if vx && vy {
			// xi's offset: {A^n...,Vararg{T,N}} ∩ {Vararg{S,M}} is
			// {(A∩S)^n...,Vararg{T∩S,N}} plus N = M-n
			ii = e.intersectVarargs(xi.(*Vararg), yi.(*Vararg), int64(lly-llx), param)
		} else {
			xu, yu := xi, yi
			if va, ok := xi.(*Vararg); ok {
				xu = unwrapVararg(va)
			}
			if va, ok := yi.(*Vararg); ok {
				yu = unwrapVararg(va)
			}
			p := param
			if p == 0 {
				p = 1
			}
			ii = e.intersect(xu, yu, p)
		}
		if ii == any(Bottom) {
			if vx && vy {
				var xb, yb *varBinding
				if xlen := xi.(*Vararg).N; xlen != nil {
					xb = e.lookup(varargLengthVar(xlen))
				}
				if ylen := yi.(*Vararg).N; ylen != nil {
					yb = e.lookup(varargLengthVar(ylen))
				}
				l := max(i, j)
				xbl, xbLong := int64(0), false
				if xb != nil {
					xbl, xbLong = xb.lb.(int64)
				}
				ybl, ybLong := int64(0), false
				if yb != nil {
					ybl, ybLong = yb.lb.(int64)
				}
				if (xbLong && int64(llx-1)+xbl != int64(l)) || (ybLong && int64(lly-1)+ybl != int64(l)) {
					bottom = true
				} else {
					if xb != nil {
						e.setVarToConst(xb, int64(l-llx+1), false)
					}
					if yb != nil {
						e.setVarToConst(yb, int64(l-lly+1), true)
					}
					np = l
				}
			} else {
				bottom = true
			}
			break
		}
		isx = isx && ii == xi
		isy = isy && ii == yi
		params[max(i, j)] = ii
		if vx && vy {
			break
		}
		if !vx {
			i++
		}
		if !vy {
			j++
		}
	}
	if bottom {
		return Bottom
	}
	isx = isx && lx == np
	isy = isy && ly == np
	if !isx && !isy {
		// a more careful check now for equivalence
		if lx == np {
			isx = true
			for k := 0; k < np; k++ {
				isx = isx && obviouslyEgal(params[k], xd.Parameters[k])
			}
		}
		if !isx && ly == np {
			isy = true
			for k := 0; k < np; k++ {
				isy = isy && obviouslyEgal(params[k], yd.Parameters[k])
			}
		}
	}
	switch {
	case isx:
		return xd
	case isy:
		return yd
	}
	return applyType(AnyTupleType, params[:np])
}

// flipVars is flip_vars (subtype.c:3766-3773).
func (e *stenv) flipVars() {
	for b := e.vars; b != nil; b = b.prev {
		b.right = !b.right
	}
}

// intersectSubDatatype is intersect_sub_datatype (subtype.c:3776-3787):
// xd nominally inherits from yd; the attempt only adds constraints to the
// environment, which finishUnionAll propagates.
func (e *stenv) intersectSubDatatype(xd, yd *DataType, R bool, param int) any {
	var isuper any
	if R {
		isuper = e.intersect(yd, superOf(xd), param)
	} else {
		isuper = e.intersect(superOf(xd), yd, param)
	}
	if isuper == any(Bottom) {
		return Bottom
	}
	return xd
}

// superOf is a DataType's supertype, Any for one whose supertype is not set.
func superOf(d *DataType) *DataType {
	if d.Super == nil {
		return AnyType
	}
	return d.Super
}

// intersectInvariant is intersect_invariant (subtype.c:3789-3826); null is
// julia's NULL, no intersection that is a type.
func (e *stenv) intersectInvariant(x, y any) any {
	if e.loffset == 0 && !HasFreeTypeVars(x) && !HasFreeTypeVars(y) {
		if Subtype(x, y) && Subtype(y, x) {
			return y
		}
		return null
	}
	e.invdepth++
	ii := e.intersect(x, y, 2)
	e.invdepth--
	if isTypeVar(x) && isTypeVar(y) && isTypeVar(ii) {
		return ii // the check below is skipped: constraints may be circular
	}
	if ii == any(Bottom) {
		if !e.subtypeInEnv(x, Bottom) {
			return null
		}
		e.flipVars()
		e.flipOffset()
		ok := e.subtypeInEnv(y, Bottom)
		e.flipVars()
		e.flipOffset()
		if !ok {
			return null
		}
		return Bottom
	}
	se := e.newSavedEnv()
	e.saveEnv(se)
	if !e.subtypeInEnvExistential(x, y) {
		ii = null
	} else {
		e.restoreEnv(se)
		e.flipOffset()
		if !e.subtypeInEnvExistential(y, x) {
			ii = null
		}
		e.flipOffset()
	}
	e.restoreEnv(se)
	e.freeSavedEnv(se)
	return ii
}

// intersectTypeType is intersect_type_type (subtype.c:3829-3849): x is
// Type{...} and y is not.
func (e *stenv) intersectTypeType(x, y any) any {
	p0 := x.(*DataType).Parameters[0]
	tv, ok := p0.(*TypeVar)
	if !ok {
		if TypeOf(p0) == y {
			return x
		}
		return Bottom
	}
	if !IsKind(y) {
		return Bottom
	}
	if y == any(TypeofBottomType) && tv.LB == any(Bottom) {
		return applyType(TypeType, []any{Bottom})
	}
	if tv.UB == any(AnyType) {
		return y
	}
	return x
}

// comparetoVar is compareto_var (subtype.c:3853-3868): cmp <= 0 asks
// whether x <= y already holds in the environment, cmp >= 0 x >= y.
func (e *stenv) comparetoVar(x any, y *TypeVar, cmp int) bool {
	if x == any(y) {
		return true
	}
	xv, ok := x.(*TypeVar)
	if !ok {
		return false
	}
	xb := e.lookup(xv)
	if xb == nil {
		return false
	}
	ans := true
	if cmp <= 0 {
		ans = ans && e.comparetoVar(xb.ub, y, cmp)
	}
	if cmp >= 0 {
		ans = ans && e.comparetoVar(xb.lb, y, cmp)
	}
	return ans
}

// subtypeByBounds is subtype_by_bounds (subtype.c:3873-3878): true when
// the recorded bounds already assert x <: y, false when unknown.
func (e *stenv) subtypeByBounds(x, y any) bool {
	xv, ok1 := x.(*TypeVar)
	yv, ok2 := y.(*TypeVar)
	if !ok1 || !ok2 {
		return false
	}
	return e.comparetoVar(x, yv, -1) || e.comparetoVar(y, xv, 1)
}

// intersectVarCcheckInEnv is intersect_var_ccheck_in_env
// (subtype.c:3880-3961): whether xlb <: yub and ylb <: xub can both hold.
func (e *stenv) intersectVarCcheckInEnv(xlb, xub, ylb, yub any, flip bool) bool {
	easy1 := xlb == any(Bottom) || yub == any(AnyType) || (e.loffset == 0 && obviouslyInUnion(yub, xlb))
	easy2 := ylb == any(Bottom) || xub == any(AnyType) || (e.loffset == 0 && obviouslyInUnion(xub, ylb))
	nofree1, nofree2 := false, false
	if !easy1 {
		nofree1 = !HasFreeTypeVars(xlb) && !HasFreeTypeVars(yub)
		if nofree1 && e.loffset == 0 {
			if easy1 = Subtype(xlb, yub); !easy1 {
				return false
			}
		}
	}
	if !easy2 {
		nofree2 = !HasFreeTypeVars(ylb) && !HasFreeTypeVars(xub)
		if nofree2 && e.loffset == 0 {
			if easy2 = Subtype(ylb, xub); !easy2 {
				return false
			}
		}
	}
	if easy1 && easy2 {
		return true
	}
	if (easy1 || nofree1) && (easy2 || nofree2) {
		vars := e.vars
		e.vars = nil
		ccheck := easy1 || e.subtypeInEnv(xlb, yub)
		if ccheck && !easy2 {
			e.flipOffset()
			ccheck = e.subtypeInEnv(ylb, xub)
			e.flipOffset()
		}
		e.vars = vars
		return ccheck
	}
	se := e.newSavedEnv()
	defer e.freeSavedEnv(se)
	e.saveEnv(se)
	// first the normal flip
	if flip {
		e.flipVars()
	}
	ccheck := easy1 || e.subtypeInEnv(xlb, yub)
	if ccheck && !easy2 {
		e.flipOffset()
		ccheck = e.subtypeInEnv(ylb, xub)
		e.flipOffset()
	}
	if flip {
		e.flipVars()
	}
	if !ccheck {
		// then the reverse flip
		e.restoreEnv(se)
		if !flip {
			e.flipVars()
		}
		ccheck = easy1 || e.subtypeInEnv(xlb, yub)
		if ccheck && !easy2 {
			e.flipOffset()
			ccheck = e.subtypeInEnv(ylb, xub)
			e.flipOffset()
		}
		if !flip {
			e.flipVars()
		}
	}
	if !ccheck {
		// then existential
		e.restoreEnv(se)
		if easy1 {
			ccheck = true
		} else {
			ccheck = e.subtypeInEnvExistential(xlb, yub)
			e.restoreEnv(se)
		}
		if ccheck && !easy2 {
			e.flipOffset()
			ccheck = e.subtypeInEnvExistential(ylb, xub)
			e.flipOffset()
			e.restoreEnv(se)
		}
	}
	return ccheck
}

// hasTypeVarViaEnv is has_typevar_via_env (subtype.c:3963-3978).
func (e *stenv) hasTypeVarViaEnv(x any, t *TypeVar) bool {
	if e.loffset == 0 {
		for b := e.vars; b != nil; b = b.prev {
			if b.v == t {
				break
			}
			if b.lb == b.ub && b.lb == any(t) && HasTypeVar(x, b.v) {
				return true
			}
		}
	}
	return HasTypeVar(x, t)
}

// intersect is intersect (subtype.c:3984-4271). param says whether x and y
// are parameters of a type constructor, as in subtype; it records where
// variables occur for the diagonal rule.
func (e *stenv) intersect(x, y any, param int) any {
	e.calls++
	if e.calls > maxSubtypeCalls {
		throwf("type intersection exceeded %d steps", maxSubtypeCalls)
	}
	if x == y {
		return y
	}
	if xv, ok := x.(*TypeVar); ok {
		if yv, ok := y.(*TypeVar); ok {
			return e.intersectVars(xv, yv, param)
		}
		e.recordVarOccurrence(e.lookup(xv), param)
		return e.intersectVar(xv, y, false, param)
	}
	if yv, ok := y.(*TypeVar); ok {
		e.recordVarOccurrence(e.lookup(yv), param)
		return e.intersectVar(yv, x, true, param)
	}
	if e.loffset == 0 && !HasFreeTypeVars(x) && !HasFreeTypeVars(y) {
		if Subtype(x, y) {
			return x
		}
		if Subtype(y, x) {
			return y
		}
	}
	if xu, ok := x.(*Union); ok {
		if obviouslyInUnion(x, y) {
			return y
		}
		if _, ok := y.(*Union); ok && obviouslyInUnion(y, x) {
			return x
		}
		return e.intersectUnion(y, xu, false, param)
	}
	if yu, ok := y.(*Union); ok {
		if obviouslyInUnion(y, x) {
			return x
		}
		if xa, ok := x.(*UnionAll); ok && (HasFreeTypeVars(x) || HasFreeTypeVars(y)) {
			return e.intersectUnionAll(y, xa, false, param)
		}
		return e.intersectUnion(x, yu, true, param)
	}
	if y == any(AnyType) {
		return x
	}
	if x == any(AnyType) {
		return y
	}
	if xa, ok := x.(*UnionAll); ok {
		if ya, ok := y.(*UnionAll); ok {
			var b any = Bottom
			se := e.newSavedEnv()
			e.saveEnv(se)
			a := e.intersectUnionAll(y, xa, false, param)
			if ua, ok := a.(*UnionAll); ok {
				if ub, ok := ua.Body.(*UnionAll); ok && (HasTypeVar(ub.Var.UB, ua.Var) || HasTypeVar(ub.Var.LB, ua.Var)) {
					e.restoreEnvRoot(se, false) // restore counts
					b = e.intersectUnionAll(x, ya, true, param)
				}
			}
			e.freeSavedEnv(se)
			var res any
			if !HasFreeTypeVars(a) && !HasFreeTypeVars(b) {
				if Subtype(a, b) {
					res = b
				} else if Subtype(b, a) {
					res = a
				}
			}
			if res == nil {
				res = simpleJoin(a, b)
			}
			return res
		}
		return e.intersectUnionAll(y, xa, false, param)
	}
	if ya, ok := y.(*UnionAll); ok {
		return e.intersectUnionAll(x, ya, true, param)
	}
	xd, xok := x.(*DataType)
	yd, yok := y.(*DataType)
	if xok && yok {
		if param < 2 {
			if IsTypeType(x) {
				if !IsTypeType(y) {
					return e.intersectTypeType(x, y)
				}
			} else if IsTypeType(y) {
				return e.intersectTypeType(y, x)
			}
		}
		if xd.Name == yd.Name {
			if xd.Name == TupleTypeName {
				return e.intersectTuple(xd, yd, param)
			}
			return e.intersectSameName(xd, yd)
		}
		if param == 2 {
			return Bottom
		}
		for xd != AnyType && xd.Name != yd.Name {
			xd = superOf(xd)
		}
		if xd == AnyType {
			xd = x.(*DataType)
			for yd != AnyType && yd.Name != xd.Name {
				yd = superOf(yd)
			}
			if yd == AnyType {
				return Bottom
			}
			return e.intersectSubDatatype(y.(*DataType), xd, true, param)
		}
		return e.intersectSubDatatype(x.(*DataType), yd, false, param)
	}
	if Egal(x, y) {
		return y
	}
	return Bottom
}

// intersectSameName is intersect's case of two DataTypes of one name that
// are not tuples (subtype.c:4214-4254): every parameter intersected
// invariantly.
func (e *stenv) intersectSameName(xd, yd *DataType) any {
	np := len(xd.Parameters)
	newparams := make([]any, np)
	isx, isy := true, true
	i := 0
	for ; i < np; i++ {
		xi, yi := xd.Parameters[i], yd.Parameters[i]
		ii := e.intersectInvariant(xi, yi)
		if ii == null {
			break
		}
		isx = isx && ii == xi
		isy = isy && ii == yi
		newparams[i] = ii
	}
	if i < np {
		return Bottom
	}
	if !isx && !isy {
		// a more careful check now for equivalence
		isx = true
		for k := 0; k < np; k++ {
			isx = isx && obviouslyEgal(newparams[k], xd.Parameters[k])
		}
		if !isx {
			isy = true
			for k := 0; k < np; k++ {
				isy = isy && obviouslyEgal(newparams[k], yd.Parameters[k])
			}
		}
	}
	if isx {
		return xd
	}
	if isy {
		return yd
	}
	// julia catches any error the application raises (JL_TRY)
	if res, err := ApplyType(xd.Name.Wrapper, newparams...); err == nil {
		return res
	}
	return Bottom
}

// intersectVars is intersect's case of two type variables
// (subtype.c:3988-4139).
func (e *stenv) intersectVars(xv, yv *TypeVar, param int) any {
	xx, yy := e.lookup(xv), e.lookup(yv)
	R := false
	if xx != nil && yy != nil && e.varOutside(xv, yv) {
		// to preserve variable identities, bounds accumulate on the outer
		// variable, which is returned, and the inner one is set equal to it
		xv, yv = yv, xv
		xx, yy = yy, xx
		R = true
	}
	var x, y any = xv, yv
	if param == 2 {
		xlb, xub, ylb, yub := xv.LB, xv.UB, yv.LB, yv.UB
		if xx != nil {
			xlb, xub = xx.lb, xx.ub
		}
		if yy != nil {
			ylb, yub = yy.lb, yy.ub
		}
		if xx != nil && yy != nil && xx.depth0 != yy.depth0 {
			e.recordVarOccurrence(xx, param)
			e.recordVarOccurrence(yy, param)
			if e.subtypeInEnv(yy.ub, yy.lb) {
				return y
			}
			return Bottom
		}
		// (julia repeats the next two tests verbatim after them; the
		// repetition cannot be reached)
		if xubv, ok := xub.(*TypeVar); ok && xub == xlb {
			e.recordVarOccurrence(xx, param)
			if y == xub {
				e.recordVarOccurrence(yy, param)
				return y
			}
			if R {
				e.flipOffset()
			}
			res := e.intersect(xubv, y, param)
			if R {
				e.flipOffset()
			}
			return res
		}
		if yubv, ok := yub.(*TypeVar); ok && yub == ylb {
			e.recordVarOccurrence(yy, param)
			if R {
				e.flipOffset()
			}
			res := e.intersect(x, yubv, param)
			if R {
				e.flipOffset()
			}
			return res
		}
		e.recordVarOccurrence(xx, param)
		e.recordVarOccurrence(yy, param)
		xoffset := e.loffset
		if R {
			xoffset = -xoffset
		}
		if !IsType(ylb) && !isTypeVar(ylb) {
			if xx != nil {
				return e.setVarToConst(xx, ylb, R)
			}
			if (xlb == any(Bottom) && xub == any(AnyType)) || Egal(xlb, ylb) {
				if xoffset == 0 {
					return ylb
				}
				if yl, ok := ylb.(int64); ok {
					if xoffset > 0 {
						return ylb
					}
					return yl + xoffset
				}
			}
			return Bottom
		}
		if !IsType(xlb) && !isTypeVar(xlb) {
			if yy != nil {
				return e.setVarToConst(yy, xlb, !R)
			}
			if ylb == any(Bottom) && yub == any(AnyType) {
				if xoffset == 0 {
					return xlb
				}
				if xl, ok := xlb.(int64); ok {
					if xoffset < 0 {
						return xlb
					}
					// julia unboxes ylb here, which is Union{}; xlb is the
					// Int this branch tested
					return xl - xoffset
				}
			}
			return Bottom
		}
		var ccheck bool
		if R {
			e.flipOffset()
		}
		switch {
		case xlb == xub && ylb == yub && HasTypeVar(xlb, yv) && HasTypeVar(ylb, xv):
			// e.g. Val{Y}<:X<:Val{Y} && Val{X}<:Y<:Val{X}, or
			// Y<:X<:Y && Val{X}<:Y<:Val{X} => Val{Y}<:Y<:Val{Y}
			ccheck = false
		case yub == xub || (e.subtypeByBounds(xlb, yub) && e.subtypeByBounds(ylb, xub)):
			ccheck = true
		default:
			// many subtype checks, to avoid a false Union{}
			ccheck = e.intersectVarCcheckInEnv(xlb, xub, ylb, yub, R)
		}
		if R {
			e.flipOffset()
		}
		if !ccheck {
			return Bottom
		}
		if (e.hasTypeVarViaEnv(xub, yv) || e.hasTypeVarViaEnv(xub, xv)) &&
			(e.hasTypeVarViaEnv(yub, xv) || e.hasTypeVarViaEnv(yub, yv)) {
			// a circular constraint: the result will be Union{}, and
			// intersect(xub, yub) would not terminate
			return y
		}
		d := 0
		if xx != nil {
			d = xx.depth0
		} else if yy != nil {
			d = yy.depth0
		}
		var ub, lb any
		if R {
			ub = e.intersectAside(yub, xub, d)
		} else {
			ub = e.intersectAside(xub, yub, d)
		}
		if e.reachableVar(xlb, yv) {
			lb = ylb
		} else {
			lb = simpleJoin(xlb, ylb)
		}
		if yy != nil && xoffset == 0 {
			yy.lb = lb
			if !e.reachableVar(ub, yv) {
				yy.ub = ub
			}
		}
		if xx != nil && xoffset == 0 && !e.reachableVar(y, xv) {
			xx.lb, xx.ub = y, y
		}
		// the shorter Vararg's length is returned
		if xoffset < 0 {
			return x
		}
		return y
	}
	e.recordVarOccurrence(xx, param)
	e.recordVarOccurrence(yy, param)
	if xx != nil && yy != nil && xx.concrete && !yy.concrete {
		return e.intersectVar(xv, y, R, param)
	}
	return e.intersectVar(yv, x, !R, param)
}

// mergeEnv is merge_env (subtype.c:4273-4343): the bounds of every union
// choice that intersected, merged into me.
func (e *stenv) mergeEnv(me, se *savedEnv, count int) int {
	if count == 0 {
		e.saveEnv(me)
		return 1
	}
	n, m, k := 0, 0, 0
	for v := e.vars; v != nil; v = v.prev {
		b0, b1, b2 := se.roots[n], me.roots[n], v.lb
		if b1 == b0 || b2 == b0 {
			me.roots[n] = b0
		} else {
			me.roots[n] = simpleMeet(b1, b2, false)
		}
		b0, b1, b2 = se.roots[n+1], me.roots[n+1], v.ub
		if b1 == b0 || b2 == b0 {
			me.roots[n+1] = b0
		} else {
			me.roots[n+1] = simpleJoin(b1, b2)
		}
		if i1, i2 := me.inner[k], v.innervars; i2 != nil && i1 != i2 {
			if i1 != nil {
				*i1 = append(*i1, *i2...)
			} else {
				me.inner[k] = i2
			}
		}
		// occurrences merge by max (never decrease), max_offset by min
		if v.occursInv > me.buf[m] {
			me.buf[m] = v.occursInv
		}
		if v.occursCov > me.buf[m+1] {
			me.buf[m+1] = v.occursCov
		}
		if !v.intersected && v.maxOffset < me.buf[m+2] {
			me.buf[m+2] = v.maxOffset
		}
		n, m, k = n+2, m+3, k+1
	}
	return count + 1
}

// intersectAll is intersect_all (subtype.c:4345-4405): the union of the
// intersections over every choice of the right side's unions.
func (e *stenv) intersectAll(x, y any) any {
	e.runions.depth, e.runions.more, e.runions.used = 0, 0, 0
	se := e.newSavedEnv()
	e.saveEnv(se)
	var me *savedEnv
	niter, totalIter := 0, 0
	merge := func() {
		if me == nil {
			me = e.newSavedEnv()
		}
		niter = e.mergeEnv(me, se, niter)
		e.restoreEnv(se)
	}
	is0 := e.intersect(x, y, 0)
	if is0 == any(Bottom) {
		e.restoreEnv(se)
	} else if !e.emptinessOnly && e.runions.more != 0 {
		merge()
	}
	for e.nextUnionState(true) {
		if e.emptinessOnly && is0 != any(Bottom) {
			break
		}
		e.runions.depth, e.runions.more = 0, 0
		is1 := e.intersect(x, y, 0)
		if is1 == any(Bottom) {
			e.restoreEnv(se)
		} else if niter > 0 || (!e.emptinessOnly && e.runions.more != 0) {
			merge()
		}
		if is0 == any(Bottom) {
			is0 = is1
		} else if is1 != any(Bottom) {
			is0 = TypeUnion(is0, is1)
		}
		totalIter++
		if e.runions.more != 0 && (niter > 4 || totalIter > 400000) {
			// precise intersection is given up: the saved env comes back
			is0 = y
			e.restoreEnv(se)
			niter = 0
			break
		}
	}
	if niter != 0 {
		e.restoreEnv(me)
	}
	if me != nil {
		e.freeSavedEnv(me)
	}
	e.freeSavedEnv(se)
	return is0
}

func isDispatchTupleType(x any) bool {
	d, ok := x.(*DataType)
	return ok && d.IsDispatchTuple
}

// intersectTypes is intersect_types (subtype.c:4409-4428).
func intersectTypes(x, y any, emptinessOnly bool) any {
	if obviouslyDisjoint(x, y, false) {
		return Bottom
	}
	if isDispatchTupleType(x) || isDispatchTupleType(y) {
		if Subtype(x, y) {
			return x
		}
		if Subtype(y, x) {
			return y
		}
		return Bottom
	}
	e := getStenv(nil)
	defer putStenv(e)
	e.intersection, e.ignoreFree, e.emptinessOnly = true, true, emptinessOnly
	return e.intersectAll(x, y)
}

// IntersectTypes is jl_intersect_types (subtype.c:4430-4433): an upper
// bound of the intersection of x and y.
func IntersectTypes(x, y any) (t any, err error) {
	defer catchError(&err)
	return intersectTypes(x, y, false), nil
}

// HasEmptyIntersection is jl_has_empty_intersection
// (subtype.c:4436-4439).
func HasEmptyIntersection(x, y any) (empty bool, err error) {
	defer catchError(&err)
	return intersectTypes(x, y, true) == any(Bottom), nil
}

// switchUnionTuple is switch_union_tuple (subtype.c:4460-4530): for
// (unions or UnionAlls of) tuples of one length, the tuple of pointwise
// unions; nil otherwise.
func switchUnionTuple(a, b any) any {
	if ua, ok := a.(*UnionAll); ok {
		if ub, ok := b.(*UnionAll); ok && ub.Var.LB == ua.Var.LB && ub.Var.UB == ua.Var.UB {
			ub2 := instType(ub.Body, &typeEnv{ub.Var, ua.Var, nil}, nil, true)
			ans := switchUnionTuple(ua.Body, ub2)
			if ans != nil {
				ans = TypeUnionAll(ua.Var, ans)
			}
			return ans
		}
		ans := switchUnionTuple(ua.Body, b)
		if ans == nil {
			return nil
		}
		return TypeUnionAll(ua.Var, ans)
	}
	if ub, ok := b.(*UnionAll); ok {
		ans := switchUnionTuple(a, ub.Body)
		if ans == nil {
			return nil
		}
		return TypeUnionAll(ub.Var, ans)
	}
	if u, ok := a.(*Union); ok {
		if a = switchUnionTuple(u.A, u.B); a == nil {
			return nil
		}
		return switchUnionTuple(a, b)
	}
	if u, ok := b.(*Union); ok {
		if b = switchUnionTuple(u.A, u.B); b == nil {
			return nil
		}
		return switchUnionTuple(a, b)
	}
	ad, aok := a.(*DataType)
	bd, bok := b.(*DataType)
	if !aok || !bok || ad.Name != TupleTypeName || bd.Name != TupleTypeName {
		return nil
	}
	if len(ad.Parameters) != len(bd.Parameters) || IsVaTuple(ad) || IsVaTuple(bd) {
		return nil
	}
	vec := make([]any, len(ad.Parameters))
	for i := range vec {
		vec[i] = TypeUnion(ad.Parameters[i], bd.Parameters[i])
	}
	return applyType(AnyTupleType, vec)
}

// mightIntersectConcrete is might_intersect_concrete (subtype.c:4535-4562):
// a may intersect some concrete b even if neither is a subtype of the
// other (a = Tuple{Type{<:Vector}}, b = Tuple{DataType}).
func mightIntersectConcrete(a any) bool {
	a = UnwrapUnionAll(a)
	switch x := a.(type) {
	case *TypeVar:
		return true // (maybe)
	case *Union:
		return mightIntersectConcrete(x.A) || mightIntersectConcrete(x.B)
	case *Vararg:
		return mightIntersectConcrete(unwrapVararg(x))
	case *DataType:
		if x.Name == TypeTypeName {
			return true
		}
		tpl := x.Name == TupleTypeName
		for _, p := range x.Parameters {
			if isTypeVar(p) {
				return true
			}
			if tpl && p == any(Bottom) {
				return true
			}
			if tpl && mightIntersectConcrete(p) {
				return true
			}
		}
	}
	return false
}

// rewrapUnionAllRaw is jl_rewrap_unionall_ (jltypes.c:1599-1608): t wrapped
// in u's UnionAlls without simplification.
func rewrapUnionAllRaw(t, u any) any {
	ua, ok := u.(*UnionAll)
	if !ok {
		return t
	}
	return &UnionAll{Var: ua.Var, Body: rewrapUnionAllRaw(t, ua.Body)}
}

func subtypeEnvSize(t any) int {
	n := 0
	for {
		u, ok := t.(*UnionAll)
		if !ok {
			return n
		}
		n++
		t = u.Body
	}
}

// typeIntersectionEnv is jl_type_intersection_env_s (subtype.c:4565-4656):
// the intersection of a and b, the values of b's variables in it (nil when
// julia leaves them unset), and whether a <: b.
func typeIntersectionEnv(a, b any) (ans any, env []any, issubty bool) {
	if obviouslyDisjoint(a, b, false) {
		return Bottom, nil, a == any(Bottom)
	}
	szb := subtypeEnvSize(b)
	envb := make([]any, szb)
	sz := 0
	lta, ltb := IsConcrete(a), IsConcrete(b)
	switch {
	case subtypeEnv(a, b, envb):
		ans, sz, issubty = a, szb, true
	case Subtype(b, a):
		ans = b
	default:
		if lta && !mightIntersectConcrete(b) {
			return Bottom, nil, false
		}
		if ltb && !mightIntersectConcrete(a) {
			return Bottom, nil, false
		}
		clear(envb)
		e := getStenv(envb)
		e.intersection, e.ignoreFree = true, true
		ans = e.intersectAll(a, b)
		putStenv(e)
		if ans == any(Bottom) {
			return Bottom, nil, false
		}
		// code dealing with method signatures cannot handle unions: two
		// tuples may not give a union, even if intersect produced one
		envFromSubtype := true
		if IsTupleType(UnwrapUnionAll(a)) && IsTupleType(UnwrapUnionAll(b)) {
			if _, ok := UnwrapUnionAll(ans).(*DataType); !ok {
				if u, ok := UnwrapUnionAll(ans).(*Union); ok {
					if s := switchUnionTuple(u.A, u.B); s != nil {
						ans = rewrapUnionAllRaw(s, ans)
					}
				}
				if _, ok := UnwrapUnionAll(ans).(*DataType); !ok {
					ans = b
					envFromSubtype = false
				}
			}
		}
		if envFromSubtype {
			sz = szb
			// the env comes from subtyping the result: julia does not yet
			// compute it during intersection
			if szb > 0 && !TypesEqual(b, TypeType) && !subtypeEnv(ans, b, envb) {
				sz = 0
			}
		}
	}
	if sz == 0 && szb > 0 {
		i := 0
		for ub, ok := b.(*UnionAll); ok; ub, ok = ub.Body.(*UnionAll) {
			envb[i] = ub.Var
			i++
		}
		sz = szb
	}
	return ans, envb[:sz], issubty
}

// TypeIntersection is jl_type_intersection (subtype.c:4663-4666).
func TypeIntersection(a, b any) (t any, err error) {
	defer catchError(&err)
	t, _, _ = typeIntersectionEnv(a, b)
	return t, nil
}

// TypeIntersectionEnv is jl_type_intersection_env_s: the intersection, the
// values of b's UnionAll variables in it (empty where julia leaves its env
// unset), and whether a <: b.
func TypeIntersectionEnv(a, b any) (t any, env []any, issubty bool, err error) {
	defer catchError(&err)
	t, env, issubty = typeIntersectionEnv(a, b)
	return t, env, issubty, nil
}
