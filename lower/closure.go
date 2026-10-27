package lower

// Pass 4 of julia-syntax.scm: closure conversion. Inner functions become
// methods of new struct types defined at top level; assignments to
// captured variables go through Boxes; global assignments get their
// binding-type conversion.

import "sort"

func (lw *lowerer) makeSSAValue() []any {
	n := lw.st.ssaCounter
	lw.st.ssaCounter++
	return list(sym("ssavalue"), int64(n))
}

func (lw *lowerer) typeForClosureParameterized(name any, P, names, fields, types []any, super any) []any {
	s := lw.makeSSAValue()
	body := []any{sBlock, list(sym("global"), name)}
	for i, p := range P {
		body = append(body, list(sAssign, p, list(sCall, coreRef("TypeVar"), list(sQuote, names[i]), coreRef("Any"))))
	}
	body = append(body,
		list(sAssign, s, list(sCall, coreRef("_structtype"), list(sym("thismodule")), list(sInert, name),
			L(sCall, coreRef("svec"), sp(P)),
			L(sCall, coreRef("svec"), sp(mapL(quotify, fields))),
			list(sCall, coreRef("svec")),
			fFalse(), int64(len(fields)))),
		list(sCall, coreRef("_setsuper!"), s, super),
		list(sym("const"), list(sym("globalref"), list(sym("thismodule")), name), s),
		list(sCall, coreRef("_typebody!"), fFalse(), s, L(sCall, coreRef("svec"), sp(types))),
		list(sReturn, null()))
	lam := list(sLambda, []any{}, list([]any{}, []any{}, int64(0), []any{}), body)
	return list(list(sym("thunk"), lw.linearize(lam)))
}

func (lw *lowerer) typeForClosure(name any, fields []any, super any) []any {
	s := lw.makeSSAValue()
	boxes := mapL(func(any) any { return coreRef("Box") }, fields)
	body := list(sBlock, list(sym("global"), name),
		list(sAssign, s, list(sCall, coreRef("_structtype"), list(sym("thismodule")), list(sInert, name),
			list(sCall, coreRef("svec")),
			L(sCall, coreRef("svec"), sp(mapL(quotify, fields))),
			list(sCall, coreRef("svec")),
			fFalse(), int64(len(fields)))),
		list(sCall, coreRef("_setsuper!"), s, super),
		list(sym("const"), list(sym("globalref"), list(sym("thismodule")), name), s),
		list(sCall, coreRef("_typebody!"), fFalse(), s, L(sCall, coreRef("svec"), sp(boxes))),
		list(sReturn, null()))
	lam := list(sLambda, []any{}, list([]any{}, []any{}, int64(0), []any{}), body)
	return list(list(sym("thunk"), lw.linearize(lam)))
}

func vinfoNotCapt(vi any) any {
	return list(car(vi), cadr(vi), vinfoFlags(vi)&^1)
}

func clearCaptureBits(vinfos []any) []any { return mapL(vinfoNotCapt, vinfos) }

// convertLambda is convert-lambda.
func (lw *lowerer) convertLambda(lam []any, fname any, interp bool, captSp []any, opaq *ftable, pms []any) []any {
	vi := lamVinfo(lam)
	body := lw.addBoxInitsToBody(lam, lw.clConvert(cadddr(lam), fname, lam, newTable(), newTable(), false, interp, opaq, pms, newTable(), vinfoToTable(asList(car(vi)))))
	return list(sLambda, lamArgs(lam),
		list(clearCaptureBits(asList(car(vi))), []any{}, caddr(vi), deleteDuplicates(appendL(lamSp(lam), captSp))),
		body)
}

// renumberAssignedSSAValues is renumber-assigned-ssavalues.
func (lw *lowerer) renumberAssignedSSAValues(e any) any {
	vals := exprFindAll(func(x any) bool { return assignment(x) && ssavalue(cadr(x)) }, e, func(x any) any { return cadr(cadr(x)) }, nil)
	if len(vals) == 0 {
		return e
	}
	repl := newTable()
	for _, id := range vals {
		repl.put(id, lw.makeSSAValue())
	}
	var doReplace func(x any) any
	doReplace = func(x any) any {
		if isAtomS(x) || quoted(x) {
			return x
		}
		if car(x) == sym("ssavalue") {
			if r := repl.get(cadr(x), false); truthy(r) {
				return r
			}
			return x
		}
		return cons(car(x), mapL(doReplace, cdr(x)))
	}
	return doReplace(e)
}

// convertForTypeDecl is convert-for-type-decl; lam may be nil (#f).
func (lw *lowerer) convertForTypeDecl(rhs, t any, assert bool, lam []any) any {
	if equal(t, coreRef("Any")) {
		return rhs
	}
	newMutableVar := func() sym {
		g := lw.st.gensyms.gensy()
		if lam != nil {
			vi := lam[2].([]any)
			vi[0] = appendL(asList(vi[0]), list(list(g, sym("Any"), int64(10))))
		}
		return g
	}
	var left any
	if !(isAtomS(t) || ssavalue(t) || quoted(t)) {
		left = lw.makeSSAValue()
	}
	temp := newMutableVar()
	ty := t
	if left != nil {
		ty = left
	}
	var ex any = list(sCall, topRef("convert"), ty, temp)
	if assert {
		ex = list(sCall, coreRef("typeassert"), ex, ty)
	}
	ex = list(sAssign, temp, ex)
	ex = list(sym("if"), list(sCall, coreRef("isa"), temp, ty), null(), ex)
	if left != nil {
		t = lw.renumberAssignedSSAValues(t)
	}
	exs := list(list(sAssign, temp, rhs), ex, temp)
	if left != nil {
		exs = cons(list(sAssign, left, t), exs)
	}
	if lam == nil {
		exs = cons(list(sym("local-def"), temp), exs)
	}
	return cons(sBlock, exs)
}

