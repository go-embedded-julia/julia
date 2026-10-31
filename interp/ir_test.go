package interp

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// Hand-built lowered code for the statements and operands lowering does not
// produce from the programs of the differential test: malformed IR, the
// heads of optimised code, and the checks prepare makes once.

func codeInfo(code []any, slots ...string) *expr.CodeInfo {
	names, flags := expr.Vector{}, expr.Vector{}
	for _, s := range slots {
		names = append(names, expr.Symbol(s))
		flags = append(flags, uint8(0))
	}
	return &expr.CodeInfo{Code: expr.Vector(code), SSAValueTypes: int64(len(code)), SlotNames: names, SlotFlags: flags}
}

func gr(mod, name string) expr.GlobalRef {
	return expr.GlobalRef{Mod: expr.Symbol(mod), Name: expr.Symbol(name)}
}

func call(args ...any) *expr.Expr { return expr.New("call", args...) }

func ret(v any) expr.ReturnNode { return expr.ReturnNode{Val: v} }

func assign(slot int, v any) *expr.Expr { return expr.New("=", expr.SlotNumber(slot), v) }

// check compares a result with want: the shown value, "!" and a substring of
// the error, or "" for any value without an error.
func check(t *testing.T, name string, v any, err error, want string) {
	t.Helper()
	if msg, isErr := strings.CutPrefix(want, "!"); isErr {
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: got %s, %v; want an error containing %q", name, Repr(v), err, msg)
		}
		return
	}
	if err != nil {
		t.Errorf("%s: error %v", name, err)
		return
	}
	if want != "" && Repr(v) != want {
		t.Errorf("%s: got %s, want %s", name, Repr(v), want)
	}
}

func runIR(s *Session, c *expr.CodeInfo, toplevel bool) (any, error) {
	code, err := prepare(c, toplevel)
	if err != nil {
		return nil, err
	}
	s.VM.taskWorld = s.VM.World()
	return s.VM.newFrame(code, s.Mod).evalBody(0)
}

