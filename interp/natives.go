package interp

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/internal/utf8proc"
)

// Native fast paths for Base methods whose julia body is a loop the compiler
// removes and this interpreter runs (#71).
//
// julia compiles `for i in newlen+1:len; @inbounds _unsetindex!(a, i); end`
// over a Vector{UInt8} to nothing: the elements hold no pointer, there is
// nothing to clear. Interpreted, each element costs about 200 julia calls (the
// bounds check, memoryref, reinterpret and isprimitivetype behind it), so that
// `normalize` of a string spent 600 calls per character in the `resize!` that
// shrinks its buffer.
//
// A native stands for ONE method of the image, found by its definition (file,
// line) and by a hash of its lowered body, so that an image built from another
// julia cannot get the wrong one. It answers the cases it covers and says it
// has not handled anything else (handled false), in which case the julia body
// runs as before: an error, an element type that needs clearing, an argument
// of another type.
type nativeFn func(vm *VM, args []any) (res any, handled bool, err error)

type nativeSpec struct {
	file  string
	line  int32
	nargs int
	body  string // hash of the lowered body, hex, see bodyHash
	fn    nativeFn
}

var natives = []nativeSpec{
	// array.jl:1226, _deleteend!(a::Vector, delta::Integer)
	{"array.jl", 1226, 3, "a9f1629e8f277fcc13421605c749b0120750aff40b92acd87c7909f15f6c984c", nativeDeleteEnd},
	// essentials.jl:730, reinterpret(::Type{Out}, x) where {Out}
	// strings/unicode.jl:640 and :656, uppercase and lowercase of an AbstractString
	{"strings/unicode.jl", 640, 2, "7e769ec8fd83c9f793a73f617de43596264ec781119240d4581a8831a885189e", nativeCaseMap(utf8proc.ToUpper)},
	{"strings/unicode.jl", 656, 2, "23e02162ddd4280996a49e77a7a628ff91e0f144e92d1c98e100cea35a177a04", nativeCaseMap(utf8proc.ToLower)},
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
// or nil for a lambda no native stands for. It is not a field of lambda: the
// shape of the types an image holds is part of its key, and an image that
// has no native in it must stay valid.
var nativeCache sync.Map // *lambda -> nativeFn (nil for none)

// nativeOf is the native that stands for l, or nil.
func nativeOf(l *lambda) nativeFn {
	if v, ok := nativeCache.Load(l); ok {
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
	nativeCache.Store(l, found)
	return found
}

// nativeReinterpret is reinterpret(::Type{Out}, x) of essentials.jl:730 for a
// primitive Out and a primitive x, which the body answers with
// bitcast(Out, x): every shift, `unsigned` and `signed` of Base goes through
// it, and its body (isprimitivetype of each, unwrap_unionall, ...) is about
// ten julia calls. Anything else, or a bitcast that refuses, is the body's.
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

// nativeCaseMap is uppercase(s::AbstractString) and lowercase(s) of
// strings/unicode.jl:640 and :656 for a String: their body is `map(f, s)`, a
// julia loop over the characters (about 600 calls each, which this interpreter
// runs and julia compiles). The same mapping, utf8proc's, over the code
// points of a valid UTF-8 string: what `map` does for each character
// (uppercase(c) is utf8proc_toupper outside ASCII, and '+- 0x20' within it,
// which the table has too). A string that is not valid UTF-8, whose bytes
// julia iterates as malformed characters, is the body's.
func nativeCaseMap(m func(int32) int32) nativeFn {
	return func(vm *VM, args []any) (any, bool, error) {
		if len(args) != 1 {
			return nil, false, nil
		}
		s, ok := args[0].(string)
		if !ok || !utf8.ValidString(s) {
			return nil, false, nil
		}
		ascii := true
		for i := 0; i < len(s); i++ {
			if s[i] >= utf8.RuneSelf {
				ascii = false
				break
			}
		}
		var b strings.Builder
		b.Grow(len(s) + len(s)/8)
		if ascii {
			for i := 0; i < len(s); i++ {
				b.WriteByte(byte(m(int32(s[i]))))
			}
		} else {
			for _, r := range s {
				b.WriteRune(rune(m(int32(r))))
			}
		}
		return b.String(), true, nil
	}
}

// nativeDeleteEnd is _deleteend!(a::Vector, delta::Integer) for an element
// type that holds no pointer: the size drops by delta and nothing is cleared.
func nativeDeleteEnd(vm *VM, args []any) (any, bool, error) {
	a, ok := args[0].(*Struct)
	if !ok || len(args) != 2 || len(a.Fields) < 2 || a.Type.Name.Name != "Array" || a.Type.Name.Module != "Core" {
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
