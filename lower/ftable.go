package lower

// ftable is femtolisp's `table`: an open-addressing hash table
// (src/support/htable.inc, src/flisp/table.c, the equal-hash of
// src/flisp/equal.c). The front end iterates some tables (table.foldl,
// table.keys) and emits code in their order, so the layout -- hash
// function, probing, growth -- is reproduced exactly, not just the
// mapping.

import (
	"encoding/binary"
	"math"
	"math/bits"

	"github.com/go-embedded-julia/julia/expr"
)

const htNInline = 32

type ftable struct {
	keys []any
	vals []any
	used []bool // the key slot holds a key (HT_NOTFOUND otherwise)
	live []bool // the value slot holds a value (deleted entries keep their key)
}

func newTable() *ftable {
	t := &ftable{}
	t.alloc(htNInline / 2)
	return t
}

// alloc sets n buckets (htable size 2n).
func (t *ftable) alloc(n int) {
	t.keys = make([]any, n)
	t.vals = make([]any, n)
	t.used = make([]bool, n)
	t.live = make([]bool, n)
}

func maxProbe(sz int) int {
	if sz <= htNInline*2 {
		return htNInline / 2
	}
	return sz >> 3
}

// lookup is HTNAME_lookup_bp_r: the bucket of key, inserted if absent.
func (t *ftable) lookup(key any) int {
	hv := flHash(key)
	for {
		sz := len(t.keys) // hash_size(h) buckets
		maxprobe := maxProbe(sz)
		index := int(hv & uint64(sz-1))
		orig := index
		empty := -1
		iter := 0
		for {
			if !t.used[index] {
				if empty == -1 {
					empty = index
				}
				break
			}
			if !t.live[index] && empty == -1 {
				empty = index
			}
			if flEqual(key, t.keys[index]) {
				return index
			}
			index = (index + 1) & (sz - 1)
			iter++
			if iter > maxprobe || index == orig {
				break
			}
		}
		if empty != -1 {
			t.keys[empty] = key
			t.used[empty] = true
			return empty
		}
		// table full: grow, rehash in slot order, retry
		oldKeys, oldVals, oldLive := t.keys, t.vals, t.live
		// htable.inc's `if (sz < HT_N_INLINE) newsz = HT_N_INLINE` is
		// left out: a table starts with HT_N_INLINE/2 buckets, so size is
		// at least HT_N_INLINE
		size := 2 * sz
		var newsz int
		switch {
		case size >= 1<<19 || size <= 1<<8:
			newsz = size << 1
		default:
			newsz = size << 2
		}
		t.alloc(newsz / 2)
		for i := range oldKeys {
			if oldLive[i] {
				j := t.lookup(oldKeys[i])
				t.vals[j] = oldVals[i]
				t.live[j] = true
			}
		}
	}
}

// peek is HTNAME_peek_bp_r: the bucket of key, or -1.
func (t *ftable) peek(key any) int {
	sz := len(t.keys)
	maxprobe := maxProbe(sz)
	index := int(flHash(key) & uint64(sz-1))
	orig := index
	for iter := 0; ; {
		if !t.used[index] {
			return -1
		}
		if flEqual(key, t.keys[index]) {
			return index
		}
		index = (index + 1) & (sz - 1)
		iter++
		if iter > maxprobe || index == orig {
			return -1
		}
	}
}

func (t *ftable) put(key, val any) {
	i := t.lookup(key)
	t.vals[i] = val
	t.live[i] = true
}

// get returns the value of key, or def.
func (t *ftable) get(key, def any) any {
	i := t.peek(key)
	if i < 0 || !t.live[i] {
		return def
	}
	return t.vals[i]
}

func (t *ftable) has(key any) bool {
	i := t.peek(key)
	return i >= 0 && t.live[i]
}

// del is del! (femtolisp raises a key error on a missing key; the front
// end only deletes present keys).
func (t *ftable) del(key any) {
	if i := t.peek(key); i >= 0 {
		t.live[i] = false
		t.vals[i] = nil
	}
}

// foldl visits the live entries in slot order.
func (t *ftable) foldl(f func(k, v, acc any) any, acc any) any {
	for i := range t.keys {
		if t.live[i] {
			acc = f(t.keys[i], t.vals[i], acc)
		}
	}
	return acc
}

// keysList is table.keys: the keys in reverse slot order.
func (t *ftable) keysList() []any {
	var out []any
	for i := len(t.keys) - 1; i >= 0; i-- {
		if t.live[i] {
			out = append(out, t.keys[i])
		}
	}
	return out
}

