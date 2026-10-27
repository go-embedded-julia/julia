package lower

// Pass 2 of julia-syntax.scm: identify and rename local variables
// (resolve-scopes).

import "strings"

func lhsDecls(e any) []any {
	switch {
	case symdecl(e):
		return list(e)
	case isPair(e) && (car(e) == sym("tuple") || car(e) == sym("parameters")):
		var out []any
		for _, x := range cdr(e) {
			out = append(out, lhsDecls(x)...)
		}
		return out
	}
	return nil
}

func lhsVars(e any) []any { return mapL(declVar, lhsDecls(e)) }

// lhsBoundNames is lhs-bound-names.
func lhsBoundNames(e any) []any {
	switch {
	case underscoreSymbol(e):
		return nil
	case isAtomS(e):
		return list(e)
	case isPair(e) && inList(car(e), sCall, sym("curly"), sym("where"), sDecl):
		return lhsBoundNames(cadr(e))
	case isPair(e) && inList(car(e), sym("tuple"), sym("parameters")):
		var out []any
		for _, x := range cdr(e) {
			out = append(out, lhsBoundNames(x)...)
		}
		return out
	}
	return nil
}

func allDeclVars(e any) any {
	switch {
	case eventuallyCall(e):
		return e
	case isDecl(e):
		return declVar(e)
	case is(e, "tuple"):
		return cons(sym("tuple"), mapL(allDeclVars, cdr(e)))
	}
	return e
}

var scopeBarriers = []any{sLambda, sym("scope-block"), sym("module"), sym("toplevel")}

// findAssignedVars is find-assigned-vars.
func findAssignedVars(e any) []any {
	var vars []any
	var walk func(e any)
	walk = func(e any) {
		if !isPair(e) || quoted(e) {
			return
		}
		switch car(e) {
		case sLambda, sym("scope-block"), sym("module"), sym("toplevel"):
		case sym("method"):
			v := declVar(methodExprName(e))
			if isSymbol(v) {
				vars = cons(v, vars)
			}
			if !lengthEq(e, 2) {
				walk(caddr(e))
			}
		case sym("assign-const-if-global"):
			vars = cons(cadr(e), vars)
		case sAssign, sym("const"):
			v := declVar(cadr(e))
			walk(caddr(e))
			if !(ssavalue(v) || globalref(v) || underscoreSymbol(v)) {
				vars = cons(v, vars)
			}
		default:
			for _, x := range cdr(e) {
				walk(x)
			}
		}
	}
	walk(e)
	return deleteDuplicates(vars)
}

// findDecls is find-decls.
func findDecls(kind sym, e any) []any {
	var vars []any
	var walk func(e any)
	walk = func(e any) {
		switch {
		case !isPair(e) || quoted(e):
		case memq(car(e), scopeBarriers) != nil:
		case car(e) == kind:
			if !underscoreSymbol(cadr(e)) {
				vars = cons(declVar(cadr(e)), vars)
			}
		default:
			for _, x := range cdr(e) {
				walk(x)
			}
		}
	}
	walk(e)
	return vars
}

func findLocalDecls(e any) []any    { return findDecls("local", e) }
func findLocalDefDecls(e any) []any { return findDecls("local-def", e) }
func findGlobalDecls(e any) []any   { return findDecls("global", e) }

// findScopeDecl is find-scope-decl: the first (kind ...) form, or nil.
func findScopeDecl(e any, kind sym) any {
	var found any
	exprContainsP(func(x any) bool {
		if is(x, kind) {
			found = x
			return true
		}
		return false
	}, e, func(x any) bool { return !(isPair(x) && memq(car(x), scopeBarriers) != nil) })
	return found
}

func checkValidName(e any) {
	if !validName(e) {
		errorf("invalid identifier name \"%s\"", symString(e))
	}
}

// scope is the vector of make-scope.
type scope struct {
	lam             []any
	args            []any
	locals          []any
	globals         []any
	sp              []any
	renames         [][2]any
	prev            *scope
	soft, hard      bool
	implicitGlobals []any
	warnVars        *ftable
	table           *ftable // name -> stack (list) of bindings
}

