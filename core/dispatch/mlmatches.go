package dispatch

import (
	"github.com/go-embedded-julia/julia/core"
)

// ml_matches (gf.c:4602-4927) and the interference sets it sorts with
// (gf.c:2630-2754, 2757-3015). julia 1.12 computes, when a method is
// inserted, the set of methods that intersect it and are not less specific;
// ml_matches visits the typemap for the candidates and orders them with
// that set. This file ports both. A table keeps its insertions in a log
// and replays them into the typemap the first time a query needs the
// answer, so a table nothing asks about costs nothing.

// Match is one jl_method_match_t.
type Match struct {
	Method *Method
	// Types is the intersection of the query with the method's signature,
	// Env the values of the signature's static parameters for it.
	Types       any
	Env         []any
	FullyCovers bool
}

// Matches is what ml_matches returns.
type Matches struct {
	// List is the matches in julia's order; Nothing is julia's `nothing`: the
	// matches exceed the limit.
	List     []Match
	Nothing  bool
	MinValid uint64
	MaxValid uint64
	// Ambig is whether the order is not exact because of an ambiguity.
	Ambig bool
}

// fullSig is the signature as julia keeps it, typeof(f) first.
func (t *Table) fullSig(sig any) any {
	if t.Head == nil {
		return sig
	}
	body := core.UnwrapUnionAll(sig).(*core.DataType)
	params := append([]any{t.Head}, body.Parameters...)
	return core.RewrapUnionAll(core.TupleType(params...), sig)
}

// vaOf is the element type of the Vararg last in the tuple type, nil when
// there is none (gf.c:4613-4621).
func vaOf(ttypes *core.DataType) any {
	if n := len(ttypes.Parameters); n > 0 {
		if va, ok := ttypes.Parameters[n-1].(*core.Vararg); ok {
			return unwrapVararg(va)
		}
	}
	return nil
}

// hasKey is has_key (gf.c:2631-2641) over a method list.
func hasKey(keys []*Method, k *Method) bool {
	for _, m := range keys {
		if m == k {
			return true
		}
	}
	return false
}

// inInterferences is method_in_interferences (gf.c:2643-2647): m2 is in
// m1's interference set, so m1 is not more specific than m2.
func inInterferences(m2, m1 *Method) bool { return hasKey(m1.interference, m2) }

// getIntersectMatches is get_intersect_matches with get_intersect_visitor
// (gf.c:1886-1945): the methods of the typemap valid in world that
// intersect the new entry, in the order the typemap yields them, and the
// entry the new one replaces.
func (t *Table) getIntersectMatches(newe *tmEntry, world uint64) (shadowed []*Method, found bool, replaced *tmEntry, err error) {
	env := &isectEnv{typ: newe.sig, va: vaOf(newe.ttypes), minValid: world, maxValid: world}
	env.fptr = func(old *tmEntry) bool {
		// don't need to consider other similar methods if this oldentry will
		// always fully intersect with them and dominates all of them
		if env.issubty && core.Subtype(old.m.full, newe.sig) {
			if replaced == nil || replaced.m.PrimaryWorld < old.m.PrimaryWorld {
				replaced = old // the newest insertion
			}
		}
		found = true
		if env.issubty && old.m.latestOnly {
			shadowed = append(shadowed[:0], old.m)
			return false
		}
		shadowed = append(shadowed, old.m)
		env.slurpSearch(old)
		return true
	}
	env.visit(t.tm.root, 0)
	return shadowed, found, replaced, env.err
}

// typeMoreSpecific is jl_type_morespecific; a failure of the type algebra
// (a query past its work bound) is raised as the panic the callers' deferred
// core.CatchError returns as an error.
func typeMoreSpecific(a, b any) bool {
	more, err := core.MoreSpecific(a, b)
	if err != nil {
		panic(err)
	}
	return more
}

