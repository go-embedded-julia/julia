package interp

import (
	"bytes"
	"fmt"
	"math"
	"runtime"
	"unsafe"
	"weak"

	"github.com/go-embedded-julia/julia/core"
)

// Strings Julia code writes. jl_alloc_string (array.c:290-305) returns a
// String of n bytes that Julia code then fills through its Ptr{UInt8}
// (strcat, Base_compiler.jl:170-182; _string_n's callers, string.jl:106-
// 110), with memmove (cmem.jl:26-29). A Go string cannot be written, so the
// VM allocates the bytes itself and makes the String over them
// (unsafe.String), and a write through a pointer reaches only such a
// string, within its length: never one the VM did not allocate, which may
// be in the binary's read-only data. As in julia, writing a String that
// has been read already (hashed, compared) leaves it inconsistent with
// what read it; the host's memory stays safe.

// allocString is jl_alloc_string(n): a fresh, writable String of n zero
// bytes, refused past VM.MaxMemoryElems bytes (julia's OutOfMemoryError).
func (vm *VM) allocString(a []any) (any, error) {
	// C's size_t, which Base passes as an Int or a Csize_t: a negative Int
	// is a size_t past any memory
	n, ok := a[0].(int64)
	if u, isU := a[0].(uint64); isU {
		n, ok = int64(u), true
	}
	if !ok {
		return nil, vm.typeError("ccall", "", core.UInt64Type, a[0])
	}
	if n < 0 || n > int64(vm.MaxMemoryElems) {
		return nil, fmt.Errorf("%w: String of %d bytes (VM.MaxMemoryElems is %d)", ErrMemoryLimit, uint64(n), vm.MaxMemoryElems)
	}
	if n == 0 {
		return "", nil // jl_an_empty_string
	}
	b := make([]byte, n)
	buf := strBuf{weak.Make(&b[0]), len(b)}
	addr := uintptr(unsafe.Pointer(&b[0]))
	vm.strMu.Lock()
	vm.strBufs[addr] = buf
	vm.strMu.Unlock()
	runtime.AddCleanup(&b[0], func(buf strBuf) {
		vm.strMu.Lock()
		// the address may hold a newer string already
		if vm.strBufs[addr].p == buf.p {
			delete(vm.strBufs, addr)
		}
		vm.strMu.Unlock()
	}, buf)
	return unsafe.String(&b[0], n), nil
}

// stringToMemory is jl_string_to_genericmemory (genericmemory.c:104-115):
// a Memory{UInt8} of str's bytes. julia's shares them, the String owning
// the data; this one holds a copy. Base writes such a memory (StringMemory,
// an IOBuffer's) and turns it back into a String with
// jl_genericmemory_to_string, which reads the memory: the copy is what is
// written and read.
func (vm *VM) stringToMemory(a []any) (any, error) {
	s, ok := a[0].(string)
	if !ok {
		return nil, vm.typeError("ccall", "", core.StringType, a[0])
	}
	elems := make([]any, len(s))
	for i := 0; i < len(s); i++ {
		elems[i] = s[i]
	}
	return &Memory{Type: memUInt8Type, Elems: elems}, nil
}

// memoryToString is jl_genericmemory_to_string(m, len)
// (genericmemory.c:189-217): a String of m's first len bytes, m's length
// set to 0 as julia does.
func (vm *VM) memoryToString(a []any) (any, error) {
	m, ok := a[0].(*Memory)
	n, ok2 := a[1].(int64)
	if !ok || m.Type != memUInt8Type || !ok2 || n < 0 || n > int64(len(m.Elems)) {
		return nil, fmt.Errorf("%w: jl_genericmemory_to_string of %s", ErrUnsupported, showValue(vm.newTuple(a)))
	}
	if n == 0 {
		return "", nil // jl_an_empty_string
	}
	b := make([]byte, n)
	for i := range b {
		c, ok := m.Elems[i].(uint8)
		if !ok {
			return nil, vm.exception(vm.tUndefRef)
		}
		b[i] = c
	}
	m.Elems = m.Elems[:0]
	return string(b), nil
}

