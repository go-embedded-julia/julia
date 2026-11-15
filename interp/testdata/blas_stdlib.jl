# The cases of julia 1.12.6's stdlib/LinearAlgebra/test/blas.jl (MIT) for the
# routines that go-blas serves, in single and double precision: dot, iamax, axpy
# and axpby, nrm2 and asum on strided vectors, scal, gemv, gemm, and the
# 0-stride refusals. The Test stdlib is not part of this interpreter, so the
# three macros the cases use are defined here; a case that fails is named in
# `fails`. Run by interp/blas_oracle_test.go on the Base image.

using LinearAlgebra, Random
using LinearAlgebra: BlasReal

const ntests = Ref(0)
const fails = String[]

macro test(ex)
    s = string(ex)
    quote
        ntests[] += 1
        local ok = try
            $(esc(ex)) === true
        catch err
            false
        end
        ok || push!(fails, $s)
    end
end

macro test_throws(T, ex)
    s = string(ex)
    quote
        ntests[] += 1
        local ok = try
            $(esc(ex))
            false
        catch err
            $(esc(T)) isa AbstractString ? occursin($(esc(T)), sprint(showerror, err)) : err isa $(esc(T))
        end
        ok || push!(fails, "@test_throws " * $s)
    end
end

macro testset(args...)
    esc(args[end])
end

fabs(x::Real) = abs(x)

@testset "vec_pointer_stride" begin
    a = float(rand(1:20,4,4,4))
    @test BLAS.asum(a) == sum(a) # dense case
    @test BLAS.asum(view(a,1:2:4,:,:)) == sum(view(a,1:2:4,:,:)) # vector like
    @test BLAS.asum(view(a,1:3,2:2,3:3)) == sum(view(a,1:3,2:2,3:3))
    @test BLAS.asum(view(a,1:1,1:3,1:1)) == sum(view(a,1:1,1:3,1:1))
    @test BLAS.asum(view(a,1:1,1:1,1:3)) == sum(view(a,1:1,1:1,1:3))
    @test_throws ArgumentError BLAS.asum(view(a,1:3:4,:,:)) # non-vector like
    @test_throws ArgumentError BLAS.asum(view(a,1:2,1:1,1:3))
