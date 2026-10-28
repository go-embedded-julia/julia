package lower

// Pass 1 of julia-syntax.scm, continued: quasiquote (macroexpand.scm),
// broadcasting, generators, n-dimensional concatenation, destructuring,
// and type definitions.

type splatToken struct{}

var splat = &splatToken{}

// bqExpandArglist is bq-expand-arglist.
func (lw *lowerer) bqExpandArglist(lst []any, d int) []any {
	var out []any
	for _, x := range lst {
		nxt := lw.bqExpand_(x, d)
		if l, ok := nxt.([]any); ok && len(l) > 0 && l[0] == any(splat) {
			out = append(out, l[1:]...)
			continue
		}
		out = append(out, nxt)
	}
	return out
}

// bqExpand_ is julia-bq-expand-.
func (lw *lowerer) bqExpand_(x any, d int) any {
	switch {
	case isSymbol(x) || ssavalue(x):
		return list(sInert, x)
	case isAtomS(x):
		return x
	case inList(car(x), sTrue, sFalse):
		return x
	case d == 0 && car(x) == sym("$"):
		if lengthEq(x, 2) {
			if vararg(cadr(x)) {
				// splice expr ($ (... x))
				return list(sym("..."), cadr(cadr(x)))
			}
			return cadr(x)
		}
		// multiple expressions spliced into $, to splice in the next stage
		return cons(splat, cdr(x))
	case !contains(func(e any) bool { return is(e, "$") }, x):
		if car(x) == sLine {
			return list(sInert, x)
		}
		return list(sym("copyast"), list(sInert, x))
	}
	switch car(x) {
	case sInert:
		return L(sCall, coreRef("QuoteNode"), sp(lw.bqExpandArglist(cdr(x), d)))
	case sLine:
		return L(sCall, coreRef("LineNumberNode"), sp(lw.bqExpandArglist(cdr(x), d)))
	case sQuote:
		return L(sCall, coreRef("_expr"), sp(lw.bqExpandArglist(asList(x), d+1)))
	case sym("$"):
		return L(sCall, coreRef("_expr"), sp(lw.bqExpandArglist(asList(x), d-1)))
	}
	return L(sCall, coreRef("_expr"), sp(lw.bqExpandArglist(asList(x), d)))
}

// bqMacro is julia-bq-macro.
func (lw *lowerer) bqMacro(x any) any {
	e := lw.bqExpand_(x, 0)
	if l, ok := e.([]any); ok && len(l) > 0 && l[0] == any(splat) {
		return list(sym("error"), sstr("\"...\" expression outside call"))
	}
	return e
}

// broadcasting

func (lw *lowerer) refToView(ex any) any {
	switch {
	case is(ex, "ref"):
		e := lw.partiallyExpandRef(ex)
		stmts := butlast(cdr(e))
		refex := last(cdr(e))
		nuref := L(sCall, topRef("dotview"), caddr(refex), sp(cdddr(refex)))
		return L(sBlock, sp(stmts), nuref)
	case lengthEq(ex, 3) && car(ex) == sym("."):
		return list(sCall, topRef("dotgetproperty"), cadr(ex), caddr(ex))
	}
	return ex
}

// expandFuseBroadcast lazily fuses nested calls f.(args...) into one
// broadcast call, or a broadcast! call when lhs is not nil.
func (lw *lowerer) expandFuseBroadcast(lhs, rhs any) any {
	isFuse := func(e any) bool { return is(e, "fuse") }
	var dotToFuse func(e any, top bool) any
	makeFuse := func(f any, args []any, top bool) any {
		var kws, pargs []any
		sk := func(args, kwargs []any) {
			for _, a := range args {
				if kwarg(a) {
					kwargs = cons(a, kwargs)
				} else {
					pargs = cons(a, pargs)
				}
			}
			kws = kwargs
		}
		if hasParameters(args) {
			sk(reverse(cdr(args)), cdar(args))
		} else {
			sk(reverse(args), nil)
		}
		if len(kws) > 0 {
			kws = list(cons(sym("parameters"), kws))
		}
		fused := mapL(func(a any) any { return dotToFuse(a, false) }, pargs)
		name := "broadcasted"
		if len(kws) > 0 {
			name = "broadcasted_kwsyntax"
		}
		mk := L(sCall, topRef(name), sp(kws), f, sp(fused))
		if top {
			return cons(sym("fuse"), mk)
		}
		return mk
	}
	dotToFuse = func(e any, top bool) any {
		switch {
		case lengthEq(e, 3) && car(e) == sym("."):
			f, x := cadr(e), caddr(e)
			switch {
			case isAtomS(x) || inList(car(x), sQuote, sInert, sym("$")):
				return list(sCall, topRef("getproperty"), f, x)
			case car(x) == sym("tuple"):
				if identifierName(f) == sym("^") && lengthEq(x, 3) && isNumber(caddr(x)) {
					return makeFuse(topRef("literal_pow"),
						list(f, cadr(x), lw.expandForms(list(sCall, list(sCall, coreRef("apply_type"), topRef("Val"), caddr(x))))), top)
				}
				return makeFuse(f, cdr(x), top)
			}
			errorf("invalid syntax \"%s\"", deparse(e))
		case is(e, sCall):
			makeFuse_ := func(f any, x []any) any {
				if identifierName(f) == sym("^") && len(x) == 2 && isNumber(cadr(x)) {
					return makeFuse(topRef("literal_pow"),
						list(f, car(x), lw.expandForms(list(sCall, list(sCall, coreRef("apply_type"), topRef("Val"), cadr(x))))), top)
				}
				return makeFuse(f, x, top)
			}
			f := cadr(e)
			switch {
			case dotopNamed(f):
				return makeFuse_(undotop(f), cddr(e))
			case lengthEq(f, 2) && car(f) == sym("."):
				// (.+)(a, b) is (call (|.| +) a b), still fused
				return makeFuse_(cadr(f), cddr(e))
			}
			return e
		case is(e, "comparison"):
			return dotToFuse(lw.expandCompareChain(cdr(e)), top)
		case is(e, ".&&"):
			return makeFuse(topRef("andand"), cdr(e), top)
		case is(e, ".||"):
			return makeFuse(topRef("oror"), cdr(e), top)
		}
		return e
	}
	e := dotToFuse(rhs, true)
	var lhsView any
	if lhs != nil {
		lhsView = lw.refToView(lhs)
	}
	if isFuse(e) {
		if lhs == nil {
			return lw.expandForms(list(sCall, topRef("materialize"), cdr(e)))
		}
		return lw.expandForms(list(sCall, topRef("materialize!"), lhsView, cdr(e)))
	}
	if lhs == nil {
		return lw.expandForms(e)
	}
	return lw.expandForms(list(sCall, topRef("materialize!"), lhsView, list(sCall, topRef("broadcasted"), topRef("identity"), e)))
}

