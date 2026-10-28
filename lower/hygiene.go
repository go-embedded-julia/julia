package lower

// Port of the hygiene half of src/macroexpand.scm (julia 1.12.6):
// julia-expand-macroscope resolves the (hygienic-scope form module lno)
// wrappers macro expansion leaves around a macro's result. Inside one, the
// names the expansion binds become gensyms (#12#x), the free ones become
// (globalref module x), and (escape e) goes back to the caller's scope.
// Meta.lower reaches this code when its input still holds such wrappers,
// as the arguments of a macro-made Expr(:toplevel) do.
//
// The binding forms are recognised, as in the Scheme code, by the pattern
// sets vars-introduced-by-patterns and keywords-introduced-by-patterns
// (match.scm: pattern-expand1), written out here as Go functions.

import "runtime"

// envEntry is one (name . replacement) pair of an expansion environment.
type envEntry struct{ key, val any }

// asEntry is the pair p of a varlist or of pair-with-gensyms as an
// environment entry: a dotted pair, or a list (car . cdr).
func asEntry(p any) envEntry {
	if e, ok := p.(envEntry); ok {
		return e
	}
	return envEntry{car(p), cdr(p)}
}

func isPairOrEntry(x any) bool {
	_, ok := x.(envEntry)
	return ok || isPair(x)
}

func envAssq(k any, env []envEntry) (envEntry, bool) {
	for _, e := range env {
		if eq(e.key, k) {
			return e, true
		}
	}
	return envEntry{}, false
}

// hygieneScope is one element of parent-scope: (env m lno).
type hygieneScope struct {
	env []envEntry
	m   any
	lno []any
}

// expandMacroscope is julia-expand-macroscope.
func (lw *lowerer) expandMacroscope(e any) any {
	e = lw.expandQuotes(e)
	if !hasHygienicScope(e) {
		// rename-symbolic-labels and julia-expand-macroscopes- are the
		// identity on a form without a hygienic scope
		return e
	}
	return lw.expandMacroscopes(lw.renameSymbolicLabels(e, map[any]any{}, nil))
}

// hasHygienicScope is whether rename-symbolic-labels- would reach a
// hygienic-scope (it does not enter quoted? forms).
func hasHygienicScope(e any) bool {
	l, ok := e.([]any)
	if !ok || len(l) == 0 || quoted(e) {
		return false
	}
	if l[0] == sym("hygienic-scope") {
		return true
	}
	for _, x := range l[1:] {
		if hasHygienicScope(x) {
			return true
		}
	}
	return false
}

// labelScope is the parent-scope of rename-symbolic-labels-: (relabels parent).
type labelScope struct {
	relabels map[any]any
	parent   *labelScope
}

// renameSymbolicLabels is rename-symbolic-labels-.
func (lw *lowerer) renameSymbolicLabels(e any, relabels map[any]any, parent *labelScope) any {
	switch {
	case !isPair(e) || quoted(e):
		return e
	case car(e) == sym("hygienic-scope"):
		ps := &labelScope{relabels, parent}
		return listStar(car(e), lw.renameSymbolicLabels(cadr(e), map[any]any{}, ps), cddr(e))
	case car(e) == sym("escape") && parent != nil:
		return list(car(e), lw.renameSymbolicLabels(cadr(e), parent.relabels, parent.parent))
	case car(e) == sym("symbolicgoto") || car(e) == sym("symboliclabel"):
		s := cadr(e)
		var have any = s
		if parent != nil && isSymbol(s) {
			v, ok := relabels[s]
			if !ok {
				v = false
			}
			have = v
		}
		newLabel := have
		if !truthy(have) {
			newLabel = lw.st.gensyms.namedGensy(s)
			relabels[s] = newLabel
		}
		return list(car(e), newLabel)
	}
	return cons(car(e), mapL(func(x any) any { return lw.renameSymbolicLabels(x, relabels, parent) }, cdr(e)))
}

// expandMacroscopes is julia-expand-macroscopes-.
func (lw *lowerer) expandMacroscopes(e any) any {
	switch {
	case !isPair(e):
		return e
	case inList(car(e), sInert, sym("module"), sym("toplevel")):
		return e
	case car(e) == sym("hygienic-scope"):
		// resolve-expansion-vars
		return lw.resolveWithNewEnv(cadr(e), nil, caddr(e), cdddr(e), nil, false, true)
	}
	return mapL(lw.expandMacroscopes, asList(e))
}

