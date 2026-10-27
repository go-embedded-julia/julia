package core

// Subtyping: a statement-by-statement port of the subtype path of
// src/subtype.c (julia 1.12.6, lines 43-2529). Intersection
// (subtype.c:2533-4700) is not ported yet, so every branch that julia takes
// only when e->intersection is set is left out; the environment never has
// it set on the subtype path (init_stenv, subtype.c:1842).

// varBinding is jl_varbinding_t (subtype.c:67-91).
type varBinding struct {
	v           *TypeVar
	lb, ub      any
	right       bool
	occursInv   int8
	occursCov   int8
	concrete    bool
	maxOffset   int8
	intValued   int8
	intersected bool
	depth0      int
	innervars   []*TypeVar
	prev        *varBinding
}

// unionState is jl_unionstate_t (subtype.c:48-53); the bit stack is a
// growable slice of words in place of the linked chunks.
type unionState struct {
	depth, more, used int
	stack             []uint32
}

// savedUnionState is jl_saved_unionstate_t.
type savedUnionState struct {
	depth, more, used int
	stack             []uint32
}

// stenv is jl_stenv_t (subtype.c:102-119), without the intersection fields.
type stenv struct {
	vars             *varBinding
	lunions, runions unionState
	envout           []any
	envidx           int
	invdepth         int
	loffset          int64
	calls            int
}

// maxSubtypeCalls bounds the work of one subtyping query. julia has no
// bound: a hostile type (unions nested inside unions on both sides) makes
// the ∀/∃ enumeration exponential, and an untrusted notebook cell can write
// one. Exceeding it raises an error instead of running for hours.
const maxSubtypeCalls = 1 << 22

func (e *stenv) lookup(v *TypeVar) *varBinding {
	for b := e.vars; b != nil; b = b.prev {
		if b.v == v {
			return b
		}
	}
	return nil
}

// statestackGet and statestackSet are statestack_get/_set
// (subtype.c:140-170).
func (st *unionState) get(i int) bool {
	w := i >> 5
	if w >= len(st.stack) {
		return false
	}
	return st.stack[w]&(1<<(uint(i)&31)) != 0
}

func (st *unionState) set(i int, val bool) {
	if i >= 32767 {
		throwf("StackOverflowError: too many unions in one subtyping query")
	}
	w := i >> 5
	for w >= len(st.stack) {
		st.stack = append(st.stack, 0)
	}
	if val {
		st.stack[w] |= 1 << (uint(i) & 31)
	} else {
		st.stack[w] &^= 1 << (uint(i) & 31)
	}
}

func (e *stenv) unions(R bool) *unionState {
	if R {
		return &e.runions
	}
	return &e.lunions
}

// nextUnionState is next_union_state (subtype.c:174-183).
func (e *stenv) nextUnionState(R bool) bool {
	st := e.unions(R)
	if st.more == 0 {
		return false
	}
	st.used = st.more
	st.set(st.used-1, true)
	return true
}

// pickUnionDecision is pick_union_decision (subtype.c:185-197).
func (e *stenv) pickUnionDecision(R bool) bool {
	st := e.unions(R)
	if st.depth >= st.used {
		st.set(st.used, false)
		st.used++
	}
	ui := st.get(st.depth)
	st.depth++
	if !ui {
		st.more = st.depth
	}
	return ui
}

// pickUnionElement is pick_union_element (subtype.c:199-208).
func (e *stenv) pickUnionElement(u any, R bool) any {
	for {
		uu, ok := u.(*Union)
		if !ok {
			return u
		}
		if e.pickUnionDecision(R) {
			u = uu.B
		} else {
			u = uu.A
		}
	}
}

// pushUnionState and popUnionState are the macros of subtype.c:210-243:
// they save and restore the first `used` bits.
func pushUnionState(src *unionState) savedUnionState {
	n := (src.used + 31) >> 5
	if n > len(src.stack) {
		n = len(src.stack)
	}
	return savedUnionState{src.depth, src.more, src.used, append([]uint32(nil), src.stack[:n]...)}
}

func popUnionState(dst *unionState, s savedUnionState) {
	dst.depth, dst.more, dst.used = s.depth, s.more, s.used
	for len(dst.stack) < len(s.stack) {
		dst.stack = append(dst.stack, 0)
	}
	copy(dst.stack, s.stack)
}

// savedEnv is jl_savedenv_t (subtype.c:256-262).
type savedEnv struct {
	roots  []any
	buf    []int8
	rdepth int
}

// saveEnv is save_env and re_save_env (subtype.c:264-332).
func (e *stenv) saveEnv(se *savedEnv) {
	se.roots = se.roots[:0]
	se.buf = se.buf[:0]
	for v := e.vars; v != nil; v = v.prev {
		se.roots = append(se.roots, v.lb, v.ub, v.innervars)
		se.buf = append(se.buf, v.occursInv, v.occursCov, v.maxOffset)
	}
	se.rdepth = e.runions.depth
}

// restoreEnv is restore_env (subtype.c:357-390).
func (e *stenv) restoreEnv(se *savedEnv) {
	i, j := 0, 0
	for v := e.vars; v != nil; v = v.prev {
		v.lb, v.ub = se.roots[i], se.roots[i+1]
		v.innervars, _ = se.roots[i+2].([]*TypeVar)
		i += 3
		v.occursInv, v.occursCov, v.maxOffset = se.buf[j], se.buf[j+1], se.buf[j+2]
		j += 3
	}
	e.runions.depth = se.rdepth
	if e.envout != nil && e.envidx < len(e.envout) {
		for k := e.envidx; k < len(e.envout); k++ {
			e.envout[k] = nil
		}
	}
}

func (e *stenv) flipOffset() { e.loffset = -e.loffset }

// canonicalBottomSuper maps supertype(TypeofBottom) to TypeofBottom: the
// two are equal although distinct (subtype.c:399-402).
func canonicalBottomSuper(a any) any {
	if a == any(TypeofBottomType.Super) {
		return TypeofBottomType
	}
	return a
}

// obviouslyEgal is obviously_egal (subtype.c:397-434).
func obviouslyEgal(a, b any) bool {
	a, b = canonicalBottomSuper(a), canonicalBottomSuper(b)
	if a == b {
		return true
	}
	if TypeOf(a) != TypeOf(b) {
		return false
	}
	switch x := a.(type) {
	case *DataType:
		y := b.(*DataType)
		if x.Name != y.Name || x.IsConcrete || y.IsConcrete || len(x.Parameters) != len(y.Parameters) {
			return false
		}
		for i := range x.Parameters {
			if !obviouslyEgal(x.Parameters[i], y.Parameters[i]) {
				return false
			}
		}
		return true
	case *Union:
		y := b.(*Union)
		return obviouslyEgal(x.A, y.A) && obviouslyEgal(x.B, y.B)
	case *UnionAll:
		y := b.(*UnionAll)
		return x.Var == y.Var && obviouslyEgal(x.Body, y.Body)
	case *Vararg:
		y := b.(*Vararg)
		return obviouslyEgal(unwrapVararg(x), unwrapVararg(y)) &&
			((x.N == nil && y.N == nil) || (x.N != nil && y.N != nil && obviouslyEgal(x.N, y.N)))
	case *TypeVar:
		return false
	}
	return !IsType(a) && Egal(a, b)
}

