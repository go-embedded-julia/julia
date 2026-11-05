package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

func (f *pcreFixture) sh(name string, args ...any) (any, error) {
	f.t.Helper()
	sh := f.vm.shims[expr.Symbol(name)]
	if sh == nil {
		f.t.Fatalf("no shim %s", name)
	}
	return sh(f.vm, args)
}

// ENV's functions: getenv, setenv, unsetenv, jl_environ, uv_os_homedir and
// the stat family. The setenv and unsetenv answers are julia 1.12.6's.
func TestEnvShims(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	vm.Env = nil
	if v, err := f.sh("getenv", f.str("JLX")); err != nil || v.(*Pointer).Target != nil {
		t.Errorf("getenv of an unset name: %v %v", v, err)
	}
	for _, c := range []struct {
		args []any
		want string
	}{
		{[]any{f.str(""), f.str("v"), int32(1)}, "-1"},        // julia: -1
		{[]any{f.str("a=b"), f.str("v"), int32(1)}, "-1"},     // julia: -1
		{[]any{f.str("JLX"), f.str("v"), int32(1)}, "0"},      // julia: 0
		{[]any{f.str("JLX"), f.str("w"), int32(0)}, "0"},      // julia: 0, JLX stays v
		{[]any{int64(1), f.str("w"), int32(0)}, "!setenv of"}, // not a Cstring
		{[]any{f.str("JLX"), int64(1), int32(0)}, "!setenv of"},
		{[]any{f.str("JLX"), f.str("w"), int64(0)}, "!setenv of"},
	} {
		v, err := f.sh("setenv", c.args...)
		check(t, "setenv", v, err, c.want)
	}
	v, err := f.sh("getenv", f.str("JLX"))
	if err != nil || v.(*Pointer).Target != "v" {
		t.Errorf("getenv after setenv without overwrite: %v %v", v, err)
	}
	if _, err := f.sh("setenv", f.str("JLX"), f.str("w"), int32(1)); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.sh("getenv", f.str("JLX")); v.(*Pointer).Target != "w" {
		t.Errorf("getenv after an overwrite: %v", v)
	}
	if _, err := f.sh("getenv", int64(1)); err == nil {
		t.Error("getenv of an Int")
	}
	for _, c := range []struct {
		name any
		want string
	}{{f.str(""), "-1"}, {f.str("a=b"), "-1"}, {f.str("JLX"), "0"}, {f.str("NOPE_X"), "0"}, {int64(1), "!unsetenv of"}} {
		v, err := f.sh("unsetenv", c.name)
		check(t, "unsetenv", v, err, c.want)
	}
	if v, _ := f.sh("getenv", f.str("JLX")); v.(*Pointer).Target != nil {
		t.Error("getenv after unsetenv")
	}

	// jl_environ: sorted "name=value", nothing past the end
	vm.Env = map[string]string{"B": "2", "A": "1"}
	for _, c := range []struct {
		i    any
		want string
	}{{int32(0), `"A=1"`}, {int32(1), `"B=2"`}, {int32(2), "nothing"}, {int32(-1), "nothing"}, {int64(0), "!jl_environ of"}} {
		v, err := f.sh("jl_environ", c.i)
		check(t, "jl_environ", v, err, c.want)
	}

	// uv_os_homedir: $HOME, else the passwd entry's; ENOBUFS (-55) with the
	// size needed
	home := func(size uint64, dst any) (int32, uint64, error) {
		ref, st := f.ref("RefU64", size)
		v, err := f.sh("uv_os_homedir", dst, ref)
		rc, _ := v.(int32)
		return rc, st.Fields[0].(uint64), err
	}
	vm.Env = map[string]string{"HOME": "/h"}
	buf, m := f.buf(8, 0xaa)
	if rc, n, err := home(8, buf); err != nil || rc != 0 || n != 2 || joinBytes(m)[:9] != "47,104,0," {
		t.Errorf("a home that fits: %d %d %v %s", rc, n, err, joinBytes(m))
	}
	if rc, n, err := home(2, buf); err != nil || rc != -55 || n != 3 {
		t.Errorf("a buffer too small: %d %d %v", rc, n, err)
	}
	vm.Env, vm.HomeDir = nil, "/x/y"
	if rc, n, err := home(8, buf); err != nil || rc != 0 || n != 4 || m.Elems[3] != uint8('y') {
		t.Errorf("the passwd home: %d %d %v", rc, n, err)
	}
	_, err = f.sh("uv_os_homedir", buf, int64(1))
	check(t, "a size that is no pointer", nil, err, "!uv_os_homedir of")
	_, err = f.sh("uv_os_homedir", buf, &Pointer{Type: f.vm.ptrType(core.UInt64Type)})
	check(t, "a size into nothing", nil, err, "!invalid pointer")
	_, _, err = home(8, int64(1))
	check(t, "a destination that is no pointer", nil, err, "!TypeError(:ccall")

	// the filesystem is out of reach: every path is absent
	v, err = f.sh("jl_sizeof_stat")
	check(t, "jl_sizeof_stat", v, err, "160")
	for _, name := range []string{"jl_stat", "jl_lstat"} {
		v, err := f.sh(name, f.str("/nope"), nil)
		check(t, name, v, err, "-2")
		_, err = f.sh(name, int64(1), nil)
		check(t, name, nil, err, "!stat of")
	}
}

