package interp

import (
	"github.com/go-embedded-julia/julia/core"
)

// The flags Base reads off a DataType and a TypeName (runtime_internals.jl:
// isconcretetype, isbitstype, ismutable…), in julia.h's bit order
// (julia.h:615-625 for a DataType, 540-543 for a TypeName).

// dataTypeFlags is a DataType's flags field.
func dataTypeFlags(d *core.DataType) uint16 {
	var f uint16
	set := func(bit uint, on bool) {
		if on {
			f |= 1 << bit
		}
	}
	isbits := d.IsBits()
	set(0, d.HasFreeTypeVars)
	set(1, d.IsConcrete)
	set(2, d.IsDispatchTuple)
	set(3, isbits)
	set(4, zeroinit(d))
	set(5, d.HasConcreteSubtype)
	set(6, d.MaybeSubtypeOfCache)
	set(7, d.Name.Bits > 0)
	set(8, mutationFree(d, map[*core.DataType]bool{}))
	set(9, identityFree(d, map[*core.DataType]bool{}))
	return f | smallTag(d)<<10
}

// smallTag is a builtin type's small typeof tag (julia.h:925-968, the
// order of JL_SMALL_TYPEOF from 1), 0 for any other type.
func smallTag(d *core.DataType) uint16 {
	for i, t := range []*core.DataType{core.TypeofBottomType, core.DataTypeType, core.UnionAllType,
		core.UnionType, core.TypeofVarargType, core.TypeVarType, core.SymbolType, core.ModuleType,
		core.SimpleVectorType, core.StringType, nil, core.BoolType, core.CharType, core.Int16Type,
		core.Int32Type, core.Int64Type, core.Int8Type, core.UInt16Type, core.UInt32Type, core.UInt64Type,
		core.UInt8Type} {
		// the Task type is made after core: matched by name
		if t == d || t == nil && d.Name.Module == "Core" && d.Name.Name == "Task" {
			return uint16(i + 1)
		}
	}
	return 0
}

// zeroinit is set for a concrete struct with a field julia stores as a
// pointer, which must start as NULL (jl_compute_field_offsets).
func zeroinit(d *core.DataType) bool {
	if !d.IsConcrete || d.IsBits() {
		return false
	}
	ft, ok := d.FieldTypes()
	if !ok {
		return false
	}
	for _, f := range ft {
		if fd, ok := f.(*core.DataType); !ok || !fd.IsBits() {
			return true
		}
	}
	return false
}

// typeNameFlags is a TypeName's flags field: abstract, mutable,
// mayinlinealloc (an immutable concrete type's instances may be stored
// inline).
func typeNameFlags(tn *core.TypeName) uint8 {
	var f uint8
	if tn.Abstract {
		f |= 1
	}
	if tn.Mutable {
		f |= 2
	}
	if !tn.Abstract && !tn.Mutable {
		f |= 4
	}
	return f
}

// mutationFree is is_type_mutationfree (datatype.c:482-494) over the
// ismutationfree of jl_compute_field_offsets (datatype.c:648-702) and the
// types jltypes.c sets by hand (:3116-3127, 3336-3346, 3882-3896). A type
// reached again through its own fields is not yet computed, 0 in julia.
func mutationFree(t any, seen map[*core.DataType]bool) bool {
	switch x := core.UnwrapUnionAll(t).(type) {
	case *core.Union:
		return mutationFree(x.A, seen) && mutationFree(x.B, seen)
	case *core.DataType:
		switch x {
		case core.StringType, core.SymbolType, core.TypeNameType, core.DataTypeType,
			core.UnionType, core.UnionAllType, core.ModuleType, core.TypeofVarargType:
			return true
		}
		switch x.Name {
		case core.TypeType.Body.(*core.DataType).Name:
			return true
		case memoryName(core.GenericMemoryType), memoryName(core.GenericMemoryRefType):
			return false
		}
		return fieldsFree(x, seen, true)
	}
	return false
}

// identityFree is is_type_identityfree (datatype.c:496-508) over
// isidentityfree (datatype.c:649-703, jltypes.c:3882-3896).
func identityFree(t any, seen map[*core.DataType]bool) bool {
	switch x := core.UnwrapUnionAll(t).(type) {
	case *core.Union:
		return identityFree(x.A, seen) && identityFree(x.B, seen)
	case *core.DataType:
		switch x {
		case core.StringType, core.SymbolType, core.SimpleVectorType, core.ModuleType:
			return true
		}
		return fieldsFree(x, seen, false)
	}
	return false
}

// fieldsFree is the loop of datatype.c:699-703: an immutable concrete
// type whose every field type is free (of mutation, or of identity).
func fieldsFree(d *core.DataType, seen map[*core.DataType]bool, mutation bool) bool {
	if d.Name.Abstract || d.Name.Mutable || !d.IsConcrete || seen[d] {
		return false
	}
	if d.Name.Bits > 0 {
		return true
	}
	ft, ok := d.FieldTypes()
	if !ok {
		// core's singletons (Nothing…) record no field list: no fields
		return d.Name.Singleton
	}
	seen[d] = true
	defer delete(seen, d)
	for _, f := range ft {
		if mutation && !mutationFree(f, seen) || !mutation && !identityFree(f, seen) {
			return false
		}
	}
	return true
}

func memoryName(ua *core.UnionAll) *core.TypeName {
	return core.UnwrapUnionAll(ua).(*core.DataType).Name
}