// generators

func (lw *lowerer) funcForGeneratorRanges(ex any, rangeExprs []any, flat bool, outervars []any) any {
	vars := mapL(cadr, rangeExprs)
	var argname any
	if len(vars) == 1 && isSymbol(car(vars)) {
		argname = car(vars)
	} else {
		argname = lw.st.gensyms.gensy()
	}
	myvars := lhsVars(cons(sym("tuple"), vars))
	var splatS []any
	switch {
	case eq(argname, car(vars)):
	case len(vars) == 1:
		splatS = L(sp(mapL(func(v any) any { return list(sym("local"), v) }, myvars)), list(sAssign, car(vars), argname))
	default:
		splatS = L(sp(mapL(func(v any) any { return list(sym("local"), v) }, myvars)), list(sAssign, cons(sym("tuple"), vars), argname))
	}
	switch {
	case eq(ex, argname):
		// identity for x->x
		return topRef("identity")
	case len(splatS) == 0 && lengthEq(ex, 3) && car(ex) == sCall && eq(caddr(ex), argname) &&
		underscoreSymbol(argname) && !dotopNamed(cadr(ex)) && !exprContainsEq(argname, cadr(ex)):
		// eta reduce _->f(_) => f
		return cadr(ex)
	}
	switch {
	case flat && is(ex, "generator"):
		ex = lw.expandGenerator(ex, false, deleteDuplicates(appendL(outervars, myvars)))
	case flat && is(ex, "flatten"):
		ex = lw.expandGenerator(cadr(ex), true, deleteDuplicates(appendL(outervars, myvars)))
	case len(outervars) > 0:
		ex = list(sym("let"), L(sBlock, sp(mapL(func(v any) any { return list(sAssign, v, v) }, filterNotUnderscore(outervars)))), ex)
	}
	return list(sym("->"), argname, L(sBlock, sp(splatS), ex))
}

func (lw *lowerer) expandGenerator(e any, flat bool, outervars []any) any {
	ex := cadr(e)
	filt := car(caddr(e)) == sym("filter")
	rangeExprs := cddr(e)
	if filt {
		rangeExprs = cddr(caddr(e))
	}
	ranges := mapL(caddr, rangeExprs)
	var iter any
	if len(ranges) == 1 {
		iter = car(ranges)
	} else {
		iter = L(sCall, topRef("product"), sp(ranges))
	}
	if filt {
		iter = list(sCall, topRef("Filter"), lw.funcForGeneratorRanges(cadr(caddr(e)), rangeExprs, false, nil), iter)
	}
	gen := list(sCall, topRef("Generator"), lw.funcForGeneratorRanges(ex, rangeExprs, flat, outervars), iter)
	if flat {
		return lw.expandForms(list(sCall, topRef("Flatten"), gen))
	}
	return lw.expandForms(gen)
}

// n-dimensional concatenation

func isRow(a any) bool { return isPair(a) && (car(a) == sym("row") || car(a) == sym("nrow")) }

func toInt(x any) int { return int(x.(int64)) }

func sumInts(xs []any) any {
	n := int64(0)
	for _, x := range xs {
		n += x.(int64)
	}
	return n
}

