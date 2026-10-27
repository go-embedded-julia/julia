package core

import "fmt"

// Error is a Julia error raised while building a type (ErrorException or
// TypeError in julia). Construction panics with it internally; the exported
// constructors recover it and return it.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func throwf(format string, args ...any) { panic(&Error{fmt.Sprintf(format, args...)}) }

// catchError turns a panic carrying *Error into a returned error.
func catchError(err *error) {
	if r := recover(); r != nil {
		e, ok := r.(*Error)
		if !ok {
			panic(r)
		}
		*err = e
	}
}

// try runs f and returns the *Error it raised, if any.
func try(f func()) (err error) {
	defer catchError(&err)
	f()
	return nil
}

// NewTypeVar is jl_new_typevar (builtins.c:1688-1701).
func NewTypeVar(name string, lb, ub any) (*TypeVar, error) {
	if lb != any(Bottom) && !IsType(lb) && !isTypeVar(lb) {
		return nil, &Error{fmt.Sprintf("TypeError: in TypeVar, in lower bound, expected Type, got %s", Show(lb))}
	}
	if ub != any(AnyType) && !IsType(ub) && !isTypeVar(ub) {
		return nil, &Error{fmt.Sprintf("TypeError: in TypeVar, in upper bound, expected Type, got %s", Show(ub))}
	}
	return &TypeVar{Name: name, LB: lb, UB: ub}, nil
}

func isTypeVar(v any) bool { _, ok := v.(*TypeVar); return ok }

// TypeUnionAll is jl_type_unionall (jltypes.c:907-948), without the
// deprecated wrapping of a bare Vararg.
func TypeUnionAll(v *TypeVar, body any) any {
	if body == any(v) {
		return v.UB
	}
	if !HasTypeVar(body, v) {
		return body
	}
	return &UnionAll{Var: v, Body: body}
}

// extractWrapper is extract_wrapper (jltypes.c:1941-1954).
func extractWrapper(t any) any {
	t = UnwrapUnionAll(t)
	switch x := t.(type) {
	case *DataType:
		return x.Name.Wrapper
	case *Union:
		if n := extractWrapper(x.A); n != nil {
			return n
		}
		return extractWrapper(x.B)
	case *TypeVar:
		return extractWrapper(x.UB)
	}
	return nil
}

// mayEqualWrapper replaces t by its wrapper when the two are equal types
// (the normalisation of jl_wrap_vararg and jl_tupletype_fill).
func mayEqualWrapper(t any) any {
	tw := extractWrapper(t)
	if tw != nil && t != tw && !HasFreeTypeVars(t) && TypesEqual(t, tw) {
		return tw
	}
	return t
}

// WrapVararg is jl_wrap_vararg with check set (jltypes.c:2791-2845).
func WrapVararg(t, n any) (v *Vararg, err error) {
	defer catchError(&err)
	return wrapVararg(t, n, true), nil
}

func wrapVararg(t, n any, check bool) *Vararg {
	if check {
		if n != nil {
			if _, ok := n.(*TypeVar); ok {
			} else if _, ok := UnwrapUnionAll(n).(*Union); ok {
			} else if k, ok := n.(int64); !ok {
				throwf("TypeError: in Vararg, in count, expected Int64, got %s", Show(n))
			} else if k < 0 {
				throwf("Vararg length is negative: %d", k)
			}
		}
		if t != nil {
			if !ValidTypeParam(t) {
				throwf("TypeError: in Vararg, in type, expected Type, got %s", Show(t))
			}
			t = mayEqualWrapper(normalizeUnionAlls(t))
		}
	}
	return &Vararg{T: t, N: n}
}