// pcharToString is jl_pchar_to_string(p, len) (array.c): a String of the
// len bytes from p, in a String or a memory of bytes.
func (vm *VM) pcharToString(a []any) (any, error) {
	if s, ok := stringBytes(a[0], a[1]); ok {
		return s, nil
	}
	p, ok := a[0].(*Pointer)
	n, ok2 := a[1].(int64)
	if ok && ok2 && n >= 0 {
		if m, isMem := p.Target.(*Memory); isMem && m.Type == memUInt8Type && p.Off >= 0 && p.Off+n <= int64(len(m.Elems)) {
			b := make([]byte, n)
			for i := range b {
				c, _ := m.Elems[p.Off+int64(i)].(uint8)
				b[i] = c
			}
			return string(b), nil
		}
	}
	return nil, fmt.Errorf("%w: jl_pchar_to_string of %s", ErrUnsupported, showValue(vm.newTuple(a)))
}

// strBuf is a string allocString made: its bytes, held weakly so that the
// table does not keep them alive, and its length.
type strBuf struct {
	p weak.Pointer[byte]
	n int
}

// writableBytes is the bytes of s, when allocString made it. The table is
// keyed by address, and the weak pointer tells the string that lives there
// from one that lived there before; weak.Make itself is never called on a
// string's bytes here, which may be outside the heap.
func (vm *VM) writableBytes(s string) ([]byte, bool) {
	if len(s) == 0 {
		return nil, false
	}
	data := unsafe.StringData(s)
	vm.strMu.Lock()
	buf, ok := vm.strBufs[uintptr(unsafe.Pointer(data))]
	vm.strMu.Unlock()
	if !ok || buf.p.Value() != data || buf.n != len(s) {
		return nil, false
	}
	return unsafe.Slice(data, buf.n), true
}