func captVarAccess(v, fname any, opaq *ftable) []any {
	if opaq != nil {
		return list(sCall, coreRef("getfield"), fname, opaq.get(v, false))
	}
	return list(sCall, coreRef("getfield"), fname, list(sInert, v))
}

// bindingToGlobalref is binding-to-globalref (or nil).
func bindingToGlobalref(e any) any {
	if !nodotSymRef(e) {
		return nil
	}
	var mod any = list(sym("thismodule"))
	if globalref(e) {
		mod = cadr(e)
	}
	var s any = e
	if !isSymbol(e) {
		s = last(e)
	}
	return list(sym("globalref"), mod, s)
}

func (lw *lowerer) convertGlobalAssignment(v, rhs0 any, globals *ftable, lam []any) any {
	var rhs1 any = rhs0
	if !(simpleAtom(rhs0) || equal(rhs0, list(sym("the_exception")))) {
		rhs1 = lw.makeSSAValue()
	}
	ref := bindingToGlobalref(v)
	ty := list(sCall, coreRef("get_binding_type"), cadr(ref), list(sInert, caddr(ref)))
	rhs := rhs1
	if truthy(globals.get(ref, true)) { // no type declaration for constants
		rhs = lw.convertForTypeDecl(rhs1, ty, false, lam)
	}
	ex := list(sAssign, v, rhs)
	if eq(rhs1, rhs0) {
		return list(sBlock, list(sym("globaldecl"), v), ex, rhs0)
	}
	return list(sBlock, list(sym("globaldecl"), v), list(sAssign, rhs1, rhs0), ex, rhs1)
}

// convertAssignment is convert-assignment.
func (lw *lowerer) convertAssignment(v, rhs0, fname any, lam []any, interp bool, opaq *ftable, pms []any, globals, locals *ftable) any {
	switch {
	case isSymbol(v):
		vi := locals.get(v, false)
		cv := assq(v, asList(cadr(lamVinfo(lam))))
		var vt any = coreRef("Any")
		if truthy(vi) {
			vt = vinfoType(vi)
		} else if cv != nil {
			vt = vinfoType(cv)
		}
		closed := cv != nil && vinfoAsgn(cv) && vinfoCapt(cv)
		capt := truthy(vi) && vinfoAsgn(vi) && vinfoCapt(vi)
		if !closed && !capt && equal(vt, coreRef("Any")) {
			if truthy(localIn(v, lam, nil)) || underscoreSymbol(v) {
				return list(sAssign, v, rhs0)
			}
			return lw.convertGlobalAssignment(v, rhs0, globals, lam)
		}
		var rhs1 any = rhs0
		if !(simpleAtom(rhs0) || equal(rhs0, list(sym("the_exception")))) {
			rhs1 = lw.makeSSAValue()
		}
		rhs := lw.convertForTypeDecl(rhs1, lw.clConvert(vt, fname, lam, nil, nil, false, interp, opaq, pms, newTable(), locals), true, lam)
		var ex any
		switch {
		case closed:
			var target any
			if interp {
				target = list(sym("$"), v)
			} else {
				target = captVarAccess(v, fname, opaq)
			}
			ex = list(sCall, coreRef("setfield!"), target, list(sInert, sym("contents")), rhs)
		case capt:
			ex = list(sCall, coreRef("setfield!"), v, list(sInert, sym("contents")), rhs)
		default:
			ex = list(sAssign, v, rhs)
		}
		if eq(rhs1, rhs0) {
			return list(sBlock, ex, rhs0)
		}
		return list(sBlock, list(sAssign, rhs1, rhs0), ex, rhs1)
	case globalref(v):
		return lw.convertGlobalAssignment(v, rhs0, globals, lam)
	case ssavalue(v):
		return list(sAssign, v, rhs0)
	}
	errorf("invalid assignment location \"%s\"", deparse(v))
	return nil
}

func sigTypeExpr(namemap *ftable, name, ex any) any {
	newname := namemap.get(name, ex)
	if isSymbol(newname) {
		return list(sym("globalref"), list(sym("thismodule")), newname)
	}
	return newname
}

// renameSigTypes is rename-sig-types: pattern-replace of
// (call (core Typeof) name).
func renameSigTypes(ex any, namemap *ftable) any {
	if !isPair(ex) || inList(car(ex), sQuote, sInert) {
		return ex
	}
	if lengthEq(ex, 3) && car(ex) == sCall && equal(cadr(ex), coreRef("Typeof")) {
		enew := sigTypeExpr(namemap, caddr(ex), ex)
		if !eq(enew, ex) {
			return enew
		}
	}
	return mapL(func(sub any) any {
		if !isPair(sub) {
			return sub
		}
		return renameSigTypes(sub, namemap)
	}, asList(ex))
}

