package interp

import "testing"

// jl_sigatomic_begin and _end count the nesting, and ending outside one is
// julia's error; leaving a try block puts back the count it was entered
// with (rtutils.c:308, 341), as stream.jl's try-finally relies on; no julia
// finalizer runs here.
func TestSigatomic(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	for _, c := range []struct{ src, want string }{
		{"ccall(:jl_gc_is_in_finalizer, Int8, ())", "0"},
		{"ccall(:jl_sigatomic_end, Nothing, ())", "!sigatomic_end called in non-sigatomic region"},
		{"ccall(:jl_sigatomic_begin, Nothing, ()); ccall(:jl_sigatomic_end, Nothing, ())", "nothing"},
		// the try block's begin is undone when it is left, normally or not
		{"try; ccall(:jl_sigatomic_begin, Nothing, ()); finally; end; ccall(:jl_sigatomic_end, Nothing, ())", "!sigatomic_end called in non-sigatomic region"},
		{"try; ccall(:jl_sigatomic_begin, Nothing, ()); Core.throw(1); catch; end; ccall(:jl_sigatomic_end, Nothing, ())", "!sigatomic_end called in non-sigatomic region"},
		// and the count from before the try block is kept
		{"ccall(:jl_sigatomic_begin, Nothing, ()); try; ccall(:jl_sigatomic_end, Nothing, ()); finally; end; ccall(:jl_sigatomic_end, Nothing, ())", "nothing"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
	// a switch saves and restores it with the task's state
	vm.deferSignal = 3
	st := vm.saveTaskState()
	vm.deferSignal = 0
	vm.loadTaskState(st)
	if vm.deferSignal != 3 {
		t.Errorf("deferSignal after a switch back: %d", vm.deferSignal)
	}
}
