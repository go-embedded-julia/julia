package lower

// Pass 5 of julia-syntax.scm: conversion to linear IR (linearize,
// compile-body).

// linearize is (linearize e).
func (lw *lowerer) linearize(e any) any {
	if !isPair(e) || quoted(e) {
		return e
	}
	if car(e) == sLambda {
		lam := asList(e)
		vi := lamVinfo(lam)
		body := lw.compileBody(cadddr(lam), appendL(asList(car(vi)), asList(cadr(vi))), lam)
		out := append([]any{}, lam...)
		out[3] = body
		return out
	}
	return cons(car(e), mapL(lw.linearize, cdr(e)))
}

func validIRArgument(e any) bool {
	return simpleAtom(e) || (isPair(e) && inList(car(e), sQuote, sInert, sTop, sCore, sym("slot"), sym("static_parameter")))
}

func validIRRvalue(lhs, e any) bool {
	return ssavalue(lhs) || validIRArgument(e) ||
		(isSymbol(lhs) && isPair(e) && inList(car(e), sym("new"), sym("splatnew"), sym("the_exception"),
			sym("isdefined"), sCall, sym("invoke"), sym("foreigncall"), sym("cfunction"),
			sym("gc_preserve_begin"), sym("copyast"), sym("new_opaque_closure"), sym("globalref")))
}

func validIRReturn(e any) bool { return validIRArgument(e) || is(e, sLambda) }

func codeTriviallyEffectFree(e any) bool {
	if !isPair(e) {
		return true
	}
	switch car(e) {
	case sBlock:
		return everyL(codeTriviallyEffectFree, cdr(e))
	case sLine, sNull:
		return true
	}
	return false
}

// fixupPoint is a `(null)` placeholder emitted before a symbolic goto,
// replaced at the end by `leave` (and followed by a pop_exception) when the
// goto leaves try blocks.
type fixupPoint struct {
	stmt any   // the statement in place of the placeholder
	pexc []any // inserted after it, if any
}

type breakLabel struct {
	name          any
	label         int64
	handlerTokens *tokens
	catchTokens   *tokens
}

type finallyHandler struct {
	variable      any
	label         int64
	actions       [][2]any // (tag . action), newest first
	handlerTokens *tokens
	catchTokens   *tokens
}

type compiler struct {
	lw            *lowerer
	lam           []any
	code          []any // in order (the Scheme code keeps them reversed)
	filename      any
	firstLine     bool
	currentLoc    any
	rett          any
	vinfoTable    *ftable
	argMap        *ftable
	labelCounter  int64
	labelMap      *ftable
	labelNesting  *ftable
	finally       *finallyHandler
	gotoFixups    []gotoFixup
	handlerTokens *tokens // innermost first
	catchTokens   *tokens
}

type gotoFixup struct {
	point         *fixupPoint
	handlerTokens *tokens
	catchTokens   *tokens
	label         any
}

func (c *compiler) emit(x any) any {
	if x == nil {
		panic(internalError("missing value in IR"))
	}
	c.code = append(c.code, x)
	return x
}

// lastCode is (car code): the last statement emitted.
func (c *compiler) lastCode() any {
	if len(c.code) == 0 {
		return nil
	}
	return c.code[len(c.code)-1]
}

func (c *compiler) makeLabel() int64 {
	l := c.labelCounter
	c.labelCounter++
	return l
}

func (c *compiler) markLabel(l int64) { c.emit(list(sym("label"), l)) }

func (c *compiler) makeAndMarkLabel() int64 {
	if lc := c.lastCode(); is(lc, "label") {
		return cadr(lc).(int64)
	}
	l := c.makeLabel()
	c.markLabel(l)
	return l
}

// tokens is a stack of exception-handler tokens, a linked list so that,
// as in the Scheme code, two stacks are eq? when one is a tail of a
// common list.
type tokens struct {
	tok  any
	next *tokens
}

func pushTok(t *tokens, x any) *tokens { return &tokens{x, t} }

func (t *tokens) length() int {
	n := 0
	for ; t != nil; t = t.next {
		n++
	}
	return n
}

func (t *tokens) list() []any {
	var out []any
	for ; t != nil; t = t.next {
		out = append(out, t.tok)
	}
	return out
}

func (c *compiler) enterFinallyBlock(action any, needGoto bool) int64 {
	tag := int64(1)
	if len(c.finally.actions) > 0 {
		tag = c.finally.actions[0][0].(int64) + 1
	}
	c.finally.actions = append([][2]any{{tag, action}}, c.finally.actions...)
	c.emit(list(sAssign, c.finally.variable, tag))
	if needGoto {
		if pexc := c.popExcExpr(c.catchTokens, c.finally.catchTokens); pexc != nil {
			c.emit(pexc)
		}
		plist := handlersUntil(c.handlerTokens, c.finally.handlerTokens.next)
		c.emit(L(S("leave"), sp(plist)))
		c.emit(list(sym("goto"), c.finally.label))
	}
	return tag
}

