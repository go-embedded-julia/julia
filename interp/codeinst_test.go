package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// codeinstFixture is a method, its MethodInstance and a CodeInstance of it.
type codeinstFixture struct {
	t    *testing.T
	vm   *VM
	s    *Session
	tt   any
	meth *Struct
	mi   *Struct
	ci   *Struct
}

func newCodeinstFixture(t *testing.T) *codeinstFixture {
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
	f := &codeinstFixture{t: t, vm: vm, s: s, tt: tt, meth: match.(*Struct).Fields[2].(*Struct)}
	miv, err := vm.specializationsGetLinfo([]any{f.meth, tt, SVec{}})
	if err != nil {
		t.Fatal(err)
	}
	f.mi = miv.(*Struct)
	f.ci = f.newCI(vm.World(), nil)
	return f
}

// newCI is a CodeInstance valid up to max, with these edges.
func (f *codeinstFixture) newCI(max uint64, edges SVec) *Struct {
	ci, err := f.vm.shims["jl_new_codeinst"](f.vm, []any{f.mi, nil, core.Int64Type, core.Bottom, nil, nil, int32(0),
		uint64(1), max, uint32(0), nil, nil, edges})
	if err != nil {
		f.t.Fatal(err)
	}
	return ci.(*Struct)
}

func (f *codeinstFixture) sh(name string, args ...any) (any, error) {
	return f.vm.shims[expr.Symbol(name)](f.vm, args)
}

// jl_invoke_api: which calling convention a CodeInstance's invoke pointer is.
func TestInvokeAPI(t *testing.T) {
	f := newCodeinstFixture(t)
	for _, c := range []struct {
		invoke any
		want   string
	}{
		{nil, "0"}, {undef, "0"},
		{cfunc("jl_fptr_args"), "1"}, {fptrConstReturn, "2"}, {cfunc("jl_fptr_sparam"), "3"},
		{cfunc("jl_fptr_interpret_call"), "4"},
		{&Pointer{Type: f.vm.ptrType(core.NothingType)}, "0"},
		{&Pointer{Type: f.vm.ptrType(core.NothingType), Off: 8}, "-1"},
		{&Pointer{Type: f.vm.ptrType(core.NothingType), Target: f.mi}, "-1"},
		{cfunc("jl_fptr_other"), "-1"}, {int64(1), "-1"},
	} {
		f.ci.Fields[ciInvoke] = c.invoke
		v, err := f.sh("jl_invoke_api", f.ci)
		check(t, showValue(c.invoke), v, err, c.want)
	}
	_, err := f.sh("jl_invoke_api", int64(1))
	check(t, "jl_invoke_api of an Int", nil, err, "!TypeError(:ccall")
}

// jl_promote_ci_to_current: valid in every later world, with the
// CodeInstances among the edges.
func TestPromoteCIToCurrent(t *testing.T) {
	f := newCodeinstFixture(t)
	w := f.vm.World()
	inner := f.newCI(w, nil)
	other := f.newCI(w-1, nil)
	outer := f.newCI(w, SVec{inner, other, int64(1), expr.Symbol("x")})
	notyet := f.newCI(w, nil)
	// another world: nothing changes
	if _, err := f.sh("jl_promote_ci_to_current", notyet, w+1); err != nil || notyet.Fields[ciMaxWorld] != any(w) {
		t.Errorf("another world: %v %v", err, notyet.Fields[ciMaxWorld])
	}
	if _, err := f.sh("jl_promote_ci_to_current", outer, w); err != nil {
		t.Fatal(err)
	}
	if outer.Fields[ciMaxWorld] != any(^uint64(0)) || inner.Fields[ciMaxWorld] != any(^uint64(0)) {
		t.Errorf("promoted: %v %v", outer.Fields[ciMaxWorld], inner.Fields[ciMaxWorld])
	}
	if other.Fields[ciMaxWorld] != any(w-1) {
		t.Errorf("an edge valid in no world to come: %v", other.Fields[ciMaxWorld])
	}
	// one already promoted is left alone
	if _, err := f.sh("jl_promote_ci_to_current", outer, w); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []any
		want string
	}{
		{[]any{int64(1), w}, "!TypeError(:ccall"},
		{[]any{f.ci, int64(1)}, "!TypeError(:ccall"},
	} {
		v, err := f.sh("jl_promote_ci_to_current", c.args...)
		check(t, "jl_promote_ci_to_current", v, err, c.want)
	}
}