// obviouslyUnequal is obviously_unequal (subtype.c:436-505).
func obviouslyUnequal(a, b any) bool {
	a, b = canonicalBottomSuper(a), canonicalBottomSuper(b)
	if a == b {
		return false
	}
	if _, ok := a.(*UnionAll); ok {
		a = UnwrapUnionAll(a)
	}
	if _, ok := b.(*UnionAll); ok {
		b = UnwrapUnionAll(b)
	}
	if ad, ok := a.(*DataType); ok {
		if b == any(Bottom) {
			return true
		}
		if bd, ok := b.(*DataType); ok {
			if a == any(TypeofBottomType) && bd.Name == TypeTypeName {
				return obviouslyUnequal(Bottom, bd.TParam0())
			}
			if ad.Name == TypeTypeName && b == any(TypeofBottomType) {
				return obviouslyUnequal(ad.TParam0(), Bottom)
			}
			if ad.Name != bd.Name {
				return true
			}
			istuple := ad.Name == TupleTypeName
			if typeEqualityIsIdentity(a, b) {
				return true
			}
			var np int
			if istuple {
				na, nb := len(ad.Parameters), len(bd.Parameters)
				if IsVaTuple(ad) {
					na--
					if IsVaTuple(bd) {
						nb--
					}
				} else if IsVaTuple(bd) {
					nb--
				} else if na != nb {
					return true
				}
				np = min(na, nb)
			} else {
				np = len(ad.Parameters)
				if np != len(bd.Parameters) {
					return true
				}
			}
			for i := 0; i < np; i++ {
				if obviouslyUnequal(ad.Parameters[i], bd.Parameters[i]) {
					return true
				}
			}
		}
	} else if a == any(Bottom) {
		if _, ok := b.(*DataType); ok {
			return true
		}
	}
	if at, ok := a.(*TypeVar); ok {
		if bt, ok := b.(*TypeVar); ok && obviouslyUnequal(at.UB, bt.UB) {
			return true
		}
	}
	if ai, ok := a.(int64); ok {
		if bi, ok := b.(int64); ok && ai != bi {
			return true
		}
	} else if _, ok := b.(int64); ok {
		return true
	}
	_, as := a.(Symbol)
	_, bs := b.(Symbol)
	if (as || bs) && a != b {
		return true
	}
	return false
}

// inUnion is in_union (subtype.c:512-517).
func inUnion(u, x any) bool {
	if u == x {
		return true
	}
	uu, ok := u.(*Union)
	if !ok {
		return false
	}
	return inUnion(uu.A, x) || inUnion(uu.B, x)
}

// obviouslyInUnion is obviously_in_union (subtype.c:519-539).
func obviouslyInUnion(u, x any) bool {
	if xu, ok := x.(*Union); ok {
		return obviouslyInUnion(u, xu.A) && obviouslyInUnion(u, xu.B)
	}
	if uu, ok := u.(*Union); ok {
		return obviouslyInUnion(uu.A, x) || obviouslyInUnion(uu.B, x)
	}
	return obviouslyEgal(u, x)
}

// obviouslyDisjoint is obviously_disjoint (subtype.c:541-630).
func obviouslyDisjoint(a, b any, specificity bool) bool {
	if a == b || a == any(AnyType) || b == any(AnyType) {
		return false
	}
	if specificity && a == any(TypeofBottomType) {
		return false
	}
	if IsConcrete(a) && IsConcrete(b) && typeEqualityIsIdentity(a, b) {
		return true
	}
	a, b = UnwrapUnionAll(a), UnwrapUnionAll(b)
	if u, ok := a.(*Union); ok {
		return obviouslyDisjoint(u.A, b, specificity) && obviouslyDisjoint(u.B, b, specificity)
	}
	if u, ok := b.(*Union); ok {
		return obviouslyDisjoint(a, u.A, specificity) && obviouslyDisjoint(a, u.B, specificity)
	}
	ad, aok := a.(*DataType)
	bd, bok := b.(*DataType)
	if aok && bok {
		if ad.Name != bd.Name {
			temp := ad
			for temp != AnyType && temp.Name != bd.Name {
				temp = temp.Super
			}
			if temp == AnyType {
				temp = bd
				for temp != AnyType && temp.Name != ad.Name {
					temp = temp.Super
				}
				if temp == AnyType {
					return true
				}
				bd = temp
			} else {
				ad = temp
			}
			if specificity {
				return false
			}
		}
		istuple := ad.Name == TupleTypeName
		var np int
		if istuple {
			na, nb := len(ad.Parameters), len(bd.Parameters)
			if IsVaTuple(ad) {
				na--
				if IsVaTuple(bd) {
					nb--
				}
			} else if IsVaTuple(bd) {
				nb--
			} else if !specificity && na != nb {
				return true
			}
			np = min(na, nb)
		} else {
			np = len(ad.Parameters)
		}
		for i := 0; i < np; i++ {
			ai, bi := ad.Parameters[i], bd.Parameters[i]
			if isTypeVar(ai) || isTypeVar(bi) {
				continue
			}
			if IsType(ai) {
				if IsType(bi) {
					if istuple && (ai == any(Bottom) || bi == any(Bottom)) {
					} else if obviouslyDisjoint(ai, bi, specificity) {
						return true
					}
				} else if ai != any(AnyType) {
					return true
				}
			} else if IsType(bi) {
				if bi != any(AnyType) {
					return true
				}
			} else if !Egal(ai, bi) {
				return true
			}
		}
	} else if a == any(Bottom) || b == any(Bottom) {
		return true
	}
	return false
}

// simpleJoin is simple_join (subtype.c:634-651).
func simpleJoin(a, b any) any {
	if a == any(Bottom) || b == any(AnyType) || obviouslyEgal(a, b) {
		return b
	}
	if b == any(Bottom) || a == any(AnyType) {
		return a
	}
	if !(IsType(a) || isTypeVar(a)) || !(IsType(b) || isTypeVar(b)) {
		return AnyType
	}
	if IsKind(a) && IsTypeType(b) && TypeOf(b.(*DataType).TParam0()) == a {
		return a
	}
	if IsKind(b) && IsTypeType(a) && TypeOf(a.(*DataType).TParam0()) == b {
		return b
	}
	if tv, ok := a.(*TypeVar); ok && obviouslyEgal(b, tv.LB) {
		return a
	}
	if tv, ok := b.(*TypeVar); ok && obviouslyEgal(a, tv.LB) {
		return b
	}
	return simpleUnion(a, b)
}

// simpleMeet is simple_meet (subtype.c:657-674).
func simpleMeet(a, b any, overesi bool) any {
	if a == any(AnyType) || b == any(Bottom) || obviouslyEgal(a, b) {
		return b
	}
	if b == any(AnyType) || a == any(Bottom) {
		return a
	}
	if !(IsType(a) || isTypeVar(a)) || !(IsType(b) || isTypeVar(b)) {
		return Bottom
	}
	if IsKind(a) && IsTypeType(b) && TypeOf(b.(*DataType).TParam0()) == a {
		return b
	}
	if IsKind(b) && IsTypeType(a) && TypeOf(a.(*DataType).TParam0()) == b {
		return a
	}
	if tv, ok := a.(*TypeVar); ok && obviouslyEgal(b, tv.UB) {
		return a
	}
	if tv, ok := b.(*TypeVar); ok && obviouslyEgal(a, tv.UB) {
		return b
	}
	return simpleIntersect(a, b, overesi)
}

func isLong(v any) bool { _, ok := v.(int64); return ok }

// subtypeCcheck is subtype_ccheck (subtype.c:683-701).
func (e *stenv) subtypeCcheck(x, y any) bool {
	if xi, ok := x.(int64); ok {
		if yi, ok := y.(int64); ok {
			return xi == yi+e.loffset
		}
	}
	if x == y {
		return true
	}
	if x == any(Bottom) && IsType(y) {
		return true
	}
	if y == any(AnyType) && IsType(x) {
		return true
	}
	if _, ok := x.(*Union); ok && Egal(x, y) {
		return true
	}
	if _, ok := y.(*DataType); ok && x == any(AnyType) {
		return false
	}
	old := pushUnionState(&e.lunions)
	sub := e.localForallExistsSubtype(x, y, 0, 1)
	popUnionState(&e.lunions, old)
	return sub
}