end
Random.seed!(100)
@testset for elty in [Float32, Float64]
    o4 = fill(elty(1), 4)
    z4 = zeros(elty, 4)

    I4 = Matrix{elty}(I, 4, 4)
    I43 = Matrix{elty}(I, 4, 3)
    L4 = tril(fill(elty(1), 4,4))
    U4 = triu(fill(elty(1), 4,4))
    Z4 = zeros(elty, (4,4))

    elm1 = elty(-1)
    el2 = elty(2)
    v14 = elty[1:4;]
    v41 = elty[4:-1:1;]

    let n = 10
        @testset "dot products" begin
            x1 = randn(elty, n)
            x2 = randn(elty, n)
            @test BLAS.dot(x1,x2) ≈ sum(x1.*x2)
            @test_throws DimensionMismatch BLAS.dot(x1,rand(elty, n + 1))
        end
        @testset "iamax" begin
            x = randn(elty, n)
            @test BLAS.iamax(x) == findmax(fabs, x)[2]
        end
        @testset "axp(b)y" begin
            x1 = randn(elty, n)
            x2 = randn(elty, n)
            α  = rand(elty)
            β  = rand(elty)
            for X1 in (x1, view(x1,n:-1:1)), X2 in (x2, view(x2, n:-1:1))
                @test BLAS.axpy!(α,deepcopy(X1),deepcopy(X2)) ≈ α*X1 + X2
                @test BLAS.axpby!(α,deepcopy(X1),β,deepcopy(X2)) ≈ α*X1 + β*X2
            end
            for ind1 in (1:n, n:-1:1), ind2 in (1:n, n:-1:1)
                @test BLAS.axpy!(α,copy(x1),ind1,copy(x2),ind2) ≈ x2 + α*(ind1 == ind2 ? x1 : reverse(x1))
            end
            @test_throws DimensionMismatch BLAS.axpy!(α, copy(x1), rand(elty, n + 1))
            @test_throws DimensionMismatch BLAS.axpby!(α, copy(x1), β, rand(elty, n + 1))
            @test_throws DimensionMismatch BLAS.axpy!(α, copy(x1), 1:div(n,2), copy(x2), 1:n)
            @test_throws ArgumentError BLAS.axpy!(α, copy(x1), 0:div(n,2), copy(x2), 1:(div(n, 2) + 1))
            @test_throws ArgumentError BLAS.axpy!(α, copy(x1), 1:div(n,2), copy(x2), 0:(div(n, 2) - 1))
        end
        @testset "nrm2, iamax, and asum for StridedVectors" begin
            a = rand(elty,n)
            for ind in (2:2:n, n:-2:2)
                b = view(a, ind, 1)
                @test BLAS.nrm2(b) ≈ sqrt(sum(abs2, b))
                @test BLAS.asum(b) ≈ sum(fabs, b)
                @test BLAS.iamax(b) == findmax(fabs, b)[2] * (step(ind) >= 0)
            end
        end
        @testset "nrm2 with non-finite elements" begin
            a = zeros(elty,n)
            a[begin] = elty(-Inf)
            @test BLAS.nrm2(a) === abs2(elty(Inf))
            a[begin] = elty(NaN)
            @test BLAS.nrm2(a) === abs2(elty(NaN))
        end
        @testset "scal" begin
            α = rand(elty)
            a = rand(elty,n)
            @test BLAS.scal(n,α,a,1) ≈ α * a
            for v in (a, view(a, n:-1:1))
                @test BLAS.scal!(α, deepcopy(v)) ≈ α * v
            end
        end
    end

    @testset "gemv" begin
        @test all(BLAS.gemv('N', I4, o4) .== o4)
        @test all(BLAS.gemv('T', I4, o4) .== o4)
        @test all(BLAS.gemv('N', el2, I4, o4) .== el2 * o4)
        @test all(BLAS.gemv('T', el2, I4, o4) .== el2 * o4)
        @test_throws DimensionMismatch BLAS.gemv('N',I43,o4)
        o4cp = copy(o4)
        @test_throws DimensionMismatch BLAS.gemv!('T',one(elty),I43,o4,one(elty),o4cp)
        @test_throws DimensionMismatch BLAS.gemv!('C',one(elty),I43,o4,one(elty),o4cp)
        @test all(BLAS.gemv!('N', one(elty), I4, o4, elm1, o4cp) .== z4)
        @test all(o4cp .== z4)
        o4cp[:] = o4
        @test all(BLAS.gemv!('T', one(elty), I4, o4, elm1, o4cp) .== z4)
        @test all(o4cp .== z4)
        @test all(BLAS.gemv('N', U4, o4) .== v41)
        @test all(BLAS.gemv('N', U4, o4) .== v41)
        @testset "non-standard strides" begin
            A = rand(elty, 3, 4)
            x = rand(elty, 5)
            for y = (view(ones(elty, 5), 1:2:5), view(ones(elty, 7), 6:-2:2))
                ycopy = copy(y)
                @test BLAS.gemv!('N', elty(2), view(A, :, 2:2:4), view(x, 1:3:4), elty(3), y) ≈ 2*A[:,2:2:4]*x[1:3:4] + 3*ycopy
                ycopy = copy(y)
                @test BLAS.gemv!('N', elty(2), view(A, :, 4:-2:2), view(x, 1:3:4), elty(3), y) ≈ 2*A[:,4:-2:2]*x[1:3:4] + 3*ycopy
                ycopy = copy(y)
                @test BLAS.gemv!('N', elty(2), view(A, :, 2:2:4), view(x, 4:-3:1), elty(3), y) ≈ 2*A[:,2:2:4]*x[4:-3:1] + 3*ycopy
                ycopy = copy(y)
                @test BLAS.gemv!('N', elty(2), view(A, :, 4:-2:2), view(x, 4:-3:1), elty(3), y) ≈ 2*A[:,4:-2:2]*x[4:-3:1] + 3*ycopy
                ycopy = copy(y)
                @test BLAS.gemv!('N', elty(2), view(A, :, StepRangeLen(1,0,1)), view(x, 1:1), elty(3), y) ≈ 2*A[:,1:1]*x[1:1] + 3*ycopy # stride(A,2) == 0
            end
            @test BLAS.gemv!('N', elty(1), zeros(elty, 0, 5), zeros(elty, 5), elty(1), zeros(elty, 0)) == elty[] # empty matrix, stride(A,2) == 0
            @test BLAS.gemv('N', elty(-1), view(A, 2:3, 1:2:3), view(x, 2:-1:1)) ≈ -1*A[2:3,1:2:3]*x[2:-1:1]
            @test BLAS.gemv('N', view(A, 2:3, 3:-2:1), view(x, 1:2:3)) ≈ A[2:3,3:-2:1]*x[1:2:3]
            for (trans, f) = (('T',transpose), ('C',adjoint))
                for y = (view(ones(elty, 3), 1:2:3), view(ones(elty, 5), 4:-2:2))
                    ycopy = copy(y)
                    @test BLAS.gemv!(trans, elty(2), view(A, :, 2:2:4), view(x, 1:2:5), elty(3), y) ≈ 2*f(A[:,2:2:4])*x[1:2:5] + 3*ycopy
                    ycopy = copy(y)
                    @test BLAS.gemv!(trans, elty(2), view(A, :, 4:-2:2), view(x, 1:2:5), elty(3), y) ≈ 2*f(A[:,4:-2:2])*x[1:2:5] + 3*ycopy
                    ycopy = copy(y)
                    @test BLAS.gemv!(trans, elty(2), view(A, :, 2:2:4), view(x, 5:-2:1), elty(3), y) ≈ 2*f(A[:,2:2:4])*x[5:-2:1] + 3*ycopy
                    ycopy = copy(y)
                    @test BLAS.gemv!(trans, elty(2), view(A, :, 4:-2:2), view(x, 5:-2:1), elty(3), y) ≈ 2*f(A[:,4:-2:2])*x[5:-2:1] + 3*ycopy
                end
                @test BLAS.gemv!(trans, elty(2), view(A, :, StepRangeLen(1,0,1)), view(x, 1:2:5), elty(3), elty[1]) ≈ 2*f(A[:,1:1])*x[1:2:5] + elty[3] # stride(A,2) == 0
            end
            for trans = ('N', 'T', 'C')
                @test_throws ErrorException BLAS.gemv(trans, view(A, 1:2:3, 1:2), view(x, 1:2)) # stride(A,1) must be 1
            end
        end
    end
    @testset "gemm" begin
        @test all(BLAS.gemm('N', 'N', I4, I4) .== I4)
        @test all(BLAS.gemm('N', 'T', I4, I4) .== I4)
        @test all(BLAS.gemm('T', 'N', I4, I4) .== I4)
        @test all(BLAS.gemm('T', 'T', I4, I4) .== I4)
        @test all(BLAS.gemm('N', 'N', el2, I4, I4) .== el2 * I4)
        @test all(BLAS.gemm('N', 'T', el2, I4, I4) .== el2 * I4)
        @test all(BLAS.gemm('T', 'N', el2, I4, I4) .== el2 * I4)
        @test all(BLAS.gemm('T', 'T', el2, I4, I4) .== el2 * I4)
        I4cp = copy(I4)
        @test all(BLAS.gemm!('N', 'N', one(elty), I4, I4, elm1, I4cp) .== Z4)
        @test all(I4cp .== Z4)
        I4cp[:] = I4
        @test all(BLAS.gemm!('N', 'T', one(elty), I4, I4, elm1, I4cp) .== Z4)
        @test all(I4cp .== Z4)
        I4cp[:] = I4
        @test all(BLAS.gemm!('T', 'N', one(elty), I4, I4, elm1, I4cp) .== Z4)
        @test all(I4cp .== Z4)
        I4cp[:] = I4
        @test all(BLAS.gemm!('T', 'T', one(elty), I4, I4, elm1, I4cp) .== Z4)
        @test all(I4cp .== Z4)
        @test all(BLAS.gemm('N', 'N', I4, U4) .== U4)
        @test all(BLAS.gemm('N', 'T', I4, U4) .== L4)
        @test_throws DimensionMismatch BLAS.gemm!('N','N', one(elty), I4, I4, elm1, Matrix{elty}(I, 5, 5))
        @test_throws DimensionMismatch BLAS.gemm!('N','N', one(elty), I43, I4, elm1, I4)
        @test_throws DimensionMismatch BLAS.gemm!('T','N', one(elty), I43, I4, elm1, I43)
        @test_throws DimensionMismatch BLAS.gemm!('N','T', one(elty), I43, I43, elm1, I43)
        @test_throws DimensionMismatch BLAS.gemm!('T','T', one(elty), I43, I43, elm1, Matrix{elty}(I, 3, 4))
    end
