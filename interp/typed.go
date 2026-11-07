package interp

import "github.com/go-embedded-julia/julia/core"

// The runtime's values that can be isbits name their Julia type to core
// (core.Typed), which asks it of a type parameter (jl_valid_type_param):
// T{x} takes a Float16, an Int128, a pointer or an isbits struct as it
// takes an Int.

func (Int128) JuliaType() any      { return core.Int128Type }
func (UInt128) JuliaType() any     { return core.UInt128Type }
func (Float16) JuliaType() any     { return core.Float16Type }
func (BFloat16) JuliaType() any    { return core.BFloat16Type }
func (v PrimValue) JuliaType() any { return v.Type }
func (s *Struct) JuliaType() any   { return s.Type }
func (p *Pointer) JuliaType() any  { return p.Type }
