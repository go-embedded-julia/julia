package dispatch

import (
	"github.com/go-embedded-julia/julia/core"
)

// julia's method table is a typemap (src/typemap.c): a tree of levels, one
// per argument position, each splitting its entries by the first
// argument's type into the caches arg1 (concrete types, by type name),
// targ (Type{T} for a concrete T), name1 and tname (the same for the types
// and Type{T}s that are not concrete), then the list of the entries it
// cannot split (linear) and those whose argument is Any. The order a
// search visits that tree in is the order ml_matches collects the methods
// in, so it is part of the answer: the tree is built with julia's
// insertion rules and walked with julia's visitor.
//
// One deliberate difference: julia's caches are hash tables keyed by
// type name, iterated in slot order; a type name's hash includes its
// module's build_id (datatype.c:84), random for a module made at run time
// and fixed in the system image for the others, so no port can reproduce
// it. A cache here is iterated in the order its keys were first inserted.

// maxMethlistCount is MAX_METHLIST_COUNT (typemap.c:12).
const maxMethlistCount = 6

// tmEntry is jl_typemap_entry_t (julia.h), a method's place in the tree.
type tmEntry struct {
	sig    any // the signature, typeof(f) first
	ttypes *core.DataType
	m      *Method
	next   *tmEntry
	va     bool
	simple bool
	leaf   bool
}

// newTmEntry is jl_typemap_alloc (typemap.c:1467-1510): the entry of a
// method and the complexity of its signature.
func newTmEntry(sig any, m *Method) *tmEntry {
	tt := core.UnwrapUnionAll(sig).(*core.DataType)
	_, isUnionAll := sig.(*core.UnionAll)
	e := &tmEntry{sig: sig, ttypes: tt, m: m, va: core.IsVaTuple(tt)}
	e.simple = !isUnionAll
	e.leaf = e.simple && !e.va
	for _, decl := range tt.Parameters {
		if !e.simple {
			break
		}
		switch {
		case core.IsKind(decl):
			e.leaf = false
		case core.IsTypeType(decl):
			e.leaf = false
		case isVararg(decl):
			e.leaf = false
		case decl == any(core.AnyType):
			e.leaf = false
		case !core.IsConcrete(decl):
			e.leaf, e.simple = false, false
		}
	}
	return e
}

func isVararg(v any) bool { _, ok := v.(*core.Vararg); return ok }

// unwrapVararg is jl_unwrap_vararg: T, or Any when T is absent.
func unwrapVararg(v *core.Vararg) any {
	if v.T == nil {
		return core.AnyType
	}
	return v.T
}

// tmMem is one of the caches of a level, a Memory{Any} of key, value pairs
// iterated in the order the keys came.
type tmMem struct {
	keys []any
	vals []any
	idx  map[any]int
}

// lookup is mtcache_hash_lookup (typemap.c:308-314): nil for jl_nothing.
func (m *tmMem) lookup(k any) any {
	if i, ok := m.idx[k]; ok {
		return m.vals[i]
	}
	return nil
}

func (m *tmMem) empty() bool { return m == nil || len(m.keys) == 0 }

// tmLevel is jl_typemap_level_t.
type tmLevel struct {
	arg1, targ, name1, tname *tmMem
	linear                   *tmEntry
	any                      any
}

// typemap is a method table's defs: a list of entries, then a level.
type typemap struct{ root any }

// insert is jl_typemap_insert (typemap.c:1512-1517).
func (tm *typemap) insert(e *tmEntry) {
	tm.root = insertGeneric(tm.root, e, 0, 0, nil)
}

// isAny is jl_is_any (typemap.c:19-24).
func isAny(t any) bool {
	for {
		tv, ok := t.(*core.TypeVar)
		if !ok {
			return t == any(core.AnyType)
		}
		t = tv.UB
	}
}

// typeofBottomName is jl_typeofbottom_type->name.
func typeofBottomName() *core.TypeName { return core.TypeofBottomType.Name }

