package core

import "fmt"

// Types the runtime creates are built in the three steps the lowered code of
// a `struct` or `abstract type` definition takes (builtins.c:1978-2284):
// Core._structtype or Core._abstracttype makes the type without a supertype
// or field types, _setsuper! gives it its supertype, _typebody! its field
// types.

// NewIncompleteType is jl_new_datatype (datatype.c:858-957) as
// jl_f__structtype and jl_f__abstracttype call it: the primary type over the
// type variables params, with field names but neither a supertype nor field
// types yet; its wrapper is the UnionAll over params (datatype.c:946-955).
// Nothing may query it before SetSuper.
func NewIncompleteType(module, name string, params []*TypeVar, abstract, mutable bool, fieldNames []Symbol) *DataType {
	tn := &TypeName{Name: name, Module: module, Abstract: abstract, Mutable: mutable, FieldNames: fieldNames}
	ps := make([]any, len(params))
	for i, p := range params {
		ps[i] = p
	}
	dt := &DataType{Name: tn, Parameters: ps, HasConcreteSubtype: true}
	tn.Primary = dt
	var w any = dt
	for i := len(params) - 1; i >= 0; i-- {
		w = &UnionAll{Var: params[i], Body: w}
	}
	tn.Wrapper = w
	precomputeMemoized(dt, len(params) == 0)
	return dt
}

// FieldTypes is jl_get_fieldtypes for a struct or tuple type: the field
// types of this instance (Q{Int64}'s in terms of Int64), false when d
// records none.
func (d *DataType) FieldTypes() ([]any, bool) { return d.fieldTypes() }

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
	// never nil: nil means "no fields recorded" to fieldTypes, and a struct
	// without fields records an empty list
	d.Name.Fields = append([]any{}, fieldTypes...)
	// a parametric type decides per instance, from its instantiated field
	// types (isSingleton, isbits)
	if !d.Name.Mutable && len(d.Parameters) == 0 {
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
	dt := NewIncompleteType(module, name, nil, abstract, mutable, fieldNames)
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
