package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// IdSet's tables (idset.c over smallintset.c, julia 1.12.6): the keys in a
// Memory{Any} (NULL, here undef, where one was removed), and an
// open-addressed index into them, a Memory of UInt8, UInt16 or UInt32 as
// wide as the keys need, whose entries are index+1, 0 for empty and the
// type's maximum for a tombstone.

var (
	memUInt8Type  = must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.UInt8Type, core.CPU)).(*core.DataType)
	memUInt16Type = must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.UInt16Type, core.CPU)).(*core.DataType)
	memUInt32Type = must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.UInt32Type, core.CPU)).(*core.DataType)
)

// intWidth is the tombstone (the maximum) of an index memory, 0 for a
// Memory{Any} (jl_max_int, smallintset.c:74-87); false for another.
func intWidth(a *Memory) (uint64, bool) {
	switch a.Type {
	case memUInt8Type:
		return 0xFF, true
	case memUInt16Type:
		return 0xFFFF, true
	case memUInt32Type:
		return 0xFFFFFFFF, true
	case memAnyType:
		return 0, true
	}
	return 0, false
}

func intAt(a *Memory, i int) uint64 {
	switch x := a.Elems[i].(type) {
	case uint8:
		return uint64(x)
	case uint16:
		return uint64(x)
	case uint32:
		return uint64(x)
	}
	return 0
}

// intref is jl_intref (smallintset.c:31-42): a tombstone reads as empty.
func intref(a *Memory, i int) uint64 {
	tomb, _ := intWidth(a)
	if v := intAt(a, i); v != tomb {
		return v
	}
	return 0
}

// intrefAcquire is jl_intref_acquire (smallintset.c:48-59): a tombstone
// reads as -1.
func intrefAcquire(a *Memory, i int) int64 {
	tomb, _ := intWidth(a)
	if v := intAt(a, i); v != tomb {
		return int64(v)
	}
	return -1
}

// intsetRelease is jl_intset_release (smallintset.c:61-72); val -1 is the
// tombstone.
func intsetRelease(a *Memory, i int, val int64) {
	switch a.Type {
	case memUInt8Type:
		a.Elems[i] = uint8(val)
	case memUInt16Type:
		a.Elems[i] = uint16(val)
	case memUInt32Type:
		a.Elems[i] = uint32(val)
	}
}

// smallintsetEmpty is smallintset_empty (smallintset.c:89-104).
func smallintsetEmpty(a *Memory) {
	for i := range a.Elems {
		intsetRelease(a, i, 0)
	}
}

// allocInt1d is jl_alloc_int_1d (smallintset.c:106-119): len entries wide
// enough for np.
func (vm *VM) allocInt1d(np, n uint64) (*Memory, error) {
	if n > uint64(vm.MaxMemoryElems) {
		return nil, fmt.Errorf("%w: an IdSet index of %d entries (VM.MaxMemoryElems is %d)", ErrMemoryLimit, n, vm.MaxMemoryElems)
	}
	t := memUInt32Type
	switch {
	case np < 0xFF:
		t = memUInt8Type
	case np < 0xFFFF:
		t = memUInt16Type
	}
	a := &Memory{Type: t, Elems: make([]any, n)}
	smallintsetEmpty(a)
	return a, nil
}

// smallintsetLookup is jl_smallintset_lookup (smallintset.c:121-148): the
// index whose key eq accepts, -1 if none; pop leaves a tombstone.
func smallintsetLookup(cache *Memory, eq func(int) bool, hv uint64, pop bool) int64 {
	sz := len(cache.Elems)
	if sz == 0 {
		return -1
	}
	maxprobe := maxProbe(sz)
	index := int(hv & uint64(sz-1))
	orig := index
	for iter := 0; ; {
		val1 := intrefAcquire(cache, index)
		if val1 == 0 {
			return -1
		}
		if val1 != -1 && eq(int(val1-1)) {
			if pop {
				intsetRelease(cache, index, -1)
			}
			return val1 - 1
		}
		index = (index + 1) & (sz - 1)
		iter++
		if iter > maxprobe || index == orig {
			return -1
		}
	}
}

