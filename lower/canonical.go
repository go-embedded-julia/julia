package lower

import (
	"strconv"

	"github.com/go-embedded-julia/julia/expr"
)

// Canonical returns v with the front end's gensyms renumbered by first
// appearance (in serialisation order). julia's femtolisp front end names
// its temporaries `#s12` and its hygienic renamings `#15#name` from counters
// that live for the whole session (src/ast.scm: *gensy-counter* and the
// gensym pool that reset-gensyms rewinds), so the same form lowers to
// different numbers in two sessions; the plan's acceptance is "identical
// modulo numbering", and this is the numbering both sides are compared in.
//
// The gensym id (`#s12`, `#15`) becomes `#g1`, `#g2`, …; the rest of the
// name (`#name`, a module counter `#3`) is kept.
func Canonical(v any) any {
	c := &canon{ids: map[string]string{}}
	return c.walk(v)
}

type canon struct {
	ids map[string]string
}

// gensymID splits a symbol into its gensym id and the rest, or reports
// that it is not a gensym.
func gensymID(s string) (id, rest string, ok bool) {
	if len(s) < 2 || s[0] != '#' {
		return "", "", false
	}
	i := 1
	if s[i] == 's' {
		i++
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == i || (j < len(s) && s[j] != '#') {
		return "", "", false
	}
	return s[:j], s[j:], true
}

// runtimeGensym splits a symbol of Base.gensym (`##123`, `##x#123`, from
// the runtime's global counter, which macros like @testset use) into its
// prefix up to the last '#', or reports that it is not one.
func runtimeGensym(s string) (prefix string, ok bool) {
	if len(s) < 3 || s[:2] != "##" {
		return "", false
	}
	j := len(s)
	for j > 2 && s[j-1] >= '0' && s[j-1] <= '9' {
		j--
	}
	if j == len(s) || s[j-1] != '#' {
		return "", false
	}
	return s[:j], true
}

func (c *canon) sym(s expr.Symbol) expr.Symbol {
	if prefix, ok := runtimeGensym(string(s)); ok {
		g, seen := c.ids[string(s)]
		if !seen {
			g = prefix + "r" + strconv.Itoa(len(c.ids)+1)
			c.ids[string(s)] = g
		}
		return expr.Symbol(g)
	}
	id, rest, ok := gensymID(string(s))
	if !ok {
		return s
	}
	g, seen := c.ids[id]
	if !seen {
		g = "#g" + strconv.Itoa(len(c.ids)+1)
		c.ids[id] = g
	}
	return expr.Symbol(g + rest)
}

func (c *canon) walk(v any) any {
	switch x := v.(type) {
	case expr.Symbol:
		return c.sym(x)
	case *expr.Expr:
		out := expr.New(c.sym(x.Head), c.list(x.Args)...)
		return out
	case expr.Vector:
		return expr.Vector(c.list(x))
	case expr.QuoteNode:
		return expr.QuoteNode{Value: c.walk(x.Value)}
	case expr.GlobalRef:
		return expr.GlobalRef{Mod: c.sym(x.Mod), Name: c.sym(x.Name)}
	case expr.LineNumberNode:
		return expr.LineNumberNode{Line: x.Line, File: c.walk(x.File)}
	case expr.GotoIfNot:
		return expr.GotoIfNot{Cond: c.walk(x.Cond), Dest: x.Dest}
	case expr.ReturnNode:
		return expr.ReturnNode{Val: c.walk(x.Val)}
	case expr.NewvarNode:
		return expr.NewvarNode{Slot: c.walk(x.Slot)}
	case expr.EnterNode:
		x.Scope = c.walk(x.Scope)
		return x
	case *expr.CodeInfo:
		ci, _ := expr.CodeInfoFrom(c.list(x.Values()))
		return ci
	case *expr.DebugInfo:
		return &expr.DebugInfo{Def: c.walk(x.Def), Linetable: c.walk(x.Linetable),
			Edges: expr.Vector(c.list(x.Edges)), Codelocs: x.Codelocs}
	}
	return v
}

func (c *canon) list(xs []any) []any {
	out := make([]any, len(xs))
	for i, a := range xs {
		out[i] = c.walk(a)
	}
	return out
}

// CanonicalString parses a serialised value, renumbers its gensyms and
// serialises it again.
func CanonicalString(s string) (string, error) {
	v, err := expr.Parse(s)
	if err != nil {
		return "", err
	}
	return expr.Serialize(Canonical(v)), nil
}