func (lw *lowerer) expandNcat(e any, hvncat []any) any {
	is1d := func(a []any) bool { return !anyL(isRow, a) }
	var getShape func(a []any, isRowFirst bool, d int) any
	getShape = func(a []any, isRowFirst bool, d int) any {
		zip := func(xss []any) []any {
			n := len(asList(xss[0]))
			for _, x := range xss {
				n = min(n, len(asList(x)))
			}
			out := make([]any, n)
			for i := range out {
				col := make([]any, len(xss))
				for j, x := range xss {
					col[j] = asList(x)[i]
				}
				out[i] = col
			}
			return out
		}
		getNext := func(x any) []any {
			switch {
			case !isRow(x) || (car(x) == sym("nrow") && d > 1+toInt(cadr(x))) || (car(x) == sym("row") && d > 1):
				return list(x)
			case car(x) == sym("nrow"):
				return cddr(x)
			}
			return cdr(x)
		}
		switch {
		case d == 0 || (!isRowFirst && d == 1):
			return int64(len(a))
		case isRowFirst && d == 3:
			return getShape(a, isRowFirst, d-1)
		}
		ashape := mapL(func(x any) any { return getShape(getNext(x), isRowFirst, d-1) }, a)
		if isPair(car(ashape)) {
			z := zip(ashape)
			rest := mapL(func(x any) any {
				var out []any
				for _, y := range asList(x) {
					out = append(out, asList(y)...)
				}
				return out
			}, cdr(z))
			return cons(sumInts(asList(car(z))), cons(car(z), rest))
		}
		return list(sumInts(ashape), ashape)
	}
	var getDims func(a []any, isRowFirst bool, d int) []any
	getDims = func(a []any, isRowFirst bool, d int) []any {
		switch {
		case d < 2 && !isRow(car(a)):
			return list(int64(len(a)))
		case d == 1:
			return list(car(getDims(cdar(a), isRowFirst, 0)), int64(len(a)))
		case d == 3 && isRowFirst:
			return getDims(a, isRowFirst, 2)
		}
		anext := list(car(a))
		if is(car(a), "nrow") && d == 1+toInt(cadar(a)) {
			anext = cddr(car(a))
		}
		return cons(int64(len(a)), getDims(anext, isRowFirst, d-1))
	}
	isBalanced := func(s []any) bool {
		total := int64(0)
		for _, x := range cdr(s) {
			y := car(x).(int64)
			for _, z := range asList(x) {
				total += z.(int64) - y
			}
		}
		return total == 0
	}
	errMix := func() {
		errorf("cannot mix space and ;; separators in an array expression, except to wrap a line")
	}
	// hasrowsFlatten returns (r . aflat): r is '() (unset), #t or #f.
	var hasrowsFlatten func(a []any) (any, []any)
	hasrowsFlatten = func(a []any) (any, []any) {
		var r any = []any{}
		var yt []any
		for _, x := range a {
			if isRow(x) {
				if car(x) == sym("nrow") {
					rinner, aflat := hasrowsFlatten(cddr(x))
					rr := r
					if isNull(orS(r, rinner)) {
						rr = andS(r, rinner)
					}
					if !isNull(rr) && (isNull(rinner) || (!truthy(rr) && truthy(rinner))) && (toInt(cadr(x)) == 2 && truthy(rr)) {
						errMix()
					}
					if toInt(cadr(x)) == 2 && isNull(rr) {
						r = false
					} else {
						r = rr
					}
					yt = appendL(aflat, yt)
				} else {
					if isNull(r) || truthy(r) {
						r = true
						yt = appendL(reverse(cdr(x)), yt)
					} else {
						errMix()
					}
				}
			} else {
				yt = cons(x, yt)
			}
		}
		return r, yt
	}
	tf := func(a bool) any {
		if a {
			return fTrue()
		}
		return fFalse()
	}
	tuplize := func(s []any) any {
		parts := cons(list(car(s)), cdr(s))
		return cons(sym("tuple"), reverse(mapL(func(x any) any { return cons(sym("tuple"), asList(x)) }, parts)))
	}
	d := cadr(e)
	a := cddr(e)
	r, raw := hasrowsFlatten(a)
	isRowFirst := false
	if !isNull(r) {
		isRowFirst = truthy(r)
	}
	aflat := reverse(raw)
	if anyL(assignment, aflat) {
		errorf("misplaced assignment statement in \"%s\"", deparse(e))
	}
	if hasParameters(aflat) {
		errorf("unexpected parameters in array expression")
	}
	if is1d(a) {
		return lw.expandForms(L(sCall, sp(hvncat), d, sp(aflat)))
	}
	if anyL(vararg, aflat) {
		errorf("Splatting ... in an hvncat with multiple dimensions is not supported")
	}
	shape := asList(getShape(a, isRowFirst, toInt(d)))
	if isBalanced(shape) {
		dims := cons(sym("tuple"), reverse(getDims(a, isRowFirst, toInt(d))))
		return lw.expandForms(L(sCall, sp(hvncat), dims, tf(isRowFirst), sp(aflat)))
	}
	return lw.expandForms(L(sCall, sp(hvncat), tuplize(shape), tf(isRowFirst), sp(aflat)))
}

// orS and andS are Scheme or/and on values where '() is truthy.
func orS(a, b any) any {
	if truthy(a) {
		return a
	}
	return b
}

func andS(a, b any) any {
	if !truthy(a) {
		return a
	}
	return b
}

// destructuring

func (lw *lowerer) maybeSSAValue(lhss []any, x any, inLhs func(any, []any) bool) any {
	switch {
	case (!inLhs(x, lhss) && isSymbol(x)) || ssavalue(x):
		return x
	case len(lhss) > 0 && vararg(last(lhss)) && eventuallyCall(cadr(last(lhss))):
		return lw.st.gensyms.gensy()
	}
	return lw.makeSSAValue()
}