// extractName is jl_type_extract_name (typemap.c:26-55): the one type name
// that stands for t, nil when none does.
func extractName(t any, invariant bool) *core.TypeName {
	if _, ok := t.(*core.UnionAll); ok {
		t = core.UnwrapUnionAll(t)
	}
	switch x := t.(type) {
	case *core.Vararg:
		return extractName(unwrapVararg(x), invariant)
	case *core.TypeVar:
		return extractName(x.UB, false)
	}
	if t == any(core.Bottom) || t == any(core.TypeofBottomType) || t == any(core.TypeofBottomType.Super) {
		return typeofBottomName() // Union{}, typeof(Union{}) and Type{Union{}} together
	}
	switch x := t.(type) {
	case *core.DataType:
		if core.IsKind(t) && !invariant {
			return core.TypeTypeName
		}
		return x.Name
	case *core.Union:
		tn1, tn2 := extractName(x.A, invariant), extractName(x.B, invariant)
		if tn1 == tn2 {
			return tn1
		}
	}
	return nil
}

// extractNamePrecise is jl_type_extract_name_precise (typemap.c:59-91):
// false when the name extracted above over-approximates t.
func extractNamePrecise(t any, invariant bool) bool {
	if _, ok := t.(*core.UnionAll); ok {
		t = core.UnwrapUnionAll(t)
	}
	switch x := t.(type) {
	case *core.Vararg:
		return extractNamePrecise(unwrapVararg(x), invariant)
	case *core.TypeVar:
		return extractNamePrecise(x.UB, false)
	}
	if t == any(core.Bottom) || t == any(core.TypeofBottomType) || t == any(core.TypeofBottomType.Super) {
		return true
	}
	if u, ok := t.(*core.Union); ok {
		if !extractNamePrecise(u.A, invariant) || !extractNamePrecise(u.B, invariant) {
			return false
		}
		return extractName(u.A, invariant) == extractName(u.B, invariant)
	}
	// a DataType; anything else a type parameter can be is precise
	d, ok := t.(*core.DataType)
	return !ok || invariant || !d.Name.Abstract || d.Name == core.TypeTypeName
}

// parameterIncludesBottom is jl_parameter_includes_bottom
// (typemap.c:94-103): Type{Union{}} <: Type{t}, for t with free variables.
func parameterIncludesBottom(t any) bool {
	if _, ok := t.(*core.TypeVar); ok || t == any(core.Bottom) {
		return true
	}
	if u, ok := t.(*core.Union); ok {
		return parameterIncludesBottom(u.A) && parameterIncludesBottom(u.B)
	}
	return false
}

// isCacheLeaf is is_cache_leaf (typemap.c:273-278): arguments the caches
// index by their type.
func isCacheLeaf(ty any, tparam bool) bool {
	if ty == any(core.Bottom) {
		return true
	}
	return core.IsConcrete(ty) && (tparam || !core.IsKind(ty))
}

// nameOfLeaf is the key a cache leaf type goes under: its name, typeof(
// Union{}) for Union{}.
func nameOfLeaf(t any) *core.TypeName {
	if t == any(core.Bottom) {
		return typeofBottomName()
	}
	return t.(*core.DataType).Name
}

// wrapperIsDatatype is jl_is_datatype(name->wrapper).
func wrapperIsDatatype(n *core.TypeName) bool {
	_, ok := n.Wrapper.(*core.DataType)
	return ok
}

// primaryOf is jl_unwrap_unionall(name->wrapper).
func primaryOf(n *core.TypeName) *core.DataType {
	return core.UnwrapUnionAll(n.Wrapper).(*core.DataType)
}

// ---- insertion (typemap.c:1260-1465) ----

// listInsert is jl_typemap_list_insert_ (typemap.c:1329-1350): the new
// entry goes at the first place that keeps the list as (leaf signatures,
// simple signatures, the others); it returns the list's head.
func listInsert(head *tmEntry, newrec *tmEntry) *tmEntry {
	var prev *tmEntry
	l := head
	for l != nil {
		if newrec.leaf || !l.leaf {
			if newrec.simple || !l.simple {
				break
			}
		}
		prev, l = l, l.next
	}
	newrec.next = l
	if prev == nil {
		return newrec
	}
	prev.next = newrec
	return head
}

func listCount(l *tmEntry) int {
	n := 0
	for ; l != nil; l = l.next {
		n++
	}
	return n
}

