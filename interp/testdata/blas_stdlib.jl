# The cases of julia 1.12.6's stdlib/LinearAlgebra/test/blas.jl (MIT) for the
# routines that go-blas serves, in single and double precision: dot, iamax, axpy,
# nrm2 and asum on strided vectors, scal, gemv, gemm, and the 0-stride
# refusals. The axpby! cases are left out: OpenBLAS's daxpby is not in go-blas
# v0.1.0. The Test stdlib is not part of this interpreter, so the
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

# Random's rand and randn of a Float64 array use llvmcall, which this
# interpreter does not have: the cases draw their values from a generator of
# their own (a 64-bit linear congruential one), with the same distribution
# shape (values around 0), which is all they ask of them.
const lcgstate = Ref(UInt64(0x9e3779b97f4a7c15))
function lcg()
    lcgstate[] = lcgstate[] * 0x5851f42d4c957f2d + 0x14057b7ef767814f
    Float64(lcgstate[] >> 11) / 9007199254740992.0
end
rnd1(::Type{T}) where {T<:AbstractFloat} = T(lcg() - 0.5)
rnd(::Type{T}, dims::Integer...) where {T<:AbstractFloat} = reshape(T[rnd1(T) for _ in 1:prod(dims)], dims...)

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
            x1 = rnd(elty,n)
            x2 = rnd(elty,n)
            @test BLAS.dot(x1,x2) ≈ sum(x1.*x2)
            @test_throws DimensionMismatch BLAS.dot(x1,rnd(elty,n + 1))
        end
        @testset "iamax" begin
            x = rnd(elty,n)
            @test BLAS.iamax(x) == findmax(fabs, x)[2]
        end
        @testset "axp(b)y" begin
            x1 = rnd(elty,n)
            x2 = rnd(elty,n)
            α  = rnd1(elty)
            β  = rnd1(elty)
            for X1 in (x1, view(x1,n:-1:1)), X2 in (x2, view(x2, n:-1:1))
                @test BLAS.axpy!(α,deepcopy(X1),deepcopy(X2)) ≈ α*X1 + X2
            end
            for ind1 in (1:n, n:-1:1), ind2 in (1:n, n:-1:1)
                @test BLAS.axpy!(α,copy(x1),ind1,copy(x2),ind2) ≈ x2 + α*(ind1 == ind2 ? x1 : reverse(x1))
            end
            @test_throws DimensionMismatch BLAS.axpy!(α, copy(x1), rnd(elty,n + 1))
            @test_throws DimensionMismatch BLAS.axpby!(α, copy(x1), β, rnd(elty,n + 1))
            @test_throws DimensionMismatch BLAS.axpy!(α, copy(x1), 1:div(n,2), copy(x2), 1:n)
            @test_throws ArgumentError BLAS.axpy!(α, copy(x1), 0:div(n,2), copy(x2), 1:(div(n, 2) + 1))
            @test_throws ArgumentError BLAS.axpy!(α, copy(x1), 1:div(n,2), copy(x2), 0:(div(n, 2) - 1))
        end
        @testset "nrm2, iamax, and asum for StridedVectors" begin
            a = rnd(elty, n)
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
            α = rnd1(elty)
            a = rnd(elty, n)
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
            A = rnd(elty,3, 4)
            x = rnd(elty,5)
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
    A = rnd(elty,10, 10);
    a = view([rnd1(elty)], 1 .+ 0(1:10))
    b = view([rnd1(elty)], 1 .+ 0(1:10))
    α, β = rnd1(elty), rnd1(elty)
    @testset "dot" begin
        @test BLAS.dot(a,b) ≈ sum(a.*b)
    end
    @testset "axp(b)y!" begin
        @test BLAS.axpy!(α,a,copy(b)) ≈ α*a + b
        @test_throws "dest" BLAS.axpy!(α,a,b)
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

# Level 3 (trsm, trmm, syrk, symm), through BLAS.* and through the operators
# LinearAlgebra lowers to them: B / UpperTriangular(A), UpperTriangular(A) * B,
# Symmetric(A) * B and A' * A (the left solve UpperTriangular(A) \ B is
# LAPACK's trtrs, not trsm). These are cases of our own (the oracle is a
# triple loop over the same matrix), in the spirit of the "level 3" cases of
# julia's blas.jl; the entries are small binary fractions.
naive(A, B) = [sum(A[i, l] * B[l, j] for l in 1:size(A, 2)) for i in 1:size(A, 1), j in 1:size(B, 2)]
tr(A) = [A[j, i] for i in 1:size(A, 2), j in 1:size(A, 1)]
symm(A, uplo) = [(uplo == 'U') == (i <= j) ? A[i, j] : A[j, i] for i in 1:size(A, 1), j in 1:size(A, 2)]