func (c *compiler) popExcExpr(src, dest *tokens) []any {
	if src == dest {
		return nil
	}
	for s := src; ; s = s.next {
		if s == nil {
			errorf("Attempt to jump into catch block")
		}
		if s.next == dest {
			return list(sym("pop_exception"), s.tok)
		}
	}
}

// popHandlerList is pop-handler-list for a goto to the label lab: the
// tokens to leave, innermost first, or nil when none (the Scheme code's
// #f).
func (c *compiler) popHandlerList(src, dest *tokens, lab any) []any {
	if src == dest {
		return nil
	}
	var l []any
	for s := src; ; s = s.next {
		if s == nil {
			errorf("cannot goto label \"%s\" inside try/catch block", symString(lab))
		}
		l = append(l, s.tok)
		if s.next == dest {
			return l
		}
	}
}

// handlersUntil is pop-handler-list for a break or the entry of a finally
// block, whose destination encloses the jump: dest is a tail of src, so
// the Scheme's "Attempt to jump into catch block" cannot be raised.
func handlersUntil(src, dest *tokens) []any {
	var l []any
	for s := src; s != dest; s = s.next {
		l = append(l, s.tok)
	}
	return l
}

func (c *compiler) emitReturn(tail any, x any) any {
	emit_ := func(x any) any {
		var ok bool
		if c.catchTokens == nil {
			ok = validIRReturn(x)
		} else {
			ok = simpleAtom(x)
		}
		if ok {
			return x
		}
		tmp := c.lw.makeSSAValue()
		c.emit(list(sAssign, tmp, x))
		return tmp
	}
	actuallyReturn := func(x any) any {
		x = emit_(x)
		// an implicit return is marked as having no location
		if tail != sym("explicit") {
			c.emit(list(sLine, false))
		}
		if c.rett != nil {
			x = c.compile(c.lw.convertForTypeDecl(x, c.rett, true, c.lam), nil, true, false)
		}
		x = emit_(x)
		if pexc := c.popExcExpr(c.catchTokens, nil); pexc != nil {
			c.emit(pexc)
		}
		return c.emit(list(sReturn, x))
	}
	if x == nil || !truthy(x) {
		return nil
	}
	if c.handlerTokens == nil {
		return actuallyReturn(x)
	}
	var tmp any
	switch {
	case simpleAtom(x) && (!ssavalue(x) || c.finally == nil):
	case c.finally != nil:
		tmp = c.newMutableVar(nil)
	default:
		tmp = c.lw.makeSSAValue()
	}
	if tmp != nil {
		c.emit(list(sAssign, tmp, x))
	}
	rv := x
	if tmp != nil {
		rv = tmp
	}
	if c.finally != nil {
		c.enterFinallyBlock(list(sReturn, rv), true)
	} else {
		c.emit(L(S("leave"), sp(c.handlerTokens.list())))
		actuallyReturn(rv)
	}
	return rv
}

func (c *compiler) emitBreak(labl *breakLabel) any {
	if c.finally != nil && c.finally.handlerTokens.length() > labl.handlerTokens.length() {
		return c.enterFinallyBlock(list(sym("break"), labl), true)
	}
	if pexc := c.popExcExpr(c.catchTokens, labl.catchTokens); pexc != nil {
		c.emit(pexc)
	}
	if plist := handlersUntil(c.handlerTokens, labl.handlerTokens); plist != nil {
		c.emit(L(S("leave"), sp(plist)))
	}
	return c.emit(list(sym("goto"), labl.label))
}

// newMutableVar adds a slot (name nil: a fresh gensym).
func (c *compiler) newMutableVar(name any) sym {
	var g sym
	if name == nil {
		g = c.lw.st.gensyms.gensy()
	} else {
		g = c.lw.st.gensyms.namedGensy(name)
	}
	vi := c.lam[2].([]any)
	vi[0] = appendL(asList(vi[0]), list(list(g, sym("Any"), int64(2))))
	return g
}

func (c *compiler) checkTopLevel(e any) {
	headToText := func(h any) string {
		switch h {
		case sym("abstract_type"):
			return "\"abstract type\" expression"
		case sym("primitive_type"):
			return "\"primitive type\" expression"
		case sym("struct_type"):
			return "\"struct\" expression"
		case sym("method"):
			return "method definition"
		case sym("set_binding_type!"):
			return "type declaration for global \"" + deparse(cadr(e)) + "\""
		case sym("latestworld"):
			return "World age increment"
		}
		return "\"" + symString(h) + "\" expression"
	}
	if !isNull(cadr(c.lam)) {
		errorf("%s not at top level", headToText(car(e)))
	}
}

func (c *compiler) validBodyIRArgument(aval any) bool {
	if validIRArgument(aval) {
		return true
	}
	if !isSymbol(aval) {
		return false
	}
	if memq(aval, lamArgs(c.lam)) != nil {
		return true
	}
	vi := c.vinfoTable.get(aval, false)
	return truthy(vi) && vinfoNeverUndef(vi)
}