// convertListToCache is jl_method_convert_list_to_cache
// (typemap.c:1293-1327).
func convertListToCache(ml *tmEntry, tparam, offs int, doublesplit bool) any {
	var mem *tmMem
	var lvl *tmLevel
	if doublesplit {
		mem = &tmMem{idx: map[any]int{}}
	} else {
		lvl = &tmLevel{}
	}
	for ml != nil {
		next := ml.next
		ml.next = nil
		if doublesplit {
			ps := ml.ttypes.Parameters
			var key any
			if offs < len(ps)-1 {
				key = ps[offs]
			} else {
				key = ps[len(ps)-1]
			}
			// (julia unwraps a Vararg key; none gets here: a Vararg is never in
			// the caches, levelInsert keeps it out)
			// (julia maps typeof(Union{}) to Type{Union{}} here too; a list is
			// converted by name, and that name's wrapper is no UnionAll)
			if tparam != 0 {
				key = key.(*core.DataType).Parameters[0]
			}
			memInsert(&mem, key, ml, 0, offs, nil)
		} else {
			levelInsert(lvl, ml, offs)
		}
		ml = next
	}
	if doublesplit {
		return mem
	}
	return lvl
}

// insertGeneric is jl_typemap_insert_generic (typemap.c:1353-1385): it
// returns what the slot holds afterwards.
func insertGeneric(ml any, newrec *tmEntry, tparam, offs int, doublesplit any) any {
	switch x := ml.(type) {
	case *tmMem:
		memInsert(&x, doublesplit, newrec, 0, offs, nil)
		return x
	case *tmLevel:
		levelInsert(x, newrec, offs)
		return x
	}
	head, _ := ml.(*tmEntry)
	if listCount(head) > maxMethlistCount {
		c := convertListToCache(head, tparam, offs, doublesplit != nil)
		switch x := c.(type) {
		case *tmMem:
			memInsert(&x, doublesplit, newrec, 0, offs, nil)
		case *tmLevel:
			levelInsert(x, newrec, offs)
		}
		return c
	}
	return listInsert(head, newrec)
}

// memInsert is jl_typemap_memory_insert_ (typemap.c:1387-1397).
func memInsert(pcache **tmMem, key any, newrec *tmEntry, tparam, offs int, doublesplit any) {
	if *pcache == nil {
		*pcache = &tmMem{idx: map[any]int{}}
	}
	m := *pcache
	i, ok := m.idx[key]
	if !ok {
		m.idx[key] = len(m.keys)
		m.keys = append(m.keys, key)
		m.vals = append(m.vals, newrec)
		return
	}
	step := 1
	if doublesplit != nil {
		step = 0
	}
	m.vals[i] = insertGeneric(m.vals[i], newrec, tparam, offs+step, doublesplit)
}

// levelInsert is jl_typemap_level_insert_ (typemap.c:1399-1465).
func levelInsert(cache *tmLevel, newrec *tmEntry, offs int) {
	ps := newrec.ttypes.Parameters
	l := len(ps)
	// the type at offs into the signature, which may be a Vararg
	var t1 any
	isva := false
	if l <= offs+1 {
		if l > 0 {
			t1 = ps[l-1]
			if va, ok := t1.(*core.Vararg); ok {
				isva = true
				t1 = unwrapVararg(va)
			} else if l <= offs {
				t1 = nil
			}
		}
	} else {
		t1 = ps[offs]
	}
	if t1 == any(core.TypeofBottomType) {
		t1 = core.TypeofBottomType.Super
	}
	// Any goes in the Any list
	// (julia goes on splitting a Vararg{Any} past the last parameter, so seven
	// entries of one such signature, a method redefined that often, recurse
	// without end there; here they go to the linear list)
	if t1 != nil && isAny(t1) && !(isva && offs >= l) {
		cache.any = insertGeneric(cache.any, newrec, 0, offs+1, nil)
		return
	}
	// Varargs stay out of the optimized caches
	if t1 != nil && !isva {
		// Type{T} for a concrete T goes in targ
		if core.IsTypeType(t1) {
			a0 := t1.(*core.DataType).Parameters[0]
			if isCacheLeaf(a0, true) {
				name := nameOfLeaf(a0)
				var ds any
				if !wrapperIsDatatype(name) {
					ds = a0
				}
				memInsert(&cache.targ, name, newrec, 1, offs, ds)
				return
			}
		}
		if isCacheLeaf(t1, false) {
			name := nameOfLeaf(t1)
			var ds any
			if !wrapperIsDatatype(name) {
				ds = t1
			}
			memInsert(&cache.arg1, name, newrec, 0, offs, ds)
			return
		}
		// then the type name caches
		t1 = core.UnwrapUnionAll(t1)
		if core.IsTypeType(t1) {
			a0 := extractName(t1.(*core.DataType).Parameters[0], true)
			super := core.AnyType
			if a0 != nil {
				super = primaryOf(a0)
			}
			memInsert(&cache.tname, super.Name, newrec, 1, offs, nil)
			return
		}
		if a0 := extractName(t1, false); a0 != nil && a0 != core.AnyType.Name {
			memInsert(&cache.name1, a0, newrec, 0, offs, nil)
			return
		}
	}
	cache.linear = listInsert(cache.linear, newrec)
}

