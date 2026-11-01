// Package interp runs julia's lowered code: a port of src/interpreter.c of
// julia 1.12.6 (eval_value, eval_body and the method definition form) over
// the values of packages core and expr.
//
// A CodeInfo is prepared once (prepare.go) into statements whose operands
// are checked, then walked with a program counter (eval.go), as
// jl_interpret_toplevel_thunk and jl_fptr_interpret_call do. Only Core is
// here: Base's code does not run yet, and lowered code that reaches a Base
// function raises an UndefVarError naming it.
package interp

import (
	"fmt"
	"io"
	"io/fs"
	"sync"
	"sync/atomic"
	"weak"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
)

// VM is one julia session: its world counter, its modules and the types the
// runtime creates.
type VM struct {
	world atomic.Uint64

	Core, Base, Main *Module
	modules          map[expr.Symbol]*Module
	// topModule is jl_top_module, the top module of every module without
	// an enclosing one. In julia it is Core (init.c:593) until a primary
	// Base sets itself (jl_set_istopmod). A VM that has not booted models
	// the Main of a running julia, whose top module is Base: there it is
	// the Base New makes, and Boot sets Core, as after julia's boot.jl.
	topModule *Module
	// baseModule is jl_base_module: the Base standard imports use, nil
	// from Boot until Base_compiler.jl's `module Base` (toplevel.c:177-180).
	// A VM that has not booted has the Base New makes.
	baseModule *Module

	// MaxDepth bounds the nesting of calls and of try blocks; past it a
	// call or an EnterNode raises StackOverflowError instead of exhausting
	// the Go stack.
	MaxDepth int
	depth    int

	// MaxSteps, when positive, bounds the statements one VM executes; past
	// it evaluation stops with ErrStepLimit. An embedder running untrusted
	// cells sets it: `while true end` is valid Julia.
	MaxSteps int64
	steps    int64

	// taskWorld is the world age of the running task (ct->world_age);
	// excStack its exception stack (jl_excstack_t), whose depth an
	// EnterNode records and :pop_exception restores.
	taskWorld uint64
	excStack  []any

	// funcs finds a generic function from its type, for jl_method_def.
	funcs map[*core.DataType]*Function
	// ctors are the constructors of each type this VM created: the methods
	// julia defines for Type{T}.
	ctors map[*core.TypeName]*dispatch.Table
	// others holds the methods of every other callee (Union{}, callable
	// structs), the callee's type first in each signature.
	others *dispatch.Table

	// shims are the Go functions ccalls reach (ccall.go); sessions the
	// lowering state of each module code is evaluated in.
	shims    map[expr.Symbol]shim
	sessions map[*Module]*Session

	// Sources holds the files include reads (load.go): julia's own sources
	// by default, rooted as julia's base/ is.
	Sources fs.FS

	// MaxMemoryElems bounds the elements of one memorynew; past it the
	// allocation stops with ErrMemoryLimit instead of exhausting the host.
	// emptyMemory holds each memory type's zero-length instance.
	MaxMemoryElems int
	emptyMemory    map[*core.DataType]*Memory

	// Stdout and Stderr receive what Julia code prints (Core.println,
	// print): io.Discard unless the embedder sets them. cglobals are the C
	// globals cglobal can name (vptr.go).
	Stdout, Stderr io.Writer
	cglobals       map[expr.Symbol]*ptrSlot

	// argVectors is each Expr's args vector (exprargs.go).
	argMu      sync.Mutex
	argVectors map[weak.Pointer[expr.Expr]]*Struct

	builtinType, intrinsicType  *core.DataType
	tError, tUndefVar           *core.DataType
	tTypeError, tMethodError    *core.DataType
	tStackOverflow, tArgError   *core.DataType
	tBounds, tUndefRef          *core.DataType
	tDivide, tLoadError         *core.DataType
	tAtomicError                *core.DataType
	tExpr, tQuoteNode           *core.DataType
	tSSAValue, tSlotNumber      *core.DataType
	tArgument, tGotoNode        *core.DataType
	tGotoIfNot, tEnterNode      *core.DataType
	tReturnNode, tNewvarNode    *core.DataType
	tCodeInfo                   *core.DataType
	tLineNumberNode, tGlobalRef *core.DataType
	typeofFn                    *Function
}

// DefaultMaxDepth is the call depth a new VM allows.
const DefaultMaxDepth = 4000

