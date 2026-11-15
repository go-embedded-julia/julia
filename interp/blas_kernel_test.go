package interp

import (
	"bytes"
	"context"
	"testing"
)

// A matrix product in a Kernel cell, on every platform (the WebAssembly lane of
// CI runs it as the playground does, from a Boot()-only image): the cell
// forwards libblastrampoline to OpenBLAS as LinearAlgebra's __init__ does, and
// calls dgemm, dgemv and ddot of go-blas through it, with Ptr arguments into
// Memory as a ccall of Base's Ref{BlasInt} makes. Without Base there is no
// LinearAlgebra; the Base image runs julia's own cases (blas_oracle_test.go).
func TestKernelMatrixProduct(t *testing.T) {
	img := saveBytes(t, bootedVM(t))
	k, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	evalOK(t, k, `
const LBT = "libblastrampoline"
const F = Core.GenericMemory{:not_atomic, Float64, Core.CPU}
const N = Core.GenericMemory{:not_atomic, Int64, Core.CPU}
const U = Core.GenericMemory{:not_atomic, UInt8, Core.CPU}
setf(m, i, v) = Core.memoryrefset!(Core.memoryref(m, i), v, :not_atomic, true)
getf(m, i) = Core.memoryrefget(Core.memoryref(m, i), :not_atomic, true)
pf(m) = Core.Intrinsics.bitcast(Ptr{Float64}, getfield(m, :ptr))
pn(m) = Core.Intrinsics.bitcast(Ptr{Int64}, getfield(m, :ptr))
pu(m) = Core.Intrinsics.bitcast(Ptr{UInt8}, getfield(m, :ptr))
f1(a) = (m = F(undef, 1); setf(m, 1, a); m)
f2(a, b) = (m = F(undef, 2); setf(m, 1, a); setf(m, 2, b); m)
f4(a, b, c, d) = (m = F(undef, 4); setf(m, 1, a); setf(m, 2, b); setf(m, 3, c); setf(m, 4, d); m)
nint(v) = (m = N(undef, 1); setf(m, 1, v); m)
nflag(c) = (m = U(undef, 1); setf(m, 1, c); m)
nreal(v) = f1(v)
path = "libopenblas64_.dylib"
forwarded = ccall((:lbt_forward, LBT), Int32, (Ptr{UInt8}, Int32, Int32, Ptr{UInt8}),
    ccall(:jl_string_ptr, Ptr{UInt8}, (Any,), path), Int32(1), Int32(0), Core.Intrinsics.bitcast(Ptr{UInt8}, 0))
forwarded`, "16")
	// C = A*B with A = [1 3; 2 4] and B = [5 7; 6 8] (column-major), then
	// y = A*x and x'*y
	evalOK(t, k, `
A = f4(1.0, 2.0, 3.0, 4.0); B = f4(5.0, 6.0, 7.0, 8.0); C = f4(0.0, 0.0, 0.0, 0.0)
ccall((:dgemm_64_, LBT), Nothing,
    (Ptr{UInt8}, Ptr{UInt8}, Ptr{Int64}, Ptr{Int64}, Ptr{Int64}, Ptr{Float64}, Ptr{Float64}, Ptr{Int64},
     Ptr{Float64}, Ptr{Int64}, Ptr{Float64}, Ptr{Float64}, Ptr{Int64}, Int64, Int64),
    pu(nflag(0x4e)), pu(nflag(0x4e)), pn(nint(2)), pn(nint(2)), pn(nint(2)), pf(nreal(1.0)), pf(A), pn(nint(2)),
    pf(B), pn(nint(2)), pf(nreal(0.0)), pf(C), pn(nint(2)), 1, 1)
x = f2(1.0, 2.0); y = f2(0.0, 0.0)
ccall((:dgemv_64_, LBT), Nothing,
    (Ptr{UInt8}, Ptr{Int64}, Ptr{Int64}, Ptr{Float64}, Ptr{Float64}, Ptr{Int64}, Ptr{Float64}, Ptr{Int64},
     Ptr{Float64}, Ptr{Float64}, Ptr{Int64}, Int64),
    pu(nflag(0x4e)), pn(nint(2)), pn(nint(2)), pf(nreal(1.0)), pf(A), pn(nint(2)), pf(x), pn(nint(1)),
    pf(nreal(0.0)), pf(y), pn(nint(1)), 1)
d = ccall((:cblas_ddot64_, LBT), Float64, (Int64, Ptr{Float64}, Int64, Ptr{Float64}, Int64), 2, pf(x), 1, pf(y), 1)
(getf(C, 1), getf(C, 2), getf(C, 3), getf(C, 4), getf(y, 1), getf(y, 2), d)`,
		"(23.0, 34.0, 31.0, 46.0, 7.0, 10.0, 27.0)")
	r := k.Eval(context.Background(), `ccall((:dgemm_64_, "libnope"), Nothing, ())`)
	if r.Err == nil {
		t.Error("a ccall in a library that is not there ran")
	}
}
