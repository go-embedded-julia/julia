package interp

import "testing"

// julia 1.12.6: Random.DSFMT.dsfmt_get_min_array_size() == 382; a dSFMT
// function the VM lacks is refused, named with its library.
func TestDSFMT(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	v, err := s.Eval("ccall((:dsfmt_get_min_array_size, :libdSFMT), Int32, ())")
	check(t, "dsfmt_get_min_array_size", v, err, "382")
	_, err = s.Eval("ccall((:dsfmt_init_gen_rand, :libdSFMT), Cvoid, ())")
	check(t, "dsfmt_init_gen_rand", nil, err, "!interp: not ported yet: ccall((:dsfmt_init_gen_rand, :libdSFMT))")
	// a second boot of the shims keeps the table
	vm.libShims = nil
	vm.bootDSFMT()
	if vm.libShims[dsfmtLib]["dsfmt_get_min_array_size"] == nil {
		t.Error("bootDSFMT on a VM without library shims")
	}
}
