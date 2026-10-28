package lower

// Port of src/ast.scm (julia 1.12.6): predicates and accessors on the
// S-expression AST, gensyms, variable-info records.

import (
	"strconv"
	"strings"

	"github.com/go-embedded-julia/julia/expr"
)

// gensyms is the front end's gensym state (ast.scm: *gensyms*,
// *current-gensyms*, *gensy-counter*). It lives for the whole session in
// julia; here for the State of a file. Its numbering does not matter
// (outputs are compared after renumbering), only which names are distinct.
type gensyms struct {
	pool    []sym // *gensyms*, newest first
	current int   // index into pool of *current-gensyms*
	counter int
}

func (g *gensyms) reset() { g.current = 0 }

// passInput moves the counter past every front-end gensym of an input
// form. In julia, macro expansion names its renamings (`#12#x`) from the
// same session-wide *gensy-counter* lowering then draws from, so a name
// lowering makes never equals one already in its input; a State that
// starts its counter elsewhere must keep that property, or a fresh
// `#12#y` shares its number with the input's `#12#x`.
func (g *gensyms) passInput(v any) {
	walkSymbols(v, 0, func(s string) {
		id, _, ok := gensymID(s)
		if !ok {
			return
		}
		// #12, #s12, and #g12, the canonical form
		digits := strings.TrimLeft(strings.TrimPrefix(id, "#"), "sg")
		n, err := strconv.Atoi(digits)
		if err == nil && n >= g.counter && n < 1<<40 {
			g.counter = n + 1
		}
	})
}

// walkSymbols calls f on every symbol of an expr value, to a bounded depth.
func walkSymbols(v any, depth int, f func(string)) {
	if depth > maxWalkDepth {
		return
	}
	switch x := v.(type) {
	case expr.Symbol:
		f(string(x))
	case *expr.Expr:
		f(string(x.Head))
		for _, a := range x.Args {
			walkSymbols(a, depth+1, f)
		}
	case expr.Vector:
		for _, a := range x {
			walkSymbols(a, depth+1, f)
		}
	case expr.QuoteNode:
		walkSymbols(x.Value, depth+1, f)
	case expr.GlobalRef:
		f(string(x.Name))
	}
}

// maxWalkDepth bounds the recursion of walkSymbols on hostile input.
const maxWalkDepth = 10000

// gensy is (gensy).
func (g *gensyms) gensy() sym {
	if g.current >= len(g.pool) {
		if g.counter == 0 {
			g.counter = 1
		}
		s := sym("#s" + strconv.Itoa(g.counter))
		g.counter++
		g.pool = append([]sym{s}, g.pool...)
		g.current = len(g.pool) // the new one is consumed
		return s
	}
	s := g.pool[g.current]
	g.current++
	return s
}

// namedGensy is (named-gensy name).
func (g *gensyms) namedGensy(name any) sym {
	if g.counter == 0 {
		g.counter = 1
	}
	s := sym("#" + strconv.Itoa(g.counter) + "#" + symString(name))
	g.counter++
	return s
}

// symString is (string x) for the atoms the front end turns into names.
func symString(x any) string {
	switch v := x.(type) {
	case sym:
		return string(v)
	case sstr:
		return string(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case []any:
		// a list, as femtolisp's display prints it: a closure named after
		// a hygienic (globalref m f) becomes "#(globalref #<julia: M> f)#1"
		p := &flPrinter{labels: map[string]int{}, seen: map[string]int{}}
		p.traverse(v)
		return p.print(v)
	case bool:
		if v {
			return "#t"
		}
		return "#f"
	case expr.Module:
		return moduleString(v)
	}
	panic(internalError("string of an opaque value"))
}

// moduleString is how femtolisp prints a julia Module (print.c:
// "#<julia: " jl_static_print ">"); jl_static_show leaves out the Main of
// a module's path.
func moduleString(v expr.Module) string {
	path := v.Path
	if len(path) > 1 && path[0] == "Main" {
		path = path[1:]
	}
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = string(p)
	}
	return "#<julia: " + strings.Join(parts, ".") + ">"
}