func (c *compiler) singleAssignVar(aval any) bool {
	if !isSymbol(aval) {
		return false
	}
	if memq(aval, lamArgs(c.lam)) != nil {
		return true
	}
	vi := c.vinfoTable.get(aval, false)
	return truthy(vi) && vinfoSa(vi)
}

func (c *compiler) constReadArg(x any) bool {
	return simpleAtom(x) || c.singleAssignVar(x) || (isPair(x) && inList(car(x), sQuote, sInert, sTop, sCore))
}

func (c *compiler) compileArgs(lst []any, breakLabels []*breakLabel) []any {
	if len(lst) == 0 {
		return nil
	}
	simple := everyL(func(x any) bool {
		return simpleAtom(x) || isSymbol(x) || (isPair(x) && inList(car(x), sQuote, sInert, sTop, sCore, sym("globalref")))
	}, lst)
	var vals []any
	for _, arg := range lst {
		aval := c.compile(arg, breakLabels, true, false)
		if aval == nil {
			aval = null()
		}
		if (simple || c.constReadArg(aval)) && c.validBodyIRArgument(aval) {
			vals = append(vals, aval)
		} else {
			tmp := c.lw.makeSSAValue()
			c.emit(list(sAssign, tmp, aval))
			vals = append(vals, tmp)
		}
	}
	return vals
}

func (c *compiler) compileCond(ex any, breakLabels []*breakLabel) any {
	cnd := c.compile(ex, breakLabels, true, false)
	if cnd == nil {
		cnd = null()
	}
	if c.validBodyIRArgument(cnd) {
		return cnd
	}
	tmp := c.lw.makeSSAValue()
	c.emit(list(sAssign, tmp, cnd))
	return tmp
}

// emitCond emits the tests of a condition; each (gotoifnot c endl) is
// returned so that its label can be patched.
func (c *compiler) emitCond(cnd any, breakLabels []*breakLabel, endl any) [][]any {
	if is(cnd, sBlock) {
		cnd = flattenEx(sBlock, cnd)
	}
	if is(cnd, sBlock) {
		if lengthGt(cnd, 2) {
			c.compile(butlast(cnd), breakLabels, false, false)
		}
		cnd = last(cnd)
	}
	if is(cnd, "||") {
		shortCircuit := list(sym("goto"), sym("_"))
		clauses := cdr(cnd)
		for _, clause := range clauses[:len(clauses)-1] {
			jmp := list(sym("gotoifnot"), c.compileCond(clause, breakLabels), endl)
			c.emit(jmp)
			c.emit(shortCircuit)
			jmp[2] = c.makeAndMarkLabel()
		}
		lastJmp := list(sym("gotoifnot"), c.compileCond(clauses[len(clauses)-1], breakLabels), endl)
		c.emit(lastJmp)
		shortCircuit[1] = c.makeAndMarkLabel()
		return [][]any{lastJmp}
	}
	clauses := []any{cnd}
	if is(cnd, "&&") {
		clauses = cdr(cnd)
	}
	var tests [][]any
	for _, clause := range clauses {
		t := list(sym("gotoifnot"), c.compileCond(clause, breakLabels), endl)
		c.emit(t)
		tests = append(tests, t)
	}
	return tests
}

func (c *compiler) emitAssignmentOrSetglobal(lhs, rhs any, op sym) {
	if globalref(lhs) && op == sAssign {
		c.emit(list(sCall, topRef("setglobal!"), cadr(lhs), list(sInert, caddr(lhs)), rhs))
	} else {
		c.emit(list(op, lhs, rhs))
	}
}

func (c *compiler) emitAssignment(lhs, rhs any, op sym) any {
	if rhs != nil {
		if validIRRvalue(lhs, rhs) {
			c.emitAssignmentOrSetglobal(lhs, rhs, op)
		} else {
			rr := c.lw.makeSSAValue()
			c.emit(list(sAssign, rr, rhs))
			c.emitAssignmentOrSetglobal(lhs, rr, op)
		}
	} else {
		// unreachable code (after a return): still emit the assignment
		c.emitAssignmentOrSetglobal(lhs, null(), op)
	}
	return nil
}

func firstNonMeta(blk any) any {
	for _, elt := range cdr(blk) {
		if !is(elt, "meta") {
			return elt
		}
	}
	return false
}

func onlyMeta(blk []any) bool {
	for _, elt := range blk {
		if !(isPair(elt) && inList(car(elt), sym("lineinfo"), sLine, sym("loopinfo"))) {
			return false
		}
	}
	return true
}

var atomicHeads = map[sym]bool{
	sNull: true, sTrue: true, sFalse: true, "ssavalue": true, sQuote: true, sInert: true, sTop: true,
	sCore: true, "copyast": true, "the_exception": true, "$": true, "globalref": true, "thismodule": true,
	"cdecl": true, "stdcall": true, "fastcall": true, "thiscall": true, "llvmcall": true, "static_parameter": true,
}