// subtypeLeftVar is subtype_left_var (subtype.c:703-718).
func (e *stenv) subtypeLeftVar(x, y any, param int) bool {
	if xi, ok := x.(int64); ok {
		if yi, ok := y.(int64); ok {
			return xi == yi+e.loffset
		}
	}
	if _, ua := y.(*UnionAll); x == y && !ua {
		return true
	}
	if x == any(Bottom) && IsType(y) {
		return true
	}
	if y == any(AnyType) && IsType(x) {
		return true
	}
	if _, ok := x.(*Union); ok && Egal(x, y) {
		return true
	}
	if _, ok := y.(*DataType); ok && x == any(AnyType) {
		return false
	}
	return e.subtype(x, y, param)
}

// recordVarOccurrence is record_var_occurrence (subtype.c:722-738).
func (e *stenv) recordVarOccurrence(vb *varBinding, param int) {
	if vb != nil && param != 0 {
		if param == 2 && e.invdepth > vb.depth0 {
			if vb.occursInv < 2 {
				vb.occursInv++
			}
		} else if vb.occursCov < 2 {
			vb.occursCov++
		}
		if !vb.intersected {
			vb.maxOffset = -1
		}
	}
}

// varOutside is var_outside (subtype.c:741-750).
func (e *stenv) varOutside(x, y *TypeVar) bool {
	for b := e.vars; b != nil; b = b.prev {
		if b.v == x {
			return false
		}
		if b.v == y {
			return true
		}
	}
	return false
}

// varLt is var_lt (subtype.c:757-795).
func (e *stenv) varLt(b *TypeVar, a any, param int) bool {
	bb := e.lookup(b)
	if bb == nil {
		return e.subtypeLeftVar(b.UB, a, param)
	}
	e.recordVarOccurrence(bb, param)
	if e.loffset != 0 && !isTypeVar(a) && a != any(Bottom) && a != any(AnyType) {
		return false
	}
	if !bb.right {
		return e.subtypeLeftVar(bb.ub, a, param)
	}
	if bb.ub == a {
		return true
	}
	if !((bb.lb == any(Bottom) && !IsType(a) && !isTypeVar(a)) || e.subtypeCcheck(bb.lb, a)) {
		return false
	}
	bb.ub = simpleMeet(bb.ub, a, true)
	if at, ok := a.(*TypeVar); ok {
		aa := e.lookup(at)
		if aa != nil && !aa.right && inUnion(bb.lb, a) && bb.depth0 != aa.depth0 && e.varOutside(b, at) {
			return e.subtypeLeftVar(aa.ub, aa.lb, param)
		}
	}
	return true
}

// varGt is var_gt (subtype.c:798-827).
func (e *stenv) varGt(b *TypeVar, a any, param int) bool {
	bb := e.lookup(b)
	if bb == nil {
		return e.subtypeLeftVar(a, b.LB, param)
	}
	e.recordVarOccurrence(bb, param)
	if e.loffset != 0 && !isTypeVar(a) && a != any(Bottom) && a != any(AnyType) {
		return false
	}
	if !bb.right {
		return e.subtypeLeftVar(a, bb.lb, param)
	}
	if bb.lb == a {
		return true
	}
	if !((bb.ub == any(AnyType) && !IsType(a) && !isTypeVar(a)) || e.subtypeCcheck(a, bb.ub)) {
		return false
	}
	bb.lb = simpleJoin(bb.lb, a)
	if at, ok := a.(*TypeVar); ok {
		aa := e.lookup(at)
		if aa != nil && !aa.right && bb.depth0 != aa.depth0 && param == 2 && e.varOutside(b, at) {
			return e.subtypeLeftVar(aa.ub, aa.lb, param)
		}
	}
	return true
}

// subtypeVar is subtype_var (subtype.c:829-851).
func (e *stenv) subtypeVar(b *TypeVar, a any, R bool, param int) bool {
	if ai, ok := a.(int64); ok && e.loffset != 0 {
		oldOffset := e.loffset
		if R {
			oldOffset = -e.loffset
		}
		na := ai + oldOffset
		e.loffset = 0
		var sub bool
		if R {
			sub = e.varGt(b, na, param)
			e.loffset = -oldOffset
		} else {
			sub = e.varLt(b, na, param)
			e.loffset = oldOffset
		}
		return sub
	}
	if R {
		return e.varGt(b, a, param)
	}
	return e.varLt(b, a, param)
}

// isLeafBound is is_leaf_bound (subtype.c:856-869).
func isLeafBound(v any) bool {
	if v == any(Bottom) {
		return true
	}
	if d, ok := v.(*DataType); ok {
		if d.Name.Abstract {
			return IsTypeType(v)
		}
		return d.IsConcrete
	}
	return !IsType(v) && !isTypeVar(v)
}

func isLeafTypeVar(v *TypeVar) bool { return isLeafBound(v.LB) }

// widenType is widen_Type (subtype.c:876-887).
func widenType(t any) any {
	if IsTypeType(t) && !isTypeVar(t.(*DataType).TParam0()) {
		return TypeOf(t.(*DataType).TParam0())
	}
	if u, ok := t.(*Union); ok {
		a, b := widenType(u.A), widenType(u.B)
		if a == b {
			return a
		}
	}
	return t
}

// fixInferredVarBound is fix_inferred_var_bound (subtype.c:896-914).
func fixInferredVarBound(v *TypeVar, ty any) any {
	if ty == nil {
		return v
	}
	if !isTypeVar(ty) && HasFreeTypeVars(ty) {
		var vs []*TypeVar
		findFreeTypeVars(ty, nil, &vs)
		ans := ty
		for _, fv := range vs {
			ans = TypeUnionAll(fv, ans)
		}
		return &TypeVar{Name: v.Name, LB: Bottom, UB: ans}
	}
	return ty
}

// varOccursInside is var_occurs_inside (subtype.c:2876-2911).
func varOccursInside(v any, tv *TypeVar, inside, wantInv bool) bool {
	if v == any(tv) {
		return inside
	}
	switch x := v.(type) {
	case *Union:
		return varOccursInside(x.A, tv, inside, wantInv) || varOccursInside(x.B, tv, inside, wantInv)
	case *UnionAll:
		if x.Var == tv {
			return false
		}
		if varOccursInside(x.Var.LB, tv, inside, wantInv) || varOccursInside(x.Var.UB, tv, inside, wantInv) {
			return true
		}
		return varOccursInside(x.Body, tv, inside, wantInv)
	case *Vararg:
		if x.T != nil {
			if varOccursInside(x.T, tv, inside || !wantInv, wantInv) {
				return true
			}
			return x.N != nil && varOccursInside(x.N, tv, true, wantInv)
		}
	case *DataType:
		istuple := x.Name == TupleTypeName
		for _, p := range x.Parameters {
			if varOccursInside(p, tv, inside || !wantInv || !istuple, wantInv) {
				return true
			}
		}
	}
	return false
}

func varOccursInvariant(v any, tv *TypeVar) bool { return varOccursInside(v, tv, false, true) }

// renameUnionAll is jl_rename_unionall (jltypes.c:1538-1548).
func renameUnionAll(u *UnionAll) *UnionAll {
	v := &TypeVar{Name: u.Var.Name, LB: u.Var.LB, UB: u.Var.UB}
	t := instType(u.Body, &typeEnv{u.Var, v, nil}, nil, false)
	return &UnionAll{Var: v, Body: t}
}

// unaliasUnionAll is unalias_unionall (subtype.c:925-953).
func (e *stenv) unaliasUnionAll(u *UnionAll) *UnionAll {
	for b := e.vars; b != nil; b = b.prev {
		aliased := b.v == u.Var ||
			(b.lb != b.v.LB && HasTypeVar(b.lb, u.Var)) ||
			(b.ub != b.v.UB && HasTypeVar(b.ub, u.Var))
		if !aliased {
			for _, iv := range b.innervars {
				if iv == u.Var {
					aliased = true
					break
				}
			}
		}
		if aliased {
			return renameUnionAll(u)
		}
	}
	return u
}