func fixFunctionArgType(te any, typ any, iskw bool, namemap *ftable, typeSp []any) any {
	typapp := caddr(te)
	types := asList(renameSigTypes(cddr(typapp), namemap))
	closureType := typ
	if len(typeSp) > 0 {
		closureType = L(sCall, coreRef("apply_type"), typ, sp(typeSp))
	}
	var newtypes []any
	if iskw {
		newtypes = L(car(types), cadr(types), closureType, sp(cdddr(types)))
	} else {
		newtypes = L(closureType, sp(cdr(types)))
	}
	loc := listRef(te, 4)
	return list(sCall, coreRef("svec"),
		L(sCall, coreRef("svec"), sp(newtypes)),
		L(sCall, coreRef("svec"), sp(appendL(cddr(cadddr(te)), typeSp))),
		loc)
}

// liftToplevel is lift-toplevel: (ex . stmts).
func liftToplevel(e any) (any, []any) {
	var top [][]any
	var lift func(e any) any
	lift = func(e any) any {
		if isAtomS(e) || quoted(e) {
			return e
		}
		e2 := cons(car(e), mapL(lift, cdr(e)))
		if car(e2) == sym("toplevel-butfirst") {
			top = append([][]any{cddr(e2)}, top...)
			return cadr(e2)
		}
		return e2
	}
	e2 := lift(e)
	var stmts []any
	for k := len(top) - 1; k >= 0; k-- {
		stmts = append(stmts, top[k]...)
	}
	structs, others := separate(func(x any) bool { return is(x, "thunk") }, stmts)
	return e2, appendL(structs, others)
}

// insertAfterMeta is insert-after-meta.
func insertAfterMeta(body any, stmts []any) any {
	if len(stmts) == 0 {
		return body
	}
	meta := takeWhile(func(x any) bool {
		return isPair(x) && inList(car(x), sym("lineinfo"), sLine, sym("meta"))
	}, cdr(body))
	return L(car(body), sp(meta), sp(stmts), sp(listTail(body, 1+len(meta))))
}

func (lw *lowerer) addBoxInitsToBody(lam []any, body any) any {
	args := mapL(argName, lamArgs(lam))
	vis := asList(car(lamVinfo(lam)))
	var inits []any
	for _, arg := range args {
		vi := assq(arg, vis)
		if vi != nil && vinfoAsgn(vi) && vinfoCapt(vi) {
			inits = append(inits, list(sAssign, arg, list(sCall, coreRef("Box"), arg)))
		}
	}
	return insertAfterMeta(body, inits)
}

func allMethodsFor(ex any, body any) []any {
	mname := methodExprName(ex)
	return exprFindAll(func(s any) bool {
		return lengthGt(s, 2) && car(s) == sym("method") && eq(methodExprName(s), mname)
	}, body, identity, func(x any) bool { return isPair(x) && car(x) != sLambda })
}

var lambdaOptIgnored = func() map[sym]bool {
	m := map[sym]bool{}
	for _, s := range fields(`quote top core lineinfo line inert local-def unnecessary copyast meta inbounds boundscheck loopinfo decl aliasscope popaliasscope thunk with-static-parameters toplevel-only global globalref global-if-global assign-const-if-global isglobal thismodule const atomic null true false ssavalue isdefined toplevel module lambda error gc_preserve_begin gc_preserve_end import using export public inline noinline purity`) {
		m[sym(s)] = true
	}
	return m
}()

// localIn is local-in?: a var-info record (truthy), or false.
func localIn(s any, lam []any, tab *ftable) any {
	if tab != nil && tab.has(s) {
		return true
	}
	if r := assq(s, asList(car(lamVinfo(lam)))); r != nil {
		return r
	}
	if r := assq(s, asList(cadr(lamVinfo(lam)))); r != nil {
		return r
	}
	return false
}

