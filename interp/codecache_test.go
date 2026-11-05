package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The module compiler options, each want julia 1.12.6's
// (testdata/codecache.jl).
func TestModuleOpts(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	opts := "(ccall(:jl_get_module_optlevel, Int32, (Any,), MO.Inner), ccall(:jl_get_module_compile, Int32, (Any,), MO.Inner), " +
		"ccall(:jl_get_module_infer, Int32, (Any,), MO.Inner), ccall(:jl_get_module_max_methods, Int32, (Any,), MO.Inner))"
	v, err := s.Eval("module MO; module Inner end; end; " + opts)
	check(t, "defaults", v, err, "(-1, -1, -1, -1)")
	v, err = s.Eval("Core.eval(MO, Expr(:meta, :optlevel, 1)); Core.eval(MO, Expr(:meta, :compile, 0)); " +
		"Core.eval(MO, Expr(:meta, :infer, 0)); Core.eval(MO, Expr(:meta, :max_methods, 2)); " + opts)
	check(t, "set", v, err, "(1, 0, 0, 2)")
	mo := vm.modules["MO"]
	if mo.opts.nospecialize != -1 {
		t.Errorf("infer off leaves nospecialize %d", mo.opts.nospecialize)
	}
	// the other metas: specialize, nospecialize, an option without an Int,
	// a meta of three arguments
	for _, c := range []struct {
		args []any
		want int32
	}{
		{[]any{expr.Symbol("specialize")}, 0},
		{[]any{expr.Symbol("nospecialize")}, -1},
		{[]any{expr.Symbol("inline")}, -1},
	} {
		moduleMeta(mo, c.args)
		if mo.opts.nospecialize != c.want {
			t.Errorf("meta %v: nospecialize %d", c.args, mo.opts.nospecialize)
		}
	}
	moduleMeta(mo, []any{expr.Symbol("optlevel"), "3"})
	moduleMeta(mo, []any{expr.Symbol("optlevel"), int64(3), int64(4)})
	if vm.moduleOpt(mo, optOptlevel) != 1 {
		t.Errorf("a meta julia ignores changed the optlevel")
	}
	// Base stops the walk: its submodules inherit nothing above it
	inner, base := vm.modules["Inner"], vm.baseModule
	vm.baseModule = inner
	if vm.moduleOpt(inner, optMaxMethods) != -1 {
		t.Error("Base inherits its parent's options")
	}
	vm.baseModule = base
	for _, c := range []struct {
		f    string
		args []any
		want string
	}{
		{"jl_set_module_optlevel", []any{mo, int32(2)}, "nothing"},
		{"jl_get_module_optlevel", []any{mo}, "2"},
		{"jl_set_module_infer", []any{mo, int32(1)}, "nothing"},
		{"jl_set_module_nospecialize", []any{mo, int32(0)}, "nothing"},
		{"jl_get_module_infer", []any{int64(1)}, "!TypeError(:ccall"},
		{"jl_set_module_compile", []any{mo, int64(1)}, "!not ported yet: jl_set_module_compile"},
		{"jl_set_module_nospecialize", []any{int64(1), int32(1)}, "!not ported yet: jl_set_module_nospecialize"},
		{"jl_is_assertsbuild", nil, "0"},
	} {
		v, err := vm.shims[expr.Symbol(c.f)](vm, c.args)
		check(t, c.f, v, err, c.want)
	}
	if mo.opts.nospecialize != 0 || vm.moduleOpt(mo, optInfer) != 1 {
		t.Errorf("after the setters: %+v", mo.opts)
	}
}

