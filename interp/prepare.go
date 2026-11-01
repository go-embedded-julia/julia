package interp

import (
	"errors"
	"fmt"

	"github.com/go-embedded-julia/julia/expr"
)

// ErrUnsupported is wrapped by the errors of IR the interpreter does not run
// yet (as opposed to julia exceptions, which are *Thrown).
var ErrUnsupported = errors.New("interp: not ported yet")

// ErrInternal is wrapped by the error of a broken invariant of the
// interpreter, reported by Session.Eval instead of a panic.
var ErrInternal = errors.New("interp: internal error")

// ErrStepLimit is the error of an evaluation that exceeded VM.MaxSteps.
var ErrStepLimit = errors.New("interp: step limit exceeded")

// ErrMemoryLimit is the error of a memorynew larger than VM.MaxMemoryElems.
var ErrMemoryLimit = errors.New("interp: memory limit exceeded")

// code is a CodeInfo prepared for the interpreter: statements as a Go sum
// type, with every SSAValue, slot and label checked against the counts once,
// so that the loop of eval.go does not check them again
// (bib/julia-runtime.md §2.2).
type code struct {
	src       *expr.CodeInfo
	nslots    int
	nssa      int
	slotNames []expr.Symbol
	stmts     []stmt
	toplevel  bool
}

// A stmt is one statement of eval_body (interpreter.c:470-712).
type stmt interface{}

type (
	sGoto      struct{ to int } // 0-based; out of range is caught by the loop
	sGotoIfNot struct {
		cond op
		to   int
	}
	sReturn struct{ v op }
	sEnter  struct {
		catch    int // 0-based, -1 without a catch
		scope    op
		hasScope bool
	}
	sAssign struct {
		slot int
		rhs  op
	}
	sLeave      struct{ n int } // handlers to pop, counted once (interpreter.c:596-606)
	sPopExc     struct{ state op }
	sNewvar     struct{ slot int }
	sValue      struct{ v op } // eval_stmt_value: the value becomes the SSA value
	sMethodDef  struct{ f, atypes, meth op }
	sGlobalDecl struct {
		ref expr.GlobalRef
		val op // nil without a value
	}
	sConst struct {
		ref expr.GlobalRef
		val op
	}
	sLatestWorld struct{}
	sToplevel    struct{ e *expr.Expr } // import, using, export, module …: toplevel.go
	sNop         struct{}               // line numbers and module-level :meta at top level
	sRaise       struct{ msg string }   // a statement julia rejects when it is reached
)

// An op is a value expression of eval_value (interpreter.c:198-364).
type op interface{}

type (
	oSSA  int // 0-based
	oSlot struct {
		i    int // 0-based
		name expr.Symbol
	}
	oConst  struct{ v any }
	oGlobal struct {
		mod  expr.Symbol
		name expr.Symbol
	}
	oSymbol          struct{ name expr.Symbol } // a bare global of the frame's module
	oCall            struct{ args []op }
	oNew             struct{ args []op }
	oIsdefSlot       struct{ i int }
	oIsdefSparam     struct{ n int }
	oThrowUndefIfNot struct {
		name expr.Symbol
		cond op
	}
	oSparam       struct{ n int }
	oTheException struct{}
	oCopyast      struct{ v op }
	oMethodDecl   struct {
		mod  expr.Symbol // "" for the frame's module
		name any
	}
	oRaise       struct{ msg string }  // jl_error when evaluated
	oUnsupported struct{ what string } // a form not ported yet
)

func (c *code) ssa(id int) op {
	if id < 1 || id > c.nssa {
		return oRaise{"access to invalid SSAValue"}
	}
	return oSSA(id - 1)
}

func (c *code) slot(n int) op {
	if n < 1 || n > c.nslots {
		return oRaise{"access to invalid slot number"}
	}
	return oSlot{n - 1, c.slotNames[n-1]}
}

