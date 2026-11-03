package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
)

// Method reflection: the Method, MethodInstance and MethodMatch objects
// julia code sees (jltypes.c:3537-3643, 3717-3720), and the lookups that
// make them, jl_matching_methods (gf.c:3113-3127, ml_matches 4602-4900)
// and jl_specializations_get_linfo (gf.c:268). The VM dispatches on its
// own tables (core/dispatch); a Method is made for a table's method when
// reflection first asks for it, and is the same object every time.

// methodFields are Method's fields and types (jltypes.c:3540-3607).
var methodFields = []struct {
	name expr.Symbol
	typ  func(vm *VM) any
}{
	{"name", fixed(core.SymbolType)}, {"module", fixed(core.ModuleType)}, {"file", fixed(core.SymbolType)},
	{"line", fixed(core.Int32Type)}, {"dispatch_status", fixed(core.UInt8Type)},
	{"interferences", func(vm *VM) any { return memAnyType }}, {"primary_world", fixed(core.UInt64Type)},
	{"sig", fixed(core.TypeType)}, {"specializations", fixed(core.AnyType)},
	{"speckeyset", func(vm *VM) any { return core.GenericMemoryType }}, {"slot_syms", fixed(core.StringType)},
	{"external_mt", fixed(core.AnyType)}, {"source", fixed(core.AnyType)},
	{"debuginfo", func(vm *VM) any { return vm.Core.own("DebugInfo").Value }}, {"unspecialized", fixed(core.AnyType)},
	{"generator", fixed(core.AnyType)}, {"roots", func(vm *VM) any { return vecAnyType }},
	{"root_blocks", func(vm *VM) any { return must(core.Apply(core.ArrayType, core.UInt64Type, int64(1))) }},
	{"nroots_sysimg", fixed(core.Int32Type)}, {"ccallable", fixed(core.SimpleVectorType)},
	{"invokes", fixed(core.AnyType)}, {"recursion_relation", fixed(core.AnyType)}, {"nargs", fixed(core.Int32Type)},
	{"called", fixed(core.Int32Type)}, {"nospecialize", fixed(core.Int32Type)}, {"nkw", fixed(core.Int32Type)},
	{"isva", fixed(core.BoolType)}, {"is_for_opaque_closure", fixed(core.BoolType)},
	{"nospecializeinfer", fixed(core.BoolType)}, {"did_scan_source", fixed(core.UInt8Type)},
	{"constprop", fixed(core.UInt8Type)}, {"max_varargs", fixed(core.UInt8Type)}, {"purity", fixed(core.UInt16Type)},
}

func fixed(t any) func(*VM) any { return func(*VM) any { return t } }

// marked is a field mask of n fields with the given ones set.
func marked(n int, set ...int) []bool {
	m := make([]bool, n)
	for _, i := range set {
		m[i] = true
	}
	return m
}

// bootReflectionTypes defines Method, MethodInstance and MethodMatch with
// julia's layouts, their atomic fields marked (jltypes.c:3612-3613, 3640-
// 3643).
func (vm *VM) bootReflectionTypes() {
	names := make([]expr.Symbol, len(methodFields))
	types := make([]any, len(methodFields))
	for i, f := range methodFields {
		names[i], types[i] = f.name, f.typ(vm)
	}
	vm.tMethod = must(core.NewType("Core", "Method", core.AnyType, false, true, names, types))
	vm.tMethod.Name.AtomicFields = marked(len(names), 4, 5, 6, 29)
	vm.setCore("Method", vm.tMethod)
	vm.tMethodInstance = must(core.NewType("Core", "MethodInstance", core.AnyType, false, true,
		[]expr.Symbol{"def", "specTypes", "sparam_vals", "backedges", "cache", "cache_with_orig", "flags", "dispatch_status"},
		[]any{core.TypeUnion(vm.tMethod, core.ModuleType), core.AnyType, core.SimpleVectorType, vecAnyType,
			core.AnyType, core.BoolType, core.BoolType, core.UInt8Type}))
	vm.tMethodInstance.Name.AtomicFields = marked(8, 4, 6, 7)
	vm.setCore("MethodInstance", vm.tMethodInstance)
	vm.tMethodMatch = must(core.NewType("Core", "MethodMatch", core.AnyType, false, false,
		[]expr.Symbol{"spec_types", "sparams", "method", "fully_covers"},
		[]any{core.TypeType, core.SimpleVectorType, vm.tMethod, core.BoolType}))
	vm.setCore("MethodMatch", vm.tMethodMatch)
	// the inference lattice's types the runtime defines (jltypes.c:3702-
	// 3715, 3834-3837)
	for _, t := range []struct {
		name   string
		fields []expr.Symbol
		types  []any
	}{
		{"Const", []expr.Symbol{"val"}, []any{core.AnyType}},
		{"PartialStruct", []expr.Symbol{"typ", "fields"}, []any{core.AnyType, vecAnyType}},
		{"InterConditional", []expr.Symbol{"slot", "thentype", "elsetype"}, []any{core.Int64Type, core.AnyType, core.AnyType}},
		{"PartialOpaque", []expr.Symbol{"typ", "env", "parent", "source"}, []any{core.TypeType, core.AnyType, vm.tMethodInstance, core.AnyType}},
	} {
		vm.setCore(expr.Symbol(t.name), must(core.NewType("Core", t.name, core.AnyType, false, false, t.fields, t.types)))
	}
}