func (lw *lowerer) expandPropertyDestruct(lhs []any, x any, wrap func(any) any) any {
	if len(lhs) != 1 {
		errorf("invalid assignment location \"%s\"", deparse(list(sym("tuple"), lhs)))
	}
	lhss := cdar(lhs)
	xx := lw.maybeSSAValue(lhss, x, func(x any, l []any) bool { return memq(x, l) != nil })
	var ini []any
	if !eq(x, xx) {
		ini = list(sinkAssignment(xx, lw.expandForms(x)))
	}
	fieldsEx := mapL(func(field any) any {
		var prop any
		switch {
		case isSymbol(field):
			prop = field
		case is(field, sDecl) && isSymbol(cadr(field)):
			prop = cadr(field)
		default:
			errorf("invalid assignment location \"%s\"", deparse(list(sym("tuple"), lhs)))
		}
		return lw.expandForms(wrap(list(sAssign, field, list(sCall, topRef("getproperty"), xx, list(sQuote, prop)))))
	}, lhss)
	return L(sBlock, sp(ini), sp(fieldsEx), list(sym("unnecessary"), xx))
}

func identityWrap(x any) any { return x }

// destructure_ is destructure-; end is the shared list of statements to run
// after the destructuring.
func (lw *lowerer) destructure_(i int, lhss []any, xx any, n int, st any, end *[]any, wrap func(any) any) []any {
	if len(lhss) == 0 {
		return nil
	}
	lhs := car(lhss)
	var lhs_ any
	switch {
	case isSymbol(lhs) || ssavalue(lhs):
		lhs_ = lhs
	case vararg(lhs):
		l := cadr(lhs)
		if isSymbol(l) || ssavalue(l) {
			lhs_ = lhs
		} else if eventuallyCall(l) {
			lhs_ = list(sym("..."), lw.st.gensyms.gensy())
		} else {
			lhs_ = list(sym("..."), lw.makeSSAValue())
		}
	case eventuallyCall(lhs):
		// no ssavalues for a function definition
		lhs_ = lw.st.gensyms.gensy()
	default:
		lhs_ = lw.makeSSAValue()
	}
	wrapSubassign := identityWrap
	if eq(lhs, lhs_) {
		wrapSubassign = wrap
	}
	wrapfirst := func(x any, i int) any {
		if i == 1 {
			return wrapSubassign(x)
		}
		return x
	}
	if vararg(lhs) && anyL(vararg, cdr(lhss)) {
		errorf("multiple \"...\" on lhs of assignment")
	}
	if !eq(lhs, lhs_) {
		if vararg(lhs) {
			*end = cons(lw.expandForms(wrap(list(sAssign, cadr(lhs), cadr(lhs_)))), *end)
		} else {
			*end = cons(lw.expandForms(wrap(list(sAssign, lhs, lhs_))), *end)
		}
	}
	var stArg []any
	if i != 1 {
		stArg = list(st)
	}
	if vararg(lhs_) {
		if i == n {
			if underscoreSymbol(cadr(lhs_)) {
				return nil
			}
			return list(lw.expandForms(wrapSubassign(list(sAssign, cadr(lhs_), L(sCall, topRef("rest"), xx, sp(stArg))))))
		}
		// the Scheme's (if (eventually-call? lhs) (gensy) (make-ssavalue)):
		// lhs is a (... x) here, never a call
		tail := lw.makeSSAValue()
		first := lw.expandForms(lw.lowerTupleAssignment(list(cadr(lhs_), tail),
			L(sCall, topRef("split_rest"), xx, int64(n-i), sp(stArg)), wrapfirst))
		return cons(first, lw.destructure_(1, cdr(lhss), tail, n-i, st, end, wrap))
	}
	var targets []any
	if i == n {
		targets = list(lhs_)
	} else {
		targets = list(lhs_, st)
	}
	first := lw.expandForms(lw.lowerTupleAssignment(targets, L(sCall, topRef("indexed_iterate"), xx, int64(i), sp(stArg)), wrapfirst))
	return cons(first, lw.destructure_(i+1, cdr(lhss), xx, n, st, end, wrap))
}

func (lw *lowerer) expandTupleDestruct(lhss []any, x any, wrap func(any) any) any {
	var sidesMatch func(l, r []any) bool
	sidesMatch = func(l, r []any) bool {
		switch {
		case len(l) == 0:
			return len(r) == 0
		case vararg(car(l)):
			return true
		case len(r) == 0:
			return false
		case vararg(car(r)):
			return len(cdr(r)) == 0
		}
		return sidesMatch(cdr(l), cdr(r))
	}
	if isPair(x) && len(lhss) > 0 && car(x) == sym("tuple") && !anyL(assignment, cdr(x)) &&
		!hasParameters(cdr(x)) && sidesMatch(lhss, cdr(x)) {
		// (a, b, ...) = (x, y, ...)
		return lw.expandForms(lw.tupleToAssignments(lhss, asList(x), wrap))
	}
	// like memq, but (... sym) is checked against sym
	var inLhs func(x any, lhss []any) bool
	inLhs = func(x any, lhss []any) bool {
		for _, l := range lhss {
			if is(l, "...") {
				return eq(cadr(l), x)
			}
			if eq(l, x) {
				return true
			}
		}
		return false
	}
	xx := lw.maybeSSAValue(lhss, x, inLhs)
	var ini []any
	if !eq(x, xx) {
		ini = list(sinkAssignment(xx, lw.expandForms(x)))
	}
	n := len(lhss)
	st := lw.st.gensyms.gensy()
	var end []any
	var decl []any
	if n > 0 {
		decl = list(list(sym("local"), st))
	}
	parts := lw.destructure_(1, lhss, xx, n, st, &end, wrap)
	return L(sBlock, sp(decl), sp(ini), sp(parts), sp(reverse(end)), list(sym("unnecessary"), xx))
}

