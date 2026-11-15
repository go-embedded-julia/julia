package interp

// The undo log of a Kernel (#93): every write to an object the image holds
// (a Memory or a Struct the loader made, or one of the boot objects New made)
// is recorded, with the value it replaces, while the log is on; Kernel.Reset
// puts the values back in reverse order. An object a cell made carries no log
// and costs one nil test per write. The log has a bound: past it, nothing more
// is recorded, the overflow is noted, and Reset reloads the image instead.

// DefaultMaxUndo is the most entries a log holds.
const DefaultMaxUndo = 1 << 20

type undoEntry struct {
	mem  *Memory
	st   *Struct
	idx  int
	old  any   // the element or field replaced
	olds []any // the whole store replaced (SetAll)
}

type undoLog struct {
	on       bool
	max      int
	entries  []undoEntry
	overflow bool
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
		l.entries = append(l.entries, undoEntry{mem: m, idx: i, old: m.elems[i]})
	}
}

func (l *undoLog) recordAll(m *Memory) {
	if !l.full() {
		l.entries = append(l.entries, undoEntry{mem: m, idx: -1, olds: m.elems})
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
