package core

import (
	"strings"
	"testing"
)

// A function's type is a singleton subtype of Function; an exception type
// is a non-isbits struct; an immutable struct of isbits fields is isbits;
// and the types the runtime creates subtype as julia's do.
func TestNewType(t *testing.T) {
	ft, err := NewType("Main", "#f", FunctionType, false, false, nil, nil)
	if err != nil || !ft.Name.Singleton || !ft.Name.Isbits || !ft.IsConcrete || ft.Name.Wrapper != any(ft) {
		t.Fatalf("function type: %+v, %v", ft, err)
	}
	if !Subtype(ft, FunctionType) || Subtype(FunctionType, ft) {
		t.Error("typeof(f) is not a strict subtype of Function")
	}
	ex, err := NewType("Core", "ErrorException", ExceptionType, false, false, []Symbol{"msg"}, []any{AnyType})
	if err != nil || ex.Name.Singleton || ex.Name.Isbits || ex.Name.FieldNames[0] != "msg" {
		t.Fatalf("exception type: %+v, %v", ex, err)
	}
	pt, err := NewType("Main", "P", nil, false, false, []Symbol{"x", "y"}, []any{Int64Type, BoolType})
	if err != nil || !pt.Name.Isbits || pt.Super != AnyType {
		t.Fatalf("isbits struct: %+v, %v", pt, err)
	}
	mt, err := NewType("Main", "M", nil, false, true, []Symbol{"x"}, []any{Int64Type})
	if err != nil || mt.Name.Isbits || mt.Name.Singleton || len(mt.Name.Fields) != 1 {
		t.Fatalf("mutable struct: %+v, %v", mt, err)
	}
	at, err := NewType("Main", "A", nil, true, false, nil, nil)
	if err != nil || at.IsConcrete {
		t.Fatalf("abstract type: %+v, %v", at, err)
	}
	if sub, err := NewType("Main", "B", at, false, false, nil, nil); err != nil || !Subtype(sub, at) {
		t.Fatalf("subtype of an abstract type: %v", err)
	}
	for _, c := range []struct {
		name, want string
		super      *DataType
		abstract   bool
		mutable    bool
		names      []Symbol
		types      []any
	}{
		{"C", "invalid subtyping in definition of C: can only subtype abstract types.", pt, false, false, nil, nil},
		{"D", "field types for", nil, false, false, []Symbol{"x"}, nil},
		{"E", "abstract type has no fields", nil, true, true, nil, nil},
	} {
		if _, err := NewType("Main", c.name, c.super, c.abstract, c.mutable, c.names, c.types); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error containing %q", c.name, err, c.want)
		}
	}
}

// SetSuper refuses what jl_set_datatype_super refuses, with its messages;
// SetFieldTypes refuses an abstract type.
func TestSetSuper(t *testing.T) {
	for _, c := range []struct {
		super any
		want  string
	}{
		{int64(1), "can only subtype data types"},
		{AnyTupleType, "cannot subtype a tuple type"},
		{DataTypeType, "cannot add subtypes to Type"},
		{Int64Type, "can only subtype abstract types"},
	} {
		d := NewIncompleteType("Main", "S", false, false, nil)
		if err := d.SetSuper(c.super); err == nil || !strings.Contains(err.Error(), "invalid subtyping in definition of S: "+c.want+".") {
			t.Errorf("super %v: %v", c.super, err)
		}
	}
	d := NewIncompleteType("Main", "S", true, false, nil)
	if err := d.SetSuper(d); err == nil || !strings.Contains(err.Error(), "a type cannot subtype itself") {
		t.Errorf("itself: %v", err)
	}
	if err := d.SetSuper(AnyType); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSuper(AnyType); err == nil || !strings.Contains(err.Error(), "type already has a supertype") {
		t.Errorf("twice: %v", err)
	}
	if err := d.SetFieldTypes(nil); err == nil || !strings.Contains(err.Error(), "abstract type has no fields") {
		t.Errorf("fields of an abstract type: %v", err)
	}
}
