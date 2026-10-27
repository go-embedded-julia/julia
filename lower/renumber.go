package lower

// Pass 6 of julia-syntax.scm: renumber slots, SSA values and labels
// (listify-lambda, compact-ir, renumber-lambda, compact-and-renumber).

// listifyLambda inserts `list` heads so that the lambda's vinfo lists are
// valid expressions.
func listifyLambda(lam []any) []any {
	vi := lamVinfo(lam)
	vars := mapL(func(l any) any { return cons(sym("list"), asList(l)) }, asList(car(vi)))
	return L(sLambda, cons(sym("list"), lamArgs(lam)),
		L(S("list"), cons(sym("list"), vars), cons(sym("list"), asList(cadr(vi))), caddr(vi), cons(sym("list"), asList(cadddr(vi)))),
		sp(cdddr(lam)))
}

func makeLineinfo(file, line any, inlinedAt any) []any {
	if inlinedAt == nil {
		inlinedAt = int64(0)
	}
	return list(sym("lineinfo"), file, line, inlinedAt)
}

type compacted struct {
	code      []any // (block stmts...)
	locs      []any // (list locs...)
	linetable []any // (list (lineinfo ...)...)
	ssavtable *ftable
	labltable *ftable
}

// compactIR is compact-ir.
func compactIR(body any, file, line any) compacted {
	code := []any{sBlock}
	locs := []any{sym("list")}
	var linetable [][]any // reversed: newest first, as the Scheme list
	linetablelen := 0
	labltable, ssavtable := newTable(), newTable()
	currentLoc := int64(0)
	nowhere := false
	currentFile, currentLine := file, line
	type locEntry struct {
		loc        int64
		line, file any
	}
	var locstack []locEntry
	i := int64(1)
	emit := func(e any) {
		if e == nil {
			panic(internalError("missing value in IR"))
		}
		if len(linetable) == 0 && !is(e, "meta") {
			linetable = append([][]any{makeLineinfo(file, line, nil)}, linetable...)
			linetablelen++
			currentLoc = 1
		}
		code = append(code, e)
		i++
		if nowhere {
			locs = append(locs, int64(0))
		} else {
			locs = append(locs, currentLoc)
		}
		nowhere = false
	}
	for _, e := range cdr(body) {
		switch {
		case isAtomS(e):
			emit(e)
		case car(e) == sLine:
			switch {
			case lengthEq(e, 2) && !truthy(cadr(e)):
				// (line #f): entering a generated statement that should not
				// count as belonging to the previous location
				nowhere = true
			case currentLine == int64(0) && lengthEq(e, 2) && len(linetable) > 0:
				// (line n) after push_loc just updates the line of the new file
				linetable[0][2] = cadr(e)
				currentLine = cadr(e)
			default:
				currentLine = cadr(e)
				if isPair(cddr(e)) {
					currentFile = caddr(e)
				}
				var li []any
				if len(locstack) == 0 {
					li = makeLineinfo(currentFile, currentLine, nil)
				} else {
					li = makeLineinfo(currentFile, currentLine, locstack[0].loc)
				}
				linetable = append([][]any{li}, linetable...)
				linetablelen++
				currentLoc = int64(linetablelen)
			}
		case lengthGt(e, 2) && car(e) == sym("meta") && cadr(e) == sym("push_loc"):
			locstack = append([]locEntry{{currentLoc, currentLine, currentFile}}, locstack...)
			currentFile = caddr(e)
			currentLine = int64(0)
			linetable = append([][]any{makeLineinfo(currentFile, currentLine, currentLoc)}, linetable...)
			linetablelen++
			currentLoc = int64(linetablelen)
		case lengthEq(e, 2) && car(e) == sym("meta") && cadr(e) == sym("pop_loc"):
			l := locstack[0]
			locstack = locstack[1:]
			currentLoc, currentLine, currentFile = l.loc, l.line, l.file
		case car(e) == sym("label"):
			labltable.put(cadr(e), i)
		case assignment(e) && ssavalue(cadr(e)):
			var idx any = false
			if ssavalue(caddr(e)) {
				idx = ssavtable.get(cadr(caddr(e)), false)
			}
			if truthy(idx) {
				// both sides SSA values: merge them
				ssavtable.put(cadr(cadr(e)), idx)
			} else {
				ssavtable.put(cadr(cadr(e)), i)
				emit(caddr(e))
			}
		default:
			emit(e)
		}
	}
	lt := []any{sym("list")}
	for k := len(linetable) - 1; k >= 0; k-- {
		lt = append(lt, linetable[k])
	}
	return compacted{code: code, locs: locs, linetable: lt, ssavtable: ssavtable, labltable: labltable}
}