// resolveWithNewEnv is resolve-expansion-vars-with-new-env.
func (lw *lowerer) resolveWithNewEnv(x any, env []envEntry, m any, lno []any, ps []hygieneScope, inarg, outermost bool) any {
	if !is(x, "let") {
		env = lw.newExpansionEnvFor(x, env, outermost)
	}
	return lw.resolveVars(x, env, m, lno, ps, inarg)
}

// newExpansionEnvFor is new-expansion-env-for.
func (lw *lowerer) newExpansionEnvFor(x any, env []envEntry, outermost bool) []envEntry {
	vlist, isVarlist := lw.introducedVars(x)
	if !isPair(x) || (!outermost && !isVarlist) {
		return env
	}
	globals := findDeclaredVarsInExpansion(x, sym("global"), true)
	var pairs, vnames []any
	for _, v := range vlist {
		if isPairOrEntry(v) {
			pairs = append(pairs, v)
		} else {
			vnames = append(vnames, v)
		}
	}
	v := diff(deleteDuplicates(appendL(findDeclaredVarsInExpansion(x, sym("local"), true),
		findAssignedVarsInExpansion(x, true), vnames)), globals)
	var out []envEntry
	for _, p := range pairs {
		out = append(out, asEntry(p))
	}
	// pair-with-gensyms names every variable, then those the environment
	// already has are dropped
	withGensyms := make([]envEntry, len(v))
	for i, s := range v {
		if isPairOrEntry(s) {
			withGensyms[i] = asEntry(s)
		} else {
			withGensyms[i] = envEntry{s, lw.namedGensyAny(s)}
		}
	}
	for _, p := range withGensyms {
		if _, ok := envAssq(p.key, env); !ok {
			out = append(out, p)
		}
	}
	for _, k := range lw.keywordsIntroducedBy(x) {
		out = append(out, envEntry{k, k})
	}
	return append(out, env...)
}

// namedGensyAny is named-gensy of any atom, () included ((string '()) is "()").
func (lw *lowerer) namedGensyAny(s any) sym {
	if isNull(s) {
		return lw.st.gensyms.namedGensy(sym("()"))
	}
	return lw.st.gensyms.namedGensy(s)
}

