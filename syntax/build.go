// Ported from JuliaSyntax src/expr.jl, src/green_tree.jl and
// src/syntax_tree.jl (Julia 1.12.6, JuliaSyntax 29ec5226f7b1).
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

import (
	"strings"

	"github.com/go-embedded-julia/julia/expr"
)

// dotOp is the (head, value) tuple _internal_node_to_Expr stores in a
// one-argument `.` node, so that _fixup_Expr_children! can decide later
// whether to coalesce the dot with the operator.
type dotOp struct {
	head SyntaxHead
	val  any
}

// absent marks a try clause that is not there (Julia uses `false`, which is
// also a legitimate catch variable).
type absentT struct{}

var absent = absentT{}

func isExprN(v any, head expr.Symbol, n int) bool {
	e, ok := v.(*expr.Expr)
	return ok && e.Head == head && len(e.Args) == n
}

func reorderParameters(args *[]any, paramsPos int) {
	a := *args
	p := 0
	for i := len(a) - 1; i >= 0; i-- {
		if !expr.Is(a[i], "parameters") {
			break
		}
		p = i + 1
	}
	if p == 0 {
		return
	}
	// nest frankentuples parameters sections
	for i := len(a) - 1; i >= p; i-- {
		last := a[len(a)-1]
		a = a[:len(a)-1]
		pe := a[i-1].(*expr.Expr)
		pe.Args = append([]any{last}, pe.Args...)
	}
	// Move parameters to args[params_pos]
	last := a[len(a)-1]
	a = a[:len(a)-1]
	a = insertAt(a, paramsPos-1, last)
	*args = a
}

func insertAt(a []any, i int, v any) []any {
	a = append(a, nil)
	copy(a[i+1:], a[i:])
	a[i] = v
	return a
}

func stripParens(ex any) any {
	for {
		e, ok := ex.(*expr.Expr)
		if !ok || e.Head != "parens" {
			return ex
		}
		if len(e.Args) == 1 {
			ex = e.Args[0]
		} else {
			// Only for error cases
			return expr.New("block", e.Args...)
		}
	}
}

func lineNode(source *SourceFile, byteIndex int) expr.LineNumberNode {
	var file any
	if source.Filename != "" {
		file = expr.Symbol(source.Filename)
	}
	return expr.LineNumberNode{Line: source.Line(byteIndex), File: file}
}

func leafToExpr(source *SourceFile, txtbuf []byte, head SyntaxHead, first, last int) any {
	k := head.Kind
	if k == kMacroName && source.slice(first, last) == "." {
		return expr.Symbol("@__dot__")
	} else if isError(k) {
		if k == kError {
			return expr.New("error")
		}
		return expr.New("error", tokenErrorDescriptions[k]+": `"+source.slice(first, last)+"`")
	}
	val := parseJuliaLiteral(txtbuf, head, first, last)
	if li, ok := val.(largeInt); ok {
		// Ignore the values of large integers and convert them back to
		// symbolic/textural form for compatibility with the Expr
		// representation of these.
		str := strings.ReplaceAll(source.slice(first, last), "_", "")
		return expr.New("macrocall", expr.GlobalRef{Mod: "Core", Name: li.macro}, nil, str)
	}
	return val
}

// stringToExpr concatenates adjacent string chunks as done by the
// reference parser.
func stringToExpr(args []any) any {
	var args2 []any
	for i := 0; i < len(args); {
		if s, ok := args[i].(string); ok {
			if i+1 < len(args) {
				if _, ok := args[i+1].(string); ok {
					var b strings.Builder
					for i < len(args) {
						s2, ok := args[i].(string)
						if !ok {
							break
						}
						b.WriteString(s2)
						i++
					}
					args2 = append(args2, b.String())
					continue
				}
			}
			args2 = append(args2, s)
			i++
			continue
		}
		ex := args[i]
		if isExprN(ex, "parens", 1) {
			ex = stripParens(ex)
			if s, ok := ex.(string); ok {
				// Wrap interpolated literal strings in (string) so we can
				// distinguish them from the surrounding text (issue #38501)
				ex = expr.New("string", s)
			}
		}
		args2 = append(args2, ex)
		i++
	}
	if len(args2) == 1 {
		if s, ok := args2[0].(string); ok {
			return s
		}
	}
	return expr.New("string", args2...)
}