// The root task, the only one.
func TestRootTask(t *testing.T) {
	f := newPCREFixture(t)
	a, err := f.sh("jl_get_current_task")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := f.sh("jl_get_current_task")
	task := a.(*Struct)
	if a != b || task.Type != f.vm.tTask || len(task.Fields) != 27 {
		t.Errorf("the root task: %v", showValue(task.Type))
	}
	if task.Fields[7] != any(uint8(0)) || task.Fields[8] != any(true) {
		t.Errorf("_state %v sticky %v", task.Fields[7], task.Fields[8])
	}
}

// The shims added for Base's startup, each called as ccall would.
func TestStartupShims(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	ty := core.Int64Type
	v, err := f.sh("jl_type_hash", ty)
	if err != nil || v != core.TypeHash(ty) {
		t.Errorf("jl_type_hash: %v %v", v, err)
	}
	vm.finalizersInhibited = 0
	f.sh("jl_gc_disable_finalizers_internal")
	f.sh("jl_gc_disable_finalizers_internal")
	f.sh("jl_gc_enable_finalizers_internal")
	if vm.finalizersInhibited != 1 {
		t.Errorf("finalizers inhibited: %d", vm.finalizersInhibited)
	}
	v, err = f.sh("jl_set_precompile_field_replace")
	check(t, "jl_set_precompile_field_replace", v, err, "nothing")
	// julia 1.12.6: uv_handle_size(1) 96, an unknown type (size_t)-1,
	// uv_req_size(1) 72, sizeof(ios_t) 184
	for _, c := range []struct {
		name string
		arg  any
		want string
	}{
		{"uv_handle_size", int32(1), "0x0000000000000060"},
		{"uv_handle_size", int32(999), "0xffffffffffffffff"},
		{"uv_req_size", int32(1), "0x0000000000000048"},
		{"jl_sizeof_ios_t", nil, "184"},
	} {
		v, err := f.sh(c.name, c.arg)
		check(t, c.name, v, err, c.want)
	}
	// the String and memory shims by their ccall names
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"jl_alloc_string", []any{int64(2)}, `"\0\0"`},
		{"jl_cstr_to_string", []any{f.str("hi\x00x")}, `"hi"`},
		{"jl_pchar_to_string", []any{f.str("hello"), int64(2)}, `"he"`},
		{"jl_string_to_genericmemory", []any{"ab"}, "UInt8[0x61, 0x62]"},
		{"memhash_seed", []any{f.str("abc"), uint64(3), uint32(0)}, "!"},
	} {
		v, err := f.sh(c.name, c.args...)
		if c.name == "memhash_seed" {
			if err != nil {
				t.Errorf("memhash_seed: %v", err)
			}
			continue
		}
		check(t, c.name, v, err, c.want)
	}
	mem := &Memory{Type: memUInt8Type, Elems: []any{uint8('a'), uint8('b')}}
	if v, err := f.sh("jl_genericmemory_to_string", mem, int64(2)); err != nil || v != any("ab") {
		t.Errorf("jl_genericmemory_to_string: %v %v", v, err)
	}
	dst := &Memory{Type: memUInt8Type, Elems: []any{uint8(0), uint8(0)}}
	if _, err := f.sh("memmove", &Pointer{Type: f.u8p, Target: dst}, f.str("xy"), uint64(2)); err != nil || joinBytes(dst) != "120,121" {
		t.Errorf("memmove: %v %s", err, joinBytes(dst))
	}
	if _, err := f.sh("memset", &Pointer{Type: f.u8p, Target: dst}, int32(7), uint64(2)); err != nil || joinBytes(dst) != "7,7" {
		t.Errorf("memset: %v %s", err, joinBytes(dst))
	}
	v, err = f.sh("memcmp", &Pointer{Type: f.u8p, Target: dst}, f.str("\x07\x08"), uint64(2))
	check(t, "memcmp", v, err, "-1")
	// jl_get_fenv_consts, jl_copy_code_info, jl_value_ptr
	fe := &Memory{Type: memType(core.Int32Type), Elems: make([]any, 9)}
	if _, err := f.sh("jl_get_fenv_consts", &Pointer{Type: vm.ptrType(core.Int32Type), Target: fe}); err != nil {
		t.Error(err)
	}
	ci, err := f.sh("jl_copy_code_info", codeInfo([]any{expr.ReturnNode{Val: int64(1)}}))
	if err != nil || ci.(*Struct).Type != vm.tCodeInfo {
		t.Errorf("jl_copy_code_info: %v", err)
	}
	p, err := f.sh("jl_value_ptr", "x")
	if err != nil || p.(*Pointer).Target != any("x") {
		t.Errorf("jl_value_ptr: %v %v", p, err)
	}
	// the grapheme break's state: the store back through a pointer of another type
	slot := &ptrSlot{name: "st", val: int32(0)}
	_, err = f.sh("utf8proc_grapheme_break_stateful", uint32('a'), uint32('b'), &Pointer{Type: vm.ptrType(core.Int64Type), Target: slot})
	check(t, "grapheme break into a slot of another type", nil, err, "!TypeError")
}