// compile is the interpreter loop of compile-body. A nil result is the
// Scheme code's #f.
func (c *compiler) compile(e any, breakLabels []*breakLabel, value bool, tail any) any {
	tailP := tail != nil && truthy(tail)
	if !isPair(e) || atomicHeads[head(e)] {
		e1 := e
		if c.argMap != nil && isSymbol(e) {
			e1 = c.argMap.get(e, e)
		}
		if underscoreSymbol(e) || (globalref(e) && underscoreSymbol(cadr(e))) {
			errorf("all-underscore identifiers are write-only and their values cannot be used in expressions%s", formatLoc(c.currentLoc))
		}
		switch {
		case tailP:
			return c.emitReturn(tail, e1)
		case value:
			return e1
		case isSymbol(e1):
			c.emit(e1)
			return nil
		case isPair(e1) && inList(car(e1), sym("globalref"), sym("static_parameter")):
			c.emit(e1)
			return nil
		}
		return nil
	}
	switch car(e) {
	case sCall, sym("new"), sym("splatnew"), sym("foreigncall"), sym("cfunction"), sym("new_opaque_closure"):
		atomOrNotTupleCall := func(fptr any) bool { return isAtomS(fptr) || !tupleCall(fptr) }
		var args []any
		switch {
		case car(e) == sym("foreigncall"):
			// the 2nd to 5th arguments of ccall stay in place; the 1st is
			// compiled if an atom
			var first []any
			if atomOrNotTupleCall(cadr(e)) {
				first = c.compileArgs(list(cadr(e)), breakLabels)
			} else {
				first = list(cadr(e))
			}
			args = appendL(first, listHead(cddr(e), 4), c.compileArgs(listTail(e, 6), breakLabels))
		case car(e) == sym("cfunction"):
			fptr := car(c.compileArgs(list(caddr(e)), breakLabels))
			args = cons(cadr(e), cons(fptr, cdddr(e)))
		case car(e) == sym("new_opaque_closure"):
			ocMethod := car(listTail(cdr(e), 4))
			lam := c.lw.linearize(listRef(ocMethod, 5))
			args = appendL(c.compileArgs(listHead(cdr(e), 4), breakLabels),
				list(appendL(butlast(ocMethod), list(lam))),
				c.compileArgs(listTail(cdr(e), 5), breakLabels))
		case lengthGt(e, 2) && (cadr(e) == sym("cglobal") || equal(cadr(e), list(sym("globalref"), list(sym("thismodule")), sym("cglobal")))):
			var a []any
			if atomOrNotTupleCall(caddr(e)) {
				a = c.compileArgs(list(caddr(e)), breakLabels)
			} else {
				a = list(caddr(e))
			}
			args = appendL(list(cadr(e)), a, c.compileArgs(cdddr(e), breakLabels))
		default:
			args = c.compileArgs(cdr(e), breakLabels)
		}
		callex := cons(car(e), args)
		switch {
		case tailP:
			return c.emitReturn(tail, callex)
		case value:
			return callex
		}
		c.emit(callex)
		return nil
	case sAssign, sym("const"):
		if car(e) == sym("const") {
			if truthy(localIn(cadr(e), c.lam, nil)) {
				errorf("unsupported `const` declaration on local variable%s", formatLoc(c.currentLoc))
			}
			if isPair(cadr(c.lam)) {
				errorf("`global const` declaration not allowed inside function%s", formatLoc(c.currentLoc))
			}
		}
		lhs := cadr(e)
		if isSymbol(lhs) && underscoreSymbol(lhs) {
			return c.compile(caddr(e), breakLabels, value, tail)
		}
		rhs := c.compile(caddr(e), breakLabels, true, false)
		if c.argMap != nil && isSymbol(lhs) {
			lhs = c.argMap.get(lhs, lhs)
		}
		if value && rhs != nil {
			rr := rhs
			if !(isAtomS(rhs) || ssavalue(rhs) || car(rhs) == sNull) {
				rr = c.lw.makeSSAValue()
			}
			if !eq(rr, rhs) {
				c.emit(list(sAssign, rr, rhs))
			}
			c.emitAssignmentOrSetglobal(lhs, rr, car(e).(sym))
			if tailP {
				c.emitReturn(tail, rr)
			}
			return rr
		}
		return c.emitAssignment(lhs, rhs, car(e).(sym))
	case sBlock:
		return c.compileBlock(e, breakLabels, value, tail)
	case sReturn:
		c.compile(cadr(e), breakLabels, true, sym("explicit"))
		return nil
	case sym("unnecessary"):
		if value {
			return c.compile(cadr(e), breakLabels, value, tail)
		}
		return nil
	case sym("if"), sym("elseif"):
		tests := c.emitCond(cadr(e), breakLabels, sym("_"))
		endJump := list(sym("goto"), sym("_"))
		var val any
		if value && !tailP {
			val = c.newMutableVar(nil)
		}
		v1 := c.compile(caddr(e), breakLabels, value, tail)
		if val != nil {
			c.emitAssignment(val, v1, sAssign)
		}
		if !tailP && (lengthGt(e, 3) || val != nil) {
			c.emit(list(sLine, false))
			c.emit(endJump)
		}
		elselabel := c.makeAndMarkLabel()
		for _, t := range tests {
			t[2] = elselabel
		}
		var v2 any = null()
		if lengthGt(e, 3) {
			v2 = c.compile(cadddr(e), breakLabels, value, tail)
		}
		if val != nil {
			c.emitAssignment(val, v2, sAssign)
		}
		if !tailP {
			endJump[1] = c.makeAndMarkLabel()
		} else if lengthEq(e, 3) {
			c.emitReturn(tail, v2)
		}
		return val
	case sym("_while"):
		endl := c.makeLabel()
		topl := c.makeAndMarkLabel()
		c.emitCond(cadr(e), breakLabels, endl)
		c.compile(caddr(e), breakLabels, false, false)
		c.emit(list(sym("goto"), topl))
		c.markLabel(endl)
		if value {
			return c.compile(null(), breakLabels, value, tail)
		}
		return nil
	case sym("_do_while"):
		endl := c.makeLabel()
		topl := c.makeAndMarkLabel()
		c.compile(cadr(e), breakLabels, false, false)
		test := c.compileCond(caddr(e), breakLabels)
		c.emit(list(sym("gotoifnot"), test, endl))
		c.emit(list(sym("goto"), topl))
		c.markLabel(endl)
		if value {
			return c.compile(null(), breakLabels, value, tail)
		}
		return nil
	case sym("break-block"):
		endl := c.makeLabel()
		bl := &breakLabel{name: cadr(e), label: endl, handlerTokens: c.handlerTokens, catchTokens: c.catchTokens}
		c.compile(caddr(e), append([]*breakLabel{bl}, breakLabels...), false, false)
		c.markLabel(endl)
		if value {
			return c.compile(null(), breakLabels, value, tail)
		}
		return nil
	case sym("break"):
		var labl *breakLabel
		for _, b := range breakLabels {
			if eq(b.name, cadr(e)) {
				labl = b
				break
			}
		}
		if labl == nil {
			errorf("break or continue outside loop")
		}
		// the value of a break is what emit-break returns: the goto it
		// emitted, or the finally block's tag (a `try` in value position
		// assigns it)
		return c.emitBreak(labl)
	case sym("label"), sym("symboliclabel"):
		if car(e) == sym("symboliclabel") {
			if c.labelNesting.has(cadr(e)) {
				errorf("label \"%s\" defined multiple times", symString(cadr(e)))
			}
			c.labelNesting.put(cadr(e), [2]*tokens{c.handlerTokens, c.catchTokens})
		}
		if m := c.labelMap.get(cadr(e), false); truthy(m) {
			c.emit(list(sym("label"), m))
		} else {
			c.labelMap.put(cadr(e), c.makeAndMarkLabel())
		}
		if tailP {
			return c.emitReturn(tail, null())
		}
		if value {
			errorf("misplaced label")
		}
		return nil
	case sym("symbolicgoto"):
		m := c.labelMap.get(cadr(e), false)
		if !truthy(m) {
			l := c.makeLabel()
			c.labelMap.put(cadr(e), l)
			m = l
		}
		pt := &fixupPoint{stmt: null()} // space for a `leave` that might be needed
		c.emit(pt)
		c.emit(list(sym("goto"), m))
		c.gotoFixups = append([]gotoFixup{{pt, c.handlerTokens, c.catchTokens, cadr(e)}}, c.gotoFixups...)
		return nil
	case sym("trycatch"), sym("tryfinally"), sym("trycatchelse"):
		return c.compileTry(e, breakLabels, value, tail)
	case sym("newvar"):
		// avoid duplicate newvar nodes; exclude deleted vars
		if !(len(c.code) > 0 && equal(c.lastCode(), e)) && c.vinfoTable.has(cadr(e)) {
			c.emit(e)
		}
		return nil
	case sym("global"):
		if value {
			errorf("misplaced \"global\" declaration")
		}
		c.emit(e)
		if isNull(cadr(c.lam)) {
			c.emit(list(sym("latestworld")))
		}
		return nil
	case sym("globaldecl"):
		if value {
			errorf("misplaced \"global\" declaration")
		}
		if lengthEq(e, 2) || isAtomS(caddr(e)) {
			c.emit(e)
		} else {
			rr := c.lw.makeSSAValue()
			c.emit(list(sAssign, rr, c.compile(caddr(e), breakLabels, true, false)))
			c.emit(list(sym("globaldecl"), cadr(e), rr))
		}
		if isNull(cadr(c.lam)) {
			c.emit(list(sym("latestworld")))
		}
		return nil
	case sym("local-def"), sym("local"):
		return nil
	case sym("moved-local"):
		vi := c.lam[2].([]any)
		vi[0] = appendL(asList(vi[0]), list(list(cadr(e), sym("Any"), int64(2))))
		return nil
	case sym("atomic"):
		errorf("misplaced atomic declaration")
	case sym("isdefined"), sym("throw_undef_if_not"), sym("boundscheck"):
		if tailP {
			return c.emitReturn(tail, e)
		}
		return e
	case sym("method"):
		if !isNull(cadr(c.lam)) {
			errorf("Global method definition%s needs to be placed at the top level, or use \"eval\".", linenodeString(c.currentLoc))
		}
		if lengthGt(e, 2) {
			sig := c.compile(caddr(e), breakLabels, true, false)
			if !validIRArgument(sig) {
				l := c.lw.makeSSAValue()
				c.emit(list(sAssign, l, sig))
				sig = l
			}
			lam := cadddr(e)
			if is(lam, sLambda) {
				lam = c.lw.linearize(lam)
			} else {
				l := c.lw.makeSSAValue()
				c.emit(list(sAssign, l, c.compile(lam, breakLabels, true, false)))
				lam = l
			}
			name := cadr(e)
			if !truthy(name) {
				name = fFalse()
			}
			c.emit(list(sym("method"), name, sig, lam))
			if value {
				return c.compile(null(), breakLabels, value, tail)
			}
			return nil
		}
		switch {
		case tailP:
			return c.emitReturn(tail, e)
		case value:
			return e
		}
		c.emit(e)
		return nil
	case sLambda:
		temp := c.lw.linearize(e)
		switch {
		case tailP:
			return c.emitReturn(tail, temp)
		case value:
			return temp
		}
		c.emit(temp)
		return nil
	case sym("thunk"), sym("module"):
		c.checkTopLevel(e)
		c.emit(e)
		if tailP {
			c.emitReturn(tail, null())
		}
		return null()
	case sym("toplevel-only"):
		c.checkTopLevel(cdr(e))
		return null()
	case sym("toplevel"):
		c.checkTopLevel(e)
		val := c.lw.makeSSAValue()
		c.emit(list(sAssign, val, e))
		c.emit(list(sym("latestworld")))
		if tailP {
			c.emitReturn(tail, val)
		}
		return val
	case sym("latestworld-if-toplevel"):
		if isNull(cadr(c.lam)) {
			c.emit(list(sym("latestworld")))
		}
		return null()
	case sym("import"), sym("using"), sym("export"), sym("public"), sym("latestworld"):
		c.checkTopLevel(e)
		if car(e) != sym("latestworld") {
			c.emit(e)
		}
		c.emit(list(sym("latestworld")))
		haveRet := isReturn(c.lastCode())
		if tailP && !haveRet {
			c.emitReturn(tail, null())
		}
		return null()
	case sym("gc_preserve_begin"):
		return cons(car(e), c.compileArgs(cdr(e), breakLabels))
	case sym("lineinfo"), sLine, sym("meta"), sym("inbounds"), sym("loopinfo"), sym("gc_preserve_end"),
		sym("aliasscope"), sym("popaliasscope"), sym("inline"), sym("noinline"), sym("purity"):
		haveRet := isReturn(c.lastCode())
		switch {
		case car(e) == sLine:
			c.currentLoc = e
			if c.firstLine {
				c.firstLine = false
				c.emit(e)
			} else {
				// strip file names out of non-initial line nodes
				c.emit(list(sLine, cadr(e)))
			}
		case car(e) == sym("meta") && lengthGt(e, 2) && cadr(e) == sym("ret-type"):
			c.rett = caddr(e)
		default:
			c.emit(e)
		}
		if tailP && !haveRet {
			c.emitReturn(tail, null())
		}
		return null()
	case sym("≔"), sym("⩴"), sym("≕"), sym(":="):
		errorf("unsupported assignment operator \"%s\"", deparse(car(e)))
	case sym("escape"):
		errorf("\"esc(...)\" used outside of macro expansion")
	case sym("error"):
		// (error (cadr e)) raises the message as it is, a julia String:
		// error-wrap returns it unchanged
		panic(loweringErrorValue{cadr(e)})
	}
	errorf("invalid syntax %s", deparse(e))
	return nil
}

