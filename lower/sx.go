package lower

// The S-expressions the femtolisp front end works on, as Go values.
//
//	femtolisp                  Go
//	list (a b c), ()           []any (a nil or empty slice is the empty list)
//	symbol                     expr.Symbol
//	fixnum                     int64
//	#t, #f                     true, false
//	string                     sstr
//	julia value (opaque)       any other Go value, as package expr holds it
//
// julia_to_scm (src/ast.c) turns julia's true, false and nothing into the
// one-element lists (true), (false) and (null), so Go's bool and nil never
// stand for julia values here.
//
// Lists are treated as immutable, except where the Scheme code mutates
// them (set-car! on variable-info records), which index assignment on the
// shared slice reproduces.

import (
	"fmt"

	"github.com/go-embedded-julia/julia/expr"
)

// sstr is a femtolisp string (as opposed to a julia String, which is an
// opaque julia value here, a Go string).
type sstr string

type sym = expr.Symbol

// Common symbols.
const (
	sBlock  sym = "block"
	sCall   sym = "call"
	sAssign sym = "="
	sLine   sym = "line"
	sNull   sym = "null"
	sTrue   sym = "true"
	sFalse  sym = "false"
	sCore   sym = "core"
	sTop    sym = "top"
	sLambda sym = "lambda"
	sReturn sym = "return"
	sQuote  sym = "quote"
	sInert  sym = "inert"
	sDecl   sym = "::"
)

// list builds a list.
func list(xs ...any) []any { return xs }

func isPair(x any) bool {
	l, ok := x.([]any)
	return ok && len(l) > 0
}

func isNull(x any) bool {
	l, ok := x.([]any)
	return ok && len(l) == 0
}

func isAtomS(x any) bool { return !isPair(x) }

func isSymbol(x any) bool {
	_, ok := x.(sym)
	return ok
}

// isNumber is femtolisp's number?: fixnums, and julia numbers converted to
// femtolisp numbers do not occur (julia_to_scm keeps them opaque).
func isNumber(x any) bool {
	_, ok := x.(int64)
	return ok
}

func isSString(x any) bool {
	_, ok := x.(sstr)
	return ok
}

// jlValue is a julia value as julia_to_scm (src/ast.c) wraps it: a new
// opaque cvalue for each occurrence in the input. femtolisp compares such
// cvalues by identity (equal.c: a cvalue that is not plain data is equal?
// only to itself) and print.c labels the ones it prints more than once,
// so the port keeps the wrapper's identity with a pointer.
type jlValue struct{ v any }

// unbox is the julia value of a wrapper, and anything else unchanged.
func unbox(x any) any {
	if b, ok := x.(*jlValue); ok {
		return b.v
	}
	return x
}

// isJuliaValue is (eq? (typeof x) 'julia_value).
func isJuliaValue(x any) bool {
	switch x.(type) {
	case []any, sym, int64, bool, sstr:
		return false
	}
	return true
}

func asList(x any) []any {
	l, ok := x.([]any)
	if !ok {
		panic(internalError(fmt.Sprintf("not a list: %v", x)))
	}
	return l
}

// pairs is x as a list for the femtolisp loops that walk `while (pair? l)`:
// an atom is the empty list to them.
func pairs(x any) []any {
	l, _ := x.([]any)
	return l
}

func car(x any) any {
	l := asList(x)
	if len(l) == 0 {
		panic(internalError("car of ()"))
	}
	return l[0]
}

func cdr(x any) []any {
	l := asList(x)
	if len(l) == 0 {
		panic(internalError("cdr of ()"))
	}
	return l[1:]
}

func cadr(x any) any   { return car(cdr(x)) }
func cddr(x any) []any { return cdr(cdr(x)) }
func caddr(x any) any  { return car(cddr(x)) }
func cdddr(x any) []any {
	return cdr(cddr(x))
}
func cadddr(x any) any { return car(cdddr(x)) }
func cdar(x any) []any { return cdr(car(x)) }
func cadar(x any) any  { return car(cdar(x)) }
func caadr(x any) any  { return car(cadr(x)) }
func cdadr(x any) []any {
	return cdr(cadr(x))
}

