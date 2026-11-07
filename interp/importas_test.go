package interp

import (
	"fmt"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// jl_set_module_uuid, as Compiler.jl calls it (Compiler.jl:77-78).
func TestModuleUUID(t *testing.T) {
	vm := New()
	set := vm.shims["jl_set_module_uuid"]
	uuid := vm.newTuple([]any{uint64(0x807dbc54_b67e_4c79), uint64(0x8afb_eafe4df6f2e1)})
	if _, err := set(vm, []any{vm.Main, uuid}); err != nil || vm.Main.uuid != [2]uint64{0x807dbc54_b67e_4c79, 0x8afb_eafe4df6f2e1} {
		t.Errorf("jl_set_module_uuid: %v %x", err, vm.Main.uuid)
	}
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"not a module", []any{int64(1), uuid}, "!TypeError(:ccall, \"\", Module, 1)"},
		{"not a tuple", []any{vm.Main, int64(1)}, "!not ported yet: jl_set_module_uuid of 1"},
		{"one half", []any{vm.Main, vm.newTuple([]any{uint64(1)})}, "!not ported yet: jl_set_module_uuid of (0x0000000000000001,)"},
		{"not UInt64s", []any{vm.Main, vm.newTuple([]any{int64(1), int64(2)})}, "!not ported yet: jl_set_module_uuid of (1, 2)"},
	} {
		v, err := set(vm, c.args)
		check(t, c.name, v, err, c.want)
	}
	// jl_module_uuid reads it back; a module of no package has (0, 0)
	get := vm.shims["jl_module_uuid"]
	v, err := get(vm, []any{vm.Main})
	check(t, "jl_module_uuid", v, err, "(0x807dbc54b67e4c79, 0x8afbeafe4df6f2e1)")
	v, err = get(vm, []any{vm.Core})
	check(t, "jl_module_uuid of Core", v, err, "(0x0000000000000000, 0x0000000000000000)")
	_, err = get(vm, []any{int64(1)})
	check(t, "jl_module_uuid of 1", nil, err, "!TypeError(:ccall")

	// jl_field_index, as fieldindex calls it (runtime_internals.jl:1096-1102)
	vm.Boot()
	fi := vm.shims["jl_field_index"]
	nt, _ := vm.NewSession().Eval("typeof((a=1, b=2))")
	for _, c := range []struct {
		args []any
		want string
	}{
		{[]any{core.DataTypeType, expr.Symbol("types"), int32(1)}, "3"},
		{[]any{core.DataTypeType, expr.Symbol("name"), int32(1)}, "0"},
		{[]any{nt, expr.Symbol("b"), int32(1)}, "1"},
		{[]any{core.DataTypeType, expr.Symbol("nope"), int32(0)}, "-1"},
		{[]any{core.DataTypeType, expr.Symbol("nope"), int32(1)}, "!FieldError(DataType, :nope)"},
		{[]any{int64(1), expr.Symbol("x"), int32(1)}, "!TypeError(:ccall, \"\", DataType, 1)"},
		{[]any{core.DataTypeType, "x", int32(1)}, "!TypeError(:ccall, \"\", Symbol, \"x\")"},
	} {
		v, err := fi(vm, c.args)
		check(t, "jl_field_index", v, err, c.want)
	}

	// jl_base_relative_to: the top module above, Core's own being Core
	rel := vm.shims["jl_base_relative_to"]
	inner := vm.NewModule("BR")
	for _, c := range []struct {
		m    any
		want string
	}{{inner, "Core"}, {vm.Core, "Core"}, {int64(1), "!TypeError(:ccall, \"\", Module, 1)"}} {
		v, err := rel(vm, []any{c.m})
		check(t, "jl_base_relative_to", v, err, c.want)
	}
}

// `as` in import and using, each want julia 1.12.6's (testdata/importas.jl),
// each case in a fresh module holding a module A.
func TestImportAs(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	defA := "module A\nexport x\nx = 1\ny = 2\nmacro m() 3 end\nmodule B; z = 4; end\nend"
	for i, c := range []struct{ src, want string }{
		{"using .A: x as x2; x2", "1"},
		{"using .A: y as y2; y2", "2"},
		{"import .A: y as y3; y3", "2"},
		{"import .A.y as y4; y4", "2"},
		{"import .A as AA; AA.y", "2"},
		{"import .A.B as BB; BB.z", "4"},
		{"using .A: @m as @mm; @mm", "3"},
		{"using .A: @m as mm", `!cannot rename macro \"@m\" to non-macro \"mm\" in \"using\"`},
		{"import .A: x as @xx", `!cannot rename non-macro \"x\" to macro \"@xx\" in \"import\"`},
		{"using .A: y as y5, x as x5; (y5, x5)", "(2, 1)"},
		{"import .A.B.z as zz; zz", "4"},
		{"import .A.@m as mm", `!cannot rename macro \"@m\" to non-macro \"mm\" in \"import\"`},
		{"import Main as MM; MM === Main", "true"},
	} {
		s := vm.sessionFor(vm.NewModule(expr.Symbol(fmt.Sprintf("IA%d", i))))
		if _, err := s.Eval(defA); err != nil {
			t.Fatal(err)
		}
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}

	// what julia's parser refuses, built directly
	s := vm.sessionFor(vm.NewModule("IAx"))
	if _, err := s.Eval(defA); err != nil {
		t.Fatal(err)
	}
	dotA := expr.New(".", expr.Symbol("."), expr.Symbol("A"))
	for _, c := range []struct {
		name string
		e    *expr.Expr
		want string
	}{
		{"using A as B", expr.New("using", expr.New("as", dotA, expr.Symbol("AA"))), `!syntax: malformed \"using\" statement`},
		{"an as without a name", expr.New("import", expr.New("as", dotA)), `!syntax: malformed \"import\" statement`},
		{"an as to a non-symbol", expr.New("import", expr.New("as", dotA, int64(1))), `!syntax: malformed \"import\" statement`},
		{"an as of a non-path", expr.New("import", expr.New("as", expr.Symbol("A"), expr.Symbol("AA"))), `!syntax: malformed \"import\" statement`},
		{"a malformed item", expr.New("using", expr.New(":", dotA, expr.New("as", dotA))), `!syntax: malformed \"using\" statement`},
	} {
		v, err := vm.toplevelEval(s.Mod, c.e)
		check(t, c.name, v, err, c.want)
	}
}