// linenodeString is linenode-string.
func linenodeString(lno any) string {
	switch {
	case lengthEq(lno, 2):
		return " around line " + symString(cadr(lno))
	case lengthEq(lno, 3):
		return " around " + symString(caddr(lno)) + ":" + symString(cadr(lno))
	}
	return ""
}

func (c *compiler) compileBlock(e any, breakLabels []*breakLabel, value bool, tail any) any {
	tailP := tail != nil && truthy(tail)
	lastFname := c.filename
	fnm := firstNonMeta(e)
	fname := c.filename
	if lengthGt(e, 1) && linenum(fnm) && lengthGt(fnm, 2) {
		fname = caddr(fnm)
	}
	fileDiff := !eqOrNil(fname, lastFname)
	needMeta := fileDiff && lastFname != nil && !eq(e, lamBody(c.lam))
	var emitFinalMeta func() // set by the loop below, which always ends on a statement
	if fileDiff {
		c.filename = fname
	}
	if needMeta {
		c.emit(list(sym("meta"), sym("push_loc"), fname))
	}
	var v any
	xs := cdr(e)
	for {
		if onlyMeta(xs[1:]) {
			rest := xs[1:]
			emitFinalMeta = func() {
				for _, m := range rest {
					c.compile(m, breakLabels, false, false)
				}
			}
			v = c.compile(xs[0], breakLabels, value, tail)
			break
		}
		c.compile(xs[0], breakLabels, false, false)
		xs = xs[1:]
	}
	if needMeta {
		switch {
		case tailP:
			// splice the pop_loc before the return, so that the return is
			// attributed to the right level of macro
			if lc := c.lastCode(); isReturn(lc) {
				retv := cadr(lc)
				c.code = c.code[:len(c.code)-1]
				if !simpleAtom(retv) {
					tmp := c.lw.makeSSAValue()
					c.emit(list(sAssign, tmp, retv))
					retv = tmp
				}
				emitFinalMeta()
				c.emit(list(sym("meta"), sym("pop_loc")))
				c.emit(list(sReturn, retv))
			} else {
				c.emit(list(sym("meta"), sym("pop_loc")))
			}
		case v != nil && value && !simpleAtom(v):
			tmp := c.lw.makeSSAValue()
			c.emit(list(sAssign, tmp, v))
			v = tmp
			emitFinalMeta()
			c.emit(list(sym("meta"), sym("pop_loc")))
		default:
			emitFinalMeta()
			c.emit(list(sym("meta"), sym("pop_loc")))
		}
	} else {
		emitFinalMeta()
	}
	if fileDiff {
		c.filename = lastFname
	}
	return v
}