// symbolString is string(x) for the values that end up concatenated into
// symbol names.
func symbolString(v any) string {
	switch x := v.(type) {
	case expr.Symbol:
		return string(x)
	case string:
		return x
	}
	return expr.Serialize(v)
}

// fixupExprChildren is _fixup_Expr_children!: shared fixups for Expr
// children where the type of the parent node affects the child layout.
func fixupExprChildren(head SyntaxHead, args []any) {
	k := head.Kind
	eqToKwInCall := ((k == kCall || k == kDotcall) && head.isPrefixCall()) || k == kRef
	eqToKwInParams := k != kVect && k != kCurly && k != kBraces && k != kRef
	coalesceDot := k == kCall || k == kDotcall || k == kCurly || (k == kQuote && head.Flags == colonQuote)
	for i := range args {
		arg := args[i]
		wasParens := expr.Is(arg, "parens")
		arg = stripParens(arg)
		if expr.Is(arg, "=") && eqToKwInCall && i > 0 {
			arg = expr.New("kw", arg.(*expr.Expr).Args...)
		} else if e, ok := arg.(*expr.Expr); ok && k != kParens && e.Head == "." && len(e.Args) == 1 {
			if d, ok := e.Args[0].(dotOp); ok {
				if (!wasParens && coalesceDot && i == 0) || (k == kComparison && (i+1)%2 == 0) || isSyntacticOperator(d.head.Kind) {
					arg = expr.Symbol("." + symbolString(d.val))
				} else {
					arg = expr.New(".", d.val)
				}
			}
		} else if expr.Is(arg, "parameters") && eqToKwInParams {
			pargs := arg.(*expr.Expr).Args
			for j, pj := range pargs {
				if expr.Is(pj, "=") {
					pargs[j] = expr.New("kw", pj.(*expr.Expr).Args...)
				}
			}
		} else if k == kLet && i == 0 && expr.Is(arg, "block") {
			e := arg.(*expr.Expr)
			kept := e.Args[:0]
			for _, a := range e.Args {
				if _, ok := a.(expr.LineNumberNode); !ok {
					kept = append(kept, a)
				}
			}
			e.Args = kept
		}
		args[i] = arg
	}
}

// extractDoLambda removes the `do` block from the final position in a
// function/macro call arg list.
func extractDoLambda(args *[]any) *expr.Expr {
	a := *args
	if len(a) > 1 && expr.Is(a[len(a)-1], "do_lambda") {
		doEx := a[len(a)-1].(*expr.Expr)
		*args = a[:len(a)-1]
		return expr.New("->", doEx.Args...)
	}
	return nil
}

func appendIterspec(args []any, ex any) []any {
	if e, ok := ex.(*expr.Expr); ok && e.Head == "iteration" {
		for _, iter := range e.Args {
			args = append(args, expr.New("=", iter.(*expr.Expr).Args...))
		}
		return args
	}
	return append(args, ex)
}

func pushArg(v any, a any) {
	if e, ok := v.(*expr.Expr); ok {
		e.Args = append(e.Args, a)
	}
}

func pushFirstArg(v any, a any) {
	if e, ok := v.(*expr.Expr); ok {
		e.Args = append([]any{a}, e.Args...)
	}
}