// The code cache: Core.CodeInstance, jl_mi_cache_insert, jl_rettype_inferred,
// a lease, jl_fill_codeinst and jl_update_codeinst; each want julia's
// (testdata/codecache.jl).
func TestCodeCache(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	tt, err := s.Eval("f(x) = x + 1; Tuple{typeof(f), Int}")
	if err != nil {
		t.Fatal(err)
	}
	null := &Pointer{Type: vm.ptrType(core.UInt64Type)}
	match, err := vm.shims["jl_gf_invoke_lookup_worlds"](vm, []any{tt, nil, vm.World(), null, null})
	if err != nil {
		t.Fatal(err)
	}
	meth := match.(*Struct).Fields[2]
	miv, _ := vm.specializationsGetLinfo([]any{meth, tt, SVec{}})
	mi := miv.(*Struct)
	w := vm.World()
	sh := func(name string, args ...any) (any, error) { return vm.shims[expr.Symbol(name)](vm, args) }
	v, err := sh("jl_rettype_inferred", nil, mi, w, w)
	check(t, "empty", v, err, "nothing")
	civ, err := sh("jl_new_codeinst", mi, expr.Symbol("me"), core.Int64Type, core.Bottom, int64(2), nil, int32(2),
		uint64(5), ^uint64(0), uint32(7), nil, nil, SVec{})
	if err != nil {
		t.Fatal(err)
	}
	ci := civ.(*Struct)
	if _, err := sh("jl_mi_cache_insert", mi, ci); err != nil {
		t.Fatal(err)
	}
	v, err = sh("jl_rettype_inferred", nil, mi, w, w)
	check(t, "owner nothing", v, err, "nothing")
	if v, err := sh("jl_rettype_inferred", expr.Symbol("me"), mi, w, w); v != any(ci) || err != nil {
		t.Errorf("owner me: %v %v", v, err)
	}
	v, err = sh("jl_rettype_inferred", expr.Symbol("me"), mi, uint64(4), w)
	check(t, "too old", v, err, "nothing")
	fields := func(c *Struct, names ...expr.Symbol) string {
		var out []any
		for _, n := range names {
			if n[0] == '?' {
				d, _ := biIsdefined(vm, []any{c, n[1:]})
				out = append(out, d)
				continue
			}
			f, err := biGetfield(vm, []any{c, n})
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, f)
		}
		return Repr(vm.newTuple(out))
	}
	if got := fields(ci, "min_world", "max_world", "rettype", "exctype", "rettype_const", "ipo_purity_bits", "?next", "?inferred"); got !=
		"(0x0000000000000005, 0xffffffffffffffff, Int64, Union{}, 2, 0x00000007, false, true)" {
		t.Errorf("fields: %s", got)
	}
	// a second CodeInstance goes first; the first is still found after it
	ci2, _ := sh("jl_new_codeinst", mi, expr.Symbol("you"), core.Int64Type, core.Bottom, nil, nil, int32(0),
		uint64(1), ^uint64(0), uint32(0), nil, nil, SVec{})
	sh("jl_mi_cache_insert", mi, ci2)
	if v, _ := sh("jl_rettype_inferred", expr.Symbol("me"), mi, w, w); v != any(ci) || ci2.(*Struct).Fields[ciNext] != any(ci) {
		t.Errorf("behind a newer one: %v", v)
	}
	// a constant return: the invoke pointer
	ci3, err := sh("jl_new_codeinst", mi, nil, core.Int64Type, core.Bottom, int64(2), nil, int32(3),
		uint64(1), ^uint64(0), uint32(0), nil, nil, SVec{})
	if err != nil || ci3.(*Struct).Fields[ciInvoke] != fptrConstReturn {
		t.Errorf("a constant return: %v", err)
	}

	r, err := sh("jl_engine_reserve", mi, expr.Symbol("other"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fields(r.(*Struct), "min_world", "max_world", "?inferred", "?rettype"); got != "(0x0000000000000001, 0x0000000000000000, false, false)" {
		t.Errorf("reserved: %s", got)
	}
	// the lease is held: reserving again is the cycle, a fresh placeholder
	r2, _ := sh("jl_engine_reserve", mi, expr.Symbol("other"))
	if r2 == r || len(vm.reservations) != 1 {
		t.Errorf("a second reservation: %d leases", len(vm.reservations))
	}
	if _, err := sh("jl_fill_codeinst", r, core.Int64Type, core.Bottom, nil, int32(0), uint64(3), ^uint64(0),
		uint32(1), nil, nil, SVec{}); err != nil {
		t.Fatal(err)
	}
	if got := fields(r.(*Struct), "min_world", "rettype", "inferred", "?rettype_const"); got != "(0x0000000000000003, Int64, nothing, false)" {
		t.Errorf("filled: %s", got)
	}
	sh("jl_engine_fulfill", r2) // not the lease's: nothing ends
	if len(vm.reservations) != 1 {
		t.Error("fulfilling another CodeInstance ended the lease")
	}
	sh("jl_engine_fulfill", r)
	if len(vm.reservations) != 0 {
		t.Error("the lease did not end")
	}
	// jl_fill_codeinst of a constant, and a filled one again
	r3, _ := sh("jl_engine_reserve", mi, nil)
	_, err = sh("jl_fill_codeinst", r3, core.Int64Type, core.Bottom, int64(2), int32(3), uint64(3), ^uint64(0),
		uint32(1), nil, &Struct{Type: vm.Core.own("DebugInfo").Value.(*core.DataType)}, SVec{})
	if err != nil || r3.(*Struct).Fields[ciRettypeConst] != int64(2) || r3.(*Struct).Fields[ciInvoke] != fptrConstReturn {
		t.Errorf("filled with a constant: %v", err)
	}
	_, err = sh("jl_fill_codeinst", r3, core.Int64Type, core.Bottom, nil, int32(0), uint64(3), ^uint64(0),
		uint32(1), nil, nil, SVec{})
	check(t, "filled twice", nil, err, "!not ported yet: jl_fill_codeinst")
	// jl_update_codeinst: the inferred code, the times as Float16 bits
	_, err = sh("jl_update_codeinst", ci, "src", int32(1), uint64(2), uint64(9), uint32(3), nil,
		float64(1), float64(0.5), float64(2), nil, SVec{})
	if err != nil {
		t.Fatal(err)
	}
	if got := fields(ci, "inferred", "min_world", "max_world", "time_infer_total", "time_infer_cache_saved", "time_infer_self", "ipo_purity_bits"); got !=
		"(\"src\", 0x0000000000000002, 0x0000000000000009, 0x3c00, 0x3800, 0x4000, 0x00000003)" {
		t.Errorf("updated: %s", got)
	}
	if ci.Fields[ciInvoke] != fptrConstReturn {
		t.Error("update of a constant return: no invoke pointer")
	}

	for _, c := range []struct {
		f    string
		args []any
		want string
	}{
		{"jl_new_codeinst", []any{int64(1), nil, nil, nil, nil, nil, int32(0), uint64(1), uint64(1), uint32(0), nil, nil, SVec{}}, "!TypeError(:ccall"},
		{"jl_new_codeinst", []any{mi, nil, nil, nil, nil, nil, int32(0), uint64(2), uint64(1), uint32(0), nil, nil, SVec{}}, "!not ported yet: jl_new_codeinst"},
		{"jl_new_codeinst", []any{mi, nil, nil, nil, nil, nil, int32(1), uint64(1), uint64(1), uint32(0), nil, nil, SVec{}}, "!without its constant"},
		{"jl_mi_cache_insert", []any{int64(1), ci}, "!TypeError(:ccall"},
		{"jl_mi_cache_insert", []any{mi, int64(1)}, "!TypeError(:ccall"},
		{"jl_rettype_inferred", []any{nil, int64(1), w, w}, "!TypeError(:ccall"},
		{"jl_rettype_inferred", []any{nil, mi, int64(1), w}, "!not ported yet: jl_rettype_inferred"},
		{"jl_engine_reserve", []any{int64(1), nil}, "!TypeError(:ccall"},
		{"jl_engine_fulfill", []any{int64(1)}, "!TypeError(:ccall"},
		{"jl_fill_codeinst", []any{int64(1)}, "!TypeError(:ccall"},
		{"jl_fill_codeinst", []any{r3, nil, nil, nil, int32(1), uint64(1), uint64(1), uint32(0), nil, nil, SVec{}}, "!not ported yet: jl_fill_codeinst"},
		{"jl_update_codeinst", []any{int64(1)}, "!TypeError(:ccall"},
		{"jl_update_codeinst", []any{ci, nil, int32(0), uint64(1), uint64(1), uint32(0), nil, float64(0), int64(0), float64(0), nil, SVec{}}, "!not ported yet: jl_update_codeinst"},
		{"jl_update_codeinst", []any{ci2, nil, int32(1), uint64(1), uint64(1), uint32(0), nil, float64(0), float64(0), float64(0), nil, SVec{}}, "!not ported yet: jl_update_codeinst"},
		{"jl_push_newly_inferred", []any{ci}, "nothing"},
	} {
		v, err := vm.shims[expr.Symbol(c.f)](vm, c.args)
		check(t, c.f, v, err, c.want)
	}
	// an ABIOverride's MethodInstance is a CodeInstance's def too
	abi, err := s.Eval("Core.ABIOverride")
	if err != nil {
		t.Fatal(err)
	}
	ov := &Struct{Type: abi.(*core.DataType), Fields: []any{tt, mi}}
	if _, err := sh("jl_new_codeinst", ov, nil, nil, nil, nil, nil, int32(0), uint64(1), uint64(1), uint32(0), nil, nil, SVec{}); err != nil {
		t.Errorf("an ABIOverride: %v", err)
	}
}

// A method's source as julia's CodeInfo, its copy, and the scan of its
// GlobalRefs; julia's for f(x) = x + 1 (testdata/codecache.jl).
func TestMethodSource(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	tt, err := s.Eval("f(x) = x + 1; Tuple{typeof(f), Int}")
	if err != nil {
		t.Fatal(err)
	}
	null := &Pointer{Type: vm.ptrType(core.UInt64Type)}
	match, _ := vm.shims["jl_gf_invoke_lookup_worlds"](vm, []any{tt, nil, vm.World(), null, null})
	meth := match.(*Struct).Fields[2].(*Struct)
	src := meth.Fields[12].(*Struct)
	cp, err := vm.copyCodeInfo([]any{src})
	if err != nil || cp == any(src) {
		t.Fatalf("copy: %v", err)
	}
	var got []any
	for _, n := range []expr.Symbol{"slotnames", "slotflags", "ssaflags", "ssavaluetypes", "nargs", "isva", "edges", "min_world", "inlining_cost"} {
		f, err := biGetfield(vm, []any{cp, n})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, f)
	}
	if r := Repr(vm.newTuple(got)); r != "([Symbol(\"#self#\"), :x], UInt8[0x00, 0x08], UInt32[0x00000000, 0x00000000, 0x00000000], 3, 0x0000000000000002, false, svec(), 0x0000000000000001, 0xffff)" {
		t.Errorf("source: %s", r)
	}
	if code, _ := biGetfield(vm, []any{cp, expr.Symbol("code")}); vm.typeOf(code) != any(vecAnyType) {
		t.Errorf("code: %s", Repr(code))
	}
	// a lowered CodeInfo copies to julia's; anything else is refused
	low := codeInfo([]any{expr.ReturnNode{Val: int64(1)}})
	if c, err := vm.copyCodeInfo([]any{low}); err != nil || c.(*Struct).Type != vm.tCodeInfo {
		t.Errorf("a lowered CodeInfo: %v", err)
	}
	_, err = vm.copyCodeInfo([]any{int64(1)})
	check(t, "copy of an Int", nil, err, "!TypeError(:ccall")
	_, err = vm.copyCodeInfo([]any{meth})
	check(t, "copy of a Method", nil, err, "!TypeError(:ccall")

	// the scan: once, 0x2 for a GlobalRef into the method's own module, as
	// f's `+` is (Main.:+ in julia's lowered code)
	scan := vm.shims["jl_scan_method_source_now"]
	for range 2 {
		if _, err := scan(vm, []any{meth, src}); err != nil {
			t.Fatal(err)
		}
	}
	if f := meth.Fields[methodDidScanSource]; f != uint8(3) {
		t.Errorf("did_scan_source of f: %v", f)
	}
	g, _ := s.Eval("h() = 1; Tuple{typeof(h)}")
	hm, _ := vm.shims["jl_gf_invoke_lookup_worlds"](vm, []any{g, nil, vm.World(), null, null})
	hmeth := hm.(*Struct).Fields[2].(*Struct)
	if _, err := scan(vm, []any{hmeth, hmeth.Fields[12]}); err != nil || hmeth.Fields[methodDidScanSource] != uint8(1) {
		t.Errorf("did_scan_source of h: %v", hmeth.Fields[methodDidScanSource])
	}
	_, err = scan(vm, []any{int64(1), src})
	check(t, "scan of an Int", nil, err, "!TypeError(:ccall")
	_, err = scan(vm, []any{meth, "src"})
	check(t, "scan of a compressed source", nil, err, "!not ported yet: jl_scan_method_source_now")

	// the IR nodes' fields
	for _, c := range []struct {
		v    any
		f    any
		want string
	}{
		{expr.SSAValue(2), expr.Symbol("id"), "2"},
		{expr.SlotNumber(3), int64(1), "3"},
		{expr.Argument(1), expr.Symbol("n"), "1"},
		{expr.GotoNode(4), expr.Symbol("label"), "4"},
		{expr.GotoIfNot{Cond: true, Dest: 5}, expr.Symbol("dest"), "5"},
		{expr.ReturnNode{Val: int64(6)}, expr.Symbol("val"), "6"},
		{expr.Unreachable{}, expr.Symbol("val"), "!UndefRefError()"},
		{expr.NewvarNode{Slot: expr.SlotNumber(1)}, expr.Symbol("slot"), Repr(expr.SlotNumber(1))},
		{expr.EnterNode{Catch: 7}, expr.Symbol("catch_dest"), "7"},
		{expr.EnterNode{Catch: 7}, expr.Symbol("scope"), "!UndefRefError()"},
		{expr.EnterNode{Catch: 7, Scope: int64(1), HasScope: true}, expr.Symbol("scope"), "1"},
		{expr.GotoNode(4), expr.Symbol("nope"), "!FieldError(GotoNode, :nope)"},
	} {
		v, err := biGetfield(vm, []any{c.v, c.f})
		check(t, Repr(c.v), v, err, c.want)
	}
	for _, c := range []struct {
		v    any
		want bool
	}{{expr.Unreachable{}, false}, {expr.ReturnNode{Val: nil}, true}} {
		if d, _ := biIsdefined(vm, []any{c.v, expr.Symbol("val")}); d != c.want {
			t.Errorf("isdefined(%s, :val) = %v", Repr(c.v), d)
		}
	}
}