// ---- the intersection visitor (typemap.c:398-813) ----

// isectEnv is typemap_intersection_env.
type isectEnv struct {
	fptr        func(e *tmEntry) bool
	typ         any // the query
	va          any // the element type of the query's Vararg
	searchSlurp int
	minValid    uint64
	maxValid    uint64
	ti          any
	env         []any
	issubty     bool
	err         error
}

func supertypeHeight(dt *core.DataType) int {
	h := 1
	for dt != nil && dt != core.AnyType {
		h++
		dt = dt.Super
	}
	return h
}

// tnameIntersectionDT is tname_intersection_dt (typemap.c:399-416).
func tnameIntersectionDT(a *core.DataType, bname *core.TypeName, ha int) bool {
	// (julia answers true for a == Any here; the callers take Any as ty, and
	// tnameIntersection tests it first, so it never arrives)
	b := primaryOf(bname)
	hb := 1
	for b != nil && b != core.AnyType {
		if a.Name == b.Name {
			return true
		}
		hb++
		b = b.Super
	}
	for ha > hb && a != nil {
		a = a.Super
		ha--
	}
	return a != nil && a.Name == bname
}

// tnameIntersection is tname_intersection (typemap.c:418-440).
func tnameIntersection(a any, bname *core.TypeName, tparam bool) bool {
	if a == any(core.AnyType) {
		return true
	}
	a = core.UnwrapUnionAll(a)
	switch x := a.(type) {
	case *core.Union:
		return tnameIntersection(x.A, bname, tparam) || tnameIntersection(x.B, bname, tparam)
	case *core.TypeVar:
		return tnameIntersection(x.UB, bname, tparam)
	case *core.DataType:
		if tparam {
			// x is Type{T}: julia answers false for any other DataType, which
			// the callers (a Type or a type variable's bound) never pass
			return tnameIntersection(core.UnwrapUnionAll(x.Parameters[0]), bname, false)
		}
		return tnameIntersectionDT(x, bname, supertypeHeight(x))
	}
	return false
}

// concreteIntersects is concrete_intersects (typemap.c:442-450).
func concreteIntersects(t, ty any, tparam int) bool {
	if ty == any(core.AnyType) {
		return true
	}
	if tparam&1 != 0 {
		return core.Isa(t, ty)
	}
	return t == ty || core.Subtype(t, ty)
}

// memVisit is jl_typemap_intersection_memory_visitor (typemap.c:454-522).
// tparam bit 0 is ::Type{T} (vs. T), bit 1 is typename(T) (vs. T).
func (c *isectEnv) memVisit(a *tmMem, ty any, tparam, offs int) bool {
	height := 0
	tydt := core.AnyType
	if tparam&2 != 0 {
		// describe ty for the intersection tests
		var ttype any = core.UnwrapUnionAll(ty)
		if tparam&1 != 0 {
			// T of Type{T}, if it can be extracted
			if core.IsTypeType(ttype) {
				ttype = ttype.(*core.DataType).Parameters[0]
			} else {
				ttype = nil
			}
		}
		if d, ok := ttype.(*core.DataType); ok {
			tydt = d
		} else if ttype != nil {
			tn := extractName(ttype, tparam&1 != 0)
			if tn != nil {
				tydt = primaryOf(tn)
			} else {
				tydt = nil
			}
		}
		switch {
		case tydt == core.AnyType:
			ty = core.AnyType
		case tydt == nil:
			tydt = core.AnyType
		default:
			height = supertypeHeight(tydt)
		}
	}
	// the cache may grow while a callback runs: index, never range
	for i := 0; i < len(a.keys); i++ {
		t := a.keys[i]
		if tparam&2 != 0 {
			ml := a.vals[i]
			tn := t.(*core.TypeName)
			var hit bool
			if tydt == core.AnyType {
				hit = tnameIntersection(ty, tn, tparam&1 != 0)
			} else {
				hit = tnameIntersectionDT(tydt, tn, height)
			}
			if hit {
				if tparam&1 != 0 && tn == typeofBottomName() {
					continue // Type{Union{}} and Type{typeof(Union{})}: the caller did them
				}
				if inner, ok := ml.(*tmMem); ok {
					if !c.memVisit(inner, ty, tparam&^2, offs) {
						return false
					}
				} else if !c.visit(ml, offs+1) {
					return false
				}
			}
		} else {
			// t is a leaf type: intersection is subtyping (kinds excluded)
			if concreteIntersects(t, ty, tparam) {
				ml := a.vals[i]
				if ml != nil && !c.visit(ml, offs+1) {
					return false
				}
			}
		}
	}
	return true
}