// memmove is memmove(dst, src, n) for what a pointer here can address in
// bytes: from a String into a String allocString made, and between
// memories of one inline element type, whole elements at element offsets
// (unsafe_copyto! of Ptrs, array.jl:265-270). Anything else, and any range
// outside them, is refused: C has no checks to refuse it with.
func (vm *VM) memmove(a []any) (any, error) {
	dp, ok1 := a[0].(*Pointer)
	sp, ok2 := a[1].(*Pointer)
	n, ok3 := a[2].(uint64)
	if ok1 && ok2 && ok3 {
		dm, okd := dp.Target.(*Memory)
		sm, oks := sp.Target.(*Memory)
		if okd && oks && memmoveElems(dm, dp.Off, sm, sp.Off, n) {
			return dp, nil
		}
		dst, ok1 := dp.Target.(string)
		src, ok2 := stringBytes(sp, n)
		if ok1 && ok2 {
			b, ok := vm.writableBytes(dst)
			if ok && dp.Off >= 0 && dp.Off <= int64(len(b)) && n <= uint64(int64(len(b))-dp.Off) {
				copy(b[dp.Off:], src)
				return dp, nil
			}
		}
		// bytes between a String and a memory of bytes, either way
		_, srcStr := sp.Target.(string)
		_, srcMem := sp.Target.(*Memory)
		_, dstMem := dp.Target.(*Memory)
		if srcStr && dstMem || srcMem && ok1 {
			if b, ok := vm.bytesAt(sp, n); ok {
				if err := vm.writeBytes(dp, b); err != nil {
					return nil, err
				}
				return dp, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: memmove of %s", ErrUnsupported, showValue(vm.newTuple(a)))
}

// memcmp is memcmp(p, q, n) (cmem.jl:54-57): the sign of the first
// difference of n bytes, which is all Base reads of it.
func (vm *VM) memcmp(a []any) (any, error) {
	p, ok1 := a[0].(*Pointer)
	q, ok2 := a[1].(*Pointer)
	n, ok3 := a[2].(uint64)
	if ok1 && ok2 && ok3 {
		x, okx := vm.bytesAt(p, n)
		y, oky := vm.bytesAt(q, n)
		if okx && oky {
			return int32(bytes.Compare(x, y)), nil
		}
	}
	return nil, fmt.Errorf("%w: memcmp of %s", ErrUnsupported, showValue(vm.newTuple(a)))
}

// memchr is memchr(p, c, n), and memrchr when last (search.jl:93, 146;
// cstring.jl:79): a pointer to the first (last) of the n bytes from p that
// is c's low byte, C_NULL when none is.
func (vm *VM) memchr(a []any, last bool) (any, error) {
	p, ok1 := a[0].(*Pointer)
	c, ok2 := a[1].(int32)
	n, ok3 := a[2].(uint64)
	if ok1 && ok2 && ok3 {
		if b, ok := vm.bytesAt(p, n); ok {
			i := bytes.IndexByte(b, byte(c))
			if last {
				i = bytes.LastIndexByte(b, byte(c))
			}
			if i < 0 {
				return &Pointer{Type: p.Type}, nil
			}
			return &Pointer{Type: p.Type, Target: p.Target, Off: p.Off + int64(i)}, nil
		}
	}
	return nil, fmt.Errorf("%w: memchr of %s", ErrUnsupported, showValue(vm.newTuple(a)))
}

// bytesAt is the n bytes from p, in a String or a memory of bytes, false
// when they are not all there.
func (vm *VM) bytesAt(p *Pointer, n uint64) ([]byte, bool) {
	if s, ok := stringBytes(p, n); ok {
		return []byte(s), true
	}
	m, ok := p.Target.(*Memory)
	if !ok || m.Type != memUInt8Type || p.Off < 0 || p.Off > int64(len(m.Elems)) || n > uint64(int64(len(m.Elems))-p.Off) {
		return nil, false
	}
	b := make([]byte, n)
	for i := range b {
		c, ok := m.Elems[p.Off+int64(i)].(uint8)
		if !ok {
			return nil, false
		}
		b[i] = c
	}
	return b, true
}

// memset is memset(p, val, n) (cmem.jl:40-43): n bytes of val's low byte
// from p, into a memory of primitive elements (whole elements at once
// where the range covers them) or a String allocString made; the rest is
// refused.
func (vm *VM) memset(a []any) (any, error) {
	p, ok1 := a[0].(*Pointer)
	v, ok2 := a[1].(int32)
	n, ok3 := a[2].(uint64)
	if ok1 && ok2 && ok3 && n <= uint64(math.MaxInt32) {
		c := byte(v)
		switch t := p.Target.(type) {
		case *Memory:
			if et, ok := t.Type.Parameters[1].(*core.DataType); ok && et.Name.Bits > 0 && et.Name.Bits%8 == 0 {
				es := int64(et.Name.Bits / 8)
				if _, err := vm.memByteRange("memset", t, p.Off, int(n)); err != nil {
					return nil, err
				}
				whole, _ := bytesValue(et, bytesOf(c, int(es)))
				for off := p.Off; off < p.Off+int64(n); {
					if off%es == 0 && off+es <= p.Off+int64(n) && whole != nil {
						t.Elems[off/es] = whole
						off += es
						continue
					}
					if err := vm.memStoreBytes("memset", t, off, c); err != nil {
						return nil, err
					}
					off++
				}
				return p, nil
			}
		case string:
			b, ok := vm.writableBytes(t)
			if ok && p.Off >= 0 && p.Off <= int64(len(b)) && n <= uint64(int64(len(b))-p.Off) {
				copy(b[p.Off:], bytesOf(c, int(n)))
				return p, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: memset of %s", ErrUnsupported, showValue(vm.newTuple(a)))
}

func bytesOf(c byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return b
}

// memmoveElems moves n bytes between memories of one inline element type
// as whole elements, overlap allowed, false where the bytes are not whole
// elements of both or fall outside either.
func memmoveElems(dm *Memory, doff int64, sm *Memory, soff int64, n uint64) bool {
	ety := dm.Type.Parameters[1]
	size, _, boxed, union := memElem(ety)
	if sm.Type.Parameters[1] != ety || boxed || union || size == 0 {
		return false
	}
	sz := int64(size)
	if doff < 0 || soff < 0 || doff%sz != 0 || soff%sz != 0 || n%uint64(sz) != 0 || n > uint64(math.MaxInt64) {
		return false
	}
	k, di, si := int64(n)/sz, doff/sz, soff/sz
	if di > int64(len(dm.Elems)) || si > int64(len(sm.Elems)) || k > int64(len(dm.Elems))-di || k > int64(len(sm.Elems))-si {
		return false
	}
	copy(dm.Elems[di:di+k], sm.Elems[si:si+k])
	return true
}