// A value that contains itself shows a circular reference, julia's
// (testdata/circular.jl); before, showing one overflowed Go's stack.
func TestShowCircular(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	for _, c := range []struct{ src, want string }{
		{"mutable struct Node\nnext\nend; a = Node(nothing); a.next = a; a", "Main.L.Node(Main.L.Node(#= circular reference @-1 =#))"},
		{"b = Node(Node(nothing)); b.next.next = b; b", "Main.L.Node(Main.L.Node(Main.L.Node(#= circular reference @-2 =#)))"},
		{"struct Wrap\nx\nend; c = Node(nothing); c.next = Wrap(c); c", "Main.L.Node(Main.L.Wrap(Main.L.Node(#= circular reference @-2 =#)))"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
	v := vm.newVector(core.AnyType, []any{int64(1), nil})
	v.Fields[0].(*MemoryRef).Mem.Elems[1] = v
	if got := Repr(v); got != "Any[1, Any[#= circular reference @-1 =#]]" {
		t.Errorf("a vector: %s", got)
	}
	n, err := s.Eval("Node(nothing)")
	if err != nil {
		t.Fatal(err)
	}
	n.(*Struct).Fields[0] = vm.newVector(core.AnyType, []any{n})
	if got := Repr(n); got != "Main.L.Node(Any[Main.L.Node(#= circular reference @-2 =#)])" {
		t.Errorf("a struct in a vector: %s", got)
	}
	for sym, want := range map[expr.Symbol]string{"x": ":x", "+": ":+", "==": ":(==)", "#self#": "Symbol(\"#self#\")", "'": "Symbol(\"'\")"} {
		if got := Repr(sym); got != want {
			t.Errorf("%s shows %s, want %s", sym, got, want)
		}
	}
}

// Bindings as inference reads them: each name's partition kind, whether it
// lasts, and its restriction, julia's (testdata/codecache.jl).
func TestBindingPartitions(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval("module B\nconst c = 1\ng = 2\nglobal d\nglobal t::Int = 3\nusing Core: tuple\nimport Core: svec\nexport e\nend"); err != nil {
		t.Fatal(err)
	}
	bm := vm.modules["B"]
	get := vm.shims["jl_get_module_binding"]
	part := vm.shims["jl_get_binding_partition"]
	kind := vm.shims["jl_bpart_get_kind"]
	restr := vm.shims["jl_bpart_get_restriction_value"]
	for _, c := range []struct {
		name expr.Symbol
		want string
	}{
		{"c", "(0x00, true, 1)"},
		{"g", "(0x02, true, Any)"},
		{"d", "(0x08, true, nothing)"},
		{"t", "(0x02, true, Int64)"},
		{"tuple", "(0x05, true, nothing)"},
		{"svec", "(0x06, true, nothing)"},
		{"Int", "(0x04, true, Int64)"},
		{"nope", "(0x09, true, nothing)"},
	} {
		b, err := get(vm, []any{bm, c.name, int32(1)})
		if err != nil {
			t.Fatal(err)
		}
		p, err := part(vm, []any{b, vm.World()})
		if err != nil {
			t.Fatal(err)
		}
		k, _ := kind(vm, []any{p})
		var r any
		switch k {
		case uint8(bkConst), uint8(bkGlobal), uint8(bkImplicitConst):
			r, _ = restr(vm, []any{p})
		}
		maxw, _ := biGetfield(vm, []any{p, expr.Symbol("max_world")})
		if got := Repr(vm.newTuple([]any{k, maxw == ^uint64(0), r})); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// before its world a binding is a guard, the newer partition's next
	b := bm.own("c")
	if p := vm.bindingPartition(b, b.World-1); p.Fields[4] != uint64(bkGuard) || p.Fields[2] != b.World-1 {
		t.Errorf("before: %s", Repr(p))
	}
	newer := vm.bindingPartition(b, b.World)
	if next := newer.Fields[3].(*Struct); next.Fields[4] != uint64(bkGuard) {
		t.Errorf("next: %s", Repr(next))
	}
	hinted, err := vm.shims["jl_get_binding_partition_with_hint"](vm, []any{b, newer, b.World - 1})
	if err != nil || hinted.(*Struct).Fields[4] != uint64(bkGuard) {
		t.Errorf("with a hint: %v", err)
	}
	// an exported, undefined const; an implicit global; a global made through
	// an implicit binding
	if _, err := s.Eval("module C\nexport k, gl\nconst k = 1\nglobal gl = 2\nend\nmodule D\nusing ..C\nend"); err != nil {
		t.Fatal(err)
	}
	cb := vm.modules["C"].own("k")
	if _, k := vm.bindingKind(cb); k != bkConst {
		t.Errorf("k: %d", k)
	}
	if vm.bindingPartition(cb, vm.World()).Fields[4] != uint64(bkConst|bkFlagExported) {
		t.Error("an exported binding has no exported flag")
	}
	gd, _ := get(vm, []any{vm.modules["D"], expr.Symbol("gl"), int32(1)})
	if r, k := vm.bindingKind(gd.(*Binding)); k != bkImplicitGlobal || r != any(vm.modules["C"].own("gl")) {
		t.Errorf("an implicit global: %d %v", k, r)
	}
	cb.Value = undef
	if _, k := vm.bindingKind(cb); k != bkUndefConst {
		t.Errorf("an undefined const: %d", k)
	}
	// Binding's fields
	bb := bm.own("g")
	for _, c := range []struct {
		f    any
		want string
	}{
		{expr.Symbol("value"), "2"},
		{expr.Symbol("flags"), "0x00"},
		{expr.Symbol("backedges"), "!UndefRefError()"},
		{expr.Symbol("nope"), "!FieldError(Binding, :nope)"},
	} {
		v, err := biGetfield(vm, []any{bb, c.f})
		check(t, "Binding."+Repr(c.f), v, err, c.want)
	}
	if gref, _ := biGetfield(vm, []any{bb, expr.Symbol("globalref")}); gref != any(expr.GlobalRef{Mod: "B", Name: "g"}) {
		t.Errorf("globalref: %s", Repr(gref))
	}
	if p, err := biGetfield(vm, []any{bb, expr.Symbol("partitions")}); err != nil || p.(*Struct).Type != vm.tBindingPartition {
		t.Errorf("partitions: %v", err)
	}
	v, err := biGetfield(vm, []any{bm.own("d"), expr.Symbol("value")})
	check(t, "an unassigned value", v, err, "!UndefRefError()")
	for _, c := range []struct {
		f    string
		want bool
	}{{"value", true}, {"backedges", false}} {
		if d, _ := biIsdefined(vm, []any{bb, expr.Symbol(c.f)}); d != c.want {
			t.Errorf("isdefined(binding, :%s) = %v", c.f, d)
		}
	}
	if !vm.isa(bb, vm.tBinding) {
		t.Error("a Binding is no Core.Binding")
	}
	// refusals
	guard := vm.bindingPartition(bm.own("nope"), vm.World())
	for _, c := range []struct {
		f    string
		args []any
		want string
	}{
		{"jl_get_module_binding", []any{bm, expr.Symbol("other"), int32(0)}, "!not ported yet: jl_get_module_binding's NULL"},
		{"jl_get_module_binding", []any{int64(1), expr.Symbol("x"), int32(0)}, "!not ported yet: jl_get_module_binding of"},
		{"jl_get_binding_partition", []any{int64(1), vm.World()}, "!TypeError(:ccall"},
		{"jl_get_binding_partition", []any{bb, int64(1)}, "!not ported yet: jl_get_binding_partition in the world 1"},
		{"jl_get_binding_partition_with_hint", []any{bb, int64(1), vm.World()}, "!TypeError(:ccall"},
		{"jl_bpart_get_kind", []any{int64(1)}, "!TypeError(:ccall"},
		{"jl_bpart_get_restriction_value", []any{int64(1)}, "!TypeError(:ccall"},
		{"jl_bpart_get_restriction_value", []any{guard}, "!UndefRefError()"},
	} {
		v, err := vm.shims[expr.Symbol(c.f)](vm, c.args)
		check(t, c.f, v, err, c.want)
	}
	// jl_get_fieldtypes
	for _, c := range []struct {
		args []any
		want string
	}{
		// julia prints Core.BindingPartition: Core's names show bare here
		{[]any{vm.tBinding}, "svec(Any, Any, BindingPartition, Any, UInt8)"},
		{[]any{core.IntegerType}, "svec()"},
		{[]any{int64(1)}, "!TypeError(:ccall"},
		// julia 1.12.6, of Core._structtype(Main, :Inc, svec(), svec(),
		// svec(), false, 0)
		{[]any{core.NewIncompleteType("Main", "Inc", nil, false, false, nil)}, `!ErrorException("cannot determine field types of incomplete type Inc")`},
		{[]any{core.SymbolType}, "svec()"},
		// julia 1.12.6, of unwrap_unionall(NamedTuple)
		{[]any{core.UnwrapUnionAll(core.NamedTupleType)}, "svec()"},
	} {
		v, err := vm.shims["jl_get_fieldtypes"](vm, c.args)
		check(t, "jl_get_fieldtypes", v, err, c.want)
	}
	// isdefined of a type's fields is what getfield reads (julia 1.12.6:
	// isdefined(Inc, :types), isdefined(Int, :instance) false,
	// isdefined(Nothing, :instance) true)
	for _, c := range []struct {
		t     any
		field expr.Symbol
		want  bool
	}{
		{core.NewIncompleteType("Main", "Inc", nil, false, false, nil), "types", false},
		{core.Int64Type, "instance", false},
		{core.NothingType, "instance", true},
		{core.Int64Type.Name, "name", true},
	} {
		v, err := biIsdefined(vm, []any{c.t, c.field})
		if err != nil || v != c.want {
			t.Errorf("isdefined(%s, :%s) = %v %v, want %v", showValue(c.t), c.field, v, err, c.want)
		}
	}
}