// resolveVars is resolve-expansion-vars-.
func (lw *lowerer) resolveVars(e any, env []envEntry, m any, lno []any, ps []hygieneScope, inarg bool) any {
	if s, ok := e.(sym); ok {
		if inList(s, sym("begin"), sym("end"), sym("ccall"), sym("cglobal")) || underscoreSymbol(s) {
			return e
		}
		if a, ok := envAssq(s, env); ok {
			return a.val
		}
		if m != nil && truthy(m) {
			return list(sym("globalref"), m, s)
		}
		return e
	}
	if !isPair(e) || quoted(e) {
		return e
	}
	sub := func(x any) any { return lw.resolveWithNewEnv(x, env, m, lno, ps, inarg, false) }
	same := func(x any) any { return lw.resolveVars(x, env, m, lno, ps, inarg) }
	switch h := car(e); h {
	case sym("ssavalue"), sym("macrocall"), sym("symboliclabel"), sym("symbolicgoto"):
		return e
	case sym("escape"):
		if len(ps) == 0 {
			return lw.expandMacroscopes(cadr(e))
		}
		sc := ps[0]
		return lw.resolveWithNewEnv(cadr(e), sc.env, sc.m, lno, ps[1:], inarg, false)
	case sym("global"):
		return cons(h, mapL(func(arg any) any {
			if assignment(arg) {
				return list(sAssign, lw.unescapeGlobalLHS(cadr(arg), env, m, lno, ps, inarg), sub(caddr(arg)))
			}
			return lw.unescapeGlobalLHS(arg, env, m, lno, ps, inarg)
		}, cdr(e)))
	case sym("toplevel"):
		// re-wrap Expr(:toplevel) in the current hygienic-scope(s)
		return cons(h, mapL(func(arg any) any {
			if justLine(arg) {
				return arg
			}
			wrapped := listStar(sym("hygienic-scope"), arg, m, lno)
			for _, sc := range ps {
				wrapped = listStar(sym("hygienic-scope"), wrapped, sc.m, sc.lno)
			}
			return wrapped
		}, cdr(e)))
	case sym("using"), sym("import"), sym("export"), sym("meta"), sLine, sym("inbounds"),
		sym("boundscheck"), sym("loopinfo"), sym("inline"), sym("noinline"), sym("purity"):
		return mapL(unescape, asList(e))
	case sym("struct"):
		return list(h, cadr(e), same(caddr(e)), mapL(func(x any) any {
			return lw.resolveStructField(x, env, m, lno, ps, inarg)
		}, asList(cadddr(e))))
	case sym("parameters"):
		return cons(h, mapL(func(x any) any {
			if ux := unescape(x); !inarg && isSymbol(ux) {
				x = list(sym("kw"), ux, x)
			}
			return lw.resolveVars(x, env, m, lno, ps, false)
		}, cdr(e)))
	case sym("->"):
		return list(h, lw.resolveInLHS(tupleWrapArrowSig(cadr(e)), env, m, lno, ps, inarg), sub(caddr(e)))
	case sAssign, sym("function"):
		return listStar(h, lw.resolveInLHS(cadr(e), env, m, lno, ps, inarg), mapL(sub, cddr(e)))
	case sym("kw"):
		return lw.resolveKw(e, env, m, lno, ps, inarg)
	case sym("let"):
		newenv := lw.newExpansionEnvFor(e, env, false)
		body := lw.resolveVars(caddr(e), newenv, m, lno, ps, inarg)
		binds := mapL(func(b any) any { return lw.resolveLetlikeAssign(b, env, newenv, m, lno, ps, inarg) }, letBinds(e))
		return list(h, cons(sBlock, binds), body)
	case sym("for"):
		newenv := lw.newExpansionEnvFor(e, env, false)
		body := lw.resolveVars(caddr(e), newenv, m, lno, ps, inarg)
		ranges := mapL(func(r any) any { return lw.resolveLetlikeAssign(r, env, newenv, m, lno, ps, inarg) }, forRangesList(cadr(e)))
		// the Scheme code computes a one-range form and discards it: the
		// ranges are always a block
		return list(h, cons(sBlock, ranges), body)
	case sym("generator"):
		newenv := lw.newExpansionEnvFor(e, env, false)
		body := lw.resolveVars(cadr(e), newenv, m, lno, ps, inarg)
		filt := car(caddr(e)) == sym("filter")
		rangeExprs := cddr(e)
		var f any
		if filt {
			rangeExprs = cddr(caddr(e))
			f = lw.resolveVars(cadr(caddr(e)), newenv, m, lno, ps, inarg)
		}
		ranges := mapL(func(r any) any { return lw.resolveLetlikeAssign(r, env, newenv, m, lno, ps, inarg) }, rangeExprs)
		if filt {
			return list(h, body, listStar(sym("filter"), f, ranges))
		}
		return listStar(h, body, ranges)
	case sym("hygienic-scope"):
		inner := append([]hygieneScope{{env, m, lno}}, ps...)
		return lw.resolveWithNewEnv(cadr(e), nil, caddr(e), cdddr(e), inner, inarg, true)
	case sym("tuple"):
		return cons(h, mapL(func(x any) any {
			if assignment(x) {
				return list(sAssign, unescape(cadr(x)), sub(caddr(x)))
			}
			return sub(x)
		}, cdr(e)))
	}
	return cons(car(e), mapL(sub, cdr(e)))
}

func (lw *lowerer) resolveKw(e any, env []envEntry, m any, lno []any, ps []hygieneScope, inarg bool) any {
	if !lengthGt(e, 2) {
		return e
	}
	// femtolisp evaluates a quasiquote's parts left to right: the name and
	// the type before the value
	rhs := func() any { return lw.resolveWithNewEnv(caddr(e), env, m, lno, ps, inarg, false) }
	if is(cadr(e), sDecl) {
		td := cadr(e)
		var argname any = false
		if lengthGt(td, 2) {
			argname = cadr(td)
		}
		typ := cadr(td)
		if truthy(argname) {
			typ = caddr(td)
		}
		decl := list(sDecl)
		if truthy(argname) {
			if inarg {
				decl = append(decl, lw.resolveVars(argname, env, m, lno, ps, inarg))
			} else {
				// in keyword arg A=B, don't transform "A"
				decl = append(decl, unescape(argname))
			}
		}
		decl = append(decl, lw.resolveVars(typ, env, m, lno, ps, inarg))
		return list(sym("kw"), decl, rhs())
	}
	var name any
	if inarg {
		name = lw.resolveVars(cadr(e), env, m, lno, ps, inarg)
	} else {
		name = unescape(cadr(e))
	}
	return list(sym("kw"), name, rhs())
}

