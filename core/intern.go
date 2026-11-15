package core

import (
	"cmp"
	"errors"
	"slices"
	"unsafe"
)

// errInternChanged is a Commit that found a cache another goroutine had
// grown since the Interner first looked at it: nothing was written, and the
// caller decodes again (IsInternChanged).
var errInternChanged = errors.New("core: a type cache changed while the types were being interned")

// IsInternChanged reports whether err is the refusal of an Interner's Commit
// that wrote nothing because a cache had changed since it was read.
func IsInternChanged(err error) bool { return errors.Is(err, errInternChanged) }

type internSlot struct {
	tn *TypeName
	hv uint32
}

type internEntry struct {
	slot internSlot
	dt   *DataType
}

// Interner is jl_cache_type_ for a load that must not change process-shared
// state until it is sure to succeed: Resolve answers what Intern would, from
// the shared caches and from the types the Interner holds itself, and writes
// nothing to the shared caches; Commit puts the held types in them, all or
// none.
type Interner struct {
	added map[internSlot][]*DataType
	seen  map[internSlot]int // the length of a bucket the first time it was read
	order []internEntry
}

// NewInterner is an Interner that holds no type.
func NewInterner() *Interner {
	return &Interner{added: map[internSlot][]*DataType{}, seen: map[internSlot]int{}}
}

// Resolve is the type equal to dt that the process holds (shared true), or
// the one the Interner holds, or dt itself, which the Interner then holds.
func (in *Interner) Resolve(dt *DataType) (c *DataType, shared bool) {
	tn, key := dt.Name, dt.Parameters
	slot := internSlot{tn, typekeyHash(tn, key, false)}
	tn.mu.Lock()
	snap := tn.cacheSlot(slot.hv)
	tn.mu.Unlock()
	if _, ok := in.seen[slot]; !ok {
		in.seen[slot] = len(snap)
	}
	if c := findIn(snap, key); c != nil {
		return c, true
	}
	if c := findIn(in.added[slot], key); c != nil {
		return c, false
	}
	in.added[slot] = append(in.added[slot], dt)
	in.order = append(in.order, internEntry{slot, dt})
	return dt, false
}

// Commit puts every type the Interner holds in its name's cache, in the order
// they were held. It checks them all under the locks of all their names, and
// writes none of them (IsInternChanged) when a bucket grew since it was read
// or a name holds another Type{Wrapper} than the one a type would set.
func (in *Interner) Commit() error {
	var names []*TypeName
	for slot := range in.seen {
		names = append(names, slot.tn)
	}
	slices.SortFunc(names, func(a, b *TypeName) int {
		return cmp.Compare(uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(b)))
	})
	names = slices.Compact(names)
	for _, tn := range names {
		tn.mu.Lock()
		defer tn.mu.Unlock()
	}
	for slot, n := range in.seen {
		if len(slot.tn.cacheSlot(slot.hv)) != n {
			return errInternChanged
		}
	}
	for _, e := range in.order {
		if w := typeofWrapperOf(e.dt); w != nil && w.Name.typeofWrapper != nil && w.Name.typeofWrapper != e.dt {
			return errInternChanged
		}
	}
	for _, e := range in.order {
		insertLocked(e.slot.tn, e.slot.hv, e.dt)
	}
	return nil
}