// tupleToAssignments converts (lhss...) = (tuple ...) to assignments.
func (lw *lowerer) tupleToAssignments(lhss0, x []any, wrap func(any) any) any {
	lhss := lhss0
	assigned := lhss0
	rhss := cdr(x)
	var stmts, after, elts []any
	for len(lhss) > 0 {
		Lh := car(lhss)
		var R any = []any{}
		if len(rhss) > 0 {
			R = car(rhss)
		}
		var restR []any
		if len(rhss) > 0 {
			restR = cdr(rhss)
		}
		switch {
		case symbolLike(Lh) && (!isPair(R) || quoted(R) || equal(R, list(sNull))) &&
			// overwrite the variable at once if it does not occur elsewhere
			!contains(func(e any) bool { return eqSym(e, Lh) }, restR) &&
			!contains(func(e any) bool { return eqSym(e, R) }, assigned):
			lhss, assigned, rhss = cdr(lhss), cons(Lh, assigned), restR
			stmts = cons(wrap(makeAssignment(Lh, R)), stmts)
			elts = cons(R, elts)
		case vararg(Lh):
			if anyL(vararg, cdr(lhss)) {
				errorf("multiple \"...\" on lhs of assignment")
			}
			if len(cdr(lhss)) == 0 {
				var temp any
				if eventuallyCall(cadr(Lh)) {
					temp = lw.st.gensyms.gensy()
				} else {
					temp = lw.makeSSAValue()
				}
				return L(sBlock, sp(reverse(stmts)),
					list(sAssign, temp, cons(sym("tuple"), rhss)),
					sp(reverse(after)),
					wrap(list(sAssign, cadr(Lh), temp)),
					list(sym("unnecessary"), L(S("tuple"), sp(reverse(elts)), list(sym("..."), temp))))
			}
			lhssR := reverse(lhss)
			rhssR := reverse(rhss)
			var lhsTail, rhsTail []any
			for !(len(lhssR) == 0 || len(rhssR) == 0 || vararg(car(lhssR)) || vararg(car(rhssR))) {
				lhsTail = cons(car(lhssR), lhsTail)
				rhsTail = cons(car(rhssR), rhsTail)
				lhssR, rhssR = cdr(lhssR), cdr(rhssR)
			}
			var temp any
			if anyL(func(x any) bool { return eventuallyCall(x) || (vararg(x) && eventuallyCall(cadr(x))) }, lhssR) {
				temp = lw.st.gensyms.gensy()
			} else {
				temp = lw.makeSSAValue()
			}
			asg := makeAssignment(temp, cons(sym("tuple"), reverse(rhssR)))
			var assigns []any
			if isSymbol(temp) {
				assigns = list(list(sym("local-def"), temp), asg)
			} else {
				assigns = list(asg)
			}
			n := len(lhssR)
			st := lw.st.gensyms.gensy()
			end := after
			if len(lhssR) == 1 && vararg(car(lhssR)) {
				end = cons(wrap(list(sAssign, cadar(lhssR), temp)), end)
			} else {
				pre := assigns
				if n > 0 {
					pre = L(sp(assigns), list(sym("local"), st))
				}
				assigns = appendL(pre, lw.destructure_(1, reverse(lhssR), temp, n, st, &end, wrap))
			}
			assigned = appendL(mapL(func(x any) any {
				if vararg(x) {
					return cadr(x)
				}
				return x
			}, lhssR), assigned)
			lhss, rhss = lhsTail, rhsTail
			stmts = appendL(reverse(assigns), stmts)
			after = end
			elts = cons(list(sym("..."), temp), elts)
		case vararg(R):
			temp := lw.makeSSAValue()
			return L(sBlock, sp(reverse(stmts)),
				makeAssignment(temp, cadr(R)),
				sp(reverse(after)),
				wrap(list(sAssign, cons(sym("tuple"), lhss), temp)),
				list(sym("unnecessary"), L(S("tuple"), sp(reverse(elts)), list(sym("..."), temp))))
		default:
			var temp any
			if eventuallyCall(Lh) {
				temp = lw.st.gensyms.gensy()
			} else {
				temp = lw.makeSSAValue()
			}
			lhss, assigned, rhss = cdr(lhss), cons(Lh, assigned), restR
			if isSymbol(temp) {
				stmts = listStar(makeAssignment(temp, R), list(sym("local-def"), temp), stmts)
			} else {
				stmts = cons(makeAssignment(temp, R), stmts)
			}
			after = cons(wrap(makeAssignment(Lh, temp)), after)
			elts = cons(temp, elts)
		}
	}
	return L(sBlock, sp(reverse(stmts)), sp(reverse(after)), list(sym("unnecessary"), cons(sym("tuple"), reverse(elts))))
}

// type definitions

func (lw *lowerer) structDefExpr(name any, params []any, super any, flds []any, mut any) any {
	pnames, bounds := sparamNameBounds(params)
	return lw.structDefExpr_(name, pnames, bounds, super, asList(flattenBlocks(cons(sBlock, flds)))[1:], mut)
}

func numNonVarargs(args []any) int {
	n := 0
	for _, a := range args {
		if !vararg(a) {
			n++
		}
	}
	return n
}