// subtypeUnionAll is subtype_unionall (subtype.c:955-1048).
func (e *stenv) subtypeUnionAll(t any, u *UnionAll, R bool, param int) bool {
	u = e.unaliasUnionAll(u)
	vb := &varBinding{v: u.Var, lb: u.Var.LB, ub: u.Var.UB, right: R, depth0: e.invdepth, prev: e.vars}
	e.vars = vb
	var ans bool
	if R {
		e.envidx++
		ans = e.subtype(t, u.Body, param)
		e.envidx--
		if vb.occursInv == 0 {
			vb.lb = widenType(vb.lb)
		}
	} else {
		ans = e.subtype(u.Body, t, param)
	}
	diagonal := vb.occursCov > 1 && !varOccursInvariant(u.Body, u.Var)
	if ans && (vb.concrete || (diagonal && isLeafTypeVar(u.Var))) {
		if vb.concrete && !diagonal && !isLeafBound(vb.ub) {
			ans = false
		} else if lbv, ok := vb.lb.(*TypeVar); ok {
			if vlb := e.lookup(lbv); vlb != nil {
				vlb.concrete = true
			}
		} else if !isLeafBound(vb.lb) {
			ans = false
		}
	}
	e.vars = vb.prev
	if !ans {
		return false
	}
	if vb.lb != vb.ub {
		for b := e.vars; b != nil; b = b.prev {
			vu, vl := b.ub, b.lb
			if b.depth0 != vb.depth0 &&
				((vu != any(vb.v) && b.v.UB != vu && varOccursInside(vu, vb.v, false, false)) ||
					(vl != any(vb.v) && b.v.LB != vl && varOccursInside(vl, vb.v, false, false))) {
				ans = false
				break
			}
		}
	}
	if R && ans && e.envidx < len(e.envout) {
		var val any
		switch {
		case vb.intValued != 0 && vb.lb == any(AnyType):
			val = &Vararg{}
		case vb.occursInv == 0 && vb.lb != any(Bottom):
			if isLeafBound(vb.lb) {
				val = vb.lb
			} else {
				val = &TypeVar{Name: u.Var.Name, LB: Bottom, UB: vb.lb}
			}
		case vb.lb == vb.ub:
			val = vb.lb
		case vb.lb != any(Bottom):
			val = vb.lb
		case vb.lb == u.Var.LB && vb.ub == u.Var.UB:
			val = u.Var
		default:
			val = &TypeVar{Name: u.Var.Name, LB: vb.lb, UB: vb.ub}
		}
		if old := e.envout[e.envidx]; old != nil && !Egal(old, val) {
			e.envout[e.envidx] = u.Var
		} else {
			e.envout[e.envidx] = val
		}
	}
	return ans
}

// checkVarargLength is check_vararg_length (subtype.c:1051-1066).
func (e *stenv) checkVarargLength(v any, n int64) bool {
	N := v.(*Vararg).N
	if N != nil {
		e.invdepth++
		ans := e.subtype(n, N, 2) && e.subtype(N, n, 0)
		e.invdepth--
		if !ans {
			return false
		}
	}
	return true
}

// subtypeTupleVarargs is subtype_tuple_varargs (subtype.c:1070-1218).
func (e *stenv) subtypeTupleVarargs(vtx, vty *Vararg, lastx, lasty any, vx, vy, xReps int64, param int) bool {
	xp0, xp1 := unwrapVararg(vtx), vtx.N
	yp0, yp1 := unwrapVararg(vty), vty.N
	var xlv, ylv *varBinding
	if t, ok := xp1.(*TypeVar); ok {
		xlv = e.lookup(t)
	}
	if t, ok := yp1.(*TypeVar); ok {
		ylv = e.lookup(t)
	}
	var maxOffsetX, maxOffsetY int8
	if xlv != nil {
		maxOffsetX = xlv.maxOffset
	}
	if ylv != nil {
		maxOffsetY = ylv.maxOffset
	}
	xl, yl := xp1, yp1
	if xlv != nil {
		xl = xlv.lb
	}
	if ylv != nil {
		yl = ylv.lb
	}
	skip := false
	if xp1 == nil {
		if yl != nil && isLong(yl) {
			return false
		}
	} else if xi, ok := xl.(int64); ok && xi+1 == vx {
		if yl != nil {
			if yi, ok := yl.(int64); ok {
				return yi+1 == vy
			}
		} else {
			skip = true
		}
	}
	if !skip {
		xSame := vx > 1 || (lastx != nil && obviouslyEgal(xp0, lastx))
		ySame := vy > 1 || (lasty != nil && obviouslyEgal(yp0, lasty))
		if ySame && xSame {
			xReps++
		} else {
			xReps = 1
		}
		switch {
		case xReps > 2:
		case xSame && e.runions.depth == 0 && ySame && !HasFreeTypeVars(xp0) && !HasFreeTypeVars(yp0):
		case (e.runions.depth == 0 && !HasFreeTypeVars(xp0) || e.runions.depth != 0 && IsConcrete(xp0)) && !HasFreeTypeVars(yp0):
			if !Subtype(xp0, yp0) {
				return false
			}
		default:
			if !e.subtype(xp0, yp0, param) {
				return false
			}
			if xReps < 2 && !e.subtype(xp0, yp0, 1) {
				return false
			}
		}
	}
	// constrain_length:
	if yp1 == nil {
		return true
	}
	if xp1 == nil {
		yl := yp1
		var ylv *varBinding
		if t, ok := yl.(*TypeVar); ok {
			ylv = e.lookup(t)
			if ylv != nil {
				yl = ylv.lb
			}
		}
		if isLong(yl) {
			return false
		}
		if ylv != nil {
			if ylv.depth0 != e.invdepth || ylv.lb != any(Bottom) || ylv.ub != any(AnyType) {
				return false
			}
			ylv.intValued = 1
		}
		e.invdepth++
		ans := e.subtype(AnyType, yp1, 2)
		if ylv != nil && !ylv.intersected {
			ylv.maxOffset = maxOffsetY
		}
		e.invdepth--
		return ans
	}
	e.invdepth++
	var bxp1, byp1 *varBinding
	if t, ok := xp1.(*TypeVar); ok {
		bxp1 = e.lookup(t)
	}
	if t, ok := yp1.(*TypeVar); ok {
		byp1 = e.lookup(t)
	}
	if bxp1 != nil {
		if bxp1.intValued == 0 {
			bxp1.intValued = 1
		}
		if isLong(bxp1.lb) {
			xp1 = bxp1.lb
		}
	}
	if byp1 != nil {
		if byp1.intValued == 0 {
			byp1.intValued = 1
		}
		if isLong(byp1.lb) {
			yp1 = byp1.lb
		}
	}
	var ans bool
	xi, xok := xp1.(int64)
	yi, yok := yp1.(int64)
	if xok && yok {
		ans = xi-vx == yi-vy
	} else {
		if xok && vx != vy {
			xp1 = xi + vy - vx
			vx = vy
		}
		if yok && vy != vx {
			yp1 = yi + vx - vy
			vy = vx
		}
		e.loffset = vx - vy
		ans = e.forallExistsEqual(xp1, yp1)
		e.loffset = 0
	}
	if ylv != nil && !ylv.intersected {
		ylv.maxOffset = maxOffsetY
	}
	if xlv != nil && !xlv.intersected {
		xlv.maxOffset = maxOffsetX
	}
	e.invdepth--
	return ans
}