// smallintsetInsertAt is smallintset_insert_ (smallintset.c:150-169).
func smallintsetInsertAt(a *Memory, hv uint64, val1 uint64) bool {
	sz := len(a.Elems)
	if sz <= 1 {
		return false
	}
	maxprobe := maxProbe(sz)
	index := int(hv & uint64(sz-1))
	orig := index
	for iter := 0; ; {
		if intref(a, index) == 0 {
			intsetRelease(a, index, int64(val1))
			return true
		}
		index = (index + 1) & (sz - 1)
		iter++
		if iter > maxprobe || index == orig {
			return false
		}
	}
}

// smallintsetInsert is jl_smallintset_insert (smallintset.c:172-201): val
// in the index a, or in the larger one it is rehashed into.
func (vm *VM) smallintsetInsert(a *Memory, hash func(int) uint64, val uint64) (*Memory, error) {
	var err error
	if max, _ := intWidth(a); val+1 >= max {
		if a, err = vm.smallintsetRehash(a, hash, uint64(len(a.Elems)), val+1); err != nil {
			return nil, err
		}
	}
	for !smallintsetInsertAt(a, hash(int(val)), val+1) {
		// the table is full: grow it really fast
		sz := uint64(len(a.Elems))
		newsz := sz << 2
		switch {
		case sz < htNInline:
			newsz = htNInline
		case sz >= 1<<19 || sz <= 1<<8:
			newsz = sz << 1
		}
		if a, err = vm.smallintsetRehash(a, hash, newsz, 0); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// smallintsetRehash is smallintset_rehash (smallintset.c:203-228): a's
// entries in a new index of newsz entries, wide enough for np.
func (vm *VM) smallintsetRehash(a *Memory, hash func(int) uint64, newsz, np uint64) (*Memory, error) {
	for i := range a.Elems {
		np = max(np, intref(a, i))
	}
	for {
		newa, err := vm.allocInt1d(np+1, newsz)
		if err != nil {
			return nil, err
		}
		i := 0
		for ; i < len(a.Elems); i++ {
			if val1 := intref(a, i); val1 != 0 && !smallintsetInsertAt(newa, hash(int(val1-1)), val1) {
				break
			}
		}
		if i == len(a.Elems) {
			return newa, nil
		}
		newsz <<= 1
	}
}

// idsetArgs are an IdSet's keys and index, as its ccalls take them.
func (vm *VM) idsetArgs(fn string, k, i any) (*Memory, *Memory, error) {
	keys, ok1 := k.(*Memory)
	idxs, ok2 := i.(*Memory)
	if !ok1 || keys.Type != memAnyType {
		return nil, nil, fmt.Errorf("%w: %s of %s", ErrUnsupported, fn, showValue(k))
	}
	if _, ok := intWidth(idxs); !ok2 || !ok {
		return nil, nil, fmt.Errorf("%w: %s of %s", ErrUnsupported, fn, showValue(i))
	}
	return keys, idxs, nil
}

// idsetHash and idsetEq are idset_hash and idset_eq (idset.c:4-16).
func (vm *VM) idsetHash(keys *Memory) func(int) uint64 {
	return func(i int) uint64 {
		if keys.Elems[i] == undef {
			return 0
		}
		return vm.objectID(keys.Elems[i])
	}
}

func (vm *VM) idsetEq(keys *Memory, key any) func(int) bool {
	return func(i int) bool { return keys.Elems[i] != undef && vm.egal(keys.Elems[i], key) }
}

// bootIdSet registers idset.c's entry points (idset.c:18-118).
func (vm *VM) bootIdSet() {
	vm.shims["jl_idset_peek_bp"] = func(vm *VM, a []any) (any, error) {
		keys, idxs, err := vm.idsetArgs("jl_idset_peek_bp", a[0], a[1])
		if err != nil {
			return nil, err
		}
		return smallintsetLookup(idxs, vm.idsetEq(keys, a[2]), vm.objectID(a[2]), false), nil
	}
	vm.shims["jl_idset_pop"] = func(vm *VM, a []any) (any, error) {
		keys, idxs, err := vm.idsetArgs("jl_idset_pop", a[0], a[1])
		if err != nil {
			return nil, err
		}
		idx := smallintsetLookup(idxs, vm.idsetEq(keys, a[2]), vm.objectID(a[2]), true)
		if idx != -1 {
			keys.Elems[idx] = undef
		}
		return idx, nil
	}
	vm.shims["jl_idset_put_key"] = func(vm *VM, a []any) (any, error) {
		keys, ok := a[0].(*Memory)
		if !ok || keys.Type != memAnyType {
			return nil, fmt.Errorf("%w: jl_idset_put_key of %s", ErrUnsupported, showValue(a[0]))
		}
		keys, newidx, err := vm.idsetPutKey(keys, a[1])
		if err != nil {
			return nil, err
		}
		return keys, vm.storeThrough("jl_idset_put_key", a[2], core.Int64Type, newidx)
	}
	vm.shims["jl_idset_put_idx"] = func(vm *VM, a []any) (any, error) {
		keys, idxs, err := vm.idsetArgs("jl_idset_put_idx", a[0], a[1])
		if err != nil {
			return nil, err
		}
		idx, ok := a[2].(int64)
		if !ok {
			return nil, vm.typeError("ccall", "", core.Int64Type, a[2])
		}
		hash := vm.idsetHash(keys)
		if idx >= 0 {
			return vm.smallintsetInsert(idxs, hash, uint64(idx))
		}
		// a full rehash
		smallintsetEmpty(idxs)
		for i := int64(0); i < -idx && i < int64(len(keys.Elems)); i++ {
			if keys.Elems[i] != undef {
				if idxs, err = vm.smallintsetInsert(idxs, hash, uint64(i)); err != nil {
					return nil, err
				}
			}
		}
		return idxs, nil
	}
	vm.shims["jl_idset_rehash"] = func(vm *VM, a []any) (any, error) {
		keys, idxs, err := vm.idsetArgs("jl_idset_rehash", a[0], a[1])
		if err != nil {
			return nil, err
		}
		n, ok := a[2].(uint64)
		if !ok {
			return nil, vm.typeError("ccall", "", core.UInt64Type, a[2])
		}
		if n == 0 {
			return idxs, nil
		}
		return vm.smallintsetRehash(idxs, vm.idsetHash(keys), nextPowerOfTwo(n), 0)
	}
}

func nextPowerOfTwo(n uint64) uint64 {
	p := uint64(1)
	for p < n {
		p <<= 1
	}
	return p
}

// idsetPutKey is jl_idset_put_key (idset.c:66-91): key after the last
// kept key, compacting or growing the keys; the index it takes, negative
// when the index must be rebuilt (-(count)-1).
func (vm *VM) idsetPutKey(keys *Memory, key any) (*Memory, int64, error) {
	l := len(keys.Elems)
	i := l
	for i > 0 && keys.Elems[i-1] == undef {
		i--
	}
	newidx := int64(i)
	if i == l {
		// idset_compact (idset.c:47-64)
		j, rehash := 0, false
		for k := 0; k < l; k++ {
			if keys.Elems[k] != undef {
				if k != j {
					rehash = true
					keys.Elems[j], keys.Elems[k] = keys.Elems[k], undef
				}
				j++
			}
		}
		i = j
		if rehash {
			newidx = int64(-j - 1)
		}
		if i >= l/3*2 {
			// grow by 50% when under a third is free after compacting
			nl := uint64(l*3) >> 1
			if l < 4 {
				nl = 4
			}
			nk, err := vm.newEqTable(nl)
			if err != nil {
				return nil, 0, err
			}
			copy(nk.Elems, keys.Elems[:i])
			keys = nk
		}
	}
	keys.Elems[i] = key
	return keys, newidx, nil
}
