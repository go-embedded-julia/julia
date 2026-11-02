package interp

import (
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/lower"
)

// lambdaCodeInfo is jl_new_code_info_from_ir (method.c:384) of a lambda
// built at run time: (lambda argnames (vinfo captured nssa sparams) (block
// stmts…) codelocs linetable), as lowering emits it for a closure's method
// added to a global function, with Core._expr. False for a malformed one.
func lambdaCodeInfo(lam *expr.Expr) (ci *expr.CodeInfo, ok bool) {
	if len(lam.Args) != 5 {
		return nil, false
	}
	argnames, ok1 := listOf(lam.Args[0])
	vinfo, ok2 := listOf(lam.Args[1])
	body, ok3 := lam.Args[2].(*expr.Expr)
	locs, ok4 := listOf(lam.Args[3])
	lines, ok5 := listOf(lam.Args[4])
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || body.Head != "block" || len(vinfo) < 3 || len(locs) != len(body.Args) || len(lines) == 0 {
		return nil, false
	}
	vars, ok1 := listOf(vinfo[0])
	nssa, ok2 := vinfo[2].(int64)
	if !ok1 || !ok2 {
		return nil, false
	}
	slots := make([]lower.Slot, len(vars))
	for i, v := range vars {
		vi, ok := listOf(v)
		if !ok || len(vi) != 3 {
			return nil, false
		}
		name, ok1 := vi[0].(expr.Symbol)
		flags, ok2 := vi[2].(int64)
		if !ok1 || !ok2 {
			return nil, false
		}
		slots[i] = lower.Slot{Name: name, Flags: flags}
	}
	codelocs := make([]int64, len(locs))
	for i, l := range locs {
		if codelocs[i], ok = l.(int64); !ok || codelocs[i] < 0 || codelocs[i] > int64(len(lines)) {
			return nil, false
		}
	}
	linetable := make([]lower.LineInfo, len(lines))
	for i, l := range lines {
		li, ok := l.(expr.LineInfoNode)
		if !ok || li.InlinedAt < 0 || int(li.InlinedAt) > len(lines) {
			return nil, false
		}
		linetable[i] = lower.LineInfo{File: li.File, Line: li.Line, Inlined: li.InlinedAt}
	}
	code := append(expr.Vector(nil), body.Args...)
	// the modules of the system image julia's would name here: the VM's
	// own, Core and Base
	inImage := func(m expr.Symbol) bool { return m == "Core" || m == "Base" }
	return lower.NewCodeInfo(len(argnames), slots, nssa, code, codelocs, linetable, inImage), true
}

// listOf is the elements of a Vector{Any} as lowering or Julia code made
// it: an expr.Vector, or an Array.
func listOf(v any) ([]any, bool) {
	if l, ok := v.(expr.Vector); ok {
		return l, true
	}
	_, dims, elems, ok := arrayOf(v)
	return elems, ok && len(dims) == 1
}