@testset "level 3" for elty in (Float32, Float64)
    n = 6
    A = elty[i == j ? 4 : (mod(3i + j, 5) - 2) / 4 for i in 1:n, j in 1:n]
    B = elty[(mod(2i + j, 7) - 3) / 2 for i in 1:n, j in 1:4]
    C = elty[(mod(i + 3j, 5) - 2) / 2 for i in 1:3, j in 1:n]
    U, L = UpperTriangular(A), LowerTriangular(A)
    Un = UnitUpperTriangular(A)
    tu, tl = triu(A), tril(A)
    unit(T) = T - Diagonal(diag(T)) + I
    @testset "trmm" begin
        @test U * B ≈ naive(tu, B)
        @test L * B ≈ naive(tl, B)
        @test U' * B ≈ naive(tr(tu), B)
        @test C * U ≈ naive(C, tu)
        @test C * L ≈ naive(C, tl)
        @test C * U' ≈ naive(C, tr(tu))
        @test Un * B ≈ naive(unit(tu), B)
        @test BLAS.trmm('L', 'U', 'N', 'N', elty(2), A, B) ≈ 2 * naive(tu, B)
        @test BLAS.trmm('R', 'L', 'T', 'U', elty(1), A, C) ≈ naive(C, tr(unit(tl)))
        @test_throws DimensionMismatch BLAS.trmm('L', 'U', 'N', 'N', elty(1), A, C)
    end
    @testset "trsm" begin
        # (the left solve U \ B of a matrix is LAPACK's trtrs, not trsm: #67)
        @test naive(C / U, tu) ≈ C
        @test naive(C / Un, unit(tu)) ≈ C
        @test naive(C / L, tl) ≈ C
        @test naive(C / U', tr(tu)) ≈ C
        @test naive(tu, BLAS.trsm('L', 'U', 'N', 'N', elty(2), A, B)) ≈ 2 * B
        @test naive(tl, BLAS.trsm('L', 'L', 'N', 'N', elty(1), A, B)) ≈ B
        @test naive(tr(tu), BLAS.trsm('L', 'U', 'T', 'N', elty(1), A, B)) ≈ B
        @test naive(unit(tu), BLAS.trsm('L', 'U', 'N', 'U', elty(1), A, B)) ≈ B
        @test BLAS.trsm('R', 'L', 'N', 'N', elty(1), A, C) ≈ C / L
        @test_throws DimensionMismatch BLAS.trsm('L', 'U', 'N', 'N', elty(1), A, C)
    end
    @testset "syrk" begin
        @test A' * A ≈ naive(tr(A), A)
        @test A * A' ≈ naive(A, tr(A))
        @test B' * B ≈ naive(tr(B), B)
        @test B * B' ≈ naive(B, tr(B))
        @test triu(BLAS.syrk('U', 'T', elty(1), A)) ≈ triu(naive(tr(A), A))
        @test tril(BLAS.syrk('L', 'N', elty(2), B)) ≈ 2 * tril(naive(B, tr(B)))
        D = elty[(mod(i * j, 4) - 1) / 2 for i in 1:n, j in 1:n]
        D0 = copy(D)
        BLAS.syrk!('U', 'N', elty(1), A, elty(2), D)
        @test triu(D) ≈ triu(naive(A, tr(A)) + 2 * D0)
        @test tril(D, -1) == tril(D0, -1) # the other triangle is not touched
        @test_throws DimensionMismatch BLAS.syrk!('U', 'N', elty(1), A, elty(0), zeros(elty, 3, 3))
    end
    @testset "symm" begin
        SU, SL = symm(A, 'U'), symm(A, 'L')
        @test Symmetric(A) * B ≈ naive(SU, B)
        @test Symmetric(A, :L) * B ≈ naive(SL, B)
        @test C * Symmetric(A) ≈ naive(C, SU)
        @test C * Symmetric(A, :L) ≈ naive(C, SL)
        @test BLAS.symm('L', 'U', elty(2), A, B) ≈ 2 * naive(SU, B)
        @test BLAS.symm('R', 'L', elty(1), A, C) ≈ naive(C, SL)
        @test_throws DimensionMismatch BLAS.symm('L', 'U', elty(1), A, C)
    end
end

(ntests[], fails)
