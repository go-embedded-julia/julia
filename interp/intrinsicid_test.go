package interp

import "testing"

// IntrinsicFunction as 32-bit values, each want julia 1.12.6's
// (testdata/intrinsicid.jl).
func TestIntrinsicIDs(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	bc := "Core.Intrinsics.bitcast"
	for src, want := range map[string]string{
		bc + "(Int32, " + bc + ")":                                                           "0",
		bc + "(Int32, Core.Intrinsics.neg_float_fast)":                                       "18",
		bc + "(Int32, Core.Intrinsics.fpiseq)":                                               "39",
		bc + "(Int32, Core.Intrinsics.have_fma)":                                             "91",
		bc + "(Core.IntrinsicFunction, Int32(1))":                                            "Core.Intrinsics.neg_int",
		bc + "(Core.IntrinsicFunction, Int32(200))":                                          "Core.Intrinsics.invalid",
		bc + "(Core.IntrinsicFunction, Int32(-1))":                                           "Core.Intrinsics.invalid",
		bc + "(Core.IntrinsicFunction, Int32(200))(1)":                                       "!not ported yet: calling the invalid intrinsic 200",
		"Core.sizeof(Core.IntrinsicFunction)":                                                "4",
		"Core.IntrinsicFunction <: Core.Builtin":                                             "true",
		bc + "(Core.IntrinsicFunction, Int32(2)) === Core.Intrinsics.add_int":                "true",
		bc + "(Core.IntrinsicFunction, Core.Intrinsics.add_int) === Core.Intrinsics.add_int": "true",
	} {
		v, err := s.Eval(src)
		check(t, src, v, err, want)
	}
	if len(intrinsicNames) != 93 || intrinsicIDs["cglobal_auto"] != 92 {
		t.Errorf("%d intrinsics", len(intrinsicNames))
	}
	for id, x := range vm.intrinsics {
		if x == nil && intrinsicNames[id] != "cglobal_auto" {
			t.Errorf("julia's intrinsic %s is not defined", intrinsicNames[id])
		}
	}
	v, err := s.Eval("Core.Intrinsics.llvmcall(1, 2, 3)")
	check(t, "llvmcall", v, err, "!`llvmcall` requires the compiler")
	defer func() {
		if recover() == nil {
			t.Error("an intrinsic julia does not have was registered")
		}
	}()
	vm.newIntrinsic("not_an_intrinsic", 1, nil)
}
