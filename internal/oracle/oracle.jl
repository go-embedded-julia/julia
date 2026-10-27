# Reference oracle for go-embedded-julia.
#
# Runs inside the pinned reference `julia` and answers one request per line on
# stdin. A request is `OP\tARG...`, every argument base64-encoded; the answer is
# one line `OK\t<base64>` or `ERR\t<base64>`. Base64 on both sides means no
# byte of the payload (Julia source, invalid UTF-8 included) can be confused
# with the framing.
#
# The Expr serialisation below is the contract with expr.Serialize in Go: both
# sides must print the same tree as the same bytes. It is type-tagged, so a
# `UInt8` is never mistaken for an `Int64` and a `Float32` never for a
# `Float64`.

using Base64

const SHARE = normpath(joinpath(Sys.BINDIR, "..", "share", "julia"))

function ser_bytes(io::IO, s::AbstractString)
    print(io, '"')
    for c in s
        if !isvalid(c)
            for b in codeunits(string(c))
                print(io, "\\x", string(b, base=16, pad=2))
            end
        elseif c == '"'
            print(io, "\\\"")
        elseif c == '\\'
            print(io, "\\\\")
        elseif c < ' ' || c == '\x7f'
            print(io, "\\x", string(UInt32(c), base=16, pad=2))
        else
            print(io, c)
        end
    end
    print(io, '"')
end

const SIMPLE = r"^[A-Za-z_@][A-Za-z0-9_!@]*\z"

function ser_sym(io::IO, s::Symbol)
    str = String(s)
    if isvalid(str) && occursin(SIMPLE, str)
        print(io, ':', str)
    else
        print(io, ':')
        ser_bytes(io, str)
    end
end

hexbits(x) = "0x" * string(reinterpret(Unsigned, x), base=16, pad=2*sizeof(x))

function ser(io::IO, x)
    if x isa Expr
        print(io, '(')
        ser_sym(io, x.head)
        for a in x.args
            print(io, ' ')
            ser(io, a)
        end
        print(io, ')')
    elseif x isa Symbol
        ser_sym(io, x)
    elseif x isa String
        ser_bytes(io, x)
    elseif x isa Bool
        print(io, x ? "true" : "false")
    elseif x === nothing
        print(io, "nothing")
    elseif x isa Int64
        print(io, x)
    elseif x isa Union{Int8,Int16,Int32,UInt8,UInt16,UInt32,UInt64,Int128,UInt128}
        print(io, '{', nameof(typeof(x)), ' ', x, '}')
    elseif x isa Float64
        print(io, "{Float64 ", hexbits(x), '}')
    elseif x isa Float32
        print(io, "{Float32 ", hexbits(x), '}')
    elseif x isa Char
        print(io, "{Char 0x", string(reinterpret(UInt32, x), base=16, pad=8), '}')
    elseif x isa LineNumberNode
        print(io, "{L ", x.line, ' ')
        ser(io, x.file)
        print(io, '}')
    elseif x isa QuoteNode
        print(io, "{Q ")
        ser(io, x.value)
        print(io, '}')
    elseif x isa GlobalRef
        print(io, "{G ")
        ser_sym(io, nameof(x.mod))
        print(io, ' ')
        ser_sym(io, x.name)
        print(io, '}')
    elseif x isa Meta.ParseError
        print(io, "{ParseError ")
        ser_bytes(io, x.msg)
        print(io, '}')
    else
        print(io, "{? ")
        ser_bytes(io, string(typeof(x)))
        print(io, ' ')
        ser_bytes(io, repr(x))
        print(io, '}')
    end
end

serialize_expr(x) = sprint(ser, x)

parseall(text, filename) = serialize_expr(Meta.parseall(text; filename=filename))

const JS = Base.JuliaSyntax

# The raw lexer's tokens, one per line: kind, 0-based first and last byte,
# dotop, suffix.
function tokens(text)
    io = IOBuffer()
    for t in JS.Tokenize.tokenize(text)
        println(io, string(JS.kind(t)), ' ', t.startbyte, ' ', t.endbyte, ' ',
                Int(t.dotop), ' ', Int(t.suffix))
    end
    String(take!(io))
end

# The green tree of parse!(stream; rule=:all) -- kinds, flags and byte spans
# of every node, trivia included -- followed by the diagnostics. This is the
# parser's output before any conversion to Expr.
function green(text)
    ps = JS.ParseStream(text)
    JS.parse!(ps; rule=:all)
    g = JS.build_tree(JS.GreenNode, ps)
    io = IOBuffer()
    ser_green(io, g)
    for d in ps.diagnostics
        print(io, "\n", d.first_byte, ' ', d.last_byte, ' ', d.level, ' ')
        ser_bytes(io, d.message)
    end
    String(take!(io))