// New returns a VM with Core, Base and Main, and a module named name under
// Main in which Eval runs (L, as the oracle names its scratch module).
func New() *VM {
	vm := &VM{modules: map[expr.Symbol]*Module{}, MaxDepth: DefaultMaxDepth,
		funcs: map[*core.DataType]*Function{}, ctors: map[*core.TypeName]*dispatch.Table{},
		sessions: map[*Module]*Session{}, others: &dispatch.Table{Name: "#callable"}, Sources: baseSources,
		MaxMemoryElems: DefaultMaxMemoryElems, emptyMemory: map[*core.DataType]*Memory{},
		argVectors: map[weak.Pointer[expr.Expr]]*Struct{}, Stdout: io.Discard, Stderr: io.Discard}
	vm.world.Store(1)
	vm.Core = vm.addModule("Core", nil)
	// Core lowers against itself, as boot.jl needs (it marks Core a top
	// module itself at its end)
	vm.Core.istopmod = true
	vm.Base = vm.addModule("Base", nil)
	vm.topModule, vm.baseModule = vm.Base, vm.Base
	vm.Main = vm.addModule("Main", nil)
	vm.Base.usings = []*Module{vm.Core}
	vm.Main.usings = []*Module{vm.Core, vm.Base}
	vm.bootCore()
	// the task starts in the newest world, after boot's definitions
	vm.taskWorld = vm.World()
	return vm
}

func (vm *VM) addModule(name expr.Symbol, parent *Module) *Module {
	m := newModule(name, parent)
	vm.modules[name] = m
	return m
}

// NewModule creates a module under Main that sees Core and Base, as a
// `module` block does.
func (vm *VM) NewModule(name expr.Symbol) *Module {
	m := vm.addModule(name, vm.Main)
	m.usings = []*Module{vm.Core, vm.Base}
	vm.Main.declare(name, 0).Value = m
	// a module made with the standard imports names Core and Base
	for _, std := range []*Module{vm.Core, vm.Base} {
		b := m.declare(std.Name, 0)
		b.Value, b.Const = std, true
	}
	return m
}

// World is the newest world.
func (vm *VM) World() uint64 { return vm.world.Load() }

// newWorld moves the counter one world ahead and returns it
// (jl_world_counter++ under the world lock).
func (vm *VM) newWorld() uint64 { return vm.world.Add(1) }

// coreExports are the names Core exports (boot.jl:207-243) that this
// version defines; ifelse, svec, Typeof and the rest stay qualified.
var coreExports = map[expr.Symbol]bool{
	"Core": true, // every module exports its own name (jl_add_default_names)
	"Any":  true, "DataType": true, "Vararg": true, "Tuple": true, "Type": true, "UnionAll": true,
	"TypeVar": true, "Union": true, "Nothing": true, "Cvoid": true, "Function": true, "Module": true,
	"Symbol": true, "Number": true, "Real": true, "Integer": true, "Bool": true, "AbstractFloat": true,
	"Float16": true, "Float32": true, "Float64": true, "Signed": true, "Int": true, "Int8": true,
	"Int16": true, "Int32": true, "Int64": true, "Unsigned": true, "UInt": true, "UInt8": true,
	"UInt16": true, "UInt32": true, "UInt64": true, "AbstractChar": true, "Char": true,
	"AbstractString": true, "String": true, "ErrorException": true, "BoundsError": true,
	"DivideError": true, "Exception": true, "StackOverflowError": true, "UndefRefError": true,
	"UndefVarError": true, "TypeError": true, "ArgumentError": true, "MethodError": true,
	"getfield": true, "nfields": true, "throw": true, "tuple": true, "===": true,
	"getglobal": true, "setglobal!": true, "isdefinedglobal": true, "<:": true, "typeof": true,
	"isa": true, "typeassert": true, "nothing": true, "Main": true,
	"Expr": true, "QuoteNode": true, "LineNumberNode": true, "GlobalRef": true,
	"fieldtype": true, "setfield!": true,
}

// setCore binds name in Core in world 0, exported when Core exports it.
func (vm *VM) setCore(name expr.Symbol, v any) {
	b := vm.Core.declare(name, 0)
	b.Value, b.Const, b.Exports = v, true, coreExports[name]
}

