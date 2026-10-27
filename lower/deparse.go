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

func deparseArglist(l []any, sep string) string {
	if hasParameters(l) {
		s := strings.Join(mapStr(deparse0, l[1:]), sep)
		if len(l[1:]) == 1 {
			s += ","
		}
		return s + deparse0(l[0])
	}
	return strings.Join(mapStr(deparse0, l), sep)
}

func mapStr(f func(any) string, l []any) []string {
	out := make([]string, len(l))
	for i, x := range l {
		out[i] = f(x)
	}
	return out
}

func deparse0(e any) string { return deparse(e) }

func deparsePrefixCall(hd any, args []any, opn, cls string) string {
	h := deparse(hd)
	if isDecl(hd) || hd == sym(":") {
		h = "(" + h + ")"
	}
	return h + opn + deparseArglist(args, ", ") + cls
}

func deparse(e any) string {
	switch x := e.(type) {
	case sym:
		return string(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case sstr:
		return strconv.Quote(string(x))
	case string:
		return reprJuliaString(x)
	case bool:
		if x {
			return "#t"
		}
		return "#f"
	}
	if !isPair(e) {
		return juliaRepr(e)
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
	case sDecl:
		if lengthEq(e, 2) {
			return "::" + deparse(cadr(e))
		}
		return deparse(cadr(e)) + "::" + deparse(caddr(e))
	case sym("kw"):
		return deparse(cadr(e)) + " = " + deparse(caddr(e))
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
	case sym("parameters"):
		return "; " + deparseArglist(cdr(e), ", ")
	case sym("where"):
		if lengthEq(e, 3) {
			return deparse(cadr(e)) + " where " + deparse(caddr(e))
		}
		return deparse(cadr(e)) + " where " + deparse(cons(sym("braces"), cddr(e)))
	case sym("braces"):
		return "{" + deparseArglist(cdr(e), ", ") + "}"
	case sTop:
		return deparse(cadr(e))
	case sCore:
		return "Core." + deparse(cadr(e))
	case sym("globalref"):
		return deparse(cadr(e)) + "." + deparseColonDot(caddr(e))
	case sym("ssavalue"):
		return "SSAValue(" + deparse(cadr(e)) + ")"
	case sym("copyast"):
		return deparse(cadr(e))
	case sQuote, sInert:
		if s, ok := cadr(e).(sym); ok && s != "" && s[0] != '=' && s[0] != ':' {
			return ":" + string(s)
		}
		return ":(" + deparse(cadr(e)) + ")"
	case sym("macrocall"):
		return deparse(cadr(e)) + " " + deparseArglist(cddr(e), " ")
	}
	if !isSymbol(h) {
		// no case of ast.scm's deparse matches: (else (string e))
		return symString(e)
	}
	panic(unsupportedf("deparse of %s", deparseHead(e)))
}

func deparseHead(e any) string {
	if s, ok := car(e).(sym); ok {
		return string(s)
	}
	return "list"
}

func deparseColonDot(e any) string {
	if isDotop(e) {
		return ":" + deparse(e)
	}
	return deparse(e)
}
