package interp

import (
	"math"
	"unsafe"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// objectID is jl_object_id (builtins.c:412-509) in what it guarantees:
// values egal to each other hash alike, a mutable value hashes by its
// identity, an immutable one by its type and contents. It follows egal
// case by case. julia's own numbers are not reproduced: they mix in each
// type's hash, which comes from its module's build_id (jl_new_typename_in),
// fixed when the system image is built; and a mutable value's is its
// address, in julia as here.
func (vm *VM) objectID(v any) uint64 {
	switch x := v.(type) {
	case *Tuple:
		h := addrHash(unsafe.Pointer(x.Type))
		for _, e := range x.Elems {
			h = bitmix(h, vm.objectID(e))
		}
		return h
	case *Struct:
		if x.Type.Name.Mutable {
			return addrHash(unsafe.Pointer(x))
		}
		h := addrHash(unsafe.Pointer(x.Type))
		for _, f := range x.Fields {
			if f != undef {
				h = bitmix(h, vm.objectID(f))
			} else {
				h = bitmix(h, 0)
			}
		}
		return h
	case SVec:
		// hash_svec (builtins.c:335-345)
		var h uint64
		for _, e := range x {
			h = bitmix(h, vm.objectID(e))
		}
		return h
	case *MemoryRef:
		return bitmix(addrHash(unsafe.Pointer(x.Mem)), uint64(x.Off))
	case *Pointer:
		// egal pointers have one type and one offset
		return bitmix(addrHash(unsafe.Pointer(x.Type)), uint64(x.Off))
	case *Function:
		return addrHash(unsafe.Pointer(x))
	case *Builtin:
		return addrHash(unsafe.Pointer(x))
	case *Intrinsic:
		return addrHash(unsafe.Pointer(x))
	case *Module:
		return addrHash(unsafe.Pointer(x))
	case *Memory:
		return addrHash(unsafe.Pointer(x))
	case *expr.Expr:
		return addrHash(unsafe.Pointer(x))
	case string:
		return strHash64(x) ^ 0xedc3b677
	case expr.Symbol:
		return strHash64(string(x))
	case int64:
		return int64hash(uint64(x))
	case float64:
		// egal compares the bits: -0.0 !== 0.0, a NaN is itself
		return int64hash(math.Float64bits(x)) ^ 0x3ff
	case bool:
		if x {
			return 0x9e3779b97f4a7c15
		}
		return 0x7f4a7c159e3779b9
	case nil:
		return 0x2545f4914f6cdd1d
	case *core.DataType:
		// egal DataTypes share their TypeName
		return addrHash(unsafe.Pointer(x.Name))
	}
	// anything else egal to v is of v's type, a DataType as every value's
	return ^addrHash(unsafe.Pointer(vm.typeOf(v).(*core.DataType).Name))
}

// int64hash is julia's (support/hashing.c:28-38).
func int64hash(key uint64) uint64 {
	key = ^key + key<<21
	key ^= key >> 24
	key = key + key<<3 + key<<8
	key ^= key >> 14
	key = key + key<<2 + key<<4
	key ^= key >> 28
	key += key << 31
	return key
}

// bitmix is julia's on 64-bit platforms (support/hashing.h:29-32).
func bitmix(a, b uint64) uint64 {
	return int64hash(a ^ (b>>56 | b>>40&0xff00 | b>>24&0xff0000 | b>>8&0xff000000 |
		b<<8&0xff00000000 | b<<24&0xff0000000000 | b<<40&0xff000000000000 | b<<56))
}

func addrHash(p unsafe.Pointer) uint64 { return int64hash(uint64(uintptr(p))) }

// strHash64 is FNV-1a over s's bytes.
func strHash64(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}
