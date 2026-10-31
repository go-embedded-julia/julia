package core

import "fmt"

// Types the runtime creates are built in the three steps the lowered code of
// a `struct` or `abstract type` definition takes (builtins.c:1978-2284):
// Core._structtype or Core._abstracttype makes the type without a supertype
// or field types, _setsuper! gives it its supertype, _typebody! its field
// types. Only types without parameters are built here so far.

// NewIncompleteType is jl_new_datatype (datatype.c:858-957) as
// jl_f__structtype and jl_f__abstracttype call it: a type without parameters,
// with field names but neither a supertype nor field types yet. Nothing may
// query it before SetSuper.
func NewIncompleteType(module, name string, abstract, mutable bool, fieldNames []Symbol) *DataType {
	tn := &TypeName{Name: name, Module: module, Abstract: abstract, Mutable: mutable, FieldNames: fieldNames}
	dt := &DataType{Name: tn, Parameters: []any{}, HasConcreteSubtype: true}
	tn.Primary = dt
	tn.Wrapper = dt
	precomputeMemoized(dt, true)
	return dt
}

// SetSuper is jl_set_datatype_super (builtins.c:2026-2049). A supertype
// that is a subtype of Core.Builtin is refused by the caller, which owns
// that type.
func (d *DataType) SetSuper(super any) error {
	var why string
	sd, ok := super.(*DataType)
	switch {
	case !ok:
		why = "can only subtype data types"
	case d.Super != nil:
		why = "type already has a supertype"
	case d.Name == sd.Name:
		why = "a type cannot subtype itself"
	case IsTupleType(sd):
		why = "cannot subtype a tuple type"
	case Subtype(sd, TypeType):
		why = "cannot add subtypes to Type"
	case !sd.Name.Abstract:
		why = "can only subtype abstract types"
	}
	if why != "" {
		return fmt.Errorf("invalid subtyping in definition of %s: %s.", d.Name.Name, why)
	}
	d.Super = sd
	return nil
}

// SetFieldTypes is the part of jl_f__typebody (builtins.c:2204-2284) that
// records the field types of a struct and what its layout implies: an
// immutable struct without fields has a single instance
// (jl_is_datatype_make_singleton, datatype.c:346-349), and one whose fields
// are all isbits is isbits (datatype.c:647-657).
func (d *DataType) SetFieldTypes(fieldTypes []any) error {
	if len(fieldTypes) != len(d.Name.FieldNames) {
		return fmt.Errorf("%s: %d field types for %d field names", d.Name.Name, len(fieldTypes), len(d.Name.FieldNames))
	}
	if d.Name.Abstract {
		return fmt.Errorf("%s: an abstract type has no fields", d.Name.Name)
	}
	d.Name.Fields = fieldTypes
	if !d.Name.Mutable {
		d.Name.Singleton = len(fieldTypes) == 0
		d.Name.Isbits = isbitsFields(fieldTypes)
	}
	return nil
}

// NewType creates a type without parameters in one step: NewIncompleteType,
// SetSuper (Any when super is nil) and, for a struct, SetFieldTypes.
func NewType(module, name string, super *DataType, abstract, mutable bool, fieldNames []Symbol, fieldTypes []any) (*DataType, error) {
	if abstract && (mutable || len(fieldNames) > 0) {
		return nil, fmt.Errorf("%s: an abstract type has no fields and is not mutable", name)
	}
	dt := NewIncompleteType(module, name, abstract, mutable, fieldNames)
	if super == nil {
		super = AnyType
	}
	if err := dt.SetSuper(super); err != nil {
		return nil, err
	}
	if !abstract {
		if err := dt.SetFieldTypes(fieldTypes); err != nil {
			return nil, err
		}
	}
	return dt, nil
}

// isbitsFields reports that every field type is an isbits type, so that
// an immutable struct of them is isbits (datatype.c:647-657).
func isbitsFields(fieldTypes []any) bool {
	for _, t := range fieldTypes {
		d, ok := t.(*DataType)
		if !ok || !d.IsConcrete || !d.Name.Isbits {
			return false
		}
	}
	return true
}
