package interp

import (
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"weak"

	"github.com/go-embedded-julia/julia/core"
)

// Native stand-ins for two Base methods whose julia body is what julia itself
// does not run (#71).
//
//   - _deleteend!(a::Vector, delta) (array.jl:1226-1236) shrinks a vector by
//     calling `@inbounds _unsetindex!(a, i)` for each element it drops.
//     _unsetindex!(::MemoryRef{T}) (genericmemory.jl:78-101) returns at once
//     when the element type is isbits and pointer-free
//     (`arrayelem == isbits && datatype_pointerfree(T) && return A`, line 88),
//     so julia's compiler, which inlines it, removes the loop: shrinking a
//     Vector{UInt8} costs a store of the new size. Interpreted, each dropped
//     element cost about 200 julia calls (the bounds check, memoryref, reinterpret
//     behind them): `normalize` of a string spent 600 calls per character in the
//     `resize!` that shrinks its buffer.
//   - reinterpret(::Type{Out}, x) (essentials.jl:730-737) is
//     `bitcast(Out, x)` when Out and typeof(x) are primitive types; julia emits
//     an LLVM bitcast for it (the bitcast intrinsic, runtime_intrinsics.c
//     jl_bitcast, codegen emit_bitcast). Every shift, `unsigned` and `signed` of
//     Base calls it; interpreted, its body (isprimitivetype of each,
//     unwrap_unionall, `==`) is about ten julia calls.
//
// A native stands for ONE method of the image, found by its definition (file,
// line, number of arguments) and by a hash of its lowered body, so that an image
// built from another julia gets none of them. It answers the cases it covers and
// says it has not handled anything else (handled false), in which case the julia
// body runs as before: an error to throw, an element type that needs clearing, an
// argument of another type. The args a native is given are BORROWED from the
// caller's frame (callFunction): it may read them and must not keep the slice.
type nativeFn func(vm *VM, args []any) (res any, handled bool, err error)

type nativeSpec struct {
	file  string
	line  int32
	nargs int    // with the function itself
	body  string // hash of the lowered body, hex, see bodyHash
	fn    nativeFn
}

var natives = []nativeSpec{
	// array.jl:1226, _deleteend!(a::Vector, delta::Integer)
	{"array.jl", 1226, 3, "a9f1629e8f277fcc13421605c749b0120750aff40b92acd87c7909f15f6c984c", nativeDeleteEnd},
	// essentials.jl:730, reinterpret(::Type{Out}, x) where {Out}
	{"essentials.jl", 730, 3, "a1fbc11c0bf724582fff987ac554d94d596d061e8c2e311ccdcbb283f78987ea", nativeReinterpret},
}

// bodyHash is the hash of a lambda's lowered statements.
func bodyHash(l *lambda) string {
	h := sha256.New()
	if l.c != nil && l.c.src != nil {
		for _, st := range l.c.src.Code {
			h.Write([]byte(showValue(st)))
			h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// nativeCache is what nativeOf found for each lambda asked about: a nativeFn,
// or nil for a lambda no native stands for. The key is a weak pointer, removed
// when the lambda is collected, so that the cache keeps no kernel alive; and it
// is not a field of lambda, because the shape of the types an image holds is
// part of its key.
var nativeCache sync.Map // weak.Pointer[lambda] -> nativeFn (nil for none)

// nativesDisabled turns every native off, so that a test can run the julia
// bodies against them.
var nativesDisabled atomic.Bool

// nativeOf is the native that stands for l, or nil.
func nativeOf(l *lambda) nativeFn {
	if nativesDisabled.Load() {
		return nil
	}
	// Every call of the interpreter asks: the lambdas at a line no native has
	// (nearly all of them) are answered by a comparison, before any map and any
	// weak pointer. (Measured: weak.Make and a sync.Map lookup per call took
	// 40 % of a plain loop.)
	candidate := false
	for i := range natives {
		if natives[i].line == l.line {
			candidate = true
			break
		}
	}
	if !candidate {
		return nil
	}
	key := weak.Make(l)
	if v, ok := nativeCache.Load(key); ok {
		fn, _ := v.(nativeFn)
		return fn
	}
	var found nativeFn
	for _, s := range natives {
		if s.line != l.line || s.nargs != l.nargs || !strings.HasSuffix(string(l.file), s.file) {
			continue
		}
		if bodyHash(l) == s.body {
			found = s.fn
			break
		}
	}
	nativeCache.Store(key, found)
	runtime.AddCleanup(l, forgetNative, key)
	return found
}

// forgetNative drops the cache entry of a lambda that has been collected.
func forgetNative(k weak.Pointer[lambda]) { nativeCache.Delete(k) }

// nativeReinterpret is reinterpret(::Type{Out}, x) for a primitive Out and a
// primitive x: bitcast(Out, x). Anything else, or a bitcast that refuses (a size
// that does not match), is the body's.
func nativeReinterpret(vm *VM, args []any) (any, bool, error) {
	if len(args) != 2 || !isPrimitive(args[1]) {
		return nil, false, nil
	}
	if _, ok := args[0].(*core.DataType); !ok {
		return nil, false, nil
	}
	v, err := biBitcast(vm, args)
	if err != nil {
		return nil, false, nil
	}
	return v, true, nil
}

// nativeDeleteEnd is _deleteend!(a::Vector, delta::Integer) for an element
// type that holds no pointer: the size drops by delta and nothing is cleared.
func nativeDeleteEnd(vm *VM, args []any) (any, bool, error) {
	if len(args) != 2 {
		return nil, false, nil
	}
	a, ok := args[0].(*Struct)
	if !ok || len(a.Fields) < 2 || a.Type.Name.Name != "Array" || a.Type.Name.Module != "Core" {
		return nil, false, nil
	}
	delta, ok := args[1].(int64)
	size, ok2 := a.Fields[1].(*Tuple)
	elem, ok3 := a.Type.Parameters[0].(*core.DataType)
	if !ok || !ok2 || !ok3 || len(size.Elems) != 1 || !elem.IsBits() {
		return nil, false, nil
	}
	n, ok := size.Elems[0].(int64)
	if !ok || delta < 0 || delta > n {
		return nil, false, nil // the julia body throws
	}
	a.Fields[1] = vm.newTuple([]any{n - delta})
	return nil, true, nil
}