// flPrinter prints a list as femtolisp's display does (print.c), with the
// labels it gives an object printed more than once: `#0=` before its
// first occurrence, `#0#` for the others. The port keeps no identity for
// the conses and strings, so only a julia Module, which hygiene shares
// between its (globalref m x) forms, is labelled.
type flPrinter struct {
	seen   map[string]int // occurrences found by traverse
	labels map[string]int // print_traverse's label of a shared object
	next   int
	done   map[string]bool
}

func (p *flPrinter) traverse(v any) {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			p.traverse(e)
		}
	case expr.Module:
		k := moduleString(x)
		p.seen[k]++
		if p.seen[k] == 2 {
			p.labels[k] = p.next
			p.next++
		}
	}
}

func (p *flPrinter) print(v any) string {
	switch x := v.(type) {
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = p.print(e)
		}
		return "(" + strings.Join(parts, " ") + ")"
	case expr.Module:
		k := moduleString(x)
		n, shared := p.labels[k]
		if !shared {
			return k
		}
		if p.done == nil {
			p.done = map[string]bool{}
		}
		if p.done[k] {
			return "#" + strconv.Itoa(n) + "#"
		}
		p.done[k] = true
		return "#" + strconv.Itoa(n) + "=" + k
	}
	return symString(v)
}

func quoted(e any) bool {
	return inList(car(e), sym("quote"), sTop, sCore, sym("globalref"), sLine, sym("break"),
		sInert, sym("meta"), sym("inbounds"), sym("inline"), sym("noinline"), sym("loopinfo"))
}

func quotify(e any) any { return list(sQuote, e) }

func unquote(e any) any {
	if isPair(e) && inList(car(e), sQuote, sInert) {
		return cadr(e)
	}
	return e
}

func quotedSym(e any) bool {
	return lengthEq(e, 2) && inList(car(e), sQuote, sInert) && isSymbol(cadr(e))
}

func lamArgs(x any) []any  { return asList(cadr(x)) }
func lamVinfo(x any) []any { return asList(caddr(x)) }
func lamBody(x any) any    { return cadddr(x) }
func lamSp(x any) []any    { return asList(cadddr(caddr(x))) }

func lamArgnames(x any) []any { return llistVars(lamArgs(x)) }

func badFormalArgument(v any) {
	errorf("%s", "\""+deparse(v)+"\" is not a valid function argument name")
}

func validName(s any) bool { return !inList(s, sym("ccall"), sym("cglobal")) }

func argName(v any) any {
	switch {
	case isSymbol(v) && validName(v):
		return v
	case !isPair(v):
		badFormalArgument(v)
	}
	switch car(v) {
	case sym("..."):
		argName(cadr(v))
		return declVar(cadr(v))
	case sDecl:
		if !isSymbol(cadr(v)) {
			badFormalArgument(cadr(v))
		}
		return declVar(v)
	case sym("meta"):
		if nospecializeMeta(v, true) {
			return argName(caddr(v))
		}
		badFormalArgument(v)
	case sym("kw"):
		return argName(cadr(v))
	}
	badFormalArgument(v)
	return nil
}

func argType(v any) any {
	switch {
	case isSymbol(v):
		return coreRef("Any")
	case !isPair(v):
		badFormalArgument(v)
	}
	switch car(v) {
	case sym("..."):
		if length(v) == 3 {
			return L(S("..."), declType(cadr(v)), caddr(v))
		}
		return L(S("..."), declType(cadr(v)))
	case sDecl:
		if !isSymbol(cadr(v)) {
			badFormalArgument(cadr(v))
		}
		return declType(v)
	case sym("meta"):
		if nospecializeMeta(v, true) {
			return argType(caddr(v))
		}
		badFormalArgument(v)
	case sym("kw"):
		return argType(cadr(v))
	}
	badFormalArgument(v)
	return nil
}

func llistVars(lst []any) []any {
	return mapL(argName, filterL(func(a any) bool { return !is(a, "parameters") }, lst))
}

func llistTypes(lst []any) []any { return mapL(argType, lst) }

