package expr

// The values of Julia's lowered form (doc/src/devdocs/ast.md, "Lowered form"
// in julia 1.12.6), and the values of the reference julia that the Go side
// does not model yet but must carry through unchanged.
//
//	Julia                    Go
//	Core.SSAValue            SSAValue
//	Core.SlotNumber          SlotNumber
//	Core.Argument            Argument
//	Core.GotoNode            GotoNode
//	Core.GotoIfNot           GotoIfNot
//	Core.ReturnNode          ReturnNode, or Unreachable when it has no value
//	Core.NewvarNode          NewvarNode
//	Core.EnterNode           EnterNode
//	Module                   Module (its fullname)
//	Core.CodeInfo            *CodeInfo
//	Core.DebugInfo           *DebugInfo
//	Vector, SimpleVector     Vector (inside CodeInfo and DebugInfo only)
//	Int128, UInt128          Tagged (type name and decimal text)
//	any other value          Opaque (type and repr, as julia prints them)

// SSAValue is the value of statement Id (1-based) of the enclosing CodeInfo.
type SSAValue int

// SlotNumber is local variable Id (1-based) of the enclosing CodeInfo.
type SlotNumber int

// Argument is argument N of a method (1-based).
type Argument int

// GotoNode jumps to statement Label.
type GotoNode int

// GotoIfNot jumps to statement Dest when Cond is false.
type GotoIfNot struct {
	Cond any
	Dest int
}

// ReturnNode returns Val.
type ReturnNode struct {
	Val any
}

// Unreachable is a ReturnNode without a value: control never reaches it.
type Unreachable struct{}

// NewvarNode marks the start of the scope of Slot (a SlotNumber).
type NewvarNode struct {
	Slot any
}

// EnterNode enters a try block whose catch starts at statement Catch. Scope
// is the optional scope value (HasScope reports whether it is present).
type EnterNode struct {
	Catch    int
	Scope    any
	HasScope bool
}

// Module is a module value, named by its fullname (e.g. Main, L).
type Module struct {
	Path []Symbol
}

// Tagged is a scalar of a Julia type that has no Go counterpart here, kept
// as its type name and the text julia prints for it ({Int128 170141…}).
type Tagged struct {
	Type, Text string
}

// Opaque is any other Julia value: macro expansion can splice functions,
// types and other objects into an Expr, which lowering passes through. It is
// kept as julia's type name and repr.
type Opaque struct {
	Type, Repr string
}

// Vector is a Julia vector inside a CodeInfo or a DebugInfo.
type Vector []any

// CodeInfo is Core.CodeInfo: the lowered code of a thunk or a method. Its
// fields are those of julia 1.12.6, serialised in declaration order.
type CodeInfo struct {
	Code                 Vector
	DebugInfo            any // *DebugInfo
	SSAValueTypes        any // the statement count, as lowering leaves it
	SSAFlags             Vector
	SlotNames            Vector
	SlotFlags            Vector
	SlotTypes            any
	RetType              any
	Parent               any
	Edges                any
	MinWorld, MaxWorld   any
	MethodForInferenceLH any
	NArgs                any
	PropagateInbounds    any
	HasFcall             any
	HasImageGlobalref    any
	NoSpecializeInfer    any
	IsVA                 any
	Inlining             any
	Constprop            any
	Purity               any
	InliningCost         any
}

// codeInfoFields is the number of fields of julia 1.12.6's CodeInfo.
const codeInfoFields = 23

// Values returns the fields in julia's declaration order.
func (c *CodeInfo) Values() []any {
	return []any{
		c.Code, c.DebugInfo, c.SSAValueTypes, c.SSAFlags, c.SlotNames, c.SlotFlags,
		c.SlotTypes, c.RetType, c.Parent, c.Edges, c.MinWorld, c.MaxWorld,
		c.MethodForInferenceLH, c.NArgs, c.PropagateInbounds, c.HasFcall,
		c.HasImageGlobalref, c.NoSpecializeInfer, c.IsVA, c.Inlining, c.Constprop,
		c.Purity, c.InliningCost,
	}
}

// CodeInfoFrom builds a CodeInfo from its fields in declaration order (as
// Values returns them); the four vector fields must be Vectors.
func CodeInfoFrom(v []any) (*CodeInfo, bool) {
	if len(v) != codeInfoFields {
		return nil, false
	}
	code, ok0 := v[0].(Vector)
	ssaflags, ok3 := v[3].(Vector)
	names, ok4 := v[4].(Vector)
	flags, ok5 := v[5].(Vector)
	if !ok0 || !ok3 || !ok4 || !ok5 {
		return nil, false
	}
	return &CodeInfo{
		Code: code, DebugInfo: v[1], SSAValueTypes: v[2], SSAFlags: ssaflags,
		SlotNames: names, SlotFlags: flags, SlotTypes: v[6], RetType: v[7],
		Parent: v[8], Edges: v[9], MinWorld: v[10], MaxWorld: v[11],
		MethodForInferenceLH: v[12], NArgs: v[13], PropagateInbounds: v[14],
		HasFcall: v[15], HasImageGlobalref: v[16], NoSpecializeInfer: v[17],
		IsVA: v[18], Inlining: v[19], Constprop: v[20], Purity: v[21],
		InliningCost: v[22],
	}, true
}

// DebugInfo is Core.DebugInfo: the line table of a CodeInfo. Codelocs is
// julia's compressed encoding, kept byte for byte.
type DebugInfo struct {
	Def       any
	Linetable any
	Edges     Vector
	Codelocs  string
}
