package interp

import (
	"fmt"
	"math"
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

// showFloat64 is julia's shortest round-trip form of a Float64: the digits
// Go's 'g' with precision -1 chooses, at the same exponent thresholds (below
// -4 or from 6 on), spelled as julia spells them: 1.0e6, 2.0, 1.0e-5.
func showFloat64(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "Inf"
	case math.IsInf(x, -1):
		return "-Inf"
	}
	s := strconv.FormatFloat(x, 'g', -1, 64)
	mant, exp, hasExp := strings.Cut(s, "e")
	if !strings.Contains(mant, ".") {
		mant += ".0"
	}
	if !hasExp {
		return mant
	}
	sign := ""
	if exp[0] == '-' {
		sign = "-"
	}
	return mant + "e" + sign + strings.TrimLeft(exp[1:], "0")
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
	case float64:
		b.WriteString(showFloat64(x))
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
	case *core.UnionAll:
		// julia shows a type's wrapper by its name: Type, Array
		if d, ok := core.UnwrapUnionAll(x).(*core.DataType); ok && d.Name.Wrapper == any(x) {
			b.WriteString(d.Name.Name)
			return
		}
		b.WriteString(core.Show(x))
	default:
		if core.IsType(v) || core.TypeOf(v) != nil {
			b.WriteString(core.Show(v))
			return
		}
		fmt.Fprintf(b, "%v", v)
	}
}