// lambdaOptimizeVars is lambda-optimize-vars!.
func lambdaOptimizeVars(lam []any) []any {
	allmethods := newTable()
	getMethods := func(ex any, stmts any) []any {
		mn := methodExprName(ex)
		if allmethods.has(mn) {
			return allmethods.get(mn, nil).([]any)
		}
		am := allMethodsFor(ex, stmts)
		allmethods.put(mn, am)
		return am
	}
	vi := asList(car(lamVinfo(lam)))
	args := lamArgnames(lam)
	decl, unused, live, seen := newTable(), newTable(), newTable(), newTable()
	for _, v := range vi {
		if vinfoCapt(v) && vinfoSa(v) {
			unused.put(car(v), true)
		}
	}
	restore := func(old *ftable) {
		live.foldl(func(k, v, acc any) any {
			if !old.has(k) {
				unused.put(k, v)
			}
			return nil
		}, nil)
		live = old
	}
	kill := func() { restore(newTable()) }
	markUsed := func(v any) {
		if unused.has(v) && memq(v, args) == nil {
			unused.del(v)
		}
	}
	markCaptured := func(v any) {
		if unused.has(v) {
			unused.del(v)
		}
	}
	assign := func(v any) {
		if unused.has(v) {
			live.put(v, true)
			seen.put(v, true)
			unused.del(v)
		}
	}
	declare := func(v any) {
		if unused.has(v) {
			decl.put(v, true)
		}
	}
	leaveLoop := func(oldDecls *ftable) {
		for _, k := range live.keysList() {
			if oldDecls.has(k) {
				live.del(k)
			}
		}
		decl = oldDecls
	}
	eagerAny := func(f func(any) bool, l []any) bool {
		r := false
		for _, x := range l {
			if f(x) {
				r = true
			}
		}
		return r
	}
	var visit func(e any) bool
	visit = func(e any) bool {
		switch {
		case isAtomS(e):
			if isSymbol(e) {
				markUsed(e)
			}
			return false
		case isSymbol(car(e)) && lambdaOptIgnored[car(e).(sym)]:
			return false
		}
		switch car(e) {
		case sym("scope-block"):
			return visit(cadr(e))
		case sBlock, sCall, sym("new"), sym("splatnew"), sym("new_opaque_closure"):
			return eagerAny(visit, cdr(e))
		case sym("break-block"):
			return visit(caddr(e))
		case sReturn:
			r := visit(cadr(e))
			kill()
			return r
		case sym("break"), sym("label"), sym("symbolicgoto"):
			kill()
			return false
		case sym("symboliclabel"):
			kill()
			return true
		case sym("if"), sym("elseif"), sym("trycatch"), sym("tryfinally"), sym("trycatchelse"):
			prev := live.clone()
			if eagerAny(func(e any) bool { r := visit(e); kill(); return r }, cdr(e)) {
				// a label inside: a prior initialization could have been skipped
				kill()
				return true
			}
			restore(prev)
			return false
		case sym("_while"), sym("_do_while"):
			prev := live.clone()
			declPrev := decl.clone()
			result := eagerAny(visit, cdr(e))
			leaveLoop(declPrev)
			if result {
				return true
			}
			restore(prev)
			return false
		case sAssign:
			r := visit(caddr(e))
			assign(cadr(e))
			return r
		case sym("local"):
			declare(cadr(e))
			return false
		case sym("method"):
			if lengthGt(e, 2) {
				mn := methodExprName(e)
				// a method captures a variable if any method of the same function does
				var all []any
				if truthy(localIn(mn, lam, nil)) {
					all = getMethods(e, lamBody(lam))
				} else {
					all = list(e)
				}
				for _, ex := range all {
					for _, v := range asList(cadr(lamVinfo(cadddr(ex)))) {
						markCaptured(car(v))
					}
				}
				assign(cadr(e))
			}
			return false
		}
		eagerAny(visit, cdr(e))
		return false
	}
	visit(lamBody(lam))
	// never-undef: set in the first block, still live, or back in unused
	for _, v := range appendL(live.keysList(), unused.keysList()) {
		if seen.has(v) {
			vinfoSetNeverUndef(assq(v, vi), true)
		}
	}
	for _, v := range vi {
		if vinfoSa(v) && vinfoNeverUndef(v) {
			asList(v)[2] = vinfoFlags(v) &^ 5
		}
	}
	return lam
}

func isVarBoxed(v any, lam []any) bool {
	if vi := assq(v, asList(car(lamVinfo(lam)))); vi != nil && vinfoAsgn(vi) && vinfoCapt(vi) {
		return true
	}
	cv := assq(v, asList(cadr(lamVinfo(lam))))
	return cv != nil && vinfoAsgn(cv) && vinfoCapt(cv)
}

func toplevelPreserving(e any) bool {
	return isPair(e) && inList(car(e), sym("if"), sym("elseif"), sBlock, sym("trycatch"), sym("tryfinally"), sym("trycatchelse"))
}

func (lw *lowerer) mapClConvert(exprs []any, fname any, lam []any, namemap, defined *ftable, toplevel, interp bool, opaq *ftable, pms []any, globals, locals *ftable) []any {
	if toplevel {
		return mapL(func(x any) any {
			e2, stmts := liftToplevel(lw.clConvert(x, fname, lam, namemap, defined, toplevel && toplevelPreserving(x), interp, opaq, pms, globals, locals))
			if len(stmts) == 0 {
				return e2
			}
			return L(sBlock, sp(stmts), e2)
		}, exprs)
	}
	return mapL(func(x any) any {
		return lw.clConvert(x, fname, lam, namemap, defined, false, interp, opaq, pms, globals, locals)
	}, exprs)
}

func prepareLambda(lam []any) []any {
	vis := asList(car(lamVinfo(lam)))
	for _, vi := range vis[len(lamArgs(lam)):] {
		vinfoSetAsgn(vi, true)
	}
	return lambdaOptimizeVars(lam)
}

func anonFunctionName(s string) bool {
	return len(s) >= 2 && s[0] == '#' && s[1] >= '0' && s[1] <= '9'
}

// moduleCounter is current-julia-module-counter (fl_module_unique_name):
// with a method stack whose outermost name has no '#', the first free
// name##i of the module; otherwise the module's next counter value.
func (lw *lowerer) moduleCounter(pms []any) string {
	if len(pms) > 0 {
		funcname := symString(pms[len(pms)-1])
		if !containsByte(funcname, '#') {
			for i := 0; ; i++ {
				name := funcname + "##" + itoa(i)
				if !lw.st.bindings[name] {
					lw.st.bindings[name] = true
					return name
				}
			}
		}
	}
	lw.st.moduleCounter++
	return itoa(lw.st.moduleCounter)
}

