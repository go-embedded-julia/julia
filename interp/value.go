package interp

import (
	"sync"
	"weak"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
)

// The values the interpreter handles are Go values. The constants lowered
// code carries keep the Go types of package expr (int64, float64, bool,
// string, expr.Symbol, nil for nothing …), and type objects those of package
// core; this file adds the objects that exist only at run time.

// undef marks a slot that holds no value (a C NULL in julia's locals).
var undef = &struct{ _ byte }{}

// Tuple is an instance of a concrete Tuple type.
type Tuple struct {
	Type  *core.DataType
	Elems []any
}

// Struct is an instance of a struct type the runtime created; a field that
// was never set holds undef.
type Struct struct {
	Type   *core.DataType
	Fields []any
	// exprOwner is the Expr whose args this Vector{Any} is (exprargs.go):
	// setting its ref or size points the Expr at the new elements. Weak, so
	// that the table of vectors does not keep the Expr alive.
	exprOwner weak.Pointer[expr.Expr]
}

// Function is a generic function: the single instance of its type, and the
// method table every call to it consults.
type Function struct {
	Name  expr.Symbol
	Type  *core.DataType
	Table *dispatch.Table
}

// Builtin is a function implemented in Go, a Core.Builtin: the single
// instance of its own type, typeof(f) <: Core.Builtin.
type Builtin struct {
	Name expr.Symbol
	Type *core.DataType
	Fn   func(vm *VM, args []any) (any, error)
}

// SVec is a Core.SimpleVector.
type SVec []any

// Intrinsic is a Core.IntrinsicFunction.
type Intrinsic struct {
	Name expr.Symbol
	Fn   func(vm *VM, args []any) (any, error)
}

// Module is a Julia module: its bindings, and the modules whose exported
// names it sees.
type Module struct {
	Name   expr.Symbol
	Parent *Module

	mu       sync.RWMutex
	bindings map[expr.Symbol]*Binding
	usings   []*Module
	// istopmod marks a module `(top x)` resolves in (jl_set_istopmod).
	istopmod bool
}

// Binding is a module's global variable. julia 1.12 partitions a binding by
// world (jl_binding_partition_t); this first version keeps the world the
// binding was declared in, before which it does not exist, and one value.
type Binding struct {
	Name    expr.Symbol
	World   uint64
	Const   bool
	Value   any // undef until assigned
	Exports bool
	// Alias is the binding an import refers to.
	Alias *Binding
	// Type is a global's declared type (`global x::T`, the partition's
	// restriction), nil for Any.
	Type any
	// implicit marks a binding only export or public made: julia's
	// implicit kind, which a read resolves through the module's usings
	// until the name is assigned or declared.
	implicit bool
}

// resolve follows an import to the binding it names.
func (b *Binding) resolve() *Binding {
	for b.Alias != nil {
		b = b.Alias
	}
	return b
}

func newModule(name expr.Symbol, parent *Module) *Module {
	return &Module{Name: name, Parent: parent, bindings: map[expr.Symbol]*Binding{}}
}

// own returns the binding m itself holds for name, nil when it has none.
func (m *Module) own(name expr.Symbol) *Binding {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.bindings[name]
}

// lookup returns the binding a global read of name in m finds in world w:
// m's own, else an exported one of a module m uses.
func (m *Module) lookup(name expr.Symbol, w uint64) *Binding {
	if b := m.own(name); b != nil && b.World <= w && !(b.implicit && b.Value == undef) {
		return b.resolve()
	}
	m.mu.RLock()
	usings := m.usings
	m.mu.RUnlock()
	for _, u := range usings {
		if b := u.own(name); b != nil && b.Exports && b.World <= w {
			return b.resolve()
		}
	}
	return nil
}

// declare makes name a binding of m in world w, when m does not have one.
func (m *Module) declare(name expr.Symbol, w uint64) *Binding {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b := m.bindings[name]; b != nil {
		return b
	}
	b := &Binding{Name: name, World: w, Value: undef}
	m.bindings[name] = b
	return b
}