// activate is the part of jl_method_table_activate (gf.c:2757-3015) that
// computes the interference sets and the dispatch status bit: what m's
// insertion does to the methods it intersects. (The invalidation of
// compiled code and caches it also does has no counterpart here.)
func (t *Table) activate(e *tmEntry) error {
	method := e.m
	maxWorld := method.PrimaryWorld - 1
	d, found, replaced, err := t.getIntersectMatches(e, maxWorld)
	if err != nil {
		return err
	}
	latestOnly := true // METHOD_SIG_LATEST_ONLY, tentatively
	interferences := method.interference
	if found {
		if replaced != nil {
			m := replaced.m
			if !m.latestOnly {
				latestOnly = false
			}
			m.latestOnly = false
			// take over the interference list from the replaced method (the new
			// method's own is empty here: julia merges into one a precompiled
			// method came with, gf.c:2827-2837, which this table never has)
			interferences = append([]*Method(nil), m.interference...)
			m.interference = append(m.interference, method)
			for _, m2 := range d {
				if inInterferences(m, m2) {
					m2.interference = append(m2.interference, method)
				}
			}
		} else {
			morespec := make([]bool, len(d))
			for j, m := range d {
				morespec[j] = typeMoreSpecific(m.full, e.sig)
			}
			for j, m := range d {
				// is there an ambiguity between the new method and the old m?
				ambig := !morespec[j] && !typeMoreSpecific(e.sig, m.full)
				if morespec[j] || ambig {
					// !morespecific(new, old): the old method joins the new one's set
					latestOnly = false
					if !hasKey(interferences, m) {
						interferences = append(interferences, m)
					}
				}
				if !morespec[j] {
					// !morespecific(old, new): the new method joins the old one's
					m.latestOnly = false
					m.interference = append(m.interference, method)
				}
			}
		}
	}
	method.latestOnly = latestOnly
	method.interference = interferences
	return nil
}

// sync replays the table's insertions and disables into the typemap
// (jl_method_table_add, gf.c:2608-2629, then jl_method_table_activate;
// jl_method_table_disable, gf.c:2585-2606). A failure of the type
// algebra is kept: the table cannot be answered for again.
func (t *Table) sync() (err error) {
	if t.failed != nil {
		return t.failed
	}
	defer func() {
		if err != nil {
			t.failed = err
		}
	}()
	defer core.CatchError(&err)
	if t.tm.root == nil {
		// julia's one method table is a level from its first methods on,
		// whatever function a query is about
		t.tm.root = &tmLevel{}
	}
	for t.replayed < len(t.events) {
		ev := t.events[t.replayed]
		t.replayed++
		if ev.disable {
			ev.m.latestOnly = false
			continue
		}
		full := t.fullSig(ev.m.Sig)
		ev.m.full = full
		e := newTmEntry(full, ev.m)
		t.tm.insert(e)
		if err := t.activate(e); err != nil {
			return err
		}
	}
	return nil
}

// mlMatch is a method match in the making, whose fully_covers takes the
// three values of enum SIGNATURE_FULLY_COVERS (gf.c:4383-4387).
type mlMatch struct {
	m   *Method
	ti  any
	env []any
	fc  int
}

const (
	notFullyCovers = 0
	fullyCovers    = 1
	sentinel       = 2
)

func findMethodInMatches(t []*mlMatch, m *Method) int {
	for i, matc := range t {
		if matc.m == m {
			return i
		}
	}
	return -1
}

// checkInterferencesCovers is check_interferences_covers (gf.c:2662-2692).
func checkInterferencesCovers(m *Method, ti any, t []*mlMatch, visited []int, recursionStack *[]*Method) bool {
	// already visiting this method: cycle detection and memoization
	for _, r := range *recursionStack {
		if r == m {
			return false
		}
	}
	*recursionStack = append(*recursionStack, m)
	for _, m2 := range m.interference {
		idx := findMethodInMatches(t, m2)
		if idx < 0 {
			continue
		}
		if inInterferences(m, m2) {
			continue // ambiguous
		}
		if visited[idx] != 1 {
			continue // part of the same SCC cycle (handled by ambiguity later)
		}
		if core.Subtype(ti, m2.full) {
			return true
		}
		// m2 is more specific: check its interferences too
		if checkInterferencesCovers(m2, ti, t, visited, recursionStack) {
			return true
		}
	}
	return false
}