// reflected is what reflection made of one of a table's methods.
type reflected struct {
	method *Struct
	specs  []*Struct // its MethodInstances
	m      *dispatch.Method
	tab    *dispatch.Table
}

// methodTableDisable is jl_method_table_disable (gf.c:2585-2606),
// Base.delete_method: the method invisible after the current world, then
// a new world.
func (vm *VM) methodTableDisable(a []any) (any, error) {
	ms, ok := a[0].(*Struct)
	if !ok || ms.Type != vm.tMethod {
		return nil, vm.typeError("ccall", "", vm.tMethod, a[0])
	}
	r := vm.reflectedOf[ms]
	if !r.tab.Disable(r.m, vm.World()) {
		return nil, vm.errorf("Method of %s already disabled", ms.Fields[0])
	}
	vm.newWorld()
	return nil, nil
}

// fullSig is m's signature as julia keeps it, typeof(f) first: a generic
// function's table leaves it out (ft), the other tables keep it.
func fullSig(sig, ft any) any {
	if ft == nil {
		return sig
	}
	body := core.UnwrapUnionAll(sig).(*core.DataType)
	params := append([]any{ft}, body.Parameters...)
	return core.RewrapUnionAll(must(core.Apply(core.AnyTupleType, params...)), sig)
}

// methodObject is the Method of m, of the table whose function's type is
// ft (nil for a table that keeps it), made on first use as jl_method_def
// and jl_method_set_source fill one in (method.c:983-1023, 1191-1324).
// Fields set from the parts of a method this VM does not keep (the
// specializations cache, roots, nospecialize and nkw from the body's
// meta) hold jl_new_method_uninit's defaults.
func (vm *VM) methodObject(tab *dispatch.Table, m *dispatch.Method, ft any, name expr.Symbol) *Struct {
	if r := vm.reflected[m]; r != nil {
		return r.method
	}
	f := make([]any, len(methodFields))
	for i := range f {
		f[i] = undef
	}
	sig := fullSig(m.Sig, ft)
	params := core.UnwrapUnionAll(sig).(*core.DataType).Parameters
	f[0], f[1], f[2], f[3] = name, vm.Main, expr.Symbol(""), int32(0)
	f[4], f[5], f[6], f[7] = uint8(0), &Memory{Type: memAnyType}, m.PrimaryWorld, sig
	f[8], f[9] = SVec{}, &Memory{Type: memAnyType}
	f[18], f[20], f[22], f[23], f[24], f[25] = int32(0), nil, int32(len(params)), int32(0xff), int32(0), int32(0)
	f[26] = len(params) > 0 && isVararg(params[len(params)-1])
	f[27], f[28], f[29], f[30], f[31], f[32] = false, false, uint8(0), uint8(0), uint8(0xff), uint16(0)
	if lam, ok := m.Def.(*lambda); ok {
		f[1], f[2], f[3] = lam.mod, lam.file, lam.line
		src := lam.c.src
		f[10] = compressArgnames(src.SlotNames)
		f[12], f[13] = src, src.DebugInfo
		f[28], f[30], f[32] = src.NoSpecializeInfer, src.Constprop, src.Purity
	}
	s := &Struct{Type: vm.tMethod, Fields: f}
	r := &reflected{method: s, m: m, tab: tab}
	vm.reflected[m], vm.reflectedOf[s] = r, r
	return s
}