// mayDubstituteUB is _may_substitute_ub (jltypes.c:1956-2006).
func maySubstituteUB(v any, tv *TypeVar, insideInv bool, covCount *int) bool {
	for {
		if v == any(tv) {
			if insideInv {
				return false
			}
			*covCount++
			return *covCount <= 1 || IsConcrete(tv.UB)
		}
		for {
			ua, ok := v.(*UnionAll)
			if !ok {
				break
			}
			if ua.Var == tv {
				return true
			}
			if ua.Var.LB != any(Bottom) && !maySubstituteUB(ua.Var.LB, tv, insideInv, covCount) {
				return false
			}
			if ua.Var.UB != any(AnyType) && !maySubstituteUB(ua.Var.UB, tv, insideInv, covCount) {
				return false
			}
			v = ua.Body
		}
		switch x := v.(type) {
		case *DataType:
			invar := insideInv || x.Name != TupleTypeName
			for _, p := range x.Parameters {
				if !maySubstituteUB(p, tv, invar, covCount) {
					return false
				}
			}
			return true
		case *Union:
			if !maySubstituteUB(x.A, tv, insideInv, covCount) {
				return false
			}
			v = x.B
		case *Vararg:
			if x.T == nil {
				return true
			}
			if x.N != nil && !maySubstituteUB(x.N, tv, true, covCount) {
				return false
			}
			if !IsConcrete(tv.UB) {
				insideInv = true
			}
			v = x.T
		default:
			return true
		}
	}
}

// normalizeUnionAlls is normalize_unionalls (jltypes.c:2019-2055).
func normalizeUnionAlls(t any) any {
	switch u := t.(type) {
	case *Union:
		a, b := normalizeUnionAlls(u.A), normalizeUnionAlls(u.B)
		if a != u.A || b != u.B {
			return &Union{a, b}
		}
	case *UnionAll:
		body := normalizeUnionAlls(u.Body)
		if body != u.Body {
			u = &UnionAll{Var: u.Var, Body: body}
			t = u
		}
		n := 0
		if u.Var.LB == u.Var.UB || maySubstituteUB(body, u.Var, false, &n) {
			// a substitution julia cannot build is skipped (JL_TRY/JL_CATCH)
			var inst any
			if try(func() { inst = instType(u.Body, &typeEnv{u.Var, u.Var.UB, nil}, nil, true) }) == nil {
				t = inst
			}
		}
	}
	return t
}

// typeStack is jl_typestack_t: the datatypes being built, so that a field
// type referring to its own type finds the object under construction.
type typeStack struct {
	tt   *DataType
	prev *typeStack
}

// instType is inst_type_w_ with nothrow 0 (jltypes.c:2601-2747).
func instType(t any, env *typeEnv, stack *typeStack, check bool) any {
	switch x := t.(type) {
	case *TypeVar:
		for e := env; e != nil; e = e.prev {
			if e.v == x {
				return e.val
			}
		}
		return t
	case *UnionAll:
		lb := instType(x.Var.LB, env, stack, check)
		ub := instType(x.Var.UB, env, stack, check)
		v := x.Var
		if lb != x.Var.LB || ub != x.Var.UB {
			v = &TypeVar{Name: x.Var.Name, LB: lb, UB: ub}
		}
		newbody := instType(x.Body, &typeEnv{x.Var, v, env}, stack, check)
		if !HasTypeVar(newbody, v) {
			return newbody
		}
		if newbody != x.Body || v != x.Var {
			return &UnionAll{Var: v, Body: newbody}
		}
		return t
	case *Union:
		a := instType(x.A, env, stack, check)
		b := instType(x.B, env, stack, check)
		if a != x.A || b != x.B {
			if !check {
				return &Union{a, b}
			}
			return TypeUnion(a, b)
		}
		return t
	case *Vararg:
		var T, N any
		if x.T != nil {
			T = instType(x.T, env, stack, check)
			if x.N != nil {
				N = instType(x.N, env, stack, check)
			}
		}
		if T != x.T || N != x.N {
			return wrapVararg(T, N, check)
		}
		return t
	case *DataType:
		if len(x.Parameters) == 0 {
			return t
		}
		if x.Name == TupleTypeName {
			return instTuple(x, env, stack, check)
		}
		iparams := make([]any, len(x.Parameters))
		bound := false
		for i, elt := range x.Parameters {
			iparams[i] = instType(elt, env, stack, check)
			bound = bound || iparams[i] != elt
		}
		if bound {
			return instDatatypeInner(x, iparams, stack, env, check)
		}
		return t
	}
	return t
}

