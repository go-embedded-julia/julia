package core

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/oracle"
	"github.com/go-embedded-julia/julia/syntax"
)

// The hand-written cases of testdata/subtype-cases.jl, evaluated from the
// parser's Expr by the small type evaluator below and answered by julia
// once (JLGO_ORACLE=1 JLGO_UPDATE_CORE=1) into subtype-cases.golden.

const (
	casesPath       = "testdata/subtype-cases.jl"
	casesGoldenPath = "testdata/subtype-cases.golden"
)

var errNotAType = errors.New("not a type expression")

func names() map[expr.Symbol]any {
	vector := func() any {
		T := tv("T")
		return &UnionAll{Var: T, Body: mustApply(ArrayType, T, int64(1))}
	}()
	absvector := func() any {
		T := tv("T")
		return &UnionAll{Var: T, Body: mustApply(AbstractArrayType, T, int64(1))}
	}()
	ntuple := func() any {
		N, T := tv("N"), tv("T")
		body := mustApply(AnyTupleType, &Vararg{T: T, N: N})
		return &UnionAll{Var: N, Body: &UnionAll{Var: T, Body: body}}
	}()
	return map[expr.Symbol]any{
		"Any": AnyType, "Int": Int64Type, "Int64": Int64Type, "Int8": Int8Type, "Integer": IntegerType,
		"Signed": SignedType, "Real": RealType, "Number": NumberType, "Float64": Float64Type,
		"Float32": Float32Type, "AbstractFloat": AbstractFloatType, "Bool": BoolType, "Char": CharType,
		"String": StringType, "AbstractString": AbstractStringType, "Nothing": NothingType,
		"Missing": MissingType, "DataType": DataTypeType, "Union": UnionType, "UnionAll": UnionAllType,
		"Type": TypeType, "Tuple": AnyTupleType, "Array": ArrayType, "AbstractArray": AbstractArrayType,
		"DenseArray": DenseArrayType, "Ref": RefType, "Ptr": PtrType, "Pair": PairType, "Val": ValType,
		"Vector": vector, "NTuple": ntuple, "TypeofBottom": TypeofBottomType, "Symbol": SymbolType,
		"AbstractVector": absvector, "Int16": Int16Type, "Int32": Int32Type, "UInt8": UInt8Type,
		"Some": SomeType, "Dict": DictType, "AbstractDict": AbstractDictType, "Function": FunctionType,
	}
}

type typeEval struct {
	names map[expr.Symbol]any
	vars  map[expr.Symbol]*TypeVar
}

func (ev *typeEval) eval(v any) (res any, err error) {
	defer catchError(&err)
	switch x := v.(type) {
	case expr.Symbol:
		if tv, ok := ev.vars[x]; ok {
			return tv, nil
		}
		if t, ok := ev.names[x]; ok {
			return t, nil
		}
		return nil, fmt.Errorf("UndefVarError: %s", x)
	case int64, bool:
		return x, nil
	case expr.QuoteNode:
		if s, ok := x.Value.(expr.Symbol); ok {
			return s, nil
		}
	case *expr.Expr:
		switch x.Head {
		case ".":
			if q, ok := x.Args[1].(expr.QuoteNode); ok {
				return ev.eval(q.Value)
			}
		case "curly":
			return ev.curly(x)
		case "where":
			return ev.where(x.Args[0], x.Args[1:])
		}
	}
	return nil, errNotAType
}

func (ev *typeEval) curly(x *expr.Expr) (any, error) {
	if x.Args[0] == any(expr.Symbol("Vararg")) {
		var T, N any
		var err error
		if len(x.Args) > 1 {
			if T, err = ev.eval(x.Args[1]); err != nil {
				return nil, err
			}
		}
		if len(x.Args) > 2 {
			if N, err = ev.eval(x.Args[2]); err != nil {
				return nil, err
			}
		}
		return WrapVararg(T, N)
	}
	head, err := ev.eval(x.Args[0])
	if err != nil {
		return nil, err
	}
	var params []any
	var fresh []*TypeVar
	for _, a := range x.Args[1:] {
		// Ref{<:X} is Ref{T} where T<:X (and >:X likewise)
		if e, ok := a.(*expr.Expr); ok && (e.Head == "<:" || e.Head == ">:") && len(e.Args) == 1 {
			b, err := ev.eval(e.Args[0])
			if err != nil {
				return nil, err
			}
			v := &TypeVar{Name: fmt.Sprintf("#%d", len(fresh)), LB: Bottom, UB: AnyType}
			if e.Head == "<:" {
				v.UB = b
			} else {
				v.LB = b
			}
			fresh = append(fresh, v)
			params = append(params, v)
			continue
		}
		p, err := ev.eval(a)
		if err != nil {
			return nil, err
		}
		params = append(params, p)
	}
	var t any
	if head == any(UnionType) {
		t = TypeUnion(params...)
	} else if t, err = Apply(head, params...); err != nil {
		return nil, err
	}
	for i := len(fresh) - 1; i >= 0; i-- {
		t = TypeUnionAll(fresh[i], t)
	}
	return t, nil
}

