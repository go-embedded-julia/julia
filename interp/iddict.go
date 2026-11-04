package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
)

// The eq hash table under IdDict and IdSet (iddict.c): a Memory{Any} of
// key, value pairs, open-addressed by objectID with linear probing. A slot
// whose key is undef (C's NULL) ends a probe; a deleted pair keeps nothing
// as its key and undef as its value, which a probe passes over.

// htNInline is HT_N_INLINE (support/htable.h:6), a new table's length.
const htNInline = 32

// maxProbe is max_probe (iddict.c:6).
func maxProbe(size int) int {
	if size <= 1024 {
		return 16
	}
	return size >> 6
}

// eqTable is the Memory{Any} argument of each jl_eqtable_* call.
func (vm *VM) eqTable(fname string, v any) (*Memory, error) {
	m, ok := v.(*Memory)
	if !ok || m.Type != memAnyType {
		return nil, fmt.Errorf("%w: %s of %s", ErrUnsupported, fname, showValue(v))
	}
	return m, nil
}

// newEqTable is jl_alloc_memory_any(n): n undefined slots, within
// VM.MaxMemoryElems.
func (vm *VM) newEqTable(n uint64) (*Memory, error) {
	if n > uint64(vm.MaxMemoryElems) {
		return nil, fmt.Errorf("%w: an IdDict table of %d slots (VM.MaxMemoryElems is %d)", ErrMemoryLimit, n, vm.MaxMemoryElems)
	}
	m := &Memory{Type: memAnyType, Elems: make([]any, n)}
	for i := range m.Elems {
		m.Elems[i] = undef
	}
	return m, nil
}

// idtableRehash is jl_idtable_rehash (iddict.c:13-30): a's pairs in a new
// table of newsz slots.
func (vm *VM) idtableRehash(a *Memory, newsz uint64) (*Memory, error) {
	newa, err := vm.newEqTable(newsz)
	if err != nil {
		return nil, err
	}
	for i := 0; i+1 < len(a.Elems); i += 2 {
		if a.Elems[i+1] != undef {
			if newa, _, err = vm.tableAssign(newa, a.Elems[i], a.Elems[i+1]); err != nil {
				return nil, err
			}
		}
	}
	return newa, nil
}

// tableAssign is jl_table_assign_bp (iddict.c:32-108): key's value set to
// val, in a or the larger table it grows into, and whether key is new.
func (vm *VM) tableAssign(a *Memory, key, val any) (*Memory, bool, error) {
	sz := len(a.Elems) / 2
	if sz == 0 {
		var err error
		if a, err = vm.newEqTable(htNInline); err != nil {
			return nil, false, err
		}
		sz = len(a.Elems) / 2
	}
	maxprobe := maxProbe(sz)
	hv := vm.objectID(key)
	for {
		tab := a.Elems
		index := int(hv&uint64(sz-1)) * 2
		sz *= 2
		orig, empty := index, -1
		for iter := 0; ; {
			k2 := tab[index]
			if k2 == undef {
				if empty == -1 {
					empty = index
				}
				break
			}
			if vm.egal(key, k2) {
				if tab[index+1] != undef {
					tab[index+1] = val
					return a, false, nil
				}
				// nothing is the deleted key: a search for nothing goes on
				if empty == -1 {
					empty = index
				}
			}
			if empty == -1 && tab[index+1] == undef {
				empty = index
			}
			index = (index + 2) & (sz - 1)
			iter++
			if iter > maxprobe || index == orig {
				break
			}
		}
		if empty != -1 {
			tab[empty], tab[empty+1] = key, val
			return a, true, nil
		}
		// table full: grow it fast (iddict.c:90-101)
		var newsz int
		switch sz = len(a.Elems); {
		case sz < htNInline:
			newsz = htNInline
		case sz >= 1<<19 || sz <= 1<<8:
			newsz = sz << 1
		default:
			newsz = sz << 2
		}
		var err error
		if a, err = vm.idtableRehash(a, uint64(newsz)); err != nil {
			return nil, false, err
		}
		sz = len(a.Elems) / 2
		maxprobe = maxProbe(sz)
	}
}

