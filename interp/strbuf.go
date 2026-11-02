package interp

import (
	"fmt"
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
	n, ok := a[0].(int64)
	if !ok {
		return nil, vm.typeError("ccall", "", core.Int64Type, a[0])
	}
	// a negative Int is a size_t past any memory
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
// bytes: from a String into a String allocString made. Anything else, and
// any range outside the strings, is refused: C has no checks to refuse it
// with.
func (vm *VM) memmove(a []any) (any, error) {
	dp, ok1 := a[0].(*Pointer)
	sp, ok2 := a[1].(*Pointer)
	n, ok3 := a[2].(uint64)
	if ok1 && ok2 && ok3 {
		dst, ok1 := dp.Target.(string)
		src, ok2 := stringBytes(sp, n)
		if ok1 && ok2 {
			b, ok := vm.writableBytes(dst)
			if ok && dp.Off >= 0 && dp.Off <= int64(len(b)) && n <= uint64(int64(len(b))-dp.Off) {
				copy(b[dp.Off:], src)
				return dp, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: memmove of %s", ErrUnsupported, showValue(vm.newTuple(a)))
}