func TestIR(t *testing.T) {
	ssa := func(i int) expr.SSAValue { return expr.SSAValue(i) }
	slot := func(i int) expr.SlotNumber { return expr.SlotNumber(i) }
	lnn := expr.LineNumberNode{Line: 1, File: expr.Symbol("none")}
	cases := []struct {
		name     string
		code     []any
		slots    []string
		toplevel bool
		want     string
	}{
		{"invalid SSAValue", []any{ret(ssa(3))}, nil, false, "!access to invalid SSAValue"},
		{"SSAValue 0", []any{ret(ssa(0))}, nil, false, "!access to invalid SSAValue"},
		{"invalid slot", []any{ret(slot(2))}, []string{"a"}, false, "!access to invalid slot number"},
		{"argument", []any{assign(1, int64(7)), ret(expr.Argument(1))}, []string{"a"}, false, "7"},
		{"unassigned slot", []any{ret(slot(1))}, []string{"a"}, false, "!UndefVarError(:a,"},
		{"no return", []any{int64(1)}, nil, false, "!`body` expression must terminate in `return`"},
		{"goto out of range", []any{expr.GotoNode(9)}, nil, false, "!`body` expression must terminate"},
		{"gotoifnot error", []any{expr.GotoIfNot{Cond: ssa(9), Dest: 1}}, nil, false, "!access to invalid SSAValue"},
		{"unreachable", []any{expr.Unreachable{}}, nil, false, "!reached code declared unreachable"},
		{"enter with a scope", []any{expr.EnterNode{Scope: int64(1), HasScope: true}, ret(int64(2))}, nil, false, "2"},
		{"enter scope error", []any{expr.EnterNode{Scope: ssa(9), HasScope: true}, ret(nil)}, nil, false, "!access to invalid SSAValue"},
		{"enter without catch", []any{expr.EnterNode{}, call(gr("Core", "throw"), int64(1)), ret(nil)}, nil, false, "!julia exception: 1"},
		{"enter keeps Go errors", []any{expr.EnterNode{Catch: 3}, expr.New("invoke"), ret(int64(1))}, nil, false, "!not ported"},
		{"nested leave", []any{
			expr.EnterNode{Catch: 5}, expr.EnterNode{Catch: 5}, expr.New("leave", ssa(1), ssa(2)),
			ret(int64(7)), ret(int64(0)),
		}, nil, false, "7"},
		{"leave of a removed enter", []any{nil, expr.New("leave", ssa(1), nil), ret(int64(3))}, nil, false, "3"},
		{"leave of a non-SSA", []any{expr.New("leave", int64(1)), ret(nil)}, nil, false, "!Invalid IR: :leave argument"},
		{"assignment error", []any{assign(1, ssa(9))}, []string{"a"}, false, "!access to invalid SSAValue"},
		{"assignment arity", []any{expr.New("=", slot(1))}, []string{"a"}, false, "!Invalid IR: Assignment LHS not a Slot"},
		{"assignment to a global", []any{expr.New("=", gr("L", "x"), int64(1))}, nil, false, "!Invalid IR: Assignment LHS not a Slot"},
		{"assignment out of range", []any{assign(5, int64(1))}, []string{"a"}, false, "!access to invalid slot number"},
		{"pop_exception arity", []any{expr.New("pop_exception")}, nil, false, "!Invalid IR: :pop_exception takes one argument"},
		{"pop_exception state", []any{expr.New("pop_exception", int64(1))}, nil, false, "!Invalid IR: :pop_exception of a value"},
		{"pop_exception error", []any{expr.New("pop_exception", ssa(9))}, nil, false, "!access to invalid SSAValue"},
		{"newvar", []any{assign(1, int64(1)), expr.NewvarNode{Slot: slot(1)}, ret(slot(1))}, []string{"a"}, false, "!UndefVarError(:a,"},
		{"newvar out of range", []any{expr.NewvarNode{Slot: slot(4)}}, []string{"a"}, false, "!access to invalid slot number"},
		{"statement error", []any{ssa(9), ret(nil)}, nil, false, "!access to invalid SSAValue"},
		{"line number in a method", []any{lnn, ret(ssa(1))}, nil, false, ""},
		{"line number at top level", []any{lnn, ret(nil)}, nil, true, "nothing"},
		{"meta at top level", []any{expr.New("meta", expr.Symbol("x")), ret(nil)}, nil, true, "nothing"},
		{"method of 2 arguments", []any{expr.New("method", gr("L", "f"), int64(1))}, nil, true, "!not ported"},
		{"method in a method body", []any{expr.New("method", int64(1), int64(2), int64(3))}, nil, false, "!unsupported or misplaced expression method"},
		{"globaldecl of a symbol", []any{expr.New("globaldecl", expr.Symbol("x"))}, nil, true, "!not ported"},
		{"globaldecl without arguments", []any{expr.New("globaldecl")}, nil, true, "!not ported"},
		{"globaldecl of type Any", []any{expr.New("globaldecl", gr("L", "g"), gr("Core", "Any")), expr.New("latestworld"), ret(gr("L", "g"))}, nil, true, "!UndefVarError(:g,"},
		{"globaldecl of another type", []any{expr.New("globaldecl", gr("L", "g"), gr("Core", "Int"))}, nil, true, "!a global declared with the type Int64"},
		{"globaldecl value error", []any{expr.New("globaldecl", gr("L", "g"), ssa(9))}, nil, true, "!access to invalid SSAValue"},
		{"globaldecl of an unknown module", []any{expr.New("globaldecl", gr("Nope", "g"))}, nil, true, "!not ported"},
		{"const without a value", []any{expr.New("const", gr("L", "c"))}, nil, true, "!not ported"},
		{"const value error", []any{expr.New("const", gr("L", "c"), ssa(9))}, nil, true, "!access to invalid SSAValue"},
		{"const of an unknown module", []any{expr.New("const", gr("Nope", "c"), int64(1))}, nil, true, "!not ported"},
		{"module form", []any{expr.New("module", true, expr.Symbol("M"), expr.New("block"))}, nil, true, "!not ported"},
		{"export", []any{expr.New("export", expr.Symbol("q")), ret(nil)}, nil, true, "nothing"},
		{"export of a non-symbol", []any{expr.New("export", int64(1))}, nil, true, "!syntax: malformed"},
		{"public", []any{expr.New("public", expr.Symbol("q")), ret(nil)}, nil, true, "nothing"},
		{"call without a function", []any{expr.New("call")}, nil, false, "!Invalid IR: :call without a function"},
		{"call argument error", []any{call(ssa(9))}, nil, false, "!access to invalid SSAValue"},
		{"invoke_modify", []any{expr.New("invoke_modify", nil, gr("Core", "tuple"), int64(1)), ret(ssa(1))}, nil, false, "(1,)"},
		{"invoke_modify without a function", []any{expr.New("invoke_modify", int64(1))}, nil, false, "!Invalid IR: :invoke_modify"},
		{"new without a type", []any{expr.New("new")}, nil, false, "!Invalid IR: :new without a type"},
		{"new argument error", []any{expr.New("new", ssa(9))}, nil, false, "!access to invalid SSAValue"},
		{"new", []any{expr.New("new", gr("Core", "ErrorException"), "m"), ret(ssa(1))}, nil, false, "ErrorException(\"m\")"},
		{"isdefined of a slot", []any{expr.New("isdefined", slot(1)), ret(ssa(1))}, []string{"a"}, false, "false"},
		{"isdefined of an argument", []any{expr.New("isdefined", expr.Argument(3))}, []string{"a"}, false, "!access to invalid slot number"},
		{"isdefined of a global", []any{expr.New("isdefined", gr("L", "x"))}, nil, false, "![Internal Error]: :isdefined on globalref"},
		{"isdefined of a sparam", []any{expr.New("isdefined", expr.New("static_parameter", int64(1)))}, nil, false, "!could not determine static parameter value"},
		{"isdefined malformed", []any{expr.New("isdefined", int64(1))}, nil, false, "!Invalid IR: malformed :isdefined"},
		{"isdefined malformed sparam", []any{expr.New("isdefined", expr.New("static_parameter"))}, nil, false, "!Invalid IR: malformed :isdefined"},
		{"throw_undef_if_not true", []any{expr.New("throw_undef_if_not", expr.Symbol("v"), true), ret(ssa(1))}, nil, false, "nothing"},
		{"throw_undef_if_not false", []any{expr.New("throw_undef_if_not", expr.Symbol("v"), false)}, nil, false, "!UndefVarError(:v,"},
		{"throw_undef_if_not undefref", []any{expr.New("throw_undef_if_not", expr.Symbol("getfield_undefref"), false)}, nil, false, "!UndefRefError()"},
		{"throw_undef_if_not error", []any{expr.New("throw_undef_if_not", expr.Symbol("v"), ssa(9))}, nil, false, "!access to invalid SSAValue"},
		{"throw_undef_if_not malformed", []any{expr.New("throw_undef_if_not", int64(1))}, nil, false, "!Invalid IR: malformed :throw_undef_if_not"},
		{"static_parameter unknown", []any{expr.New("static_parameter", int64(1))}, nil, false, "!could not determine static parameter value"},
		{"static_parameter malformed", []any{expr.New("static_parameter")}, nil, false, "!Invalid IR: malformed :static_parameter"},
		{"copyast", []any{expr.New("copyast", expr.QuoteNode{Value: call(expr.Symbol("f"), int64(1))}), ret(ssa(1))}, nil, false, ""},
		{"copyast error", []any{expr.New("copyast", ssa(9))}, nil, false, "!access to invalid SSAValue"},
		{"copyast arity", []any{expr.New("copyast")}, nil, false, "!unsupported or misplaced expression copyast"},
		{"the_exception outside a catch", []any{expr.New("the_exception"), ret(ssa(1))}, nil, false, "nothing"},
		{"boundscheck", []any{expr.New("boundscheck"), ret(ssa(1))}, nil, false, "true"},
		{"inbounds", []any{expr.New("inbounds", true), ret(ssa(1))}, nil, false, "nothing"},
		{"method declaration", []any{expr.New("method", expr.Symbol("mf")), ret(ssa(1))}, nil, false, "mf"},
		{"method declaration of a non-symbol", []any{expr.New("method", int64(1))}, nil, false, "!method: invalid declaration"},
		{"method declaration in an unknown module", []any{expr.New("method", gr("Nope", "f"))}, nil, false, "!method: invalid declaration"},
		{"foreigncall", []any{expr.New("foreigncall", expr.Symbol("f"))}, nil, false, "!`ccall` requires the compiler"},
		{"cfunction", []any{expr.New("cfunction")}, nil, false, "!`cfunction` requires the compiler"},
		{"invoke", []any{expr.New("invoke")}, nil, false, "!not ported"},
		{"unknown head", []any{expr.New("frobnicate")}, nil, false, "!unsupported or misplaced expression frobnicate"},
		{"global of an unknown module", []any{gr("Nope", "x")}, nil, false, "!not ported"},
		{"bare symbol", []any{expr.Symbol("an_undefined_name")}, nil, false, "!UndefVarError(:an_undefined_name,"},
		{"module value", []any{expr.Module{Path: []expr.Symbol{"Main", "L"}}, ret(ssa(1))}, nil, false, "Main.L"},
		{"unknown module value", []any{expr.Module{Path: []expr.Symbol{"Nope"}}, ret(ssa(1))}, nil, false, ""},
	}
	for _, c := range cases {
		s := New().NewSession()
		v, err := runIR(s, codeInfo(c.code, c.slots...), c.toplevel)
		check(t, c.name, v, err, c.want)
	}
}

