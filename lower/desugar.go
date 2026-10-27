package lower

// Pass 1 of julia-syntax.scm: syntax desugaring (expand-forms and the
// expand-table), lines 1-2996 of the Scheme source.

// fillMissingArgname allows (:: T) => (:: #gensym T) in formal argument lists.
func (lw *lowerer) fillMissingArgname(a any, unused bool) any {
	if is(a, sDecl) && isNull(cddr(a)) {
		var n any = unusedSym
		if !unused {
			n = lw.st.gensyms.gensy()
		}
		return list(sDecl, n, cadr(a))
	}
	return a
}

func (lw *lowerer) fixArglist(l []any, unused bool) []any {
	if anyL(vararg, butlast(l)) {
		errorf("invalid \"...\" on non-final argument")
	}
	return mapL(func(a any) any {
		switch {
		case is(a, "kw"):
			return list(sym("kw"), lw.fillMissingArgname(cadr(a), unused), caddr(a))
		case is(a, "..."):
			return list(sym("..."), lw.fillMissingArgname(cadr(a), unused))
		}
		return lw.fillMissingArgname(a, unused)
	}, l)
}

// comparison chains

func (lw *lowerer) compareOne(e []any) (any, []any) {
	arg := caddr(e)
	arg2 := arg
	if isPair(arg) && isPair(cdddr(e)) {
		arg2 = lw.makeSSAValue()
	}
	if !dotopNamed(cadr(e)) && lengthGt(e, 5) && isPair(cadddr(cdr(e))) && dotopNamed(cadddr(cddr(e))) {
		// the 2nd argument of the next comparison is also an argument to an
		// eager (dot) op: do not skip its initialization by short-circuiting
		s := lw.makeSSAValue()
		var init []any
		if !eq(arg, arg2) {
			init = list(list(sAssign, arg2, arg))
		}
		first := L(sBlock, sp(init), list(sAssign, s, cadddr(cdr(e))), list(sCall, cadr(e), car(e), arg2))
		return first, listStar(arg2, cadddr(e), s, listTail(e, 6))
	}
	return addInit(arg, arg2, list(sCall, cadr(e), car(e), arg2)), cons(arg2, cdddr(e))
}

func addInit(arg, arg2, ex any) any {
	if eq(arg, arg2) {
		return ex
	}
	return list(sBlock, list(sAssign, arg2, arg), ex)
}

// compAccum is comp-accum: (expr . rest).
func compAccum(e []any, makeAnd func(a, b any) any, done func([]any) bool, take func([]any) (any, []any)) (any, []any) {
	var ex any
	for !done(e) {
		x, rest := take(e)
		if ex == nil {
			ex = x
		} else {
			ex = makeAnd(ex, x)
		}
		e = rest
	}
	return ex, e
}

func (lw *lowerer) expandScalarCompare(e []any) (any, []any) {
	return compAccum(e, func(a, b any) any { return list(sym("&&"), a, b) },
		func(x []any) bool { return !lengthGt(x, 2) || dotopNamed(cadr(x)) }, lw.compareOne)
}

func (lw *lowerer) expandVectorCompare(e []any) (any, []any) {
	return compAccum(e, func(a, b any) any { return list(sCall, sym(".&"), a, b) },
		func(x []any) bool { return !lengthGt(x, 2) },
		func(e []any) (any, []any) {
			if dotopNamed(cadr(e)) {
				return lw.compareOne(e)
			}
			return lw.expandScalarCompare(e)
		})
}

func (lw *lowerer) expandCompareChain(e []any) any {
	ex, _ := lw.expandVectorCompare(e)
	if ex == nil {
		return []any{}
	}
	return ex
}

// begin/end in indexing

func endVal(a any, n int, tuples []any, last bool) any {
	if len(tuples) == 0 {
		if last && n == 1 {
			return list(sCall, topRef("lastindex"), a)
		}
		return list(sCall, topRef("lastindex"), a, int64(n))
	}
	dimno := L(sCall, topRef("+"), int64(n-len(tuples)), sp(mapL(func(t any) any { return list(sCall, topRef("length"), t) }, tuples)))
	return list(sCall, topRef("lastindex"), a, dimno)
}

func beginVal(a any, n int, tuples []any, last bool) any {
	if len(tuples) == 0 {
		if last && n == 1 {
			return list(sCall, topRef("firstindex"), a)
		}
		return list(sCall, topRef("firstindex"), a, int64(n))
	}
	dimno := L(sCall, topRef("+"), int64(n-len(tuples)), sp(mapL(func(t any) any { return list(sCall, topRef("length"), t) }, tuples)))
	return list(sCall, topRef("first"), list(sCall, topRef("axes"), a, dimno))
}

func replaceBeginend(ex, a any, n int, tuples []any, last bool) any {
	switch {
	case ex == sym("end"):
		return endVal(a, n, tuples, last)
	case ex == sym("begin"):
		return beginVal(a, n, tuples, last)
	case isAtomS(ex) || quoted(ex):
		return ex
	case car(ex) == sym("ref"):
		return listStar(sym("ref"), replaceBeginend(cadr(ex), a, n, tuples, last), cddr(ex))
	case car(ex) == sym("kw"):
		return list(sym("kw"), cadr(ex), replaceBeginend(caddr(ex), a, n, tuples, last))
	}
	return cons(car(ex), mapL(func(x any) any { return replaceBeginend(x, a, n, tuples, last) }, cdr(ex)))
}

// processIndices is process-indices: (values index-list stmts).
func (lw *lowerer) processIndices(a any, idx []any) ([]any, []any) {
	hasVa := anyL(vararg, idx)
	var stmts, tuples, ret []any
	for k, idx0 := range idx {
		n := k + 1
		ix := idx0
		if vararg(idx0) {
			ix = cadr(idx0)
		}
		isLast := k == len(idx)-1
		replaced := replaceBeginend(ix, a, n, tuples, isLast)
		val := replaced
		if kwarg(replaced) {
			val = caddr(replaced)
		}
		var id any = val
		if hasVa && !simpleAtom(val) {
			id = lw.makeSSAValue()
		}
		if !eq(id, val) {
			stmts = append(stmts, list(sAssign, id, val))
		}
		if vararg(idx0) {
			tuples = cons(id, tuples)
		}
		switch {
		case vararg(idx0):
			ret = append(ret, list(sym("..."), id))
		case eq(val, replaced):
			ret = append(ret, id)
		default:
			ret = append(ret, list(sym("kw"), cadr(replaced), id))
		}
	}
	return ret, stmts
}

// methodLambdaExpr is method-lambda-expr.
func (lw *lowerer) methodLambdaExpr(argl []any, body any, rett any) any {
	args := mapL(func(x any) any {
		n := argName(x)
		if underscoreSymbol(n) {
			return unusedSym
		}
		return n
	}, argl)
	b := blockify(body, nil)
	var inner any = b
	if !equal(rett, coreRef("Any")) {
		meta := takeWhile(func(x any) bool { return isPair(x) && inList(car(x), sym("lineinfo"), sLine, sym("meta")) }, cdr(b))
		R := lw.makeSSAValue()
		inner = L(car(b), sp(meta), list(sAssign, R, rett), list(sym("meta"), sym("ret-type"), R), sp(listTail(b, 1+len(meta))))
	}
	return list(sLambda, args, []any{}, list(sym("scope-block"), inner))
}

// analyzeTypevar is analyze-typevar: (name lower-bound upper-bound), #f bounds.
func analyzeTypevar(e any) []any {
	checkSym := func(s any) any {
		if isSymbol(unescape(s)) {
			return s
		}
		errorf("invalid type parameter name \"%s\"", deparse(s))
		return nil
	}
	switch {
	case isAtomS(e):
		return list(checkSym(e), false, false)
	case car(e) == sym("var-bounds"):
		return cdr(e)
	case car(e) == sym("comparison") && lengthEq(e, 6):
		lhs, rel, rhs := listRef(e, 1), listRef(e, 2), listRef(e, 5)
		t := checkSym(listRef(e, 3))
		relSame := eq(rel, listRef(e, 4))
		switch {
		case relSame && rel == sym("<:"):
			return list(t, lhs, rhs)
		case relSame && rel == sym(">:"):
			return list(t, rhs, lhs)
		}
		errorf("invalid bounds in \"where\"")
	case car(e) == sym("<:"):
		return list(checkSym(cadr(e)), false, caddr(e))
	case car(e) == sym(">:"):
		return list(checkSym(cadr(e)), caddr(e), false)
	}
	errorf("invalid variable expression in \"where\"")
	return nil
}

func unescape(e any) any {
	if is(e, "escape") {
		return cadr(e)
	}
	return e
}

func sparamNameBounds(params []any) ([]any, []any) {
	bounds := mapL(func(p any) any { return analyzeTypevar(p) }, params)
	return mapL(car, bounds), bounds
}

// boundsToTypeVar is bounds-to-TypeVar.
func boundsToTypeVar(v any, unmangle bool) any {
	name := car(v)
	if unmangle {
		name = unmangledName(name)
	}
	lb, ub := cadr(v), caddr(v)
	var rest []any
	switch {
	case truthy(ub) && truthy(lb):
		rest = list(lb, ub)
	case truthy(ub):
		rest = list(ub)
	case truthy(lb):
		rest = list(lb, coreRef("Any"))
	}
	return L(sCall, coreRef("TypeVar"), list(sQuote, name), sp(rest))
}

func isMethod(x any) bool {
	if !is(x, "method") {
		return false
	}
	name := cadr(x)
	if is(name, "globalref") {
		return isSymbol(caddr(name))
	}
	return isSymbol(name)
}

func methodExprName(m any) any {
	name := cadr(m)
	if globalref(name) {
		return caddr(name)
	}
	return name
}

// methodExprStaticParameters extracts the static parameter names of a
// (method ...) expression.
func methodExprStaticParameters(m any) []any {
	typeEx := caddr(m)
	if car(typeEx) != sBlock {
		return nil
	}
	spSsavals := cddr(cadddr(last(typeEx)))
	return mapL(func(a any) any { return cadr(caddr(caddr(a))) },
		filterL(func(e any) bool { return is(e, sAssign) && member(cadr(e), spSsavals) != nil }, cdr(typeEx)))
}

func nodotSymRef(e any) bool {
	return isSymbol(e) || (lengthEq(e, 3) && car(e) == sym("globalref"))
}

func symRef(e any) bool {
	return nodotSymRef(e) ||
		(lengthEq(e, 3) && car(e) == sym(".") && (isAtomS(cadr(e)) || symRef(cadr(e))) &&
			isPair(caddr(e)) && inList(car(caddr(e)), sQuote, sInert) && isSymbol(cadr(caddr(e))))
}

func overlay(e any) bool       { return is(e, "overlay") }
func symRefOrOverlay(e any) bool { return overlay(e) || symRef(e) }

func dotsToVararg(a []any) []any {
	if len(a) == 0 {
		return a
	}
	h, las := butlast(a), last(a)
	if vararg(las) {
		return L(sp(h), list(sym("curly"), sym("Vararg"), cadr(las)))
	}
	return L(sp(h), las)
}

// replaceVars is replace-vars with an association list of renames.
func replaceVars(e any, renames []any) any {
	switch {
	case isSymbol(e):
		if r := assq(e, renames); r != nil {
			return cdrAny(r)
		}
		return e
	case !isPair(e) || quoted(e):
		return e
	case inList(car(e), sym("->"), sym("function"), sym("scope-block")):
		return e
	}
	return cons(car(e), mapL(func(x any) any { return replaceVars(x, renames) }, cdr(e)))
}

// cdrAny is the cdr of an association-list entry built with cons2.
func cdrAny(r []any) any {
	if len(r) == 2 {
		if p, ok := r[1].(dotted); ok {
			return p.v
		}
	}
	return r[1:]
}

// dotted marks the cdr of a dotted pair (a . b) stored as the list (a dotted{b}).
type dotted struct{ v any }

