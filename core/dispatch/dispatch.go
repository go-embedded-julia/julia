// Package dispatch is the method table of a generic function and the
// choice of the method a call runs, after src/gf.c of julia 1.12.6.
//
// This first version handles calls whose argument types are concrete, the
// case the interpreter meets at run time: the applicable methods are those
// whose signature the argument tuple is a subtype of (gf.c ml_matches with
// a dispatch tuple), and the call runs the one that is more specific than
// every other (jl_method_morespecific, subtype.c:5536-5552); when none is,
// the call is ambiguous. julia 1.12 orders the matches of a query through
// the interference sets it computes when a method is inserted and the
// typemap's visit order (gf.c ml_matches, typemap.c); mlmatches.go and
// typemap.go port them for MlMatches, which the differential test
// against julia's Base._methods_by_ftype measures. Lookup does not use them.
package dispatch

import (
	"errors"
	"fmt"
	"sync"

	"github.com/go-embedded-julia/julia/core"
)

// Method is one method of a generic function.
type Method struct {
	// Sig is the signature, the Tuple type of the arguments, possibly
	// wrapped in UnionAlls for its static parameters. julia's signatures
	// start with typeof(f); a Table belongs to one function, so its
	// signatures leave that first parameter out.
	Sig any
	// PrimaryWorld is the world the method was defined in; MaxWorld the last
	// world it is visible in (^uint64(0) while it is current).
	PrimaryWorld, MaxWorld uint64
	// Index numbers the methods of a table in definition order.
	Index int
	// Def is what the caller defined the method with (the interpreter's
	// prepared body); the table only carries it.
	Def any
	// nenv is envSize(Sig), the number of its static parameters
	nenv int

	// what ml_matches keeps of a method (gf.c, jl_method_t): its full
	// signature, the methods that are not less specific than it (its
	// interference set, in the order they were added), and dispatch_status's
	// METHOD_SIG_LATEST_ONLY bit
	full         any
	interference []*Method
	latestOnly   bool
}

// Table is a generic function's method table (jl_methtable_t).
type Table struct {
	Name string
	// Head is typeof(f) when the signatures leave it out, nil when they keep
	// the callee's type first.
	Head any

	mu      sync.RWMutex
	world   uint64
	methods []*Method

	// the leaf cache of Lookup (mt->leafcache, gf.c: lookup_leafcache and
	// cache_method): for a concrete argument tuple type, its answers, each
	// with the world range ml_matches found it valid in
	leaf    map[*core.DataType][]leafEntry
	changes uint64 // counts the calls of changed
	// clips[i] is the oldest world of the changes i, i+1, …: an entry cached
	// after i changes holds up to clips[i]-1 at most
	clips []uint64

	// the typemap, built by replaying the insertions and disables in order
	// when a query first needs it
	events   []tmEvent
	replayed int
	tm       typemap
	failed   error
}

// raceHook runs between a cache miss's lookup and its caching; tests make
// the table change there.
var raceHook = func() {}

// leafEntry is a cached Lookup: the method and static parameters, the
// worlds minWorld to maxWorld it was found valid in, and gen, the number of
// changes of the table when it was cached.
type leafEntry struct {
	minWorld, maxWorld uint64
	m                  *Method
	env                []any
	gen                uint64
}

// changed records a change of the visible methods from world w on (w ≥ 1):
// no cached answer holds from w on any more, as julia's invalidation of the
// leaf cache ends its entries' worlds (gf.c:2982-2999). The entries are not
// visited: last gives each its end when it is read. A table defining
// thousands of methods (LinearAlgebra) spent 44% of a Base load rewriting
// every entry on each definition.
func (t *Table) changed(w uint64) {
	t.changes++
	t.clips = append(t.clips, w)
	for i := len(t.clips) - 2; i >= 0 && t.clips[i] > w; i-- {
		t.clips[i] = w
	}
}

// last is the newest world e still answers in: its own maxWorld, ended
// before the oldest change made after it was cached. An entry whose range
// is empty answers in no world.
func (t *Table) last(e leafEntry) uint64 {
	if e.gen < uint64(len(t.clips)) {
		return min(e.maxWorld, t.clips[e.gen]-1)
	}
	return e.maxWorld
}

// answers reports whether e is the cached answer in world w.
func (t *Table) answers(e leafEntry, w uint64) bool {
	return e.minWorld <= w && w <= t.last(e)
}

