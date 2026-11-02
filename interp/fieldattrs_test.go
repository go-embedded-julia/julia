package interp

import (
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// const and @atomic fields: _structtype's attributes and how getfield and
// setfield! honour them, each want julia 1.12.6's answer to the same call
// on the same struct. The VM is booted, for ConcurrencyViolationError and
// FieldError.
func TestFieldAttributes(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	sym := func(n string) expr.Symbol { return expr.Symbol(n) }
	sv := func(xs ...any) SVec { return SVec(xs) }
	for _, c := range []struct {
		name    string
		attrs   SVec
		mutable bool
		want    string
	}{
		{"const in an immutable struct", sv(int64(1), sym("const")), false, "!invalid field attribute const for immutable struct"},
		{"atomic in an immutable struct", sv(int64(1), sym("atomic")), false, "!invalid field attribute atomic for immutable struct"},
		{"field past the last", sv(int64(2), sym("const")), true, "!invalid field attribute 2"},
		{"field 0", sv(int64(0), sym("const")), true, "!invalid field attribute 0"},
		{"unknown attribute", sv(int64(1), sym("volatile")), true, "!invalid field attribute volatile"},
		{"unknown attribute, immutable", sv(int64(1), sym("volatile")), false, "!invalid field attribute volatile"},
		{"non-integer field", sv(1.0, sym("const")), true, "!TypeError(:typeassert, \"\", Int64, 1.0)"},
		{"non-symbol attribute", sv(int64(1), "const"), true, "!TypeError(:typeassert, \"\", Symbol, \"const\")"},
		{"odd trailing element", sv(int64(1), sym("const"), int64(1)), true, "Main.L.A9"},
	} {
		v, err := biStructtype(vm, []any{s.Mod, sym("A9"), SVec{}, sv(sym("a")), c.attrs, c.mutable, int64(1)})
		check(t, c.name, v, err, c.want)
	}

	// mutable struct LZ; const parts::Tuple; @atomic str::Union{String,Nothing}; n::Int; LZ(p) = new(p); end
	def := expr.New("struct", true, sym("LZ"), expr.New("block",
		expr.New("const", expr.New("::", sym("parts"), sym("Tuple"))),
		expr.New("atomic", expr.New("::", sym("str"), expr.New("curly", sym("Union"), sym("String"), sym("Nothing")))),
		expr.New("::", sym("n"), sym("Int")),
		expr.New("=", expr.New("call", sym("LZ"), sym("p")), expr.New("call", sym("new"), sym("p")))))
	if _, err := s.evalForm(def); err != nil {
		t.Fatal(err)
	}
	x, err := s.Eval("LZ((1,))")
	if err != nil {
		t.Fatal(err)
	}
	ex, _ := s.Eval(":(a+b)")
	tn := core.Int64Type.Name
	get, set := coreValue(t, vm, "getfield"), coreValue(t, vm, "setfield!")
	for _, c := range []struct {
		f    any
		args []any
		want string
	}{
		{get, []any{x, sym("parts")}, "(1,)"},
		{get, []any{x, sym("str")}, "!UndefRefError()"},
		{get, []any{x, sym("str"), sym("acquire")}, "!UndefRefError()"},
		{get, []any{x, sym("str"), sym("not_atomic")}, "!ConcurrencyViolationError(\"getfield: atomic field cannot be accessed non-atomically\")"},
		{get, []any{x, sym("n"), sym("acquire")}, "!ConcurrencyViolationError(\"getfield: non-atomic field cannot be accessed atomically\")"},
		{get, []any{x, sym("n"), sym("release")}, "!ConcurrencyViolationError(\"invalid atomic ordering\")"},
		{get, []any{x, sym("n"), sym("acquire"), true}, "!ConcurrencyViolationError(\"getfield: non-atomic field cannot be accessed atomically\")"},
		{get, []any{x, sym("n"), sym("acquire"), int64(1)}, "!TypeError(:getfield, \"\", Bool, 1)"},
		{get, []any{x, sym("n"), int64(1)}, "!TypeError(:getfield, \"\", Symbol, 1)"},
		{get, []any{vm.newTuple([]any{int64(1), int64(2)}), int64(1), sym("acquire")}, "!ConcurrencyViolationError(\"getfield: non-atomic field cannot be accessed atomically\")"},
		{get, []any{vm.newTuple([]any{int64(1), int64(2)}), int64(3), sym("acquire")}, "!BoundsError((1, 2), 3)"},
		{get, []any{vm.newTuple([]any{int64(1), int64(2)}), int64(1), sym("not_atomic")}, "1"},
		{get, []any{s.Mod, sym("x"), sym("acquire")}, "!UndefVarError(:x,"},
		{get, []any{vm.Core, sym("Int"), sym("acquire")}, "Int64"},
		{get, []any{tn, sym("name"), sym("acquire")}, "!ConcurrencyViolationError(\"getfield: non-atomic field cannot be accessed atomically\")"},
		{get, []any{tn, sym("cache"), sym("not_atomic")}, "!not ported yet"},
		{set, []any{x, sym("parts"), vm.newTuple([]any{int64(2)})}, "!setfield!: const field .parts of type LZ cannot be changed"},
		{set, []any{x, sym("str"), "a"}, "!ConcurrencyViolationError(\"setfield!: atomic field cannot be written non-atomically\")"},
		{set, []any{x, sym("str"), "a", sym("release")}, "\"a\""},
		{get, []any{x, sym("str"), sym("acquire")}, "\"a\""},
		{set, []any{x, sym("n"), int64(2), sym("release")}, "!ConcurrencyViolationError(\"setfield!: non-atomic field cannot be written atomically\")"},
		{set, []any{x, sym("n"), int64(2), sym("acquire")}, "!ConcurrencyViolationError(\"invalid atomic ordering\")"},
		{set, []any{x, sym("n"), int64(2)}, "2"},
		{set, []any{x, sym("q"), int64(2)}, "!FieldError(Main.L.LZ, :q)"}, // julia's LZ is in Main
		{set, []any{vm.newTuple([]any{int64(1)}), int64(1), int64(2), sym("release")}, "!setfield!: immutable struct of type Tuple cannot be changed"},
		{set, []any{ex, sym("head"), sym("call"), sym("release")}, "!ConcurrencyViolationError(\"setfield!: non-atomic field cannot be written atomically\")"},
		{set, []any{tn, sym("name"), sym("X")}, "!setfield!: const field .name of type TypeName cannot be changed"},
		{set, []any{tn, sym("max_methods"), uint8(1), sym("release")}, "!ConcurrencyViolationError(\"setfield!: non-atomic field cannot be written atomically\")"},
		{set, []any{tn, sym("cache"), SVec{}, sym("not_atomic")}, "!ConcurrencyViolationError(\"setfield!: atomic field cannot be written non-atomically\")"},
		{set, []any{tn, sym("partial"), int64(1)}, "!not ported yet: setfield! of a TypeName's partial"},
		{set, []any{vm.Core, sym("x"), int64(1)}, "!cannot assign variables in other modules"},
	} {
		v, err := vm.apply(c.f, c.args)
		check(t, Repr(c.f)+" "+showValue(c.args[1]), v, err, c.want)
	}
	// redefining the struct with other attributes is another type
	a, _ := biStructtype(vm, []any{s.Mod, sym("E"), SVec{}, sv(sym("a")), sv(int64(1), sym("const")), true, int64(1)})
	b, _ := biStructtype(vm, []any{s.Mod, sym("E"), SVec{}, sv(sym("a")), sv(int64(1), sym("atomic")), true, int64(1)})
	if same, _ := biEquivTypedef(vm, []any{a, b}); same != false {
		t.Error("const and atomic definitions are equivalent")
	}
}

// Imports of bindings, relative ones and conflicting ones: julia 1.12.6
// prints these warnings, with its module names, for the same nest
// (module.c:1261-1304).
func TestImportBindings(t *testing.T) {
	vm := New()
	var stderr strings.Builder
	vm.Stderr = &stderr
	v, err := vm.NewSession().Eval(`module P
f() = 1
const k = 2
module Q
import ..f, ..k
import ..f
import ..nope
import ...f
g() = 3
import ..g
h() = (f(), k)
end
end
getfield(getfield(P, :Q), :h)()`)
	check(t, "the nest", v, err, "(1, 2)")
	want := `WARNING: Imported binding P.nope was undeclared at import time during import to Q.
WARNING: Imported binding L.f was undeclared at import time during import to Q.
WARNING: ignoring conflicting import of L.f into Q
WARNING: Imported binding P.g was undeclared at import time during import to Q.
WARNING: import of P.g into Q conflicts with an existing identifier; ignored.
`
	if stderr.String() != want {
		t.Errorf("warnings:\n%s\njulia:\n%s", stderr.String(), want)
	}
}