// where evaluates body where vars..., the first variable outermost.
func (ev *typeEval) where(body any, vars []any) (any, error) {
	if len(vars) == 0 {
		return ev.eval(body)
	}
	var name expr.Symbol
	var lb, ub any = Bottom, AnyType
	var err error
	switch v := vars[0].(type) {
	case expr.Symbol:
		name = v
	case *expr.Expr:
		switch {
		case v.Head == "<:" && len(v.Args) == 2:
			name, _ = v.Args[0].(expr.Symbol)
			ub, err = ev.eval(v.Args[1])
		case v.Head == ">:" && len(v.Args) == 2:
			name, _ = v.Args[0].(expr.Symbol)
			lb, err = ev.eval(v.Args[1])
		case v.Head == "comparison" && len(v.Args) == 5:
			name, _ = v.Args[2].(expr.Symbol)
			if lb, err = ev.eval(v.Args[0]); err == nil {
				ub, err = ev.eval(v.Args[4])
			}
		case v.Head == "braces":
			return ev.where(body, append(append([]any(nil), v.Args...), vars[1:]...))
		default:
			return nil, errNotAType
		}
	}
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, errNotAType
	}
	tv, err := NewTypeVar(string(name), lb, ub)
	if err != nil {
		return nil, err
	}
	saved, had := ev.vars[name]
	ev.vars[name] = tv
	inner, err := ev.where(body, vars[1:])
	if had {
		ev.vars[name] = saved
	} else {
		delete(ev.vars, name)
	}
	if err != nil {
		return nil, err
	}
	return TypeUnionAll(tv, inner), nil
}

// caseLines returns the question lines of the case file.
func caseLines(t *testing.T) []string {
	b, err := os.ReadFile(casesPath)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// casePrelude defines, on the julia side, the question forms other than
// A <: B: env(A, B) is jl_subtype_env, answered as the result and the
// values it found, rendered by gs (oracle_test.go).
const casePrelude = `
function env(x, y)
    n = 0; t = y
    while t isa UnionAll; n += 1; t = t.body; end
    e = Vector{Any}(undef, n)
    r = ccall(:jl_subtype_env, Int32, (Any, Any, Ptr{Any}, Int32), x, y, e, n)
    string(r, join((isassigned(e, i) ? " " * gs(e[i]) : " #null" for i in 1:n)))
end
answer(v::Bool) = string(Int(v))
answer(v::String) = v
`

// goCase answers one line as julia's answer(...) prints it: "1" or "0" for
// A <: B, A == B and isa(v, T), the result and values for env(A, B), and
// "E" when the line raises.
func goCase(line string) string {
	top, ok := syntax.ParseAll([]byte(line), "case").(*expr.Expr)
	if !ok || len(top.Args) != 2 {
		return "E"
	}
	q, ok := top.Args[1].(*expr.Expr)
	if !ok {
		return "E"
	}
	ev := &typeEval{names: names(), vars: map[expr.Symbol]*TypeVar{}}
	var op string
	var args []any
	switch {
	case q.Head == "<:" && len(q.Args) == 2:
		op, args = "<:", q.Args
	case q.Head == "call" && len(q.Args) == 3:
		s, _ := q.Args[0].(expr.Symbol)
		op, args = string(s), q.Args[1:]
	default:
		return "E"
	}
	a, err := ev.eval(args[0])
	if err != nil {
		return "E"
	}
	b, err := ev.eval(args[1])
	if err != nil {
		return "E"
	}
	bit := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	switch op {
	case "<:":
		return goAnswer(a, b)
	case "==":
		return bit(TypesEqual(a, b))
	case "isa":
		return bit(Isa(a, b))
	case "env":
		n := 0
		for t := b; ; {
			u, ok := t.(*UnionAll)
			if !ok {
				break
			}
			n++
			t = u.Body
		}
		e := make([]any, n)
		sub, err := SubtypeEnv(a, b, e)
		if err != nil {
			return "E"
		}
		var s strings.Builder
		s.WriteString(bit(sub))
		for _, v := range e {
			s.WriteByte(' ')
			if v == nil {
				s.WriteString("#null")
			} else {
				s.WriteString(Show(v))
			}
		}
		return s.String()
	}
	return "E"
}

func TestSubtypeCasesAgainstJulia(t *testing.T) {
	lines := caseLines(t)
	if os.Getenv("JLGO_ORACLE") != "" && os.Getenv("JLGO_UPDATE_CORE") != "" {
		p, err := oracle.Start()
		if err != nil {
			t.Fatal(err)
		}
		var prog strings.Builder
		prog.WriteString(gshow)
		prog.WriteString(casePrelude)
		for _, l := range lines {
			fmt.Fprintf(&prog, "print(try answer(%s) catch; \"E\" end, '\\n')\n", l)
		}
		prog.WriteString("nothing\n")
		out, err := p.Call("eval", prog.String())
		p.Close()
		if err != nil {
			t.Fatal(err)
		}
		printed, _, _ := strings.Cut(out, "\n=> ")
		ans := strings.Split(strings.TrimRight(printed, "\n"), "\n")
		if len(ans) != len(lines) {
			t.Fatalf("julia answered %d lines for %d cases", len(ans), len(lines))
		}
		var b strings.Builder
		for i, l := range lines {
			fmt.Fprintf(&b, "%s\t%s\n", l, ans[i])
		}
		if err := os.WriteFile(casesGoldenPath, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gb, err := os.ReadFile(casesGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	golden := strings.Split(strings.TrimRight(string(gb), "\n"), "\n")
	if len(golden) != len(lines) {
		t.Fatalf("golden has %d cases, the case file %d: regenerate it", len(golden), len(lines))
	}
	bad := 0
	for i, g := range golden {
		l, want, _ := strings.Cut(g, "\t")
		if l != lines[i] {
			t.Fatalf("case %d changed (%q, golden %q): regenerate it", i, lines[i], l)
		}
		if got := goCase(l); alphaRename(got) != alphaRename(want) {
			bad++
			t.Errorf("%s: go %s, julia %s", l, got, want)
		}
	}
	if bad > 0 {
		t.Errorf("%d / %d cases differ from julia", bad, len(lines))
	}
}