// justLine is just-line?.
func justLine(ex any) bool {
	return is(ex, sLine) && isAtomS(cadr(ex)) && (isAtomS(caddr(ex)) || nothingP(caddr(ex)))
}

// forRangesList is for-ranges-list.
func forRangesList(ranges any) []any {
	switch car(ranges) {
	case sym("escape"):
		return mapL(func(r any) any { return list(sym("escape"), r) }, forRangesList(cadr(ranges)))
	case sBlock:
		return cdr(ranges)
	}
	return list(ranges)
}

// unescapeGlobalLHS is unescape-global-lhs.
func (lw *lowerer) unescapeGlobalLHS(e any, env []envEntry, m any, lno []any, ps []hygieneScope, inarg bool) any {
	switch {
	case !isPair(e):
		return e
	case car(e) == sym("escape"):
		return lw.unescapeGlobalLHS(cadr(e), env, m, lno, ps, inarg)
	case inList(car(e), sym("parameters"), sym("tuple")):
		return cons(car(e), mapL(func(x any) any { return lw.unescapeGlobalLHS(x, env, m, lno, ps, inarg) }, cdr(e)))
	case inList(car(e), sDecl, sym("kw")) && lengthEq(e, 3):
		return list(car(e), lw.unescapeGlobalLHS(cadr(e), env, m, lno, ps, inarg),
			lw.resolveWithNewEnv(caddr(e), env, m, lno, ps, inarg, false))
	}
	return lw.resolveWithNewEnv(e, env, m, lno, ps, inarg, false)
}

// resolveInLHS is resolve-in-lhs: inarg is turned on in a formal argument list.
func (lw *lowerer) resolveInLHS(e any, env []envEntry, m any, lno []any, ps []hygieneScope, inarg bool) any {
	recur := func(x any) any { return lw.resolveInLHS(x, env, m, lno, ps, inarg) }
	other := func(x any) any { return lw.resolveWithNewEnv(x, env, m, lno, ps, inarg, false) }
	arg := func(x any) any { return lw.resolveWithNewEnv(x, env, m, lno, ps, true, false) }
	switch head(e) {
	case "where":
		return listStar(car(e), recur(cadr(e)), mapL(other, cddr(e)))
	case sDecl:
		return list(car(e), recur(cadr(e)), other(caddr(e)))
	case sCall:
		return listStar(car(e), other(cadr(e)), mapL(arg, cddr(e)))
	case "tuple":
		return cons(car(e), mapL(arg, cdr(e)))
	}
	return other(e)
}

// tupleWrapArrowSig is tuple-wrap-arrow-sig.
func tupleWrapArrowSig(e any) any {
	switch {
	case isAtomS(e):
		return list(sym("tuple"), e)
	case car(e) == sym("where"):
		// julia 1.12.6 calls tuple-wrap-arrow-arglist here, which no file
		// of the front end defines: femtolisp's unbound-variable error,
		// which error-wrap reports as a malformed expression
		panic(loweringError("malformed expression"))
	case car(e) == sym("tuple"):
		return e
	case car(e) == sym("escape"):
		return list(sym("escape"), tupleWrapArrowSig(cadr(e)))
	}
	return list(sym("tuple"), e)
}

func reescape(ux, x any) any {
	for is(x, "escape") {
		ux = list(sym("escape"), ux)
		x = cadr(x)
	}
	return ux
}

