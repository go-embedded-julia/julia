// Package dispatch is the method table of a generic function and the
// choice of the method a call runs, after src/gf.c of julia 1.12.6.
//
// This first version handles calls whose argument types are concrete, the
// case the interpreter meets at run time: the applicable methods are those
// whose signature the argument tuple is a subtype of (gf.c ml_matches with
// a dispatch tuple), and the call runs the one that is more specific than
// every other (jl_method_morespecific, subtype.c:5536-5552); when none is,
// the call is ambiguous. julia 1.12 orders the matches through the
// interference sets it computes when a method is inserted (gf.c:4487-4589),
// which need type intersection; they are not ported yet, and the
// differential test against julia's `which` measures what that changes.
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
}

// Table is a generic function's method table (jl_methtable_t).
type Table struct {
	Name string

	mu      sync.RWMutex
	world   uint64
	methods []*Method
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
// replaced, as in Add.
func (t *Table) Define(sig any, w uint64, def any) (*Method, error) {
	if !core.IsTupleType(core.UnwrapUnionAll(sig)) {
		return nil, fmt.Errorf("method signature %s is not a Tuple type", core.Show(sig))
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if w <= t.world {
		return nil, fmt.Errorf("method of %s defined in world %d, not newer than world %d", t.Name, w, t.world)
	}
	t.world = w
	for _, m := range t.methods {
		if m.MaxWorld == ^uint64(0) && core.TypesEqual(m.Sig, sig) {
			m.MaxWorld = w - 1
		}
	}
	m := &Method{Sig: sig, PrimaryWorld: w, MaxWorld: ^uint64(0), Index: len(t.methods), Def: def}
	t.methods = append(t.methods, m)
	return m, nil
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
	// one jl_subtype_env query per method gives both the answer and the
	// static parameters the chosen method will need
	var cands []*Method
	var envs [][]any
	for _, m := range t.Methods(w) {
		env := make([]any, envSize(m.Sig))
		sub, err := core.SubtypeEnv(tt, m.Sig, env)
		if err != nil {
			return nil, nil, err
		}
		if sub {
			cands = append(cands, m)
			envs = append(envs, env)
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