// subtypeTupleTail is subtype_tuple_tail (subtype.c:1220-1312).
func (e *stenv) subtypeTupleTail(xd, yd *DataType, param int) bool {
	lx, ly := int64(len(xd.Parameters)), int64(len(yd.Parameters))
	var i, j, vx, vy int64
	xReps := int64(1)
	var lastx, lasty, xi, yi any
	for {
		if i < lx {
			xi = xd.Parameters[i]
			if _, va := xi.(*Vararg); i == lx-1 && (vx != 0 || va) {
				vx++
			}
		}
		if j < ly {
			yi = yd.Parameters[j]
			if _, va := yi.(*Vararg); j == ly-1 && (vy != 0 || va) {
				vy++
			}
		}
		if i >= lx {
			break
		}
		allVarargs := vx != 0 && vy != 0
		if !allVarargs && vy == 1 {
			if unwrapVararg(yi.(*Vararg)) == any(AnyType) {
				xi = xd.Parameters[lx-1]
				if _, ok := xi.(*Vararg); ok {
					allVarargs = true
					vy += lx - i - 1
					vx = 1
				} else {
					break
				}
			}
		}
		if allVarargs {
			return e.subtypeTupleVarargs(xi.(*Vararg), yi.(*Vararg), lastx, lasty, vx, vy, xReps, param)
		}
		if j >= ly {
			return vx != 0
		}
		if vx != 0 {
			xi = unwrapVararg(xi.(*Vararg))
		}
		if vy != 0 {
			yi = unwrapVararg(yi.(*Vararg))
		}
		xSame := vx > 1 || (lastx != nil && obviouslyEgal(xi, lastx))
		ySame := vy > 1 || (lasty != nil && obviouslyEgal(yi, lasty))
		if ySame && xSame {
			xReps++
		} else {
			xReps = 1
		}
		switch {
		case xReps > 2:
		case xSame && e.runions.depth == 0 &&
			((ySame && !HasFreeTypeVars(xi) && !HasFreeTypeVars(yi)) ||
				(yi == lastx && vx == 0 && vy != 0 && IsConcrete(xi))):
		case (e.runions.depth == 0 && !HasFreeTypeVars(xi) || e.runions.depth != 0 && IsConcrete(xi)) && !HasFreeTypeVars(yi):
			if !Subtype(xi, yi) {
				return false
			}
		default:
			if !e.subtype(xi, yi, param) {
				return false
			}
		}
		lastx, lasty = xi, yi
		if i < lx-1 || vx == 0 {
			i++
		}
		if j < ly-1 || vy == 0 {
			j++
		}
	}
	if vy != 0 && vx == 0 && lx+1 >= ly {
		if !e.checkVarargLength(yi, lx+1-ly) {
			return false
		}
	}
	return true
}

// subtypeTuple is subtype_tuple (subtype.c:1314-1376).
func (e *stenv) subtypeTuple(xd, yd *DataType, param int) bool {
	lx, ly := int64(len(xd.Parameters)), int64(len(yd.Parameters))
	if lx == 0 && ly == 0 {
		return true
	}
	vvx, vvy := varargNone, varargNone
	var xbb *varBinding
	var xva, yva any
	if lx > 0 {
		xva = xd.Parameters[lx-1]
		vvx = kindOfVararg(xva)
		if vvx == varargBound {
			xbb = e.lookup(xva.(*Vararg).N.(*TypeVar))
		}
	}
	if ly > 0 {
		yva = yd.Parameters[ly-1]
		vvy = kindOfVararg(yva)
	}
	if vvx != varargNone && vvx != varargInt && (xbb == nil || !isLong(xbb.lb)) {
		if vvx == varargUnbound || (xbb != nil && !xbb.right) {
			if vvy == varargNone || vvy == varargInt {
				return false
			} else if lx < ly {
				return false
			}
		} else if vvy == varargNone && !e.checkVarargLength(xva, ly+1-lx) {
			return false
		}
	} else {
		nx := lx
		if vvx == varargInt {
			nx += varargLength(xva) - 1
		} else if xbb != nil && isLong(xbb.lb) {
			nx += xbb.lb.(int64) - 1
		}
		ny := ly
		if vvy == varargInt {
			ny += varargLength(yva) - 1
		} else if vvy != varargNone {
			ny--
		}
		if vvy == varargNone || vvy == varargInt {
			if nx != ny {
				return false
			}
		} else if ny > nx {
			return false
		}
	}
	if param == 0 {
		param = 1
	}
	return e.subtypeTupleTail(xd, yd, param)
}

// subtype is subtype (subtype.c:1385-1566).
func (e *stenv) subtype(x, y any, param int) bool {
	e.calls++
	if e.calls > maxSubtypeCalls {
		throwf("subtyping %s exceeded %d steps", "query", maxSubtypeCalls)
	}
	if _, ok := x.(*Union); ok {
		if obviouslyEgal(x, y) {
			return true
		}
		if yvar, ok := y.(*TypeVar); ok && e.runions.depth == 0 && !HasFreeTypeVars(x) {
			yb := e.lookup(yvar)
			if yb == nil || !yb.right || !e.hasExistsTypeVar(yb.ub) {
				return e.subtypeVar(yvar, x, true, param)
			}
		}
		x = e.pickUnionElement(x, false)
	}
	if _, ok := y.(*Union); ok {
		if obviouslyInUnion(y, x) {
			return true
		}
		if xu, ok := x.(*UnionAll); ok {
			return e.subtypeUnionAll(y, xu, false, param)
		}
		ui := true
		if xv, ok := x.(*TypeVar); ok {
			xx := e.lookup(xv)
			ui = ((xx != nil && xx.right) || HasFreeTypeVars(y)) && e.pickUnionDecision(true)
		}
		if ui {
			y = e.pickUnionElement(y, true)
		}
	}
	if xv, ok := x.(*TypeVar); ok {
		if yv, ok := y.(*TypeVar); ok {
			if x == y {
				return true
			}
			xx, yy := e.lookup(xv), e.lookup(yv)
			xub, ylb := xv.UB, yv.LB
			if xx != nil {
				xub = xx.ub
			}
			if yy != nil {
				ylb = yy.lb
			}
			xr := xx != nil && xx.right
			yr := yy != nil && yy.right
			if xr {
				if yy != nil {
					e.recordVarOccurrence(yy, param)
				}
				if yr {
					e.recordVarOccurrence(xx, param)
					return e.subtype(xx.lb, yy.ub, 0)
				}
				return e.varLt(xv, y, param)
			} else if yr {
				if xx != nil {
					e.recordVarOccurrence(xx, param)
				}
				return e.varGt(yv, x, param)
			}
			return e.subtype(xub, y, param) || e.subtype(x, ylb, param)
		}
		if yu, ok := y.(*UnionAll); ok {
			xb := e.lookup(xv)
			xub := xv.UB
			if xb != nil {
				xub = xb.ub
			}
			if (xb == nil || !xb.right) && xub != y {
				return e.subtypeUnionAll(x, yu, true, param)
			}
		}
		return e.subtypeVar(xv, y, false, param)
	}
	if yv, ok := y.(*TypeVar); ok {
		return e.subtypeVar(yv, x, true, param)
	}
	if y == any(AnyType) && !HasFreeTypeVars(x) {
		return true
	}
	if x == any(Bottom) && !HasFreeTypeVars(y) {
		return true
	}
	ux, uy := UnwrapUnionAll(x), UnwrapUnionAll(y)
	xd, xdok := ux.(*DataType)
	yd, ydok := uy.(*DataType)
	if (x != ux || y != uy) && y != any(AnyType) && xdok && ydok && !IsTypeType(ux) {
		if uy == any(AnyType) {
			return true
		}
		for xd != nil && xd != AnyType && xd.Name != yd.Name {
			xd = xd.Super
		}
		if xd == AnyType {
			return false
		}
	}
	if xu, ok := x.(*UnionAll); ok {
		if x == y && !(e.envidx < len(e.envout)) {
			return true
		}
		return e.subtypeUnionAll(y, xu, false, param)
	}
	if yu, ok := y.(*UnionAll); ok {
		return e.subtypeUnionAll(x, yu, true, param)
	}
	xd, xdok = x.(*DataType)
	yd, ydok = y.(*DataType)
	if xdok && ydok {
		if x == y {
			return true
		}
		if y == any(AnyType) {
			return true
		}
		if IsTypeType(x) && !IsTypeType(y) {
			tp0 := xd.TParam0()
			if !isTypeVar(tp0) {
				return TypeOf(tp0) == any(yd)
			}
			return false
		}
		if IsTypeType(y) && !IsTypeType(x) && x != any(TypeofBottomType) {
			tp0 := yd.TParam0()
			if !isTypeVar(tp0) || !IsKind(x) {
				return false
			}
			return e.subtype(typeTypePrimary, y, param)
		}
		for xd != AnyType && xd.Name != yd.Name {
			if xd.Super == nil {
				throwf("circular type parameter constraint in definition of %s", xd.Name.Name)
			}
			xd = xd.Super
		}
		if xd == AnyType {
			return false
		}
		if xd.Name == TupleTypeName {
			return e.subtypeTuple(xd, yd, param)
		}
		ans := true
		e.invdepth++
		for i := range xd.Parameters {
			xi, yi := xd.Parameters[i], yd.Parameters[i]
			if !(xi == yi || e.forallExistsEqual(xi, yi)) {
				ans = false
				break
			}
		}
		e.invdepth--
		return ans
	}
	if IsType(y) {
		return x == any(Bottom)
	}
	if xi, ok := x.(int64); ok {
		if yi, ok := y.(int64); ok {
			return xi == yi+e.loffset
		}
	}
	return Egal(x, y)
}