// bootCore binds what Core holds before boot.jl runs: the built-in types
// (builtins.c:2487-2573), the builtins and intrinsics, and the parts of
// boot.jl this first version needs, built in Go.
func (vm *VM) bootCore() {
	// Core and Main name themselves, exported (jl_add_default_names), and
	// Core names Main (jl_init_main_module, toplevel.c:49-53)
	for _, m := range []*Module{vm.Core, vm.Main} {
		self := m.declare(m.Name, 0)
		self.Value, self.Const, self.Exports = m, true, true
	}
	cm := vm.Core.declare("Main", 0)
	cm.Value, cm.Const = vm.Main, true
	for name, t := range map[expr.Symbol]any{
		"Any": core.AnyType, "Type": core.TypeType, "Nothing": core.NothingType, "nothing": nil,
		"TypeName": core.TypeNameType, "DataType": core.DataTypeType, "TypeVar": core.TypeVarType,
		"UnionAll": core.UnionAllType, "Union": core.UnionType, "TypeofBottom": core.TypeofBottomType,
		"Tuple": core.AnyTupleType, "TypeofVararg": core.TypeofVarargType, "Vararg": core.VarargType,
		"SimpleVector": core.SimpleVectorType, "Module": core.ModuleType, "Symbol": core.SymbolType,
		"Function": core.FunctionType, "Bool": core.BoolType, "Int": core.Int64Type,
		"UInt": core.UInt64Type, "Cvoid": core.NothingType,
		"Int8": core.Int8Type, "UInt8": core.UInt8Type, "Int16": core.Int16Type, "UInt16": core.UInt16Type,
		"Int32": core.Int32Type, "UInt32": core.UInt32Type, "Int64": core.Int64Type, "UInt64": core.UInt64Type,
		"Float16": core.Float16Type, "Float32": core.Float32Type, "Float64": core.Float64Type,
		"Number": core.NumberType, "Real": core.RealType, "AbstractFloat": core.AbstractFloatType,
		"Integer": core.IntegerType, "Signed": core.SignedType, "Unsigned": core.UnsignedType,
		"AbstractString": core.AbstractStringType, "String": core.StringType, "Char": core.CharType,
		"AbstractChar": core.AbstractCharType, "Exception": core.ExceptionType,
		// builtins.c:2530-2538
		"AddrSpace": core.AddrSpaceType, "CPU": core.CPU, "Ref": core.RefType, "Ptr": core.PtrType,
		"AbstractArray": core.AbstractArrayType, "DenseArray": core.DenseArrayType, "Array": core.ArrayType,
		"GenericMemory": core.GenericMemoryType, "GenericMemoryRef": core.GenericMemoryRefType,
		"Core": vm.Core, "Base": vm.Base, "Main": vm.Main,
	} {
		vm.setCore(name, t)
	}
	vm.builtinType = mustType("Core", "Builtin", core.FunctionType, true, nil, nil)
	vm.setCore("Builtin", vm.builtinType)
	// primitive type IntrinsicFunction <: Builtin 32 end (boot.jl): many
	// instances, one per intrinsic, so not a singleton
	vm.intrinsicType = mustType("Core", "IntrinsicFunction", vm.builtinType, false, nil, nil)
	vm.intrinsicType.Name.Singleton = false
	vm.setCore("IntrinsicFunction", vm.intrinsicType)

	// boot.jl:368-446, the exceptions the interpreter itself raises
	ex := func(name expr.Symbol, fields []expr.Symbol, types ...any) *core.DataType {
		t := mustType("Core", string(name), core.ExceptionType, false, fields, types)
		vm.setCore(name, t)
		return t
	}
	vm.tError = ex("ErrorException", []expr.Symbol{"msg"}, core.AbstractStringType)
	vm.tBounds = ex("BoundsError", []expr.Symbol{"a", "i"}, core.AnyType, core.AnyType)
	vm.tDivide = ex("DivideError", nil)
	vm.tStackOverflow = ex("StackOverflowError", nil)
	vm.tUndefRef = ex("UndefRefError", nil)
	vm.tUndefVar = ex("UndefVarError", []expr.Symbol{"var", "world", "scope"}, core.SymbolType, core.UInt64Type, core.AnyType)
	vm.tTypeError = ex("TypeError", []expr.Symbol{"func", "context", "expected", "got"},
		core.SymbolType, core.AnyType, core.TypeType, core.AnyType)
	vm.tArgError = ex("ArgumentError", []expr.Symbol{"msg"}, core.AbstractStringType)
	vm.tMethodError = ex("MethodError", []expr.Symbol{"f", "args", "world"}, core.AnyType, core.AnyType, core.UInt64Type)

	// the AST types (jltypes.c:3284-3288, 3387-3399, 3455-3459), the types of
	// quoted code a program holds as a value
	vecAny, _ := core.Apply(core.ArrayType, core.AnyType, int64(1))
	vm.tExpr, _ = core.NewType("Core", "Expr", core.AnyType, false, true,
		[]expr.Symbol{"head", "args"}, []any{core.SymbolType, vecAny})
	vm.tQuoteNode = mustType("Core", "QuoteNode", core.AnyType, false, []expr.Symbol{"value"}, []any{core.AnyType})
	vm.tLineNumberNode = mustType("Core", "LineNumberNode", core.AnyType, false,
		[]expr.Symbol{"line", "file"}, []any{core.Int64Type, core.TypeUnion(core.SymbolType, core.NothingType)})
	vm.tGlobalRef = mustType("Core", "GlobalRef", core.AnyType, false,
		[]expr.Symbol{"mod", "name", "binding"}, []any{core.ModuleType, core.SymbolType, core.AnyType})
	for name, t := range map[expr.Symbol]*core.DataType{"Expr": vm.tExpr, "QuoteNode": vm.tQuoteNode,
		"LineNumberNode": vm.tLineNumberNode, "GlobalRef": vm.tGlobalRef} {
		vm.setCore(name, t)
	}

	vm.bootCTypes()
	vm.bootBuiltins()
	vm.bootTypedefs()
	vm.bootIntrinsics()
	vm.bootMemory()
	vm.bootPointers()
	vm.bootShims()

	// Base.iterate, the iterate lowering passes to Core._apply_iterate for
	// every splat: declared with no methods, so that spreading a tuple
	// (which never calls it) works; calling it is a MethodError until
	// Base's methods run
	must(vm.declareFunction(vm.Base, "iterate"))
	vm.Base.own("iterate").Exports = true

	// boot.jl:282-286: Typeof(x) = isa(x,Type) ? Type{x} : typeof(x), a
	// generic function of Core with one method; built here until boot.jl
	// itself is evaluated.
	vm.typeofFn = vm.newFunction(vm.Core, "Typeof")
	vm.setCore("Typeof", vm.typeofFn)
	sig, _ := core.Apply(core.AnyTupleType, core.AnyType)
	m, _ := vm.typeofFn.Table.Define(sig, vm.newWorld(), nil)
	m.Def = goMethod(func(vm *VM, args []any) (any, error) {
		x := args[0]
		if core.IsType(x) {
			return core.Apply(core.TypeType, x)
		}
		return vm.typeOf(x), nil
	})
}

