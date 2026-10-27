package lower

// Quasiquote support: L(a, sp(b), c) builds the list `(a ,@b c)`, so that
// Scheme templates translate one for one.

type spliced []any

// sp marks a list to splice into the enclosing L.
func sp(l any) spliced {
	if l == nil {
		return nil
	}
	return spliced(asList(l))
}

// L builds a list from its arguments, splicing the sp(...) ones.
func L(xs ...any) []any {
	n := 0
	for _, x := range xs {
		if s, ok := x.(spliced); ok {
			n += len(s)
		} else {
			n++
		}
	}
	out := make([]any, 0, n)
	for _, x := range xs {
		if s, ok := x.(spliced); ok {
			out = append(out, s...)
		} else {
			out = append(out, x)
		}
	}
	return out
}

// S is a symbol literal.
func S(s string) sym { return sym(s) }

// coreRef is (core name); topRef (top name).
func coreRef(name string) []any { return list(sCore, sym(name)) }
func topRef(name string) []any  { return list(sTop, sym(name)) }

var (
	nullV  = []any{sNull}
	trueV  = []any{sTrue}
	falseV = []any{sFalse}
)

// null is a fresh (null) list (lists are compared by identity with eq?, so
// each use gets its own).
func null() []any  { return list(sNull) }
func fTrue() []any { return list(sTrue) }
func fFalse() []any {
	return list(sFalse)
}

func iota(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = int64(i)
	}
	return out
}

func listTail(l any, n int) []any { return asList(l)[n:] }
func listHead(l any, n int) []any {
	x := asList(l)
	return x[:n:n]
}
func listRef(l any, n int) any { return asList(l)[n] }

func reverse(l any) []any {
	x := asList(l)
	out := make([]any, len(x))
	for i, v := range x {
		out[len(x)-1-i] = v
	}
	return out
}

// takeWhile is take-while.
func takeWhile(f func(any) bool, l []any) []any {
	for i, x := range l {
		if !f(x) {
			return l[:i:i]
		}
	}
	return l
}

// separate is (separate pred lst): (values in out) keeping order.
func separate(f func(any) bool, l []any) ([]any, []any) {
	var yes, no []any
	for _, x := range l {
		if f(x) {
			yes = append(yes, x)
		} else {
			no = append(no, x)
		}
	}
	return yes, no
}

// diff is utils.scm's diff: the items of s1 not memq in s2.
func diff(s1, s2 []any) []any {
	return filterL(func(x any) bool { return memq(x, s2) == nil }, s1)
}

func intersect(s1, s2 []any) []any {
	return filterL(func(x any) bool { return memq(x, s2) != nil }, s1)
}

// hasDups is has-dups: the tail at the first element that occurs again, or nil.
func hasDups(l []any) []any {
	for i, x := range l {
		if memq(x, l[i+1:]) != nil {
			return l[i:]
		}
	}
	return nil
}

// member is (member x l) with equal?.
func member(x any, l []any) []any {
	for i, y := range l {
		if equal(x, y) {
			return l[i:]
		}
	}
	return nil
}

// deleteDuplicates is femtolisp's delete-duplicates (system.lsp): for
// lists longer than 20 the first occurrence of each element is kept, for
// shorter ones the last.
func deleteDuplicates(l []any) []any {
	if len(l) > 20 {
		t := newTable()
		var out []any
		for _, x := range l {
			if !t.has(x) {
				t.put(x, true)
				out = append(out, x)
			}
		}
		return out
	}
	var out []any
	for i, x := range l {
		if member(x, l[i+1:]) == nil {
			out = append(out, x)
		}
	}
	return out
}

// exprContainsEq is expr-contains-eq.
func exprContainsEq(x, e any) bool {
	if eq(e, x) {
		return true
	}
	return isPair(e) && !quoted(e) && anyL(func(y any) bool { return exprContainsEq(x, y) }, cdr(e))
}

// exprContainsP is expr-contains-p with an optional filter.
func exprContainsP(p func(any) bool, e any, filt func(any) bool) bool {
	if filt != nil && !filt(e) {
		return false
	}
	if p(e) {
		return true
	}
	return isPair(e) && !quoted(e) && anyL(func(y any) bool { return exprContainsP(p, y, filt) }, cdr(e))
}

// contains is utils.scm's contains: p anywhere, list heads included.
func contains(p func(any) bool, e any) bool {
	if p(e) {
		return true
	}
	return isPair(e) && anyL(func(y any) bool { return contains(p, y) }, asList(e))
}

// exprReplace is expr-replace.
func exprReplace(p func(any) bool, e any, repl func(any) any) any {
	if p(e) {
		return repl(e)
	}
	if isPair(e) && !quoted(e) {
		return cons(car(e), mapL(func(x any) any { return exprReplace(p, x, repl) }, cdr(e)))
	}
	return e
}

// exprFindAll is expr-find-all.
func exprFindAll(p func(any) bool, e any, key func(any) any, filt func(any) bool) []any {
	if filt != nil && !filt(e) {
		return nil
	}
	var found []any
	if p(e) {
		found = append(found, key(e))
	}
	if isAtomS(e) || quoted(e) {
		return found
	}
	for _, x := range cdr(e) {
		found = append(found, exprFindAll(p, x, key, filt)...)
	}
	return found
}

func identity(x any) any { return x }