// checkFullyAmbiguous is check_fully_ambiguous (gf.c:2694-2711).
func checkFullyAmbiguous(m *Method, ti any, t []*mlMatch, includeAmbiguous bool, hasAmbiguity *bool) bool {
	for _, m2 := range m.interference {
		if findMethodInMatches(t, m2) < 0 {
			continue
		}
		if !inInterferences(m, m2) {
			continue
		}
		*hasAmbiguity = true
		if !includeAmbiguous && core.Subtype(ti, m2.full) {
			return true
		}
	}
	return false
}

// inInterferencesRecursive is method_in_interferences_recursive
// (gf.c:2713-2742): target is more specific than start, through the
// edges to more specific methods.
func inInterferencesRecursive(target, start *Method, seen *[]*Method) bool {
	if inInterferences(start, target) {
		return false
	}
	if inInterferences(target, start) {
		return true
	}
	for _, s := range *seen {
		if s == start {
			return false
		}
	}
	*seen = append(*seen, start)
	for _, im := range start.interference {
		if inInterferences(start, im) {
			continue // only the edges to more specific methods, not ambiguities
		}
		if inInterferencesRecursive(target, im, seen) {
			return true
		}
	}
	return false
}

// moreSpecificViaInterferences is method_morespecific_via_interferences
// (gf.c:2744-2754).
func moreSpecificViaInterferences(target, start *Method) bool {
	// (julia returns 0 for target == start; ml_matches never asks that: it
	// compares a method with the others, never with itself)
	var seen []*Method
	return inInterferencesRecursive(target, start, &seen)
}

// mlSort is the state of sort_mlmatches.
type mlSort struct {
	t                []*mlMatch
	visited          []int
	stack            []int
	result           []int
	recursionStack   []*Method
	includeAmbiguous bool
	hasAmbiguity     bool
	foundMinmax      int
}

// sort is sort_mlmatches (gf.c:4487-4589): Tarjan's strongly connected
// components over the interference sets, visiting the more specific
// methods first. -1 is too many matches for lim; 0 the children are in the
// output; 1+ the children are part of this SCC, up to this depth.
func (s *mlSort) sort(idx, lim int) int {
	cycle := s.visited[idx]
	if cycle != 0 {
		return cycle - 1 // depth remaining
	}
	s.stack = append(s.stack, idx)
	depth := len(s.stack)
	{
		s.visited[idx] = 1 + depth
		matc := s.t[idx]
		m := matc.m
		ti := matc.ti
		subt := matc.fc != notFullyCovers
		cycle = depth
		// iterate over the interference set to get the more specific methods
		for _, m2 := range m.interference {
			childidx := findMethodInMatches(s.t, m2)
			if childidx < 0 || childidx == idx {
				continue
			}
			childCycle := s.visited[childidx]
			if childCycle == 1 {
				continue // already handled
			}
			if childCycle != 0 && childCycle-1 >= cycle {
				continue // already part of this cycle
			}
			if inInterferences(m, m2) {
				continue
			}
			// m2 is more specific, so attempt to visit it first
			childCycle = s.sort(childidx, lim)
			if childCycle == -1 {
				return -1
			}
			// record the cycle will resolve at depth "cycle"
			if childCycle != 0 && childCycle < cycle {
				cycle = childCycle
			}
		}
		// this method may already be fully covered now, and can be deleted
		// without anyone noticing
		if subt && s.foundMinmax != 0 {
			if s.foundMinmax == 2 {
				s.visited[idx] = 1
			}
		} else {
			// recursion_stack is never emptied in julia: it also memoizes the
			// methods already visited, across the calls of one ml_matches
			if checkInterferencesCovers(m, ti, s.t, s.visited, &s.recursionStack) {
				s.visited[idx] = 1
			} else if checkFullyAmbiguous(m, ti, s.t, s.includeAmbiguous, &s.hasAmbiguity) {
				s.visited[idx] = 1
			}
		}
		// no cycles hit either: all its edges can potentially go too
		if s.visited[idx] == 1 && len(s.stack) == depth {
			s.stack = s.stack[:len(s.stack)-1]
			return 0
		}
		if cycle != depth {
			return cycle
		}
	}
	// in an SCC group: more checks before returning or setting has_ambiguity
	if depth != len(s.stack) {
		count := 0
		for i := depth - 1; i < len(s.stack); i++ {
			if s.visited[s.stack[i]] == 1 {
				continue
			}
			count++
		}
		if count > 1 {
			s.hasAmbiguity = true
		}
	}
	// copy this cycle into the results
	for i := depth - 1; i < len(s.stack); i++ {
		childidx := s.stack[i]
		subt := s.t[childidx].fc != notFullyCovers
		if subt && s.foundMinmax != 0 {
			s.visited[childidx] = 1
		}
		if s.visited[childidx] == 1 {
			continue
		}
		s.visited[childidx] = 1
		if lim == -1 || len(s.result) < lim {
			s.result = append(s.result, childidx)
		} else {
			return -1
		}
	}
	// finally clean up the stack
	for len(s.stack) >= depth {
		childidx := s.stack[len(s.stack)-1]
		s.stack = s.stack[:len(s.stack)-1]
		// always remove fully_covers matches after the first minmax ambiguity
		// group is handled
		if s.t[childidx].fc == fullyCovers && s.foundMinmax == 1 {
			s.foundMinmax = 2
		}
	}
	return 0
}