// internalNodeToExpr converts an internal node of the parse tree to an Expr.
func internalNodeToExpr(source *SourceFile, first, last int, head SyntaxHead,
	childRanges [][2]int, childHeads []SyntaxHead, args []any) any {
	k := head.Kind
	if (k == kVar || k == kCharNode) && len(args) == 1 {
		return args[0]
	} else if k == kStringNode {
		return stringToExpr(args)
	}
	loc := lineNode(source, first)
	endloc := lineNode(source, last)
	if k == kCmdstring {
		return expr.New("macrocall", expr.GlobalRef{Mod: "Core", Name: "@cmd"}, loc, stringToExpr(args))
	}
	fixupExprChildren(head, args)

	headsym := expr.Symbol(head.headName())

	switch {
	case k == kQuestion:
		headsym = "if"
	case k == kOpEq && len(args) == 3:
		lhs, op, rhs := args[0], args[1], args[2]
		hs := symbolString(op) + "="
		if head.isDotted() {
			hs = "." + hs
		}
		headsym = expr.Symbol(hs)
		args = []any{lhs, rhs}
	case k == kMacrocall:
		if len(args) >= 2 {
			if a2, ok := args[1].(*expr.Expr); ok && a2.Head == "macrocall" && childHeads[0].Kind == kCmdMacroName {
				// Fix up for custom cmd macros like foo`x`
				args[1] = a2.Args[2]
			}
		}
		doLambda := extractDoLambda(&args)
		reorderParameters(&args, 2)
		args = insertAt(args, 1, loc)
		if doLambda != nil {
			return expr.New("do", expr.New(headsym, args...), doLambda)
		}
	case k == kBlock || (k == kToplevel && !head.Flags.has(toplevelSemicolonFlag)):
		if len(args) == 0 {
			args = append(args, loc)
		} else {
			out := make([]any, 2*len(args))
			for i := len(childRanges) - 1; i >= 0; i-- {
				out[2*i+1] = args[i]
				out[2*i] = lineNode(source, childRanges[i][0])
			}
			args = out
		}
		if k == kBlock && head.Flags.has(parensFlag) {
			args = args[1:]
		}
	case k == kDoc:
		headsym = "macrocall"
		args = append([]any{expr.GlobalRef{Mod: "Core", Name: "@doc"}, loc}, args...)
	case k == kDotcall || k == kCall:
		// Julia's standard `Expr` ASTs have children stored in a canonical
		// order which is often not always source order. We permute the
		// children here as necessary to get the canonical order.
		if head.isInfixOpCall() || head.isPostfixOpCall() {
			args[0], args[1] = args[1], args[0]
		}
		// Lower (call x ') to special ' head
		if head.isPostfixOpCall() && args[0] == expr.Symbol("'") {
			args = args[1:]
			headsym = "'"
		}
		doLambda := extractDoLambda(&args)
		// Move parameters blocks to args[2]
		reorderParameters(&args, 2)
		if headsym == "dotcall" {
			funcname := args[0]
			if head.isPrefixCall() {
				headsym = "."
				args = []any{funcname, expr.New("tuple", args[1:]...)}
			} else {
				// operator calls
				headsym = "call"
				if s, ok := funcname.(expr.Symbol); ok {
					args[0] = "." + s
				} // else funcname could be an Expr(:error), just propagate it
			}
		}
		if doLambda != nil {
			return expr.New("do", expr.New(headsym, args...), doLambda)
		}
	case k == kDot:
		if len(args) == 2 {
			a2 := args[1]
			_, isQN := a2.(expr.QuoteNode)
			if !expr.Is(a2, "quote") && !isQN {
				args[1] = expr.QuoteNode{Value: a2}
			}
		} else if len(args) == 1 {
			// Hack: Here we preserve the head of the operator to determine
			// whether we need to coalesce it with the dot into a single
			// symbol later on.
			args[0] = dotOp{childHeads[0], args[0]}
		}
	case k == kRef || k == kCurly:
		// Move parameters blocks to args[2]
		reorderParameters(&args, 2)
	case k == kFor:
		iters := appendIterspec(nil, args[0])
		if len(iters) == 1 {
			args[0] = iters[0]
		} else {
			args[0] = expr.New("block", iters...)
		}
		// Add extra line number node for the `end` of the block. This may
		// seem useless but it affects code coverage.
		pushArg(args[1], endloc)
	case k == kWhile:
		// Line number node for the `end` of the block as in `for` loops.
		pushArg(args[1], endloc)
	case k == kTuple || k == kVect || k == kBraces:
		// Move parameters blocks to args[1]
		reorderParameters(&args, 1)
	case k == kWhere:
		if len(args) == 2 {
			if a2, ok := args[1].(*expr.Expr); ok && a2.Head == "braces" {
				a2a := a2.Args
				reorderParameters(&a2a, 2)
				args = append([]any{args[0]}, a2a...)
			}
		}
	case k == kCatch:
		if childHeads[0].Kind == kPlaceholder {
			args[0] = false
		}
	case k == kTry:
		// Try children in source order:
		//   try_block catch_var catch_block else_block finally_block
		// Expr ordering:
		//   try_block catch_var catch_block [finally_block] [else_block]
		try := args[0]
		var catchVar, catch, elseB, finallyB any = false, false, absent, absent
		for _, a := range args[1:] {
			e, _ := a.(*expr.Expr)
			switch {
			case expr.Is(a, "catch"):
				catchVar, catch = e.Args[0], e.Args[1]
			case expr.Is(a, "else"):
				elseB = e.Args[0]
			case expr.Is(a, "finally"):
				finallyB = e.Args[0]
			case expr.Is(a, "error"):
				finallyB = expr.New("block", a) // Unclear where to put this but here will do?
			}
		}
		args = []any{try, catchVar, catch}
		if finallyB != absent || elseB != absent {
			if finallyB == absent {
				finallyB = false
			}
			args = append(args, finallyB)
			if elseB != absent {
				args = append(args, elseB)
			}
		}
	case k == kGenerator:
		// Reconstruct the nested Expr form for generator from our flatter
		// source-ordered `generator` format.
		gen := args[0]
		for j := len(args) - 1; j >= 1; j-- {
			g := expr.New("generator", gen)
			g.Args = appendIterspec(g.Args, args[j])
			gen = g
			if j < len(args)-1 {
				// Additional `for`s flatten the inner generator
				gen = expr.New("flatten", gen)
			}
		}
		return gen
	case k == kFilter:
		args = appendIterspec([]any{args[1]}, args[0])
	case k == kNrow || k == kNcat:
		// For lack of a better place, the dimension argument to nrow/ncat
		// is stored in the flags
		args = append([]any{int64(head.Flags.numeric())}, args...)
	case k == kTypedNcat:
		args = insertAt(args, 1, int64(head.Flags.numeric()))
	case k == kElseif:
		// Block for conditional's source location
		args[0] = expr.New("block", loc, args[0])
	case k == kArrow:
		if a1, ok := args[0].(*expr.Expr); ok && a1.Head == "tuple" {
			// TODO: This makes the Expr form objectively worse for the sake
			// of compatibility.
			if len(a1.Args) == 1 &&
				(!childHeads[0].Flags.has(parensFlag) || !childHeads[0].Flags.has(trailingCommaFlag)) &&
				!expr.Is(a1.Args[0], "parameters") {
				// `(a) -> c` is parsed without tuple on lhs in Expr form
				args[0] = a1.Args[0]
			} else if len(a1.Args) == 2 {
				if a11, ok := a1.Args[0].(*expr.Expr); ok && a11.Head == "parameters" && len(a11.Args) <= 1 && !expr.Is(a1.Args[1], "...") {
					// `(a; b=1) -> c`  parses args as `block` in Expr form :-(
					if len(a11.Args) == 0 {
						args[0] = expr.New("block", a1.Args[1])
					} else {
						a111 := a11.Args[0]
						assgn := a111
						if e, ok := a111.(*expr.Expr); ok && e.Head == "kw" {
							assgn = expr.New("=", e.Args...)
						}
						argloc := lineNode(source, childRanges[0][1])
						args[0] = expr.New("block", a1.Args[1], argloc, assgn)
					}
				}
			}
		}
		// Add function source location to rhs; add block if necessary
		if expr.Is(args[1], "block") {
			pushFirstArg(args[1], loc)
		} else {
			args[1] = expr.New("block", loc, args[1])
		}
	case k == kFunction:
		if len(args) > 1 {
			if head.Flags.has(shortFormFunctionFlag) {
				if !expr.Is(args[1], "block") && !expr.Is(args[0], "'") {
					args[1] = expr.New("block", args[1])
				}
				headsym = "="
			} else if a1, ok := args[0].(*expr.Expr); ok && a1.Head == "tuple" {
				// Convert to weird Expr forms for long-form anonymous
				// functions.
				// (function (tuple (... xs)) body) ==> (function (... xs) body)
				if len(a1.Args) == 1 && expr.Is(a1.Args[0], "...") {
					args[0] = a1.Args[0]
				}
			}
			// Add location if not ErrorVal or unwrapped block
			if expr.Is(args[1], "block") {
				pushFirstArg(args[1], loc)
			}
		}
	case k == kMacro:
		if len(args) > 1 {
			pushFirstArg(args[1], loc)
		}
	case k == kModule:
		args = append([]any{!head.Flags.has(bareModuleFlag)}, args...)
		pushFirstArg(args[2], loc)
	case k == kInert:
		return expr.QuoteNode{Value: args[0]}
	case k == kQuote && len(args) == 1:
		switch args[0].(type) {
		case *expr.Expr, expr.QuoteNode, bool:
		default:
			// Flisp parser does an optimization here: simple values are
			// stored as inert QuoteNode rather than in `Expr(:quote)`
			// quasiquote
			return expr.QuoteNode{Value: args[0]}
		}
	case k == kDo:
		// Temporary head which is picked up by _extract_do_lambda
		headsym = "do_lambda"
	case k == kLet:
		if a1, ok := args[0].(*expr.Expr); ok && a1.Head == "block" && len(a1.Args) == 1 {
			// Ugly logic to strip the Expr(:block) in certain cases for
			// compatibility
			a := a1.Args[0]
			if _, isSym := a.(expr.Symbol); isSym || expr.Is(a, "=") || expr.Is(a, "::") {
				args[0] = a
			}
		}
	case k == kLocal || k == kGlobal:
		if len(args) == 1 {
			if a1, ok := args[0].(*expr.Expr); ok {
				if a1.Head == "const" {
					// Normalize `local const` to `const local`
					args[0] = expr.New(headsym, a1.Args...)
					headsym = "const"
				} else if a1.Head == "tuple" {
					// Normalize `global (x, y)` to `global x, y`
					args = a1.Args
				}
			}
		}
	case k == kReturn && len(args) == 0:
		args = append(args, nil)
	case k == kJuxtapose:
		headsym = "call"
		args = append([]any{expr.Symbol("*")}, args...)
	case k == kStruct:
		if body, ok := args[1].(*expr.Expr); ok {
			fields := expr.New("block")
			for _, field := range body.Args {
				if f, ok := field.(*expr.Expr); ok && f.Head == "macrocall" && len(f.Args) >= 4 &&
					f.Args[0] == (expr.GlobalRef{Mod: "Core", Name: "@doc"}) {
					// @doc macro calls don't occur within structs, in Expr
					// form.
					fields.Args = append(fields.Args, f.Args[2], f.Args[3])
				} else {
					fields.Args = append(fields.Args, field)
				}
			}
			args[1] = fields
		}
		args = append([]any{head.Flags.has(mutableFlag)}, args...)
	case k == kImportpath:
		headsym = "."
		for i, ai := range args {
			if q, ok := ai.(expr.QuoteNode); ok {
				// Permit nonsense additional quoting such as
				// import A.(:b).:c
				args[i] = q.Value
			}
		}
	case k == kWrapper:
		// This should only happen for errors wrapped next to what should
		// have been single statements or atoms - represent these as blocks.
		headsym = "block"
	}
	return expr.New(headsym, args...)
}