func containsByte(s string, b byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return true
		}
	}
	return false
}

func itoa(i int) string { return symString(int64(i)) }

func (lw *lowerer) closureConvert(e any) any {
	return lw.clConvert(e, nil, nil, newTable(), newTable(), false, false, nil, nil, newTable(), newTable())
}

// clConvert is cl-convert (fname nil and lam nil are the Scheme #f).
func (lw *lowerer) clConvert(e, fname any, lam []any, namemap, defined *ftable, toplevel, interp bool, opaq *ftable, pms []any, globals, locals *ftable) any {
	if isMethod(e) {
		pms = cons(methodExprName(e), pms)
	}
	return lw.clConvert_(e, fname, lam, namemap, defined, toplevel, interp, opaq, pms, globals, locals)
}

func (lw *lowerer) clConvert_(e, fname any, lam []any, namemap, defined *ftable, toplevel, interp bool, opaq *ftable, pms []any, globals, locals *ftable) any {
	cc := func(x any) any {
		return lw.clConvert(x, fname, lam, namemap, defined, toplevel, interp, opaq, pms, globals, locals)
	}
	if lam == nil && !(isPair(e) && inList(car(e), sLambda, sym("method"), sym("macro"), sym("opaque_closure"))) {
		if isAtomS(e) {
			return e
		}
		return cons(car(e), lw.mapClConvert(cdr(e), fname, lam, namemap, defined, toplevel, interp, opaq, pms, globals, locals))
	}
	if isSymbol(e) {
		return lw.clConvertSymbol(e, fname, lam, namemap, defined, toplevel, interp, opaq, pms, globals, locals)
	}
	if isAtomS(e) {
		return e
	}
	switch car(e) {
	case sQuote, sTop, sCore, sym("globalref"), sym("thismodule"), sym("lineinfo"), sLine, sym("break"),
		sInert, sym("module"), sym("toplevel"), sNull, sTrue, sFalse, sym("meta"), sym("import"), sym("using"):
		return e
	case sym("toplevel-only"):
		// hack to avoid generating a (method x) expr for struct types
		if cadr(e) == sym("struct") {
			defined.put(caddr(e), true)
		}
		return e
	case sAssign:
		rhs := cc(caddr(e))
		return lw.convertAssignment(cadr(e), rhs, fname, lam, interp, opaq, pms, globals, locals)
	case sym("local-def"):
		// a new Box for the local declaration of a defined variable
		vi := locals.get(cadr(e), false)
		if truthy(vi) && vinfoAsgn(vi) && vinfoCapt(vi) {
			return list(sAssign, cadr(e), list(sCall, coreRef("Box")))
		}
		return null()
	case sym("local"):
		vi := locals.get(cadr(e), false)
		if truthy(vi) && vinfoAsgn(vi) && vinfoCapt(vi) {
			return list(sAssign, cadr(e), list(sCall, coreRef("Box")))
		}
		if vinfoNeverUndef(vi) {
			return null()
		}
		return list(sym("newvar"), cadr(e))
	case sym("const"):
		if !lengthEq(e, 3) {
			panic(internalError("const with other than 2 arguments"))
		}
		if globalref(cadr(e)) {
			globals.put(cadr(e), false)
		}
		return e
	case sym("atomic"):
		return e
	case sym("isdefined"):
		s := cadr(e)
		var vi any = false
		var cv []any
		if isSymbol(s) {
			vi = locals.get(s, false)
			cv = assq(s, asList(cadr(lamVinfo(lam))))
		}
		switch {
		case eq(s, fname):
			return e
		case memq(s, lamSp(lam)) != nil:
			return e
		case cv != nil:
			if vinfoAsgn(cv) && vinfoCapt(cv) {
				var access any
				if interp {
					access = list(sym("$"), list(sCall, coreRef("QuoteNode"), s))
				} else {
					access = captVarAccess(s, fname, opaq)
				}
				return list(sCall, coreRef("isdefined"), access, list(sInert, sym("contents")))
			}
			return fTrue()
		case truthy(vi):
			if vinfoAsgn(vi) && vinfoCapt(vi) {
				return list(sCall, coreRef("isdefined"), s, list(sInert, sym("contents")))
			}
			return e
		case globalref(s):
			return list(sCall, coreRef("isdefinedglobal"), cadr(s), list(sInert, caddr(s)))
		}
		return e
	case sym("_opaque_closure"):
		return lw.clConvertOpaqueClosure(e, fname, lam, opaq, pms)
	case sym("method"):
		return lw.clConvertMethod(e, fname, lam, namemap, defined, toplevel, interp, opaq, pms, globals, locals)
	case sLambda:
		// inside (thunk ...) and generated function bodies
		vis := asList(car(lamVinfo(e)))
		for _, vi := range vis[len(lamArgs(e)):] {
			vinfoSetAsgn(vi, true)
		}
		lambdaOptimizeVars(asList(e))
		body := lw.mapClConvert(cdr(lamBody(e)), sym("anon"), asList(e), newTable(), newTable(),
			isNull(cadr(e)), interp, opaq, pms, globals, vinfoToTable(asList(car(lamVinfo(e)))))
		return list(sLambda, cadr(e),
			L(clearCaptureBits(asList(car(lamVinfo(e)))), []any{}, sp(cddr(lamVinfo(e)))),
			L(sBlock, sp(body)))
	case sDecl:
		// remaining `::` expressions are type assertions
		return cc(L(sCall, coreRef("typeassert"), sp(cdr(e))))
	case sym("decl"):
		// only a type assertion if the argument is global or not a symbol
		if isSymbol(cadr(e)) && truthy(localIn(cadr(e), lam, locals)) {
			return null()
		}
		ref := bindingToGlobalref(cadr(e))
		if ref != nil {
			globals.put(ref, true)
			return cc(list(sBlock, list(sym("toplevel-only"), sym("set_binding_type!"), cadr(e)),
				list(sym("globaldecl"), ref, caddr(e)), null()))
		}
		return cc(L(sCall, coreRef("typeassert"), sp(cdr(e))))
	case sym("with-static-parameters"):
		return cc(cadr(e))
	}
	return cons(car(e), lw.mapClConvert(cdr(e), fname, lam, namemap, defined, toplevel, interp, opaq, pms, globals, locals))
}