// nodeVisit is jl_typemap_intersection_node_visitor (typemap.c:527-555).
func (c *isectEnv) nodeVisit(ml *tmEntry) bool {
	for ; ml != nil; ml = ml.next {
		if c.maxValid < ml.m.PrimaryWorld {
			continue
		}
		if c.minValid > ml.m.MaxWorld {
			continue
		}
		ti, env, issubty, err := core.TypeIntersectionEnv(c.typ, ml.sig)
		if err != nil {
			c.err = err
			return false
		}
		c.ti, c.env, c.issubty = ti, env, issubty
		if ti != any(core.Bottom) {
			// intersection is conservative in corner cases: a dispatch tuple
			// that is not a subtype matches nothing (issue #30394)
			d, _ := c.typ.(*core.DataType)
			if c.issubty || d == nil || !d.IsDispatchTuple {
				if !c.fptr(ml) {
					return false
				}
			}
		}
	}
	return true
}

// hasCovariantVar is has_covariant_var (typemap.c:561-568).
func hasCovariantVar(ttypes *core.DataType, tv *core.TypeVar) bool {
	for _, p := range ttypes.Parameters {
		if p == any(tv) {
			return true
		}
	}
	return false
}

// slurpSearch is typemap_slurp_search (typemap.c:570-587).
func (c *isectEnv) slurpSearch(ml *tmEntry) {
	if c.searchSlurp != 0 && ml.va {
		sig := ml.ttypes
		nargs := len(sig.Parameters)
		if nargs > 1 && nargs-1 == c.searchSlurp {
			va := sig.Parameters[nargs-1].(*core.Vararg)
			if (va.T == nil || va.T == any(core.AnyType)) && va.N == nil {
				// typemap may set exclude_typeofbottom on parameter nargs
				c.searchSlurp = 0
			}
		}
	}
}