func pushVar(tab *ftable, v, val any) {
	prev := tab.get(v, false)
	var l []any
	if truthy(prev) {
		l = prev.([]any)
	}
	tab.put(v, cons(val, l))
}

func popVar(tab *ftable, v any) {
	tab.put(v, cdr(tab.get(v, nil)))
}

func makeScope(lam []any, args, locals, globals, sp []any, renames [][2]any, prev *scope,
	soft, hard bool, implicitGlobals []any, warnVars *ftable) *scope {
	var tab *ftable
	if prev != nil {
		tab = prev.table
	} else {
		tab = newTable()
	}
	for _, v := range sp {
		pushVar(tab, v, v)
	}
	for _, v := range locals {
		pushVar(tab, v, v)
	}
	for _, p := range renames {
		pushVar(tab, p[0], p[1])
	}
	for _, v := range globals {
		pushVar(tab, v, list(sym("globalref"), list(sym("thismodule")), v))
	}
	for _, v := range args {
		pushVar(tab, v, v)
	}
	return &scope{lam: lam, args: args, locals: locals, globals: globals, sp: sp, renames: renames,
		prev: prev, soft: soft, hard: hard, implicitGlobals: implicitGlobals, warnVars: warnVars, table: tab}
}

func popScope(s *scope) {
	for _, v := range s.sp {
		popVar(s.table, v)
	}
	for _, v := range s.locals {
		popVar(s.table, v)
	}
	for _, p := range s.renames {
		popVar(s.table, p[0])
	}
	for _, v := range s.globals {
		popVar(s.table, v)
	}
	for _, v := range s.args {
		popVar(s.table, v)
	}
}

// varKind is var-kind.
func varKind(v any, s *scope, excludeTopLevelGlobals bool) sym {
	for ; s != nil; s = s.prev {
		switch {
		case memq(v, s.args) != nil:
			return "argument"
		case memq(v, s.locals) != nil:
			return "local"
		case memq(v, s.globals) != nil:
			if excludeTopLevelGlobals && isNull(cadr(s.lam)) && (s.prev == nil || s.prev.prev == nil) {
				return "none"
			}
			return "global"
		case memq(v, s.sp) != nil:
			return "static-parameter"
		}
	}
	return "none"
}

func inScope(v any, s *scope) bool { return varKind(v, s, false) != "none" }

func warnVar(v any, s *scope) bool {
	for ; s != nil; s = s.prev {
		if s.warnVars != nil && s.warnVars.has(v) {
			s.warnVars.del(v)
			return true
		}
	}
	return false
}

func allLocalNames(s *scope) []any {
	var out []any
	for ; s != nil; s = s.prev {
		out = append(out, s.args...)
		out = append(out, s.sp...)
		out = append(out, s.locals...)
	}
	return out
}

// isGensymName is gensym? of femtolisp (a symbol created by gensym).
// Lowering-generated names are interned symbols here, so none is.
func isGensymName(any) bool { return false }

func (lw *lowerer) resolveScopes(e any) any { return lw.resolveScopes_(e, nil, nil, false) }