// resolveStructField is resolve-struct-field-expansion: identifiers in a
// struct body are field names, not expressions.
func (lw *lowerer) resolveStructField(x any, env []envEntry, m any, lno []any, ps []hygieneScope, inarg bool) any {
	ux := unescape(x)
	switch {
	case isAtomS(ux):
		return ux
	case car(ux) == sDecl:
		return list(sDecl, unescape(cadr(ux)), lw.resolveVars(reescape(caddr(ux), x), env, m, lno, ps, inarg))
	case inList(car(ux), sym("const"), sym("atomic")):
		return list(car(ux), lw.resolveStructField(reescape(cadr(ux), x), env, m, lno, ps, inarg))
	}
	return lw.resolveWithNewEnv(x, env, m, lno, ps, inarg, false)
}

// resolveLetlikeAssign is resolve-letlike-assign.
func (lw *lowerer) resolveLetlikeAssign(bind any, env, newenv []envEntry, m any, lno []any, ps []hygieneScope, inarg bool) any {
	if assignment(bind) {
		// binds expand in newenv with a dummy right-hand side, values in env
		lhs := cadr(lw.resolveVars(makeAssignment(cadr(bind), int64(0)), newenv, m, lno, ps, inarg))
		return makeAssignment(lhs, lw.resolveVars(caddr(bind), env, m, lno, ps, inarg))
	}
	return lw.resolveVars(bind, newenv, m, lno, ps, inarg)
}

// declVarStar is decl-var*: decl-var that also identifies f in f()=...
func declVarStar(e any) any {
	if !isPair(e) {
		return e
	}
	switch car(e) {
	case sym("hygienic-scope"), sym("escape"):
		return []any{}
	case sCall, sAssign, sym("curly"), sym("where"):
		return declVarStar(cadr(e))
	case sDecl:
		if lengthEq(e, 2) {
			return []any{}
		}
		return declVarStar(cadr(e))
	}
	return e
}

func declVarsStar(e any) []any {
	if is(e, "tuple") {
		var out []any
		for _, x := range cdr(e) {
			out = append(out, declVarsStar(x)...)
		}
		return out
	}
	return list(declVarStar(e))
}

// letDeclVar is let-decl-var.
func letDeclVar(e any) any {
	if !isPair(e) {
		return e
	}
	switch car(e) {
	case sym("hygienic-scope"), sym("escape"):
		return letDeclVar(cadr(e))
	case sDecl:
		if lengthEq(e, 2) {
			return []any{}
		}
		return letDeclVar(cadr(e))
	}
	return e
}

// resumeOnEscape is resume-on-escape: f applied to the parts of e at the
// same hygienic level as e's parent.
func resumeOnEscape(f func(any) []any, e any, nblocks int) []any {
	if !isPair(e) || quoted(e) {
		return nil
	}
	switch car(e) {
	case sLambda, sym("module"), sym("toplevel"):
		return nil
	case sym("hygienic-scope"):
		return resumeOnEscape(f, cadr(e), nblocks+1)
	case sym("escape"):
		if nblocks == 0 {
			return f(cadr(e))
		}
		return resumeOnEscape(f, cadr(e), nblocks-1)
	}
	var out []any
	for _, a := range cdr(e) {
		out = append(out, resumeOnEscape(f, a, nblocks)...)
	}
	return out
}

// findDeclaredVarsInExpansion is find-declared-vars-in-expansion.
func findDeclaredVarsInExpansion(e any, decl sym, outer bool) []any {
	switch {
	case !isPair(e) || quoted(e), car(e) == sym("escape"):
		return nil
	case car(e) == sym("hygienic-scope"):
		return resumeOnEscape(func(e any) []any { return findDeclaredVarsInExpansion(e, decl, outer) }, cadr(e), 0)
	case car(e) == decl:
		return mapL(declVarStar, cdr(e))
	case !outer && functionDef(e):
		return nil
	}
	var out []any
	for _, x := range asList(e) {
		out = append(out, findDeclaredVarsInExpansion(x, decl, false)...)
	}
	return out
}