// isIndefiniteLengthTupleType is is_indefinite_length_tuple_type
// (subtype.c:1568-1575).
func isIndefiniteLengthTupleType(x any) bool {
	d, ok := UnwrapUnionAll(x).(*DataType)
	if !ok || d.Name != TupleTypeName {
		return false
	}
	n := len(d.Parameters)
	return n > 0 && kindOfVararg(d.Parameters[n-1]) == varargUnbound
}

// isDefiniteLengthTupleType is is_definite_length_tuple_type
// (subtype.c:1577-1589).
func isDefiniteLengthTupleType(x any) bool {
	if tv, ok := x.(*TypeVar); ok {
		x = tv.UB
	}
	d, ok := UnwrapUnionAll(x).(*DataType)
	if !ok || d.Name != TupleTypeName {
		return false
	}
	n := len(d.Parameters)
	if n == 0 {
		return true
	}
	k := kindOfVararg(d.Parameters[n-1])
	return k == varargNone || k == varargInt
}

// isExistsTypeVar is is_exists_typevar (subtype.c:1591-1597).
func (e *stenv) isExistsTypeVar(x any) bool {
	tv, ok := x.(*TypeVar)
	if !ok {
		return false
	}
	vb := e.lookup(tv)
	return vb != nil && vb.right
}

// hasExistsTypeVar is has_exists_typevar (subtype.c:1599-1614).
func (e *stenv) hasExistsTypeVar(x any) bool {
	var env *typeEnv
	for v := e.vars; v != nil; v = v.prev {
		if v.right {
			env = &typeEnv{v.v, nil, env}
		}
	}
	return env != nil && hasBoundTypeVars(x, env)
}

// localForallExistsSubtype is local_forall_exists_subtype
// (subtype.c:1618-1697).
func (e *stenv) localForallExistsSubtype(x, y any, param, limitSlow int) bool {
	oldRmore := e.runions.more
	if obviouslyInUnion(y, x) {
		return true
	}
	kindx, kindy := !HasFreeTypeVars(x), !HasFreeTypeVars(y)
	if kindx && kindy {
		return Subtype(x, y)
	}
	hasExists := (!kindx && e.hasExistsTypeVar(x)) || (!kindy && e.hasExistsTypeVar(y))
	if !hasExists {
		old := pushUnionState(&e.runions)
		e.lunions.used, e.runions.used = 0, 0
		e.lunions.depth, e.runions.depth = 0, 0
		e.lunions.more, e.runions.more = 0, 0
		sub := e.forallExistsSubtype(x, y, param)
		popUnionState(&e.runions, old)
		return sub
	}
	var sub bool
	if e.isExistsTypeVar(x) != e.isExistsTypeVar(y) {
		e.lunions.used = 0
		for {
			e.lunions.more, e.lunions.depth = 0, 0
			sub = e.subtype(x, y, param)
			if !sub || !e.nextUnionState(false) {
				break
			}
		}
		return sub
	}
	if limitSlow == -1 {
		if kindx || kindy {
			limitSlow = 1
		} else {
			limitSlow = 0
		}
	}
	var se savedEnv
	e.saveEnv(&se)
	count, iniCount := 0, 0
	limited := false
	var latest savedUnionState
	for {
		count = iniCount
		if iniCount == 0 {
			e.lunions.used = 0
		} else {
			popUnionState(&e.lunions, latest)
		}
		for {
			e.lunions.more, e.lunions.depth = 0, 0
			if count < 4 {
				count++
			}
			sub = e.subtype(x, y, param)
			if limitSlow != 0 && count == 4 {
				limited = true
			}
			if !sub || !e.nextUnionState(false) {
				break
			}
			if limited || e.runions.more == oldRmore {
				iniCount = count
				latest = pushUnionState(&e.lunions)
				e.saveEnv(&se)
				e.runions.more = oldRmore
			}
		}
		if sub || e.runions.more == oldRmore {
			break
		}
		e.nextUnionState(true)
		e.restoreEnv(&se)
		e.runions.more = oldRmore
	}
	if sub && limited {
		e.runions.more = oldRmore
	}
	return sub
}

// equalVar is equal_var (subtype.c:1699-1733).
func (e *stenv) equalVar(v *TypeVar, x any) bool {
	vb := e.lookup(v)
	e.recordVarOccurrence(vb, 2)
	if vb == nil {
		return e.localForallExistsSubtype(x, v.LB, 2, boolBit(!HasFreeTypeVars(x))) &&
			e.localForallExistsSubtype(v.UB, x, 0, 0)
	}
	if !vb.right {
		return e.localForallExistsSubtype(x, vb.lb, 2, boolBit(!HasFreeTypeVars(x))) &&
			e.localForallExistsSubtype(vb.ub, x, 0, 0)
	}
	if vb.lb == x {
		return e.varLt(v, x, 0)
	}
	if !e.subtypeCcheck(x, vb.ub) {
		return false
	}
	vb.lb = simpleJoin(vb.lb, x)
	if vb.ub == x {
		return true
	}
	if !e.subtypeCcheck(vb.lb, x) {
		return false
	}
	vb.ub = x
	return true
}

// forallExistsEqual is forall_exists_equal (subtype.c:1735-1781).
func (e *stenv) forallExistsEqual(x, y any) bool {
	if obviouslyEgal(x, y) {
		return true
	}
	if (isIndefiniteLengthTupleType(x) && isDefiniteLengthTupleType(y)) ||
		(isDefiniteLengthTupleType(x) && isIndefiniteLengthTupleType(y)) {
		return false
	}
	xd, xok := x.(*DataType)
	yd, yok := y.(*DataType)
	if xok && yok {
		if xd.Name != yd.Name {
			return false
		}
		if xd.Name != TupleTypeName {
			return e.subtype(x, y, 2)
		}
	}
	xu, xuok := x.(*Union)
	yu, yuok := y.(*Union)
	if xuok && yuok {
		if !e.pickUnionDecision(true) {
			return e.forallExistsEqual(xu.A, yu.A) && e.forallExistsEqual(xu.B, yu.B)
		}
	}
	if yv, ok := y.(*TypeVar); ok && e.loffset == 0 && IsType(x) {
		return e.equalVar(yv, x)
	}
	old := pushUnionState(&e.lunions)
	sub := e.localForallExistsSubtype(x, y, 2, -1)
	if sub {
		e.flipOffset()
		sub = e.localForallExistsSubtype(y, x, 0, 0)
		e.flipOffset()
	}
	popUnionState(&e.lunions, old)
	return sub
}

