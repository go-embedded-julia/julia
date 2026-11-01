package interp

import (
	"fmt"
	"strings"

	"github.com/go-embedded-julia/julia/core"
)

// julia's show of arrays (base/arrayshow.jl), which Base computes and this
// package reproduces for repr until Base runs: the element-type prefix that
// typeinfo_prefix decides (arrayshow.jl:556-599), elements shown with that
// type as their context, #undef for an unassigned slot, the ;-separated
// rows and ;;;-separated slices of show_nd, the T(undef, dims) of an empty
// array (:477-480) and fill(x) of a zero-dimensional one (:498-507).

// arrayOf returns an Array's element type, its dimensions and its elements
// in column-major order, false for any other value.
func arrayOf(v any) (any, []int64, []any, bool) {
	s, ok := v.(*Struct)
	if !ok || s.Type.Name != core.ArrayType.Body.(*core.UnionAll).Body.(*core.DataType).Name || len(s.Fields) != 2 {
		return nil, nil, nil, false
	}
	ref, ok := s.Fields[0].(*MemoryRef)
	size, ok2 := s.Fields[1].(*Tuple)
	if !ok || !ok2 {
		return nil, nil, nil, false
	}
	dims := make([]int64, len(size.Elems))
	n := int64(1)
	for i, d := range size.Elems {
		dims[i] = d.(int64)
		n *= dims[i]
	}
	end := ref.Off + int(n)
	if end > len(ref.Mem.Elems) {
		end = len(ref.Mem.Elems)
	}
	return s.Type.Parameters[0], dims, ref.Mem.Elems[ref.Off:end], true
}

// typeinfoImplicit is typeinfo_implicit (arrayshow.jl:556-565): the types
// whose values read back undecorated.
func typeinfoImplicit(t any) bool {
	switch t {
	case core.Float64Type, core.Int64Type, core.CharType, core.StringType, core.SymbolType:
		return true
	}
	d, ok := t.(*core.DataType)
	if !ok {
		return false
	}
	if d.Name.Singleton || d == core.NothingType {
		return true
	}
	if !d.IsConcrete {
		return false
	}
	if d.Name == core.ArrayType.Body.(*core.UnionAll).Body.(*core.DataType).Name {
		return typeinfoImplicit(d.Parameters[0])
	}
	if d.Name == core.TupleTypeName {
		for _, p := range d.Parameters {
			if !typeinfoImplicit(p) {
				return false
			}
		}
		return true
	}
	return false
}

// typeinfoPrefix is typeinfo_prefix (arrayshow.jl:568-599) for an array of
// element type eltype in a context that already knows ctx (nil: nothing):
// the prefix, and whether the elements need no context of their own.
func typeinfoPrefix(eltype, ctx any, empty bool) (string, bool) {
	switch {
	case ctx != nil && core.Egal(eltype, ctx):
		return "", false
	case !empty && typeinfoImplicit(eltype):
		return "", true
	}
	if name := showWrapperName(eltype); name != "" {
		return name, false
	}
	var b strings.Builder
	writeType(&b, eltype)
	return b.String(), false
}

// writeArray writes an array of element type eltype and dimensions dims
// whose elements are elems, as show(io, X::AbstractArray) does in a
// context whose typeinfo is ctx; typ is the array's type, shown for an
// empty one.
func writeArray(b *strings.Builder, typ, eltype any, dims []int64, elems []any, ctx any) {
	if len(dims) == 0 {
		// show_zero_dim
		if len(elems) == 1 && elems[0] != undef {
			b.WriteString("fill(")
			writeIn(b, elems[0], nil)
			b.WriteByte(')')
			return
		}
		b.WriteString("Array{")
		writeType(b, eltype)
		b.WriteString(", 0}(UndefInitializer())")
		return
	}
	prefix, implicit := typeinfoPrefix(eltype, ctx, len(elems) == 0)
	var elctx any
	if !implicit {
		elctx = eltype
	}
	if len(dims) == 1 {
		// show_vector
		b.WriteString(prefix + "[")
		for i, e := range elems {
			if i > 0 {
				b.WriteString(", ")
			}
			writeElem(b, e, elctx)
		}
		b.WriteByte(']')
		return
	}
	if len(elems) == 0 {
		// _show_empty
		writeType(b, typ)
		b.WriteString("(undef")
		for _, d := range dims {
			fmt.Fprintf(b, ", %d", d)
		}
		b.WriteByte(')')
		return
	}
	b.WriteString(prefix + "[")
	writeSlices(b, dims, elems, elctx)
	b.WriteByte(']')
}

// writeSlices writes the contents of an array of two or more dimensions:
// a matrix's rows separated by "; " and its columns by " ", each slice of a
// higher dimension k separated by k semicolons (show_nd).
func writeSlices(b *strings.Builder, dims []int64, elems []any, ctx any) {
	k := len(dims)
	if k == 2 {
		rows, cols := dims[0], dims[1]
		for i := int64(0); i < rows; i++ {
			if i > 0 {
				b.WriteString("; ")
			}
			for j := int64(0); j < cols; j++ {
				if j > 0 {
					b.WriteByte(' ')
				}
				writeElem(b, elems[j*rows+i], ctx)
			}
		}
		return
	}
	stride := len(elems) / int(dims[k-1])
	for s := int64(0); s < dims[k-1]; s++ {
		if s > 0 {
			b.WriteString(strings.Repeat(";", k) + " ")
		}
		writeSlices(b, dims[:k-1], elems[int(s)*stride:int(s+1)*stride], ctx)
	}
}

func writeElem(b *strings.Builder, e any, ctx any) {
	if e == undef {
		b.WriteString("#undef")
		return
	}
	writeIn(b, e, ctx)
}

// writeIn is show(IOContext(io, :typeinfo => ctx), v): a value whose type
// the context gives drops what would repeat it (1.0 for a Float32, 1 for a
// Bool); a nested array uses the context for its prefix.
func writeIn(b *strings.Builder, v any, ctx any) {
	switch x := v.(type) {
	case float32:
		if ctx == any(core.Float32Type) {
			// without the f0 suffix and the specials' 32; an exponent other
			// than 0 stays (Float32[1.0f32, NaN, Inf], checked against julia)
			s := showFloat(float64(x), 32)
			switch s {
			case "NaN32", "Inf32", "-Inf32":
				s = strings.TrimSuffix(s, "32")
			default:
				s = strings.TrimSuffix(s, "f0")
			}
			b.WriteString(s)
			return
		}
	case bool:
		if ctx == any(core.BoolType) {
			if x {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
			return
		}
	case *Struct:
		if eltype, dims, elems, ok := arrayOf(x); ok {
			var inner any
			if d, ok := ctx.(*core.DataType); ok && d.Name == x.Type.Name {
				inner = d.Parameters[0]
			}
			writeArray(b, x.Type, eltype, dims, elems, inner)
			return
		}
	case *Memory:
		var inner any
		if d, ok := ctx.(*core.DataType); ok && d.Name == x.Type.Name {
			inner = d.Parameters[1]
		}
		writeArray(b, x.Type, x.Type.Parameters[1], []int64{int64(len(x.Elems))}, x.Elems, inner)
		return
	}
	writeValue(b, v)
}