// head is (car e) for a pair and nil otherwise: the tag the Scheme code
// dispatches on, as in (and (pair? e) (eq? (car e) 'x)).
func head(x any) sym {
	if l, ok := x.([]any); ok && len(l) > 0 {
		if s, ok := l[0].(sym); ok {
			return s
		}
	}
	return ""
}

// is reports (and (pair? x) (eq? (car x) h)).
func is(x any, h sym) bool { return head(x) == h }

// cons prepends a to the list l.
func cons(a any, l []any) []any {
	out := make([]any, 0, len(l)+1)
	out = append(out, a)
	return append(out, l...)
}

// listStar is list*: the last argument is a list the others are prepended to.
func listStar(xs ...any) []any {
	last := asList(xs[len(xs)-1])
	out := make([]any, 0, len(xs)-1+len(last))
	out = append(out, xs[:len(xs)-1]...)
	return append(out, last...)
}

// appendL is append: the lists concatenated into a new list.
func appendL(ls ...[]any) []any {
	n := 0
	for _, l := range ls {
		n += len(l)
	}
	out := make([]any, 0, n)
	for _, l := range ls {
		out = append(out, l...)
	}
	return out
}

func length(x any) int { return len(asList(x)) }

// lengthEq is length= (false for non-lists).
func lengthEq(x any, n int) bool {
	l, ok := x.([]any)
	return ok && len(l) == n
}

// lengthGt is length>.
func lengthGt(x any, n int) bool {
	l, ok := x.([]any)
	return ok && len(l) > n
}

func last(x any) any {
	l := asList(x)
	return l[len(l)-1]
}

func butlast(x any) []any {
	l := asList(x)
	if len(l) == 0 {
		return nil
	}
	return l[: len(l)-1 : len(l)-1]
}

// mapL applies f to each element.
func mapL(f func(any) any, l []any) []any {
	out := make([]any, len(l))
	for i, x := range l {
		out[i] = f(x)
	}
	return out
}

func filterL(f func(any) bool, l []any) []any {
	var out []any
	for _, x := range l {
		if f(x) {
			out = append(out, x)
		}
	}
	return out
}

func anyL(f func(any) bool, l []any) bool {
	for _, x := range l {
		if f(x) {
			return true
		}
	}
	return false
}

func everyL(f func(any) bool, l []any) bool {
	for _, x := range l {
		if !f(x) {
			return false
		}
	}
	return true
}

// eq is eq?: symbols, fixnums, booleans and strings by value; lists by
// identity (the same first cell); julia values by the identity of their
// wrapper.
func eq(a, b any) bool {
	if x, ok := a.([]any); ok {
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		return len(x) == 0 || &x[0] == &y[0]
	}
	if _, ok := b.([]any); ok {
		return false
	}
	// the atoms of an S-expression are comparable: julia values are
	// behind a *jlValue
	return a == b
}

// equal is equal?: structural equality of lists, eq? of atoms (a julia
// value is equal? only to its own wrapper).
func equal(a, b any) bool {
	x, okx := a.([]any)
	y, oky := b.([]any)
	if okx || oky {
		if !okx || !oky || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equal(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

// memq returns the tail of l starting at the first element eq? to x.
func memq(x any, l []any) []any {
	for i, y := range l {
		if eq(x, y) {
			return l[i:]
		}
	}
	return nil
}

func inList(x any, l ...any) bool { return memq(x, l) != nil }

// assq returns the first pair of the association list whose car is eq? to k.
func assq(k any, alist []any) []any {
	for _, p := range alist {
		if isPair(p) && eq(car(p), k) {
			return asList(p)
		}
	}
	return nil
}

// truthy is Scheme truth: everything but #f.
func truthy(x any) bool {
	b, ok := x.(bool)
	return !ok || b
}

// loweringError is (error msg): error-wrap turns it into Expr(:error, msg).
type loweringError string

// loweringErrorValue is (error v) for a message that is not a femtolisp
// string (a julia String from an Expr(:error) of the input): error-wrap
// returns (error v) unchanged.
type loweringErrorValue struct{ msg any }

// internalError marks a broken invariant of the port (femtolisp would
// raise a type error there).
type internalError string

func errorf(format string, args ...any) {
	panic(loweringError(fmt.Sprintf(format, args...)))
}