func isVararg(v any) bool { _, ok := v.(*core.Vararg); return ok }

// compressArgnames is jl_compress_argnames (ircode.c:1255-1277): the slot
// names, each ended by a NUL, in one String.
func compressArgnames(names expr.Vector) string {
	var b []byte
	for _, n := range names {
		s, _ := n.(expr.Symbol)
		b = append(append(b, s...), 0)
	}
	return string(b)
}

// methodTableOf is the table a query's function type ft selects, the
// function's type when the table leaves it out, and the methods' name
// (method.c:1236-1244); false for a callee no one table holds.
func (vm *VM) methodTableOf(ft any) (*dispatch.Table, any, expr.Symbol, bool) {
	if d, ok := ft.(*core.DataType); ok && vm.funcs[d] != nil {
		return vm.funcs[d].Table, d, vm.funcs[d].Name, true
	}
	if name := ctorTypeName(ft); name != nil && vm.ctors[name] != nil {
		return vm.ctors[name], nil, expr.Symbol(name.Name), true
	}
	// the table methodDef puts any other callee in, which keeps the
	// callee's type first; only for one callee type, a type's Type{…} or a
	// concrete callable, as the other callees' methods are elsewhere
	if name := ctorTypeName(ft); name != nil {
		return vm.others, nil, expr.Symbol(name.Name), true
	}
	if d, ok := ft.(*core.DataType); ok && d.IsConcrete {
		return vm.others, nil, expr.Symbol(d.Name.Name), true
	}
	return nil, nil, "", false
}

// match is one MethodMatch in the making.
type match struct {
	m           *dispatch.Method
	sig, ti     any
	env         SVec
	fullyCovers bool
}

// matchingMethods is jl_matching_methods(types, mt, lim, include_ambiguous,
// world, min_valid, max_valid, ambig) where subtyping decides the
// intersection of the query with each signature: the signature is a
// subtype of the query, or the query of the signature, or the two are
// obviously disjoint. In order, most specific first, a match the
// fully-covering most specific one dominates left out (the minmax rule,
// gf.c:4745-4838; check_interferences_covers, gf.c:2662-2692, leaves out
// nothing more here). Two matches neither
// more specific than the other are ordered by julia's typemap, which is not
// ported: they are refused, as is any intersection subtyping cannot decide.
func (vm *VM) matchingMethods(a []any) (any, error) {
	tt, lim, world := a[0], a[2], a[4]
	n, ok1 := lim.(int32)
	w, ok2 := world.(uint64)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("%w: jl_matching_methods of %s", ErrUnsupported, showValue(vm.newTuple(a)))
	}
	if err := vm.storeThrough("jl_matching_methods", a[7], core.Int32Type, int32(0)); err != nil {
		return nil, err
	}
	empty := vm.newVector(core.AnyType, nil)
	unw, ok := core.UnwrapUnionAll(tt).(*core.DataType)
	if !ok || !core.IsTupleType(unw) || len(unw.Parameters) == 0 || unw.Parameters[0] == any(core.Bottom) {
		return empty, nil
	}
	if w > vm.World() {
		return nil, nil // the future is not enumerable (gf.c:4607-4608)
	}
	if a[1] != nil {
		return nil, fmt.Errorf("%w: jl_matching_methods in a method table of its own", ErrUnsupported)
	}
	tab, ft, name, ok := vm.methodTableOf(unw.Parameters[0])
	if !ok {
		return nil, fmt.Errorf("%w: jl_matching_methods of the methods of %s", ErrUnsupported, showValue(unw.Parameters[0]))
	}
	minValid, maxValid := uint64(0), ^uint64(0)
	var ms []match
	for _, m := range tab.Methods(w) {
		sig := fullSig(m.Sig, ft)
		mt := match{m: m, sig: sig}
		env := make([]any, envSize(sig))
		// past the work bound, an error, never a panic
		sub, err := core.SubtypeEnv(tt, sig, env)
		within := false
		if err == nil && !sub {
			within, err = core.SubtypeChecked(sig, tt)
		}
		if err != nil {
			return nil, err
		}
		switch {
		case sub:
			mt.ti, mt.env, mt.fullyCovers = tt, SVec(env), true
		case within:
			mt.ti, mt.env = sig, typeVarsOf(sig)
		case core.ObviouslyDisjoint(tt, sig):
			continue
		default:
			return nil, fmt.Errorf("%w: jl_matching_methods: the intersection of %s and %s (type intersection)", ErrUnsupported, showValue(tt), showValue(sig))
		}
		minValid = max(minValid, m.PrimaryWorld)
		maxValid = min(maxValid, m.MaxWorld)
		ms = append(ms, mt)
	}
	ms, err := orderMatches(ms)
	if err != nil {
		return nil, err
	}

	if n >= 0 && len(ms) > int(n) {
		return nil, nil
	}
	if err := vm.storeThrough("jl_matching_methods", a[5], core.UInt64Type, minValid); err != nil {
		return nil, err
	}
	if err := vm.storeThrough("jl_matching_methods", a[6], core.UInt64Type, maxValid); err != nil {
		return nil, err
	}
	out := make([]any, len(ms))
	for i, mt := range ms {
		out[i] = &Struct{Type: vm.tMethodMatch, Fields: []any{mt.ti, mt.env, vm.methodObject(tab, mt.m, ft, name), mt.fullyCovers}}
	}
	return vm.newVector(core.AnyType, out), nil
}

