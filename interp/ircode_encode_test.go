package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// More than 255 roots and edges, an svec of 300, and the encoder's errors:
// each is refused with an error, never a panic.
func TestIRLongAndErrors(t *testing.T) {
	f := newIRFixture(t)
	vm := f.vm
	mut := f.typ("Mut")
	var roots, svec []any
	for i := 0; i < 300; i++ {
		roots = append(roots, f.new(mut, int64(i)))
		svec = append(svec, int64(i))
	}
	// 300 roots: indexes past one byte
	ci := f.code(roots, int64(len(roots)))
	data, _ := f.roundTrip("long roots", ci, nil)
	if len(data) == 0 {
		t.Error("nothing compressed")
	}
	// 300 edges, the last of which is wanted
	edges := SVec(roots)
	f.ci.Fields[ciEdges] = edges
	ci = f.code([]any{roots[299], roots[0]}, int64(2))
	f.setCI(ci, "edges", edges)
	f.roundTrip("long edges", ci, f.ci)
	// an svec of 300
	f.roundTrip("long svec", f.code([]any{SVec(svec), SVec{}}, int64(2)), nil)
	// a CodeInstance without edges
	f.ci.Fields[ciEdges] = nil
	f.roundTrip("no edges", f.code([]any{int64(1)}, int64(1)), f.ci)

	bad := expr.GlobalRef{Mod: "NoSuch", Name: "x"}
	for _, c := range []struct {
		name string
		stmt any
	}{
		{"gotoifnot", expr.GotoIfNot{Cond: bad, Dest: 1}},
		{"svec", SVec{int64(1), bad}},
		{"call", &expr.Expr{Head: "call", Args: []any{bad, int64(1)}}},
		{"expr", &expr.Expr{Head: "new", Args: []any{bad}}},
		{"enter scope", expr.EnterNode{Catch: 1, Scope: bad, HasScope: true}},
		{"return", expr.ReturnNode{Val: bad}},
		{"phi", f.new(f.core("PhiNode"), vm.newVector(core.Int32Type, []any{int32(1)}), vm.newVector(core.AnyType, []any{bad}))},
		{"phic", f.new(f.core("PhiCNode"), vm.newVector(core.AnyType, []any{bad}))},
		{"globalref", expr.GlobalRef{Mod: "NoSuch", Name: "y"}},
		{"a value of a struct", f.new(f.core("PiNode"), bad, core.Int64Type)},
		{"quote", expr.QuoteNode{Value: bad}},
	} {
		_, err := f.compress(f.code([]any{c.stmt}, int64(1)))
		check(t, c.name, nil, err, "!a module this VM does not name")
	}
	// the slot types of an opaque closure
	f.m.Fields[methodIsOpaque] = true
	ci = f.code([]any{int64(1)}, int64(1))
	f.setCI(ci, "slottypes", vm.newVector(core.AnyType, []any{bad}))
	_, err := f.compress(ci)
	check(t, "slottypes", nil, err, "!a module this VM does not name")
	f.m.Fields[methodIsOpaque] = false

	// values julia could write but whose memory this VM cannot lay out
	mem := func(elt any, elems []any) *Memory {
		return &Memory{Type: must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), elt, core.CPU)).(*core.DataType), Elems: elems}
	}
	if _, err := f.s.Eval("struct SU\na::Union{Int,Nothing}\nend\nstruct Outer\na::Int8\nt::Two\nend"); err != nil {
		t.Fatal(err)
	}
	two := f.typ("Two").(*core.DataType)
	for _, c := range []struct {
		name string
		v    any
		want string
	}{
		{"a primitive of the wrong kind", mem(core.Int64Type, []any{"x"}), "!the bits of a"},
		{"a struct with an isbits Union", &Struct{Type: f.typ("SU").(*core.DataType), Fields: []any{int64(1)}}, "!the image of an inline Union field"},
		{"an undefined field", &Struct{Type: f.typ("Mut").(*core.DataType), Fields: []any{undef}}, "!UndefRefError"},
		{"a bad nested struct", &Struct{Type: f.typ("Outer").(*core.DataType), Fields: []any{int8(1), "x"}}, "!"},
		{"a struct as bits", &Struct{Type: two, Fields: []any{"x", "y"}}, "!"},
	} {
		_, err := f.compress(f.code([]any{int64(1)}, c.v))
		check(t, c.name, nil, err, c.want)
	}
	// a struct whose pointer field fails, as a literal
	_, err = f.compress(f.code([]any{int64(1)}, f.new(f.typ("Mixed"), int8(1), bad, int32(3))))
	check(t, "a pointer field", nil, err, "!a module this VM does not name")
	// ciField of a field CodeInfo has not
	func() {
		defer func() {
			if recover() == nil {
				t.Error("ciField of an unknown field did not panic")
			}
		}()
		vm.ciField(f.src, "nosuch")
	}()
	_, err = vm.shims["jl_uncompress_argnames"](vm, []any{int64(1)})
	check(t, "uncompress_argnames", nil, err, "!TypeError(:ccall")
	v, err := vm.shims["jl_uncompress_argnames"](vm, []any{"a\x00bc\x00"})
	check(t, "uncompress_argnames", v, err, "[:a, :bc]")
}
