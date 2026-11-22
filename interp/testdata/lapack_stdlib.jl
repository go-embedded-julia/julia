# The cases of julia 1.12.6's LinearAlgebra tests (test/lu.jl and
# test/cholesky.jl of JuliaLang/LinearAlgebra.jl at the commit
# stdlib/LinearAlgebra.version of v1.12.6 names, 7249e66; MIT) that reach
# LAPACK's getrf, getrs, potrf and potrs, in single and double precision.
# Left out, because their routine is not in go-blas v0.4.0 or is not real:
#   - inv, cond and det of a factorization (getri, potri), pivoted Cholesky
#     (pstrf), Tridiagonal LU (gttrf), det of a Symmetric (Bunch-Kaufman,
#     sytrf), the SymTridiagonal Cholesky, `UpperTriangular(A) \ B` (trtrs, #67);
#   - the complex, BigFloat, Int and Quaternion element types (c and z come with
#     go-blas' complex types; the others do not call LAPACK);
#   - @inferred, show and Factorization conversion cases, which test julia's
#     inference and printing, not the routines.
# cond(a, 1) is computed as opnorm(a, 1) * opnorm(a \ I, 1), through the solve
# under test. The Test stdlib is not part of this interpreter, so the macros the
# cases use are defined here; a case that fails is named in `fails`. Run by
# interp/lapack_oracle_test.go on the Base image.

using LinearAlgebra, Random
using LinearAlgebra: BlasReal, BlasInt, BlasFloat, ldiv!, rdiv!, PosDefException

const ntests = Ref(0)
const fails = String[]

macro test(ex)
    s = string(ex)
    quote
        ntests[] += 1
        local why = ""
        local ok = try
            $(esc(ex)) === true
        catch err
            why = " threw " * sprint(showerror, err)
            false
        end
        ok || push!(fails, $s * why)
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
            local want = $(esc(T))
            want isa Type ? err isa want :
                (typeof(err) === typeof(want) && getfield(err, 1) == getfield(want, 1))
        end
        ok || push!(fails, "@test_throws " * $s)
    end
end

macro testset(args...)
    esc(args[end])
end

# the generator of blas_stdlib.jl: Random's rand and randn of an array use
# llvmcall, which this interpreter does not have
const lcgstate = Ref(UInt64(0x9e3779b97f4a7c15))
function lcg()
    lcgstate[] = lcgstate[] * 0x5851f42d4c957f2d + 0x14057b7ef767814f
    Float64(lcgstate[] >> 11) / 9007199254740992.0
end
rnd1(::Type{T}) where {T<:AbstractFloat} = T(lcg() - 0.5)
rnd(::Type{T}, dims::Integer...) where {T<:AbstractFloat} = reshape(T[rnd1(T) for _ in 1:prod(dims)], dims...)

condition1(a) = opnorm(a, 1) * opnorm(a \ Matrix{eltype(a)}(I, size(a, 1), size(a, 1)), 1)

# ---- test/lu.jl ----
n = 10
n1 = div(n, 2)
areal = rnd(Float64, n, n)
breal = rnd(Float64, n, 2)
creal = rnd(Float64, n)