// jl_method_instance_add_backedge: the caller on the callee's backedges,
// after the invoke signature when there is one.
func TestMethodInstanceAddBackedge(t *testing.T) {
	f := newCodeinstFixture(t)
	f.mi.Fields[3] = undef
	if _, err := f.sh("jl_method_instance_add_backedge", f.mi, nil, f.ci); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sh("jl_method_instance_add_backedge", f.mi, core.Int64Type, f.ci); err != nil {
		t.Fatal(err)
	}
	if _, _, got, _ := arrayOf(f.mi.Fields[3]); len(got) != 3 || got[0] != any(f.ci) || got[1] != any(core.Int64Type) || got[2] != any(f.ci) {
		t.Errorf("backedges: %d of them", len(got))
	}
	for _, c := range []struct {
		args []any
		want string
	}{
		{[]any{int64(1), nil, f.ci}, "!TypeError(:ccall"},
		{[]any{f.mi, nil, int64(1)}, "!TypeError(:ccall"},
		{[]any{f.mi, int64(1), f.ci}, "!TypeError(:ccall"},
	} {
		v, err := f.sh("jl_method_instance_add_backedge", c.args...)
		check(t, "jl_method_instance_add_backedge", v, err, c.want)
	}
}

// jl_promote_mi_to_current: a MethodInstance whose method has no other
// signature in force is marked so, in the world it was validated in.
func TestPromoteMIToCurrent(t *testing.T) {
	f := newCodeinstFixture(t)
	w := f.vm.World()
	minW := f.meth.Fields[6].(uint64)
	reset := func() {
		f.meth.Fields[4] = uint8(0)
		f.mi.Fields[7] = uint8(0)
	}
	// the world is not the current one
	reset()
	if _, err := f.sh("jl_promote_mi_to_current", f.mi, minW, w+1); err != nil || f.mi.Fields[7] != uint8(0) {
		t.Errorf("another world: %v %v", err, f.mi.Fields[7])
	}
	// the method has another signature in force, or began in another world,
	// or the instance is already marked
	f.meth.Fields[4] = uint8(methodSigLatestOnly)
	if _, err := f.sh("jl_promote_mi_to_current", f.mi, minW, w); err != nil || f.mi.Fields[7] != uint8(0) {
		t.Errorf("the method is latest-only: %v %v", err, f.mi.Fields[7])
	}
	reset()
	if _, err := f.sh("jl_promote_mi_to_current", f.mi, minW+1, w); err != nil || f.mi.Fields[7] != uint8(0) {
		t.Errorf("another primary world: %v %v", err, f.mi.Fields[7])
	}
	f.mi.Fields[7] = uint8(methodSigLatestOnly | 1)
	if _, err := f.sh("jl_promote_mi_to_current", f.mi, minW, w); err != nil || f.mi.Fields[7] != uint8(methodSigLatestOnly|1) {
		t.Errorf("already marked: %v %v", err, f.mi.Fields[7])
	}
	// promoted
	reset()
	if _, err := f.sh("jl_promote_mi_to_current", f.mi, minW, w); err != nil || f.mi.Fields[7] != uint8(methodSigLatestOnly) {
		t.Errorf("promoted: %v %v", err, f.mi.Fields[7])
	}
	// a MethodInstance of no method (a top-level thunk's)
	thunk := &Struct{Type: f.vm.tMethodInstance, Fields: append([]any(nil), f.mi.Fields...)}
	thunk.Fields[0] = int64(1)
	if _, err := f.sh("jl_promote_mi_to_current", thunk, minW, w); err != nil || thunk.Fields[7] != f.mi.Fields[7] {
		t.Errorf("a thunk: %v", err)
	}
	for _, c := range []struct {
		args []any
		want string
	}{
		{[]any{int64(1), minW, w}, "!TypeError(:ccall"},
		{[]any{f.mi, int64(1), w}, "!not ported yet: jl_promote_mi_to_current"},
		{[]any{f.mi, minW, int64(1)}, "!not ported yet: jl_promote_mi_to_current"},
	} {
		v, err := f.sh("jl_promote_mi_to_current", c.args...)
		check(t, "jl_promote_mi_to_current", v, err, c.want)
	}
}