// newCall is new-call: `new(...)` inside a constructor of Tname.
func (lw *lowerer) newCall(Tname any, typeParams, sparams, params, args, fieldNames, fieldTypes []any, selftype bool) any {
	if anyL(kwarg, args) {
		errorf("\"new\" does not accept keyword arguments")
	}
	nnv := numNonVarargs(typeParams)
	if !anyL(vararg, typeParams) && len(params) > nnv {
		errorf("too few type parameters specified in \"new{...}\"")
	}
	if nnv > len(params) {
		errorf("too many type parameters specified in \"new{...}\"")
	}
	var Texpr any
	switch {
	case len(typeParams) == 0:
		Texpr = list(sym("globalref"), list(sym("thismodule")), Tname)
	case selftype:
		Texpr = sym("#ctor-self#")
	default:
		Texpr = L(S("curly"), list(sym("globalref"), list(sym("thismodule")), Tname), sp(typeParams))
	}
	var tn any = Texpr
	if !isSymbol(Texpr) {
		tn = lw.makeSSAValue()
	}
	fieldConvert := func(fld int, fty, val any) any {
		if equal(fty, coreRef("Any")) {
			return val
		}
		// usually the fieldtype, not the fty expression
		var ty any
		if !selftype && equal(typeParams, params) && memq(fty, params) != nil && memq(fty, sparams) != nil {
			ty = fty
		} else {
			ty = list(sCall, coreRef("fieldtype"), tn, int64(fld+1))
		}
		return lw.convertForTypeDecl(val, ty, false, nil)
	}
	var init []any
	if !isSymbol(tn) {
		init = list(list(sAssign, tn, Texpr))
	}
	nf := int64(len(fieldNames))
	switch {
	case numNonVarargs(args) > len(fieldNames):
		return list(sCall, coreRef("throw"), list(sCall, topRef("ArgumentError"),
			sstr("new: too many arguments (expected "+symString(nf)+")")))
	case anyL(vararg, args):
		if everyL(func(ty any) bool { return equal(ty, coreRef("Any")) }, fieldTypes) {
			return list(sym("splatnew"), Texpr, L(sCall, coreRef("tuple"), sp(args)))
		}
		argt := lw.makeSSAValue()
		nfv := lw.makeSSAValue()
		var newArgs []any
		for i, fty := range listHead(fieldTypes, len(fieldNames)) {
			newArgs = append(newArgs, fieldConvert(i, fty, list(sCall, coreRef("getfield"), argt, int64(i+1), fFalse())))
		}
		return L(sBlock, sp(init),
			list(sAssign, argt, L(sCall, coreRef("tuple"), sp(args))),
			list(sAssign, nfv, list(sCall, coreRef("nfields"), argt)),
			list(sym("if"), list(sCall, topRef("ult_int"), nfv, nf),
				list(sCall, coreRef("throw"), list(sCall, topRef("ArgumentError"), sstr("new: too few arguments (expected "+symString(nf)+")")))),
			list(sym("if"), list(sCall, topRef("ult_int"), nf, nfv),
				list(sCall, coreRef("throw"), list(sCall, topRef("ArgumentError"), sstr("new: too many arguments (expected "+symString(nf)+")")))),
			L(S("new"), tn, sp(newArgs)))
	}
	var newArgs []any
	for i, fty := range listHead(fieldTypes, len(args)) {
		newArgs = append(newArgs, fieldConvert(i, fty, args[i]))
	}
	return L(sBlock, sp(init), L(S("new"), tn, sp(newArgs)))
}

// ctorSig converts X(...) to (|#ctor-self#|::Type{X})(...), or nil.
func ctorSig(s any) any {
	switch {
	case car(s) == sDecl || car(s) == sym("where"):
		s2 := ctorSig(cadr(s))
		if s2 == nil {
			return nil
		}
		return L(car(s), s2, sp(cddr(s)))
	case car(s) == sCall:
		h := cadr(s)
		if isDecl(h) {
			if cadr(h) == sym("#ctor-self#") {
				return s // already in the required form
			}
			return nil
		}
		return L(sCall, list(sDecl, sym("#ctor-self#"), list(sym("curly"), coreRef("Type"), h)), sp(cddr(s)))
	}
	return nil
}

type ctorBodyFn func(body any, typeParams, sparams []any, selftype bool) any

func ctorDef(name, Tname any, ctorBody ctorBodyFn, s, body any, wheres []any) any {
	curly := is(name, "curly")
	var curlyargs []any
	if curly {
		curlyargs = cddr(name)
		name = cadr(name)
	}
	sparams := mapL(func(w any) any { return car(analyzeTypevar(w)) }, wheres)
	if !eq(name, Tname) {
		// '() requires user-specified parameters with new{...} inside a
		// non-constructor inner definition
		return list(sym("function"), s, ctorBody(body, nil, sparams, false))
	}
	newsig := ctorSig(s)
	sg := newsig
	if sg == nil {
		sg = s
	}
	return list(sym("function"), sg, ctorBody(body, curlyargs, sparams, newsig != nil))
}

// matchCallSig matches (-$ (call name . sig) (|::| (call name . sig) _t)),
// returning name.
func matchCallSig(x any) (any, bool) {
	if is(x, sCall) && lengthGt(x, 1) {
		return cadr(x), true
	}
	if is(x, sDecl) && lengthEq(x, 3) && is(cadr(x), sCall) && lengthGt(cadr(x), 1) {
		return cadr(cadr(x)), true
	}
	return nil, false
}