// gfInvokeLookupWorlds is jl_gf_invoke_lookup_worlds(types, mt, world,
// min_world, max_world) (gf.c:4217-4254): ml_matches with intersections
// off, so only the methods whose signature covers the query count, and
// the answer is the one more specific than all the others (the minmax,
// gf.c:4755-4775), nothing when there is none or no unique one.
func (vm *VM) gfInvokeLookupWorlds(a []any) (any, error) {
	w, ok := a[2].(uint64)
	if !ok {
		return nil, fmt.Errorf("%w: jl_gf_invoke_lookup_worlds of %s", ErrUnsupported, showValue(vm.newTuple(a)))
	}
	if a[1] != nil {
		return nil, fmt.Errorf("%w: jl_gf_invoke_lookup_worlds in a method table of its own", ErrUnsupported)
	}
	tt := a[0]
	unw, ok := core.UnwrapUnionAll(tt).(*core.DataType)
	if !ok || !core.IsTupleType(unw) || len(unw.Parameters) == 0 || unw.Parameters[0] == any(core.Bottom) || w > vm.World() {
		return nil, nil
	}
	tab, ft, name, ok := vm.methodTableOf(unw.Parameters[0])
	if !ok {
		return nil, fmt.Errorf("%w: jl_gf_invoke_lookup_worlds of the methods of %s", ErrUnsupported, showValue(unw.Parameters[0]))
	}
	var covering []match
	minValid, maxValid := uint64(0), ^uint64(0)
	for _, m := range tab.Methods(w) {
		sig := fullSig(m.Sig, ft)
		env := make([]any, envSize(sig))
		sub, err := core.SubtypeEnv(tt, sig, env)
		if err != nil {
			return nil, err
		}
		if sub {
			covering = append(covering, match{m: m, sig: sig, ti: tt, env: SVec(env), fullyCovers: true})
			minValid, maxValid = max(minValid, m.PrimaryWorld), min(maxValid, m.MaxWorld)
		}
	}
	i, err := mostSpecific(covering)
	if err != nil || i < 0 {
		return nil, err
	}
	if err := vm.storeThrough("jl_gf_invoke_lookup_worlds", a[3], core.UInt64Type, minValid); err != nil {
		return nil, err
	}
	if err := vm.storeThrough("jl_gf_invoke_lookup_worlds", a[4], core.UInt64Type, maxValid); err != nil {
		return nil, err
	}
	c := covering[i]
	return &Struct{Type: vm.tMethodMatch, Fields: []any{c.ti, c.env, vm.methodObject(tab, c.m, ft, name), true}}, nil
}

