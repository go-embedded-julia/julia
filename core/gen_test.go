package core

import (
	"fmt"
	"math/rand/v2"
)

// typeGen builds random types through the public constructors, so that the
// differential test exercises construction and normalisation as well as
// subtyping. It is deterministic for a seed.
type typeGen struct {
	r     *rand.Rand
	nvars int
}

func newTypeGen(seed uint64) *typeGen {
	return &typeGen{r: rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))}
}

var genAtoms = func() []any {
	return []any{Int64Type, Float64Type, RealType, NumberType, IntegerType, SignedType, AnyType, Bottom,
		StringType, AbstractStringType, NothingType, Int8Type, BoolType, CharType, MissingType, DataTypeType}
}

// gen returns a type of depth at most d over the type variables in scope.
// ok is false when the constructors refused the combination (julia would
// raise too); callers draw again.
func (g *typeGen) gen(d int, scope []*TypeVar) (any, bool) {
	r := g.r
	atoms := genAtoms()
	if d <= 0 || r.IntN(10) < 3 {
		if len(scope) > 0 && r.IntN(3) == 0 {
			return scope[r.IntN(len(scope))], true
		}
		return atoms[r.IntN(len(atoms))], true
	}
	sub := func() (any, bool) { return g.gen(d-1, scope) }
	var t any
	var err error
	switch r.IntN(11) {
	case 0, 1: // Union
		a, ok1 := sub()
		b, ok2 := sub()
		if !ok1 || !ok2 {
			return nil, false
		}
		var res any
		func() {
			defer catchError(&err)
			res = TypeUnion(a, b)
		}()
		t = res
	case 2, 3: // Tuple, maybe with a Vararg
		n := r.IntN(4)
		ps := make([]any, 0, n+1)
		for i := 0; i < n; i++ {
			p, ok := sub()
			if !ok {
				return nil, false
			}
			ps = append(ps, p)
		}
		if r.IntN(3) == 0 {
			p, ok := sub()
			if !ok {
				return nil, false
			}
			var N any
			switch r.IntN(3) {
			case 1:
				N = int64(r.IntN(3))
			case 2:
				if len(scope) > 0 {
					N = scope[r.IntN(len(scope))]
				}
			}
			va, e := WrapVararg(p, N)
			if e != nil {
				return nil, false
			}
			ps = append(ps, va)
		}
		t, err = Apply(AnyTupleType, ps...)
	case 4: // Array{T,N}
		p, ok := sub()
		if !ok {
			return nil, false
		}
		var N any = int64(1 + r.IntN(2))
		if len(scope) > 0 && r.IntN(4) == 0 {
			N = scope[r.IntN(len(scope))]
		}
		t, err = Apply(ArrayType, p, N)
	case 5: // AbstractArray{T,1}
		p, ok := sub()
		if !ok {
			return nil, false
		}
		t, err = Apply(AbstractArrayType, p, int64(1))
	case 6: // Ref{T}
		p, ok := sub()
		if !ok {
			return nil, false
		}
		t, err = Apply(RefType, p)
	case 7: // Pair{A,B}
		a, ok1 := sub()
		b, ok2 := sub()
		if !ok1 || !ok2 {
			return nil, false
		}
		t, err = Apply(PairType, a, b)
	case 8: // Type{T}
		p, ok := sub()
		if !ok {
			return nil, false
		}
		t, err = Apply(TypeType, p)
	case 9, 10: // UnionAll
		g.nvars++
		name := fmt.Sprintf("T%d", g.nvars)
		var lb, ub any = Bottom, AnyType
		switch r.IntN(4) {
		case 1:
			ub = atoms[r.IntN(len(atoms))]
		case 2:
			lb = atoms[r.IntN(len(atoms))]
		}
		v, e := NewTypeVar(name, lb, ub)
		if e != nil {
			return nil, false
		}
		body, ok := g.gen(d-1, append(append([]*TypeVar(nil), scope...), v))
		if !ok {
			return nil, false
		}
		t = TypeUnionAll(v, body)
	}
	if err != nil || t == nil {
		return nil, false
	}
	return t, true
}

// closed draws a type with no free type variables.
func (g *typeGen) closed(d int) any {
	for {
		if t, ok := g.gen(d, nil); ok && !HasFreeTypeVars(t) {
			return t
		}
	}
}

// diagonal draws a type where one variable occurs several times in
// covariant position (Tuple{T, T} where T, Tuple{T, Vararg{T}} where T,
// Tuple{T, Ref{T}} where T ...), the shapes of the diagonal rule
// (subtype.c:974-995), which random draws rarely produce.
func (g *typeGen) diagonal() (any, bool) {
	r := g.r
	g.nvars++
	atoms := genAtoms()
	var lb, ub any = Bottom, AnyType
	switch r.IntN(3) {
	case 1:
		ub = atoms[r.IntN(len(atoms))]
	case 2:
		lb = atoms[r.IntN(len(atoms))]
	}
	v, err := NewTypeVar(fmt.Sprintf("D%d", g.nvars), lb, ub)
	if err != nil {
		return nil, false
	}
	var ps []any
	switch r.IntN(5) {
	case 0:
		ps = []any{v, v}
	case 1:
		ps = []any{v, &Vararg{T: v}}
	case 2:
		rv, err := Apply(RefType, v)
		if err != nil {
			return nil, false
		}
		ps = []any{v, rv}
	case 3:
		u, ok := g.gen(1, nil)
		if !ok {
			return nil, false
		}
		ps = []any{v, TypeUnion(v, u), v}
	default:
		ps = []any{v, v, v}
	}
	t, err := Apply(AnyTupleType, ps...)
	if err != nil {
		return nil, false
	}
	return TypeUnionAll(v, t), true
}

