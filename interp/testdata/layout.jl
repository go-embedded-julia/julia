# The layout julia 1.12.6 gives each type of layout_types.txt (defined by
# layout_defs.jl): jl_stored_inline, then sizeof, every fieldoffset, the
# jl_datatype_layout_t (size, nfields, npointers, first_ptr, alignment, flags),
# the low ten bits of DataType.flags and TypeName.flags, tab separated, or ERR
# for a type without a layout. Regenerate with
#   pkgx julia@1.12.6 --startup-file=no interp/testdata/layout.jl > interp/testdata/layout.golden
include(joinpath(@__DIR__, "layout_defs.jl"))
for line in eachline(joinpath(@__DIR__, "layout_types.txt"))
    isempty(line) && continue
    T = Core.eval(Main, Meta.parse(line))
    r = try
        T isa DataType || error("not a DataType")
        sz = sizeof(T)
        offs = join((fieldoffset(T, i) for i in 1:fieldcount(T)), ",")
        l = unsafe_load(Ptr{DTL}(T.layout))
        ly = join((l.size, l.nfields, l.npointers, l.firstptr, l.alignment, l.flags), ",")
        string(sz, "\t", offs, "\t", ly, "\t", T.flags & 0x3ff, "\t", T.name.flags)
    catch
        "ERR"
    end
    println(line, "\t", ccall(:jl_stored_inline, Cint, (Any,), T), "\t", r)
end