// instTuple is inst_tuple_w_ (jltypes.c:2529-2595).
func instTuple(tt *DataType, env *typeEnv, stack *typeStack, check bool) any {
	if IsVaTuple(tt) && len(tt.Parameters) == 1 {
		va := tt.Parameters[0].(*Vararg)
		ttT, ttN := unwrapVararg(va), va.N
		var T, N any
		for e := env; e != nil; e = e.prev {
			if any(e.v) == ttT {
				T = e.val
			} else if ttN != nil && any(e.v) == ttN {
				N = e.val
			}
		}
		if T != nil && N != nil {
			if nt, ok := N.(int64); ok {
				if nt >= 0 {
					return tupletypeFill(nt, T, check)
				}
				throwf("Vararg length is negative: %d", nt)
			}
		}
	}
	iparams := make([]any, len(tt.Parameters))
	bound := false
	for i, elt := range tt.Parameters {
		iparams[i] = instType(elt, env, stack, check)
		bound = bound || iparams[i] != elt
	}
	if bound {
		return instDatatypeInner(tt, iparams, stack, env, check)
	}
	return tt
}

// tupletypeFill is jl_tupletype_fill (jltypes.c:2058-2085).
func tupletypeFill(n int64, t any, check bool) any {
	if check {
		if !ValidTypeParam(t) {
			throwf("TypeError: in Vararg, in type, expected Type, got %s", Show(t))
		}
		t = mayEqualWrapper(normalizeUnionAlls(t))
		check = false
	}
	if n > maxTupleFill {
		throwf("Vararg length %d exceeds the limit of %d", n, maxTupleFill)
	}
	p := make([]any, n)
	for i := range p {
		p[i] = t
	}
	return instDatatypeInner(AnyTupleType, p, nil, nil, check)
}

// maxTupleFill bounds NTuple{n,T} expansion. julia has no bound and would
// allocate n parameters; an untrusted notebook cell must not make the
// runtime allocate gigabytes from one literal.
const maxTupleFill = 1 << 16

// typekeyEq is typekey_eq (jltypes.c:952-987).
func typekeyEq(tt *DataType, key []any) bool {
	if len(key) != len(tt.Parameters) {
		return false
	}
	if tt.Name == TypeTypeName {
		kj, tj := key[0], tt.Parameters[0]
		return kj == tj || (TypeOf(tj) == TypeOf(kj) && TypesEqual(tj, kj))
	}
	for j, kj := range key {
		tj := tt.Parameters[j]
		if tj == kj {
			continue
		}
		if tt.Name == TupleTypeName && (IsTypeType(tj) || IsTypeType(kj)) {
			return false
		}
		if typeEqualityIsIdentity(tj, kj) {
			return false
		}
		if !TypesEqual(tj, kj) {
			return false
		}
	}
	return true
}

// bitmix mixes a hash into an accumulator (julia's bitmix; any good mixer
// serves, the value never leaves the process).
func bitmix(a, b uint32) uint32 {
	h := uint64(a)<<32 | uint64(b)
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return uint32(h)
}

// objectID hashes a non-type value consistently with Egal (jl_object_id).
func objectID(v any) uint32 {
	switch x := v.(type) {
	case int64:
		return bitmix(uint32(x), uint32(uint64(x)>>32))
	case bool:
		if x {
			return 0x9e3779b9
		}
		return 0x7f4a7c15
	case Symbol:
		return strHash(string(x))
	case *Instance:
		return bitmix(x.Type.hash, strHash(x.Bits))
	case *bottomType:
		return 0x1b873593
	}
	return 0x85ebca6b // TypeVars and Varargs at top level: hash alike, compare by typekeyEq
}

