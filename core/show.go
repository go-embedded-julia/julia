package core

import (
	"strconv"
	"strings"
)

// qualified are the Core names Main does not export, shown with their module
// so that the text Show produces can be read back by julia.
var qualified = map[string]bool{
	"TypeofBottom": true, "TypeofVararg": true, "TypeName": true, "SimpleVector": true, "BFloat16": true,
}

// Show writes v as Julia source that julia reads back as an equal value.
// It is not julia's show: it never abbreviates (Vector{T} is Array{T, 1}),
// it brackets every where clause, and it spells type variable bounds out.
func Show(v any) string {
	var b strings.Builder
	show(&b, v)
	return b.String()
}

func show(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("#null")
	case *bottomType:
		b.WriteString("Union{}")
	case *DataType:
		if x.Name.Module == "Core" && qualified[x.Name.Name] {
			b.WriteString("Core.")
		}
		b.WriteString(x.Name.Name)
		if len(x.Parameters) == 0 && x.Name == TupleTypeName {
			b.WriteString("{}")
		}
		if len(x.Parameters) > 0 && x != AnyTupleType {
			b.WriteByte('{')
			for i, p := range x.Parameters {
				if i > 0 {
					b.WriteString(", ")
				}
				show(b, p)
			}
			b.WriteByte('}')
		}
	case *Union:
		b.WriteString("Union{")
		first := true
		var walk func(any)
		walk = func(u any) {
			if uu, ok := u.(*Union); ok {
				walk(uu.A)
				walk(uu.B)
				return
			}
			if !first {
				b.WriteString(", ")
			}
			first = false
			show(b, u)
		}
		walk(x)
		b.WriteByte('}')
	case *UnionAll:
		b.WriteByte('(')
		show(b, x.Body)
		b.WriteString(" where {")
		hasLB, hasUB := x.Var.LB != any(Bottom), x.Var.UB != any(AnyType)
		if hasLB && hasUB {
			show(b, x.Var.LB)
			b.WriteString("<:")
		}
		b.WriteString(x.Var.Name)
		if hasUB {
			b.WriteString("<:")
			show(b, x.Var.UB)
		} else if hasLB {
			// julia reads `where {L<:T}` with no upper bound as an error
			b.WriteString(">:")
			show(b, x.Var.LB)
		}
		b.WriteString("})")
	case *TypeVar:
		b.WriteString(x.Name)
	case *Vararg:
		b.WriteString("Vararg")
		if x.T != nil {
			b.WriteByte('{')
			show(b, x.T)
			if x.N != nil {
				b.WriteString(", ")
				show(b, x.N)
			}
			b.WriteByte('}')
		}
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case Symbol:
		b.WriteString(":(")
		b.WriteString(string(x))
		b.WriteByte(')')
	case *Instance:
		b.WriteString(x.Bits)
	case ModuleValue:
		b.WriteString(x.ModuleName())
	default:
		b.WriteString("#unknown")
	}
}

// String methods for debugging and error messages.

func (d *DataType) String() string { return Show(d) }
func (u *Union) String() string    { return Show(u) }
func (u *UnionAll) String() string { return Show(u) }
func (t *TypeVar) String() string  { return Show(t) }
func (v *Vararg) String() string   { return Show(v) }
