package dispatch

import (
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// A wider vocabulary than the differential test's, for the paths of the
// typemap that test cannot pin to julia (hash-ordered caches, parametric
// type names, kinds, Union{}): the tables are random, the queries abstract
// and concrete, and what is checked is what holds whatever julia's order
// is: the query gives the same answer twice, every match intersects the
// query, the method list holds no method twice, and lim bounds the list.

type wideGen struct {
	r  *rand.Rand
	t  testing.TB
	v  *mlVocab
	up []any // the atoms
	// narrow draws from the first atoms only, so that the methods overlap
	narrow bool
	// parametric draws Vectors of many element types and Type{Vector{…}}s,
	// whose type name has many instances, the caches' double split
	parametric bool
	par        []any
	// ambig draws from a few types that overlap in pairs
	ambig bool
	amb   []any
}

func newWideGen(t testing.TB, seed uint64) *wideGen {
	v := mlTypes(t)
	g := &wideGen{r: rand.New(rand.NewPCG(seed, 3)), t: t, v: v}
	vec := func(x any) any { return mustApply(t, core.ArrayType, x, int64(1)) }
	typeOf := func(x any) any { return mustApply(t, core.TypeType, x) }
	g.up = []any{v.a, v.b, v.c, v.d, v.s1, v.s2, v.s3, v.s4, core.Int64Type, core.Int8Type, core.IntegerType, core.RealType,
		core.NumberType, core.Float64Type, core.StringType, core.SymbolType, core.ModuleType, core.NothingType, core.AnyType,
		vec(core.Int64Type), vec(core.StringType), vec(core.NothingType), vec(core.IntegerType), vec(core.AnyType),
		mustApply(t, core.ArrayType, core.Int64Type, int64(2)), mustApply(t, core.RefType, core.Int64Type),
		core.TypeUnion(v.s1, v.s2), core.TypeUnion(v.b, core.Int64Type), core.TypeUnion(core.Int64Type, core.StringType),
		core.TypeUnion(core.NothingType, core.Int64Type), core.Bottom, core.TypeofBottomType, core.DataTypeType, core.UnionType,
		core.UnionAllType, core.TypeType, typeOf(v.s1), typeOf(v.a), typeOf(core.Int64Type), typeOf(core.IntegerType),
		typeOf(core.Bottom), typeOf(core.TypeofBottomType), typeOf(core.DataTypeType), typeOf(core.AnyType),
		typeOf(vec(core.Int64Type)), typeOf(vec(core.IntegerType)), typeOf(core.TypeUnion(v.s1, v.s2)),
		typeOf(core.TypeUnion(core.Int64Type, core.StringType)), typeOf(typeOf(core.Int64Type)),
		mustApply(t, core.AnyTupleType, core.Int64Type), mustApply(t, core.AnyTupleType, core.Int64Type, core.StringType),
		core.AnyTupleType, core.EmptyTupleType, core.FunctionType, core.AbstractArrayType,
		core.TypeUnion(vec(core.Int64Type), vec(core.StringType)), typeOf(core.TypeUnion(vec(core.Int64Type), vec(core.StringType))),
		core.TypeUnion(typeOf(v.s1), core.Int64Type), core.TypeUnion(typeOf(v.a), v.s1), core.TypeUnion(core.IntegerType, core.SignedType),
		typeOf(core.AbstractArrayType),
		core.TypeUnion(mustApply(t, core.AbstractArrayType, core.Int64Type, int64(1)), mustApply(t, core.AbstractArrayType, core.StringType, int64(1)))}
	g.amb = []any{v.a, v.b, v.c, v.s1, v.s2, core.AnyType, core.AnyType, v.s3}
	for _, x := range []any{core.Int64Type, core.Int8Type, core.Int16Type, core.Int32Type, core.UInt8Type, core.StringType,
		core.Float64Type, core.NothingType, core.SymbolType, core.BoolType} {
		g.par = append(g.par, vec(x), typeOf(vec(x)))
	}
	g.par = append(g.par, core.AbstractArrayType, core.ArrayType, vec(core.IntegerType), vec(core.AnyType), typeOf(vec(core.IntegerType)),
		typeOf(core.ArrayType), core.AnyType, core.TypeType, typeOf(mustApply(t, core.RefType, core.Int64Type)), typeOf(core.RefType),
		mustApply(t, core.RefType, core.Int64Type), mustApply(t, core.RefType, core.StringType))
	return g
}

func (g *wideGen) atom() any {
	if g.parametric {
		return g.par[g.r.IntN(len(g.par))]
	}
	if g.ambig {
		return g.amb[g.r.IntN(len(g.amb))]
	}
	if g.narrow {
		return g.up[g.r.IntN(11)] // the user types, Int64, Int8, Integer
	}
	return g.up[g.r.IntN(len(g.up))]
}

func (g *wideGen) typeVar(i int) *core.TypeVar {
	tv := &core.TypeVar{Name: string(rune('T' + i)), LB: core.Bottom, UB: core.AnyType}
	switch g.r.IntN(6) {
	case 5:
		tv.UB = core.Bottom
	case 1:
		tv.UB = g.v.a
	case 2:
		tv.UB = core.IntegerType
	case 3:
		tv.UB = core.TypeUnion(g.v.s1, g.v.s2)
	case 4:
		tv.LB = g.v.s1
	}
	return tv
}

func (g *wideGen) param(vars []*core.TypeVar) any {
	if len(vars) > 0 && g.r.IntN(4) == 0 {
		tv := vars[g.r.IntN(len(vars))]
		switch g.r.IntN(5) {
		case 4:
			return mustApply(g.t, core.TypeType, mustApply(g.t, core.ArrayType, tv, int64(1)))
		case 0:
			return mustApply(g.t, core.TypeType, tv)
		case 1:
			return mustApply(g.t, core.ArrayType, tv, int64(1))
		case 2:
			return core.TypeUnion(tv, core.NothingType)
		}
		return tv
	}
	return g.atom()
}

func (g *wideGen) sig(head any) any {
	var vars []*core.TypeVar
	for i := g.r.IntN(3); i > 0; i-- {
		vars = append(vars, g.typeVar(len(vars)))
	}
	ps := []any{}
	if head != nil {
		ps = append(ps, head)
	}
	for i := g.r.IntN(4); i > 0; i-- {
		ps = append(ps, g.param(vars))
	}
	switch g.r.IntN(7) {
	case 0:
		ps = append(ps, &core.Vararg{T: g.param(vars)})
	case 1:
		ps = append(ps, &core.Vararg{})
	case 2:
		ps = append(ps, &core.Vararg{T: g.param(vars), N: int64(1 + g.r.IntN(2))})
	}
	t, err := core.Apply(core.AnyTupleType, ps...)
	if err != nil {
		return nil
	}
	for i := len(vars) - 1; i >= 0; i-- {
		t = core.TypeUnionAll(vars[i], t)
	}
	return t
}

func (g *wideGen) query(head any) any {
	ps := []any{}
	if head != nil {
		ps = append(ps, head)
	}
	for i := g.r.IntN(4); i > 0; i-- {
		ps = append(ps, g.atom())
	}
	if g.r.IntN(3) == 0 {
		ps = append(ps, &core.Vararg{T: g.atom()})
	}
	t, err := core.Apply(core.AnyTupleType, ps...)
	if err != nil {
		return nil
	}
	if g.r.IntN(5) == 0 {
		tv := g.typeVar(0)
		var arg any = mustApply(g.t, core.TypeType, tv)
		if g.r.IntN(2) == 0 {
			arg = mustApply(g.t, core.TypeType, mustApply(g.t, core.ArrayType, tv, int64(1)))
		}
		t = core.TypeUnionAll(tv, mustApply(g.t, core.AnyTupleType, append(ps, arg)...))
	}
	return t
}

func TestMlMatchesWide(t *testing.T) {
	v := mlTypes(t)
	g := newWideGen(t, 5)
	answered, matched := 0, 0
	for i := 0; i < 60000; i++ {
		var head any
		tab := &Table{Name: "f"}
		if i%3 != 0 {
			head, tab.Head = v.head, v.head
		}
		g.parametric = i%4 == 1
		g.ambig = i%8 == 3 || i%8 == 4 || i%8 == 6
		g.narrow = i%2 == 0 && !g.ambig
		var ms []*Method
		var sigs []any
		for n := 2 + g.r.IntN(12); n > 0; n-- {
			s := g.sig(nil)
			if s != nil && len(sigs) > 0 && g.r.IntN(6) == 0 {
				s = sigs[g.r.IntN(len(sigs))] // a method redefined
			}
			if s != nil {
				m, err := tab.Add(s)
				if err != nil {
					t.Fatal(err)
				}
				ms = append(ms, m)
				sigs = append(sigs, s)
			}
		}
		if i%11 == 0 && len(ms) > 2 {
			tab.Disable(ms[g.r.IntN(len(ms))], tab.World())
		}
		for q := 0; q < 6; q++ {
			tt := g.query(head)
			if tt == nil || (head == nil && core.UnwrapUnionAll(tt).(*core.DataType).Parameters == nil) {
				continue
			}
			if head == nil {
				// the callee's type first, as the other tables keep it
				if len(core.UnwrapUnionAll(tt).(*core.DataType).Parameters) == 0 {
					continue
				}
			}
			lim := []int{-1, -1, 0, 1, 2, 5}[g.r.IntN(6)]
			incl := g.r.IntN(2) == 0
			w := tab.World()
			if g.r.IntN(4) == 0 {
				w = uint64(g.r.IntN(int(w) + 1)) // an older world
			}
			res, err := tab.MlMatches(tt, lim, incl, w, 0, ^uint64(0))
			if err != nil {
				t.Fatalf("%s: %v", core.Show(tt), err)
			}
			again, err := tab.MlMatches(tt, lim, incl, w, 0, ^uint64(0))
			if err != nil || len(again.List) != len(res.List) || again.Nothing != res.Nothing || again.Ambig != res.Ambig {
				t.Fatalf("%s: the same query answered twice differently", core.Show(tt))
			}
			answered++
			seen := map[*Method]bool{}
			for _, m := range res.List {
				if seen[m.Method] {
					t.Fatalf("%s: %p twice", core.Show(tt), m.Method)
				}
				seen[m.Method] = true
				matched++
			}
			if lim >= 0 && len(res.List) > lim {
				t.Fatalf("%s: %d matches for lim %d", core.Show(tt), len(res.List), lim)
			}
			if res.Nothing && len(res.List) != 0 {
				t.Fatalf("%s: nothing with a list", core.Show(tt))
			}
		}
	}
	t.Logf("%d queries, %d matches", answered, matched)
}

// Every bound from 1 up makes the replay or the search fail somewhere else:
// each failure is an error and the table then stays failed.
func TestMlMatchesWorkBoundSweep(t *testing.T) {
	v := mlTypes(t)
	tv := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	tu := &core.TypeVar{Name: "U", LB: core.Bottom, UB: v.a}
	vecT := mustApply(t, core.ArrayType, tv, int64(1))
	sigs := []any{
		core.TypeUnionAll(tv, mustApply(t, core.AnyTupleType, tv, tv)),
		mustApply(t, core.AnyTupleType, v.s1, core.AnyType), mustApply(t, core.AnyTupleType, core.AnyType, v.s1),
		core.TypeUnionAll(tv, mustApply(t, core.AnyTupleType, vecT, tv)),
		mustApply(t, core.AnyTupleType, core.IntegerType, core.RealType), mustApply(t, core.AnyTupleType, core.RealType, core.IntegerType),
		core.TypeUnionAll(tu, mustApply(t, core.AnyTupleType, tu, mustApply(t, core.TypeType, tu))),
		mustApply(t, core.AnyTupleType, core.TypeUnion(v.b, core.Int64Type), core.AnyType),
		mustApply(t, core.AnyTupleType, v.d, v.s3), mustApply(t, core.AnyTupleType, core.AnyType, core.AnyType),
	}
	queries := []any{
		mustApply(t, core.AnyTupleType, v.head, v.s1, v.s1), mustApply(t, core.AnyTupleType, v.head, core.Int64Type, core.Int64Type),
		mustApply(t, core.AnyTupleType, v.head, &core.Vararg{}), mustApply(t, core.AnyTupleType, v.head, v.d, v.s3),
	}
	failed := 0
	for n := 1; n < 600; n++ {
		tab := &Table{Name: "f", Head: v.head}
		for _, s := range sigs {
			if _, err := tab.Add(s); err != nil {
				t.Fatal(err)
			}
		}
		old := core.SetMaxSubtypeSteps(n)
		for _, q := range queries {
			for _, incl := range []bool{false, true} {
				if _, err := tab.MlMatches(q, -1, incl, tab.World(), 0, ^uint64(0)); err != nil {
					failed++
				}
			}
		}
		core.SetMaxSubtypeSteps(old)
	}
	if failed == 0 {
		t.Error("no bound made a query fail")
	}
}

// A query past the work bound is an error, from the replay of the table
// and from the search, and the table stays failed.
func TestMlMatchesWorkBound(t *testing.T) {
	v := mlTypes(t)
	tv := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	sigs := []any{core.TypeUnionAll(tv, mustApply(t, core.AnyTupleType, tv, tv)),
		mustApply(t, core.AnyTupleType, core.Int64Type, core.Int64Type), mustApply(t, core.AnyTupleType, core.AnyType, core.AnyType)}
	tab := &Table{Name: "f", Head: v.head}
	for _, s := range sigs {
		if _, err := tab.Add(s); err != nil {
			t.Fatal(err)
		}
	}
	tt := mustApply(t, core.AnyTupleType, v.head, core.IntegerType, core.IntegerType)
	vec := tt
	old := core.SetMaxSubtypeSteps(1)
	_, err := tab.MlMatches(tt, -1, false, tab.World(), 0, ^uint64(0))
	core.SetMaxSubtypeSteps(old)
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("past the work bound: %v", err)
	}
	if _, err2 := tab.MlMatches(tt, -1, false, tab.World(), 0, ^uint64(0)); err2 == nil {
		t.Error("a failed table answers")
	}

	// the search past the bound, the table intact
	tab2 := &Table{Name: "f", Head: v.head}
	for _, s := range sigs {
		if _, err := tab2.Add(s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tab2.MlMatches(tt, -1, false, tab2.World(), 0, ^uint64(0)); err != nil {
		t.Fatal(err)
	}
	old = core.SetMaxSubtypeSteps(1)
	_, err = tab2.MlMatches(vec, -1, false, tab2.World(), 0, ^uint64(0))
	core.SetMaxSubtypeSteps(old)
	if !errors.As(err, &ce) {
		t.Fatalf("a search past the work bound: %v", err)
	}
}