// eqOrNil compares file names, where nil is the Scheme code's #f.
func eqOrNil(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return eq(a, b)
}

func (c *compiler) compileTry(e any, breakLabels []*breakLabel, value bool, tail any) any {
	tailP := tail != nil && truthy(tail)
	handlerToken := c.lw.makeSSAValue()
	catch := c.makeLabel()
	var catchcode any
	if car(e) == sym("tryfinally") {
		catchcode = list(sCall, topRef("rethrow"))
	} else {
		catchcode = caddr(e)
	}
	var els any
	if car(e) == sym("trycatchelse") {
		els = c.makeLabel()
	}
	var endl any = c.makeLabel()
	lastFinally := c.finally
	var finally any
	if car(e) == sym("tryfinally") && !codeTriviallyEffectFree(caddr(e)) {
		finally = c.newMutableVar(nil)
	}
	var scope []any
	if car(e) == sym("tryfinally") {
		scope = cdddr(e)
	}
	var myFinally *finallyHandler
	c.emit(list(sAssign, handlerToken, L(S("enter"), catch, sp(c.compileArgs(scope, breakLabels)))))
	c.handlerTokens = pushTok(c.handlerTokens, handlerToken)
	if finally != nil {
		myFinally = &finallyHandler{variable: finally, label: endl.(int64), handlerTokens: c.handlerTokens, catchTokens: c.catchTokens}
		c.finally = myFinally
		c.emit(list(sAssign, finally, int64(-1)))
	}
	v1 := c.compile(cadr(e), breakLabels, value, false)
	var val any
	if value && !tailP {
		val = c.newMutableVar(nil)
	}
	if val != nil && v1 != nil {
		c.emitAssignment(val, v1, sAssign)
	}
	if tailP {
		if els != nil {
			if val == nil && v1 != nil {
				c.emit(v1)
			}
			c.emit(list(sym("leave"), handlerToken))
		} else if v1 != nil {
			c.emitReturn(tail, v1)
		}
		if finally == nil {
			endl = nil
		}
	} else {
		c.emit(list(sym("leave"), handlerToken))
		target := endl
		if els != nil {
			target = els
		}
		c.emit(list(sym("goto"), target))
	}
	c.handlerTokens = c.handlerTokens.next
	if els != nil {
		c.markLabel(els.(int64))
		v3 := c.compile(cadddr(e), breakLabels, value, tail)
		if val != nil {
			c.emitAssignment(val, v3, sAssign)
		}
		if endl != nil {
			c.emit(list(sym("goto"), endl))
		}
	}
	c.markLabel(catch)
	if finally != nil {
		c.finally = lastFinally
		c.catchTokens = pushTok(c.catchTokens, handlerToken)
		c.compile(caddr(e), breakLabels, false, false) // entered via exception
		c.emit(list(sCall, topRef("rethrow")))
		c.emitReturn(tail, null()) // unreachable
		c.catchTokens = c.catchTokens.next
		c.markLabel(endl.(int64)) // non-exceptional control flow enters here
		c.compile(c.lw.renumberAssignedSSAValues(caddr(e)), breakLabels, false, false)
		// actions at the exit of the finally block, by the tag variable
		actions := myFinally.actions
		for k, a := range actions {
			var skip any
			ac := asList(a[1])
			if !(tailP && k == len(actions)-1 && car(ac) == sReturn) {
				skip = c.makeLabel()
			}
			if skip != nil {
				tmp := c.lw.makeSSAValue()
				c.emit(list(sAssign, tmp, list(sCall, coreRef("==="), finally, a[0])))
				c.emit(list(sym("gotoifnot"), tmp, skip))
			}
			// enter-finally-block records only returns and breaks: the
			// Scheme's "assumed to be a rethrow" case is never taken
			if car(ac) == sReturn {
				c.emitReturn(tail, cadr(ac))
			} else {
				c.emitBreak(cadr(ac).(*breakLabel))
			}
			if skip != nil {
				c.markLabel(skip.(int64))
			}
		}
	} else {
		c.catchTokens = pushTok(c.catchTokens, handlerToken)
		v2 := c.compile(catchcode, breakLabels, value, tail)
		if val != nil {
			c.emitAssignment(val, v2, sAssign)
		}
		if !tailP {
			c.emit(list(sym("pop_exception"), handlerToken))
		}
		if endl != nil {
			c.markLabel(endl.(int64))
		}
		c.catchTokens = c.catchTokens.next
	}
	return val
}