// resolveScopes_ is resolve-scopes-.
func (lw *lowerer) resolveScopes_(e any, s *scope, sp []any, loc any) any {
	if isSymbol(e) {
		if s != nil {
			if val := s.table.get(e, false); truthy(val) {
				return car(val)
			}
		}
		if underscoreSymbol(e) {
			return e
		}
		return list(sym("globalref"), list(sym("thismodule")), e)
	}
	if !isPair(e) || quoted(e) || inList(car(e), sym("toplevel"), sym("symbolicgoto"), sym("symboliclabel"), sym("toplevel-only")) {
		return e
	}
	switch car(e) {
	case sym("isglobal"):
		if s != nil && truthy(s.table.get(cadr(e), false)) {
			return fFalse()
		}
		if underscoreSymbol(cadr(e)) {
			return fFalse()
		}
		return fTrue()
	case sym("global"):
		checkValidName(cadr(e))
		return e
	case sym("assign-const-if-global"):
		if varKind(cadr(e), s, false) == "local" {
			if lengthEq(e, 2) {
				return null()
			}
			return lw.resolveScopes_(cons(sAssign, cdr(e)), s, sp, loc)
		}
		return lw.resolveScopes_(cons(sym("const"), cdr(e)), s, sp, loc)
	case sym("global-if-global"):
		if varKind(cadr(e), s, false) == "local" {
			return null()
		}
		return cons(sym("global"), cdr(e))
	case sym("local"), sym("local-def"):
		checkValidName(cadr(e))
		return null()
	case sym("using"), sym("import"), sym("export"), sym("public"):
		return e
	case sym("require-existing-local"):
		if !inScope(cadr(e), s) {
			errorf("no outer local variable declaration exists for \"for outer\"")
		}
		return null()
	case sym("softscope"), sym("hardscope"):
		return null()
	case sym("locals"):
		names := filterL(func(v any) bool {
			return !isGensymName(v) && len(strings.Split(symString(v), "#")) != 2
		}, allLocalNames(s))
		names = deleteDuplicates(filterL(func(v any) bool { return v != sym("") }, mapL(unmangledName, names)))
		d := lw.makeSSAValue()
		stmts := L(sBlock, list(sAssign, d, list(sCall, list(sCall, coreRef("apply_type"), topRef("Dict"), coreRef("Symbol"), coreRef("Any")))))
		for _, v := range names {
			vr := lw.resolveScopes_(v, s, nil, false)
			stmts = append(stmts, list(sym("if"), list(sym("isdefined"), vr),
				list(sCall, topRef("setindex!"), d, vr, list(sQuote, v))))
		}
		return append(stmts, d)
	case sym("islocal"):
		if k := varKind(cadr(e), s, false); k == "global" || k == "none" {
			return fFalse()
		}
		return fTrue()
	case sLambda:
		args := lamArgnames(e)
		newScope := makeScope(asList(e), args, nil, nil, sp, nil, s, false, false, nil, nil)
		body := lw.resolveScopes_(lamBody(e), newScope, nil, false)
		popScope(newScope)
		return list(sLambda, cadr(e), caddr(e), body)
	case sym("scope-block"):
		return lw.resolveScopeBlock(e, s, loc)
	case sym("module"):
		errorf("\"module\" expression not at top level")
	case sym("break-block"):
		return list(sym("break-block"), cadr(e), lw.resolveScopes_(caddr(e), s, nil, loc))
	case sym("with-static-parameters"):
		return listStar(sym("with-static-parameters"), lw.resolveScopes_(cadr(e), s, cddr(e), loc), cddr(e))
	case sym("method"):
		if lengthGt(e, 2) {
			return list(sym("method"),
				lw.resolveScopes_(cadr(e), s, nil, false),
				lw.resolveScopes_(caddr(e), s, nil, false),
				lw.resolveScopes_(cadddr(e), s, methodExprStaticParameters(e), false))
		}
	}
	// soft-scope assignment warnings are logged and discarded by Meta.lower
	if inList(car(e), sAssign, sym("const")) && isSymbol(cadr(e)) && s != nil && isNull(lamArgs(s.lam)) {
		warnVar(cadr(e), s)
	}
	out := list(car(e))
	for _, x := range cdr(e) {
		if linenum(x) {
			loc = x
		}
		out = append(out, lw.resolveScopes_(x, s, nil, loc))
	}
	return out
}

