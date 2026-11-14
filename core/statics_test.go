package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
)

// Every exported variable of the package is in the static table, and every
// name of the table is a variable: the source is parsed, so that a variable
// added to boot.go without a line in StaticVars fails here.
func TestStaticVarsComplete(t *testing.T) {
	notObjects := map[string]string{
		"CatchError": "a func, not an object an image names",
	}
	fset := token.NewFileSet()
	declared := map[string]bool{}
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range pkgs["core"].Files {
		if strings.HasSuffix(fset.File(f.Pos()).Name(), "_test.go") {
			continue
		}
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			for _, s := range g.Specs {
				for _, n := range s.(*ast.ValueSpec).Names {
					declared[n.Name] = true
				}
			}
		}
	}
	listed := map[string]bool{}
	for _, s := range StaticVars() {
		if listed[s.Name] {
			t.Errorf("%s is listed twice", s.Name)
		}
		listed[s.Name] = true
		if !declared[s.Name] {
			t.Errorf("%s is listed and is not a variable of the package", s.Name)
		}
		if v := reflect.ValueOf(s.Value); !v.IsValid() || v.Kind() == reflect.Pointer && v.IsNil() {
			t.Errorf("%s is nil", s.Name)
		}
		if _, ok := StaticIndex(s.Value); !ok {
			t.Errorf("%s is not in the table", s.Name)
		}
	}
	for name := range declared {
		if !ast.IsExported(name) {
			continue
		}
		if _, no := notObjects[name]; no {
			continue
		}
		if !listed[name] {
			t.Errorf("the exported variable %s is not in StaticVars", name)
		}
	}
	// typeTypePrimary is the one variable of boot.go's block left unexported
	if !listed["typeTypePrimary"] {
		t.Error("typeTypePrimary is not listed")
	}
}

// The table has each object once, starts with the variables in order, holds
// what they reach (their names, wrappers, type variables), and does not hold
// a type made later.
func TestStaticTable(t *testing.T) {
	objs := StaticObjects()
	seen := map[any]bool{}
	for i, o := range objs {
		if seen[o] {
			t.Fatalf("object %d (%T) is twice in the table", i, o)
		}
		seen[o] = true
		if j, ok := StaticIndex(o); !ok || j != i {
			t.Errorf("object %d has the index %d", i, j)
		}
	}
	if objs[0] != any(AnyType) {
		t.Errorf("the table starts with %T, not AnyType", objs[0])
	}
	for _, o := range []any{AnyType.Name, Int64Type.Name, ArrayType.Body, ArrayType.Var, Bottom, CoreModule, CPU.Type} {
		if _, ok := StaticIndex(o); !ok {
			t.Errorf("%T is reachable from a variable and not in the table", o)
		}
	}
	later, err := NewType("Main", "StaticLater", AnyType, false, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []any{later, later.Name, &TypeVar{Name: "T"}, int64(1)} {
		if _, ok := StaticIndex(o); ok {
			t.Errorf("%T made after init is in the table", o)
		}
	}
	// built twice with nothing between, the table is the same; the table of
	// init is put back after: a rebuild now also holds what tests interned
	initList, initIndex := staticList, staticIndex
	defer func() { staticList, staticIndex = initList, initIndex }()
	buildStatics()
	first := StaticObjects()
	buildStatics()
	second := StaticObjects()
	if len(first) != len(second) {
		t.Fatalf("%d objects, then %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("object %d changed", i)
		}
	}
}

// staticWalk reaches every kind of object a static type refers to, and an
// object it has already added is not entered again.
func TestStaticWalk(t *testing.T) {
	var got []any
	add := func(v any) bool {
		for _, g := range got {
			if g == v {
				return false
			}
		}
		got = append(got, v)
		return true
	}
	v := &TypeVar{Name: "S", LB: Bottom, UB: &Union{A: Int64Type, B: &Vararg{T: Int8Type, N: &Instance{Type: BoolType}}}}
	tup := &Tuple{Type: EmptyTupleType, Elems: []any{v, v}}
	staticWalk(tup, add)
	staticWalk(tup, add)
	want := []any{tup, EmptyTupleType, v}
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Errorf("%T not reached", w)
		}
	}
	n := len(got)
	// nil pointers and values that are no object add nothing
	for _, nilv := range []any{(*DataType)(nil), (*TypeName)(nil), (*UnionAll)(nil), (*TypeVar)(nil), (*Union)(nil),
		(*Vararg)(nil), (*Instance)(nil), (*Tuple)(nil), nil, int64(3)} {
		staticWalk(nilv, add)
	}
	if len(got) != n {
		t.Errorf("%d objects added by nothing", len(got)-n)
	}
}