// symbolToIdxMap is symbol-to-idx-map.
func symbolToIdxMap(l []any) *ftable {
	t := newTable()
	for i, x := range l {
		t.put(x, int64(i+1))
	}
	return t
}

// renumberLambda is renumber-lambda.
func (lw *lowerer) renumberLambda(lam []any, file, line any) []any {
	stuff := compactIR(lamBody(lam), file, line)
	code := stuff.code
	vi := lamVinfo(lam)
	slotTable := symbolToIdxMap(mapL(car, asList(car(vi))))
	spTable := symbolToIdxMap(lamSp(lam))
	var renumberStuff func(e any) any
	renumberStuff = func(e any) any {
		switch {
		case e == unusedSym:
			errorf("Attempted to use slot marked unused")
		case isSymbol(e):
			if idx := slotTable.get(e, false); truthy(idx) {
				return list(sym("slot"), idx)
			}
			if idx := spTable.get(e, false); truthy(idx) {
				return list(sym("static_parameter"), idx)
			}
			return e
		case nospecializeMeta(e, false):
			// nospecialize vars to slot numbers
			return L(S("meta"), cadr(e), sp(mapL(renumberStuff, cddr(e))))
		case isAtomS(e) || quoted(e) || inList(car(e), sym("using"), sym("import"), sym("export"), sym("public"), sym("global"), sym("toplevel")):
			return e
		case ssavalue(e):
			idx := stuff.ssavtable.get(cadr(e), false)
			if !truthy(idx) {
				errorf("internal bug: ssavalue with no def")
			}
			return list(sym("ssavalue"), idx)
		case car(e) == sym("goto"):
			return list(sym("goto"), stuff.labltable.get(cadr(e), false))
		case car(e) == sym("enter"):
			return L(S("enter"), stuff.labltable.get(cadr(e), false), sp(mapL(renumberStuff, cddr(e))))
		case car(e) == sym("gotoifnot"):
			return list(sym("gotoifnot"), renumberStuff(cadr(e)), stuff.labltable.get(caddr(e), false))
		case car(e) == sLambda:
			return lw.renumberLambda(asList(e), sym("none"), int64(0))
		}
		out := cons(car(e), mapL(renumberStuff, cdr(e)))
		if car(out) == sym("foreigncall") && tupleCall(cadr(out)) &&
			exprContainsP(func(x any) bool { return ssavalue(x) || isSlot(x) }, cadr(out), nil) {
			errorf("ccall function name and library expression cannot reference local variables")
		}
		return out
	}
	body := renumberStuff(code)
	return listifyLambda(L(sLambda, cadr(lam),
		L(car(vi), cadr(vi), int64(len(cdr(body))), last(vi)),
		body, stuff.locs, stuff.linetable))
}

// compactAndRenumber is compact-and-renumber.
func (lw *lowerer) compactAndRenumber(ex any, file, line any) any {
	if isAtomS(ex) {
		return ex
	}
	if car(ex) == sLambda {
		if isNull(cadr(ex)) {
			return lw.renumberLambda(asList(ex), file, line)
		}
		return lw.renumberLambda(asList(ex), sym("none"), int64(0))
	}
	return cons(car(ex), mapL(func(e any) any { return lw.compactAndRenumber(e, file, line) }, cdr(ex)))
}
