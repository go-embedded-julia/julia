# julia 1.12.6's compilation signatures, for interp/compilesig_test.go: each
# case of compilesig_cases.txt printed, tab-separated, as its text, then
# jl_normalize_to_compilable_sig without and with return_if_compileable,
# jl_isa_compileable_sig of the signature, and of the first result ("-" for
# what the case does not ask). Run from the repository with
#   pkgx julia@1.12.6 --startup-file=no interp/testdata/compilesig.jl > interp/testdata/compilesig.golden
module L
end
Base.include(L, joinpath(@__DIR__, "compilesig_defs.jl"))

show_in_L(x) = x === nothing ? "nothing" : sprint(io -> show(IOContext(io, :module => L), x))

function lookup(tt)
    ccall(:jl_gf_invoke_lookup_worlds, Any, (Any, Any, Csize_t, Ref{Csize_t}, Ref{Csize_t}),
          tt, Core.methodtable, Base.get_world_counter(), Ref{Csize_t}(0), Ref{Csize_t}(typemax(UInt)))
end

ev(src) = Core.eval(L, Meta.parse(src))
isa_sig(tt, env, m) = ccall(:jl_isa_compileable_sig, Cint, (Any, Any, Any), tt, env, m)
normalize(tt, env, m, r) = ccall(:jl_normalize_to_compilable_sig, Any, (Any, Any, Any, Cint), tt, env, m, r)

for line in eachline(joinpath(@__DIR__, "compilesig_cases.txt"))
    (isempty(line) || startswith(line, "#")) && continue
    if startswith(line, "set ")
        ttsrc, field, val = match(r"^set (.*) (\w+) (\w+)$", line).captures
        mm = lookup(ev(ttsrc))
        mm === nothing && error("no method for $ttsrc")
        m = mm.method
        if field == "nospecialize"
            ccall(:jl_set_nth_field, Cvoid, (Any, Csize_t, Any), m, 24, Int32(parse(Int, val)))
        elseif field == "called"
            ccall(:jl_set_nth_field, Cvoid, (Any, Csize_t, Any), m, 23, Int32(parse(Int, val)))
        elseif field == "max_varargs"
            ccall(:jl_set_nth_field, Cvoid, (Any, Csize_t, Any), m, 31, UInt8(parse(Int, val)))
        elseif field == "generator"
            stub = Core.GeneratedFunctionStub(identity, Core.svec(:a), Core.svec())
            ccall(:jl_set_nth_field, Cvoid, (Any, Csize_t, Any), m, 15, stub)
        else
            error("unknown field $field")
        end
        continue
    end
    kind, rest = split(line, ' '; limit=2)
    methsrc, ttsrc = occursin(" | ", rest) ? split(rest, " | ") : (rest, rest)
    tt = ev(ttsrc)
    mm = lookup(ev(methsrc))
    if mm === nothing
        println(rest, "\tnomethod")
        continue
    end
    m, env = mm.method, mm.sparams
    if kind == "call" || kind == "callm"
        n0 = normalize(tt, env, m, 0)
        n1 = normalize(tt, env, m, 1)
        c = isa_sig(tt, env, m)
        c0 = n0 === nothing ? "-" : string(isa_sig(n0, env, m))
        println(rest, "\t", show_in_L(n0), "\t", show_in_L(n1), "\t", c, "\t", c0)
    elseif kind == "isa"
        println(rest, "\t-\t-\t", isa_sig(tt, env, m), "\t-")
    elseif kind == "isa0"
        println(rest, "\t-\t-\t", isa_sig(tt, Core.svec(), m), "\t-")
    else
        error("unknown case $line")
    end
end