// findAssignedVarsInExpansion is find-assigned-vars-in-expansion.
func findAssignedVarsInExpansion(e any, outer bool) []any {
	switch {
	case !isPair(e) || quoted(e), car(e) == sym("escape"):
		return nil
	case car(e) == sym("hygienic-scope"):
		return resumeOnEscape(func(e any) []any { return findAssignedVarsInExpansion(e, outer) }, cadr(e), 0)
	case !outer && functionDef(e):
		// pick up only the function name
		var fname any = false
		switch car(e) {
		case sAssign:
			fname = declVarStar(cadr(e))
		case sym("function"):
			switch {
			case isAtomS(cadr(e)):
				fname = cadr(e)
			case car(cadr(e)) == sym("tuple"):
			default:
				fname = declVarStar(cadr(e))
			}
		}
		if isSymbol(fname) {
			return list(fname)
		}
		return nil
	case car(e) == sAssign && !functionDef(e):
		return appendL(filterL(isSymbol, declVarsStar(cadr(e))), findAssignedVarsInExpansion(caddr(e), false))
	case car(e) == sym("tuple"):
		var out []any
		for _, x := range cdr(e) {
			if assignment(x) {
				x = caddr(x)
			}
			out = append(out, findAssignedVarsInExpansion(x, false)...)
		}
		return out
	}
	var out []any
	for _, x := range cdr(e) {
		out = append(out, findAssignedVarsInExpansion(x, false)...)
	}
	return out
}

// --- the pattern sets -----------------------------------------------------

// introducedVars is (pattern-expand1 vars-introduced-by-patterns x): the
// varlist's elements, and whether the result is a varlist.
func (lw *lowerer) introducedVars(x any) ([]any, bool) {
	return lw.expandPatterns(lw.varsIntroducedBy, x)
}

// keywordsIntroducedBy is keywords-introduced-by.
func (lw *lowerer) keywordsIntroducedBy(x any) []any {
	v, ok := lw.expandPatterns(lw.keywordsPatterns, x)
	if !ok {
		return nil
	}
	return v
}

// patternResult is what a pattern set's lambda returns: a rewritten form
// to expand again, or a final varlist (or the empty list, not a varlist).
type patternResult struct {
	varlist  []any
	final    bool // varlist (or ()) is the answer
	isVar    bool // the answer is a varlist
	rewrite  any  // otherwise, the form to expand again
	nomatch  bool
}

// expandPatterns is pattern-expand1 for the two sets, whose rewrites all
// lead to a varlist or to (): pattern-expand stops at a varlist, and a ()
// is an atom to it.
func (lw *lowerer) expandPatterns(set func(any) patternResult, x any) ([]any, bool) {
	for i := 0; ; i++ {
		if !isPair(x) || inList(car(x), sQuote, sInert) || i > maxRewrites {
			return nil, false
		}
		r := set(x)
		switch {
		case r.nomatch:
			return nil, false
		case r.final:
			return r.varlist, r.isVar
		}
		x = r.rewrite
	}
}

func varlistOf(v []any) patternResult  { return patternResult{varlist: v, final: true, isVar: true} }
func rewriteTo(x any) patternResult     { return patternResult{rewrite: x} }
func notVarlist() patternResult         { return patternResult{final: true} }
func noPattern() patternResult          { return patternResult{nomatch: true} }

// callSig matches (call name . argl), at least a name.
func callSig(e any) bool { return is(e, sCall) && lengthGt(e, 1) }

// callOrTypedCall matches (-$ (call name . argl) (|::| (call name . argl) _t))
// and returns the call.
func callOrTypedCall(e any) (any, bool) {
	if callSig(e) {
		return e, true
	}
	if is(e, sDecl) && lengthEq(e, 3) && callSig(cadr(e)) {
		return cadr(e), true
	}
	return nil, false
}

// curlyCall matches (call (curly name . sparams) . argl).
func curlyCall(e any) bool {
	return callSig(e) && is(cadr(e), "curly") && lengthGt(cadr(e), 1)
}