// visit is jl_typemap_intersection_visitor (typemap.c:589-813): false
// when the callback asked to stop.
func (c *isectEnv) visit(m any, offs int) bool {
	cache, ok := m.(*tmLevel)
	if !ok {
		head, _ := m.(*tmEntry)
		return c.nodeVisit(head)
	}
	ttypes := core.UnwrapUnionAll(c.typ).(*core.DataType)
	l := len(ttypes.Parameters)
	var ty any
	switch {
	case c.va != nil && l <= offs+1:
		ty = c.va
	case l > offs:
		ty = ttypes.Parameters[offs]
	}
	if ty == any(core.TypeofBottomType) {
		ty = core.TypeofBottomType.Super
	}
	if ty != nil {
		for {
			tv, ok := ty.(*core.TypeVar)
			if !ok {
				break
			}
			ty = tv.UB
		}
		// approxify the tparam until we have a valid type
		if core.HasFreeTypeVars(ty) {
			ty = core.RewrapUnionAll(ty, c.typ)
		}
		targ, tname := cache.targ, cache.tname
		maybeType, maybeKind, excludeTypeofBottom := false, false, false
		var typetype any
		var nameTN *core.TypeName
		// pre-check: could ty intersect any Type or Kind
		if !targ.empty() || !tname.empty() {
			maybeKind = core.HasIntersectKindNotType(ty)
			maybeType = maybeKind || core.HasIntersectTypeNotKind(ty)
			if maybeType && !maybeKind {
				tt := core.UnwrapUnionAll(ty)
				if core.IsTypeType(tt) {
					typetype = tt.(*core.DataType).Parameters[0]
				}
				if typetype != nil {
					nameTN = extractName(typetype, true)
				}
				switch {
				case typetype == nil:
					excludeTypeofBottom = !core.Subtype(core.TypeofBottomType, ty)
				default:
					if tv, ok := typetype.(*core.TypeVar); ok {
						excludeTypeofBottom = hasCovariantVar(ttypes, tv)
					} else {
						excludeTypeofBottom = !parameterIncludesBottom(typetype)
					}
				}
			}
		}
		// first, methods defined on Type{T}, T concrete
		if !targ.empty() && maybeType && (typetype == nil || core.HasFreeTypeVars(typetype) || isCacheLeaf(typetype, true)) {
			if !excludeTypeofBottom {
				// Type{Union{}}, Type{Type{Union{}}} and Type{typeof(Union{})} early
				if ml := targ.lookup(typeofBottomName()); ml != nil {
					slurp := c.searchSlurp
					c.searchSlurp = offs + 1
					if !c.visit(ml, offs+1) {
						c.searchSlurp = slurp
						return false
					}
					if c.searchSlurp == 0 {
						excludeTypeofBottom = true
					}
					c.searchSlurp = slurp
				}
			}
			if nameTN != typeofBottomName() {
				if excludeTypeofBottom && nameTN != nil && extractNamePrecise(typetype, true) {
					// semi-direct lookup of types by their names
					ml := targ.lookup(nameTN)
					if inner, ok := ml.(*tmMem); ok {
						if typetype != nil && !core.HasFreeTypeVars(typetype) {
							// direct lookup of leaf types
							if isCacheLeaf(typetype, true) {
								if ml := inner.lookup(typetype); ml != nil {
									if !c.visit(ml, offs+1) {
										return false
									}
								}
							}
						} else if !c.memVisit(inner, ty, 1, offs) {
							return false
						}
					} else if ml != nil {
						if !c.visit(ml, offs+1) {
							return false
						}
					}
				} else {
					// a scan is required to consider all the possible subtypes
					var sty any = core.AnyType
					if excludeTypeofBottom && !maybeKind {
						sty = ty
					}
					if !c.memVisit(targ, sty, 3, offs) {
						return false
					}
				}
			}
		}
		if cachearg1 := cache.arg1; !cachearg1.empty() {
			if isCacheLeaf(ty, false) {
				// direct lookup of leaf types
				ml := cachearg1.lookup(nameOfLeaf(ty))
				if inner, ok := ml.(*tmMem); ok {
					ml = inner.lookup(ty)
				}
				if ml != nil {
					if !c.visit(ml, offs+1) {
						return false
					}
				}
			} else {
				n := extractName(ty, false)
				if n != nil && extractNamePrecise(ty, false) {
					ml := cachearg1.lookup(n)
					if inner, ok := ml.(*tmMem); ok {
						if !c.memVisit(inner, ty, 0, offs) {
							return false
						}
					} else if ml != nil {
						if !c.visit(ml, offs+1) {
							return false
						}
					}
				} else if !c.memVisit(cachearg1, ty, 2, offs) {
					// a scan is required to check subtypes
					return false
				}
			}
		}
		// then methods defined on Type{T}, T not concrete but with a type name
		if !tname.empty() && maybeType {
			if !excludeTypeofBottom || (typetype == nil && core.Isa(core.TypeofBottomType, ty)) {
				if ml := cache.tname.lookup(typeofBottomName()); ml != nil {
					slurp := c.searchSlurp
					c.searchSlurp = offs + 1
					if !c.visit(ml, offs+1) {
						c.searchSlurp = slurp
						return false
					}
					if c.searchSlurp == 0 {
						excludeTypeofBottom = true
					}
					c.searchSlurp = slurp
				}
			}
			if excludeTypeofBottom && nameTN != nil && extractNamePrecise(typetype, true) {
				// just the type and its direct super types
				super := primaryOf(nameTN)
				if super.Name == typeofBottomName() {
					super = super.Super // handled above
				}
				for {
					if ml := cache.tname.lookup(super.Name); ml != nil {
						if !c.visit(ml, offs+1) {
							return false
						}
					}
					if super == core.AnyType || super.Super == nil {
						break
					}
					super = super.Super
				}
			} else {
				var sty any = core.AnyType
				if excludeTypeofBottom && !maybeKind {
					sty = ty
				}
				if !c.memVisit(cache.tname, sty, 3, offs) {
					return false
				}
			}
		}
		if !cache.name1.empty() {
			n := extractName(ty, false)
			if n != nil && extractNamePrecise(ty, false) {
				super := primaryOf(n)
				// direct lookup of concrete types
				for {
					if ml := cache.name1.lookup(super.Name); ml != nil {
						if !c.visit(ml, offs+1) {
							return false
						}
					}
					if super == core.AnyType || super.Super == nil {
						break
					}
					super = super.Super
				}
			} else if !c.memVisit(cache.name1, ty, 2, offs) {
				return false
			}
		}
	}
	if !c.nodeVisit(cache.linear) {
		return false
	}
	return c.visit(cache.any, offs+1)
}