// compileBody is compile-body.
func (lw *lowerer) compileBody(e any, vi []any, lam []any) any {
	c := &compiler{lw: lw, lam: lam, firstLine: true,
		vinfoTable: vinfoToTable(asList(car(lamVinfo(lam)))),
		labelMap:   newTable(), labelNesting: newTable()}
	// new slots for assigned arguments
	nargs := len(lamArgs(lam))
	for _, v := range vi[:min(nargs, len(vi))] {
		if vinfoAsgn(v) {
			if c.argMap == nil {
				c.argMap = newTable()
			}
			c.argMap.put(car(v), c.newMutableVar(car(v)))
		}
	}
	c.compile(e, nil, true, true)
	for _, x := range c.gotoFixups {
		nest := c.labelNesting.get(x.label, false)
		if !truthy(nest) {
			errorf("label \"%s\" referenced but not defined", symString(x.label))
		}
		tn := nest.([2]*tokens)
		if plist := c.popHandlerList(x.handlerTokens, tn[0], x.label); plist != nil {
			x.point.stmt = L(S("leave"), sp(plist))
		}
		if pexc := c.popExcExpr(x.catchTokens, tn[1]); pexc != nil {
			x.point.pexc = pexc
		}
	}
	var stmts []any
	for _, s := range c.code {
		if pt, ok := s.(*fixupPoint); ok {
			stmts = append(stmts, pt.stmt)
			if pt.pexc != nil {
				stmts = append(stmts, pt.pexc)
			}
			continue
		}
		stmts = append(stmts, s)
	}
	di := definitelyInitializedVars(stmts, vi)
	body := []any{sBlock}
	for _, s := range stmts {
		if is(s, "newvar") && di.has(cadr(s)) {
			continue
		}
		body = append(body, s)
	}
	if c.argMap != nil {
		var inits []any
		c.argMap.foldl(func(k, v, acc any) any {
			inits = append([]any{list(sAssign, v, k)}, inits...)
			return nil
		}, nil)
		return insertAfterMeta(body, inits)
	}
	return body
}

func forEachIsdefined(f func(any), e any) {
	switch {
	case isAtomS(e) || quoted(e):
	case is(e, "isdefined"):
		f(cadr(e))
	default:
		for _, x := range cdr(e) {
			forEachIsdefined(f, x)
		}
	}
}

// definitelyInitializedVars is definitely-initialized-vars.
func definitelyInitializedVars(stmts []any, vi []any) *ftable {
	vars, di := newTable(), newTable()
	for _, e := range stmts {
		forEachIsdefined(func(x any) {
			if vars.has(x) {
				vars.del(x)
			}
		}, e)
		switch {
		case is(e, "newvar"):
			vinf := varInfoFor(cadr(e), vi)
			if vinf != nil && !vinfoCapt(vinf) {
				vars.put(cadr(e), true)
			}
		case isPair(e) && (inList(car(e), sym("goto"), sym("gotoifnot")) ||
			(car(e) == sAssign && is(caddr(e), "enter"))):
			vars = newTable()
		case is(e, sAssign):
			if vars.has(cadr(e)) {
				vars.del(cadr(e))
				di.put(cadr(e), true)
			}
		}
	}
	return di
}