// sharedTemplates place two slots in positions of different variance; y
// fills both with one variable, x with two drawn types. They exercise the
// bounds an existential variable accumulates across its occurrences
// (var_lt, var_gt, equal_var), which random draws rarely share.
var sharedTemplates = []func(p, q any) (any, error){
	func(p, q any) (any, error) {
		t, err := Apply(AnyTupleType, p)
		if err != nil {
			return nil, err
		}
		return Apply(PairType, t, q)
	},
	func(p, q any) (any, error) {
		r, err := Apply(RefType, q)
		if err != nil {
			return nil, err
		}
		return Apply(PairType, p, r)
	},
	func(p, q any) (any, error) {
		t, err := Apply(AnyTupleType, p, q)
		if err != nil {
			return nil, err
		}
		return Apply(RefType, t)
	},
	func(p, q any) (any, error) {
		r, err := Apply(RefType, p)
		if err != nil {
			return nil, err
		}
		return Apply(AnyTupleType, r, q)
	},
	func(p, q any) (any, error) {
		pr, err := Apply(PairType, p, q)
		if err != nil {
			return nil, err
		}
		return Apply(ArrayType, pr, int64(1))
	},
	func(p, q any) (any, error) {
		return Apply(PairType, TypeUnion(p, NothingType), q)
	},
	func(p, q any) (any, error) {
		t, err := Apply(AnyTupleType, p)
		if err != nil {
			return nil, err
		}
		return Apply(AnyTupleType, t, &Vararg{T: q})
	},
}

func (g *typeGen) shared() (any, any, bool) {
	r := g.r
	f := sharedTemplates[r.IntN(len(sharedTemplates))]
	g.nvars++
	atoms := genAtoms()
	var lb, ub any = Bottom, AnyType
	switch r.IntN(4) {
	case 1:
		ub = atoms[r.IntN(len(atoms))]
	case 2:
		lb = atoms[r.IntN(len(atoms))]
	}
	v, err := NewTypeVar(fmt.Sprintf("S%d", g.nvars), lb, ub)
	if err != nil {
		return nil, nil, false
	}
	body, err := f(v, v)
	if err != nil {
		return nil, nil, false
	}
	a := g.closed(1)
	b := a
	switch r.IntN(3) {
	case 1:
		b = g.closed(1)
	case 2:
		if d, ok := a.(*DataType); ok && d.Super != nil && !HasFreeTypeVars(d.Super) {
			b = d.Super
		}
	}
	if r.IntN(2) == 0 {
		a, b = b, a
	}
	x, err := f(a, b)
	if err != nil {
		return nil, nil, false
	}
	return x, TypeUnionAll(v, body), true
}

// pair draws x and y, half of the time y a widening of x so that both
// answers are frequent; one time in five a diagonal y against a tuple, and
// one time in five a shared-variable template.
func (g *typeGen) pair() (any, any) {
	if g.r.IntN(4) == 0 {
		if x, y, ok := g.shared(); ok && !HasFreeTypeVars(x) {
			return x, y
		}
	}
	if g.r.IntN(5) == 0 {
		if y, ok := g.diagonal(); ok {
			n := 2 + g.r.IntN(2)
			ps := make([]any, n)
			for i := range ps {
				ps[i] = g.closed(1)
				if i > 0 && g.r.IntN(2) == 0 {
					ps[i] = ps[0]
				}
			}
			if x, err := Apply(AnyTupleType, ps...); err == nil {
				return x, y
			}
		}
	}
	x := g.closed(3)
	if g.r.IntN(2) == 0 {
		return x, g.closed(3)
	}
	for k := 0; k < 8; k++ {
		y, ok := g.widen(x, nil)
		if ok && !HasFreeTypeVars(y) {
			return x, y
		}
	}
	return x, g.closed(3)
}

// widen replaces a random sub-term of t by a supertype, a fresh type
// variable or a union with something else.
func (g *typeGen) widen(t any, scope []*TypeVar) (any, bool) {
	r := g.r
	if r.IntN(3) == 0 {
		switch r.IntN(3) {
		case 0:
			if d, ok := t.(*DataType); ok && d.Super != nil && !HasFreeTypeVars(d.Super) {
				return d.Super, true
			}
			return AnyType, true
		case 1:
			o, ok := g.gen(1, scope)
			if !ok {
				return nil, false
			}
			var res any
			var err error
			func() {
				defer catchError(&err)
				res = TypeUnion(t, o)
			}()
			return res, err == nil
		default:
			g.nvars++
			v := &TypeVar{Name: fmt.Sprintf("W%d", g.nvars), LB: Bottom, UB: AnyType}
			return TypeUnionAll(v, v), true
		}
	}
	d, ok := t.(*DataType)
	if !ok || len(d.Parameters) == 0 {
		return t, true
	}
	ps := append([]any(nil), d.Parameters...)
	i := r.IntN(len(ps))
	if _, isva := ps[i].(*Vararg); isva || isLong(ps[i]) {
		return t, true
	}
	g.nvars++
	v := &TypeVar{Name: fmt.Sprintf("W%d", g.nvars), LB: Bottom, UB: AnyType}
	if r.IntN(2) == 0 {
		w, ok := g.widen(ps[i], scope)
		if !ok {
			return nil, false
		}
		v.UB = w
	}
	ps[i] = v
	if d.Name == TupleTypeName {
		nt, err := Apply(AnyTupleType, ps...)
		if err != nil {
			return nil, false
		}
		return TypeUnionAll(v, nt), true
	}
	nt, err := Apply(d.Name.Wrapper, ps...)
	if err != nil {
		return nil, false
	}
	return TypeUnionAll(v, nt), true
}