func isDecl(e any) bool    { return is(e, sDecl) }
func symdecl(e any) bool   { return isSymbol(e) || isDecl(e) }
func ssavalue(e any) bool  { return is(e, "ssavalue") }
func isSlot(e any) bool    { return is(e, "slot") }
func globalref(e any) bool { return is(e, "globalref") }
func nothingP(e any) bool  { return is(e, sNull) }

func eventuallyDecl(e any) bool {
	return isSymbol(e) || (isPair(e) && inList(car(e), sDecl, sym("atomic"), sym("const")) && eventuallyDecl(cadr(e)))
}

func makeDecl(n, t any) []any { return list(sDecl, n, t) }

func symbolLike(e any) bool { return isSymbol(e) || ssavalue(e) }

// simpleAtom is simple-atom?.
func simpleAtom(x any) bool {
	switch x.(type) {
	case int64, sstr:
		return true
	}
	if isPair(x) {
		return inList(car(x), sym("ssavalue"), sNull, sTrue, sFalse, sym("thismodule"))
	}
	return isJuliaValue(x)
}

func effectFree(e any) bool {
	return !isPair(e) || ssavalue(e) || symDot(e) || quoted(e) || inList(car(e), sNull, sTrue, sFalse)
}

func declVar(v any) any {
	if isDecl(v) {
		return cadr(v)
	}
	return v
}

func declType(v any) any {
	if isDecl(v) {
		return caddr(v)
	}
	return coreRef("Any")
}

func symDot(e any) bool {
	return lengthEq(e, 3) && car(e) == sym(".") && symbolLike(cadr(e))
}

func undotName(e any) any {
	if is(e, ".") {
		return cadr(caddr(e))
	}
	return e
}

func identifierName(e any) any {
	switch {
	case isSymbol(e):
		return e
	case globalref(e):
		return caddr(e)
	}
	return e
}

func dotopNamed(e any) bool { return isDotop(identifierName(e)) }

func undotop(op any) any {
	if globalref(op) {
		return L(S("globalref"), cadr(op), undotop(caddr(op)))
	}
	s := symString(op)
	if s[0] != '.' {
		panic(internalError("undotop"))
	}
	return sym(s[1:])
}

func checkDotop(e any) any {
	if dotopNamed(e) {
		errorf("invalid function name \"%s\"", deparse(e))
	}
	if isPair(e) {
		if car(e) == sym(".") && lengthEq(e, 3) {
			checkDotop(caddr(e))
		} else if quoted(e) {
			checkDotop(cadr(e))
		}
	}
	return e
}

func vararg(x any) bool { return is(x, "...") }

func varargTypeExpr(x any) bool {
	return x == sym("Vararg") ||
		(lengthGt(x, 1) && ((car(x) == sym("curly") && varargTypeExpr(cadr(x))) ||
			(car(x) == sym("where") && varargTypeExpr(cadr(x)))))
}

func varargexpr(x any) bool { return is(x, "::") && varargTypeExpr(caddr(x)) }

func linenum(x any) bool { return is(x, sLine) }

func makeAssignment(l, r any) []any { return list(sAssign, l, r) }
func assignment(e any) bool         { return is(e, sAssign) }
func isReturn(e any) bool           { return is(e, sReturn) }

func tupleCall(e any) bool {
	return lengthGt(e, 1) && car(e) == sCall && equal(cadr(e), coreRef("tuple"))
}

func eqSym(a, b any) bool {
	return eq(a, b) || (ssavalue(a) && ssavalue(b) && equal(cdr(a), cdr(b)))
}

// blockify is (blockify e [lno]); lno may be nil.
func blockify(e any, lno any) []any {
	var lnos []any
	if lno != nil && truthy(lno) {
		lnos = list(lno)
	}
	if is(e, sBlock) {
		if len(cdr(e)) == 0 {
			return L(sBlock, sp(lnos), null())
		}
		if len(lnos) == 0 {
			return asList(e)
		}
		return L(sBlock, sp(lnos), sp(cdr(e)))
	}
	return L(sBlock, sp(lnos), e)
}

// var-info records: (name type flags), mutated in place.
func makeVarInfo(name any) any { return list(name, coreRef("Any"), int64(0)) }

