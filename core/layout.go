package core

// What julia decides about a type's layout before it computes one: whether a
// type can reference itself (so cannot be stored inline in its own fields),
// and whether a type with free parameters has a layout all the same.

// referencesName is references_name (builtins.c:2154-2201): whether p
// mentions the type name, where that would make the layout of a type of that
// name depend on itself. affectsLayout says p is reached through a position
// that decides a layout; freevars is a conservative stand-in for
// jl_has_bound_typevars from name.Wrapper.
func referencesName(p any, name *TypeName, affectsLayout, freevars bool) bool {
	if freevars && !HasFreeTypeVars(p) {
		freevars = false
	}
	for {
		ua, ok := p.(*UnionAll)
		if !ok {
			break
		}
		if referencesName(ua.Var.LB, name, false, freevars) || referencesName(ua.Var.UB, name, false, freevars) {
			return true
		}
		p = ua.Body
	}
	switch x := p.(type) {
	case *Union:
		return referencesName(x.A, name, affectsLayout, freevars) || referencesName(x.B, name, affectsLayout, freevars)
	case *Vararg:
		return x.T != nil && referencesName(x.T, name, affectsLayout, freevars) ||
			x.N != nil && referencesName(x.N, name, affectsLayout, freevars)
	case *DataType:
		if affectsLayout && x.Name == name {
			return true
		}
		prim := x.Name.Primary
		affectsLayout = isGenericMemoryName(x.Name) || wrapperLayoutIsNull(prim)
		// and even with a layout, the fields might trigger layouts by using a
		// parameter; assumed for all of them
		if !affectsLayout && freevars && len(x.Name.FieldNames) > 0 {
			for _, ft := range prim.Name.Fields {
				if _, isVar := ft.(*TypeVar); !isVar && HasFreeTypeVars(ft) {
					affectsLayout = true
					break
				}
			}
		}
		for _, q := range x.Parameters {
			if referencesName(q, name, affectsLayout, freevars) {
				return true
			}
		}
	}
	return false
}

// wrapperLayoutIsNull is `jl_unwrap_unionall(name->wrapper)->layout == NULL`:
// the primary type has no layout, being abstract, still being defined, or
// parametric with a layout that depends on its parameters.
func wrapperLayoutIsNull(prim *DataType) bool {
	if prim.Name.Abstract {
		return true
	}
	if prim.Name.Bits > 0 {
		return false
	}
	if _, ok := prim.FieldTypes(); !ok {
		return true
	}
	return !prim.IsConcrete && !HasFixedLayout(prim)
}

// HasFixedLayout is jl_has_fixed_layout (jltypes.c:293-319): whether a type
// that may have free type variables has a layout all the same. A concrete
// type has one (julia's first case), and so does one whose layout is
// computed; d is the body of a UnionAll otherwise.
func HasFixedLayout(d *DataType) bool {
	switch {
	case d.IsConcrete:
		return true
	case isGenericMemoryName(d.Name):
		// GenericMemory uses its element type for the layout, a parameter
		return false
	case d.Name.Abstract, d.Name == TupleTypeName:
		return false
	case d.Name == NamedTupleTypeName:
		return !layoutUsesFreeTypevars(d.Parameters[0], nil) && !layoutUsesFreeTypevars(d.Parameters[1], nil)
	}
	ft, _ := d.FieldTypes()
	for _, f := range ft {
		if layoutUsesFreeTypevars(f, nil) {
			return false
		}
	}
	return true
}

// layoutUsesFreeTypevars is layout_uses_free_typevars (jltypes.c:52-106),
// env the variables bound around v.
func layoutUsesFreeTypevars(v any, env []*TypeVar) bool {
	for {
		if tv, ok := v.(*TypeVar); ok {
			for _, b := range env {
				if b == tv {
					return false
				}
			}
			return true
		}
		for {
			ua, ok := v.(*UnionAll)
			if !ok {
				break
			}
			env = append(env, ua.Var)
			v = ua.Body
		}
		switch x := v.(type) {
		case *DataType:
			switch {
			case x.IsConcrete, !x.Name.MayInlineAlloc:
				return false
			case x.Name == NamedTupleTypeName:
				return layoutUsesFreeTypevars(x.Parameters[0], env) || layoutUsesFreeTypevars(x.Parameters[1], env)
			case x.Name == TupleTypeName:
				return true // conservative, as julia
			}
			ft, _ := x.FieldTypes()
			for _, f := range ft {
				if layoutUsesFreeTypevars(f, env) {
					return true
				}
			}
			return false
		case *Union:
			if layoutUsesFreeTypevars(x.A, env) {
				return true
			}
			v = x.B
		default:
			// julia also follows a Vararg's T and N; a Vararg is only ever
			// a Tuple's last parameter, and a Tuple is answered above
			return false
		}
	}
}

// isGenericMemoryName is jl_is_genericmemory_type of a type with this name.
func isGenericMemoryName(n *TypeName) bool {
	return n == GenericMemoryType.Body.(*UnionAll).Body.(*UnionAll).Body.(*DataType).Name
}