// mostSpecific is the index of the match more specific than every other,
// -1 when none is.
func mostSpecific(ms []match) (int, error) {
	for i, c := range ms {
		best := true
		for j, o := range ms {
			if i == j {
				continue
			}
			more, err := core.MoreSpecific(c.sig, o.sig)
			if err != nil {
				return -1, err
			}
			if !more {
				best = false
				break
			}
		}
		if best {
			return i, nil
		}
	}
	return -1, nil
}

// orderMatches sorts the matches most specific first and leaves out the
// dominated ones; matches neither more specific than the other are refused.
func orderMatches(ms []match) ([]match, error) {
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0; j-- {
			before, err := core.MoreSpecific(ms[j].sig, ms[j-1].sig)
			after := false
			if err == nil {
				after, err = core.MoreSpecific(ms[j-1].sig, ms[j].sig)
			}
			if err != nil {
				return nil, err
			}
			if before == after {
				return nil, fmt.Errorf("%w: jl_matching_methods: the order of %s and %s, neither more specific (julia's typemap order)", ErrUnsupported, core.Show(ms[j-1].sig), core.Show(ms[j].sig))
			}
			if !before {
				break
			}
			ms[j], ms[j-1] = ms[j-1], ms[j]
		}
	}
	// the first fully-covering match dominates the rest: a later match
	// another covers would have the query inside both, so that other
	// fully covers too (the minmax rule; check_interferences_covers adds
	// nothing where subtyping decided the intersections)
	for i, mt := range ms {
		if mt.fullyCovers {
			return ms[:i+1], nil
		}
	}
	return ms, nil
}

// typeVarsOf is the variables of a signature's UnionAlls, outermost first.
func typeVarsOf(t any) SVec {
	var vs SVec
	for {
		u, ok := t.(*core.UnionAll)
		if !ok {
			return vs
		}
		vs = append(vs, u.Var)
		t = u.Body
	}
}

// envSize is the number of UnionAlls around t.
func envSize(t any) int { return len(typeVarsOf(t)) }

// specializationsGetLinfo is jl_specializations_get_linfo(m, type,
// sparams) (gf.c:268): the MethodInstance of the Method m for these
// argument types, the same one for types egal to an earlier call's.
func (vm *VM) specializationsGetLinfo(a []any) (any, error) {
	ms, ok := a[0].(*Struct)
	if !ok || ms.Type != vm.tMethod {
		return nil, vm.typeError("ccall", "", vm.tMethod, a[0])
	}
	sp, ok := a[2].(SVec)
	if !ok {
		return nil, vm.typeError("ccall", "", core.SimpleVectorType, a[2])
	}
	r := vm.reflectedOf[ms] // every Method is methodObject's
	for _, mi := range r.specs {
		if vm.egal(mi.Fields[1], a[1]) {
			return mi, nil
		}
	}
	mi := &Struct{Type: vm.tMethodInstance, Fields: []any{ms, a[1], sp, undef, undef, false, false, uint8(0)}}
	r.specs = append(r.specs, mi)
	return mi, nil
}

// storeThrough writes v, of type t, through a pointer made from a Ref of
// that type (refvalue.jl:38-54): C's *p = v; C_NULL wants no result.
func (vm *VM) storeThrough(fname string, p any, t *core.DataType, v any) error {
	ptr, ok := p.(*Pointer)
	if !ok {
		return fmt.Errorf("%w: %s's result through %s", ErrUnsupported, fname, showValue(p))
	}
	if ptr.Target == nil && ptr.Off == 0 {
		return nil
	}
	s, ok := ptr.Target.(*Struct)
	if !ok || ptr.Off != 0 || !s.Type.Name.Mutable || len(s.Fields) == 0 {
		return fmt.Errorf("%w: %s's result through %s", ErrUnsupported, fname, showValue(p))
	}
	if ft, _ := s.Type.FieldTypes(); len(ft) == 0 || ft[0] != any(t) {
		return fmt.Errorf("%w: %s's result through %s", ErrUnsupported, fname, showValue(p))
	}
	s.Fields[0] = v
	return nil
}