func (lw *lowerer) clConvertSymbol(e, fname any, lam []any, namemap, defined *ftable, toplevel, interp bool, opaq *ftable, pms []any, globals, locals *ftable) any {
	newUndefVar := func(name any) sym {
		g := lw.st.gensyms.namedGensy(name)
		vi := lam[2].([]any)
		vi[0] = appendL(asList(vi[0]), list(list(g, sym("Any"), int64(32))))
		return g
	}
	getBoxContents := func(box any, typ any) any {
		// lower in an UndefVar check to a similarly named variable (#20016)
		var access any = box
		if !isSymbol(box) {
			access = lw.makeSSAValue()
		}
		undeftest := list(sCall, coreRef("isdefined"), access, list(sInert, sym("contents")))
		undefvar := newUndefVar(e)
		undefcheck := list(sym("if"), undeftest, null(), list(sBlock, list(sym("newvar"), undefvar), undefvar))
		var val any = list(sCall, coreRef("getfield"), access, list(sInert, sym("contents")))
		if !equal(typ, coreRef("Any")) {
			convt := lw.clConvert(typ, fname, lam, namemap, defined, toplevel, interp, opaq, pms, globals, locals)
			if !(symbolLike(convt) || quoted(convt)) {
				convt = lw.renumberAssignedSSAValues(convt)
			}
			val = list(sCall, coreRef("typeassert"), val, convt)
		}
		var pre []any
		if !eq(box, access) {
			pre = list(list(sAssign, access, box))
		}
		return L(sBlock, sp(pre), undefcheck, val)
	}
	vi := locals.get(e, false)
	cv := assq(e, asList(cadr(lamVinfo(lam))))
	switch {
	case eq(e, fname):
		return e
	case memq(e, lamSp(lam)) != nil:
		return e
	case cv != nil:
		var access any
		if interp {
			access = list(sym("$"), list(sCall, coreRef("QuoteNode"), e))
		} else {
			access = captVarAccess(e, fname, opaq)
		}
		if vinfoAsgn(cv) && vinfoCapt(cv) {
			return getBoxContents(access, vinfoType(cv))
		}
		return access
	case truthy(vi):
		if vinfoAsgn(vi) && vinfoCapt(vi) {
			return getBoxContents(e, vinfoType(vi))
		}
		return e
	}
	return e
}

func (lw *lowerer) clConvertOpaqueClosure(e, fname any, lam []any, opaq *ftable, pms []any) any {
	rest := listTail(e, 4)
	isva, nargs, allowPartial, functionloc := rest[0], rest[1], rest[2], rest[3]
	lam2 := asList(last(e))
	cvs := mapL(car, asList(cadr(lamVinfo(lam2))))
	prepareLambda(lam2)
	varExprs := mapL(func(v any) any {
		if cv := assq(v, asList(cadr(lamVinfo(lam)))); cv != nil {
			return captVarAccess(v, fname, opaq)
		}
		return v
	}, cvs)
	// static parameters must be captured as values inside opaque_closure
	asList(lam2[2])[3] = []any{}
	rt := caddr(e)
	if !truthy(rt) {
		rt = list(sCall, coreRef("apply_type"), coreRef("Union"))
	}
	ub := cadddr(e)
	if !truthy(ub) {
		ub = coreRef("Any")
	}
	return L(S("new_opaque_closure"), cadr(e), rt, ub, allowPartial,
		list(sym("opaque_closure_method"), null(), nargs, isva, functionloc,
			lw.convertLambda(lam2, car(lamArgs(lam2)), false, nil, symbolToIdxMap(cvs), pms)),
		sp(varExprs))
}

func simpleSort(l []any) []any {
	out := append([]any{}, l...)
	sort.SliceStable(out, func(i, j int) bool { return symString(out[i]) < symString(out[j]) })
	return out
}

