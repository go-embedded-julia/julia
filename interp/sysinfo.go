package interp

import "runtime"

// bootSysinfo registers what Sys.__init__ (sysinfo.jl:150-169) asks the
// runtime, which the Base probe reached after the scheduler's calls. The
// answers are those of julia 1.12.6 on darwin/arm64 (Sys.SC_CLK_TCK 100,
// Sys.JIT "ORCJIT", Sys.PAGESIZE 16384); the processor count is the host's,
// as julia's is, and the CPU name that of the target the build models.
func (vm *VM) bootSysinfo() {
	vm.shims["jl_cpu_threads"] = func(vm *VM, a []any) (any, error) { return int32(runtime.NumCPU()), nil }
	vm.shims["jl_SC_CLK_TCK"] = func(vm *VM, a []any) (any, error) { return int64(100), nil }
	vm.shims["jl_get_cpu_name"] = func(vm *VM, a []any) (any, error) { return "apple-m1", nil }
	vm.shims["jl_get_JIT"] = func(vm *VM, a []any) (any, error) { return "ORCJIT", nil }
	vm.shims["jl_getpagesize"] = func(vm *VM, a []any) (any, error) { return int64(16384), nil }
}
