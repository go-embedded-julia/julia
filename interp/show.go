package interp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// showValue is what julia's repr prints for the values this version makes,
// so that a result can be compared with the reference's: the default show
// of a struct (TypeName(field, …)), of a tuple, and the literals. Base's
// show is Julia code that does not run here yet; this is its output for
// these values, not a port of it.
func showValue(v any) string {
	var b strings.Builder
	writeValue(&b, v)
	return b.String()
}

// Repr is showValue, exported for the notebook and the tests.
func Repr(v any) string { return showValue(v) }

func writeValue(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("nothing")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case int8, int16, int32:
		fmt.Fprintf(b, "%d", x)
	case uint8:
		fmt.Fprintf(b, "0x%02x", x)
	case uint16:
		fmt.Fprintf(b, "0x%04x", x)
	case uint32:
		fmt.Fprintf(b, "0x%08x", x)
	case uint64:
		fmt.Fprintf(b, "0x%016x", x)
	case string:
		b.WriteString(strconv.Quote(x))
	case expr.Symbol:
		b.WriteString(":" + string(x))
	case *Tuple:
		b.WriteByte('(')
		for i, e := range x.Elems {
			if i > 0 {
				b.WriteString(", ")
			}
			writeValue(b, e)
		}
		if len(x.Elems) == 1 {
			b.WriteByte(',')
		}
		b.WriteByte(')')
	case *Struct:
		b.WriteString(x.Type.Name.Name)
		b.WriteByte('(')
		for i, f := range x.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			if f == undef {
				b.WriteString("#undef")
				continue
			}
			writeValue(b, f)
		}
		b.WriteByte(')')
	case *Function:
		b.WriteString(string(x.Name))
	case *Builtin:
		b.WriteString(string(x.Name))
	case *Intrinsic:
		b.WriteString("Core.Intrinsics." + string(x.Name))
	case *Module:
		if x.Parent != nil && x.Parent.Name != "Core" {
			b.WriteString(string(x.Parent.Name) + ".")
		} else if x.Parent != nil {
			b.WriteString("Core.")
		}
		b.WriteString(string(x.Name))
	case SVec:
		b.WriteString("svec(")
		for i, e := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			writeValue(b, e)
		}
		b.WriteByte(')')
	default:
		if core.IsType(v) || core.TypeOf(v) != nil {
			b.WriteString(core.Show(v))
			return
		}
		fmt.Fprintf(b, "%v", v)
	}
}