// clone is table.clone (utils.scm): the entries re-put in slot order.
func (t *ftable) clone() *ftable {
	nt := newTable()
	for i := range t.keys {
		if t.live[i] {
			nt.put(t.keys[i], t.vals[i])
		}
	}
	return nt
}

// flEqual is equal_lispvalue for the keys the front end uses.
func flEqual(a, b any) bool { return equal(a, b) }

// flHash is hash_lispvalue (64-bit build).
func flHash(v any) uint64 {
	return boundedHash(v, 16384)
}

func boundedHash(v any, bound int) uint64 {
	switch x := v.(type) {
	case sym:
		return uint64(symbolHash(string(x)))
	case int64:
		return int64hash(math.Float64bits(float64(x)))
	case []any:
		if len(x) == 0 {
			return int64hash(flNil)
		}
		var h uint64
		for _, e := range x {
			if bound <= 0 {
				return h
			}
			h = mix(h, boundedHash(e, bound/2))
			bound--
		}
		// the terminating () of the list
		return mix(h, int64hash(flNil)^2)
	case bool:
		if x {
			return int64hash(flTrue)
		}
		return int64hash(flFalse)
	case sstr:
		return memhash([]byte(x))
	}
	// A julia value: femtolisp hashes the object's address, so no table
	// keyed by julia values is iterated; any hash consistent with
	// equality does.
	return memhash([]byte(expr.Serialize(unbox(v))))
}

// femtolisp's #t, #f and () are builtins (flisp.c: builtin(OP_BOOL_CONST_T)
// and the next two opcodes of opcodes.h, tagged TAG_FUNCTION), which
// bounded_hash hashes by their value with inthash.
const (
	flTrue  = 95<<3 | 2
	flFalse = 96<<3 | 2
	flNil   = 97<<3 | 2
)

func mix(a, b uint64) uint64 { return int64hash(a ^ b) }

func int64hash(key uint64) uint64 {
	key = (^key) + (key << 21)
	key = key ^ (key >> 24)
	key = (key + (key << 3)) + (key << 8)
	key = key ^ (key >> 14)
	key = (key + (key << 2)) + (key << 4)
	key = key ^ (key >> 28)
	key = key + (key << 31)
	return key
}

// symbolHash is mk_symbol's hash: memhash32(name) ^ 0xAAAAAAAA.
func symbolHash(s string) uint32 {
	return murmur3x86_32([]byte(s), 0xcafe8881) ^ 0xAAAAAAAA
}

// memhash is support/hashing.c's memhash (MurmurHash3_x64_128, second word).
func memhash(b []byte) uint64 {
	_, h2 := murmur3x64_128(b, 0xcafe8881)
	return h2
}

func murmur3x86_32(data []byte, seed uint32) uint32 {
	const c1, c2 = 0xcc9e2d51, 0x1b873593
	h1 := seed
	n := len(data) / 4
	for i := 0; i < n; i++ {
		k1 := binary.LittleEndian.Uint32(data[4*i:])
		k1 *= c1
		k1 = bits.RotateLeft32(k1, 15)
		k1 *= c2
		h1 ^= k1
		h1 = bits.RotateLeft32(h1, 13)
		h1 = h1*5 + 0xe6546b64
	}
	tail := data[4*n:]
	var k1 uint32
	switch len(tail) {
	case 3:
		k1 ^= uint32(tail[2]) << 16
		fallthrough
	case 2:
		k1 ^= uint32(tail[1]) << 8
		fallthrough
	case 1:
		k1 ^= uint32(tail[0])
		k1 *= c1
		k1 = bits.RotateLeft32(k1, 15)
		k1 *= c2
		h1 ^= k1
	}
	h1 ^= uint32(len(data))
	h1 ^= h1 >> 16
	h1 *= 0x85ebca6b
	h1 ^= h1 >> 13
	h1 *= 0xc2b2ae35
	h1 ^= h1 >> 16
	return h1
}

func fmix64(k uint64) uint64 {
	k ^= k >> 33
	k *= 0xff51afd7ed558ccd
	k ^= k >> 33
	k *= 0xc4ceb9fe1a85ec53
	k ^= k >> 33
	return k
}

func murmur3x64_128(data []byte, seed uint32) (uint64, uint64) {
	const c1, c2 = 0x87c37b91114253d5, 0x4cf5ad432745937f
	h1, h2 := uint64(seed), uint64(seed)
	n := len(data) / 16
	for i := 0; i < n; i++ {
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
	tail := data[16*n:]
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
	h1 ^= uint64(len(data))
	h2 ^= uint64(len(data))
	h1 += h2
	h2 += h1
	h1 = fmix64(h1)
	h2 = fmix64(h2)
	h1 += h2
	h2 += h1
	return h1, h2
}