// prepare converts src. toplevel is whether it is a thunk run by the top
// level (jl_interpret_toplevel_thunk) rather than a method body.
func prepare(src *expr.CodeInfo, toplevel bool) (*code, error) {
	c := &code{src: src, nslots: len(src.SlotFlags), toplevel: toplevel}
	switch n := src.SSAValueTypes.(type) {
	case int64:
		c.nssa = int(n)
	case expr.Vector:
		c.nssa = len(n)
	default:
		return nil, fmt.Errorf("%w: CodeInfo.ssavaluetypes of Go type %T", ErrUnsupported, n)
	}
	if len(src.SlotNames) != c.nslots {
		return nil, fmt.Errorf("interp: %d slot names for %d slot flags", len(src.SlotNames), c.nslots)
	}
	for _, s := range src.SlotNames {
		name, _ := s.(expr.Symbol)
		c.slotNames = append(c.slotNames, name)
	}
	if len(src.Code) > c.nssa {
		// eval_stmt_value stores statement ip at nslots+ip: a statement
		// beyond the SSA count would write outside the frame
		return nil, fmt.Errorf("interp: %d statements for %d SSA values", len(src.Code), c.nssa)
	}
	c.stmts = make([]stmt, len(src.Code))
	for i, s := range src.Code {
		c.stmts[i] = c.prepStmt(s)
	}
	return c, nil
}

func (c *code) prepStmt(s any) stmt {
	switch x := s.(type) {
	case expr.GotoNode:
		return sGoto{int(x) - 1}
	case expr.GotoIfNot:
		return sGotoIfNot{c.prepValue(x.Cond), x.Dest - 1}
	case expr.ReturnNode:
		return sReturn{c.prepValue(x.Val)}
	case expr.Unreachable:
		return sRaise{"reached code declared unreachable"}
	case expr.EnterNode:
		e := sEnter{catch: x.Catch - 1, hasScope: x.HasScope}
		if x.HasScope {
			e.scope = c.prepValue(x.Scope)
		}
		return e
	case expr.NewvarNode:
		if n, ok := x.Slot.(expr.SlotNumber); ok && int(n) >= 1 && int(n) <= c.nslots {
			return sNewvar{int(n) - 1}
		}
		return sRaise{"access to invalid slot number"}
	case expr.LineNumberNode:
		if c.toplevel {
			return sNop{}
		}
	case *expr.Expr:
		return c.prepExprStmt(x)
	}
	return sValue{c.prepValue(s)}
}

func (c *code) prepExprStmt(x *expr.Expr) stmt {
	switch x.Head {
	case "=":
		if len(x.Args) != 2 {
			return sRaise{"Invalid IR: Assignment LHS not a Slot"}
		}
		lhs, ok := x.Args[0].(expr.SlotNumber)
		if !ok {
			return sRaise{"Invalid IR: Assignment LHS not a Slot"}
		}
		if int(lhs) < 1 || int(lhs) > c.nslots {
			// julia only asserts this (interpreter.c:584): undefined in a
			// release build, an error here
			return sRaise{"access to invalid slot number"}
		}
		return sAssign{int(lhs) - 1, c.prepValue(x.Args[1])}
	case "leave":
		n := 0
		for _, a := range x.Args {
			if a == nil {
				continue
			}
			id, ok := a.(expr.SSAValue)
			if !ok || int(id) < 1 || int(id) > len(c.src.Code) {
				return sRaise{"Invalid IR: :leave argument not an SSAValue of an EnterNode"}
			}
			if c.src.Code[int(id)-1] == nil {
				continue
			}
			n++
		}
		return sLeave{n}
	case "pop_exception":
		if len(x.Args) != 1 {
			return sRaise{"Invalid IR: :pop_exception takes one argument"}
		}
		return sPopExc{c.prepValue(x.Args[0])}
	}
	if c.toplevel {
		switch x.Head {
		case "method":
			if len(x.Args) > 1 {
				if len(x.Args) != 3 {
					return sValue{oUnsupported{"a :method form with " + fmt.Sprint(len(x.Args)) + " arguments"}}
				}
				return sMethodDef{c.prepValue(x.Args[0]), c.prepValue(x.Args[1]), c.prepValue(x.Args[2])}
			}
		case "globaldecl":
			ref, ok := globalRefArg(x.Args)
			if !ok {
				return sValue{oUnsupported{":globaldecl of a non-GlobalRef"}}
			}
			d := sGlobalDecl{ref: ref}
			if len(x.Args) >= 2 {
				d.val = c.prepValue(x.Args[1])
			}
			return d
		case "const":
			ref, ok := globalRefArg(x.Args)
			if !ok || len(x.Args) != 2 {
				return sValue{oUnsupported{":const without a GlobalRef and a value"}}
			}
			return sConst{ref, c.prepValue(x.Args[1])}
		case "latestworld":
			return sLatestWorld{}
		case "toplevel", "import", "using", "export", "public", "module", "thunk", "global", "struct", "abstract", "primitive":
			return sToplevel{x}
		case "meta":
			return sNop{}
		}
	}
	return sValue{c.prepValue(x)}
}