// existsSubtype is exists_subtype (subtype.c:1783-1805).
func (e *stenv) existsSubtype(x, y any, se *savedEnv, param int) bool {
	e.runions.used = 0
	for {
		e.runions.depth, e.runions.more = 0, 0
		e.lunions.depth, e.lunions.more = 0, 0
		if e.subtype(x, y, param) {
			return true
		}
		if e.nextUnionState(true) {
			oldidx := e.envidx
			e.envidx = len(e.envout)
			e.restoreEnv(se)
			e.envidx = oldidx
		} else {
			e.restoreEnv(se)
			return false
		}
	}
}

// forallExistsSubtype is forall_exists_subtype (subtype.c:1807-1828).
func (e *stenv) forallExistsSubtype(x, y any, param int) bool {
	var se savedEnv
	e.saveEnv(&se)
	e.lunions.used = 0
	var sub bool
	for {
		sub = e.existsSubtype(x, y, &se, param)
		if !sub || !e.nextUnionState(false) {
			break
		}
		e.saveEnv(&se)
	}
	return sub
}

// concreteMin is concrete_min (subtype.c:1867-1890).
func concreteMin(t any) int {
	t = UnwrapUnionAll(t)
	switch x := t.(type) {
	case *bottomType:
		return 1
	case *DataType:
		if IsTypeType(t) {
			return 0
		}
		if x.IsConcrete {
			return 1
		}
		return 2
	case *Vararg, *TypeVar:
		return 0
	case *Union:
		c := concreteMin(x.A)
		if c > 1 {
			return c
		}
		return c + concreteMin(x.B)
	}
	return 1
}

// findVarBody is find_var_body (subtype.c:1892-1927).
func findVarBody(t any, v *TypeVar) any {
	switch x := t.(type) {
	case *UnionAll:
		if x.Var == v {
			return x.Body
		}
		if b := findVarBody(x.Var.LB, v); b != nil {
			return b
		}
		if b := findVarBody(x.Var.UB, v); b != nil {
			return b
		}
		return findVarBody(x.Body, v)
	case *Union:
		if b := findVarBody(x.A, v); b != nil {
			return b
		}
		return findVarBody(x.B, v)
	case *Vararg:
		if x.T != nil {
			if b := findVarBody(x.T, v); b != nil {
				return b
			}
			if x.N != nil {
				return findVarBody(x.N, v)
			}
		}
	case *DataType:
		for _, p := range x.Parameters {
			if b := findVarBody(p, v); b != nil {
				return b
			}
		}
	}
	return nil
}

// obviousSubtype is obvious_subtype (subtype.c:1934-2235): ok reports that
// the answer sub could be determined quickly.
func obviousSubtype(x, y, y0 any, sub *bool) bool {
	if x == y || y == any(AnyType) {
		*sub = true
		return true
	}
	for {
		xu, ok := x.(*UnionAll)
		if !ok {
			break
		}
		yu, ok := y.(*UnionAll)
		if !ok {
			if obviousSubtype(UnwrapUnionAll(x), y, y0, sub) && !*sub {
				return true
			}
			return false
		}
		x, y = xu.Body, yu.Body
	}
	y = UnwrapUnionAll(y)
	x, y = canonicalBottomSuper(x), canonicalBottomSuper(y)
	if x == y || y == any(AnyType) {
		*sub = true
		return true
	}
	if xv, ok := x.(*TypeVar); ok {
		if xv.LB != any(Bottom) {
			return false
		}
		if obviousSubtype(xv.UB, y, y0, sub) && *sub {
			return true
		}
		return false
	}
	if yv, ok := y.(*TypeVar); ok {
		if yv.LB != any(Bottom) {
			return false
		}
		if obviousSubtype(x, yv.UB, y0, sub) && !*sub {
			return true
		}
		return false
	}
	if x == any(Bottom) {
		*sub = true
		return true
	}
	if y == any(Bottom) {
		*sub = false
		return true
	}
	if _, ok := x.(*Vararg); ok {
		if _, ok := y.(*Vararg); !ok {
			*sub = false
			return true
		}
		return false
	}
	if !IsType(x) || !IsType(y) {
		*sub = Egal(x, y)
		return true
	}
	if xu, ok := x.(*Union); ok {
		if obviousSubtype(xu.A, y, y0, sub) && *sub {
			if obviousSubtype(xu.B, y, y0, sub) && *sub {
				return true
			}
		}
		return false
	}
	if yu, ok := y.(*Union); ok {
		if obviousSubtype(x, yu.A, y0, sub) {
			if *sub {
				return true
			}
			if obviousSubtype(x, yu.B, y0, sub) {
				return true
			}
		} else if obviousSubtype(x, yu.B, y0, sub) {
			if *sub {
				return true
			}
		}
		return false
	}
	if x == any(AnyType) {
		*sub = false
		return true
	}
	// Here both are DataTypes: y is a type that is not Any, Bottom, a Union,
	// a TypeVar or a UnionAll (unwrapped above), and x is a type that is not
	// Bottom, Any, a Union, a TypeVar or a UnionAll (the loop above returns
	// unless both are UnionAlls, and then unwraps both). julia tests
	// jl_is_datatype on both (subtype.c:2027, 2037), which cannot fail here.
	yd := y.(*DataType)
	xd := x.(*DataType)
	istuple := yd.Name == TupleTypeName
	iscov := istuple
	uncertain := false
	if xd.Name != yd.Name {
		if IsTypeType(x) && IsKind(y) {
			t0 := xd.TParam0()
			if isTypeVar(t0) {
				return false
			}
			*sub = TypeOf(t0) == y
			return true
		}
		if IsTypeType(y) {
			t0 := yd.TParam0()
			if IsKind(x) && isTypeVar(t0) {
				return false
			}
			*sub = false
			return true
		}
		temp := xd
		for temp.Name != yd.Name {
			temp = temp.Super
			if temp == nil {
				return false
			}
			if temp == AnyType {
				*sub = false
				return true
			}
		}
		if obviousSubtype(temp, y, y0, sub) && *sub {
			return true
		}
		return false
	}
	// julia then asks obvious_subtype(x->name->wrapper, y) and returns when
	// it answers "subtype" (subtype.c:2074-2078). That answer cannot come:
	// x has parameters here (a parameterless x with y's name is y itself,
	// returned at the top), so its wrapper is a UnionAll, y is not one, and
	// obvious_subtype of a UnionAll against a non-UnionAll only ever answers
	// "not a subtype" (subtype.c:1940-1945). The call has no other effect,
	// so it is left out.
	npx, npy := len(xd.Parameters), len(yd.Parameters)
	vx, vy := varargNone, varargNone
	var vxt any
	nx, ny := npx, npy
	if istuple {
		if npx > 0 {
			xva := xd.Parameters[npx-1]
			vx = kindOfVararg(xva)
			if vx != varargNone {
				vxt = unwrapVararg(xva.(*Vararg))
				nx--
				if vx == varargInt {
					nx += int(varargLength(xva))
				}
			}
		}
		if npy > 0 {
			yva := yd.Parameters[npy-1]
			vy = kindOfVararg(yva)
			if vy != varargNone {
				ny--
				if vy == varargInt {
					ny += int(varargLength(yva))
				}
			}
		}
		if nx != ny || (vx != varargNone && vx != varargInt) || (vy != varargNone && vy != varargInt) {
			if vy == varargNone || vy == varargInt {
				if vx == varargNone || vx == varargInt || vx == varargUnbound || nx > ny || npx > ny {
					*sub = false
					return true
				}
			}
			if nx < ny {
				*sub = false
				return true
			}
			uncertain = true
		}
	} else if npx != npy {
		*sub = false
		return true
	}
	vyOff, vxOff := 0, 0
	if vy != varargNone {
		vyOff = 1
	}
	if vx != varargNone {
		vxOff = 1
	}
	i := 0
	for ; i < npy-vyOff; i++ {
		var a any
		if i >= npx-vxOff {
			a = vxt
		} else {
			a = xd.Parameters[i]
		}
		b := yd.Parameters[i]
		if iscov || isTypeVar(b) {
			if obviousSubtype(a, b, y0, sub) {
				if !*sub {
					return true
				}
				if HasFreeTypeVars(b) {
					uncertain = true
				}
			} else {
				uncertain = true
			}
		} else if !obviouslyEgal(a, b) {
			if obviousSubtype(a, b, y0, sub) {
				if !*sub {
					return true
				}
				if HasFreeTypeVars(b) {
					uncertain = true
				}
			} else {
				uncertain = true
			}
			if !HasFreeTypeVars(b) && obviousSubtype(b, a, y0, sub) {
				if !*sub {
					return true
				}
				if HasFreeTypeVars(a) {
					uncertain = true
				}
			} else {
				uncertain = true
			}
		}
	}
	if i < nx {
		var a1 any
		if vx != varargNone && i >= npx-1 {
			a1 = vxt
		} else {
			a1 = xd.Parameters[i]
		}
		b := unwrapVararg(yd.Parameters[i].(*Vararg))
		bv, bIsVar := b.(*TypeVar)
		if bIsVar {
			body := findVarBody(y0, bv)
			if body == nil {
				body = y0
			}
			if varOccursInvariant(body, bv) {
				return false
			}
		}
		if nx > npy && bIsVar && isLeafTypeVar(bv) && concreteMin(a1) > 1 {
			*sub = false
			return true
		}
		if a1u, ok := UnwrapUnionAll(a1).(*DataType); ok && IsTypeType(a1u) && IsType(a1u.TParam0()) {
			a1 = TypeOf(a1u.TParam0())
		}
		for ; i < nx; i++ {
			var a any
			if vx != varargNone && i >= npx-1 {
				a = vxt
			} else {
				a = xd.Parameters[i]
			}
			if i > npy && bIsVar && isLeafTypeVar(bv) {
				a2 := a
				if au, ok := UnwrapUnionAll(a).(*DataType); ok && IsTypeType(au) && IsType(au.TParam0()) {
					a2 = TypeOf(au.TParam0())
				}
				if !obviouslyEgal(a1, a2) {
					if obviousSubtype(a2, a1, y0, sub) {
						if !*sub {
							return true
						}
						if HasFreeTypeVars(a1) {
							uncertain = true
						}
					} else {
						uncertain = true
					}
					if obviousSubtype(a1, a2, y0, sub) {
						if !*sub {
							return true
						}
						if HasFreeTypeVars(a2) {
							uncertain = true
						}
					} else {
						uncertain = true
					}
				}
			}
			if obviousSubtype(a, b, y0, sub) {
				if !*sub {
					return true
				}
				if HasFreeTypeVars(b) {
					uncertain = true
				}
			} else {
				uncertain = true
			}
		}
	}
	if uncertain {
		return false
	}
	*sub = true
	return true
}

