package interp

import (
	"fmt"
	"math"
	"strings"

	"github.com/go-embedded-julia/julia/core"
)

// The vector llvmcalls of Random's XoshiroSimd.jl (julia 1.12.6,
// stdlib/Random/src/XoshiroSimd.jl:17-150), which rand! of an array of at
// least 64 bytes runs. julia's generator forks eight lanes of xoshiro256++
// from the parent and steps them together; each step is a handful of vector
// operations on NTuple{N,VecElement{UInt64}} that the file writes as LLVM IR.
// The stream those lanes make is part of julia's output (the lane count and
// the threshold "influence random stream", lines 11-15), so the operations
// are performed here and not replaced by the scalar generator, whose numbers
// differ.
//
// Like the atomics of llvmcall.go the IR is recognized by its text, not
// compiled: llvmVecTemplates holds each template the file generates (for
// N = 4, 8 and 16, the widths its loop covers), spelled as julia spells it,
// with the lane function it computes. IR that matches none still requires the
// compiler. The two ccalls of the file's loop, the funnel shifts
// `llvm.fshl.vNi64`, are shims (vm.bootShims, llvmFshl).

// llvmVecTemplate is one IR template: the lanes of its vector operand, how
// the operands combine, and the function on one 64-bit lane.
type llvmVecTemplate struct {
	lanes int
	shift bool                     // the second operand is an Int64, one shift count for every lane
	un    func(x uint64) uint64    // one vector operand
	bin   func(x, y uint64) uint64 // two vector operands
	by    func(x, k uint64) uint64 // a vector and a shift count
}

// bits2float64 is `uitofp (x >> 11)` scaled by 2^-53 (the template's
// 0x3ca0000000000000), as the bits of the Float64.
func bits2float64(x uint64) uint64 { return math.Float64bits(float64(x>>11) * 0x1p-53) }

// bits2float32 is the same on each 32-bit half of the lane, `lshr 8` and 2^-24
// (0x3e70000000000000), the halves in memory order: the first (low) half is
// the low word.
func bits2float32(x uint64) uint64 {
	f := func(u uint32) uint64 { return uint64(math.Float32bits(float32(u>>8) * 0x1p-24)) }
	return f(uint32(x)) | f(uint32(x>>32))<<32
}

// bits2float16 is `lshr 5` and 2^-11 (0x3f40000000000000) on each of the four
// 16-bit parts: at most 2047 * 2^-11, exact in a Float16.
func bits2float16(x uint64) uint64 {
	var r uint64
	for i := uint(0); i < 4; i++ {
		r |= uint64(doubleToHalf(float64(uint16(x>>(16*i))>>5)*0x1p-11)) << (16 * i)
	}
	return r
}

var llvmVecTemplates = func() map[string]llvmVecTemplate {
	m := map[string]llvmVecTemplate{}
	for _, n := range []int{4, 8, 16} {
		vt := fmt.Sprintf("<%d x i64>", n)
		binary := func(op string, f func(x, y uint64) uint64) {
			m[fmt.Sprintf("%%res = %s %s %%1, %%0\nret %s %%res", op, vt, vt)] = llvmVecTemplate{lanes: n, bin: f}
		}
		binary("add", func(x, y uint64) uint64 { return x + y })
		binary("xor", func(x, y uint64) uint64 { return x ^ y })
		binary("and", func(x, y uint64) uint64 { return x & y })
		binary("or", func(x, y uint64) uint64 { return x | y })
		m[fmt.Sprintf("%%lshiftOp = shufflevector <1 x i64> <i64 17>, <1 x i64> undef, <%d x i32> zeroinitializer\n%%res = shl %s %%0, %%lshiftOp\nret %s %%res", n, vt, vt)] =
			llvmVecTemplate{lanes: n, un: func(x uint64) uint64 { return x << 17 }}
		m[fmt.Sprintf("%%tmp = insertelement <1 x i64> undef, i64 %%1, i32 0\n%%shift = shufflevector <1 x i64> %%tmp, <1 x i64> %%tmp, <%d x i32> zeroinitializer\n%%res = lshr %s %%0, %%shift\nret %s %%res", n, vt, vt)] =
			llvmVecTemplate{lanes: n, shift: true, by: func(x, k uint64) uint64 { return x >> k }}
		m[fmt.Sprintf("%%shiftamt = shufflevector <1 x i64> <i64 11>, <1 x i64> undef, <%d x i32> zeroinitializer\n%%sh = lshr %s %%0, %%shiftamt\n%%f = uitofp %s %%sh to <%d x double>\n%%scale = shufflevector <1 x double> <double 0x3ca0000000000000>, <1 x double> undef, <%d x i32> zeroinitializer\n%%m = fmul <%d x double> %%f, %%scale\n%%i = bitcast <%d x double> %%m to %s\nret %s %%i", n, vt, vt, n, n, n, n, vt, vt)] =
			llvmVecTemplate{lanes: n, un: bits2float64}
		m[fmt.Sprintf("%%as32 = bitcast %s %%0 to <%d x i32>\n%%shiftamt = shufflevector <1 x i32> <i32 8>, <1 x i32> undef, <%d x i32> zeroinitializer\n%%sh = lshr <%d x i32> %%as32, %%shiftamt\n%%f = uitofp <%d x i32> %%sh to <%d x float>\n%%scale = shufflevector <1 x float> <float 0x3e70000000000000>, <1 x float> undef, <%d x i32> zeroinitializer\n%%m = fmul <%d x float> %%f, %%scale\n%%i = bitcast <%d x float> %%m to %s\nret %s %%i", vt, 2*n, 2*n, 2*n, 2*n, 2*n, 2*n, 2*n, 2*n, vt, vt)] =
			llvmVecTemplate{lanes: n, un: bits2float32}
		m[fmt.Sprintf("%%as16 = bitcast %s %%0 to <%d x i16>\n%%shiftamt = shufflevector <1 x i16> <i16 5>, <1 x i16> undef, <%d x i32> zeroinitializer\n%%sh = lshr <%d x i16> %%as16, %%shiftamt\n%%f = uitofp <%d x i16> %%sh to <%d x half>\n%%scale = shufflevector <1 x half> <half 0x3f40000000000000>, <1 x half> undef, <%d x i32> zeroinitializer\n%%m = fmul <%d x half> %%f, %%scale\n%%i = bitcast <%d x half> %%m to %s\nret %s %%i", vt, 4*n, 4*n, 4*n, 4*n, 4*n, 4*n, 4*n, 4*n, vt, vt)] =
			llvmVecTemplate{lanes: n, un: bits2float16}
	}
	return m
}()