// tablePeek is jl_table_peek_bp (iddict.c:111-141): the index of key's
// value, -1 when key is not in a.
func (vm *VM) tablePeek(a *Memory, key any) int {
	sz := len(a.Elems) / 2
	if sz == 0 {
		return -1
	}
	maxprobe := maxProbe(sz)
	index := int(vm.objectID(key)&uint64(sz-1)) * 2
	sz *= 2
	orig := index
	for iter := 0; ; {
		k2 := a.Elems[index]
		if k2 == undef {
			return -1
		}
		if vm.egal(key, k2) {
			if a.Elems[index+1] != undef {
				return index + 1
			}
			if key != nil {
				return -1
			}
		}
		index = (index + 2) & (sz - 1)
		iter++
		if iter > maxprobe || index == orig {
			return -1
		}
	}
}

// valuePtr is jl_value_ptr (Ptr{Cvoid}, (Any,)), pointer_from_objref: a
// pointer to the object itself.
func (vm *VM) valuePtr(a []any) (any, error) {
	return &Pointer{Type: vm.ptrType(core.NothingType), Target: a[0]}, nil
}

// The jl_eqtable_* entry points (iddict.c:143-192).
func (vm *VM) bootEqtableShims() {
	vm.bootIdSet()
	vm.shims["jl_value_ptr"] = func(vm *VM, a []any) (any, error) { return vm.valuePtr(a) }
	vm.shims["jl_idtable_rehash"] = func(vm *VM, a []any) (any, error) {
		t, err := vm.eqTable("jl_idtable_rehash", a[0])
		if err != nil {
			return nil, err
		}
		n, ok := a[1].(uint64)
		if !ok {
			return nil, vm.typeError("ccall", "", core.UInt64Type, a[1])
		}
		return vm.idtableRehash(t, n)
	}
	vm.shims["jl_eqtable_put"] = func(vm *VM, a []any) (any, error) {
		t, err := vm.eqTable("jl_eqtable_put", a[0])
		if err != nil {
			return nil, err
		}
		t, inserted, err := vm.tableAssign(t, a[1], a[2])
		if err != nil {
			return nil, err
		}
		var n int32
		if inserted {
			n = 1
		}
		return t, vm.storeThrough("jl_eqtable_put", a[3], core.Int32Type, n)
	}
	vm.shims["jl_eqtable_get"] = func(vm *VM, a []any) (any, error) {
		t, err := vm.eqTable("jl_eqtable_get", a[0])
		if err != nil {
			return nil, err
		}
		if i := vm.tablePeek(t, a[1]); i >= 0 {
			return t.Elems[i], nil
		}
		return a[2], nil
	}
	vm.shims["jl_eqtable_pop"] = func(vm *VM, a []any) (any, error) {
		t, err := vm.eqTable("jl_eqtable_pop", a[0])
		if err != nil {
			return nil, err
		}
		i := vm.tablePeek(t, a[1])
		var found int32
		if i >= 0 {
			found = 1
		}
		if err := vm.storeThrough("jl_eqtable_pop", a[3], core.Int32Type, found); err != nil {
			return nil, err
		}
		if i < 0 {
			return a[2], nil
		}
		val := t.Elems[i]
		t.Elems[i-1], t.Elems[i] = nil, undef // the key becomes nothing
		return val, nil
	}
	vm.shims["jl_eqtable_nextind"] = func(vm *VM, a []any) (any, error) {
		t, err := vm.eqTable("jl_eqtable_nextind", a[0])
		if err != nil {
			return nil, err
		}
		i, ok := a[1].(uint64)
		if !ok {
			return nil, vm.typeError("ccall", "", core.UInt64Type, a[1])
		}
		// an odd-length table's last slot has no value: C reads past the
		// memory there, here it ends the table
		i += i & 1
		for i+1 < uint64(len(t.Elems)) && t.Elems[i+1] == undef {
			i += 2
		}
		if i+1 >= uint64(len(t.Elems)) {
			return ^uint64(0), nil
		}
		return i, nil
	}
}
