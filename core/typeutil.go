package core

// typeEnv is jl_typeenv_t: a linked list binding type variables to values.
type typeEnv struct {
	v    *TypeVar
	val  any
	prev *typeEnv
}

func (e *typeEnv) has(v *TypeVar) bool {
	for ; e != nil; e = e.prev {
		if e.v == v {
			return true
		}
	}
	return false
}

// hasNE is typeenv_has_ne: present and not bound to itself
// (jltypes.c:41-49).
func (e *typeEnv) hasNE(v *TypeVar) bool {
	for ; e != nil; e = e.prev {
		if e.v == v {
			return e.val != any(v)
		}
	}
	return false
}

// hasFreeTypeVars is has_free_typevars (jltypes.c:109-156).
func hasFreeTypeVars(v any, env *typeEnv) bool {
	for {
		if tv, ok := v.(*TypeVar); ok {
			return !env.has(tv)
		}
		for {
			ua, ok := v.(*UnionAll)
			if !ok {
				break
			}
			if ua.Var.LB != any(Bottom) && hasFreeTypeVars(ua.Var.LB, env) {
				return true
			}
			if ua.Var.UB != any(AnyType) && hasFreeTypeVars(ua.Var.UB, env) {
				return true
			}
			env = &typeEnv{ua.Var, nil, env}
			v = ua.Body
		}
		switch x := v.(type) {
		case *DataType:
			if !x.HasFreeTypeVars || env == nil {
				return x.HasFreeTypeVars
			}
			for _, p := range x.Parameters {
				if hasFreeTypeVars(p, env) {
					return true
				}
			}
			return false
		case *Union:
			if hasFreeTypeVars(x.A, env) {
				return true
			}
			v = x.B
		case *Vararg:
			if x.T == nil {
				return false
			}
			if x.N != nil && hasFreeTypeVars(x.N, env) {
				return true
			}
			v = x.T
		default:
			return false
		}
	}
}

// HasFreeTypeVars is jl_has_free_typevars.
func HasFreeTypeVars(v any) bool { return hasFreeTypeVars(v, nil) }

// findFreeTypeVars is find_free_typevars (jltypes.c:163-208).
func findFreeTypeVars(v any, env *typeEnv, out *[]*TypeVar) {
	for {
		if tv, ok := v.(*TypeVar); ok {
			if !env.has(tv) {
				*out = append(*out, tv)
			}
			return
		}
		for {
			ua, ok := v.(*UnionAll)
			if !ok {
				break
			}
			if ua.Var.LB != any(Bottom) {
				findFreeTypeVars(ua.Var.LB, env, out)
			}
			if ua.Var.UB != any(AnyType) {
				findFreeTypeVars(ua.Var.UB, env, out)
			}
			env = &typeEnv{ua.Var, nil, env}
			v = ua.Body
		}
		switch x := v.(type) {
		case *DataType:
			if !x.HasFreeTypeVars {
				return
			}
			for _, p := range x.Parameters {
				findFreeTypeVars(p, env, out)
			}
			return
		case *Union:
			findFreeTypeVars(x.A, env, out)
			v = x.B
		case *Vararg:
			if x.T == nil {
				return
			}
			if x.N != nil {
				findFreeTypeVars(x.N, env, out)
			}
			v = x.T
		default:
			return
		}
	}
}

// hasBoundTypeVars is jl_has_bound_typevars (jltypes.c:220-271).
func hasBoundTypeVars(v any, env *typeEnv) bool {
	for {
		if tv, ok := v.(*TypeVar); ok {
			return env.hasNE(tv)
		}
		for {
			ua, ok := v.(*UnionAll)
			if !ok {
				break
			}
			if ua.Var.LB != any(Bottom) && hasBoundTypeVars(ua.Var.LB, env) {
				return true
			}
			if ua.Var.UB != any(AnyType) && hasBoundTypeVars(ua.Var.UB, env) {
				return true
			}
			if env.hasNE(ua.Var) {
				env = &typeEnv{ua.Var, ua.Var, env}
			}
			v = ua.Body
		}
		switch x := v.(type) {
		case *DataType:
			if !x.HasFreeTypeVars {
				return false
			}
			for _, p := range x.Parameters {
				if hasBoundTypeVars(p, env) {
					return true
				}
			}
			return false
		case *Union:
			if hasBoundTypeVars(x.A, env) {
				return true
			}
			v = x.B
		case *Vararg:
			if x.T == nil {
				return false
			}
			if x.N != nil && hasBoundTypeVars(x.N, env) {
				return true
			}
			v = x.T
		default:
			return false
		}
	}
}

