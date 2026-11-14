package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// objectIDSample is one value of each kind objectID gives an identity to.
func objectIDSample(tb testing.TB, vm *VM) []any {
	tb.Helper()
	var fn *Function
	for _, f := range vm.funcs {
		fn = f
		break
	}
	return []any{
		&Struct{Type: mutType("BenchM", core.Int32Type), Fields: []any{int32(0)}}, // a mutable struct
		&Memory{Type: memAnyType},          // a Memory
		vm.Core,                            // a Module
		fn,                                 // a Function
		expr.New("call", expr.Symbol("f")), // an Expr
		core.Int64Type,                     // a DataType, by its TypeName
		&MemoryRef{Type: memRefAnyType, Mem: &Memory{Type: memAnyType}},
		int64(42), "str", expr.Symbol("sym"),
	}
}

// BenchmarkObjectID is the cost of one objectid of each kind, in turn.
func BenchmarkObjectID(b *testing.B) {
	vm := New()
	if err := vm.Boot(); err != nil {
		b.Fatal(err)
	}
	vals := objectIDSample(b, vm)
	b.ReportAllocs()
	b.ResetTimer()
	var sink uint64
	for i := 0; i < b.N; i++ {
		for _, v := range vals {
			sink += vm.objectID(v)
		}
	}
	_ = sink
}