// tmEvent is a change of the table the typemap has yet to follow.
type tmEvent struct {
	m       *Method
	disable bool
}

// ErrNoMethod and ErrAmbiguous are wrapped by the errors Lookup returns,
// julia's MethodError in its two forms.
var (
	ErrNoMethod  = errors.New("no method matching")
	ErrAmbiguous = errors.New("ambiguous")
)

// World is the newest world of the table.
func (t *Table) World() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.world
}

// Add defines a method with signature sig in a new world and returns it. A
// method whose signature is type-equal to sig is replaced: it stays visible
// in the worlds before this one (jl_method_table_insert, gf.c:3017).
func (t *Table) Add(sig any) (*Method, error) {
	t.mu.Lock()
	w := t.world + 1
	t.mu.Unlock()
	return t.Define(sig, w, nil)
}

// Define defines a method with signature sig and body def in world w, which
// a VM takes from its one world counter (jl_world_counter): julia's worlds
// are global, shared by every table and binding. w must be newer than every
// world the table has seen. A method whose signature is type-equal to sig is
// replaced, as in Add. A failure of the type algebra (its work bound) is an
// error and leaves the table as it was.
func (t *Table) Define(sig any, w uint64, def any) (_ *Method, err error) {
	if !core.IsTupleType(core.UnwrapUnionAll(sig)) {
		return nil, fmt.Errorf("method signature %s is not a Tuple type", core.Show(sig))
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if w <= t.world {
		return nil, fmt.Errorf("method of %s defined in world %d, not newer than world %d", t.Name, w, t.world)
	}
	replaced, err := t.replacedBy(sig)
	if err != nil {
		return nil, err
	}
	t.world = w
	for _, m := range replaced {
		m.MaxWorld = w - 1
	}
	m := &Method{Sig: sig, PrimaryWorld: w, MaxWorld: ^uint64(0), Index: len(t.methods), Def: def, nenv: envSize(sig)}
	t.methods = append(t.methods, m)
	t.events = append(t.events, tmEvent{m: m})
	t.changed(w)
	return m, nil
}

// replacedBy is the current methods whose signature is type-equal to sig.
func (t *Table) replacedBy(sig any) (ms []*Method, err error) {
	defer core.CatchError(&err)
	for _, m := range t.methods {
		if m.MaxWorld == ^uint64(0) && core.TypesEqual(m.Sig, sig) {
			ms = append(ms, m)
		}
	}
	return ms, nil
}

// Disable makes m, a current method of the table, invisible after world w
// (jl_method_table_disable, gf.c:2585-2606), false when it was not current.
// The caller then moves its world counter past w.
func (t *Table) Disable(m *Method, w uint64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if m.MaxWorld != ^uint64(0) {
		return false
	}
	m.MaxWorld = w
	t.events = append(t.events, tmEvent{m: m, disable: true})
	t.changed(w + 1)
	return true
}

// Methods returns the methods visible in world w, in definition order
// (julia's methods(f) in that world).
func (t *Table) Methods(w uint64) []*Method {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []*Method
	for _, m := range t.methods {
		if m.visible(w) {
			out = append(out, m)
		}
	}
	return out
}

// visible reports that m is defined in world w.
func (m *Method) visible(w uint64) bool { return m.PrimaryWorld <= w && w <= m.MaxWorld }

// moreSpecific is jl_method_morespecific (subtype.c:5536-5552) for two
// visible methods. Its rule for type-equal signatures (the newer wins)
// cannot apply: Add retires a method when a type-equal one replaces it, so
// two visible methods never have type-equal signatures.
func moreSpecific(a, b *Method) (bool, error) { return core.MoreSpecific(a.Sig, b.Sig) }

// Lookup returns the method a call with argument tuple tt runs in world w,
// and the values of its static parameters. tt is the tuple of the arguments
// (without typeof(f)) as jl_inst_arg_tuple_type builds it (jltypes.c:
// 2477-2502): each argument's type, or Type{a} for an argument a that is
// itself a type. It is usually a dispatch tuple; a type argument with free
// type variables makes it hold a kind instead, which julia also looks up
// through jl_mt_assoc_by_type. The candidates are the methods tt is a
// subtype of.
func (t *Table) Lookup(tt any, w uint64) (*Method, []any, error) {
	if !core.IsTupleType(tt) {
		return nil, nil, fmt.Errorf("dispatch on %s: the argument tuple is not a Tuple type", core.Show(tt))
	}
	// a concrete argument tuple: the cached answer for its world
	leaf, _ := tt.(*core.DataType)
	if leaf == nil || !leaf.IsDispatchTuple {
		return t.lookup(tt, w)
	}
	t.mu.RLock()
	gen := t.changes
	for _, e := range t.leaf[leaf] {
		if t.answers(e, w) {
			t.mu.RUnlock()
			return e.m, append([]any(nil), e.env...), nil
		}
	}
	t.mu.RUnlock()
	m, env, minW, maxW, err := t.lookupML(leaf, w)
	raceHook()
	if err == nil {
		t.mu.Lock()
		if t.leaf == nil {
			t.leaf = map[*core.DataType][]leafEntry{}
		}
		// unless the table changed while ml_matches ran: its range may
		// reach past that change
		if t.changes == gen {
			// the entries no world is left to are dropped here
			kept := t.leaf[leaf][:0]
			for _, e := range t.leaf[leaf] {
				if e.minWorld <= t.last(e) {
					kept = append(kept, e)
				}
			}
			t.leaf[leaf] = append(kept, leafEntry{minW, maxW, m, append([]any(nil), env...), gen})
		}
		t.mu.Unlock()
	}
	return m, env, err
}

// lookupML is jl_mt_assoc_by_type's miss path (gf.c:1848-1877,
// _gf_invoke_lookup gf.c:4217-4230): ml_matches of the argument tuple, one
// match at most; the method when exactly one is found.
func (t *Table) lookupML(tt *core.DataType, w uint64) (*Method, []any, uint64, uint64, error) {
	res, err := t.MlMatches(t.fullSig(tt), 1, false, w, 0, ^uint64(0))
	if err != nil {
		return nil, nil, 0, 0, err
	}
	if res.Nothing || len(res.List) != 1 {
		// none, or more than one with none the most specific: the scan
		// tells a missing method from an ambiguity
		m, _, err := t.lookup(tt, w)
		if err == nil {
			err = fmt.Errorf("dispatch: %s%s in world %d: the scan chose %v where ml_matches found %d methods", t.Name, core.Show(tt), w, m, len(res.List))
		}
		return nil, nil, 0, 0, err
	}
	return res.List[0].Method, res.List[0].Env, res.MinValid, res.MaxValid, nil
}

// lookup is Lookup without its cache.
func (t *Table) lookup(tt any, w uint64) (*Method, []any, error) {
	// one jl_subtype_env query per method gives both the answer and the
	// static parameters the chosen method will need; the methods are read
	// in place and one scratch env serves the ones that do not match
	var cands []*Method
	var envs [][]any
	var scratch []any
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, m := range t.methods {
		if !m.visible(w) {
			continue
		}
		if cap(scratch) < m.nenv {
			scratch = make([]any, m.nenv)
		}
		env := scratch[:m.nenv]
		clear(env)
		sub, err := core.SubtypeEnv(tt, m.Sig, env)
		if err != nil {
			return nil, nil, err
		}
		if sub {
			cands = append(cands, m)
			envs = append(envs, append([]any(nil), env...))
		}
	}
	if len(cands) == 0 {
		return nil, nil, fmt.Errorf("%w %s%s", ErrNoMethod, t.Name, core.Show(tt))
	}
	for ci, c := range cands {
		best := true
		for _, o := range cands {
			if o == c {
				continue
			}
			more, err := moreSpecific(c, o)
			if err != nil {
				return nil, nil, err
			}
			if !more {
				best = false
				break
			}
		}
		if best {
			return c, envs[ci], nil
		}
	}
	return nil, nil, fmt.Errorf("%s%s is %w", t.Name, core.Show(tt), ErrAmbiguous)
}

// Env returns the values of m's static parameters for the argument tuple
// tt, false when tt is not a subtype of m's signature: what invoke needs
// once it has chosen a method by a declared tuple, for the actual
// arguments (jl_gf_invoke_by_method specializes on them).
func (m *Method) Env(tt any) ([]any, bool, error) {
	env := make([]any, envSize(m.Sig))
	sub, err := core.SubtypeEnv(tt, m.Sig, env)
	return env, sub, err
}

// envSize is jl_subtype_env_size: the number of outer UnionAlls of t.
func envSize(t any) int {
	n := 0
	for {
		u, ok := t.(*core.UnionAll)
		if !ok {
			return n
		}
		n++
		t = u.Body
	}
}