end

function ser_green(io, g)
    print(io, '(', string(JS.kind(g.head)), ' ', Int(g.head.flags), ' ', Int(g.span))
    if !JS.is_leaf(g)
        for c in JS.children(g)
            print(io, ' ')
            ser_green(io, c)
        end
    end
    print(io, ')')
end

# Whether JuliaSyntax itself throws on `text`: the body of the `try` in
# core_parser_hook (hooks.jl) for options=:all, without its `catch`, which
# would log "JuliaSyntax parser failed" and answer from flisp instead. The
# answer is "ok" or "throws <exception type>".
function jsfails(text, filename)
    try
        stream = JS.ParseStream(text, 1)
        JS.parse!(stream; rule=:all)
        if JS.any_error(stream)
            pos_before_comments = JS.last_non_whitespace_byte(stream)
            tree = JS.build_tree(JS.SyntaxNode, stream, first_line=1, filename=filename)
            tag = JS._incomplete_tag(tree, pos_before_comments)
            exc = JS.ParseError(stream, filename=filename, first_line=1, incomplete_tag=tag)
            msg = sprint(showerror, exc)
            error_ex = Expr(tag === :none ? :error : :incomplete, Meta.ParseError(msg, exc))
            topex = Expr(tree)
            @assert topex.head == :toplevel
            i = findfirst(JS._has_nested_error, topex.args)
            if i > 1 && topex.args[i-1] isa LineNumberNode
                i -= 1
            end
            resize!(topex.args, i-1)
            _, errort = JS._first_error(tree)
            push!(topex.args, LineNumberNode(JS.source_line(errort), filename))
            push!(topex.args, error_ex)
        else
            JS.build_tree(Expr, stream; filename=filename, first_line=1)
        end
        return "ok"
    catch exc
        return "throws " * string(typeof(exc))
    end
end

# Every *.jl file of the corpus roots, relative to SHARE, sorted.
function corpus_files()
    out = String[]
    for root in ("base", "Compiler", joinpath("stdlib", "v$(VERSION.major).$(VERSION.minor)"), "test")
        dir = joinpath(SHARE, root)
        isdir(dir) || continue
        for (d, _, fs) in walkdir(dir)
            for f in fs
                endswith(f, ".jl") || continue
                push!(out, relpath(joinpath(d, f), SHARE))
            end
        end
    end
    sort!(out)
    join(out, '\n')
end

# The left-hand strings of every `"input" => "output"` pair in JuliaSyntax's
# own test files: the cases its authors wrote down as parser behaviour.
function syntax_snippets()
    out = String[]
    seen = Set{String}()
    dir = joinpath(SHARE, "base", "JuliaSyntax", "test")
    for f in sort(readdir(dir))
        endswith(f, ".jl") || continue
        ex = Meta.parseall(read(joinpath(dir, f), String); filename=f)
        walk(x) = if x isa Expr
            if x.head == :call && length(x.args) == 3 && x.args[1] == :(=>) &&
                    x.args[2] isa String && x.args[3] isa String
                s = x.args[2]
                if !(s in seen)
                    push!(seen, s)
                    push!(out, s)
                end
            end
            foreach(walk, x.args)
        end
        walk(ex)
    end
    join(map(base64encode, out), '\n')
end

# Meta.lower of each top-level statement (lowering a whole :toplevel only
# wraps it), printed as julia shows the result: a CodeInfo listing for
# lowered code.
#
# The front end's gensyms (`#s12`, `#15#Shape`) come from counters that live
# for the whole julia session (src/ast.scm: *gensy-counter*, and a pool that
# reset-gensyms rewinds), so their numbers depend on what the session lowered
# before -- they differ between two platforms' julia. They are numbered here
# by first appearance instead.
function lower(text)
    m = Module(:OracleLower)
    io = IOBuffer()
    for a in Meta.parseall(text).args
        a isa LineNumberNode && continue
        s = sprint(show, MIME"text/plain"(), Meta.lower(m, a))
        seen = Dict{String,String}()
        s = replace(s, r"#(s\d+|\d+#)" => g -> get!(seen, g) do
            "#g$(length(seen) + 1)" * (endswith(g, "#") ? "#" : "")
        end)
        println(io, s)
    end
    String(take!(io))
end

function evaluate(text)
    m = Module(:OracleEval)
    Core.eval(m, :(using Base))
    out = mktemp() do path, io
        local v
        redirect_stdout(io) do
            v = Base.include_string(m, text, "eval")
        end
        flush(io)
        close(io)
        (read(path, String), v)
    end
    printed, v = out
    string(printed, "\n=> ", sprint(show, MIME"text/plain"(), v))
end

