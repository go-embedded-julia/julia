package dispatch

import (
	"math/rand/v2"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// ring builds n methods and matches whose interference sets are edges[i]:
// m2 is in m.interference when m2 is more specific than m.
func ring(edges [][]int) ([]*Method, []*mlMatch) {
	ms := make([]*Method, len(edges))
	for i := range ms {
		ms[i] = &Method{full: core.AnyTupleType}
	}
	var tl []*mlMatch
	for i, m := range ms {
		for _, j := range edges[i] {
			m.interference = append(m.interference, ms[j])
		}
		tl = append(tl, &mlMatch{m: m, ti: core.AnyType})
	}
	return ms, tl
}

// sort_mlmatches on an interference graph by hand: methods each more
// specific than the one before, round a ring (specificity is not
// transitive), come out as one group, all ambiguous with one another.
func TestSortRing(t *testing.T) {
	_, tl := ring([][]int{{1}, {2}, {0}})
	s := &mlSort{t: tl, visited: make([]int, len(tl))}
	if got := s.sort(0, -1); got != 0 {
		t.Fatalf("sort: %d", got)
	}
	if len(s.result) != 3 || !s.hasAmbiguity {
		t.Errorf("a ring: %v ambiguous %v", s.result, s.hasAmbiguity)
	}
	// the same ring with room for two: too many matches
	s = &mlSort{t: tl, visited: make([]int, len(tl))}
	if got := s.sort(0, 2); got != -1 {
		t.Errorf("a ring past lim: %d", got)
	}
	// a ring of four with a chord, entered by the chord's end first: the
	// child that is still in the cycle is not visited again
	_, tl = ring([][]int{{2, 1}, {2}, {3}, {0}})
	s = &mlSort{t: tl, visited: make([]int, len(tl))}
	if got := s.sort(0, -1); got != 0 || len(s.result) != 4 {
		t.Errorf("a ring with a chord: %d %v", got, s.result)
	}
	// one of the ring fully covers and a minmax was found: it is dropped
	_, tl = ring([][]int{{1}, {2}, {0}})
	tl[1].fc = fullyCovers
	s = &mlSort{t: tl, visited: make([]int, len(tl)), foundMinmax: 2}
	if got := s.sort(0, -1); got != 0 || len(s.result) != 2 {
		t.Errorf("a ring with a covering member: %d %v", got, s.result)
	}
}

// Random interference graphs: the sort ends, with each vertex at most once in
// its output.
func TestSortRandomGraphs(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for it := 0; it < 20000; it++ {
		n := 2 + r.IntN(6)
		edges := make([][]int, n)
		for i := range edges {
			for j := 0; j < n; j++ {
				if i != j && r.IntN(3) == 0 {
					edges[i] = append(edges[i], j)
				}
			}
			r.Shuffle(len(edges[i]), func(a, b int) { edges[i][a], edges[i][b] = edges[i][b], edges[i][a] })
		}
		_, tl := ring(edges)
		s := &mlSort{t: tl, visited: make([]int, n), foundMinmax: r.IntN(3)}
		seen := map[int]bool{}
		for i := 0; i < n; i++ {
			if s.sort(i, -1) != 0 {
				t.Fatalf("sort of %v failed", edges)
			}
		}
		for _, i := range s.result {
			if seen[i] {
				t.Fatalf("%v: %d twice in %v", edges, i, s.result)
			}
			seen[i] = true
		}
	}
}

// The typemap's type algebra past its work bound panics with core's error,
// which the table's queries turn into an error.
func TestTypeMoreSpecificPastTheBound(t *testing.T) {
	tv := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	a := core.TypeUnionAll(tv, mustApply(t, core.AnyTupleType, tv, tv))
	b := mustApply(t, core.AnyTupleType, core.IntegerType, core.IntegerType)
	old := core.SetMaxSubtypeSteps(1)
	defer core.SetMaxSubtypeSteps(old)
	defer func() {
		if r := recover(); r == nil {
			t.Error("no panic")
		}
	}()
	typeMoreSpecific(b, a)
}

// A limit of 0 stops the search at the first method that is the only one
// for its signature, even deep in the caches: here under the Type{Vector{T}}
// of a query with a free variable.
func TestLimitZeroStopsInTypeCaches(t *testing.T) {
	v := mlTypes(t)
	tab := &Table{Name: "f", Head: v.head}
	vec := func(x any) any { return mustApply(t, core.ArrayType, x, int64(1)) }
	for _, x := range []any{core.Int64Type, core.Int8Type, core.Int16Type, core.Int32Type, core.UInt8Type, core.StringType, core.Float64Type, core.BoolType} {
		// eight Types of one parametric type name: the cache splits twice
		if _, err := tab.Add(mustApply(t, core.AnyTupleType, mustApply(t, core.TypeType, vec(x)))); err != nil {
			t.Fatal(err)
		}
	}
	tv := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	q := core.TypeUnionAll(tv, mustApply(t, core.AnyTupleType, v.head, mustApply(t, core.TypeType, vec(tv))))
	res, err := tab.MlMatches(q, 0, false, tab.World(), 0, ^uint64(0))
	if err != nil || !res.Nothing {
		t.Errorf("lim 0: %v %v", res, err)
	}
}

// A method redefined while a wider one stands, and another more specific
// one, is in the wider one's interference set as the replaced one was.
func TestReplacedInInterferences(t *testing.T) {
	v := mlTypes(t)
	tab := &Table{Name: "f", Head: v.head}
	sig := func(ps ...any) any { return mustApply(t, core.AnyTupleType, ps...) }
	var ms []*Method
	for _, s := range []any{sig(v.a, core.AnyType), sig(core.AnyType, core.AnyType), sig(v.s1, v.s1), sig(v.a, core.AnyType)} {
		m, err := tab.Add(s)
		if err != nil {
			t.Fatal(err)
		}
		ms = append(ms, m)
	}
	tt := mustApply(t, core.AnyTupleType, v.head, v.s1, v.s1)
	res, err := tab.MlMatches(tt, -1, true, tab.World(), 0, ^uint64(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.List) == 0 {
		t.Fatal("no match")
	}
	if !hasKey(ms[1].interference, ms[3]) {
		t.Errorf("the new method is not in the wider one's interferences: %d", len(ms[1].interference))
	}
}
