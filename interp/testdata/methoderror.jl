# The oracle of testdata/methoderror.golden: the text julia 1.12.6 prints for a
# MethodError, character for character, for cells evaluated as the Kernel
# evaluates them (parsed with the file name "none", so that the candidates of
# a method a cell defines say `none:LINE`). Regenerate with
#
#     pkgx julia@1.12.6 methoderror.jl > methoderror.golden
#
# Not here: a case whose candidates are all equally close (`sin()`), whose
# order is the order of the method table, which is a function of the hashes of
# type names, and these mix in the build id of each module (datatype.c:84), a
# number julia draws when it makes the module: the order is that of one
# system image and cannot be reproduced.
#
# Each case is a list of cells; the last one throws a MethodError and the text
# is sprint(showerror, e), what the Kernel puts in JuliaError.Message. Names are
# unique per case: all cases run in Main.

cases = [
    ("no method for the argument types", ["1 + \"a\""]),
    ("a function with no methods", ["function nomethods_a end", "nomethods_a(1)"]),
    ("an ambiguity", ["amb_a(x::Int, y) = 1", "amb_a(x, y::Int) = 2", "amb_a(1, 1)"]),
    ("a keyword argument that does not exist", ["kw_a(x; a=1) = x", "kw_a(1; b=2)"]),
    ("too few arguments", ["few_a(x::Int, y::Int) = x", "few_a(1)"]),
    ("an argument of the wrong type", ["typed_a(x::Int) = x", "typed_a(\"s\")"]),
    ("several candidates", ["sev_a(x::Int) = 1", "sev_a(x::String) = 2", "sev_a(x::Int, y::Int) = 3", "sev_a(1.5)"]),
    ("a constructor", ["struct Pa; x::Int; end", "Pa(\"a\")"]),
    ("calling a value that is no function", ["nf_a = 1", "nf_a(2)"]),
    ("a call of a method with type parameters", ["par_a(x::Vector{T}) where T<:Integer = x", "par_a([1.5])"]),
    ("the world of the call", ["wa_f() = 1", "wa_w = Base.get_world_counter()", "wa_g(x::Int) = x", "Base.invoke_in_world(wa_w, wa_g, 1)"]),
]

function jsonstr(s::AbstractString)
    io = IOBuffer()
    print(io, '"')
    for c in s
        if c == '"'
            print(io, "\\\"")
        elseif c == '\\'
            print(io, "\\\\")
        elseif c == '\n'
            print(io, "\\n")
        elseif c == '\t'
            print(io, "\\t")
        elseif c < ' '
            print(io, "\\u", lpad(string(UInt16(c), base=16), 4, '0'))
        else
            print(io, c)
        end
    end
    print(io, '"')
    String(take!(io))
end

for (name, cells) in cases
    local text
    try
        for cell in cells
            Base.invokelatest(Core.eval, Main, Meta.parseall(cell, filename="none"))
        end
        text = "NO ERROR"
    catch e
        # printed in the newest world, as a kernel (or the REPL) prints an error
        text = e isa MethodError ? Base.invokelatest(sprint, showerror, e) : "NOT A MethodError: " * sprint(showerror, e)
    end
    println("{\"name\":", jsonstr(name), ",\"cells\":[", join(map(jsonstr, cells), ","), "],\"want\":", jsonstr(text), "}")
end