// prepare refuses a CodeInfo whose counts do not agree, before any statement
// runs.
func TestPrepareRefuses(t *testing.T) {
	ok := codeInfo([]any{ret(nil)})
	ok.SSAValueTypes = expr.Vector{nil}
	if _, err := prepare(ok, false); err != nil {
		t.Errorf("a vector of SSA types: %v", err)
	}
	bad := codeInfo([]any{ret(nil)})
	bad.SSAValueTypes = "x"
	if _, err := prepare(bad, false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("SSA types of the wrong Go type: %v", err)
	}
	names := codeInfo([]any{ret(nil)}, "a")
	names.SlotNames = expr.Vector{}
	if _, err := prepare(names, false); err == nil || !strings.Contains(err.Error(), "slot names") {
		t.Errorf("slot names and flags of different lengths: %v", err)
	}
	long := codeInfo([]any{ret(nil), ret(nil)})
	long.SSAValueTypes = int64(1)
	if _, err := prepare(long, false); err == nil || !strings.Contains(err.Error(), "statements for") {
		t.Errorf("more statements than SSA values: %v", err)
	}
}

// A :leave that escapes every try block of a thunk is an error of the top
// level, not a value.
func TestLeaveOutsideTry(t *testing.T) {
	s := New().NewSession()
	thunk := expr.New("thunk", codeInfo([]any{
		expr.GotoNode(3), expr.EnterNode{Catch: 0}, expr.New("leave", expr.SSAValue(2)), ret(nil),
	}))
	_, err := s.VM.toplevelEval(s.Mod, thunk)
	check(t, "escaped leave", nil, err, "!Invalid IR: :leave outside a try block")
	if (&leaveSignal{}).Error() == "" {
		t.Error("leaveSignal has no message")
	}
}
