package interp

import (
	"encoding/binary"
	"fmt"
	"math/bits"
)

// memhashSeed is memhash_seed (support/hashing.c:68-78) on a 64-bit
// platform: the second word of MurmurHash3_x64_128 of buf
// (support/MurmurHash3.c:220-296), which hash(::String) is made of
// (hashing.jl:195-200); a Dict of String keys iterates in its order.
func memhashSeed(buf []byte, seed uint32) uint64 {
	_, h2 := murmur3x64128(buf, seed)
	return h2
}

func murmur3x64128(data []byte, seed uint32) (uint64, uint64) {
	n := len(data)
	nblocks := n / 16
	h1, h2 := uint64(seed), uint64(seed)
	const c1, c2 = 0x87c37b91114253d5, 0x4cf5ad432745937f
	// body
	for i := 0; i < nblocks; i++ {
		k1 := binary.LittleEndian.Uint64(data[16*i:])
		k2 := binary.LittleEndian.Uint64(data[16*i+8:])
		k1 *= c1
		k1 = bits.RotateLeft64(k1, 31)
		k1 *= c2
		h1 ^= k1
		h1 = bits.RotateLeft64(h1, 27)
		h1 += h2
		h1 = h1*5 + 0x52dce729
		k2 *= c2
		k2 = bits.RotateLeft64(k2, 33)
		k2 *= c1
		h2 ^= k2
		h2 = bits.RotateLeft64(h2, 31)
		h2 += h1
		h2 = h2*5 + 0x38495ab5
	}
	// tail: the switch's fallthroughs, bytes 15 down to 9 into k2, 8 down
	// to 1 into k1
	tail := data[16*nblocks:]
	var k1, k2 uint64
	for i := len(tail) - 1; i >= 8; i-- {
		k2 ^= uint64(tail[i]) << (8 * (i - 8))
	}
	if len(tail) > 8 {
		k2 *= c2
		k2 = bits.RotateLeft64(k2, 33)
		k2 *= c1
		h2 ^= k2
	}
	for i := min(len(tail), 8) - 1; i >= 0; i-- {
		k1 ^= uint64(tail[i]) << (8 * i)
	}
	if len(tail) > 0 {
		k1 *= c1
		k1 = bits.RotateLeft64(k1, 31)
		k1 *= c2
		h1 ^= k1
	}
	// finalization
	h1 ^= uint64(n)
	h2 ^= uint64(n)
	h1 += h2
	h2 += h1
	h1 = fmix64(h1)
	h2 = fmix64(h2)
	h1 += h2
	h2 += h1
	return h1, h2
}

// fmix64 is MurmurHash3's finalization mix (MurmurHash3.c:47-56).
func fmix64(k uint64) uint64 {
	k ^= k >> 33
	k *= 0xff51afd7ed558ccd
	k ^= k >> 33
	k *= 0xc4ceb9fe1a85ec53
	k ^= k >> 33
	return k
}

// memhashShim is ccall(:memhash_seed, UInt, (Ptr{UInt8}, Csize_t, UInt32),
// p, n, seed).
func (vm *VM) memhashShim(a []any) (any, error) {
	p, ok1 := a[0].(*Pointer)
	n, ok2 := a[1].(uint64)
	seed, ok3 := a[2].(uint32)
	if ok1 && ok2 && ok3 {
		if b, ok := vm.bytesAt(p, n); ok {
			return memhashSeed(b, seed), nil
		}
	}
	return nil, fmt.Errorf("%w: memhash_seed of %s", ErrUnsupported, showValue(vm.newTuple(a)))
}