// matchSelfTypeCall matches (call (|::| self (where (curly (core Type)
// name) . wheres)) . sig) and its typed form, returning name and wheres.
func matchSelfTypeCall(x any) (any, []any, bool) {
	c := x
	if is(x, sDecl) && lengthEq(x, 3) {
		c = cadr(x)
	}
	if !(is(c, sCall) && lengthGt(c, 1)) {
		return nil, nil, false
	}
	d := cadr(c)
	if !(is(d, sDecl) && lengthEq(d, 3)) {
		return nil, nil, false
	}
	w := caddr(d)
	if !(is(w, "where") && lengthGt(w, 1)) {
		return nil, nil, false
	}
	cu := cadr(w)
	if !(is(cu, "curly") && lengthEq(cu, 3) && equal(cadr(cu), coreRef("Type"))) {
		return nil, nil, false
	}
	return caddr(cu), cddr(w), true
}

// patternReplace is match.scm's pattern-replace with a function that
// returns nil when no pattern applies.
func patternReplace(apply func(any) any, ex any) any {
	if !isPair(ex) || inList(car(ex), sQuote, sInert) {
		return ex
	}
	if enew := apply(ex); enew != nil && !eq(enew, ex) {
		return enew
	}
	return mapL(func(sub any) any {
		if !isPair(sub) {
			return sub
		}
		return patternReplace(apply, sub)
	}, asList(ex))
}

// rewriteCtor rewrites the calls to new(...) of a constructor definition.
func (lw *lowerer) rewriteCtor(ctor, Tname any, params, fieldNames, fieldTypes []any) any {
	var ctorBody ctorBodyFn
	ctorBody = func(body any, typeParams, sparams []any, selftype bool) any {
		return patternReplace(func(x any) any {
			if !(is(x, sCall) && lengthGt(x, 1)) {
				return nil
			}
			args := cddr(x)
			rec := func() []any {
				return mapL(func(a any) any { return ctorBody(a, typeParams, sparams, selftype) }, args)
			}
			if cadr(x) == sym("new") {
				return lw.newCall(Tname, typeParams, sparams, params, rec(), fieldNames, fieldTypes, selftype)
			}
			if f := cadr(x); is(f, "curly") && cadr(f) == sym("new") {
				return lw.newCall(Tname, cddr(f), sparams, params, rec(), fieldNames, fieldTypes, false)
			}
			return nil
		}, body)
	}
	flat := patternReplace(func(x any) any {
		if is(x, "where") && is(cadr(x), "where") {
			return flattenWhereExpr(x)
		}
		return nil
	}, ctor)
	return patternReplace(func(x any) any {
		if !(isPair(x) && lengthEq(x, 3)) {
			return nil
		}
		h := car(x)
		sig, body := cadr(x), caddr(x)
		if h == sym("function") {
			if name, wheres, ok := matchSelfTypeCall(sig); ok {
				return ctorDef(name, Tname, ctorBody, sig, body, wheres)
			}
		}
		if h != sym("function") && h != sAssign {
			return nil
		}
		if name, ok := matchCallSig(sig); ok {
			return ctorDef(name, Tname, ctorBody, sig, body, nil)
		}
		if is(sig, "where") && lengthGt(sig, 1) {
			if name, ok := matchCallSig(cadr(sig)); ok {
				return ctorDef(name, Tname, ctorBody, sig, body, cddr(sig))
			}
		}
		return nil
	}, flat)
}

// ctorsMinInitialized is ctors-min-initialized; -1 is #f (no call to new).
func ctorsMinInitialized(ex any) int {
	if !isPair(ex) {
		return -1
	}
	m := -1
	take := func(v int) {
		if v >= 0 && (m < 0 || v < m) {
			m = v
		}
	}
	l := asList(ex)
	x := l[0]
	if is(x, sCall) && lengthGt(x, 1) {
		if cadr(x) == sym("new") {
			take(len(cddr(x)))
		}
		if f := cadr(x); is(f, "curly") && cadr(f) == sym("new") {
			take(len(cddr(x)))
		}
	}
	take(ctorsMinInitialized(x))
	take(ctorsMinInitialized(l[1:]))
	return m
}

func insertStructShim(fieldTypes []any, name any) []any {
	return mapL(func(x any) any {
		return exprReplace(func(y any) bool {
			return lengthEq(y, 3) && car(y) == sym(".") &&
				(equal(caddr(y), list(sQuote, name)) || equal(caddr(y), list(sInert, name)))
		}, x, func(y any) any {
			return list(sCall, coreRef("struct_name_shim"), cadr(y), caddr(y), list(sym("thismodule")), name)
		})
	}, fieldTypes)
}

