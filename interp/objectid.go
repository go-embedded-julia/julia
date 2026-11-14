package interp

import (
	"math"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// objectID is jl_object_id (builtins.c:412-509) in what it guarantees:
// values egal to each other hash alike, a mutable value hashes by its
// identity, an immutable one by its type and contents. It follows egal
// case by case. julia's own numbers are not reproduced.
//
// No Go address goes into an id, as none does in julia's own for an object
// of the system image (staticdata.c:1502-1514 writes each mutable object's id
// into the image, builtins.c:481-483 returns it). A mutable value (a Struct,
// Memory, Function, Module, Expr, Builtin, Intrinsic) carries an id that the
// VM assigns on the first objectid from its own counter, so that the same
// program gives the same ids in every run and an image can keep them. A type
// is hashed by its name and parameters (core.DataType.Hash, which julia's
// jl_object_id also reads), the same in every process.
func (vm *VM) objectID(v any) uint64 {
	switch x := v.(type) {
	case *Tuple:
		h := typeID(x.Type)
		for _, e := range x.Elems {
			h = bitmix(h, vm.objectID(e))
		}
		return h
	case *Struct:
		if x.Type.Name.Mutable {
			return vm.assignID(&x.id)
		}
		h := typeID(x.Type)
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
		return bitmix(vm.assignID(&x.Mem.id), uint64(x.Off))
	case *Pointer:
		// egal pointers have one type and one offset
		return bitmix(typeID(x.Type), uint64(x.Off))
	case PrimValue:
		return bitmix(bitmix(typeID(x.Type), x.Bits.lo), x.Bits.hi)
	case *Function:
		return vm.assignID(&x.id)
	case *Builtin:
		return vm.assignID(&x.id)
	case *Intrinsic:
		return vm.assignID(&x.id)
	case *Module:
		return vm.assignID(&x.id)
	case *Memory:
		return vm.assignID(&x.id)
	case *expr.Expr:
		return vm.assignID(&x.ObjID)
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
		return int64hash(uint64(x.Name.Hash()))
	}
	// anything else egal to v is of v's type, a DataType as every value's
	return ^int64hash(uint64(vm.typeOf(v).(*core.DataType).Name.Hash()))
}

// assignID is the id kept in *p, given from the VM's counter the first time
// it is asked: the counter mixed by int64hash (a bijection, so ids are
// distinct) and never 0, which means "not assigned".
func (vm *VM) assignID(p *uint64) uint64 {
	for *p == 0 {
		vm.idCtr++
		*p = int64hash(vm.idCtr)
	}
	return *p
}

// typeID hashes a type for the values of that type: by its name and
// parameters, or by its name when the parameters do not hash.
func typeID(t *core.DataType) uint64 {
	if h := t.Hash(); h != 0 {
		return int64hash(uint64(h))
	}
	return int64hash(uint64(t.Name.Hash()))
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

// strHash64 is FNV-1a over s's bytes.
func strHash64(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}
