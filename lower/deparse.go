package lower

// deparse (src/ast.scm), for the error messages of lowering.

import (
	"strconv"
	"strings"
)

var syntacticOperators = setOf(addDots(fields(`&& || = += -= *= /= //= \= ^= ÷= %= <<= >>= >>>= |= &= ⊻=`)),
	fields(`:= $= . ... ->`))

func isOperator(x any) bool {
	s, ok := x.(sym)
	if !ok {
		return false
	}
	return operatorSet[maybeStripOpSuffix(string(s))]
}

var operatorSet = func() map[string]bool {
	m := setOf([]string{"->", "'", ".'", "..."}, unaryOps, precAssignmentList, precPair,
		precConditional, precArrow, precLazyOr, precLazyAnd, precComparison, precPipeLt,
		precPipeGt, precColon, precPlus, precTimes, precRational, precBitshift, precPower,
		precDecl, precDot)
	for op := range m {
		if isWordOperator(op) {
			delete(m, op)
		}
	}
	return m
}()

// deparseArglist is deparse-arglist.
func deparseArglist(l []any, sep string) string {
	if hasParameters(l) {
		s := strings.Join(mapStr(deparse, l[1:]), sep)
		if len(l[1:]) == 1 {
			s += ","
		}
		return s + deparse(l[0])
	}
	return strings.Join(mapStr(deparse, l), sep)
}

func mapStr(f func(any) string, l []any) []string {
	out := make([]string, len(l))
	for i, x := range l {
		out[i] = f(x)
	}
	return out
}

// deparsePrefixCall is deparse-prefix-call.
func deparsePrefixCall(hd any, args []any, opn, cls string) string {
	h := deparse(hd)
	if isDecl(hd) || hd == sym(":") {
		h = "(" + h + ")"
	}
	return h + opn + deparseArglist(args, ", ") + cls
}

// deparseGenerator is deparse-generator.
func deparseGenerator(e any) string {
	if car(e) == sym("flatten") {
		return deparseFlatten(e, nil, true)
	}
	return deparse(cadr(e)) + " for " + deparseArglist(cddr(e), ", ")
}

// deparseFlatten is deparse-flatten.
func deparseFlatten(e any, iters []string, flat bool) string {
	switch {
	case flat && is(e, "generator"):
		return deparseFlatten(cadr(e), append([]string{deparseArglist(cddr(e), ", ")}, iters...), false)
	case flat && is(e, "flatten"):
		e = cadr(e)
		return deparseFlatten(cadr(e), append([]string{deparseArglist(cddr(e), ", ")}, iters...), true)
	}
	rev := make([]string, len(iters))
	for i, s := range iters {
		rev[len(iters)-1-i] = s
	}
	return deparse(e) + " for " + strings.Join(rev, " for ")
}

// indentedBlock is indented-block.
func indentedBlock(lst []any, ilvl int) string {
	parts := make([]string, len(lst))
	for i, ex := range lst {
		parts[i] = strings.Repeat("    ", ilvl+1) + deparseIndent(ex, ilvl+1)
	}
	s := strings.Join(parts, "\n")
	if len(lst) > 0 {
		s += "\n"
	}
	return s
}

// deparseBlock is deparse-block.
func deparseBlock(head string, lst []any, ilvl int) string {
	return head + "\n" + indentedBlock(lst, ilvl) + strings.Repeat("    ", ilvl) + "end"
}

func deparseColonDot(e any) string {
	if isDotop(e) {
		return ":" + deparse(e)
	}
	return deparse(e)
}

// deparseImportPath is deparse-import-path.
func deparseImportPath(e any) string {
	switch {
	case is(e, "."):
		lst := cdr(e)
		ndots := 0
		for len(lst) > 0 && lst[0] == sym(".") {
			lst = lst[1:]
			ndots++
		}
		return strings.Repeat(".", ndots) + strings.Join(mapStr(deparse, lst), ".")
	case is(e, ":"):
		return deparseImportPath(cadr(e)) + ": " + strings.Join(mapStr(deparseImportPath, cddr(e)), ", ")
	}
	return symString(e)
}

func deparseSemicolons(n any) string {
	k, _ := n.(int64)
	return strings.Repeat(";", int(max(k, 0)))
}

func deparse(e any) string { return deparseIndent(e, 0) }

// blockStmts is the statements of a block, or the one statement.
func blockStmts(e any) []any {
	if is(e, sBlock) {
		return cdr(e)
	}
	return list(e)
}