// the scalar template of XoshiroSimd.jl:25-31, _lshr(x::UInt64, y::Int64)
const llvmScalarLshr = "%res = lshr i64 %0, %1\nret i64 %res"

// vecLanes is the UInt64 lanes of an NTuple{n,VecElement{UInt64}} value.
func vecLanes(v any, n int) ([]uint64, bool) {
	t, ok := v.(*Tuple)
	if !ok || len(t.Elems) != n {
		return nil, false
	}
	lanes := make([]uint64, n)
	for i, e := range t.Elems {
		s, ok := e.(*Struct)
		if !ok || len(s.Fields) != 1 {
			return nil, false
		}
		if lanes[i], ok = s.Fields[0].(uint64); !ok {
			return nil, false
		}
	}
	return lanes, true
}

// vecValue is lanes as a value of the tuple type ret names, the llvmcall's
// return type; its elements are VecElement{UInt64}.
func vecValue(ret any, lanes []uint64) (any, bool) {
	rt, ok := ret.(*core.DataType)
	if !ok || len(rt.Parameters) != len(lanes) {
		return nil, false
	}
	elems := make([]any, len(lanes))
	for i, x := range lanes {
		et, ok := rt.Parameters[i].(*core.DataType)
		if !ok || et.Name.Name != "VecElement" {
			return nil, false
		}
		elems[i] = &Struct{Type: et, Fields: []any{x}}
	}
	return &Tuple{Type: rt, Elems: elems}, true
}

// llvmVector performs the vector (or Int64 lshr) template ir is, on
// args = (ir, rettype, argtypes, operands...), and says whether it did: ir is
// not one of them, or its operands are not what the template takes, is not
// this function's to answer.
func llvmVector(ir string, args []any) (any, bool) {
	ir = strings.TrimSpace(ir)
	if ir == llvmScalarLshr {
		x, ok1 := argAt(args, 3).(uint64)
		k, ok2 := argAt(args, 4).(int64)
		if len(args) != 5 || !ok1 || !ok2 || args[1] != any(core.UInt64Type) {
			return nil, false
		}
		return x >> uint64(k), true
	}
	tp, ok := llvmVecTemplates[ir]
	if !ok {
		return nil, false
	}
	x, ok := vecLanes(argAt(args, 3), tp.lanes)
	if !ok {
		return nil, false
	}
	switch {
	case tp.un != nil && len(args) == 4:
		for i := range x {
			x[i] = tp.un(x[i])
		}
	case tp.bin != nil && len(args) == 5:
		y, ok := vecLanes(args[4], tp.lanes)
		if !ok {
			return nil, false
		}
		for i := range x {
			x[i] = tp.bin(x[i], y[i])
		}
	case tp.shift && len(args) == 5:
		k, ok := args[4].(int64)
		if !ok {
			return nil, false
		}
		for i := range x {
			x[i] = tp.by(x[i], uint64(k))
		}
	default:
		return nil, false
	}
	return vecValue(args[1], x)
}

// argAt is args[i], nil past the end.
func argAt(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return nil
}

// llvmFshl is `llvm.fshl.vNi64`, which XoshiroSimd.jl calls as
// ccall("llvm.fshl.v8i64", llvmcall, VT, (VT, VT, VT), x, x, s) for its
// rotations: per lane, the high 64 bits of (x:y) << (s mod 64).
func llvmFshl(n int) shim {
	return func(vm *VM, a []any) (any, error) {
		x, ok1 := vecLanes(argAt(a, 0), n)
		y, ok2 := vecLanes(argAt(a, 1), n)
		s, ok3 := vecLanes(argAt(a, 2), n)
		if len(a) != 3 || !ok1 || !ok2 || !ok3 {
			return nil, fmt.Errorf("%w: llvm.fshl.v%di64 of %s", ErrUnsupported, n, showValue(vm.newTuple(a)))
		}
		for i := range x {
			k := s[i] % 64
			x[i] = x[i]<<k | y[i]>>(64-k) // Go shifts by 64 to 0
		}
		v, ok := vecValue(vm.typeOf(a[0]), x)
		if !ok {
			return nil, fmt.Errorf("%w: llvm.fshl.v%di64 of %s", ErrUnsupported, n, showValue(a[0]))
		}
		return v, nil
	}
}