// goMethod is the body of a method written in Go.
type goMethod func(vm *VM, args []any) (any, error)

func mustType(module, name string, super *core.DataType, abstract bool, fields []expr.Symbol, types []any) *core.DataType {
	t, err := core.NewType(module, name, super, abstract, false, fields, types)
	if err != nil {
		panic(err)
	}
	return t
}

// newFunction creates a generic function: a singleton type `typeof(f)`
// named #f under Function (jl_declare_const_gf, jl_new_generic_function).
func (vm *VM) newFunction(m *Module, name expr.Symbol) *Function {
	t := mustType(string(m.Name), "#"+string(name), core.FunctionType, false, nil, nil)
	fn := &Function{Name: name, Type: t, Table: &dispatch.Table{Name: string(name)}}
	vm.funcs[t] = fn
	return fn
}

// typeOf is jl_typeof.
func (vm *VM) typeOf(v any) any {
	switch x := v.(type) {
	case nil:
		return core.NothingType
	case int8:
		return core.Int8Type
	case int16:
		return core.Int16Type
	case int32:
		return core.Int32Type
	case uint8:
		return core.UInt8Type
	case uint16:
		return core.UInt16Type
	case uint32:
		return core.UInt32Type
	case uint64:
		return core.UInt64Type
	case float32:
		return core.Float32Type
	case float64:
		return core.Float64Type
	case string:
		return core.StringType
	case expr.Char:
		return core.CharType
	case *Tuple:
		return x.Type
	case *Struct:
		return x.Type
	case *Function:
		return x.Type
	case *Builtin:
		return x.Type
	case SVec:
		return core.SimpleVectorType
	case *Memory:
		return x.Type
	case *MemoryRef:
		return x.Type
	case *Pointer:
		return x.Type
	case *Intrinsic:
		return vm.intrinsicType
	case *Module:
		return core.ModuleType
	case *expr.Expr:
		return vm.tExpr
	case expr.QuoteNode:
		return vm.tQuoteNode
	case expr.LineNumberNode:
		return vm.tLineNumberNode
	case expr.GlobalRef:
		return vm.tGlobalRef
	case expr.SSAValue:
		return vm.tSSAValue
	case expr.SlotNumber:
		return vm.tSlotNumber
	case expr.Argument:
		return vm.tArgument
	case expr.GotoNode:
		return vm.tGotoNode
	case expr.GotoIfNot:
		return vm.tGotoIfNot
	case expr.EnterNode:
		return vm.tEnterNode
	case expr.ReturnNode, expr.Unreachable:
		return vm.tReturnNode
	case expr.NewvarNode:
		return vm.tNewvarNode
	case *expr.CodeInfo:
		return vm.tCodeInfo
	}
	if t := core.TypeOf(v); t != nil {
		return t
	}
	panic(fmt.Sprintf("interp: no Julia type for the Go value %T", v))
}
