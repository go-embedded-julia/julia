package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Two modules of one name at different paths (Base.Libc.Libdl and the
// stdlib's Libdl are such): a method of the first reads what only the first
// has, after the second is defined. Its keyword sorter's body names the
// module by its name alone, which resolved in the last module registered.
func TestSameNameModules(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	v, err := s.Eval(`module A
module Dup
only_here = 41
end
end
module B
module Dup
end
end
Core.eval(A.Dup, :(later() = only_here))
Core.eval(A.Dup, :(const k = 7))
(A.Dup.later(), A.Dup.k, isdefined(B.Dup, :later), isdefined(B.Dup, :k), A.Dup === B.Dup)`)
	check(t, "definitions of the first module", v, err, "(41, 7, false, false, false)")
	v, err = s.Eval(`(getfield(GlobalRef(A.Dup, :only_here), :mod) === A.Dup,
		getfield(GlobalRef(B.Dup, :only_here), :mod) === B.Dup,
		GlobalRef(A.Dup, :only_here) === GlobalRef(B.Dup, :only_here),
		GlobalRef(A.Dup, :only_here) === GlobalRef(A.Dup, :only_here))`)
	check(t, "GlobalRef.mod is the module", v, err, "(true, true, false, true)")
}

// The compressed IR of a method keeps the module of each GlobalRef, not its
// name: the nearby encoding for the method's own module, the full one for
// another of the same name.
func TestIRGlobalRefSameName(t *testing.T) {
	f := newIRFixture(t)
	vm := f.vm
	first := f.s.Mod
	other := vm.addModule(first.Name, vm.Main) // a second L, as Main.L.L would be
	nested := vm.addModule("Twin", first)
	nested2 := vm.addModule("Twin", other)
	refs := []any{gref(first, "a"), gref(other, "b"), gref(nested, "c"), gref(nested2, "d"), gref(vm.Core, "e")}
	stmts := make([]any, 0, len(refs)+1)
	stmts = append(stmts, refs...)
	stmts = append(stmts, expr.ReturnNode{Val: ssa(1)})
	_, back := f.roundTrip("same-name modules", f.code(stmts, int64(len(stmts))), nil)
	_, _, code, _ := arrayOf(f.vm.ciField(back, "code"))
	for i, want := range refs {
		got, ok := code[i].(expr.GlobalRef)
		w := want.(expr.GlobalRef)
		if !ok || got.Obj != w.Obj || got.Name != w.Name {
			t.Errorf("statement %d: %#v, want the module %v", i, code[i], showValue(w.Obj))
		}
	}
	// the name alone resolves in the method's module, as lowering writes it
	data, err := f.compress(f.code([]any{expr.GlobalRef{Mod: first.Name, Name: "n"}, expr.ReturnNode{Val: ssa(1)}}, int64(2)))
	if err != nil {
		t.Fatal(err)
	}
	back, err = f.uncompress(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, code, _ = arrayOf(f.vm.ciField(back, "code"))
	if g := code[0].(expr.GlobalRef); g.Obj != any(first) {
		t.Errorf("a name alone: %#v", g)
	}
}

func TestModuleReferences(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	a := vm.addModule("A", vm.Main)
	dupA := vm.addModule("Dup", a)
	dupB := vm.addModule("Dup", vm.Main)
	a.declare("Dup", 0).Value = dupA
	vm.Main.declare("A", 0).Value = a
	if vm.modules["Dup"] != nil || vm.modules["A"] != nil {
		t.Error("vm.modules holds a module that is not a root")
	}
	// Base's `e.args[1] === GlobalRef(Core, :tuple)` meets the reference the
	// front end wrote
	if gref(vm.Core, "tuple") != (expr.GlobalRef{Mod: "Core", Name: "tuple"}) || gref(dupA, "x").Obj != any(dupA) {
		t.Error("gref")
	}
	if vm.refModule(nil, expr.GlobalRef{Mod: "Dup"}) != nil {
		t.Error("a name alone found a module outside its context")
	}
	if vm.refModule(dupA, expr.GlobalRef{Mod: "Dup"}) != dupA || vm.refModule(a, expr.GlobalRef{Mod: "A"}) != a ||
		vm.refModule(dupA, expr.GlobalRef{Mod: "A"}) != a || vm.refModule(dupA, expr.GlobalRef{Mod: "Core"}) != vm.Core ||
		vm.refModule(dupA, expr.GlobalRef{Mod: "Dup", Obj: dupB}) != dupB {
		t.Error("refModule")
	}
	if g := vm.bindRef(dupA, expr.GlobalRef{Mod: "Dup", Name: "x"}); g.Obj != any(dupA) {
		t.Errorf("bindRef: %#v", g)
	}
	if g := vm.bindRef(nil, expr.GlobalRef{Mod: "Nope", Name: "x"}); g.Obj != nil {
		t.Errorf("bindRef of nothing: %#v", g)
	}
	// module values: by object, by path
	if vm.constValue(expr.Module{Path: []expr.Symbol{"Dup"}, Obj: dupB}) != any(dupB) ||
		vm.constValue(expr.Module{Path: []expr.Symbol{"Main", "A", "Dup"}}) != any(dupA) ||
		vm.constValue(expr.Module{Path: []expr.Symbol{"Main", "Nope"}}) == any(dupA) {
		t.Error("constValue of a module")
	}
	for _, p := range [][]expr.Symbol{{"Nope"}, {"Nope", "A"}, {"Main", "Nope"}, {"Main", "A", "x"}} {
		if m := vm.modulePath(p); m != nil {
			t.Errorf("modulePath %v: %v", p, showValue(m))
		}
	}
	vm.Main.declare("x", 0).Value = int64(1)
	if vm.modulePath([]expr.Symbol{"Main", "x"}) != nil {
		t.Error("modulePath of a value")
	}
	// the module of a type
	if vm.typeNameModule(core.Int64Type.Name) != vm.Core {
		t.Error("typeNameModule of a Core type")
	}
	// bindRefs reaches every node that holds a reference
	ref := expr.GlobalRef{Mod: "Dup", Name: "x"}
	core1 := expr.GlobalRef{Mod: "Core", Name: "tuple"}
	ci := &expr.CodeInfo{Code: expr.Vector{ref}}
	e := expr.New("call", ref, core1, expr.QuoteNode{Value: ref}, expr.Vector{ref}, ci,
		expr.GotoIfNot{Cond: ref, Dest: 1}, expr.ReturnNode{Val: ref}, expr.NewvarNode{Slot: ref},
		expr.EnterNode{Catch: 1, Scope: ref, HasScope: true}, int64(3))
	vm.bindRefs(dupA, e)
	bound := func(v any) bool { g, ok := v.(expr.GlobalRef); return ok && g.Obj == any(dupA) }
	if !bound(e.Args[0]) || !bound(e.Args[2].(expr.QuoteNode).Value) || !bound(e.Args[3].(expr.Vector)[0]) ||
		!bound(ci.Code[0]) || !bound(e.Args[5].(expr.GotoIfNot).Cond) || !bound(e.Args[6].(expr.ReturnNode).Val) ||
		!bound(e.Args[7].(expr.NewvarNode).Slot) || !bound(e.Args[8].(expr.EnterNode).Scope) {
		t.Errorf("bindRefs: %v", e.Args)
	}
	if g := e.Args[1].(expr.GlobalRef); g.Obj != nil {
		t.Errorf("a root module's name was bound: %#v", g)
	}
}