# Unicode properties of every code point, run-length encoded as lines
# `first last value`. The tables of the Go lexer are generated from this
# answer, so they come from the very utf8proc build the reference parser uses.
function unicode_tables()
    io = IOBuffer()
    println(io, "unicode ", unsafe_string(ccall(:utf8proc_unicode_version, Cstring, ())))
    props = [
        ("idstart", c -> Int(Base.is_id_start_char(c))),
        ("idchar",  c -> Int(Base.is_id_char(c))),
        ("cat",     c -> Int(Base.Unicode.category_code(c))),
        ("space",   c -> Int(isspace(c))),
        ("bound",   c -> begin
            p = ccall(:utf8proc_get_property, Ptr{UInt8}, (Int32,), Int32(UInt32(c)))
            w = unsafe_load(Ptr{UInt32}(p + 20))
            Int((w >> 8) & 0xff)
        end),
        ("width",   c -> textwidth(c)),
    ]
    for (name, f) in props
        println(io, "prop ", name)
        lo = 0
        cur = f(Char(0))
        for u in 1:0x10ffff
            v = f(Char(u))
            if v != cur
                println(io, lo, ' ', u - 1, ' ', cur)
                lo = u
                cur = v
            end
        end
        println(io, lo, ' ', 0x10ffff, ' ', cur)
    end
    String(take!(io))
end

# normalize_identifier of each code point whose image is not itself: the
# per-character part of JuliaSyntax's identifier normalisation.
function normalize_singletons()
    io = IOBuffer()
    for u in 0:0x10ffff
        (0xd800 <= u <= 0xdfff) && continue
        s = string(Char(u))
        n = Base.JuliaSyntax.normalize_identifier(s)
        n == s || println(io, u, ' ', join(string.(UInt32.(collect(n))), ','))
    end
    String(take!(io))
end

function answer(op, args)
    if op == "version"
        return string(VERSION, ' ', Base.GIT_VERSION_INFO.commit)
    elseif op == "parseall"
        return parseall(String(base64decode(args[1])), String(base64decode(args[2])))
    elseif op == "parsefile"
        rel = String(base64decode(args[1]))
        return parseall(read(joinpath(SHARE, rel), String), rel)
    elseif op == "benchparse"
        # Nanoseconds julia takes to Meta.parseall every corpus file once,
        # best of args[1] rounds after a warm-up round.
        files = split(corpus_files(), '\n')
        texts = [(f, read(joinpath(SHARE, f), String)) for f in files]
        best = typemax(UInt64)
        for r in 0:parse(Int, String(base64decode(args[1])))
            t0 = time_ns()
            for (f, s) in texts
                Meta.parseall(s; filename=f)
            end
            dt = time_ns() - t0
            r > 0 && (best = min(best, dt))
        end
        return string(best, ' ', sum(sizeof(s) for (_, s) in texts))
    elseif op == "repr"
        return repr(String(base64decode(args[1])))
    elseif op == "reprchar"
        return repr(first(String(base64decode(args[1]))))
    elseif op == "tokens"
        return tokens(String(base64decode(args[1])))
    elseif op == "green"
        return green(String(base64decode(args[1])))
    elseif op == "jsfails"
        return jsfails(String(base64decode(args[1])), String(base64decode(args[2])))
    elseif op == "readfile"
        return read(joinpath(SHARE, String(base64decode(args[1]))), String)
    elseif op == "corpus"
        return corpus_files()
    elseif op == "snippets"
        return syntax_snippets()
    elseif op == "lower"
        return lower(String(base64decode(args[1])))
    elseif op == "eval"
        return evaluate(String(base64decode(args[1])))
    elseif op == "unicode"
        return unicode_tables()
    elseif op == "normalize"
        return normalize_singletons()
    elseif op == "graphemebreak"
        # args[1]: code points separated by commas; answer one 0/1 per pair,
        # with the state threaded through as Base's lexer does.
        cps = parse.(UInt32, split(String(base64decode(args[1])), ','))
        st = Ref{Int32}(0)
        return join((Int(Base.Unicode.isgraphemebreak!(st, Char(cps[i]), Char(cps[i+1])))
                     for i in 1:length(cps)-1), ',')
    end
    error("unknown oracle op $(repr(op))")
end

function main()
    println(stdout, "READY\t", base64encode(string(VERSION)))
    flush(stdout)
    while !eof(stdin)
        line = readline(stdin)
        isempty(line) && continue
        parts = split(line, '\t')
        reply = try
            "OK\t" * base64encode(answer(String(parts[1]), String.(parts[2:end])))
        catch err
            "ERR\t" * base64encode(sprint(showerror, err))
        end
        println(stdout, reply)
        flush(stdout)
    end
end

main()