// exprEntry is the stack entry of the Expr build.
type exprEntry struct {
	first, last int
	head        SyntaxHead
	ex          any
}

// buildExpr is build_tree(Expr, stream; filename, first_line).
func (s *parseStream) buildExpr(filename string, firstLine int) any {
	source := s.sourceFile(filename, firstLine)
	txtbuf := s.textbuf
	// Scratch arrays reused from node to node, as expr.jl reuses its
	// childranges and childheads buffers; args is retained by the Expr.
	var ranges [][2]int
	var heads []SyntaxHead
	// Identifiers repeat: each distinct name is converted (and boxed as an
	// Expr argument) once per parse.
	symbols := map[string]any{}
	entry := buildTree(s, func(h SyntaxHead, first, last int, children []exprEntry, leaf bool) (exprEntry, bool) {
		if h.isTrivia() && !isError(h.Kind) {
			return exprEntry{}, false
		}
		if leaf {
			if h.Kind == kIdentifier && !h.Flags.has(rawStringFlag) {
				text := txtbuf[first-1 : last]
				v, ok := symbols[string(text)]
				if !ok {
					v = leafToExpr(source, txtbuf, h, first, last)
					symbols[string(text)] = v
				}
				return exprEntry{first, last, h, v}, true
			}
			return exprEntry{first, last, h, leafToExpr(source, txtbuf, h, first, last)}, true
		}
		ranges = ranges[:0]
		heads = heads[:0]
		args := make([]any, len(children))
		for i, c := range children {
			ranges = append(ranges, [2]int{c.first, c.last})
			heads = append(heads, c.head)
			args[i] = c.ex
		}
		return exprEntry{first, last, h, internalNodeToExpr(source, first, last, h, ranges, heads, args)}, true
	})
	args := []any{entry.ex}
	fixupExprChildren(SyntaxHead{kNone, emptyFlags}, args)
	return args[0]
}