// jl_specializations_lookup: the MethodInstance a method already has for a
// type, nothing for another.
func TestSpecializationsLookup(t *testing.T) {
	f := newCodeinstFixture(t)
	other, err := f.s.Eval("Tuple{typeof(f), Int8}")
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.sh("jl_specializations_lookup", f.meth, f.tt)
	if err != nil || v != any(f.mi) {
		t.Errorf("a specialization: %v %v", v, err)
	}
	v, err = f.sh("jl_specializations_lookup", f.meth, other)
	check(t, "no specialization", v, err, "nothing")
	for _, c := range []struct {
		args []any
		want string
	}{
		{[]any{int64(1), f.tt}, "!TypeError(:ccall"},
		{[]any{f.meth, int64(1)}, "!TypeError(:specializations"},
		{[]any{f.meth, "x"}, "!TypeError(:specializations"},
	} {
		v, err := f.sh("jl_specializations_lookup", c.args...)
		check(t, "jl_specializations_lookup", v, err, c.want)
	}
}

// The jl_ir_* accessors of compressed IR, of a CodeInfo, and of what is
// neither.
func TestIRAccessorsOfStrings(t *testing.T) {
	f := newIRFixture(t)
	vm := f.vm
	data, err := f.compress(f.src)
	if err != nil {
		t.Fatal(err)
	}
	slots := vm.ciField(f.src, "slotflags")
	_, _, flags, _ := arrayOf(slots)
	for i, want := range flags {
		v, err := vm.shims["jl_ir_slotflag"](vm, []any{data, uint64(i)})
		if err != nil || v != want {
			t.Errorf("slotflag %d of the string: %v %v, want %v", i, v, err, want)
		}
		v, err = vm.shims["jl_ir_slotflag"](vm, []any{f.src, uint64(i)})
		if err != nil || v != want {
			t.Errorf("slotflag %d of the CodeInfo: %v %v, want %v", i, v, err, want)
		}
	}
	n := uint64(len(flags))
	for _, c := range []struct {
		f    string
		args []any
		want string
	}{
		{"jl_ir_slotflag", []any{data, n}, "!slot " + showValue(n) + " out of range"},
		{"jl_ir_slotflag", []any{data, int64(0)}, "!out of range"},
		{"jl_ir_slotflag", []any{f.src, n}, "!out of range"},
		{"jl_ir_slotflag", []any{int64(1), uint64(0)}, "!TypeError(:ccall"},
		{"jl_ir_slotflag", []any{"x", uint64(0)}, "!malformed compressed IR"},
		{"jl_ir_nslots", []any{"x"}, "!malformed compressed IR"},
		{"jl_ir_nslots", []any{int64(1)}, "!TypeError(:ccall"},
		{"jl_ir_flag_inlining", []any{"x"}, "!malformed compressed IR"},
		{"jl_ir_flag_has_image_globalref", []any{int64(1)}, "0x00"},
		{"jl_ir_flag_has_image_globalref", []any{"x"}, "!malformed compressed IR"},
		{"jl_ir_flag_has_fcall", []any{int64(1)}, "!TypeError(:ccall"},
		{"jl_ir_inlining_cost", []any{int64(1)}, "!TypeError(:ccall"},
		{"jl_ir_inlining_cost", []any{"x"}, "!malformed compressed IR"},
	} {
		v, err := vm.shims[expr.Symbol(c.f)](vm, c.args)
		check(t, c.f, v, err, c.want)
	}
	// a CodeInfo whose flag is not a UInt8 or a Bool
	bad := &Struct{Type: vm.tCodeInfo, Fields: append([]any(nil), f.src.Fields...)}
	f.setCI(bad, "inlining", int64(3))
	v, err := vm.shims["jl_ir_flag_inlining"](vm, []any{bad})
	check(t, "a bad inlining", v, err, "!TypeError(:ccall")
	f.setCI(bad, "inlining_cost", "x")
	v, err = vm.shims["jl_ir_inlining_cost"](vm, []any{bad})
	check(t, "a bad cost", v, err, "!TypeError(:ccall")
}

// A method added through source to a table whose world is ahead of the
// VM's is refused, not lost.
func TestMethodDefOfAStaleWorld(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval("g(x) = 1"); err != nil {
		t.Fatal(err)
	}
	ft := vm.typeOf(must(s.Eval("g"))).(*core.DataType)
	tab := vm.funcs[ft].Table
	sig, err := core.ApplyType(core.AnyTupleType, core.Int64Type)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tab.Define(sig, tab.World()+1000, nil); err != nil {
		t.Fatal(err)
	}
	_, err = s.Eval("g(x::Int8) = 2")
	check(t, "a stale world", nil, err, "!not newer than world")
}