// cons2 is (cons a b) for an association-list entry whose cdr is not a list.
func cons2(a, b any) []any { return list(a, dotted{b}) }

func (lw *lowerer) makeGeneratorFunction(name any, spNames, argNames []any, body any) any {
	an := appendL(spNames, mapL(func(n any) any {
		if n == sym("#self#") {
			return lw.st.gensyms.gensy()
		}
		return n
	}, argNames))
	slots := mapL(func(idx any) any { return list(sym("slot"), idx.(int64)+2) }, iota(len(an)))
	b := insertAfterMeta(body, list(L(S("meta"), sym("nospecialize"), sp(slots))))
	return list(sBlock, list(sym("global"), name), list(sym("function"), L(sCall, name, sp(an)), b))
}

func generatedPart(x any, genpart bool) any {
	switch {
	case isAtomS(x) || quoted(x) || functionDef(x):
		return x
	case ifGenerated(x):
		if genpart {
			return list(sym("$"), caddr(x))
		}
		return cadddr(x)
	}
	return cons(car(x), mapL(func(e any) any { return generatedPart(e, genpart) }, cdr(x)))
}

func (lw *lowerer) generatedVersion(body any) any {
	return list(sBlock, lw.bqMacro(generatedPart(body, true)))
}

func nonGeneratedVersion(body any) any { return generatedPart(body, false) }

// extractMethodPrologue is extract-method-prologue.
func extractMethodPrologue(body any) []any {
	if !isPair(body) {
		return nil
	}
	return takeWhile(func(e any) bool { return isPair(e) && (car(e) == sLine || car(e) == sym("meta")) }, cdr(body))
}

// maybeRemoveFunctionloc is maybe-remove-functionloc!: it returns the
// function's line node and the body with it removed when the prologue has
// two line nodes.
func maybeRemoveFunctionloc(body any) (any, any) {
	prologue := extractMethodPrologue(body)
	lnos := filterL(linenum, prologue)
	var functionloc any
	if len(lnos) > 0 {
		functionloc = car(lnos)
	} else if l := filterL(linenum, pairs(body)); len(l) > 0 {
		// femtolisp's filter walks while pair?, so a body that is an atom
		// (the `#s2` of a destructuring `f() = ...`) has no line nodes
		functionloc = car(l)
	} else {
		functionloc = list(sLine, int64(0), sym("none"))
	}
	if len(lnos) > 1 {
		// the first of two line nodes in the prologue is the definition's
		// location, removed from the body
		b := asList(body)
		for i := 0; i+1 < len(b); i++ {
			if eq(functionloc, b[i+1]) {
				nb := append(append([]any{}, b[:i+1]...), b[i+2:]...)
				return functionloc, nb
			}
		}
	}
	return functionloc, body
}

// methodDefExpr_ is method-def-expr-.
func (lw *lowerer) methodDefExpr_(name any, sparams []any, argl []any, body any, rett any) any {
	if rett == nil {
		rett = coreRef("Any")
	}
	if anyL(kwarg, argl) {
		// optional positional arguments
		seen := false
		for _, a := range argl {
			if kwarg(a) {
				seen = true
			} else if seen && !vararg(a) {
				errorf("optional positional arguments must occur at end")
			} else {
				seen = false
			}
		}
		kws, rest := separate(kwarg, argl)
		opt := mapL(cadr, kws)
		dfl := mapL(caddr, kws)
		va, req := separate(vararg, rest)
		return lw.optionalPositionalDefs(name, sparams, req, opt, dfl, body, appendL(req, opt, va), rett)
	}
	names := mapL(car, sparams)
	anames := mapL(func(x any) any {
		if underscoreSymbol(x) {
			return unusedSym
		}
		return x
	}, llistVars(argl))
	unusedAnames := filterL(func(x any) bool { return x != unusedSym }, anames)
	var ename any
	switch {
	case nodotSymRef(name):
		ename = name
	case overlay(name):
		ename = cadr(name)
	default:
		ename = null()
	}
	if d := hasDups(unusedAnames); d != nil {
		errorf("function argument name not unique: \"%s\"", symString(car(d)))
	}
	if d := hasDups(names); d != nil {
		errorf("function static parameter name not unique: \"%s\"", symString(car(d)))
	}
	if anyL(func(x any) bool { return x != unusedSym && memq(x, names) != nil }, anames) {
		errorf("function argument and static parameter name not distinct: \"%s\"", symString(car(intersect(names, unusedAnames))))
	}
	if (truthy(name) && !symRefOrOverlay(name)) || !validName(name) {
		errorf("invalid function name \"%s\"", deparse(name))
	}
	loc, body := maybeRemoveFunctionloc(body)
	var generator []any
	if exprContainsP(ifGenerated, body, func(x any) bool { return !functionDef(x) }) {
		gen := lw.generatedVersion(body)
		nongen := nonGeneratedVersion(body)
		gname := sym(string(lw.st.gensyms.gensy()) + "#" + lw.moduleCounter(nil))
		gf := lw.makeGeneratorFunction(gname, names, anames, gen)
		body = insertAfterMeta(nongen, list(list(sym("meta"), sym("generated"),
			list(sym("new"), coreRef("GeneratedFunctionStub"), gname,
				L(sCall, coreRef("svec"), sp(mapL(quotify, anames))),
				L(sCall, coreRef("svec"), sp(mapL(quotify, names)))))))
		generator = list(gf)
	}
	types := llistTypes(argl)
	lam := lw.methodLambdaExpr(argl, body, rett)
	// HACK: the typevars are bound to ssavalues, since this code might be
	// moved to a different scope by closure-convert
	temps := mapL(func(any) any { return lw.makeSSAValue() }, names)
	renames := make([]any, len(names))
	for i := range names {
		renames[i] = cons2(names[i], temps[i])
	}
	var mdef any
	if len(sparams) == 0 {
		mdef = list(sym("method"), ename,
			list(sCall, coreRef("svec"),
				L(sCall, coreRef("svec"), sp(dotsToVararg(types))),
				list(sCall, coreRef("svec")),
				list(sInert, loc)),
			lam)
	} else {
		var assigns, ren []any
		sps := mapL(func(v any) any { return boundsToTypeVar(v, false) }, sparams)
		for i := range names {
			ren = cons(cons2(names[i], temps[i]), ren)
			// each static parameter sees just the previous ones
			assigns = append(assigns, makeAssignment(temps[i], replaceVars(sps[i], ren[1:])))
		}
		mdef = list(sym("method"), ename,
			L(sBlock, sp(assigns),
				list(sCall, coreRef("svec"),
					L(sCall, coreRef("svec"), sp(dotsToVararg(mapL(func(ty any) any { return replaceVars(ty, renames) }, types)))),
					L(sCall, coreRef("svec"), sp(temps)),
					list(sInert, loc))),
			lam)
	}
	if isSymbol(name) || globalref(name) {
		return L(sBlock, sp(generator), list(sym("method"), name), list(sym("latestworld-if-toplevel")), mdef, list(sym("unnecessary"), name))
	}
	if len(generator) > 0 {
		return L(sBlock, sp(generator), mdef)
	}
	return mdef
}

func scopenest(names, vals []any, ex any) any {
	if len(names) == 0 {
		return ex
	}
	return list(sym("let"), list(sAssign, car(names), car(vals)), list(sBlock, scopenest(cdr(names), cdr(vals), ex)))
}

func makeAssignments(names, vals []any, ex any) any {
	out := []any{sBlock}
	for i := range names {
		out = append(out, makeAssignment(names[i], vals[i]))
	}
	return append(out, ex)
}

func withoutGenerated(stmts []any) []any {
	return filterL(func(x any) bool { return !(generatedMeta(x) || generatedOnlyMeta(x)) }, stmts)
}