// MlMatches is ml_matches (gf.c:4602-4927) with intersections on, as
// jl_matching_methods calls it (gf.c:3113-3127): the methods of the table
// that intersect tt, a Tuple type (typeof(f) first, wrapped in the UnionAlls
// of its variables), valid in world; julia's order, which puts a more
// specific method before the ones it has an intersection with. lim is the
// most matches to return, -1 for any number; minValid and maxValid the
// world range to narrow. The jl_mt_assoc_by_type cache shortcuts
// (gf.c:4655-4725) answer what the full search does and are left out.
func (t *Table) MlMatches(tt any, lim int, includeAmbiguous bool, world, minValid, maxValid uint64) (res *Matches, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.sync(); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			res = nil
		}
	}()
	defer core.CatchError(&err)
	unw := core.UnwrapUnionAll(tt).(*core.DataType)
	env := &isectEnv{typ: tt, va: vaOf(unw), minValid: minValid, maxValid: maxValid}
	var tl []*mlMatch
	curLim, aborted := lim, false
	// ml_matches_visitor (gf.c:4406-4463)
	env.fptr = func(e *tmEntry) bool {
		// the world range of the entry against the query's: an entry outside it
		// narrows the answer's range, so that leaving it out is valid for all
		minWorld, maxWorld := e.m.PrimaryWorld, e.m.MaxWorld
		if world < minWorld {
			if env.maxValid >= minWorld {
				env.maxValid = minWorld - 1
			}
			return true
		} else if world > maxWorld {
			if env.minValid <= maxWorld {
				env.minValid = maxWorld + 1
			}
			return true
		}
		if env.maxValid > maxWorld {
			env.maxValid = maxWorld
		}
		only := e.m.latestOnly
		if curLim >= 0 && only {
			if curLim == 0 {
				tl, aborted = nil, true
				return false
			}
			curLim--
		}
		fc := notFullyCovers
		if env.issubty {
			fc = fullyCovers
		}
		matc := &mlMatch{m: e.m, ti: env.ti, env: env.env, fc: fc}
		if env.issubty && only {
			tl = []*mlMatch{matc}
			return false
		}
		tl = append(tl, matc)
		// don't need to consider other similar methods if this ml will always
		// fully intersect with them and dominates all of them
		if !includeAmbiguous || curLim != -1 {
			env.slurpSearch(e)
		}
		return true
	}
	env.visit(t.tm.root, 0)
	if env.err != nil {
		return nil, env.err
	}
	// if we return early, only the min/max valid collected from matching
	res = &Matches{MinValid: env.minValid, MaxValid: env.maxValid}
	if aborted {
		res.Nothing = true
		return res, nil
	}
	// all intersecting methods have been collected: sort them and apply
	// specificity to find the dispatch-possible call targets
	length := len(tl)
	var minmax *mlMatch
	anySubtypes := false
	hasAmbiguity := false
	if length > 1 {
		// the 'minmax' method fully covers the queried type and is more specific
		// than any other fully-covering method; first find a candidate
		for i := 0; i < length; i++ {
			matc := tl[i]
			if matc.fc == fullyCovers {
				anySubtypes = true
				j := 0
				for ; j < length; j++ {
					if i == j {
						continue
					}
					if matc2 := tl[j]; matc2.fc == fullyCovers && !moreSpecificViaInterferences(matc.m, matc2.m) {
						break
					}
				}
				if j == length {
					minmax = matc
					break
				}
			}
		}
		// it may dominate some choices that are not fully covering: move those
		// into the subtype group, to filter them out shortly after
		allSubtypes := anySubtypes
		if anySubtypes {
			var minmaxm *Method
			if minmax != nil {
				minmaxm = minmax.m
			}
			for i := 0; i < length; i++ {
				matc := tl[i]
				if matc.fc != fullyCovers {
					if minmaxm != nil {
						if moreSpecificViaInterferences(minmaxm, matc.m) {
							matc.fc = sentinel // a sentinel value for sorting
							continue
						}
						if inInterferences(minmaxm, matc.m) { // !morespecific(m, minmaxm)
							hasAmbiguity = true
						}
					}
					allSubtypes = false
				}
			}
		}
		// a fast return when every possible output has been processed
		if allSubtypes {
			if minmax == nil {
				// all intersecting methods fully cover, but none is the most specific
				if !includeAmbiguous {
					length, tl = 0, nil
				} else if lim == 1 {
					res.Nothing = true
					return res, nil
				}
			} else {
				// minmax is more specific than every other match: the only result
				tl, length = tl[:1], 1
				tl[0] = minmax
			}
		}
		if minmax != nil && lim == 0 {
			res.Nothing = true // protect some later algorithms from underflow
			return res, nil
		}
	}
	if length > 1 {
		s := &mlSort{t: tl, visited: make([]int, length), includeAmbiguous: includeAmbiguous}
		// with a minmax method (any subtypes), some methods may be cleaned up
		// quickly
		switch {
		case hasAmbiguity:
			s.foundMinmax = 1
		case minmax != nil:
			s.foundMinmax = 2
		case anySubtypes && !includeAmbiguous:
			s.foundMinmax = 1
		}
		for i := 0; i < length; i++ {
			if tl[i].fc != notFullyCovers && s.foundMinmax != 0 {
				// handled above and below already: nothing new to learn
				continue
			}
			l := lim
			if lim != -1 && minmax != nil {
				l = lim - 1
			}
			if s.sort(i, l) == -1 {
				res.Nothing = true
				return res, nil
			}
		}
		hasAmbiguity = s.hasAmbiguity
		out := make([]*mlMatch, 0, len(s.result)+1)
		for _, i := range s.result {
			// (julia also resets the sentinel markers here, gf.c:4886-4888; one
			// is never in the result: with a minmax the sort drops every match
			// that is not NOT_FULLY_COVERS)
			out = append(out, tl[i])
		}
		if minmax != nil {
			out = append(out, minmax)
		}
		tl, length = out, len(out)
	}
	for _, matc := range tl[:length] {
		// method applicability is the same as typemapentry applicability
		if res.MinValid < matc.m.PrimaryWorld {
			res.MinValid = matc.m.PrimaryWorld
		}
	}
	res.Ambig = hasAmbiguity
	if lim >= 0 && length > lim {
		res.Nothing = true
		return res, nil
	}
	for _, matc := range tl[:length] {
		res.List = append(res.List, Match{Method: matc.m, Types: matc.ti, Env: matc.env, FullyCovers: matc.fc == fullyCovers})
	}
	return res, nil
}