func globalRefArg(args []any) (expr.GlobalRef, bool) {
	if len(args) == 0 {
		return expr.GlobalRef{}, false
	}
	r, ok := args[0].(expr.GlobalRef)
	return r, ok
}

func (c *code) prepValue(v any) op {
	switch x := v.(type) {
	case expr.SSAValue:
		return c.ssa(int(x))
	case expr.SlotNumber:
		return c.slot(int(x))
	case expr.Argument:
		return c.slot(int(x))
	case expr.QuoteNode:
		return oConst{x.Value}
	case expr.GlobalRef:
		return oGlobal{x.Mod, x.Name}
	case expr.Symbol:
		return oSymbol{x}
	case *expr.Expr:
		return c.prepExpr(x)
	}
	return oConst{v}
}

func (c *code) prepArgs(args []any) []op {
	out := make([]op, len(args))
	for i, a := range args {
		out[i] = c.prepValue(a)
	}
	return out
}

func (c *code) prepExpr(x *expr.Expr) op {
	switch x.Head {
	case "call":
		if len(x.Args) == 0 {
			return oRaise{"Invalid IR: :call without a function"}
		}
		return oCall{c.prepArgs(x.Args)}
	case "invoke_modify":
		if len(x.Args) < 2 {
			return oRaise{"Invalid IR: :invoke_modify without a function"}
		}
		return oCall{c.prepArgs(x.Args[1:])}
	case "new":
		if len(x.Args) == 0 {
			return oRaise{"Invalid IR: :new without a type"}
		}
		return oNew{c.prepArgs(x.Args)}
	case "isdefined":
		if len(x.Args) == 1 {
			switch a := x.Args[0].(type) {
			case expr.SlotNumber, expr.Argument:
				n := slotNumber(a)
				if n < 1 || n > c.nslots {
					return oRaise{"access to invalid slot number"}
				}
				return oIsdefSlot{n - 1}
			case expr.GlobalRef, expr.Symbol:
				return oRaise{"[Internal Error]: :isdefined on globalref should use `isdefinedglobal`"}
			case *expr.Expr:
				if a.Head == "static_parameter" && len(a.Args) == 1 {
					if n, ok := a.Args[0].(int64); ok && n > 0 {
						return oIsdefSparam{int(n)}
					}
				}
			}
		}
		return oRaise{"Invalid IR: malformed :isdefined"}
	case "throw_undef_if_not":
		if len(x.Args) == 2 {
			if name, ok := x.Args[0].(expr.Symbol); ok {
				return oThrowUndefIfNot{name, c.prepValue(x.Args[1])}
			}
		}
		return oRaise{"Invalid IR: malformed :throw_undef_if_not"}
	case "static_parameter":
		if len(x.Args) == 1 {
			if n, ok := x.Args[0].(int64); ok && n > 0 {
				return oSparam{int(n)}
			}
		}
		return oRaise{"Invalid IR: malformed :static_parameter"}
	case "copyast":
		if len(x.Args) == 1 {
			return oCopyast{c.prepValue(x.Args[0])}
		}
	case "the_exception":
		return oTheException{}
	case "boundscheck":
		return oConst{true}
	case "meta", "coverageeffect", "inbounds", "loopinfo", "aliasscope", "popaliasscope",
		"inline", "noinline", "gc_preserve_begin", "gc_preserve_end":
		return oConst{nil}
	case "method":
		if len(x.Args) == 1 {
			switch f := x.Args[0].(type) {
			case expr.GlobalRef:
				return oMethodDecl{f.Mod, f.Name}
			default:
				return oMethodDecl{"", f}
			}
		}
	case "foreigncall":
		return c.prepForeign(x)
	case "cfunction":
		return oRaise{"`cfunction` requires the compiler"}
	case "invoke", "splatnew", "new_opaque_closure":
		return oUnsupported{":" + string(x.Head)}
	}
	return oRaise{"unsupported or misplaced expression " + string(x.Head)}
}

func slotNumber(v any) int {
	switch x := v.(type) {
	case expr.SlotNumber:
		return int(x)
	case expr.Argument:
		return int(x)
	}
	return 0
}