// varsIntroducedBy is one application of vars-introduced-by-patterns.
func (lw *lowerer) varsIntroducedBy(e any) patternResult {
	n := length(e)
	switch car(e) {
	case sym("function"):
		if n != 3 {
			break
		}
		sig := cadr(e)
		if curlyCall(sig) {
			return varlistOf(appendL(lw.safeLlistPositionalArgs(lw.fixArglist(cddr(sig), true), false),
				typevarNames(cddr(cadr(sig)))))
		}
		if call, ok := callOrTypedCall(sig); ok {
			argl := appendL(selfArgname(cadr(call)), cddr(call))
			return varlistOf(lw.safeLlistPositionalArgs(lw.fixArglist(argl, true), false))
		}
		if is(sig, "where") && lengthGt(sig, 1) {
			others, ok := lw.introducedVars(list(sym("function"), cadr(sig), caddr(e)))
			if !ok {
				others = nil
			}
			return varlistOf(appendL(others, typevarNames(cddr(sig))))
		}
		if is(sig, "tuple") {
			return rewriteTo(list(sym("->"), sig, caddr(e)))
		}
	case sAssign:
		if n != 3 {
			break
		}
		sig := cadr(e)
		if curlyCall(sig) {
			return rewriteTo(list(sym("function"), sig, caddr(e)))
		}
		if _, ok := callOrTypedCall(sig); ok {
			return rewriteTo(list(sym("function"), sig, caddr(e)))
		}
		if is(sig, "where") && lengthGt(sig, 1) {
			return rewriteTo(cons(sym("function"), cdr(e)))
		}
		if is(sig, "curly") && lengthGt(sig, 1) {
			return varlistOf(typevarNames(cddr(sig)))
		}
	case sym("->"):
		if n != 3 {
			break
		}
		a := cadr(e)
		args := list(a)
		if is(a, "tuple") {
			args = cdr(a)
		}
		return varlistOf(lw.safeLlistPositionalArgs(lw.fixArglist(args, true), false))
	case sym("where"):
		if n >= 2 {
			return varlistOf(typevarNames(cddr(e)))
		}
	case sym("let"):
		if n == 3 {
			return letVars(e)
		}
	case sym("for"):
		if n != 3 {
			break
		}
		assgn := cadr(e)
		if car(assgn) == sBlock {
			return varlistOf(mapL(cadr, cdr(assgn)))
		}
		// (cons 'varlist (cadr assgn)): a list's elements, or an improper
		// list whose elements are none
		l, _ := cadr(assgn).([]any)
		return varlistOf(l)
	case sym("generator"):
		if n < 2 {
			break
		}
		assgn := cddr(e)
		if n == 3 && is(caddr(e), "filter") && lengthGt(caddr(e), 1) {
			assgn = cddr(caddr(e))
		}
		return varlistOf(mapL(cadr, assgn))
	case sym("macro"):
		if n == 3 && callSig(cadr(e)) {
			return rewriteTo(list(sym("->"), cons(sym("tuple"), cddr(cadr(e))), caddr(e)))
		}
	case sym("try"):
		if n == 5 || n == 4 {
			if v := caddr(e); truthy(v) {
				return varlistOf(list(v))
			}
			return notVarlist()
		}
	case sym("struct"):
		if n == 4 {
			spec := caddr(e)
			tn := unescape(typedefExprName(spec))
			return varlistOf(appendL(list(envEntry{tn, tn}, envEntry{sym("new"), sym("new")}), typevarNames(typedefExprTvars(spec))))
		}
	case sym("abstract"), sym("primitive"):
		if (car(e) == sym("abstract") && n == 2) || (car(e) == sym("primitive") && n == 3) {
			spec := cadr(e)
			tn := unescape(typedefExprName(spec))
			return varlistOf(cons(envEntry{tn, tn}, typevarNames(typedefExprTvars(spec))))
		}
	}
	return noPattern()
}

// letVars is the let pattern of vars-introduced-by-patterns.
func letVars(e any) patternResult {
	var vars []any
	for _, b := range letBinds(e) {
		ux := unescape(b)
		switch {
		case isSymbol(ux) || isDecl(ux):
			vars = cons(letDeclVar(ux), vars)
		case lengthEq(b, 3) && car(b) == sAssign:
			ux = unescape(cadr(b))
			switch {
			case isSymbol(ux) || isDecl(ux):
				vars = cons(letDeclVar(ux), vars)
			case eventuallyCall(cadr(b)):
				vars = cons(assignedName(cadr(b)), vars)
			case is(cadr(b), "tuple"):
				vars = appendL(mapL(letDeclVar, lhsVars(cadr(b))), vars)
			default:
				return notVarlist()
			}
		default:
			return notVarlist()
		}
	}
	return varlistOf(vars)
}

