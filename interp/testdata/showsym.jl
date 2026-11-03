# show of a Symbol (show.jl:1875-1886): an identifier or an operator after
# a colon, a quoted_syms one in parentheses, any other as Symbol("…").
for s in (:x, :+, :(==), Symbol("#self#"), Symbol("'"), Symbol("@m"), Symbol("hygienic-scope"), Symbol("method signature"), :.+, :√)
    println(repr(s))
end
println(repr(UndefVarError(Symbol("@m"))))
