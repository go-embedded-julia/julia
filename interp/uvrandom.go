package interp

import (
	"crypto/rand"
	"fmt"

	"github.com/go-embedded-julia/julia/core"
)

// uvRandom is libuv's uv_random(loop, req, buf, buflen, flags, cb) as
// Libc.getrandom! calls it (libc.jl:425-430): synchronously, without a
// loop, filling buflen bytes at buf with the operating system's
// randomness, which Go's crypto/rand reads (getentropy on darwin, as
// libuv's). buf points at a Ref of a primitive value, or into a memory of
// primitive elements; 0 on success.
func (vm *VM) uvRandom(a []any) (any, error) {
	p, ok := a[2].(*Pointer)
	n, isN := a[3].(uint64)
	if !ok || !isN {
		return nil, fmt.Errorf("%w: uv_random into %s", ErrUnsupported, showValue(a[2]))
	}
	switch t := p.Target.(type) {
	case *Struct:
		ft, _ := t.Type.FieldTypes()
		var et *core.DataType
		if len(ft) == 1 {
			et, _ = ft[0].(*core.DataType)
		}
		if p.Off != 0 || et == nil || et.Name.Bits == 0 || uint64(et.Name.Bits/8) != n {
			return nil, fmt.Errorf("%w: uv_random of %d bytes into a %s", ErrUnsupported, n, showValue(t.Type))
		}
		b := make([]byte, n)
		rand.Read(b)
		v, _ := bytesValue(et, b)
		t.Fields[0] = v
		return int32(0), nil
	case *Memory:
		et, isPrim := t.Type.Parameters[1].(*core.DataType)
		if !isPrim || et.Name.Bits == 0 || et.Name.Bits%8 != 0 {
			return nil, fmt.Errorf("%w: uv_random into a %s", ErrUnsupported, showValue(t.Type))
		}
		if _, err := vm.memByteRange("uv_random", t, p.Off, int(min(n, 1<<31))); err != nil || n >= 1<<31 {
			return nil, vm.errorf("uv_random: invalid pointer")
		}
		b := make([]byte, n)
		rand.Read(b)
		for i, c := range b {
			// a byte of a primitive memory, in the range checked above:
			// memStoreBytes cannot refuse it
			_ = vm.memStoreBytes("uv_random", t, p.Off+int64(i), c)
		}
		return int32(0), nil
	}
	return nil, fmt.Errorf("%w: uv_random into %s", ErrUnsupported, showValue(a[2]))
}