// keywordsPatterns is one application of keywords-introduced-by-patterns.
func (lw *lowerer) keywordsPatterns(e any) patternResult {
	if length(e) != 3 {
		return noPattern()
	}
	sig := cadr(e)
	switch car(e) {
	case sym("function"):
		if curlyCall(sig) {
			return varlistOf(lw.safeLlistKeywordArgs(lw.fixArglist(cddr(sig), true)))
		}
		if call, ok := callOrTypedCall(sig); ok {
			return varlistOf(lw.safeLlistKeywordArgs(lw.fixArglist(cddr(call), true)))
		}
		if is(sig, "where") && lengthGt(sig, 1) {
			return rewriteTo(list(sym("function"), cadr(sig), caddr(e)))
		}
	case sAssign:
		if curlyCall(sig) {
			return rewriteTo(list(sym("function"), sig, caddr(e)))
		}
		if call, ok := callOrTypedCall(sig); ok {
			return rewriteTo(list(sym("function"), listStar(sCall, cadr(call), cddr(call)), caddr(e)))
		}
		if is(sig, "where") && lengthGt(sig, 1) {
			return rewriteTo(cons(sym("function"), cdr(e)))
		}
	}
	return noPattern()
}

func typedefExprName(e any) any {
	if isPair(e) && (car(e) == sym("curly") || car(e) == sym("<:")) {
		return typedefExprName(cadr(e))
	}
	return e
}

func typedefExprTvars(e any) []any {
	switch head(e) {
	case "<:":
		return typedefExprTvars(cadr(e))
	case "curly":
		return cddr(e)
	}
	return nil
}

// typevarNames is typevar-names: the names of where-variable expressions,
// skipping the ones analyze-typevar rejects (trycatch).
func typevarNames(lst []any) []any {
	var out []any
	for _, v := range lst {
		if n, ok := typevarExprName(v); ok {
			out = append(out, n)
		}
	}
	return out
}

func typevarExprName(e any) (name any, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			switch r.(type) {
			case loweringError, internalError, runtime.Error:
				name, ok = nil, false
			default:
				panic(r)
			}
		}
	}()
	return unescape(car(analyzeTypevar(e))), true
}

// tryArgName is try-arg-name.
func tryArgName(v any, escaped bool) []any {
	if isSymbol(v) {
		if escaped {
			return nil
		}
		return list(v)
	}
	if !isPair(v) {
		return nil
	}
	switch car(v) {
	case sDecl:
		if lengthEq(v, 2) {
			return nil
		}
		return tryArgName(cadr(v), escaped)
	case sym("..."), sym("kw"), sAssign, sym("hygienic-scope"):
		return tryArgName(cadr(v), escaped)
	case sym("escape"):
		if escaped {
			return list(cadr(v))
		}
		return nil
	case sym("tuple"):
		var out []any
		for _, e := range cdr(v) {
			out = append(out, tryArgName(e, escaped)...)
		}
		return out
	case sym("meta"):
		if nospecializeMeta(v, true) {
			return tryArgName(caddr(v), escaped)
		}
	}
	return nil
}

func safeArgNames(lst []any, escaped bool) []any {
	var out []any
	for _, v := range lst {
		out = append(out, tryArgName(v, escaped)...)
	}
	return out
}

// safeLlistPositionalArgs is safe-llist-positional-args.
func (lw *lowerer) safeLlistPositionalArgs(lst []any, escaped bool) []any {
	var params, normal []any
	for _, a := range lst {
		if is(a, "parameters") {
			params = append(params, a)
		} else {
			normal = append(normal, a)
		}
	}
	args := normal
	for _, p := range params {
		// the rest keywords' name is not a keyword
		args = appendL(args, filterL(vararg, asList(p)))
	}
	return safeArgNames(args, escaped)
}

// safeLlistKeywordArgs is safe-llist-keyword-args.
func (lw *lowerer) safeLlistKeywordArgs(lst []any) []any {
	var kwargs []any
	for _, a := range lst {
		if is(a, "parameters") {
			kwargs = appendL(kwargs, cdr(a))
		}
	}
	kwargs = filterL(func(x any) bool { return !vararg(x) }, kwargs)
	return appendL(safeArgNames(kwargs, false), safeArgNames(kwargs, true), lw.safeLlistPositionalArgs(lst, true))
}

// selfArgname is self-argname.
func selfArgname(name any) []any {
	if lengthEq(name, 3) && car(name) == sDecl {
		return list(cadr(name))
	}
	return nil
}
