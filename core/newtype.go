package core

import "fmt"

// NewType creates a type without parameters at run time, as jl_new_datatype
// does (datatype.c:858-957) for an empty parameter list: a struct when
// abstract is false, with fields named fieldNames of types fieldTypes, or an
// abstract type. An immutable struct whose fields are all of size zero has a
// single instance (jl_is_datatype_make_singleton, datatype.c:346-349); here
// that is one without fields, the only zero-size layout NewType builds.
func NewType(module, name string, super *DataType, abstract, mutable bool, fieldNames []Symbol, fieldTypes []any) (*DataType, error) {
	if super == nil {
		super = AnyType
	}
	if !super.Name.Abstract {
		return nil, fmt.Errorf("invalid subtyping in definition of %s: can only subtype abstract types", name)
	}
	if len(fieldNames) != len(fieldTypes) {
		return nil, fmt.Errorf("%s: %d field names for %d field types", name, len(fieldNames), len(fieldTypes))
	}
	if abstract && (mutable || len(fieldNames) > 0) {
		return nil, fmt.Errorf("%s: an abstract type has no fields and is not mutable", name)
	}
	tn := &TypeName{Name: name, Module: module, Abstract: abstract, Mutable: mutable,
		FieldNames: fieldNames}
	if !abstract && !mutable {
		tn.Fields = fieldTypes
		tn.Singleton = len(fieldNames) == 0
		tn.Isbits = isbitsFields(fieldTypes)
	}
	dt := &DataType{Name: tn, Super: super, Parameters: []any{}, HasConcreteSubtype: true}
	tn.Primary = dt
	tn.Wrapper = dt
	precomputeMemoized(dt, true)
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