// subtypeEnv is jl_subtype_env (subtype.c:2246-2297) as a release build
// runs it (NDEBUG): an obvious answer of false, or of true when no
// environment is requested, is final.
func subtypeEnv(x, y any, env []any) bool {
	if y == any(AnyType) || x == any(Bottom) {
		return true
	}
	_, yua := y.(*UnionAll)
	_, yu := y.(*Union)
	if x == y || (TypeOf(x) == TypeOf(y) && (yua || yu) && TypesEgal(x, y)) {
		if len(env) != 0 {
			ua := x.(*UnionAll)
			for i := range env {
				env[i] = ua.Var
				ua, _ = ua.Body.(*UnionAll)
			}
		}
		return true
	}
	var obvious bool
	if obviousSubtype(x, y, y, &obvious) {
		if !obvious {
			return false
		}
		if len(env) == 0 {
			return true
		}
	}
	for i := range env {
		env[i] = nil
	}
	e := &stenv{envout: env}
	sub := e.forallExistsSubtype(x, y, 0)
	if env != nil {
		ub := y.(*UnionAll)
		for i := range env {
			env[i] = fixInferredVarBound(ub.Var, env[i])
			ub, _ = ub.Body.(*UnionAll)
		}
	}
	return sub
}

// Subtype is jl_subtype: x <: y. It panics with *Error when the query
// exceeds the work bound (see maxSubtypeCalls); SubtypeChecked returns it.
func Subtype(x, y any) bool { return subtypeEnv(x, y, nil) }

// SubtypeChecked is Subtype returning the work-bound error instead of
// panicking.
func SubtypeChecked(x, y any) (sub bool, err error) {
	defer catchError(&err)
	return Subtype(x, y), nil
}

// SubtypeEnv is jl_subtype_env: x <: y, filling env with the values of the
// variables of y's outer UnionAlls (len(env) at most their number).
func SubtypeEnv(x, y any, env []any) (sub bool, err error) {
	defer catchError(&err)
	return subtypeEnv(x, y, env), nil
}

// TypesEqual is jl_types_equal (subtype.c:2319-2399), release build.
func TypesEqual(a, b any) bool {
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
	ab, abKnown := false, false
	if b == any(AnyType) || a == any(Bottom) {
		ab, abKnown = true, true
	} else if obviousSubtype(a, b, b, &ab) {
		if !ab {
			return false
		}
		abKnown = true
	}
	ba, baKnown := false, false
	if a == any(AnyType) || b == any(Bottom) {
		ba, baKnown = true, true
	} else if obviousSubtype(b, a, a, &ba) {
		if !ba {
			return false
		}
		baKnown = true
	}
	if !abKnown {
		e := &stenv{}
		if !e.forallExistsSubtype(a, b, 0) {
			return false
		}
	}
	if !baKnown {
		e := &stenv{}
		ba = e.forallExistsSubtype(b, a, 0)
	}
	return ba
}

// hasIntersectTypeNotKind is jl_has_intersect_type_not_kind
// (subtype.c:2444-2459).
func hasIntersectTypeNotKind(t any) bool {
	t = UnwrapUnionAll(t)
	if t == any(AnyType) {
		return true
	}
	switch x := t.(type) {
	case *Union:
		return hasIntersectTypeNotKind(x.A) || hasIntersectTypeNotKind(x.B)
	case *TypeVar:
		return hasIntersectTypeNotKind(x.UB)
	case *DataType:
		return x.Name == TypeTypeName
	}
	return false
}

// Isa is jl_isa (subtype.c:2477-2529): isa(x, t).
func Isa(x, t any) bool {
	if t == any(AnyType) || TypeOf(x) == t {
		return true
	}
	if IsType(x) {
		if t == any(TypeType) {
			return true
		}
		if !HasFreeTypeVars(x) {
			if IsConcrete(t) {
				return false
			}
			if IsTypeType(t) {
				return TypesEqual(x, t.(*DataType).TParam0())
			}
			t2 := UnwrapUnionAll(t)
			if d, ok := t2.(*DataType); ok {
				if d.Name == TypeTypeName {
					tp := d.TParam0()
					if tv, ok := tp.(*TypeVar); ok {
						if tv.LB == any(Bottom) {
							for {
								tpv, ok := tp.(*TypeVar)
								if !ok {
									break
								}
								tp = tpv.UB
							}
							if !HasFreeTypeVars(tp) {
								return Subtype(x, tp)
							}
						} else if tv.UB == any(AnyType) {
							for {
								tpv, ok := tp.(*TypeVar)
								if !ok {
									break
								}
								tp = tpv.LB
							}
							if !HasFreeTypeVars(tp) {
								return Subtype(tp, x)
							}
						}
					}
				} else {
					return false
				}
			}
			if Subtype(TypeOf(x), t) {
				return true
			}
			if hasIntersectTypeNotKind(t2) {
				return Subtype(mustApply(TypeType, x), t)
			}
			return false
		}
	}
	if IsConcrete(t) {
		return false
	}
	return Subtype(TypeOf(x), t)
}