func (lw *lowerer) structDefExpr_(name any, params, bounds []any, super any, fields0 []any, mut any) any {
	fieldsRaw, defs := separate(eventuallyDecl, fields0)
	var attrs []any
	fields := make([]any, len(fieldsRaw))
	for k, x := range fieldsRaw {
		n := int64(k + 1)
		for isPair(x) && !isDecl(x) {
			attrs = append(attrs, n, quotify(car(x)))
			x = cadr(x)
		}
		fields[k] = x
	}
	defs = filterL(func(x any) bool { return !(effectFree(x) || car(x) == sym("string")) }, defs)
	var loc any = list(sLine, int64(0), sym(""))
	if len(fields0) > 0 && linenum(car(fields0)) {
		loc = car(fields0)
	}
	fieldNames := mapL(declVar, fields)
	fieldTypes := mapL(declType, fields)
	minInit := int64(len(fields))
	if m := ctorsMinInitialized(defs); m >= 0 && int64(m) < minInit {
		minInit = int64(m)
	}
	hasprev := lw.makeSSAValue()
	prev := lw.makeSSAValue()
	newdef := lw.makeSSAValue()
	if d := hasDups(fieldNames); d != nil {
		errorf("duplicate field name: \"%s\" is not unique", symString(car(d)))
	}
	for _, v := range fieldNames {
		if !isSymbol(v) {
			errorf("field name \"%s\" is not a symbol", deparse(v))
		}
	}
	thisName := list(sym("globalref"), list(sym("thismodule")), name)
	var reuseParams []any
	if len(params) > 0 {
		var acc any = prev
		for range params {
			acc = list(sym("."), acc, list(sQuote, sym("body")))
		}
		reuseParams = list(list(sAssign, cons(sym("tuple"), params), list(sym("."), acc, list(sQuote, sym("parameters")))))
	}
	var assigns []any
	for i, n := range params {
		assigns = append(assigns, makeAssignment(n, boundsToTypeVar(bounds[i], true)))
	}
	var ctors any
	if len(defs) == 0 {
		ctors = list(sCall, coreRef("_defaultctors"), newdef, list(sInert, loc))
	} else {
		ctors = list(sym("scope-block"), L(sBlock, list(sym("hardscope")), list(sym("global"), name),
			sp(mapL(func(c any) any { return lw.rewriteCtor(c, name, params, fieldNames, fieldTypes) }, defs))))
	}
	return list(sBlock,
		list(sym("global"), name),
		list(sym("scope-block"), L(sBlock,
			list(sym("hardscope")),
			list(sym("local-def"), name),
			sp(mapL(func(v any) any { return list(sym("local"), v) }, params)),
			sp(assigns),
			list(sym("toplevel-only"), sym("struct"), thisName),
			list(sAssign, name, list(sCall, coreRef("_structtype"), list(sym("thismodule")), list(sInert, name),
				L(sCall, coreRef("svec"), sp(params)),
				L(sCall, coreRef("svec"), sp(mapL(quotify, fieldNames))),
				L(sCall, coreRef("svec"), sp(attrs)),
				mut, minInit)),
			list(sCall, coreRef("_setsuper!"), name, super),
			list(sAssign, hasprev, list(sym("&&"),
				list(sCall, coreRef("isdefinedglobal"), list(sym("thismodule")), list(sInert, name), fFalse()),
				list(sCall, coreRef("_equiv_typedef"), thisName, name))),
			list(sAssign, prev, list(sym("if"), hasprev, thisName, fFalse())),
			list(sym("if"), hasprev, L(sBlock, sp(reuseParams))),
			list(sAssign, newdef, list(sCall, coreRef("_typebody!"), prev, name, L(sCall, coreRef("svec"), sp(insertStructShim(fieldTypes, name))))),
			list(sym("const"), thisName, newdef),
			list(sym("latestworld")),
			null())),
		// always define constructors, even if the definition did not change
		ctors,
		list(sym("latestworld")),
		null())
}

func (lw *lowerer) typeDefScope(name any, params []any, kind sym, mk any, super any) any {
	pnames, bounds := sparamNameBounds(params)
	var assigns []any
	for i, n := range pnames {
		assigns = append(assigns, makeAssignment(n, boundsToTypeVar(bounds[i], true)))
	}
	thisName := list(sym("globalref"), list(sym("thismodule")), name)
	return list(sBlock,
		list(sym("global"), name),
		list(sym("scope-block"), L(sBlock,
			list(sym("local-def"), name),
			sp(mapL(func(v any) any { return list(sym("local"), v) }, pnames)),
			sp(assigns),
			list(sym("toplevel-only"), kind),
			list(sAssign, name, mk),
			list(sCall, coreRef("_setsuper!"), name, super),
			list(sCall, coreRef("_typebody!"), fFalse(), name),
			list(sym("if"), list(sym("&&"),
				list(sCall, coreRef("isdefinedglobal"), list(sym("thismodule")), list(sInert, name), fFalse()),
				list(sCall, coreRef("_equiv_typedef"), thisName, name)),
				null(),
				list(sym("const"), thisName, name)),
			list(sym("latestworld")),
			null())))
}

func (lw *lowerer) abstractTypeDefExpr(name any, params []any, super any) any {
	pnames, _ := sparamNameBounds(params)
	mk := list(sCall, coreRef("_abstracttype"), list(sym("thismodule")), list(sInert, name), L(sCall, coreRef("svec"), sp(pnames)))
	return lw.typeDefScope(name, params, "abstract_type", mk, super)
}

func (lw *lowerer) primitiveTypeDefExpr(n, name any, params []any, super any) any {
	pnames, _ := sparamNameBounds(params)
	mk := list(sCall, coreRef("_primitivetype"), list(sym("thismodule")), list(sInert, name), L(sCall, coreRef("svec"), sp(pnames)), n)
	return lw.typeDefScope(name, params, "primitive_type", mk, super)
}
