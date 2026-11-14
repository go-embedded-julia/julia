package interp

import (
	"fmt"
	"runtime"
	"sync"
	"weak"
)

// Addresses without host memory. Julia code compares pointers and does
// arithmetic on them as integers (pointer.jl: ==(x::Ptr, y::Ptr) is
// UInt(x) == UInt(y)). A pointer here has no host address, so each target
// whose address is taken gets a region of a VM-local address space:
// address = id<<regionBits | offset, never 0 (C_NULL) and never a host
// address, and an integer in a known region becomes a pointer into that
// target again. Memory targets are held weakly: a region goes with its
// memory.

const (
	regionBits = 40
	maxRegions = 1<<(63-regionBits) - 1
)

type addrSpace struct {
	mu       sync.Mutex
	next     uint64
	byTarget map[any]uint64 // a target, or a weak.Pointer[Memory]
	byID     map[uint64]any
}

// key is how the table holds target: a memory weakly, anything else as is.
func addrKey(target any) any {
	if m, ok := target.(*Memory); ok {
		return weak.Make(m)
	}
	return target
}

// addressOf is the address of a pointer into a target.
func (vm *VM) addressOf(p *Pointer) (uint64, error) {
	if p.Off < 0 || p.Off >= 1<<regionBits {
		return 0, fmt.Errorf("%w: the address of %s, %d bytes from its start", ErrUnsupported, showValue(p), p.Off)
	}
	a := &vm.addrs
	key := addrKey(p.Target)
	a.mu.Lock()
	defer a.mu.Unlock()
	id, ok := a.byTarget[key]
	if !ok {
		if a.next >= maxRegions {
			return 0, fmt.Errorf("%w: more than %d addressed objects", ErrMemoryLimit, maxRegions)
		}
		a.next++
		id = a.next
		a.byTarget[key], a.byID[id] = id, key
		if m, isMem := p.Target.(*Memory); isMem {
			runtime.AddCleanup(m, func(k any) { vm.dropRegion(k) }, key)
		}
	}
	return id<<regionBits | uint64(p.Off), nil
}

func (vm *VM) dropRegion(key any) {
	a := &vm.addrs
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.byID, a.byTarget[key])
	delete(a.byTarget, key)
}

// targetAt is the target and offset of an address in a known region,
// false for any other address.
func (vm *VM) targetAt(addr uint64) (any, int64, bool) {
	a := &vm.addrs
	a.mu.Lock()
	key, ok := a.byID[addr>>regionBits]
	a.mu.Unlock()
	if !ok {
		return nil, 0, false
	}
	if w, isWeak := key.(weak.Pointer[Memory]); isWeak {
		m := w.Value()
		if m == nil {
			return nil, 0, false
		}
		return m, int64(addr & (1<<regionBits - 1)), true
	}
	return key, int64(addr & (1<<regionBits - 1)), true
}