@testset for eltya in (Float32, Float64)
    a = convert(Matrix{eltya}, areal)
    εa = eps(abs(float(one(eltya))))
    @testset "LU factorization for Number" begin
        num = rnd1(eltya)
        @test (lu(num)...,) == (hcat(one(eltya)), hcat(num), [1])
        @test convert(Array, lu(num)) ≈ eltya[num]
    end
    κ = condition1(a)
    @testset "(Automatic) Square LU decomposition" begin
        lua = factorize(a)
        l, u, p = lua.L, lua.U, lua.p
        ll, ul, pl = lu(a)
        @test ll * ul ≈ a[pl, :]
        @test l * u ≈ a[p, :]
        @test (l * u)[invperm(p), :] ≈ a
        @test copy(lua) == lua
    end
    @testset for eltyb in (Float32, Float64)
        b = convert(Matrix{eltyb}, breal)
        c = convert(Vector{eltyb}, creal)
        εb = eps(abs(float(one(eltyb))))
        ε = max(εa, εb)
        @testset "(Automatic) Square LU decomposition" begin
            lua = factorize(a)
            let Bs = copy(b), Cs = copy(c)
                for (bb, cc) in ((Bs, Cs), (view(Bs, 1:n, 1), view(Cs, 1:n)))
                    @test norm(a * (lua \ bb) - bb, 1) < ε * κ * n * 2
                    @test norm(a' * (lua' \ bb) - bb, 1) < ε * κ * n * 2
                    @test norm(a' * (lua' \ a') - a', 1) < ε * κ * n^2
                    @test norm(a * (lua \ cc) - cc, 1) < ε * κ * n
                    @test norm(a' * (lua' \ cc) - cc, 1) < ε * κ * n
                    @test AbstractArray(lua) ≈ a
                    @test norm(transpose(a) * (transpose(lua) \ bb) - bb, 1) < ε * κ * n * 2
                    @test norm(transpose(a) * (transpose(lua) \ cc) - cc, 1) < ε * κ * n
                end
                resultT = typeof(oneunit(eltyb) / oneunit(eltya))
                b_dest = similar(b, resultT)
                c_dest = similar(c, resultT)
                ldiv!(b_dest, lua, b)
                ldiv!(c_dest, lua, c)
                @test norm(b_dest - lua \ b, 1) < ε * κ * 2n
                @test norm(c_dest - lua \ c, 1) < ε * κ * n
                ldiv!(b_dest, transpose(lua), b)
                ldiv!(c_dest, transpose(lua), c)
                @test norm(b_dest - transpose(lua) \ b, 1) < ε * κ * 2n
                @test norm(c_dest - transpose(lua) \ c, 1) < ε * κ * n
                ldiv!(b_dest, adjoint(lua), b)
                ldiv!(c_dest, adjoint(lua), c)
                @test norm(b_dest - lua' \ b, 1) < ε * κ * 2n
                @test norm(c_dest - lua' \ c, 1) < ε * κ * n
                p = Matrix(b')
                q = Matrix(c')
                p_dest = copy(p)
                q_dest = copy(q)
                rdiv!(p_dest, lua)
                rdiv!(q_dest, lua)
                @test norm(p_dest - p / lua, 1) < ε * κ * 2n
                @test norm(q_dest - q / lua, 1) < ε * κ * n
            end
            e = rnd(eltyb, n, n)
            @test norm(e / lua - e / a, 1) < ε * κ * n^2
        end
        @testset "Thin LU" begin
            lua = lu(a[:, 1:n1])
            @test lua.L * lua.U ≈ lua.P * a[:, 1:n1]
        end
        @testset "Fat LU" begin
            lua = lu(a[1:n1, :])
            @test lua.L * lua.U ≈ lua.P * a[1:n1, :]
        end
    end
    @testset "LU of Symmetric/Hermitian" begin
        for HS in (Hermitian(a'a), Symmetric(a'a))
            luhs = lu(HS)
            @test luhs.L * luhs.U ≈ luhs.P * Matrix(HS)
        end
    end
    @testset "A \\ b" begin
        ca = convert(Vector{eltya}, creal)
        ba = convert(Matrix{eltya}, breal)
        @test norm(a * (a \ ca) - ca, 1) < εa * κ * n
        @test norm(a * (a \ ba) - ba, 1) < εa * κ * n * 2
    end
end

@testset "Singular matrices" for T in (Float32, Float64)
    A = T[1 2; 0 0]
    @test_throws SingularException lu(A)
    @test_throws SingularException lu!(copy(A))
    @test_throws SingularException lu(A; check = true)
    @test_throws SingularException lu!(copy(A); check = true)
    @test !issuccess(lu(A; check = false))
    @test !issuccess(lu!(copy(A); check = false))
    @test_throws ZeroPivotException lu(A, NoPivot())
    @test_throws ZeroPivotException lu!(copy(A), NoPivot())
    @test_throws ZeroPivotException lu(A, NoPivot(); check = true)
    @test_throws ZeroPivotException lu!(copy(A), NoPivot(); check = true)
    @test !issuccess(lu(A, NoPivot(); check = false))
    @test !issuccess(lu!(copy(A), NoPivot(); check = false))
    F = lu(A, NoPivot(); check = false)
    @test sprint((io, x) -> show(io, "text/plain", x), F) ==
        "Failed factorization of type $(typeof(F))"
    F2 = lu(A; allowsingular = true)
    @test !issuccess(F2)
    @test issuccess(F2, allowsingular = true)
    @test occursin("U factor (rank-deficient)", sprint((io, x) -> show(io, "text/plain", x), F2))
    # the exception names the zero pivot LAPACK reports
    @test_throws SingularException(2) lu([1.0 2.0; 2.0 4.0])
    @test_throws SingularException(1) lu(zeros(T, 3, 3))
    @test_throws SingularException T[1 2; 2 4] \ T[1, 2]
end

# ---- test/cholesky.jl ----
@testset "core functionality" begin
    for eltya in (Float32, Float64)
        a = convert(Matrix{eltya}, areal)
        ε = εa = eps(abs(float(one(eltya))))

        apd = Matrix(Hermitian(a' * a))
        capd = cholesky(apd)
        r = capd.U
        κ = condition1(apd)

        # unary_ops_tests, without inv (potri) and the logdet of a pivoted one
        @test abs((det(capd) - det(apd)) / det(capd)) <= ε * κ * n
        @test logdet(capd) ≈ log(det(capd))
        logabsdet_ca = logabsdet(capd)
        logabsdet_a = logabsdet(apd)
        @test logabsdet_ca[1] ≈ logabsdet_a[1]
        @test logabsdet_ca[2] ≈ logabsdet_a[2]
        @test isposdef(capd)
        @test size(capd) == size(apd)
        @test Array(copy(capd)) ≈ apd

        @testset "throw for non-square input" begin
            A = rnd(eltya, 2, 3)
            @test_throws DimensionMismatch cholesky(A)
            @test_throws DimensionMismatch cholesky!(A)
        end

        # the error bound on the reconstruction, LAWNS 14, Lemma 2.1
        E = abs.(apd - r' * r)
        for i = 1:n, j = 1:n
            @test E[i, j] <= (n + 1) * ε / (1 - (n + 1) * ε) * sqrt(real(apd[i, i] * apd[j, j]))
        end
        E = abs.(apd - Matrix(capd))
        for i = 1:n, j = 1:n
            @test E[i, j] <= (n + 1) * ε / (1 - (n + 1) * ε) * sqrt(real(apd[i, i] * apd[j, j]))
        end
        @test LinearAlgebra.issuccess(capd)

        apos = real(apd[1, 1])
        @test all(x -> x ≈ √apos, cholesky(apos).factors)

        apds = Symmetric(apd)
        apdsL = Symmetric(apd, :L)
        apdhL = Hermitian(apd, :L)
        capds = cholesky(apds)
        @test abs((det(capds) - det(apd)) / det(capds)) <= ε * κ * n
        @test Array(copy(capds)) ≈ apds
        capds = cholesky!(copy(apds))
        @test Array(copy(capds)) ≈ apds
        ulstring = sprint((t, s) -> show(t, "text/plain", s), capds.UL)
        @test sprint((t, s) -> show(t, "text/plain", s), capds) == "$(typeof(capds))\nU factor:\n$ulstring"

        # factor_recreation_tests
        c_U = cholesky(apds)
        c_L = cholesky(apdsL)
        cl = c_L.U
        ls = c_L.L
        @test Array(c_U) ≈ Array(c_L) ≈ apds
        @test ls * ls' ≈ apds
        @test triu(c_U.factors) ≈ c_U.U
        @test tril(c_L.factors) ≈ c_L.L
        @test istriu(cl)
        @test cl'cl ≈ apds
        @test cl'cl ≈ apdsL

        for eltyb in (Float32, Float64)
            b = convert(Matrix{eltyb}, breal)
            εb = eps(abs(float(one(eltyb))))
            ε = max(εa, εb)
            for b in (b, view(b, 1:n, 1))
                # the error bound on the solver, LAWNS 14, Theorem 2.1
                x = capd \ b
                @test norm(x - apd \ b, 1) / norm(x, 1) <= (3n^2 + n + n^3 * ε) * ε / (1 - (n + 1) * ε) * κ
                @test norm(apd * x - b, 1) / norm(b, 1) <= (3n^2 + n + n^3 * ε) * ε / (1 - (n + 1) * ε) * κ
                @test norm(a * (capd \ (a' * b)) - b, 1) / norm(b, 1) <= ε * κ * n
                lapd = cholesky(apdhL)
                @test norm(apd * (lapd \ b) - b) / norm(b) <= ε * κ * n
                @test norm(apd * (lapd \ b[1:n]) - b[1:n]) / norm(b[1:n]) <= ε * κ * n
            end
        end
        for eltyb in (Float64,)
            B = convert(Matrix{eltyb}, rnd(Float64, n, n))
            εb = eps(abs(float(one(eltyb))))
            ε = max(εa, εb)
            for B in (B, view(B, 1:n, 1:n))
                BB = copy(B)
                ldiv!(capd, BB)
                @test norm(apd \ B - BB, 1) / norm(BB, 1) <= (3n^2 + n + n^3 * ε) * ε / (1 - (n + 1) * ε) * κ
                @test norm(apd * BB - B, 1) / norm(B, 1) <= (3n^2 + n + n^3 * ε) * ε / (1 - (n + 1) * ε) * κ
            end
        end
    end
end

@testset "behavior for non-positive definite matrices" for T in (Float32, Float64)
    A = T[1 2; 2 1]
    B = T[1 2; 0 1]
    C = T[2 0; 0 0]
    for M in (A, Hermitian(A), B, C)
        @test_throws PosDefException cholesky(M)
        @test_throws PosDefException cholesky!(copy(M))
        @test_throws PosDefException cholesky(M; check = true)
        @test_throws PosDefException cholesky!(copy(M); check = true)
        @test !issuccess(cholesky(M; check = false))
        @test !issuccess(cholesky!(copy(M); check = false))
    end
    @test !isposdef(A)
    str = sprint((io, x) -> show(io, "text/plain", x), cholesky(A; check = false))
    # the exception names the order of the leading minor LAPACK reports
    @test_throws PosDefException(2) cholesky(A)
    @test_throws PosDefException(1) cholesky(T[-1 0; 0 1])
end

(ntests[], fails)
