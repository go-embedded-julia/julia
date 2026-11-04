# julia 1.12.6's libpcre2-8 calls as Base's pcre.jl makes them, for
# interp/pcre_test.go: each call's results, one line each. Run with
#   pkgx julia@1.12.6 --startup-file=no interp/testdata/pcre.jl > interp/testdata/pcre.golden
const L = Base.PCRE.PCRE_LIB

function compile(pat, opts)
    ec, eo = Ref{Cint}(0), Ref{Csize_t}(0)
    re = ccall((:pcre2_compile_8, L), Ptr{Cvoid}, (Ptr{UInt8}, Csize_t, UInt32, Ref{Cint}, Ref{Csize_t}, Ptr{Cvoid}),
               pat, ncodeunits(pat), opts, ec, eo, C_NULL)
    return re, ec[], eo[]
end

function info(re, what, T)
    r = Ref{T}()
    rc = ccall((:pcre2_pattern_info_8, L), Cint, (Ptr{Cvoid}, UInt32, Ptr{Cvoid}), re, what, r)
    return rc, rc == 0 ? r[] : nothing
end

function errmsg(code, size)
    buf = zeros(UInt8, size)
    rc = ccall((:pcre2_get_error_message_8, L), Cint, (Cint, Ptr{UInt8}, Csize_t), code, buf, size)
    size > 16 && return rc, String(buf[1:findfirst(==(0), buf)-1])
    return rc, join(buf, ",")
end

function match(re, subject, offset, opts)
    md = ccall((:pcre2_match_data_create_from_pattern_8, L), Ptr{Cvoid}, (Ptr{Cvoid}, Ptr{Cvoid}), re, C_NULL)
    rc = ccall((:pcre2_match_8, L), Cint, (Ptr{Cvoid}, Ptr{UInt8}, Csize_t, Csize_t, UInt32, Ptr{Cvoid}, Ptr{Cvoid}),
               re, subject, ncodeunits(subject), offset, opts, md, C_NULL)
    return rc, md
end

function ovector(md)
    n = ccall((:pcre2_get_ovector_count_8, L), UInt32, (Ptr{Cvoid},), md)
    p = ccall((:pcre2_get_ovector_pointer_8, L), Ptr{Csize_t}, (Ptr{Cvoid},), md)
    return n, join([unsafe_load(p, i) for i in 1:2n], ",")
end

# compile errors: the code and the offset
for (name, pat, opts) in (("open", "(", 0), ("class", "[b-a]", 0), ("quant", "a**", 0), ("badopt", "a", 0xffffffff),
                          ("ok", "(?<a>b)(c)?", 0), ("empty", "", 0), ("utf", "\xff", 0x00080000))
    re, ec, eo = compile(pat, opts)
    println("compile ", name, " ", re != C_NULL, " ", ec, " ", eo)
end
for code in (101, 102, 115, 0, -1, -48, -49, 9999, -9999)
    println("message ", code, " ", errmsg(code, 256))
end
println("message small ", errmsg(101, 5))
println("message one ", errmsg(101, 1))
println("message zero ", errmsg(101, 0))

re, _, _ = compile("(?<a>b)(c)?(?<zz>d)?", 0)
for (what, T) in ((4, UInt32), (17, UInt32), (18, UInt32), (1, UInt32), (0xffff, UInt32))
    println("info ", what, " ", info(re, what, T))
end
rc, p = info(re, 19, Ptr{UInt8})
println("nametable ", rc, " ", join([unsafe_load(p, i) for i in 1:10], ","))
println("number_from_name ", [ccall((:pcre2_substring_number_from_name_8, L), Cint, (Ptr{Cvoid}, Cstring), re, n) for n in ("a", "zz", "nope", "", "aa")])
println("jit ", ccall((:pcre2_jit_compile_8, L), Cint, (Ptr{Cvoid}, UInt32), re, 1))
println("jit badopt ", ccall((:pcre2_jit_compile_8, L), Cint, (Ptr{Cvoid}, UInt32), re, 0xffff))

for (name, subject, offset, opts) in (("xbc", "xbc", 0, 0), ("xb", "xb", 0, 0), ("xyz", "xyz", 0, 0), ("past", "xbc", 9, 0), ("notempty", "", 0, 4),
                                       ("again", "bcbc", 2, 0), ("bad opts", "xbc", 0, 0xffffffff))
    rc, md = match(re, subject, offset, opts)
    println("match ", name, " ", rc, " ", rc > 0 ? ovector(md) : ovector(md)[1])
end

md = match(re, "xbc", 0, 0)[2]
for n in (0, 1, 2, 3, 4, 100)
    s = Ref{Csize_t}(0)
    r = ccall((:pcre2_substring_length_bynumber_8, L), Cint, (Ptr{Cvoid}, Cint, Ref{Csize_t}), md, n, s)
    println("length ", n, " ", r, " ", r == 0 ? s[] : nothing)
end
for (n, size) in ((0, 16), (1, 16), (2, 16), (1, 1), (1, 2), (1, 0), (3, 16), (100, 16))
    buf = fill(0xaa, 16)
    s = Ref{Csize_t}(size)
    r = ccall((:pcre2_substring_copy_bynumber_8, L), Cint, (Ptr{Cvoid}, UInt32, Ptr{UInt8}, Ref{Csize_t}), md, n, buf, s)
    println("copy ", n, " ", size, " ", r, " ", s[], " ", join(buf, ","))
end
md = match(re, "xb", 0, 0)[2]
for n in (2, 3)
    s = Ref{Csize_t}(0)
    r = ccall((:pcre2_substring_length_bynumber_8, L), Cint, (Ptr{Cvoid}, Cint, Ref{Csize_t}), md, n, s)
    println("unset length ", n, " ", r)
    buf = fill(0xaa, 4)
    s = Ref{Csize_t}(4)
    r = ccall((:pcre2_substring_copy_bynumber_8, L), Cint, (Ptr{Cvoid}, UInt32, Ptr{UInt8}, Ref{Csize_t}), md, n, buf, s)
    println("unset copy ", n, " ", r, " ", s[], " ", join(buf, ","))
end