// memchr and memrchr on "abcabc"; each want is julia 1.12.6's offset (-1 for
// C_NULL).
func TestMemchr(t *testing.T) {
	f := newPCREFixture(t)
	s := f.str("abcabc")
	off := func(name string, c int32, n uint64) int64 {
		v, err := f.sh(name, s, c, n)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if v.(*Pointer).Target == nil {
			return -1
		}
		return v.(*Pointer).Off
	}
	for _, c := range []struct {
		c    int32
		n    uint64
		want [2]int64
	}{
		{'b', 6, [2]int64{1, 4}}, {'z', 6, [2]int64{-1, -1}}, {'b', 0, [2]int64{-1, -1}},
		{0x162, 6, [2]int64{1, 4}}, {'c', 2, [2]int64{-1, -1}}, {'c', 3, [2]int64{2, 2}},
	} {
		if got := [2]int64{off("memchr", c.c, c.n), off("memrchr", c.c, c.n)}; got != c.want {
			t.Errorf("memchr/memrchr of %q in %d bytes: %v, julia %v", rune(c.c&0xff), c.n, got, c.want)
		}
	}
	for _, args := range [][]any{{int64(1), int32(1), uint64(1)}, {s, int64(1), uint64(1)}, {s, int32(1), int64(1)}, {s, int32(1), uint64(7)}} {
		_, err := f.sh("memchr", args...)
		check(t, "memchr", nil, err, "!memchr of")
	}
}

