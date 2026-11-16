package interp

import "github.com/go-embedded-julia/julia/core/dispatch"

// The undo log of a Kernel (#93): every write to an object the image holds
// (a Memory or a Struct the loader made, or one of the boot objects New made)
// is recorded, with the value it replaces, while the log is on; Kernel.Reset
// puts the values back in reverse order. An object a cell made carries no log
// and costs one nil test per write. The log has a bound: past it, nothing more
// is recorded, the overflow is noted, and Reset reloads the image instead.

// DefaultMaxUndo is the most entries a log holds.
const DefaultMaxUndo = 1 << 18 // entries of about 70 bytes: at most about 18 MiB

type undoEntry struct {
	mem  *Memory
	st   *Struct
	idx  int
	old  any   // the element or field replaced
	olds []any // the whole store replaced (SetAll), or the range from idx (ranged)

	ranged bool
}

// undo puts back what e replaced, through the accessors of Memory: the log is
// off while it runs, so that putting back is not recorded.
func (e *undoEntry) undo() {
	switch {
	case e.st != nil:
		e.st.Fields[e.idx] = e.old
	case e.ranged:
		copy(e.mem.Slice(e.idx, e.idx+len(e.olds)), e.olds)
	case e.idx < 0:
		e.mem.SetAll(e.olds)
	default:
		e.mem.Set(e.idx, e.old)
	}
}

type undoLog struct {
	on       bool
	max      int
	entries  []undoEntry
	overflow bool
	// tables are the method tables a cell defined into or deleted from
	tables map[*dispatch.Table]struct{}
}

func (l *undoLog) full() bool {
	if len(l.entries) >= l.max {
		l.overflow = true
		return true
	}
	return false
}

func (l *undoLog) recordElem(m *Memory, i int) {
	if !l.full() {
		l.entries = append(l.entries, undoEntry{mem: m, idx: i, old: m.Get(i)})
	}
}

func (l *undoLog) recordAll(m *Memory) {
	if !l.full() {
		l.entries = append(l.entries, undoEntry{mem: m, idx: -1, olds: m.All()})
	}
}

// recordRange records the values of elements lo to hi (exclusive) as one entry.
func (l *undoLog) recordRange(m *Memory, lo, hi int) {
	if hi > lo && !l.full() {
		l.entries = append(l.entries, undoEntry{mem: m, idx: lo, olds: append([]any(nil), m.Slice(lo, hi)...), ranged: true})
	}
}

func (l *undoLog) recordField(s *Struct, i int) {
	if !l.full() {
		l.entries = append(l.entries, undoEntry{st: s, idx: i, old: s.Fields[i]})
	}
}

// logField records the write of field i of s, when s is an image object.
func (s *Struct) logField(i int) {
	if l := s.undo; l != nil && l.on {
		l.recordField(s, i)
	}
}