// filterSparams keeps only the sparams used by ex or by other sparams.
func filterSparams(ex any, sparams []any) []any {
	var filtered []any
	for i, p := range sparams {
		used := exprContainsEq(car(p), ex) || anyL(func(v any) bool { return exprContainsEq(car(p), v) }, sparams[i+1:])
		if used {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func (lw *lowerer) optionalPositionalDefs(name any, sparams, req, opt, dfl []any, body any, overallArgl []any, rett any) any {
	prologue := withoutGenerated(extractMethodPrologue(body))
	out := []any{sBlock}
	for n := 0; n < len(opt); n++ {
		passed := appendL(req, listHead(opt, n))
		s := filterSparams(cons(sym("list"), passed), sparams)
		vals := listTail(dfl, n)
		absent := listTail(opt, n)
		if anyL(vararg, butlast(vals)) {
			errorf("invalid \"...\" in non-final positional argument default value")
		}
		dependsOnAbsent := anyL(func(defaultv any) bool {
			return contains(func(e any) bool {
				return anyL(func(a any) bool { return contains(func(u any) bool { return eq(u, e) }, a) }, absent)
			}, defaultv)
		}, vals)
		var b any
		if dependsOnAbsent {
			b = L(sBlock, sp(prologue), L(sCall, argName(car(req)), sp(mapL(argName, cdr(passed))), car(vals)))
		} else {
			b = L(sBlock, sp(prologue), L(sCall, argName(car(req)), sp(mapL(argName, cdr(passed))), sp(vals)))
		}
		out = append(out, lw.methodDefExpr_(name, s, passed, b, nil))
	}
	return append(out, lw.methodDefExpr_(name, sparams, overallArgl, body, rett))
}

func removeEmptyParameters(argl []any) []any {
	if hasParameters(argl) && isNull(cdar(argl)) {
		return cdr(argl)
	}
	return argl
}

func checkKwArgs(kw []any) {
	invalid := filterL(func(x any) bool {
		return !(kwarg(x) || vararg(x) || (nospecializeMeta(x, false) && (kwarg(caddr(x)) || vararg(caddr(x)))))
	}, kw)
	if len(invalid) > 0 {
		if is(car(invalid), "parameters") {
			errorf("more than one semicolon in argument list")
		}
		errorf("invalid keyword argument syntax \"%s\"", deparse(car(invalid)))
	}
}

// throwUnassignedKwArgs replaces unassigned keyword arguments with a
// default that throws.
func throwUnassignedKwArgs(argl []any) []any {
	throwUnassigned := func(n any) any {
		return list(sCall, coreRef("throw"), list(sCall, coreRef("UndefKeywordError"), list(sInert, n)))
	}
	var toKw func(x any) any
	toKw = func(x any) any {
		switch {
		case symdecl(x):
			return list(sym("kw"), x, throwUnassigned(declVar(x)))
		case nospecializeMeta(x, false):
			return list(sym("meta"), cadr(x), toKw(caddr(x)))
		}
		return x
	}
	if hasParameters(argl) {
		return cons(cons(sym("parameters"), mapL(toKw, cdar(argl))), cdr(argl))
	}
	return argl
}

// methodDefExpr is method-def-expr.
func (lw *lowerer) methodDefExpr(name any, sparams, argl []any, body any, rett any) any {
	argl = throwUnassignedKwArgs(removeEmptyParameters(argl))
	if hasParameters(argl) {
		checkKwArgs(cdar(argl))
		return lw.keywordsMethodDefExpr(name, sparams, argl, body, rett)
	}
	return lw.methodDefExpr_(name, sparams, argl, body, rett)
}

func keepFirst(pred func(any) bool, l []any) []any {
	for i, x := range l {
		if pred(x) {
			return L(sp(l[:i]), x, sp(filterL(func(y any) bool { return !pred(y) }, l[i+1:])))
		}
	}
	return l
}

// keywordsMethodDefExpr is keywords-method-def-expr.
func (lw *lowerer) keywordsMethodDefExpr(name any, sparams, argl []any, body any, rett any) any {
	kargl := cdar(argl) // keyword expressions (= k v)
	annotations := mapL(func(a any) any { return list(sym("meta"), cadr(a), argName(cadr(caddr(a)))) },
		filterL(func(a any) bool { return nospecializeMeta(a, false) }, kargl))
	kargl = mapL(func(a any) any {
		if nospecializeMeta(a, false) {
			return caddr(a)
		}
		return a
	}, kargl)
	pargl := cdr(argl) // positional args
	body = blockify(body, nil)
	ftype := declType(car(pargl))
	var va []any
	if len(pargl) > 0 {
		l := last(pargl)
		l_ := l
		if kwarg(l) {
			l_ = cadr(l)
		}
		if vararg(l_) || varargexpr(l_) {
			va = list(l)
		}
	}
	var splattedVararg []any
	if len(va) > 0 {
		splattedVararg = list(list(sym("..."), argName(car(va))))
	}
	parglAll := pargl
	if len(va) > 0 {
		pargl = butlast(pargl)
	}
	notOptional := mapL(func(a any) any {
		if kwarg(a) {
			return cadr(a)
		}
		return a
	}, pargl)
	var restkw []any
	if l := last(kargl); vararg(l) {
		restkw = list(cadr(l))
	}
	kws := kargl
	if len(restkw) > 0 {
		kws = butlast(kargl)
	}
	if anyL(vararg, kws) {
		errorf("invalid \"...\" on non-final keyword argument")
	}
	kargl = kws
	vars := mapL(cadr, kargl)
	vals := mapL(caddr, kargl)
	keynames := mapL(declVar, vars)
	orderedDefaults := anyL(func(v any) bool {
		return contains(func(x any) bool { return eq(x, v) }, vals)
	}, keynames)
	ssaKeyvars := !orderedDefaults && !contains(assignment, vals)
	keyvars := keynames
	if ssaKeyvars {
		keyvars = mapL(func(any) any { return lw.makeSSAValue() }, keynames)
	}
	tempslot := lw.st.gensyms.gensy()
	prologue := extractMethodPrologue(body)
	stmts := cdr(body)
	positionalSparams := filterSparams(cons(sym("list"), parglAll), sparams)
	keywordSparams := filterL(func(s any) bool {
		return !anyL(func(p any) bool { return eq(car(p), car(s)) }, positionalSparams)
	}, sparams)
	kw := lw.st.gensyms.gensy()
	kwdecl := list(sDecl, kw, coreRef("NamedTuple"))
	var rkw any
	if len(restkw) > 0 {
		rkw = sym(symString(car(restkw)) + "...")
	}
	restkw = mapL(func(v any) any { return list(sDecl, v, list(sCall, topRef("pairs"), coreRef("NamedTuple"))) }, restkw)
	var und any = false
	if truthy(name) {
		und = undotName(name)
	}
	// (string name) of a non-symbol name prints a list, which starts with "("
	prefix := "#"
	if isSymbol(name) && symString(name) != "" && symString(name)[0] == '#' {
		prefix = ""
	}
	undStr := "_"
	if truthy(und) {
		undStr = symString(und)
	}
	mangled := sym(prefix + undStr + "#" + lw.moduleCounter(nil))
	// nested inside a call so that they get closure converted together,
	// allowing all needed types to be defined before any methods
	var fwd []any
	if isSymbol(name) || globalref(name) {
		fwd = list(list(sym("method"), name))
	}
	// call with keyword args pre-sorted: the original method code
	core := lw.methodDefExpr_(mangled, sparams,
		L(list(sDecl, mangled, list(sCall, coreRef("typeof"), mangled)), sp(vars), sp(restkw), sp(notOptional), sp(va)),
		insertAfterMeta(L(sBlock, sp(stmts)), cons(list(sym("meta"), sym("nkw"), int64(len(vars)+len(restkw))), annotations)),
		rett)
	// call with no keyword args
	var restArg []any
	if len(restkw) > 0 {
		restArg = list(list(sCall, topRef("pairs"), list(sCall, coreRef("NamedTuple"))))
	}
	defaults := vals
	if orderedDefaults {
		defaults = keynames
	}
	ret := list(sReturn, L(sCall, mangled, sp(defaults), sp(restArg), sp(mapL(argName, pargl)), sp(splattedVararg)))
	var retEx any = ret
	if orderedDefaults {
		retEx = scopenest(keynames, vals, ret)
	}
	noKw := lw.methodDefExpr_(name, positionalSparams, parglAll,
		L(sBlock, sp(keepFirst(linenum, withoutGenerated(prologue))), retEx), nil)
	// call with unsorted keyword args: sorts and re-dispatches
	var selfName any = unusedSym
	if anyL(kwarg, appendL(pargl, va)) {
		selfName = lw.st.gensyms.gensy()
	}
	var lnns []any
	if l := filterL(linenum, prologue); len(l) > 0 {
		lnns = list(car(l))
	}
	var localDecls []any
	if ssaKeyvars {
		localDecls = mapL(func(v any) any { return list(sym("local"), v) }, reverse(keynames))
	}
	kwVals := make([]any, len(vars))
	for i, v := range vars {
		dflt := vals[i]
		k := declVar(v)
		rval0 := list(sCall, coreRef("getfield"), kw, list(sInert, k))
		var rval any = rval0
		// a declared type that uses keyword sparams is not asserted here
		if isDecl(v) && !anyL(func(s any) bool { return exprContainsEq(car(s), caddr(v)) }, keywordSparams) {
			T := caddr(v)
			temp := lw.makeSSAValue()
			rval = list(sBlock, list(sAssign, temp, rval0),
				list(sym("if"), list(sCall, coreRef("isa"), temp, T), null(),
					list(sCall, coreRef("throw"), list(sym("new"), coreRef("TypeError"),
						list(sInert, sym("keyword argument")), list(sInert, k), T, temp))),
				temp)
		}
		kwVals[i] = list(sBlock, list(sym("if"), list(sCall, coreRef("isdefined"), kw, list(sQuote, k)),
			list(sAssign, tempslot, rval), list(sAssign, tempslot, dflt)), tempslot)
	}
	var check any
	if len(restkw) == 0 {
		check = list(sym("if"), list(sCall, topRef("isempty"),
			list(sCall, topRef("diff_names"), list(sCall, topRef("keys"), kw), L(S("tuple"), sp(mapL(quotify, keynames))))),
			null(),
			L(sCall, topRef("kwerr"), kw, sp(mapL(argName, pargl)), sp(splattedVararg)))
	} else {
		var src any = kw
		if len(keynames) > 0 {
			src = list(sCall, topRef("structdiff"), kw, list(sym("curly"), coreRef("NamedTuple"), L(S("tuple"), sp(mapL(quotify, keynames)))))
		}
		check = list(sAssign, rkw, list(sCall, topRef("pairs"), src))
	}
	var rkwArg []any
	if len(restkw) > 0 {
		rkwArg = list(rkw)
	}
	final := list(sBlock, check,
		list(sReturn, L(sCall, mangled, sp(keyvars), sp(rkwArg), sp(mapL(argName, pargl)), sp(splattedVararg))))
	var sorterBody any
	if ssaKeyvars {
		sorterBody = makeAssignments(keyvars, kwVals, final)
	} else {
		sorterBody = scopenest(keyvars, kwVals, final)
	}
	sorter := lw.methodDefExpr_(name, positionalSparams,
		L(list(sDecl, selfName, list(sCall, coreRef("kwftype"), ftype)), kwdecl, sp(pargl), sp(va)),
		L(sBlock,
			sp(mapL(propagateMethodMeta, filterL(isMeta, prologue))),
			sp(filterL(argwideNospecializeMeta, prologue)),
			sp(lnns),
			sp(mapL(func(m any) any {
				return L(S("meta"), cadr(m), sp(filterL(func(v any) bool { return memq(v, keynames) == nil }, cddr(m))))
			}, filterL(func(x any) bool { return nospecializeMeta(x, false) }, prologue))),
			sp(localDecls),
			sorterBody),
		nil)
	var primary any = name
	if !isSymbol(name) {
		primary = null()
	}
	return list(sCall, coreRef("ifelse"), fFalse(), fFalse(),
		L(sBlock, sp(fwd), list(sym("latestworld-if-toplevel")), core, noKw, sorter, primary))
}

// sinkAssignment moves an assignment into the last statement of a block.
func sinkAssignment(lhs, rhs any) any {
	if is(rhs, sBlock) {
		rr := reverse(cdr(rhs))
		return L(sBlock, sp(reverse(cdr(rr))), list(sAssign, lhs, car(rr)))
	}
	return list(sAssign, lhs, rhs)
}

var expandFormsAtoms = map[sym]bool{
	sQuote: true, sInert: true, sTop: true, sCore: true, "globalref": true, "module": true,
	"toplevel": true, "ssavalue": true, sNull: true, sTrue: true, sFalse: true, "meta": true,
	"using": true, "import": true, "export": true, "public": true, "thismodule": true, "toplevel-only": true,
}

// expandForms is expand-forms.
func (lw *lowerer) expandForms(e any) any {
	if isAtomS(e) {
		return e
	}
	if h, ok := car(e).(sym); ok {
		if expandFormsAtoms[h] {
			return e
		}
		if f := lw.expandTable(h); f != nil {
			return f(asList(e))
		}
	}
	return cons(car(e), mapL(lw.expandForms, cdr(e)))
}

func (lw *lowerer) mapExpand(l []any) []any { return mapL(lw.expandForms, l) }

func something(e []any) any {
	for _, x := range e {
		if !equal(x, list(sNull)) {
			return x
		}
	}
	return false
}

var updateOps = map[sym]bool{}

func init() {
	for _, s := range fields(`+= -= *= .*= /= ./= //= .//= \= .\= .+= .-= ^= .^= ÷= .÷= %= .%= |= .|= &= .&= $= ⊻= .⊻= <<= .<<= >>= .>>= >>>= .>>>=`) {
		updateOps[sym(s)] = true
	}
}

// expandTable is expand-table.
func (lw *lowerer) expandTable(h sym) func(e []any) any {
	switch h {
	case "function":
		return lw.expandFunctionDef
	case "->":
		return lw.expandArrow
	case "let":
		return func(e []any) any { return lw.expandLet(e, true) }
	case "soft-let":
		return func(e []any) any { return lw.expandLet(e, false) }
	case "macro":
		return lw.expandMacroDef
	case "struct":
		return lw.expandStructDef
	case "try":
		return lw.expandTry
	case "lambda":
		return func(e []any) any {
			var locals []any
			if lengthEq(e, 3) {
				locals = list([]any{})
			}
			return L(sLambda, lw.mapExpand(asList(cadr(e))), sp(locals), sp(lw.mapExpand(cddr(e))))
		}
	case "opaque_closure":
		return lw.expandOpaqueClosure
	case "block":
		return func(e []any) any {
			switch {
			case isNull(cdr(e)):
				return null()
			case isNull(cddr(e)) && !linenum(cadr(e)):
				return lw.expandForms(cadr(e))
			}
			return cons(sBlock, lw.mapExpand(cdr(e)))
		}
	case ".":
		return func(e []any) any {
			if lengthEq(e, 2) {
				return list(sCall, topRef("BroadcastFunction"), cadr(e))
			}
			return lw.expandFuseBroadcast(nil, e)
		}
	case ".&&", ".||":
		return func(e []any) any { return lw.expandFuseBroadcast(nil, e) }
	case ".=":
		return func(e []any) any { return lw.expandFuseBroadcast(cadr(e), caddr(e)) }
	case "<:", ">:", "-->":
		return func(e []any) any { return lw.expandForms(L(sCall, car(e), sp(cdr(e)))) }
	case "where":
		return func(e []any) any { return lw.expandForms(lw.expandWheres(cadr(e), cddr(e))) }
	case "const":
		return lw.expandConstDecl
	case "atomic":
		return func([]any) any {
			errorf("unimplemented or unsupported atomic declaration")
			return nil
		}
	case "local", "global", "local-def":
		return func(e []any) any {
			if isSymbol(cadr(e)) && lengthEq(e, 2) {
				return e
			}
			return lw.expandForms(lw.expandDecls(car(e).(sym), cdr(e)))
		}
	case "=":
		return func(e []any) any { return lw.expandAssignment(e, false) }
	case "abstract":
		return func(e []any) any {
			name, params, super := analyzeTypeSig(cadr(e))
			return lw.expandForms(lw.abstractTypeDefExpr(name, params, super))
		}
	case "primitive":
		return func(e []any) any {
			name, params, super := analyzeTypeSig(cadr(e))
			return lw.expandForms(lw.primitiveTypeDefExpr(caddr(e), name, params, super))
		}
	case "comparison":
		return func(e []any) any { return lw.expandForms(lw.expandCompareChain(cdr(e))) }
	case "ref":
		return func(e []any) any {
			if hasParameters(cddr(e)) {
				errorf("unexpected semicolon in array expression")
			}
			return lw.expandForms(lw.partiallyExpandRef(e))
		}
	case "curly":
		return func(e []any) any {
			if hasParameters(cddr(e)) {
				errorf("unexpected semicolon in \"%s\"", deparse(e))
			}
			if anyL(assignment, cddr(e)) {
				errorf("misplaced assignment statement in \"%s\"", deparse(e))
			}
			curlyparams, whereparams := lw.extractImplicitWhereparams(e)
			if len(whereparams) == 0 {
				return lw.expandForms(L(sCall, coreRef("apply_type"), sp(cdr(e))))
			}
			return lw.expandForms(L(S("where"), L(S("curly"), cadr(e), sp(curlyparams)), sp(whereparams)))
		}
	case "call":
		return lw.expandCall
	case "do":
		return func(e []any) any {
			call := cadr(e)
			f := cadr(call)
			argl := cddr(call)
			af := caddr(e)
			if hasParameters(argl) {
				return lw.expandForms(L(sCall, f, car(argl), af, sp(cdr(argl))))
			}
			return lw.expandForms(L(sCall, f, af, sp(argl)))
		}
	case "tuple":
		return func(e []any) any {
			switch {
			case lengthGt(e, 1) && is(cadr(e), "parameters"):
				if lengthEq(e, 2) {
					return lw.expandForms(lw.lowerNamedTuple(cdr(cadr(e)), nil, "named tuple field", "named tuple element", false))
				}
				errorf("unexpected semicolon in tuple")
			case anyL(assignment, cdr(e)):
				return lw.expandForms(lw.lowerNamedTuple(cdr(e), nil, "named tuple field", "named tuple element", false))
			}
			return lw.expandForms(L(sCall, coreRef("tuple"), sp(cdr(e))))
		}
	case "braces":
		return func([]any) any {
			errorf("{ } vector syntax is discontinued")
			return nil
		}
	case "bracescat":
		return func([]any) any {
			errorf("{ } matrix syntax is discontinued")
			return nil
		}
	case "string":
		return func(e []any) any {
			return lw.expandForms(L(sCall, topRef("string"), sp(mapL(func(s any) any {
				if lengthEq(s, 2) && car(s) == sym("string") && isSString(cadr(s)) {
					return cadr(s)
				}
				return s
			}, cdr(e)))))
		}
	case "::":
		return func(e []any) any {
			if !lengthEq(e, 3) {
				errorf("invalid \"::\" syntax")
			}
			if !symbolLike(cadr(e)) {
				return list(sCall, coreRef("typeassert"), lw.expandForms(cadr(e)), lw.expandForms(caddr(e)))
			}
			return lw.mapExpand(e)
		}
	case "if", "elseif":
		return func(e []any) any { return listStar(car(e), lw.expandCondition(cadr(e)), lw.mapExpand(cddr(e))) }
	case "while":
		return func(e []any) any {
			return list(sym("break-block"), sym("loop-exit"),
				list(sym("_while"), lw.expandCondition(cadr(e)),
					list(sym("break-block"), sym("loop-cont"),
						list(sym("scope-block"), blockify(lw.expandForms(caddr(e)), nil)))))
		}
	case "break":
		return func(e []any) any {
			if isPair(cdr(e)) {
				return e
			}
			return list(sym("break"), sym("loop-exit"))
		}
	case "continue":
		return func([]any) any { return list(sym("break"), sym("loop-cont")) }
	case "for":
		return func(e []any) any {
			var ranges []any
			if car(cadr(e)) == sBlock {
				ranges = cdr(cadr(e))
			} else {
				ranges = list(cadr(e))
			}
			return lw.expandForms(lw.expandFor(mapL(cadr, ranges), mapL(caddr, ranges), caddr(e)))
		}
	case "&&":
		return func(e []any) any { return lw.expandForms(expandAnd(e)) }
	case "||":
		return func(e []any) any { return lw.expandForms(lw.expandOr(e)) }
	case "&":
		return func(e []any) any {
			errorf("invalid syntax %s", deparse(e))
			return nil
		}
	case "...":
		return func([]any) any {
			errorf("\"...\" expression outside call")
			return nil
		}
	case "$":
		return func([]any) any {
			errorf("\"$\" expression outside quote")
			return nil
		}
	case "vect":
		return func(e []any) any {
			if hasParameters(cdr(e)) {
				errorf("unexpected semicolon in array expression")
			}
			if anyL(assignment, cdr(e)) {
				errorf("misplaced assignment statement in \"%s\"", deparse(e))
			}
			return lw.expandForms(L(sCall, topRef("vect"), sp(cdr(e))))
		}
	case "hcat":
		return func(e []any) any {
			if anyL(assignment, cdr(e)) {
				errorf("misplaced assignment statement in \"%s\"", deparse(e))
			}
			return lw.expandForms(L(sCall, topRef("hcat"), sp(cdr(e))))
		}
	case "vcat":
		return func(e []any) any {
			return lw.expandVcat(e, list(topRef("vcat")), list(topRef("hvcat")), list(topRef("hvcat_rows")))
		}
	case "ncat":
		return func(e []any) any { return lw.expandNcat(e, list(topRef("hvncat"))) }
	case "typed_hcat":
		return func(e []any) any {
			if anyL(assignment, cddr(e)) {
				errorf("misplaced assignment statement in \"%s\"", deparse(e))
			}
			return lw.expandForms(L(sCall, topRef("typed_hcat"), sp(cdr(e))))
		}
	case "typed_vcat":
		return func(e []any) any {
			t := cadr(e)
			return lw.expandVcat(cdr(e), list(topRef("typed_vcat"), t), list(topRef("typed_hvcat"), t), list(topRef("typed_hvcat_rows"), t))
		}
	case "typed_ncat":
		return func(e []any) any { return lw.expandNcat(cdr(e), list(topRef("typed_hvncat"), cadr(e))) }
	case "'":
		return func(e []any) any { return lw.expandForms(list(sCall, sym("'"), cadr(e))) }
	case "generator":
		return func(e []any) any {
			checkNoReturn(e)
			return lw.expandGenerator(e, false, nil)
		}
	case "flatten":
		return func(e []any) any { return lw.expandGenerator(cadr(e), true, nil) }
	case "comprehension":
		return func(e []any) any {
			if lengthGt(e, 2) {
				// backwards compatibility for macros generating :comprehension exprs
				return lw.expandForms(list(sym("comprehension"), cons(sym("generator"), cdr(e))))
			}
			if caadr(e) == sym("generator") && anyL(func(x any) bool { return x == sym(":") }, cddr(cadr(e))) {
				errorf("comprehension syntax with `:` ranges has been removed")
			}
			return lw.expandForms(list(sCall, topRef("collect"), cadr(e)))
		}
	case "typed_comprehension":
		return func(e []any) any {
			if car(caddr(e)) == sym("generator") {
				ranges := cddr(caddr(e))
				if anyL(func(x any) bool { return x == sym(":") }, ranges) {
					errorf("comprehension syntax with `:` ranges has been removed")
				}
				if everyL(func(x any) bool { return is(x, sAssign) }, ranges) {
					// a hack to lower simple comprehensions to loops very early
					return lw.expandForms(lw.lowerComprehension(cadr(e), cadr(caddr(e)), ranges))
				}
			}
			return lw.expandForms(list(sCall, topRef("collect"), cadr(e), caddr(e)))
		}
	case "gc_preserve":
		return func(e []any) any {
			s := lw.makeSSAValue()
			r := lw.makeSSAValue()
			return list(sBlock,
				list(sAssign, s, L(S("gc_preserve_begin"), sp(cddr(e)))),
				list(sAssign, r, lw.expandForms(cadr(e))),
				list(sym("gc_preserve_end"), s),
				r)
		}
	case "line":
		return func(e []any) any {
			lw.desugarLoc = e
			return e
		}
	}
	if updateOps[h] {
		return lw.lowerUpdateOp
	}
	return nil
}

func hasReturn(e any) bool {
	return exprContainsP(isReturn, e, func(x any) bool { return !functionDef(x) })
}

func checkNoReturn(e any) {
	if hasReturn(e) {
		errorf("\"return\" not allowed inside comprehension or generator")
	}
}

func hasBreakOrContinue(e any) bool {
	return exprContainsP(func(x any) bool { return isPair(x) && inList(car(x), sym("break"), sym("continue")) }, e,
		func(x any) bool { return !(isPair(x) && inList(car(x), sym("for"), sym("while"))) })
}

func (lw *lowerer) lowerComprehension(ty, ex any, itrs []any) any {
	checkNoReturn(ex)
	if hasBreakOrContinue(ex) {
		errorf("break or continue outside loop")
	}
	result := lw.makeSSAValue()
	idx := lw.st.gensyms.gensy()
	oneresult := lw.makeSSAValue()
	prod := lw.makeSSAValue()
	isz := lw.makeSSAValue()
	szunk := lw.makeSSAValue()
	iv := mapL(func(any) any { return lw.makeSSAValue() }, itrs)
	var constructLoops func(itrs, iv []any) any
	constructLoops = func(itrs, iv []any) any {
		if len(itrs) == 0 {
			return list(sBlock, list(sAssign, oneresult, ex),
				list(sym("inbounds"), fTrue()),
				list(sym("if"), szunk,
					list(sCall, topRef("push!"), result, oneresult),
					list(sCall, topRef("setindex!"), result, oneresult, idx)),
				list(sym("inbounds"), sym("pop")),
				list(sAssign, idx, list(sCall, topRef("add_int"), idx, int64(1))))
		}
		return list(sym("for"), list(sAssign, cadr(car(itrs)), car(iv)), constructLoops(cdr(itrs), cdr(iv)))
	}
	overallItr := prod
	if len(itrs) == 1 {
		overallItr = asList(car(iv))
	}
	var inits []any
	for i, v := range iv {
		inits = append(inits, list(sAssign, v, caddr(itrs[i])))
	}
	var prodInit []any
	if len(itrs) != 1 {
		prodInit = list(list(sAssign, prod, L(sCall, topRef("product"), sp(iv))))
	}
	return list(sym("scope-block"), L(sBlock,
		list(sym("local"), idx),
		sp(inits),
		sp(prodInit),
		list(sAssign, isz, list(sCall, topRef("IteratorSize"), overallItr)),
		list(sAssign, szunk, list(sCall, coreRef("isa"), isz, topRef("SizeUnknown"))),
		list(sAssign, result, list(sCall, topRef("_array_for"), ty, overallItr, isz)),
		list(sAssign, idx, list(sCall, topRef("first"), list(sCall, topRef("LinearIndices"), result))),
		constructLoops(reverse(itrs), reverse(iv)),
		result))
}

// expandCall is the expand-table entry of `call`.
func (lw *lowerer) expandCall(e []any) any {
	if !lengthGt(e, 2) {
		return lw.mapExpand(e)
	}
	f := cadr(e)
	switch {
	case dotopNamed(f):
		return lw.expandFuseBroadcast(nil, list(sym("."), undotop(f), cons(sym("tuple"), cddr(e))))
	case lengthEq(f, 2) && car(f) == sym("."):
		// "(.op)(...)"
		return lw.expandFuseBroadcast(nil, list(sym("."), cadr(f), cons(sym("tuple"), cddr(e))))
	case f == sym("ccall"):
		if !lengthGt(e, 4) {
			errorf("too few arguments to ccall")
		}
		cconv := cadddr(e)
		haveCconvExpr := is(cconv, "cconv")
		haveCconv := haveCconvExpr || inList(cconv, sym("cdecl"), sym("stdcall"), sym("fastcall"), sym("thiscall"), sym("llvmcall"))
		afterCconv := cdddr(e)
		if haveCconv {
			afterCconv = listTail(e, 4)
		}
		name := caddr(e)
		RT := car(afterCconv)
		argtypes := cadr(afterCconv)
		args := cddr(afterCconv)
		if !is(argtypes, "tuple") {
			if is(RT, "tuple") {
				errorf("ccall argument types must be a tuple; try \"(T,)\" and check if you specified a correct return type")
			}
			errorf("ccall argument types must be a tuple; try \"(T,)\"")
		}
		var cc any = sym("ccall")
		var nreq any = false
		if haveCconv {
			if haveCconvExpr {
				cc = cadr(cconv)
				nreq = caddr(cconv)
			} else {
				cc = cconv
			}
		}
		return lw.lowerCcall(name, RT, cdr(argtypes), args, cc, nreq)
	case anyL(kwarg, cddr(e)):
		// f(..., a=b, ...)
		return lw.expandForms(lw.lowerKwCall(f, cddr(e)))
	case hasParameters(cddr(e)):
		// f(...; ...)
		if isNull(cdr(car(cddr(e)))) {
			// empty parameters block; issue #18845
			return lw.expandForms(L(sCall, f, sp(cdddr(e))))
		}
		return lw.expandForms(lw.lowerKwCall(f, cddr(e)))
	case anyL(vararg, cddr(e)):
		// call with splat: wrap runs of non-... arguments in tuple()
		var tupleWrap func(a, run []any) []any
		tupleWrap = func(a, run []any) []any {
			if len(a) == 0 {
				if len(run) == 0 {
					return nil
				}
				return list(L(sCall, coreRef("tuple"), sp(reverse(run))))
			}
			x := car(a)
			if lengthEq(x, 2) && car(x) == sym("...") {
				if len(run) == 0 {
					return listStar(cadr(x), tupleWrap(cdr(a), nil))
				}
				return listStar(L(sCall, coreRef("tuple"), sp(reverse(run))), cadr(x), tupleWrap(cdr(a), nil))
			}
			return tupleWrap(cdr(a), cons(x, run))
		}
		return lw.expandForms(L(sCall, coreRef("_apply_iterate"), topRef("iterate"), f, sp(tupleWrap(cddr(e), nil))))
	case identifierName(f) == sym("^") && lengthEq(e, 4) && isNumber(cadddr(e)):
		return lw.expandForms(list(sCall, topRef("literal_pow"), f, caddr(e),
			list(sCall, list(sCall, coreRef("apply_type"), topRef("Val"), cadddr(e)))))
	case f == sym("include"):
		r := lw.makeSSAValue()
		return list(sBlock, list(sAssign, r, lw.mapExpand(e)), list(sym("latestworld-if-toplevel")), r)
	}
	return lw.mapExpand(e)
}

func (lw *lowerer) expandOpaqueClosure(e []any) any {
	argt := something(list(lw.expandForms(cadr(e)), false))
	rtLb := something(list(lw.expandForms(caddr(e)), false))
	rtUb := something(list(lw.expandForms(cadddr(e)), false))
	allowPartial := listRef(e, 4)
	F := listRef(e, 5)
	arglist := lw.functionArglist(F)
	var lastarg any = false
	if len(arglist) > 0 {
		lastarg = last(arglist)
	}
	if truthy(argt) && anyL(func(arg any) bool {
		if vararg(arg) {
			arg = cadr(arg)
		}
		return !isSymbol(arg)
	}, arglist) {
		errorf("Opaque closure argument type may not be specified both in the method signature and separately")
	}
	var isva any = fFalse()
	if varargexpr(lastarg) || vararg(lastarg) {
		isva = fTrue()
	}
	meth := cadddr(caddr(lw.expandForms(F))) // the `method` expr
	lam := cadddr(meth)
	sigBlock := caddr(meth)
	if !is(sigBlock, sBlock) {
		sigBlock = list(sBlock, sigBlock)
	}
	stmts := cdr(butlast(sigBlock))
	sigSvec := last(sigBlock)
	typSvec := caddr(sigSvec)
	tvars := cddr(cadddr(sigSvec))
	argtypes := cdddr(typSvec)
	functionloc := cadr(listRef(sigSvec, 4))
	var argtype any = lw.expandForms(L(S("curly"), coreRef("Tuple"), sp(argtypes)))
	for _, v := range reverse(tvars) {
		argtype = list(sCall, coreRef("UnionAll"), v, argtype)
	}
	if truthy(argt) {
		argtype = argt
	}
	if len(stmts) > 0 {
		argtype = L(sBlock, sp(stmts), argtype)
	}
	return list(sym("_opaque_closure"), argtype, rtLb, rtUb, isva, int64(len(argtypes)), allowPartial, functionloc, lam)
}

func (lw *lowerer) expandUnionallDef(name, typeEx any, isConst bool) any {
	if is(name, "curly") {
		n := cadr(name)
		params := cddr(name)
		rr := lw.makeSSAValue()
		if len(params) == 0 {
			errorf("empty type parameter list in \"%s\"", deparse(list(sAssign, list(sym("curly"), n), typeEx)))
		}
		kind := sym("const")
		if !isConst {
			kind = "assign-const-if-global"
		}
		return lw.expandForms(list(sBlock,
			list(sAssign, rr, L(S("where"), typeEx, sp(params))),
			list(kind, n, rr),
			list(sym("latestworld-if-toplevel")),
			rr))
	}
	return lw.expandForms(list(sym("const"), list(sAssign, name, typeEx)))
}

func filterNotUnderscore(syms []any) []any {
	return filterL(func(x any) bool { return !underscoreSymbol(x) }, syms)
}

// expandConstDecl expands `[global] const a::T = val`.
func (lw *lowerer) expandConstDecl(e []any) any {
	checkAssignment := func(a any) {
		if !is(a, sAssign) {
			errorf("expected assignment after \"const\"")
		}
	}
	if lengthEq(e, 3) {
		return list(sym("const"), cadr(e), lw.expandForms(caddr(e)))
	}
	arg := cadr(e)
	switch car(arg) {
	case sym("global"):
		asgn := cadr(arg)
		checkAssignment(asgn)
		return L(sBlock, sp(mapL(func(v any) any { return list(sym("global"), v) }, lhsBoundNames(cadr(asgn)))),
			lw.expandAssignment(asList(asgn), true))
	case sAssign:
		checkAssignment(arg)
		return lw.expandAssignment(asList(arg), true)
	}
	errorf("expected assignment after \"const\"")
	return nil
}

// assignedName is the symbol a complex assignment LHS ultimately assigns.
func assignedName(e any) any {
	switch {
	case isAtomS(e):
		return e
	case inList(car(e), sCall, sym("curly"), sym("where")) || (car(e) == sDecl && eventuallyCall(e)):
		return assignedName(cadr(e))
	}
	return e
}

// expandDecls: local x, (y=2), z => local x;local y;local z;y = 2
func (lw *lowerer) expandDecls(what sym, binds []any) any {
	var decls, assigns []any
	for _, x := range binds {
		switch {
		case assignmentLike(x) || functionDef(x):
			newVars := lhsDecls(assignedName(cadr(x)))
			decls = appendL(mapL(func(v any) any { return list(what, v) }, newVars), decls)
			assigns = cons(list(car(x), allDeclVars(cadr(x)), caddr(x)), assigns)
		case is(x, sDecl):
			decls = cons(cons(sym("decl"), cdr(x)), cons(list(what, declVar(x)), decls))
		case isSymbol(x):
			decls = cons(list(what, x), decls)
		default:
			errorf("invalid syntax in \"%s\" declaration", string(what))
		}
	}
	var tail []any
	if len(assigns) == 0 {
		tail = list(null())
	}
	return L(sBlock, sp(reverse(decls)), sp(reverse(assigns)), sp(tail))
}

func functionLhs(lhs any) bool {
	return isPair(lhs) && (car(lhs) == sCall || car(lhs) == sym("where") ||
		(car(lhs) == sDecl && is(cadr(lhs), sCall)))
}

// expandAssignment is expand-assignment.
func (lw *lowerer) expandAssignment(e []any, isConst bool) any {
	lhs := cadr(e)
	assignmentToFunction := func(e any) any { return cons(sym("function"), cdr(e)) }
	maybeWrapConst := func(x any) any {
		if isConst {
			return list(sym("const"), x)
		}
		return x
	}
	switch {
	case functionLhs(lhs):
		// `const f() = ...`: the const is inoperative and stripped
		return lw.expandForms(assignmentToFunction(e))
	case is(lhs, "curly"):
		return lw.expandUnionallDef(cadr(e), caddr(e), isConst)
	case assignment(caddr(e)):
		// chain of assignments: a=b=c becomes b=c; a=c
		lhss := list(lhs)
		rhs := caddr(e)
		for assignment(rhs) && !functionLhs(cadr(rhs)) {
			lhss = cons(cadr(rhs), lhss)
			rhs = caddr(rhs)
		}
		var rr any = rhs
		if !symbolLike(rhs) {
			rr = lw.makeSSAValue()
		}
		rev := reverse(lhss)
		lhs0 := car(rev)
		rest := reverse(cdr(rev))
		var init []any
		if !eq(rr, rhs) {
			var r any = rhs
			if assignment(rhs) {
				r = assignmentToFunction(rhs)
			}
			init = list(list(sAssign, rr, r))
		}
		return lw.expandForms(L(sBlock, sp(init),
			sp(mapL(func(l any) any { return list(sAssign, l, rr) }, rest)),
			// in const x = y = z, only x becomes const
			maybeWrapConst(list(sAssign, lhs0, rr)),
			list(sym("unnecessary"), rr)))
	case (symbolLike(lhs) && validName(lhs)) || globalref(lhs):
		if isConst {
			rr := lw.makeSSAValue()
			return list(sBlock, sinkAssignment(rr, lw.expandForms(caddr(e))),
				list(sym("const"), lhs, rr), list(sym("latestworld")), list(sym("unnecessary"), rr))
		}
		return sinkAssignment(lhs, lw.expandForms(caddr(e)))
	case isAtomS(lhs):
		errorf("invalid assignment location \"%s\"", deparse(lhs))
	}
	switch car(lhs) {
	case sym("."):
		// a.b =
		if isConst {
			errorf("cannot declare \"%s\" `const`", deparse(lhs))
		}
		a, b, rhs := cadr(lhs), caddr(lhs), caddr(e)
		if lengthEq(b, 2) && car(b) == sym("tuple") {
			errorf("invalid syntax \"%s\"", deparse(a)+".("+deparse(cadr(b))+") = ...")
		}
		var aa any = a
		if !symbolLike(a) {
			aa = lw.makeSSAValue()
		}
		var bb any = b
		if !(isAtomS(b) || symbolLike(b) || (isPair(b) && quoted(b))) {
			bb = lw.makeSSAValue()
		}
		var rr any = rhs
		if !(symbolLike(rhs) || isAtomS(rhs)) {
			rr = lw.makeSSAValue()
		}
		var s1, s2, s3 []any
		if !eq(aa, a) {
			s1 = list(sinkAssignment(aa, lw.expandForms(a)))
		}
		if !eq(bb, b) {
			s2 = list(sinkAssignment(bb, lw.expandForms(b)))
		}
		if !eq(rr, rhs) {
			s3 = list(sinkAssignment(rr, lw.expandForms(rhs)))
		}
		return L(sBlock, sp(s1), sp(s2), sp(s3), list(sCall, topRef("setproperty!"), aa, bb, rr), list(sym("unnecessary"), rr))
	case sym("tuple"):
		lhss := cdr(lhs)
		x := caddr(e)
		if hasParameters(lhss) {
			// property destructuring
			return lw.expandPropertyDestruct(lhss, x, maybeWrapConst)
		}
		return lw.expandTupleDestruct(lhss, x, maybeWrapConst)
	case sym("typed_hcat"):
		errorf("invalid spacing in left side of indexed assignment")
	case sym("typed_vcat"), sym("typed_ncat"):
		errorf("unexpected \";\" in left side of indexed assignment")
	case sym("ref"):
		// (= (ref a . idxs) rhs)
		if isConst {
			errorf("cannot declare \"%s\" `const`", deparse(lhs))
		}
		a, idxs, rhs := cadr(lhs), cddr(lhs), caddr(e)
		reuse := isPair(a) && contains(func(x any) bool { return x == sym("end") }, idxs)
		arr := a
		var stmts []any
		if reuse {
			arr = lw.makeSSAValue()
			stmts = list(list(sAssign, arr, lw.expandForms(a)))
		}
		rrhs := isPair(rhs) && !ssavalue(rhs) && !quoted(rhs)
		r := rhs
		var rini []any
		if rrhs {
			r = lw.makeSSAValue()
			rini = list(sinkAssignment(r, lw.expandForms(rhs)))
		}
		newIdxs, stuff := lw.processIndices(arr, idxs)
		return L(sBlock, sp(stmts), sp(lw.mapExpand(stuff)), sp(rini),
			lw.expandForms(L(sCall, topRef("setindex!"), arr, r, sp(newIdxs))),
			list(sym("unnecessary"), r))
	case sDecl:
		// (= (:: T) rhs) is an error
		if isNull(cddr(lhs)) {
			errorf("invalid assignment location \"%s\"", deparse(lhs))
		}
		x, T, rhs := cadr(lhs), caddr(lhs), caddr(e)
		ex, stmts := lw.removeArgumentSideEffects(x)
		if isConst {
			var val any = rhs
			if !underscoreSymbol(ex) {
				val = lw.convertForTypeDecl(rhs, T, true, nil)
			}
			return lw.expandForms(list(sym("const"), list(sAssign, ex, val)))
		}
		var decl []any
		if !underscoreSymbol(ex) {
			decl = list(list(sym("decl"), ex, T))
		}
		return lw.expandForms(L(sBlock, sp(stmts), sp(decl), maybeWrapConst(list(sAssign, ex, rhs))))
	case sym("vcat"), sym("ncat"):
		errorf("use \"(a, b) = ...\" to assign multiple values")
	}
	errorf("invalid assignment location \"%s\"", deparse(lhs))
	return nil
}

// removeArgumentSideEffects makes an expression safe for multiple
// evaluation: (expr, assignments).
func (lw *lowerer) removeArgumentSideEffects(e any) (any, []any) {
	if !isPair(e) {
		return e, nil
	}
	var a []any
	var argToTemp func(x any) any
	argToTemp = func(x any) any {
		switch {
		case effectFree(x):
			return x
		case car(x) == sym("..."):
			return list(sym("..."), argToTemp(cadr(x)))
		case car(x) == sym("kw"):
			return list(sym("kw"), cadr(x), argToTemp(caddr(x)))
		case car(x) == sym("parameters"):
			return cons(sym("parameters"), mapL(argToTemp, cdr(x)))
		}
		g := lw.makeSSAValue()
		a = cons(list(sAssign, g, x), a)
		return g
	}
	if car(e) == sym("let") {
		r := argToTemp(e)
		return r, reverse(a)
	}
	r := cons(car(e), mapL(argToTemp, cdr(e)))
	return r, reverse(a)
}

func (lw *lowerer) lowerKwCall(f any, args []any) any {
	var para []any
	if hasParameters(args) {
		para = cdar(args)
		args = cdr(args)
	}
	callEx, stmts := lw.removeArgumentSideEffects(L(sCall, f, sp(args)))
	fexpr := cadr(callEx)
	cargs := cddr(callEx)
	kws, pargs := separate(kwarg, cargs)
	return L(sBlock, sp(stmts), lw.lowerKwCall_(fexpr, appendL(kws, para), pargs))
}

func (lw *lowerer) lowerKwCall_(fexpr any, kw, pa []any) any {
	var f any = fexpr
	if !symRef(fexpr) {
		f = lw.makeSSAValue()
	}
	kwContainer := lw.makeSSAValue()
	var init []any
	if !eq(f, fexpr) {
		init = list(list(sAssign, f, fexpr))
	}
	nt := lw.lowerNamedTuple(kw, func(name any) string {
		return "keyword argument \"" + symString(name) + "\" repeated in call to \"" + deparse(fexpr) + "\""
	}, "keyword argument", "keyword argument syntax", true)
	var call any
	if everyL(vararg, kw) {
		call = list(sym("if"), list(sCall, topRef("isempty"), kwContainer),
			L(sCall, f, sp(pa)),
			L(sCall, coreRef("kwcall"), kwContainer, f, sp(pa)))
	} else {
		call = L(sCall, coreRef("kwcall"), kwContainer, f, sp(pa))
	}
	return L(sBlock, sp(init), list(sAssign, kwContainer, nt), call)
}

// expandUpdateOperator_ converts `a+=b` to `a=a+b`.
func (lw *lowerer) expandUpdateOperator_(op, opEq, lhs, rhs any, declT []any) any {
	newlhs, stmts := lw.removeArgumentSideEffects(lhs)
	var temp any
	if opEq == sym(".=") && isPair(newlhs) && !(car(newlhs) == sym("ref") || (car(newlhs) == sym(".") && lengthEq(newlhs, 3))) {
		temp = lw.makeSSAValue()
		stmts = append(append([]any{}, stmts...), list(sAssign, temp, newlhs))
		newlhs = temp
	}
	if is(lhs, "tuple") {
		a, b := cdr(newlhs), cdr(lhs)
		for i := range a {
			// a replaced expression cannot be updated by assignment (#30062)
			if ssavalue(a[i]) && !ssavalue(b[i]) {
				errorf("invalid multiple assignment location \"%s\"", deparse(b[i]))
			}
		}
	}
	if len(declT) == 0 {
		return L(sBlock, sp(stmts), list(opEq, newlhs, list(sCall, op, newlhs, rhs)))
	}
	return L(sBlock, sp(stmts), list(opEq, newlhs, list(sCall, op, list(sDecl, newlhs, car(declT)), rhs)))
}

func (lw *lowerer) partiallyExpandRef(e any) any {
	a, idxs := cadr(e), cddr(e)
	reuse := isPair(a) && contains(func(x any) bool { return x == sym("begin") || x == sym("end") }, idxs)
	arr := a
	var stmts []any
	if reuse {
		arr = lw.makeSSAValue()
		stmts = list(list(sAssign, arr, a))
	}
	newIdxs, stuff := lw.processIndices(arr, idxs)
	return L(sBlock, sp(appendL(stmts, stuff)), L(sCall, topRef("getindex"), arr, sp(newIdxs)))
}

func (lw *lowerer) expandUpdateOperator(op, opEq, lhs, rhs any, declT []any) any {
	switch {
	case is(lhs, "ref"):
		// expand indexing inside op= first, to remove begin, end and ":"
		ex := lw.partiallyExpandRef(lhs)
		stmts := butlast(cdr(ex))
		refex := last(cdr(ex))
		nuref := L(S("ref"), caddr(refex), sp(cdddr(refex)))
		return L(sBlock, sp(stmts), lw.expandUpdateOperator_(op, opEq, nuref, rhs, declT))
	case is(lhs, sDecl):
		// (+= (:: x T) rhs)
		ex, stmts := lw.removeArgumentSideEffects(cadr(lhs))
		T := caddr(lhs)
		return L(sBlock, sp(stmts), lw.expandUpdateOperator(op, opEq, ex, rhs, list(T)))
	}
	if isPair(lhs) && opEq == sAssign && !inList(car(lhs), sym("."), sym("globalref"), sym("tuple"), sym("vcat"), sym("ncat"), sym("typed_hcat"), sym("typed_vcat"), sym("typed_ncat")) {
		errorf("invalid assignment location \"%s\"", deparse(lhs))
	}
	return lw.expandUpdateOperator_(op, opEq, lhs, rhs, declT)
}

func (lw *lowerer) lowerUpdateOp(e []any) any {
	s := symString(car(e))
	opEq := sAssign
	if s[0] == '.' {
		opEq = ".="
	}
	return lw.expandForms(lw.expandUpdateOperator(sym(s[:len(s)-1]), opEq, cadr(e), caddr(e), nil))
}

func expandAnd(e any) any {
	tail := cdr(flattenEx("&&", e))
	var loop func(tail []any) any
	loop = func(tail []any) any {
		switch len(tail) {
		case 0:
			return fTrue()
		case 1:
			return car(tail)
		}
		return list(sym("if"), car(tail), loop(cdr(tail)), fFalse())
	}
	return loop(tail)
}

func (lw *lowerer) expandOr(e any) any {
	tail := cdr(flattenEx("||", e))
	var loop func(tail []any) any
	loop = func(tail []any) any {
		switch len(tail) {
		case 0:
			return fFalse()
		case 1:
			return car(tail)
		}
		if symbolLike(car(tail)) {
			return list(sym("if"), car(tail), car(tail), loop(cdr(tail)))
		}
		g := lw.makeSSAValue()
		return list(sBlock, list(sAssign, g, car(tail)), list(sym("if"), g, g, loop(cdr(tail))))
	}
	return loop(tail)
}

func (lw *lowerer) expandFor(lhss, itrs []any, body any) any {
	outer := func(x any) bool { return is(x, "outer") }
	var cv []any
	for _, x := range butlast(lhss) {
		if !outer(x) {
			cv = append(cv, lhsVars(x)...)
		}
	}
	copiedVars := deleteDuplicates(filterNotUnderscore(cv))
	var nest func(lhss, itrs []any) any
	nest = func(lhss, itrs []any) any {
		if len(lhss) == 0 {
			return body
		}
		coll := lw.makeSSAValue()
		next := lw.st.gensyms.gensy()
		state := lw.makeSSAValue()
		isOuter := outer(car(lhss))
		lhs := car(lhss)
		if isOuter {
			lhs = cadar(lhss)
		}
		var locals []any
		if !isOuter {
			locals = mapL(func(v any) any { return list(sym("local"), v) }, lhsVars(lhs))
		}
		b := L(sBlock, sp(locals), lw.lowerTupleAssignment(list(lhs, state), next, nil), nest(cdr(lhss), cdr(itrs)))
		if len(cdr(lhss)) == 0 {
			b = list(sym("break-block"), sym("loop-cont"),
				list(sym("soft-let"), L(sBlock, sp(mapL(func(v any) any { return list(sAssign, v, v) }, copiedVars))), b))
		} else {
			b = list(sym("scope-block"), b)
		}
		var req []any
		if isOuter {
			req = list(list(sym("require-existing-local"), lhs))
		}
		return L(sBlock, list(sAssign, coll, car(itrs)),
			list(sym("local"), next),
			list(sAssign, next, list(sCall, topRef("iterate"), coll)),
			sp(req),
			list(sym("if"), list(sCall, topRef("not_int"), list(sCall, coreRef("==="), next, null())),
				list(sym("_do_while"),
					list(sBlock, b, list(sAssign, next, list(sCall, topRef("iterate"), coll, state))),
					list(sCall, topRef("not_int"), list(sCall, coreRef("==="), next, null())))))
	}
	return list(sym("break-block"), sym("loop-exit"), nest(lhss, itrs))
}

// lowerTupleAssignment converts (lhss...) = x to tuple indexing; wrap may
// be nil.
func (lw *lowerer) lowerTupleAssignment(lhss []any, x any, wrap func(any, int) any) any {
	if wrap == nil {
		wrap = func(x any, _ int) any { return x }
	}
	t := lw.makeSSAValue()
	out := []any{sBlock, list(sAssign, t, x)}
	for k, l := range lhss {
		i := int64(k + 1)
		if eventuallyCall(l) {
			// a function assignment: a capture-able variable, not an ssavalue
			temp := lw.st.gensyms.gensy()
			out = append(out, list(sBlock, list(sym("local-def"), temp),
				list(sAssign, temp, list(sCall, coreRef("getfield"), t, i)),
				wrap(list(sAssign, l, temp), k+1)))
		} else {
			out = append(out, wrap(list(sAssign, l, list(sCall, coreRef("getfield"), t, i)), k+1))
		}
	}
	return append(out, t)
}

func (lw *lowerer) expandCondition(cnd any) any {
	blk := is(cnd, sBlock)
	var stmts []any
	test := cnd
	if blk {
		stmts = cdr(butlast(cnd))
		test = last(cnd)
	}
	if isPair(test) && inList(car(test), sym("&&"), sym("||")) {
		clauses := cons(car(test), lw.mapExpand(cdr(flattenEx(car(test).(sym), test))))
		var cl any = clauses
		if len(cdr(clauses)) == 0 {
			if car(clauses) == sym("&&") {
				cl = fTrue()
			} else {
				cl = fFalse()
			}
		}
		if blk {
			return L(sBlock, sp(lw.mapExpand(stmts)), cl)
		}
		return cl
	}
	return lw.expandForms(cnd)
}

func (lw *lowerer) expandWhere(body, v any) any {
	bounds := analyzeTypevar(v)
	n := car(bounds)
	return list(sym("let"), list(sAssign, n, boundsToTypeVar(bounds, false)), list(sCall, coreRef("UnionAll"), n, body))
}

func (lw *lowerer) expandWheres(body any, vars []any) any {
	if len(vars) == 0 {
		return body
	}
	return lw.expandWhere(lw.expandWheres(body, cdr(vars)), car(vars))
}

// extractImplicitWhereparams turns Foo{<:Bar} into Foo{T} where T<:Bar.
func (lw *lowerer) extractImplicitWhereparams(e any) ([]any, []any) {
	var newparams, whereparams []any
	for _, p := range cddr(e) {
		if lengthEq(p, 2) && (car(p) == sym("<:") || car(p) == sym(">:")) {
			T := lw.st.gensyms.gensy()
			newparams = append(newparams, T)
			whereparams = append(whereparams, list(car(p), T, cadr(p)))
		} else {
			newparams = append(newparams, p)
		}
	}
	return newparams, whereparams
}

func namedTupleExpr(names, values []any) any {
	return list(sCall, list(sym("curly"), coreRef("NamedTuple"), cons(sym("tuple"), names)),
		// not the `tuple` head, so an assignment as a value is not another named tuple
		L(sCall, coreRef("tuple"), sp(values)))
}

// lowerNamedTuple is lower-named-tuple; dupErr nil is the default message.
func (lw *lowerer) lowerNamedTuple(lst []any, dupErr func(any) string, nameStr, syntaxStr string, callWithKw bool) any {
	if dupErr == nil {
		dupErr = func(name any) string { return "field name \"" + symString(name) + "\" repeated in named tuple" }
	}
	var names []any
	for _, x := range lst {
		switch {
		case isSymbol(x):
			names = append(names, x)
		case (assignment(x) || kwarg(x)) && isSymbol(cadr(x)):
			names = append(names, cadr(x))
		case lengthEq(x, 3) && car(x) == sym("."):
			names = append(names, cadr(caddr(x)))
		}
	}
	if d := hasDups(names); d != nil {
		errorf("%s", dupErr(car(d)))
	}
	notVararg := func(x any) any {
		if vararg(x) {
			errorf("\"...\" expression cannot be used as %s value", nameStr)
		}
		return x
	}
	toNt := func(n, v []any) any {
		if len(n) == 0 {
			return nil
		}
		return namedTupleExpr(reverse(mapL(quotify, n)), reverse(v))
	}
	merge := func(old, nw any) any {
		if old != nil {
			if nw != nil {
				return list(sCall, topRef("merge"), old, nw)
			}
			return old
		}
		return nw
	}
	var curNames, curVals []any
	var ex any
	for i, el := range lst {
		switch {
		case assignment(el) || kwarg(el):
			if !isSymbol(cadr(el)) {
				errorf("invalid %s name \"%s\"", nameStr, deparse(cadr(el)))
			}
			notVararg(caddr(el))
			curNames = cons(cadr(el), curNames)
			curVals = cons(caddr(el), curVals)
		case isSymbol(el):
			// x  =>  x = x
			curNames = cons(el, curNames)
			curVals = cons(el, curVals)
		case lengthEq(el, 3) && car(el) == sym(".") && quotedSym(caddr(el)):
			// a.x  =>  x = a.x
			curNames = cons(cadr(caddr(el)), curNames)
			curVals = cons(el, curVals)
		case lengthEq(el, 4) && car(el) == sCall && cadr(el) == sym("=>"):
			ex = merge(merge(ex, toNt(curNames, curVals)), namedTupleExpr(list(caddr(el)), list(notVararg(cadddr(el)))))
			curNames, curVals = nil, nil
		case vararg(el):
			current := merge(ex, toNt(curNames, curVals))
			if current != nil {
				ex = merge(current, cadr(el))
			} else {
				ex = list(sCall, topRef("merge"), list(sCall, topRef("NamedTuple")), cadr(el))
			}
			curNames, curVals = nil, nil
		case callWithKw && hasParameters(lst[i:]):
			errorf("more than one semicolon in argument list")
		default:
			errorf("invalid %s \"%s\"", syntaxStr, deparse(el))
		}
	}
	if r := merge(ex, toNt(curNames, curVals)); r != nil {
		return r
	}
	// the named tuple is empty
	return list(sCall, coreRef("NamedTuple"))
}

func (lw *lowerer) expandVcat(e any, vcat, hvcat, hvcatRows []any) any {
	a := cdr(e)
	if anyL(assignment, a) {
		errorf("misplaced assignment statement in \"%s\"", deparse(e))
	}
	if hasParameters(a) {
		errorf("unexpected semicolon in array expression")
	}
	if anyL(func(x any) bool { return is(x, "row") }, a) {
		// nested hcat inside vcat becomes hvcat
		rows := mapL(func(x any) any {
			if is(x, "row") {
				return cdr(x)
			}
			return list(x)
		}, a)
		if anyL(func(row any) bool { return anyL(vararg, asList(row)) }, rows) {
			// splatting inside hvcat: each row a separate tuple (#38844)
			return lw.expandForms(L(sCall, sp(hvcatRows), sp(mapL(func(x any) any { return cons(sym("tuple"), asList(x)) }, rows))))
		}
		var all []any
		for _, r := range rows {
			all = append(all, asList(r)...)
		}
		return lw.expandForms(L(sCall, sp(hvcat), cons(sym("tuple"), mapL(func(r any) any { return int64(length(r)) }, rows)), sp(all)))
	}
	return lw.expandForms(L(sCall, sp(vcat), sp(a)))
}

// lowerCcall inserts calls to convert() in ccall, and pulls out the
// expressions that might need to be rooted before conversion.
func (lw *lowerer) lowerCcall(name, RT any, atypes, args []any, cconv any, nreq any) any {
	F, A := atypes, args
	var stmts, T, C, GC []any
	for {
		if len(F) == 0 && len(A) > 0 {
			errorf("more arguments than types for ccall")
		}
		if len(A) == 0 && !(len(F) == 0 || (vararg(car(F)) && len(cdr(F)) == 0)) {
			errorf("more types than arguments for ccall")
		}
		isseq := len(F) > 0 && vararg(car(F))
		if isseq && len(T) == 0 {
			errorf("C ABI prohibits vararg without one required argument")
		}
		if len(A) == 0 {
			n := nreq
			if !truthy(n) {
				if isseq {
					n = int64(len(atypes) - 1)
				} else {
					n = int64(0)
				}
			}
			return L(sBlock, sp(stmts),
				L(S("foreigncall"), lw.expandForms(name), lw.expandForms(RT), L(sCall, coreRef("svec"), sp(T)),
					n, list(sQuote, cconv), sp(C), sp(GC)))
		}
		a := lw.expandForms(car(A))
		var tyx any = car(F)
		if isseq {
			tyx = cadar(F)
		}
		ty := lw.expandForms(tyx)
		if isseq && len(cdr(F)) > 0 {
			errorf("only the trailing ccall argument type should have \"...\"")
		}
		nextF := cdr(F)
		if isseq {
			nextF = F
		}
		if ty == sym("Any") {
			F, A = nextF, cdr(A)
			T = append(T, coreRef("Any"))
			C = append(C, a)
			continue
		}
		g := lw.makeSSAValue()
		stmts = append(stmts, list(sAssign, g, list(sCall, topRef("cconvert"), ty, a)))
		F, A = nextF, cdr(A)
		T = append(T, ty)
		C = append(C, list(sCall, topRef("unsafe_convert"), ty, g))
		GC = append([]any{g}, GC...)
	}
}

func justArglist(ex any) bool {
	return isPair(ex) && (inList(car(ex), sym("tuple"), sBlock, sym("...")) ||
		(car(ex) == sym("where") && justArglist(cadr(ex))))
}

func (lw *lowerer) expandFunctionDef(e []any) any {
	if justArglist(cadr(e)) {
		return lw.expandForms(cons(sym("->"), cdr(e)))
	}
	return lw.expandFunctionDef_(e)
}

// flattenWhereExpr converts (where (where x S) T) to (where x T S).
func flattenWhereExpr(e any) []any {
	var vars []any
	ex := e
	for is(ex, "where") {
		vars = appendL(reverse(cddr(ex)), vars)
		ex = cadr(ex)
	}
	return L(S("where"), ex, sp(reverse(vars)))
}

func (lw *lowerer) lowerDesctructuringArgs(argl []any) ([]any, []any) {
	checkLhs := func(a any) any {
		if exprContainsP(func(e any) bool { return isDecl(e) || assignment(e) || kwarg(e) }, a, nil) {
			errorf("invalid argument destructuring syntax \"%s\"", deparse(a))
		}
		return a
	}
	var transform func(a any) (any, any)
	transform = func(a any) (any, any) {
		switch {
		case is(a, "tuple"):
			a2 := lw.st.gensyms.gensy()
			return a2, list(sym("local"), list(sAssign, checkLhs(a), a2))
		case (isDecl(a) && lengthEq(a, 3)) || kwarg(a):
			x, st := transform(cadr(a))
			return list(car(a), x, caddr(a)), st
		case vararg(a):
			x, st := transform(cadr(a))
			return list(sym("..."), x), st
		}
		return a, nil
	}
	var newa, stmts []any
	for _, a := range argl {
		x, st := transform(a)
		newa = append(newa, x)
		if st != nil {
			stmts = append(stmts, st)
		}
	}
	if len(stmts) > 0 {
		// return `nothing` from the assignments (#26518)
		stmts = append(stmts, null())
	}
	return newa, stmts
}

func arglistUnshift(sig []any, item any) []any {
	if isPair(sig) && is(car(sig), "parameters") {
		return L(car(sig), item, sp(cdr(sig)))
	}
	return cons(item, sig)
}

func (lw *lowerer) expandFunctionDef_(e []any) any {
	name := cadr(e)
	var where []any
	hasWhere := false
	if is(name, "where") {
		w := flattenWhereExpr(name)
		where, hasWhere = cddr(w), true
		if !(isPair(cadr(w)) && inList(caadr(w), sCall, sDecl)) {
			errorf("invalid assignment location \"%s\"", deparse(name))
		}
		name = cadr(w)
	}
	dcl := is(name, sDecl)
	var rett any = coreRef("Any")
	if dcl {
		rett = caddr(name)
		name = cadr(name)
	}
	switch {
	case lengthEq(e, 2) && (isSymbol(name) || globalref(name)):
		if !validName(name) {
			errorf("invalid function name \"%s\"", symString(name))
		}
		if globalref(name) {
			return list(sBlock, list(sym("global"), name), list(sym("method"), name))
		}
		return list(sBlock, list(sym("global-if-global"), name), list(sym("method"), name))
	case !isPair(name):
		return e
	case car(name) == sCall:
		_ = hasWhere
		sparams := mapL(func(t any) any { return analyzeTypevar(t) }, where)
		argl := cdr(name)
		// strip @nospecialize
		annotations := mapL(func(a any) any { return list(sym("meta"), cadr(a), argName(caddr(a))) },
			filterL(func(a any) bool { return nospecializeMeta(a, false) }, argl))
		body := insertAfterMeta(caddr(e), annotations)
		argl = mapL(func(a any) any {
			if nospecializeMeta(a, false) {
				return caddr(a)
			}
			return a
		}, argl)
		argl, stmts := lw.lowerDesctructuringArgs(argl)
		fname := checkDotop(car(argl))
		argname := fname
		if overlay(fname) {
			argname = caddr(fname)
		}
		adjDecl := func(n any) any {
			if isDecl(n) && lengthEq(n, 2) {
				return list(sDecl, sym("#self#"), cadr(n))
			}
			return n
		}
		var farg any
		if isDecl(argname) {
			farg = adjDecl(argname)
		} else {
			farg = list(sDecl, sym("#self#"), list(sCall, coreRef("Typeof"), argname))
		}
		body = insertAfterMeta(body, stmts)
		rest := cdr(argl)
		unused := !anyL(kwarg, rest) && !(isPair(rest) && is(car(rest), "parameters"))
		fullArgl := lw.fixArglist(arglistUnshift(rest, farg), unused)
		var n any = fname
		if isDecl(fname) || (isPair(fname) && inList(car(fname), sym("curly"), sym("where"))) {
			n = false
		}
		return lw.expandForms(lw.methodDefExpr(n, sparams, fullArgl, body, rett))
	}
	errorf("invalid assignment location \"%s\"", deparse(name))
	return nil
}

// tupleToArglist is tuple-to-arglist (julia-parser.scm).
func tupleToArglist(e any) []any {
	eqToKw := func(x any) any {
		if assignment(x) {
			return cons(sym("kw"), cdr(x))
		}
		return x
	}
	switch car(e) {
	case sym("tuple"):
		return mapL(eqToKw, cdr(e))
	case sBlock:
		switch {
		case lengthEq(e, 1):
			return nil
		case lengthEq(e, 2):
			return list(eqToKw(cadr(e)))
		case lengthEq(e, 3):
			return list(list(sym("parameters"), eqToKw(caddr(e))), eqToKw(cadr(e)))
		}
		errorf("more than one semicolon in argument list")
	}
	return list(eqToKw(e))
}

func (lw *lowerer) expandArrow(e []any) any {
	a := cadr(e)
	body := caddr(e)
	var where []any
	hasWhere := false
	if is(a, "where") {
		w := flattenWhereExpr(a)
		where, hasWhere = cddr(w), true
		a = cadr(w)
	}
	var argl []any
	if isPair(a) {
		argl = tupleToArglist(filterL(func(x any) bool { return !linenum(x) }, asList(a)))
	} else {
		argl = list(a)
	}
	name := sym("#" + lw.moduleCounter(nil))
	var sig any
	if hasWhere {
		sig = L(S("where"), L(sCall, name, sp(argl)), sp(where))
	} else {
		sig = L(sCall, name, sp(argl))
	}
	return lw.expandForms(list(sBlock, list(sym("local"), name), list(sym("function"), sig, body)))
}

func (lw *lowerer) functionArglist(e any) []any {
	switch car(e) {
	case sym("function"):
		if justArglist(cadr(e)) {
			return lw.functionArglist(cons(sym("->"), cdr(e)))
		}
		name := cadr(e)
		if is(name, sDecl) {
			name = cadr(name)
		}
		return cddr(name)
	case sym("->"):
		a := cadr(e)
		if isPair(a) {
			return tupleToArglist(filterL(func(x any) bool { return !linenum(x) }, asList(a)))
		}
		return list(a)
	}
	return nil
}

func letBinds(e any) []any {
	if is(cadr(e), sBlock) {
		return cdr(cadr(e))
	}
	return list(cadr(e))
}

func (lw *lowerer) expandLet(e []any, hard bool) any {
	ex := caddr(e)
	binds := letBinds(e)
	var hs []any
	if hard {
		hs = list(list(sym("hardscope")))
	}
	if len(binds) == 0 {
		return lw.expandForms(list(sym("scope-block"), L(sBlock, sp(hs), ex)))
	}
	blk := ex
	for _, b := range reverse(binds) {
		switch {
		case symdecl(b):
			// just a symbol: a local
			blk = list(sym("scope-block"), L(sBlock, sp(hs), list(sym("local"), b), blk))
		case lengthEq(b, 3) && car(b) == sAssign:
			switch {
			case eventuallyCall(cadr(b)):
				// f() = c
				name := assignedName(cadr(b))
				if !isSymbol(name) {
					errorf("invalid let syntax")
				}
				var decl any
				if exprContainsEq(name, caddr(b)) {
					decl = list(sym("local"), name) // might need a Box for recursive functions
				} else {
					decl = list(sym("local-def"), name)
				}
				blk = list(sym("scope-block"), L(sBlock, sp(hs), decl, b, blk))
			case symdecl(cadr(b)):
				vname := declVar(cadr(b))
				tmp := lw.makeSSAValue()
				blk = list(sBlock, list(sAssign, tmp, caddr(b)),
					list(sym("scope-block"), L(sBlock, sp(hs), list(sym("local-def"), cadr(b)), list(sAssign, vname, tmp), blk)))
			case is(cadr(b), "tuple"):
				// (a, b, c, ...) = rhs
				vars := lhsVars(cadr(b))
				tmp := lw.makeSSAValue()
				blk = list(sBlock, list(sAssign, tmp, caddr(b)),
					list(sym("scope-block"), L(sBlock, sp(hs),
						sp(mapL(func(v any) any { return list(sym("local-def"), v) }, vars)),
						list(sAssign, cadr(b), tmp), blk)))
			default:
				errorf("invalid let syntax")
			}
		default:
			errorf("invalid let syntax")
		}
	}
	return lw.expandForms(blk)
}

func validModref(e any) bool {
	return lengthEq(e, 3) && car(e) == sym(".") && isPair(caddr(e)) &&
		inList(car(caddr(e)), sQuote, sInert) && isSymbol(cadr(caddr(e))) &&
		(isSymbol(cadr(e)) || validModref(cadr(e)))
}

func macroifyName(e any) any {
	switch {
	case isSymbol(e):
		return sym("@" + symString(e))
	case is(e, sQuote):
		return list(sQuote, macroifyName(cadr(e)))
	case is(e, sInert):
		return list(sInert, macroifyName(cadr(e)))
	case globalref(e):
		return list(sym("globalref"), cadr(e), macroifyName(caddr(e)))
	case validModref(e):
		return list(sym("."), cadr(e), macroifyName(caddr(e)))
	}
	errorf("invalid macro usage \"@(%s)\"", deparse(e))
	return nil
}

func validMacroDefName(e any) bool { return isSymbol(e) || validModref(e) || globalref(e) }

func (lw *lowerer) expandMacroDef(e []any) any {
	switch {
	case is(cadr(e), sCall) && validMacroDefName(cadr(cadr(e))):
		anames := removeEmptyParameters(cddr(cadr(e)))
		if hasParameters(anames) {
			errorf("macros cannot accept keyword arguments")
		}
		return lw.expandForms(L(S("function"),
			L(sCall, macroifyName(cadr(cadr(e))),
				list(sDecl, sym("__source__"), coreRef("LineNumberNode")),
				list(sDecl, sym("__module__"), coreRef("Module")),
				sp(mapL(func(v any) any {
					if isSymbol(v) {
						return list(sym("meta"), sym("nospecialize"), v)
					}
					return v
				}, anames))),
			sp(cddr(e))))
	case lengthEq(e, 2) && validMacroDefName(cadr(e)):
		return lw.expandForms(list(sym("function"), macroifyName(cadr(e))))
	}
	errorf("invalid macro definition")
	return nil
}

func findSymbolicLabels(h sym, e any, tbl *ftable) {
	if !isPair(e) || quoted(e) {
		return
	}
	if car(e) == h {
		tbl.put(cadr(e), true)
		return
	}
	for _, x := range asList(e) {
		findSymbolicLabels(h, x, tbl)
	}
}

func hasUnmatchedSymbolicGoto(e any) bool {
	refs, defs := newTable(), newTable()
	findSymbolicLabels("symbolicgoto", e, refs)
	findSymbolicLabels("symboliclabel", e, defs)
	return anyL(func(k any) bool { return !truthy(defs.get(k, false)) }, refs.keysList())
}

func (lw *lowerer) expandTry(e []any) any {
	tryb, v, catchb := cadr(e), caddr(e), cadddr(e)
	switch {
	case lengthGt(e, 4) && !equal(listRef(e, 4), fFalse()):
		if hasUnmatchedSymbolicGoto(tryb) {
			errorf("goto from a try/finally block is not permitted")
		}
		finalb := listRef(e, 4)
		var inner any
		if equal(catchb, fFalse()) && lengthEq(e, 5) {
			inner = list(sym("scope-block"), tryb)
		} else {
			inner = L(S("try"), tryb, v, catchb, fFalse(), sp(listTail(e, 5)))
		}
		return lw.expandForms(list(sym("tryfinally"), inner, list(sym("scope-block"), finalb)))
	case lengthGt(e, 3):
		if lengthGt(e, 6) {
			errorf("invalid \"try\" form")
		}
		var elseb []any
		if lengthEq(e, 6) {
			elseb = list(L(S("scope-block"), sp(listTail(e, 5))))
		}
		head := sym("trycatch")
		if len(elseb) > 0 {
			head = "trycatchelse"
		}
		var catchPart any
		if symbolLike(v) {
			catchPart = list(sym("scope-block"), list(sBlock, list(sAssign, v, list(sym("the_exception"))), catchb))
		} else {
			catchPart = list(sym("scope-block"), catchb)
		}
		return lw.expandForms(L(head, list(sym("scope-block"), tryb), list(sym("scope-block"), catchPart), sp(elseb)))
	}
	errorf("invalid \"try\" form")
	return nil
}

func (lw *lowerer) expandStructDef(e []any) any {
	mut, sig, flds := cadr(e), caddr(e), cdr(cadddr(e))
	for _, x := range flds {
		if eventuallyDecl(x) || linenum(x) {
			continue
		}
		if assignment(x) && eventuallyDecl(cadr(x)) {
			errorf("\"%s\" inside type definition is reserved", deparse(x))
		}
		break
	}
	name, params, super := analyzeTypeSig(sig)
	return lw.expandForms(lw.structDefExpr(name, params, super, flds, mut))
}

// analyzeTypeSig takes apart a type signature, e.g. T{X} <: S{Y}.
func analyzeTypeSig(ex any) (any, []any, any) {
	switch {
	case isSymbol(ex):
		return ex, nil, coreRef("Any")
	case is(ex, "curly") && isSymbol(cadr(ex)):
		return cadr(ex), cddr(ex), coreRef("Any")
	case is(ex, "<:") && lengthEq(ex, 3) && isSymbol(cadr(ex)):
		return cadr(ex), nil, caddr(ex)
	case is(ex, "<:") && lengthEq(ex, 3) && is(cadr(ex), "curly") && isSymbol(cadr(cadr(ex))):
		return cadr(cadr(ex)), cddr(cadr(ex)), caddr(ex)
	}
	errorf("invalid type signature")
	return nil, nil, nil
}