// deparseIndent is (deparse e ilvl).
func deparseIndent(e any, ilvl int) string {
	switch x := e.(type) {
	case sym:
		return string(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case sstr:
		return strconv.Quote(string(x))
	case bool:
		return symString(x)
	}
	if isJuliaValue(e) {
		return juliaRepr(e)
	}
	if !isPair(e) {
		return symString(e)
	}
	h := car(e)
	switch {
	case h == sym("."):
		if lengthEq(e, 2) {
			return "(." + deparse(cadr(e)) + ")"
		}
		a := caddr(e)
		var rhs string
		switch {
		case isPair(a) && inList(car(a), sQuote, sInert):
			rhs = deparseColonDot(cadr(a))
		case is(a, "copyast"):
			rhs = deparseColonDot(cadr(cadr(a)))
		default:
			rhs = "(" + deparse(a) + ")"
		}
		return deparse(cadr(e)) + "." + rhs
	case h == sym("...") || h == sym("'"):
		return deparse(cadr(e)) + symString(h)
	case isSymbol(h) && (syntacticOperators[string(h.(sym))] || h == sym("<:") || h == sym(">:") || h == sym("-->")):
		if lengthEq(e, 2) {
			return symString(h) + deparse(cadr(e))
		}
		return deparse(cadr(e)) + " " + symString(h) + " " + deparse(caddr(e))
	case h == sym("as"):
		return deparseImportPath(cadr(e)) + " as " + deparse(caddr(e))
	}
	switch h {
	case sNull:
		return "nothing"
	case sTrue:
		return "true"
	case sFalse:
		return "false"
	case sCall:
		if cadr(e) == sym(":") && (lengthEq(e, 4) || lengthEq(e, 5)) {
			s := deparse(caddr(e)) + ":" + deparse(cadddr(e))
			if lengthGt(e, 4) {
				s += ":" + deparse(listRef(e, 4))
			}
			return s
		}
		if lengthEq(e, 4) && isOperator(cadr(e)) {
			return "(" + deparse(caddr(e)) + " " + symString(cadr(e)) + " " + deparse(cadddr(e)) + ")"
		}
		return deparsePrefixCall(cadr(e), cddr(e), "(", ")")
	case sym("$"), sym("&"):
		if isPair(cadr(e)) && !inList(car(cadr(e)), sNull, sTrue, sFalse, sym("tuple"), sym("$"), sym("vect"), sym("braces")) {
			return symString(h) + "(" + deparse(cadr(e)) + ")"
		}
		return symString(h) + deparse(cadr(e))
	case sDecl:
		if lengthEq(e, 2) {
			return "::" + deparse(cadr(e))
		}
		return deparse(cadr(e)) + "::" + deparse(caddr(e))
	case sym("comparison"):
		return strings.Join(mapStr(deparse, cdr(e)), " ")
	case sym("macrocall"):
		return symString(cadr(e)) + " " + deparseArglist(cddr(e), " ")
	case sym("kw"):
		return deparse(cadr(e)) + " = " + deparse(caddr(e))
	case sym("where"):
		if lengthEq(e, 3) {
			return deparse(cadr(e)) + " where " + deparse(caddr(e))
		}
		return deparse(cadr(e)) + " where " + deparse(cons(sym("braces"), cddr(e)))
	case sym("parameters"):
		return "; " + deparseArglist(cdr(e), ", ")
	case sym("tuple"):
		s := "(" + deparseArglist(cdr(e), ", ")
		if lengthEq(e, 2) {
			s += ","
		}
		return s + ")"
	case sym("ref"):
		return deparsePrefixCall(cadr(e), cddr(e), "[", "]")
	case sym("curly"):
		return deparsePrefixCall(cadr(e), cddr(e), "{", "}")
	case sym("vect"):
		return "[" + deparseArglist(cdr(e), ", ") + "]"
	case sym("vcat"):
		return "[" + deparseArglist(cdr(e), "; ") + "]"
	case sym("typed_vcat"):
		return deparse(cadr(e)) + deparse(cons(sym("vcat"), cddr(e)))
	case sym("hcat"):
		return "[" + deparseArglist(cdr(e), " ") + "]"
	case sym("typed_hcat"):
		return deparse(cadr(e)) + deparse(cons(sym("hcat"), cddr(e)))
	case sym("ncat"):
		s := "[" + deparseArglist(cddr(e), deparseSemicolons(cadr(e))+" ")
		if len(cddr(e)) <= 1 {
			s += deparseSemicolons(cadr(e))
		}
		return s + "]"
	case sym("typed_ncat"):
		return deparse(cadr(e)) + deparse(cons(sym("ncat"), cddr(e)))
	case sym("row"):
		return deparseArglist(cdr(e), " ")
	case sym("nrow"):
		return deparseArglist(cddr(e), deparseSemicolons(cadr(e))+" ")
	case sym("braces"):
		return "{" + deparseArglist(cdr(e), ", ") + "}"
	case sym("bracescat"):
		return "{" + deparseArglist(cdr(e), "; ") + "}"
	case sym("string"):
		parts := mapStr(func(s any) string {
			switch v := unbox(s).(type) {
			case string:
				return v
			case sstr:
				return string(v)
			case sym:
				return "$" + string(v)
			}
			return "$(" + deparse(s) + ")"
		}, cdr(e))
		return "\"" + strings.Join(parts, "") + "\""
	case sym("generator"), sym("flatten"):
		return "(" + deparseGenerator(e) + ")"
	case sym("filter"):
		return deparse(caddr(e)) + " if " + deparse(cadr(e))
	case sym("comprehension"):
		return "[ " + deparseGenerator(cadr(e)) + " ]"
	case sym("typed_comprehension"):
		return deparse(cadr(e)) + deparse(cons(sym("comprehension"), cddr(e)))
	case sBlock:
		return deparseBlock("begin", cdr(e), ilvl)
	case sym("toplevel"):
		return strings.Join(mapStr(deparse, cdr(e)), "\n")
	case sym("function"), sym("macro"), sym("for"), sym("while"):
		var stmts []any
		if len(cddr(e)) > 0 {
			stmts = blockStmts(caddr(e))
		}
		return deparseBlock(symString(h)+" "+deparse(cadr(e)), stmts, ilvl)
	case sReturn:
		return "return " + deparse(cadr(e))
	case sym("break"), sym("continue"):
		return symString(h)
	case sym("if"), sym("elseif"):
		cond := cadr(e)
		if h == sym("elseif") {
			cond = caddr(cadr(e))
		}
		if lengthEq(e, 3) {
			return deparseBlock(symString(h)+" "+deparse(cond), cdr(caddr(e)), ilvl)
		}
		s := symString(h) + " " + deparse(cond) + "\n" + indentedBlock(cdr(caddr(e)), ilvl)
		if els := cadddr(e); is(els, "elseif") {
			return s + deparse(els)
		} else {
			return s + deparseBlock("else", cdr(els), ilvl)
		}
	case sym("let"):
		return deparseBlock("let "+strings.Join(mapStr(deparse, cdadr(e)), ", "), cdr(caddr(e)), ilvl)
	case sym("try"):
		return deparseTry(asList(e), ilvl)
	case sym("do"):
		call, args, body := cadr(e), cdr(cadr(caddr(e))), caddr(caddr(e))
		sp := ""
		if len(args) > 0 {
			sp = " "
		}
		return deparseBlock(deparse(call)+" do"+sp+deparseArglist(args, ", "), cdr(body), ilvl)
	case sym("struct"):
		mut := ""
		if equal(cadr(e), list(sTrue)) {
			mut = "mutable "
		}
		return mut + "struct " + deparseBlock(deparse(caddr(e)), cdr(cadddr(e)), ilvl)
	case sym("abstract"):
		return "abstract type " + deparse(cadr(e)) + " end"
	case sym("primitive"):
		return "primitive type " + deparse(cadr(e)) + " " + deparse(caddr(e)) + " end"
	case sym("module"):
		kind := "baremodule "
		if equal(cadr(e), list(sTrue)) {
			kind = "module "
		}
		return kind + symString(caddr(e)) + "\n" + strings.Join(mapStr(deparse, cdr(cadddr(e))), "\n") + "\nend"
	case sym("import"), sym("using"):
		return symString(h) + " " + strings.Join(mapStr(deparseImportPath, cdr(e)), ", ")
	case sym("global"), sym("local"), sym("export"), sym("public"):
		return symString(h) + " " + strings.Join(mapStr(deparse, cdr(e)), ", ")
	case sym("const"):
		return "const " + deparse(cadr(e))
	case sTop:
		return deparse(cadr(e))
	case sCore:
		return "Core." + deparse(cadr(e))
	case sym("globalref"):
		return deparse(cadr(e)) + "." + deparseColonDot(caddr(e))
	case sym("ssavalue"):
		return "SSAValue(" + symString(cadr(e)) + ")"
	case sLine:
		if lengthEq(e, 2) {
			return "# line " + symString(cadr(e))
		}
		return "# " + symString(caddr(e)) + ", line " + symString(cadr(e))
	case sym("copyast"):
		return deparse(cadr(e))
	case sQuote, sInert:
		if s, ok := cadr(e).(sym); ok && s != "" && s[0] != '=' && s[0] != ':' {
			return ":" + deparse(s)
		}
		return ":(" + deparse(cadr(e)) + ")"
	}
	// no case matches: (else (string e))
	return symString(e)
}

// deparseTry is the try case of deparse.
func deparseTry(e []any, ilvl int) string {
	ind := strings.Repeat("    ", ilvl)
	s := "try\n" + indentedBlock(cdr(cadr(e)), ilvl)
	if len(e) > 3 && is(e[3], sBlock) {
		s += ind + "catch"
		if !equal(e[2], list(sFalse)) {
			s += " " + symString(e[2])
		}
		s += "\n" + indentedBlock(cdr(e[3]), ilvl)
	}
	if len(e) > 5 && is(e[5], sBlock) {
		s += ind + "else\n" + indentedBlock(cdr(e[5]), ilvl)
	}
	if len(e) > 4 && is(e[4], sBlock) {
		s += ind + "finally\n" + indentedBlock(cdr(e[4]), ilvl)
	}
	return s + ind + "end"
}
