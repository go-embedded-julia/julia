package interp

import (
	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The other types jl_init_types creates in C (jltypes.c:2950-3840) and
// builtins.c:2487-2573 binds in Core, which boot.jl names but does not
// define. The IR nodes get their layouts, since boot.jl builds them with
// :new (boot.jl:519-539); the rest are opaque until something reads their
// fields.

func (vm *VM) bootCTypes() {
	imm := func(name string, fields []expr.Symbol, types ...any) *core.DataType {
		t := mustType("Core", name, core.AnyType, false, fields, types)
		vm.setCore(expr.Symbol(name), t)
		return t
	}
	vecInt32 := must(core.Apply(core.ArrayType, core.Int32Type, int64(1)))
	vecAny := must(core.Apply(core.ArrayType, core.AnyType, int64(1)))
	// jltypes.c:3165-3178
	vm.tSSAValue = imm("SSAValue", []expr.Symbol{"id"}, core.Int64Type)
	vm.tSlotNumber = imm("SlotNumber", []expr.Symbol{"id"}, core.Int64Type)
	vm.tArgument = imm("Argument", []expr.Symbol{"n"}, core.Int64Type)
	// jltypes.c:3401-3480
	imm("LegacyLineInfoNode", []expr.Symbol{"file", "line", "inlined_at"}, core.SymbolType, core.Int32Type, core.Int32Type)
	vm.tGotoNode = imm("GotoNode", []expr.Symbol{"label"}, core.Int64Type)
	vm.tGotoIfNot = imm("GotoIfNot", []expr.Symbol{"cond", "dest"}, core.AnyType, core.Int64Type)
	vm.tEnterNode = imm("EnterNode", []expr.Symbol{"catch_dest", "scope"}, core.Int64Type, core.AnyType)
	vm.tReturnNode = imm("ReturnNode", []expr.Symbol{"val"}, core.AnyType)
	imm("PiNode", []expr.Symbol{"val", "typ"}, core.AnyType, core.AnyType)
	imm("PhiNode", []expr.Symbol{"edges", "values"}, vecInt32, vecAny)
	imm("PhiCNode", []expr.Symbol{"values"}, vecAny)
	imm("UpsilonNode", []expr.Symbol{"val"}, core.AnyType)
	vm.tNewvarNode = imm("NewvarNode", []expr.Symbol{"slot"}, vm.tSlotNumber)
	// mayinlinealloc cleared by hand (jltypes.c:3481)
	imm("DebugInfo", []expr.Symbol{"def", "linetable", "edges", "codelocs"},
		core.AnyType, core.AnyType, core.SimpleVectorType, core.StringType).Name.MayInlineAlloc = false

	opaque := func(name string, params ...string) *core.DataType {
		tvs := make([]*core.TypeVar, len(params))
		for i, p := range params {
			tvs[i] = &core.TypeVar{Name: p, LB: core.Bottom, UB: core.AnyType}
		}
		t := core.NewIncompleteType("Core", name, tvs, false, true, nil)
		must(0, t.SetSuper(core.AnyType))
		must(0, t.SetFieldTypes(nil))
		vm.setCore(expr.Symbol(name), t.Name.Wrapper)
		return t
	}
	for _, name := range []string{"MethodCache", "TypeMapEntry", "TypeMapLevel"} {
		opaque(name)
	}
	vm.bootTaskType()
	// julia 1.12's one method table, Core.methodtable (jltypes.c:2959,
	// builtins.c:2508); inference compares edges against it by identity
	vm.methodTable = &Struct{Type: opaque("MethodTable")}
	vm.setCore("methodtable", vm.methodTable)
	vm.bootBindingTypes()
	vm.bootReflectionTypes()
	vm.bootCodeInstanceType()
	vm.bootCodeInfoType()
	vm.setCore("NamedTuple", core.NamedTupleType)
	opaque("OpaqueClosure", "A", "R")
	llvm := core.NewIncompletePrimitive("Core", "LLVMPtr", []*core.TypeVar{
		{Name: "T", LB: core.Bottom, UB: core.AnyType}, {Name: "AS", LB: core.Bottom, UB: core.AnyType}}, 64)
	must(0, llvm.SetSuper(core.AnyType))
	vm.setCore("LLVMPtr", llvm.Name.Wrapper)
	// the n_uninitialized of the runtime's types, as julia 1.12.6 reports
	// them (jltypes.c gives each its count; boot.jl's types get theirs from
	// _structtype, the VM's own definitions of BoundsError, UndefVarError
	// and Box julia's)
	for name, n := range map[string]int32{"BoundsError": 2, "UndefVarError": 1, "Box": 1,
		"Binding": 5, "BindingPartition": 5, "CodeInfo": 1,
		"CodeInstance": 20, "DataType": 5, "EnterNode": 1, "Method": 23, "MethodCache": 2,
		"MethodInstance": 5, "ReturnNode": 1, "Task": 21, "TypeMapEntry": 6, "TypeName": 16,
		"TypeofVararg": 2, "UpsilonNode": 1} {
		if b := vm.Core.own(expr.Symbol(name)); b != nil {
			if d, ok := core.UnwrapUnionAll(b.Value).(*core.DataType); ok {
				d.Name.NUninitialized = n
			}
		}
	}
}