// HasTypeVar is jl_has_typevar: t mentions v free.
func HasTypeVar(t any, v *TypeVar) bool {
	return hasBoundTypeVars(t, &typeEnv{v, nil, nil})
}

// Egal is jl_egal (===) on the values this package handles
// (builtins.c:243-299, julia.h:1869-1889).
func Egal(a, b any) bool {
	if a == b {
		return true
	}
	switch x := a.(type) {
	case *DataType:
		y, ok := b.(*DataType)
		if !ok || x.Name != y.Name {
			return false
		}
		if x.Name != TupleTypeName && (x.IsConcrete || y.IsConcrete) {
			return false
		}
		return egalSlices(x.Parameters, y.Parameters)
	case *Union:
		y, ok := b.(*Union)
		return ok && Egal(x.A, y.A) && Egal(x.B, y.B)
	case *UnionAll:
		y, ok := b.(*UnionAll)
		return ok && egalTypes(x, y, nil, true)
	case *Vararg:
		y, ok := b.(*Vararg)
		return ok && egalOpt(x.T, y.T) && egalOpt(x.N, y.N)
	case *TypeVar, *bottomType:
		return false
	case *Instance:
		y, ok := b.(*Instance)
		return ok && x.Type == y.Type && x.Bits == y.Bits
	}
	return false
}

func egalOpt(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return Egal(a, b)
}

func egalSlices(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !Egal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// egalTypes is egal_types (builtins.c:167-224): structural equality,
// matching bound variables pairwise.
func egalTypes(a, b any, env *typeEnv, tvarNames bool) bool {
	if a == b {
		return true
	}
	switch x := a.(type) {
	case *DataType:
		y, ok := b.(*DataType)
		if !ok || x.Name != y.Name || len(x.Parameters) != len(y.Parameters) {
			return false
		}
		for i := range x.Parameters {
			if !egalTypes(x.Parameters[i], y.Parameters[i], env, tvarNames) {
				return false
			}
		}
		return true
	case *TypeVar:
		if _, ok := b.(*TypeVar); !ok {
			return false
		}
		for pe := env; pe != nil; pe = pe.prev {
			if pe.v == x {
				return pe.val == b
			}
		}
		return false
	case *UnionAll:
		y, ok := b.(*UnionAll)
		if !ok {
			return false
		}
		if tvarNames && x.Var.Name != y.Var.Name {
			return false
		}
		if !(egalTypes(x.Var.LB, y.Var.LB, env, tvarNames) && egalTypes(x.Var.UB, y.Var.UB, env, tvarNames)) {
			return false
		}
		return egalTypes(x.Body, y.Body, &typeEnv{x.Var, y.Var, env}, tvarNames)
	case *Union:
		y, ok := b.(*Union)
		return ok && egalTypes(x.A, y.A, env, tvarNames) && egalTypes(x.B, y.B, env, tvarNames)
	case *Vararg:
		y, ok := b.(*Vararg)
		if !ok {
			return false
		}
		if !egalTypes(unwrapVararg(x), unwrapVararg(y), env, tvarNames) {
			return false
		}
		if x.N != nil && y.N != nil {
			return egalTypes(x.N, y.N, env, tvarNames)
		}
		return x.N == nil && y.N == nil
	}
	return Egal(a, b)
}

// TypesEgal is jl_types_egal (builtins.c:226-229).
func TypesEgal(a, b any) bool { return egalTypes(a, b, nil, false) }
