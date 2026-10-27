package lower

// Pass 3 of julia-syntax.scm: analyze variables (which are captured,
// assigned, declared).

func lambdaAllVars(e any) []any { return appendL(lamArgnames(e), asList(caddr(e))) }

// freeVars_ is free-vars-.
func freeVars_(e any, tab *ftable) *ftable {
	switch {
	case e == unusedSym || underscoreSymbol(e):
	case isSymbol(e):
		tab.put(e, true)
	case is(e, "globalref"):
	case is(e, "break-block"):
		freeVars_(caddr(e), tab)
	case is(e, "with-static-parameters"):
		freeVars_(cadr(e), tab)
	case isAtomS(e) || quoted(e):
	case car(e) == sLambda:
		bound := newTable()
		for _, b := range lambdaAllVars(e) {
			bound.put(b, true)
		}
		for _, v := range freeVars(lamBody(e)) {
			if !bound.has(v) {
				tab.put(v, true)
			}
		}
	default:
		for _, x := range cdr(e) {
			freeVars_(x, tab)
		}
	}
	return tab
}

// freeVars is free-vars: the keys in table.keys order.
func freeVars(e any) []any { return freeVars_(e, newTable()).keysList() }

func vinfoToTable(vi []any) *ftable {
	tab := newTable()
	for _, v := range vi {
		tab.put(car(v), v)
	}
	return tab
}

// analyzeVarsLambda is analyze-vars-lambda.
func (lw *lowerer) analyzeVarsLambda(e []any, env, captvars, sp, newSp []any, methsig any, tab *ftable) any {
	args := lamArgs(e)
	locl := asList(caddr(e))
	allv := appendL(mapL(argName, args), locl)
	fv0 := diff(freeVars(lamBody(e)), allv)
	// variables referenced in the declared types of free variables
	var dv []any
	for _, v := range fv0 {
		if vi := tab.get(v, false); truthy(vi) {
			dv = append(dv, freeVars(vinfoType(vi))...)
		}
	}
	fv := appendL(diff(dv, fv0), fv0)
	var sigFv []any
	if methsig != nil {
		sigFv = freeVars(methsig)
	}
	glo := findGlobalDecls(lamBody(e))
	vi := appendL(mapL(func(d any) any { return makeVarInfo(declVar(d)) }, args), mapL(makeVarInfo, locl))
	captSp := filterL(func(v any) bool {
		return (memq(v, fv) != nil && memq(v, glo) == nil && memq(v, newSp) == nil) || memq(v, sigFv) != nil
	}, sp)
	cv := appendL(filterL(func(v any) bool {
		n := vinfoName(v)
		return memq(n, fv) != nil && memq(n, newSp) == nil && memq(n, glo) == nil
	}, env), mapL(makeVarInfo, captSp))
	newEnv := appendL(vi, filterL(func(v any) bool {
		return memq(vinfoName(v), allv) == nil && memq(vinfoName(v), glo) == nil
	}, env))
	lw.analyzeVars(lamBody(e), newEnv, cv, deleteDuplicates(appendL(newSp, sp)), vinfoToTable(newEnv))
	for _, v := range cv {
		vinfoSetCapt(v, true)
	}
	e[2] = list(vi, cv, int64(0), deleteDuplicates(appendL(newSp, captSp)))
	return e
}

// analyzeVars is analyze-vars; it records, in place in each lambda, the
// list (var-info-lst captured-var-infos ssavalues static_params).
func (lw *lowerer) analyzeVars(e any, env, captvars, sp []any, tab *ftable) any {
	if isAtomS(e) || quoted(e) {
		if isSymbol(e) {
			if vi := tab.get(e, false); truthy(vi) {
				vinfoSetRead(vi, true)
			}
		}
		return e
	}
	switch car(e) {
	case sym("local-def"):
		// a local whose assignment dominates all its uses
		vinfoSetNeverUndef(tab.get(cadr(e), false), true)
	case sAssign, sym("const"):
		if isSymbol(cadr(e)) {
			if vi := tab.get(cadr(e), false); truthy(vi) {
				vinfoSetSa(vi, !vinfoAsgn(vi))
				vinfoSetAsgn(vi, true)
			}
		}
		lw.analyzeVars(caddr(e), env, captvars, sp, tab)
	case sCall:
		if vi := tab.get(cadr(e), false); truthy(vi) {
			vinfoSetCalled(vi, true)
		}
		// calls f(x...) go through _apply_iterate; calls with keywords through kwcall
		if lengthGt(e, 3) && (equal(cadr(e), coreRef("_apply_iterate")) || equal(cadr(e), coreRef("kwcall"))) {
			if vi2 := tab.get(cadddr(e), false); truthy(vi2) {
				vinfoSetCalled(vi2, true)
			}
		}
		for _, x := range cdr(e) {
			lw.analyzeVars(x, env, captvars, sp, tab)
		}
	case sym("decl"):
		// var::T: the type goes into the var-info record
		if vi := tab.get(cadr(e), false); truthy(vi) {
			if !equal(vinfoType(vi), coreRef("Any")) {
				errorf("multiple type declarations for \"%s\"", symString(cadr(e)))
			}
			if assq(cadr(e), captvars) != nil {
				errorf("type of \"%s\" declared in inner scope", symString(cadr(e)))
			}
			vinfoSetType(vi, caddr(e))
		}
	case sLambda:
		lw.analyzeVarsLambda(asList(e), env, captvars, sp, nil, nil, tab)
	case sym("with-static-parameters"):
		lw.analyzeVarsLambda(asList(cadr(e)), env, captvars, sp, cddr(e), nil, tab)
	case sym("method"):
		if lengthEq(e, 2) {
			if vi := tab.get(methodExprName(e), false); truthy(vi) {
				vinfoSetSa(vi, !vinfoAsgn(vi))
				vinfoSetAsgn(vi, true)
			}
			return e
		}
		lw.analyzeVars(caddr(e), env, captvars, sp, tab)
		lw.analyzeVarsLambda(asList(cadddr(e)), env, captvars, sp, methodExprStaticParameters(e), caddr(e), tab)
	case sym("module"), sym("toplevel"):
	default:
		for _, x := range cdr(e) {
			lw.analyzeVars(x, env, captvars, sp, tab)
		}
	}
	return e
}

func (lw *lowerer) analyzeVariables(e any) any {
	lw.analyzeVars(e, nil, nil, nil, newTable())
	return e
}