func (lw *lowerer) clConvertMethod(e, fname any, lam []any, namemap, defined *ftable, toplevel, interp bool, opaq *ftable, pms []any, globals, locals *ftable) any {
	cc := func(x any) any {
		return lw.clConvert(x, fname, lam, namemap, defined, toplevel, interp, opaq, pms, globals, locals)
	}
	name := methodExprName(e)
	short := lengthEq(e, 2)
	var lam2 []any
	var vis []any
	if short {
		vis = list([]any{}, []any{}, []any{})
	} else {
		lam2 = asList(cadddr(e))
		vis = lamVinfo(lam2)
	}
	cvs := mapL(car, asList(cadr(vis)))
	isLocal := func(s any) bool { return lam != nil && isSymbol(s) && truthy(localIn(s, lam, locals)) }
	local := !globalref(cadr(e)) && isLocal(name)
	var sig any
	if !short {
		sig = caddr(e)
	}
	var spInits []any
	if !short && car(sig) == sBlock {
		spInits = lw.mapClConvert(butlast(cdr(sig)), fname, lam, namemap, defined, toplevel, interp, opaq, pms, globals, locals)
	}
	if sig != nil && car(sig) == sBlock {
		sig = last(sig)
	}
	if local {
		if memq(name, lamArgs(lam)) != nil {
			errorf("cannot add method to function argument %s", symString(name))
		}
		if symString(name)[0] == '@' {
			errorf("macro definition not allowed inside a local scope")
		}
	}
	if lam2 != nil {
		prepareLambda(lam2)
	}
	if !local {
		// not a local function: not closure converted to a new type
		switch {
		case short:
			if defined.has(cadr(e)) {
				return e
			}
			defined.put(cadr(e), true)
			// toplevel-butfirst so that it moves up with closure type definitions
			return list(sym("toplevel-butfirst"), list(sym("unnecessary"), cadr(e)), e, list(sym("latestworld")))
		case len(cvs) == 0:
			body := lw.addBoxInitsToBody(lam2, lw.clConvert(cadddr(lam2), sym("anon"), lam2, newTable(), newTable(), false, interp, opaq, pms, newTable(),
				vinfoToTable(asList(car(lamVinfo(lam2))))))
			return L(sBlock, sp(spInits),
				list(sym("method"), cadr(e),
					// anonymous functions with keyword args generate global functions
					// that refer to the type of a local function
					cc(renameSigTypes(sig, namemap)),
					list(sLambda, cadr(lam2), L(clearCaptureBits(asList(car(vis))), sp(cdr(vis))), body)),
				list(sym("latestworld")))
		}
		ex, topStmts := liftToplevel(lw.convertLambda(lam2, sym("#anon"), true, nil, nil, pms))
		newlam := lw.compactAndRenumber(lw.linearize(ex), sym("none"), int64(0))
		return L(S("toplevel-butfirst"),
			L(sBlock, sp(spInits),
				list(sym("method"), cadr(e), cc(sig), lw.bqMacro(newlam)),
				list(sym("latestworld"))),
			sp(topStmts))
	}
	// local case: lift to a new type at top level
	exists := truthy(defined.get(name, false))
	typeName := namemap.get(name, false)
	if !truthy(typeName) && truthy(name) {
		s := symString(name)
		var prefix string
		if s[0] == '#' {
			if anonFunctionName(s) {
				prefix = "#" + lw.moduleCounter(pms)
			} else {
				prefix = s
			}
		} else {
			prefix = "#" + s
		}
		typeName = sym(prefix + "#" + lw.moduleCounter(pms))
	}
	alldefs := exprFindAll(func(ex any) bool {
		return lengthGt(ex, 2) && car(ex) == sym("method") && !eq(ex, e) && eq(methodExprName(ex), name)
	}, lamBody(lam), identity, func(x any) bool { return isPair(x) && car(x) != sLambda })
	if truthy(name) {
		namemap.put(name, typeName)
	}
	var allCapt []any
	allCapt = append(allCapt, cvs...)
	for _, m := range alldefs {
		allCapt = append(allCapt, mapL(car, asList(cadr(lamVinfo(cadddr(m)))))...)
	}
	allCaptVars := deleteDuplicates(allCapt)
	var allSp []any
	if lam2 != nil {
		allSp = append(allSp, lamSp(lam2)...)
	}
	for _, m := range alldefs {
		allSp = append(allSp, lamSp(cadddr(m))...)
	}
	allSparams := deleteDuplicates(allSp)
	captSp := simpleSort(intersect(allCaptVars, allSparams))
	captVars := diff(allCaptVars, captSp)
	var movedVars []any
	if lam2 != nil {
		movedVars = deleteDuplicates(exprFindAll(func(s any) bool {
			if !(isSymbol(s) && !eq(name, s) && memq(s, captSp) == nil) {
				return false
			}
			if !(len(lamArgs(lam)) > 0 && isLocal(s)) {
				return false
			}
			// a local used in the signature: move it, or error if it is used
			// outside the signature
			usedOutside := exprContainsP(func(v any) bool { return eq(v, s) }, lamBody(lam),
				func(ex any) bool { return !(eq(ex, caddr(e)) || is(ex, "local")) })
			assignedInSig := exprContainsP(func(x any) bool {
				return isPair(x) && inList(car(x), sAssign, sym("method")) && eq(cadr(x), s)
			}, caddr(e), nil)
			if usedOutside || !assignedInSig {
				if namemap.has(s) {
					return false
				}
				errorf("local variable %s cannot be used in closure declaration", symString(s))
			}
			return true
		}, caddr(e), identity, nil))
	}
	findLocalsInMethodSig := func(methdef any) []any {
		return exprFindAll(func(s any) bool {
			return isSymbol(s) && !eq(name, s) && memq(s, captSp) == nil && memq(s, lamSp(lam)) != nil
		}, caddr(methdef), identity, nil)
	}
	var sl []any
	if lam2 != nil {
		sl = append(sl, findLocalsInMethodSig(e)...)
	}
	for _, m := range alldefs {
		sl = append(sl, findLocalsInMethodSig(m)...)
	}
	sigLocals := simpleSort(deleteDuplicates(sl))
	closureParamNames := appendL(captSp, sigLocals)
	closureParamSyms := mapL(func(any) any { return lw.makeSSAValue() }, closureParamNames)
	var typedef []any
	{
		fieldtypes := mapL(func(v any) any {
			if isVarBoxed(v, lam) {
				return coreRef("Box")
			}
			return lw.makeSSAValue()
		}, captVars)
		para := appendL(closureParamSyms, filterL(func(x any) bool { return ssavalue(x) }, fieldtypes))
		fieldnames := appendL(closureParamNames, filterL(func(v any) bool { return !isVarBoxed(v, lam) }, captVars))
		if len(para) == 0 {
			typedef = lw.typeForClosure(typeName, captVars, coreRef("Function"))
		} else {
			typedef = lw.typeForClosureParameterized(typeName, para, fieldnames, captVars, fieldtypes, coreRef("Function"))
		}
	}
	var mkMethod []any
	if !short {
		iskw := contains(func(x any) bool { return x == sym("kwftype") }, sig)
		renamemap := make([]any, len(closureParamNames))
		for i := range closureParamNames {
			renamemap[i] = cons(closureParamNames[i], list(closureParamSyms[i]))
		}
		argDefs := replaceVarsPairs(fixFunctionArgType(sig, list(sym("globalref"), list(sym("thismodule")), typeName), iskw, namemap, closureParamSyms), closureParamNames, closureParamSyms)
		for i, gs := range closureParamSyms {
			mkMethod = append(mkMethod, makeAssignment(gs, list(sCall, coreRef("TypeVar"), list(sQuote, closureParamNames[i]), coreRef("Any"))))
		}
		var selfArg any
		if iskw {
			selfArg = caddr(lamArgs(lam2))
		} else {
			selfArg = car(lamArgs(lam2))
		}
		mkMethod = append(mkMethod, list(sym("method"), false, cc(argDefs),
			lw.convertLambda(lam2, selfArg, false, closureParamNames, nil, pms)))
	}
	var mkClosure any
	{
		varExprs := mapL(func(v any) any {
			if cv := assq(v, asList(cadr(lamVinfo(lam)))); cv != nil {
				if interp {
					return list(sym("$"), list(sCall, coreRef("QuoteNode"), v))
				}
				return captVarAccess(v, fname, opaq)
			}
			return v
		}, captVars)
		P := append([]any{}, closureParamNames...)
		for i, v := range captVars {
			if !isVarBoxed(v, lam) {
				P = append(P, list(sCall, coreRef("_typeof_captured_variable"), varExprs[i]))
			}
		}
		var ty any
		if len(P) == 0 {
			ty = list(sym("globalref"), list(sym("thismodule")), typeName)
		} else {
			ty = L(sCall, coreRef("apply_type"), list(sym("globalref"), list(sym("thismodule")), typeName), sp(P))
		}
		mkClosure = L(S("new"), ty, sp(varExprs))
	}
	if len(movedVars) > 0 {
		vi := lam[2].([]any)
		vi[0] = filterL(func(v any) bool { return memq(car(v), movedVars) == nil }, asList(vi[0]))
	}
	moved := mapL(func(v any) any { return list(sym("moved-local"), v) }, movedVars)
	if exists || (short && len(alldefs) > 0) {
		return L(S("toplevel-butfirst"), null(), sp(moved), sp(spInits), sp(mkMethod), list(sym("latestworld")))
	}
	defined.put(name, true)
	return L(S("toplevel-butfirst"),
		lw.convertAssignment(name, mkClosure, fname, lam, interp, opaq, pms, globals, locals),
		sp(typedef), list(sym("latestworld")), sp(moved), sp(spInits), sp(mkMethod), list(sym("latestworld")))
}

// replaceVarsPairs is replace-vars with the renames (names[i] . syms[i]).
func replaceVarsPairs(e any, names, syms []any) any {
	switch {
	case isSymbol(e):
		for i, n := range names {
			if eq(n, e) {
				return syms[i]
			}
		}
		return e
	case !isPair(e) || quoted(e):
		return e
	case inList(car(e), sym("->"), sym("function"), sym("scope-block")):
		return e
	}
	return cons(car(e), mapL(func(x any) any { return replaceVarsPairs(x, names, syms) }, cdr(e)))
}