end

@testset "test for 0-strides" for elty in (Float32, Float64)
    A = randn(elty, 10, 10);
    a = view([randn(elty)], 1 .+ 0(1:10))
    b = view([randn(elty)], 1 .+ 0(1:10))
    α, β = randn(elty), randn(elty)
    @testset "dot" begin
        @test BLAS.dot(a,b) ≈ sum(a.*b)
    end
    @testset "axp(b)y!" begin
        @test BLAS.axpy!(α,a,copy(b)) ≈ α*a + b
        @test BLAS.axpby!(α,a,β,copy(b)) ≈ α*a + β*b
        @test_throws "dest" BLAS.axpy!(α,a,b)
        @test_throws "dest" BLAS.axpby!(α,a,β,b)
    end
    @test BLAS.iamax(a) == 0
    @test_throws "dest" BLAS.scal!(b[1], a)
    @testset "nrm2/asum" begin # OpenBLAS always return 0.0
        @test_throws "input" BLAS.nrm2(a)
        @test_throws "input" BLAS.asum(a)
    end
    @testset "gemv!" begin
        @test_throws "input" BLAS.gemv!('N', true, A, a, false, copy(b))
        @test_throws "dest" BLAS.gemv!('N', true, A, copy(a), false, b)
    end
end

(ntests[], fails)