func strHash(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// typeHash is type_hash (jltypes.c:1742-1777); failed reports a key that
// cannot be hashed (a union, or a type whose own hash failed), which then
// lives in the linear cache.
func typeHash(kj any, failed *bool) uint32 {
	uw := UnwrapUnionAll(kj)
	switch x := uw.(type) {
	case *DataType:
		h := x.hash
		if h == 0 {
			if !*failed {
				*failed = true
				return 0
			}
			h = typekeyHash(x.Name, x.Parameters, *failed)
		}
		return h
	case *TypeVar:
		return typeHash(x.UB, failed)
	case *Union:
		if !*failed {
			*failed = true
			return 0
		}
		return typeHash(x.A, failed) + typeHash(x.B, failed)
	}
	return objectID(uw)
}

// typekeyHash is typekey_hash (jltypes.c:1787-1813).
func typekeyHash(tn *TypeName, key []any, nofail bool) uint32 {
	if tn == TypeTypeName && key[0] == any(Bottom) {
		return TypeofBottomType.hash
	}
	hash := uint32(3)
	failed := nofail
	for _, p := range key {
		repeats := int64(1)
		if vm, ok := p.(*Vararg); ok {
			if n, ok := vm.N.(int64); ok {
				repeats = n
			} else {
				hash = bitmix(0x064eeaab, hash)
			}
			p = unwrapVararg(vm)
		}
		hp := typeHash(p, &failed)
		if failed && !nofail {
			return 0
		}
		for ; repeats > 0; repeats-- {
			hash = bitmix(hp, hash)
		}
	}
	// julia returns `hash ? hash : 1`; 0 means "did not hash", so any
	// nonzero value serves
	return bitmix(^tn.hash, hash) | 1
}

// cacheSlot returns the cache list a key belongs to: the bucket of its hash,
// or the linear cache when it does not hash (lookup_type,
// jltypes.c:1112-1131). The caller holds tn.mu.
func (tn *TypeName) cacheSlot(hv uint32) []*DataType {
	if hv == 0 {
		return tn.linear
	}
	return tn.hashed[hv]
}

// findIn scans entries with typekeyEq. It runs without the lock: equality
// may build types (TypesEqual runs subtyping, which may apply types of this
// very name), and the lists only grow, so a snapshot stays valid.
func findIn(entries []*DataType, key []any) *DataType {
	for _, dt := range entries {
		if typekeyEq(dt, key) {
			return dt
		}
	}
	return nil
}

// lookupType is lookup_type (jltypes.c:1112-1131).
func lookupType(tn *TypeName, key []any) *DataType {
	if tn == TypeTypeName {
		if uw, ok := UnwrapUnionAll(key[0]).(*DataType); ok && key[0] == uw.Name.Wrapper && uw.Name.typeofWrapper != nil {
			return uw.Name.typeofWrapper
		}
	}
	hv := typekeyHash(tn, key, false)
	tn.mu.Lock()
	snap := tn.cacheSlot(hv)
	tn.mu.Unlock()
	return findIn(snap, key)
}

// cacheOrGet inserts dt in its name's cache (jl_cache_type_,
// jltypes.c:1254-1280) unless an equal entry is already there, which it
// returns instead.
func cacheOrGet(dt *DataType) *DataType {
	tn := dt.Name
	key := dt.Parameters
	hv := typekeyHash(tn, key, false)
	seen := 0
	for {
		tn.mu.Lock()
		cur := tn.cacheSlot(hv)
		if len(cur) == seen {
			if hv == 0 {
				tn.linear = append(tn.linear, dt)
			} else {
				if tn.hashed == nil {
					tn.hashed = map[uint32][]*DataType{}
				}
				tn.hashed[hv] = append(tn.hashed[hv], dt)
			}
			if tn == TypeTypeName {
				if uw, ok := UnwrapUnionAll(key[0]).(*DataType); ok && key[0] == uw.Name.Wrapper {
					uw.Name.typeofWrapper = dt
				}
			}
			tn.mu.Unlock()
			return dt
		}
		tn.mu.Unlock()
		if c := findIn(cur[seen:], key); c != nil {
			return c
		}
		seen = len(cur)
	}
}

// withinTypeVar is within_typevar (jltypes.c:1376-1391).
func withinTypeVar(t, vlb, vub any) bool {
	lb, ub := t, t
	if isTypeVar(t) || HasFreeTypeVars(t) {
		return true
	} else if !IsType(t) {
		return vlb == any(Bottom) && vub == any(AnyType)
	}
	return (HasFreeTypeVars(vlb) || Subtype(vlb, lb)) && (HasFreeTypeVars(vub) || Subtype(ub, vub))
}

// substituteVar is jl_substitute_var.
func substituteVar(t any, v *TypeVar, val any) any {
	if val == any(v) {
		return t
	}
	return instType(t, &typeEnv{v, val, nil}, nil, true)
}

// checkDatatypeParameters is check_datatype_parameters
// (jltypes.c:1888-1939).
func checkDatatypeParameters(tn *TypeName, params []any) {
	var bounds []any
	w := tn.Wrapper
	for {
		ua, ok := w.(*UnionAll)
		if !ok {
			break
		}
		bounds = append(bounds, ua.Var.LB, ua.Var.UB)
		w = ua.Body
	}
	w = tn.Wrapper
	for i := range params {
		tv := w.(*UnionAll).Var
		if !withinTypeVar(params[i], bounds[2*i], bounds[2*i+1]) {
			throwf("TypeError: in %s, in %s, expected %s, got %s", tn.Name, tv.Name,
				Show(&TypeVar{Name: tv.Name, LB: bounds[2*i], UB: bounds[2*i+1]}), Show(params[i]))
		}
		for j := 2*i + 2; j < len(bounds); j++ {
			if bj := bounds[j]; bj != any(AnyType) && bj != any(Bottom) {
				bounds[j] = substituteVar(bj, tv, params[i])
			}
		}
		w = w.(*UnionAll).Body
	}
}

// instDatatypeInner is inst_datatype_inner (jltypes.c:2089-2455) without
// NamedTuple and GenericMemory validation and without field types.
func instDatatypeInner(dt *DataType, iparams []any, stack *typeStack, env *typeEnv, check bool) any {
	tn := dt.Name
	istuple := tn == TupleTypeName
	cacheable := true
	ntp := len(iparams)
	if istuple {
		for i := 0; i < ntp; i++ {
			pi := iparams[i]
			if va, ok := pi.(*Vararg); ok && unwrapVararg(va) == any(Bottom) {
				va1 := va.N
				if k, ok := va1.(int64); ok {
					if k == 0 {
						va1 = nil
					} else {
						pi = Bottom
					}
				}
				if va1 == nil {
					ntp--
					iparams = iparams[:ntp]
					break
				}
			}
			if pi == any(Bottom) {
				throwf("Tuple field type cannot be Union{}")
			}
			if cacheable && !IsConcrete(pi) {
				cacheable = false
			}
		}
	} else {
		for i := 0; cacheable && i < ntp; i++ {
			if HasFreeTypeVars(iparams[i]) {
				cacheable = false
			}
		}
	}
	if cacheable {
		if l := lookupType(tn, iparams); l != nil {
			return l
		}
	}
	if check {
		for i := 0; i < ntp; i++ {
			pi := iparams[i]
			switch pi.(type) {
			case *bottomType, *DataType, *Vararg:
				continue
			}
			if !cacheable && HasFreeTypeVars(pi) {
				continue
			}
			tw := extractWrapper(pi)
			if tw != nil && tw != pi && (tn != TypeTypeName || TypeOf(pi) == TypeOf(tw)) &&
				!HasFreeTypeVars(pi) && TypesEqual(pi, tw) {
				iparams[i] = tw
			}
		}
		if tn == TypeTypeName {
			if d, ok := iparams[0].(*DataType); ok && d.Name == TypeTypeName && d.Parameters[0] == any(Bottom) {
				iparams[0] = TypeofBottomType
			}
		}
	}
	if cacheable {
		if l := lookupType(tn, iparams); l != nil {
			return l
		}
	}
	for s := stack; s != nil; s = s.prev {
		if s.tt.Name == tn && len(s.tt.Parameters) == ntp && typekeyEq(s.tt, iparams) {
			return s.tt
		}
	}
	if check && !istuple {
		checkDatatypeParameters(tn, iparams)
	} else if ntp == 0 && EmptyTupleType != nil {
		return EmptyTupleType
	}
	if istuple && ntp > 0 {
		if va, ok := iparams[ntp-1].(*Vararg); ok {
			va0, va1 := unwrapVararg(va), va.N
			if ntp == 1 && va0 == any(AnyType) && va1 == nil {
				return AnyTupleType
			}
			if nt, ok := va1.(int64); ok && (nt == 0 || !HasFreeTypeVars(va0)) {
				if ntp == 1 {
					return tupletypeFill(nt, va0, false)
				}
				if nt > maxTupleFill {
					throwf("Vararg length %d exceeds the limit of %d", nt, maxTupleFill)
				}
				p := make([]any, 0, ntp-1+int(nt))
				p = append(p, iparams[:ntp-1]...)
				for k := int64(0); k < nt; k++ {
					p = append(p, va0)
				}
				return instDatatypeInner(AnyTupleType, p, nil, nil, check)
			}
		}
	}
	if check && tn != TypeTypeName {
		changed := false
		if istuple {
			cacheable = true
		}
		for i := 0; i < ntp; i++ {
			newp := normalizeUnionAlls(iparams[i])
			if newp != iparams[i] {
				iparams[i] = newp
				changed = true
			}
			if istuple && cacheable && !IsConcrete(newp) {
				cacheable = false
			}
		}
		if changed {
			if cacheable {
				if l := lookupType(tn, iparams); l != nil {
					return l
				}
			}
		}
	}
	p := append([]any(nil), iparams...)
	ndt := &DataType{Name: tn, Parameters: p, HasConcreteSubtype: true}
	top := &typeStack{ndt, stack}
	precomputeMemoized(ndt, cacheable)
	if istuple {
		ndt.Super = AnyType
	} else if dt.Super != nil {
		ndt.Super = instType(dt.Super, env, top, check).(*DataType)
	}
	if cacheable {
		return cacheOrGet(ndt)
	}
	return ndt
}

// maybeSubtypeOfCache is maybe_subtype_of_cache (jltypes.c:1292-1314).
func maybeSubtypeOfCache(kj any, covariant bool) bool {
	switch uw := UnwrapUnionAll(kj).(type) {
	case *DataType:
		return uw.MaybeSubtypeOfCache
	case *Union:
		return maybeSubtypeOfCache(uw.A, covariant) && maybeSubtypeOfCache(uw.B, covariant)
	case *bottomType:
		return true
	case *TypeVar:
		if !covariant {
			return uw.LB == uw.UB || uw.LB != any(Bottom)
		}
	}
	return true
}

// precomputeMemoized is jl_precompute_memoized_dt (jltypes.c:1836-1886).
func precomputeMemoized(dt *DataType, cacheable bool) {
	if dt.Name.hash == 0 {
		dt.Name.hash = strHash(dt.Name.Module+"."+dt.Name.Name) | 1
	}
	istuple := dt.Name == TupleTypeName
	dt.HasFreeTypeVars = false
	dt.MaybeSubtypeOfCache = true
	dt.IsConcrete = !dt.Name.Abstract
	dt.IsDispatchTuple = istuple
	for _, p := range dt.Parameters {
		if !dt.HasFreeTypeVars {
			dt.HasFreeTypeVars = HasFreeTypeVars(p)
			if dt.HasFreeTypeVars {
				dt.IsConcrete = false
			}
		}
		if istuple {
			if dt.IsConcrete {
				dt.IsConcrete = IsConcrete(p) || p == any(Bottom)
			}
			if dt.IsDispatchTuple {
				pd, ok := p.(*DataType)
				dt.IsDispatchTuple = ok && ((!IsKind(p) && pd.IsConcrete) || p == any(TypeofBottomType) ||
					(pd.Name == TypeTypeName && !pd.HasFreeTypeVars))
			}
		}
		if va, ok := p.(*Vararg); ok {
			p = va.T
		}
		if istuple && dt.HasConcreteSubtype {
			if p != nil && !IsType(p) && !isTypeVar(p) {
				dt.HasConcreteSubtype = false
			}
			if p == any(Bottom) {
				dt.HasConcreteSubtype = false
			}
		}
		if dt.MaybeSubtypeOfCache {
			dt.MaybeSubtypeOfCache = p == nil || maybeSubtypeOfCache(p, istuple) || !HasFreeTypeVars(p)
		}
	}
	if dt.Name == TypeTypeName {
		p := dt.Parameters[0]
		if !IsType(p) && !isTypeVar(p) {
			dt.HasConcreteSubtype = false
		}
		dt.MaybeSubtypeOfCache = true
		if uw, ok := UnwrapUnionAll(p).(*DataType); !ok || uw.Name.Wrapper != p {
			cacheable = false
		}
	}
	dt.hash = typekeyHash(dt.Name, dt.Parameters, cacheable)
}

// hasConcreteSupertype is has_concrete_supertype (jltypes.c:1317-1352).
func hasConcreteSupertype(kj any) bool {
	switch uw := UnwrapUnionAll(kj).(type) {
	case *DataType:
		if uw.Name.Abstract && uw.Name != TypeTypeName {
			return false
		}
		if !uw.MaybeSubtypeOfCache {
			return false
		}
		if uw.Name == TupleTypeName {
			for _, p := range uw.Parameters {
				if va, ok := p.(*Vararg); ok {
					p = unwrapVararg(va)
				}
				if !hasConcreteSupertype(p) {
					return false
				}
			}
		}
		return true
	case *Union:
		return hasConcreteSupertype(uw.A) && hasConcreteSupertype(uw.B)
	case *bottomType:
		return true
	case *TypeVar:
		return hasConcreteSupertype(uw.UB)
	}
	return false
}

// typeEqualityIsIdentity is jl_type_equality_is_identity
// (jltypes.c:1354-1372).
func typeEqualityIsIdentity(t1, t2 any) bool {
	c1, c2 := IsConcrete(t1), IsConcrete(t2)
	if c1 && c2 {
		d1, d2 := t1.(*DataType), t2.(*DataType)
		if d1.Name != TupleTypeName || d2.Name != TupleTypeName {
			return true
		}
		if d1.HasConcreteSubtype && d2.HasConcreteSubtype {
			return true
		}
	}
	if c1 && !hasConcreteSupertype(t2) {
		return true
	}
	if c2 && !hasConcreteSupertype(t1) {
		return true
	}
	return false
}

// Apply is jl_apply_type (jltypes.c:1412-1473): tc{params...}.
func Apply(tc any, params ...any) (t any, err error) {
	defer catchError(&err)
	return applyType(tc, params), nil
}

func applyType(tc any, params []any) any {
	if tc == any(AnyTupleType) {
		return instDatatypeInner(AnyTupleType, append([]any(nil), params...), nil, nil, true)
	}
	if tc == any(UnionType) {
		return TypeUnion(params...)
	}
	if len(params) > 1 {
		// the common case of applying a wrapper: all parameters become
		// direct parameters of its DataType (jltypes.c:1419-1427)
		if u, ok := UnwrapUnionAll(tc).(*DataType); ok && len(params) == len(u.Parameters) && u.Name.Wrapper == tc {
			var env *typeEnv
			w := tc
			for _, pi := range params {
				ua := w.(*UnionAll)
				env = &typeEnv{ua.Var, pi, env}
				w = ua.Body
			}
			return instDatatypeInner(u, append([]any(nil), params...), nil, env, true)
		}
	}
	tc0 := tc
	for _, pi := range params {
		ua0, ok := tc0.(*UnionAll)
		if !ok {
			name := ""
			if d, ok := tc0.(*DataType); ok {
				name = d.Name.Name
			}
			throwf("too many parameters for type %s", name)
		}
		tc0 = ua0.Body
		// a substitution can drop later UnionAlls, as in
		// NTuple{0,T} where T => Tuple{}; values for them are accepted
		ua, ok := tc.(*UnionAll)
		if !ok {
			continue
		}
		if !HasFreeTypeVars(ua.Var.LB) && !HasFreeTypeVars(ua.Var.UB) && !withinTypeVar(pi, ua.Var.LB, ua.Var.UB) {
			inner, isdt := UnwrapUnionAll(tc).(*DataType)
			iswrapper := false
			if isdt {
				for temp := inner.Name.Wrapper; ; {
					tu, ok := temp.(*UnionAll)
					if !ok {
						break
					}
					if any(tu) == tc {
						iswrapper = true
						break
					}
					temp = tu.Body
				}
			}
			if !iswrapper {
				name := "Type"
				if isdt {
					name = inner.Name.Name
				}
				throwf("TypeError: in %s, in %s, expected %s, got %s", name, ua.Var.Name, Show(ua.Var), Show(pi))
			}
		}
		tc = instType(ua.Body, &typeEnv{ua.Var, pi, nil}, nil, true)
	}
	return tc
}