// jl_instantiate_type_in_env through an svec's data and a memory's; each
// want is julia 1.12.6's.
func TestInstantiateTypeInEnvShim(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	u := must(f.s.Eval("Ptr{T} where T"))
	ub := core.UnwrapUnionAll(u)
	vv := must(f.s.Eval("Tuple{A,B} where A where B"))
	vb := core.UnwrapUnionAll(vv)
	any8 := vm.ptrType(core.AnyType)
	svp := func(off int64, vs ...any) *Pointer { return &Pointer{Type: any8, Target: SVec(vs), Off: off} }
	memp := func(vs ...any) *Pointer {
		return &Pointer{Type: any8, Target: &Memory{Type: memAnyType, Elems: vs}}
	}
	null := &Pointer{Type: any8}
	for _, c := range []struct {
		name         string
		t, env, vals any
		want         string
	}{
		{"an svec", ub, u, svp(8, core.Int64Type), "Ptr{Int64}"},
		{"a memory", ub, u, memp(core.Int8Type), "Ptr{Int8}"},
		{"no variable", core.Int64Type, core.Int64Type, null, "Int64"},
		{"a value that is no type", ub, u, svp(8, int64(3)), "Ptr{3}"},
		{"two in an svec", vb, vv, svp(8, core.Int64Type, core.Int8Type), "Tuple{Int8, Int64}"},
		{"two in a memory", vb, vv, memp(core.Int64Type, core.Int8Type), "Tuple{Int8, Int64}"},
		{"not a UnionAll", must(f.s.Eval("Array{Int,1}")), core.Int64Type, null, "Vector{Int64}"},
		{"an svec's second element", vb, vv, svp(16, core.Int64Type, core.Int8Type, core.Int8Type), "Tuple{Int8, Int8}"},
		{"before the elements", ub, u, svp(0, core.Int64Type), "!jl_instantiate_type_in_env of"},
		{"misaligned", ub, u, svp(12, core.Int64Type), "!jl_instantiate_type_in_env of"},
		{"too few", vb, vv, svp(8, core.Int64Type), "!jl_instantiate_type_in_env of"},
		{"a memory with an undefined element", ub, u, memp(undef), "!jl_instantiate_type_in_env of"},
		{"another target", ub, u, &Pointer{Type: any8, Target: "x"}, "!jl_instantiate_type_in_env of"},
		{"no pointer", ub, u, int64(1), "!jl_instantiate_type_in_env of"},
		{"nothing", ub, u, null, "!jl_instantiate_type_in_env of"},
	} {
		v, err := f.sh("jl_instantiate_type_in_env", c.t, c.env, c.vals)
		if v != nil {
			if s := showValue(v); s != c.want && "Main.L."+c.want != s {
				t.Errorf("%s: %s, julia %s", c.name, s, c.want)
			}
			continue
		}
		check(t, c.name, v, err, c.want)
	}
}

// A ccall whose argument types hold a static parameter, whose callee is
// computed, and whose pointer result is a Cstring-like primitive.
func TestForeignCalls(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	s := f.s
	v, err := s.Eval("h(x::T) where {T} = ccall(:jl_object_id, UInt64, (T,), x); h(5)")
	check(t, "a static parameter in the argument types", v, err, Repr(vm.objectID(int64(5))))
	v, err = s.Eval("const cn = :jl_threadid\nct() = ccall(cn, Int16, ())\nct()")
	check(t, "a computed callee", v, err, "0")
	_, err = s.Eval("const cp = 5\ncq() = ccall(cp, Int16, ())\ncq()")
	check(t, "a callee that is no Symbol", nil, err, "!a ccall to 5")
	// the callee a global that holds nothing when the call runs
	undefCallee := expr.New("foreigncall", expr.GlobalRef{Mod: s.Mod.Name, Name: "nosuchcallee"}, core.Int16Type, SVec{}, int64(0),
		expr.QuoteNode{Value: expr.Symbol("ccall")})
	_, err = runIR(s, codeInfo([]any{undefCallee, expr.ReturnNode{Val: expr.SSAValue(0)}}), false)
	check(t, "a callee that is undefined", nil, err, "!UndefVarError")
	v, err = s.Eval("pc() =ccall(:jl_gc_active_impl, PCStr, ())\npc()")
	if err != nil {
		t.Fatal(err)
	}
	if pv, ok := v.(PrimValue); !ok || pv.Type.Name.Name != "PCStr" {
		t.Errorf("a pointer returned as a Cstring: %v", v)
	}
}

// The tuple and Expr nodes `new` makes.
func TestAstNodeOfAnExpr(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	args := vm.newVector(core.AnyType, []any{int64(1), int64(2)})
	v, err := vm.newStruct(vm.tExpr, []any{expr.Symbol("foo"), args})
	if err != nil {
		t.Fatal(err)
	}
	e, ok := v.(*expr.Expr)
	if !ok || e.Head != "foo" || len(e.Args) != 2 {
		t.Errorf("Expr(:foo, [1, 2]): %v", v)
	}
	// a head that is no Symbol is no Expr node here
	if _, ok := vm.astNode(vm.tExpr, []any{int64(1), args}); ok {
		t.Error("an Expr with an Int head")
	}
}

// A spread of an Array, of a Memory with an undefined element.
func TestApplyIterateOfArrays(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	tup := vm.Core.own("tuple").Value
	v, err := biApplyIterate(vm, []any{nil, tup, vm.newVector(core.AnyType, []any{int64(1), int64(2)})})
	check(t, "a Vector", v, err, "(1, 2)")
	_, err = biApplyIterate(vm, []any{nil, tup, vm.newVector(core.AnyType, []any{undef})})
	check(t, "a Vector with an undefined element", nil, err, "!UndefRefError")
}