func vinfoName(v any) any { return car(v) }
func vinfoType(v any) any { return cadr(v) }
func vinfoSetType(v any, t any) {
	asList(v)[1] = t
}
func vinfoFlags(v any) int64 { return caddr(v).(int64) }
func vinfoFlag(v any, b int64) bool {
	return vinfoFlags(v)&b != 0
}
func vinfoCapt(v any) bool       { return vinfoFlag(v, 1) }
func vinfoAsgn(v any) bool       { return vinfoFlag(v, 2) }
func vinfoNeverUndef(v any) bool { return vinfoFlag(v, 4) }
func vinfoRead(v any) bool       { return vinfoFlag(v, 8) }
func vinfoSa(v any) bool         { return vinfoFlag(v, 16) }

func setBit(x, b int64, val bool) int64 {
	if val {
		return x | b
	}
	return x &^ b
}

func vinfoSetFlag(v any, b int64, val bool) {
	asList(v)[2] = setBit(vinfoFlags(v), b, val)
}
func vinfoSetCapt(v any, c bool)       { vinfoSetFlag(v, 1, c) }
func vinfoSetAsgn(v any, c bool)       { vinfoSetFlag(v, 2, c) }
func vinfoSetNeverUndef(v any, c bool) { vinfoSetFlag(v, 4, c) }
func vinfoSetRead(v any, c bool)       { vinfoSetFlag(v, 8, c) }
func vinfoSetSa(v any, c bool)         { vinfoSetFlag(v, 16, c) }
func vinfoSetCalled(v any, c bool)     { vinfoSetFlag(v, 64, c) }

func varInfoFor(name any, vi []any) []any { return assq(name, vi) }

func assignmentLike(e any) bool { return isPair(e) && isPrecAssignment(car(e)) }

func kwarg(e any) bool { return is(e, "kw") }

func nospecializeMeta(e any, one bool) bool {
	ok := lengthGt(e, 2)
	if one {
		ok = lengthEq(e, 3)
	}
	return ok && car(e) == sym("meta") && inList(cadr(e), sym("nospecialize"), sym("specialize"))
}

func isMeta(e any) bool { return lengthGt(e, 1) && car(e) == sym("meta") }

func methodMetaSym(x any) bool {
	return inList(x, sym("inline"), sym("noinline"), sym("aggressive_constprop"), sym("no_constprop"), sym("propagate_inbounds"))
}

func propagateMethodMeta(e any) any {
	return L(S("meta"), sp(filterL(func(x any) bool { return methodMetaSym(x) || is(x, "purity") }, cdr(e))))
}

func argwideNospecializeMeta(e any) bool {
	return lengthEq(e, 2) && car(e) == sym("meta") && inList(cadr(e), sym("nospecialize"), sym("specialize"))
}

func ifGenerated(e any) bool {
	return lengthEq(e, 4) && car(e) == sym("if") && equal(cadr(e), list(sym("generated")))
}

func generatedMeta(e any) bool {
	return lengthEq(e, 3) && car(e) == sym("meta") && cadr(e) == sym("generated")
}

func generatedOnlyMeta(e any) bool {
	return lengthEq(e, 2) && car(e) == sym("meta") && cadr(e) == sym("generated_only")
}

func functionDef(e any) bool {
	return isPair(e) && (car(e) == sym("function") || car(e) == sym("->") ||
		(car(e) == sAssign && lengthEq(e, 3) && eventuallyCall(cadr(e))))
}

// flattenEx is (flatten-ex head e).
func flattenEx(h sym, e any) any {
	if isAtomS(e) {
		return e
	}
	out := list(car(e))
	for _, x := range cdr(e) {
		if is(x, h) {
			out = append(out, cdr(flattenEx(h, x))...)
		} else {
			out = append(out, x)
		}
	}
	return out
}

func flattenBlocks(e any) any { return flattenEx(sBlock, e) }

// underscoreSymbol is underscore-symbol? (julia-parser.scm).
func underscoreSymbol(e any) bool {
	s, ok := e.(sym)
	return ok && s != "" && strings.Trim(string(s), "_") == ""
}
