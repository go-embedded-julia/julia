package interp

import "github.com/go-embedded-julia/julia/expr"

// dsfmtLib is the library julia's Random calls for MersenneTwister's
// generator (stdlib Random, DSFMT.jl).
const dsfmtLib = "libdSFMT"

// bootDSFMT registers the dSFMT functions Random reaches as it loads.
// The generator itself (dsfmt_init_gen_rand, dsfmt_fill_array_*) belongs in
// the bit-generator library the embedded languages share
// (go-embedded-ruby/ruby#919, docs/SHARED-ENGINES.md); until then a call of
// one is refused as not ported.
func (vm *VM) bootDSFMT() {
	if vm.libShims == nil {
		vm.libShims = map[string]map[expr.Symbol]shim{}
	}
	vm.libShims[dsfmtLib] = map[expr.Symbol]shim{
		// dsfmt_get_min_array_size (dSFMT.c): DSFMT_N64, 382 for the Mersenne
		// exponent 19937 julia builds dSFMT with; DSFMT.jl:59 reads it as
		// Random loads
		"dsfmt_get_min_array_size": func(vm *VM, a []any) (any, error) { return int32(382), nil },
	}
}