func (lw *lowerer) resolveScopeBlock(e any, s *scope, loc any) any {
	blok := cadr(e)
	lam := s.lam
	argnames := lamArgnames(lam)
	toplevel := len(argnames) == 0 && eq(e, lamBody(lam))
	currentLocals := asList(caddr(lam))
	globals := findGlobalDecls(blok)
	assigned := findAssignedVars(blok)
	localsDef := findLocalDefDecls(blok)
	localDecls := findLocalDecls(blok)
	hard := len(argnames) == 0 && (s.hard || findScopeDecl(blok, "hardscope") != nil)
	soft := false
	if len(argnames) == 0 && !hard {
		ss := findScopeDecl(blok, "softscope")
		switch {
		case ss == nil:
			soft = s.soft
		case equal(cadr(ss), fTrue()):
			soft = true
		case equal(cadr(ss), fFalse()):
			soft = false
		default:
			soft = s.soft
		}
	}
	nonlocAssigned := filterL(func(v any) bool {
		return memq(v, localsDef) == nil && memq(v, localDecls) == nil
	}, assigned)
	var implicitGlobals []any
	if toplevel {
		implicitGlobals = nonlocAssigned
	}
	var implicitLocals []any
	if !toplevel {
		implicitLocals = filterL(func(v any) bool {
			k := varKind(v, s, true)
			return (k == "none" || k == "static-parameter") &&
				!(soft && (memq(v, s.implicitGlobals) != nil || definedJuliaGlobal(v))) &&
				memq(v, globals) == nil
		}, nonlocAssigned)
	}
	localsNondef := deleteDuplicates(appendL(localDecls, implicitLocals))
	needRename := func(vars []any) []any {
		return filterL(func(v any) bool { return memq(v, currentLocals) != nil || inScope(v, s) }, vars)
	}
	needRenameNondef := needRename(localsNondef)
	needRenameDef := needRename(localsDef)
	renamed := mapL(func(v any) any { return lw.st.gensyms.namedGensy(v) }, needRenameNondef)
	renamedDef := mapL(func(v any) any { return lw.st.gensyms.namedGensy(v) }, needRenameDef)
	newnames := appendL(diff(localsNondef, needRenameNondef), renamed)
	newnamesDef := appendL(diff(localsDef, needRenameDef), renamedDef)
	var warnVars *ftable
	if !toplevel && len(argnames) == 0 && !soft && !hard {
		vars := filterL(func(v any) bool {
			return (memq(v, s.implicitGlobals) != nil || definedJuliaGlobal(v)) &&
				varKind(v, s, false) == "none" && memq(v, globals) == nil
		}, nonlocAssigned)
		if len(vars) > 0 {
			warnVars = newTable()
			for _, v := range vars {
				warnVars.put(v, true)
			}
		}
	}
	for _, v := range globals {
		if memq(v, localsDef) != nil || memq(v, localDecls) != nil {
			errorf("variable \"%s\" declared both local and global", symString(v))
		}
		if k := varKind(v, s, false); len(argnames) == 0 && (k == "argument" || k == "local") {
			errorf("`global %s`: %s is a local variable in its enclosing scope", symString(v), symString(v))
		}
	}
	if len(argnames) > 0 && eq(e, lamBody(lam)) {
		for _, v := range localDecls {
			if memq(v, argnames) != nil {
				errorf("local variable name \"%s\" conflicts with an argument", symString(v))
			}
		}
	}
	for _, lst := range [][]any{localDecls, implicitLocals} {
		for _, v := range lst {
			if varKind(v, s, false) == "static-parameter" {
				errorf("local variable name \"%s\" conflicts with a static parameter", symString(v))
			}
		}
	}
	if lam != nil {
		lam[2] = appendL(asList(caddr(lam)), newnames, newnamesDef)
	}
	var renames [][2]any
	for i, v := range needRenameNondef {
		renames = append(renames, [2]any{v, renamed[i]})
	}
	for i, v := range needRenameDef {
		renames = append(renames, [2]any{v, renamedDef[i]})
	}
	ig := s.implicitGlobals
	if toplevel {
		ig = implicitGlobals
	}
	newScope := makeScope(lam, nil, appendL(localsNondef, localsDef), globals, nil, renames, s,
		soft && len(argnames) == 0, hard, ig, warnVars)
	body := lw.resolveScopes_(blok, newScope, nil, loc)
	popScope(newScope)
	decls := appendL(mapL(func(v any) any { return list(sym("local"), v) }, newnames),
		mapL(func(v any) any { return list(sym("local-def"), v) }, newnamesDef))
	return insertAfterMeta(blockify(body, nil), decls)
}

// definedJuliaGlobal is defined-julia-global, which julia overrides with
// a lookup of the binding in the module being lowered. The oracle lowers
// in a fresh module whose bindings lowering itself never defines.
func definedJuliaGlobal(any) bool { return false }

func unmangledName(v any) any {
	if v == sym("") {
		return v
	}
	s := symString(v)
	if s[0] == '#' {
		parts := strings.Split(s, "#")
		return sym(parts[len(parts)-1])
	}
	return v
}
