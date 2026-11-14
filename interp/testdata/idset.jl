# julia 1.12.6's Base.IdSet driven through jl_idset_peek_bp, jl_idset_put_key,
# jl_idset_put_idx and jl_idset_pop, for interp/idset_test.go: after each
# scenario the keys' list (# where a key was removed), the width and length of
# the index, its checksum, count and max. Run with
#   pkgx julia@1.12.6 --startup-file=no interp/testdata/idset.jl
hash_key(k::Int) = k
hash_key(k) = 0
function dump(name, s)
    n = length(s.list)
    ks = 0
    for i in 1:n
        isassigned(s.list, i) && (ks += i * hash_key(s.list[i]))
    end
    keys = n <= 64 ? join([isassigned(s.list, i) ? string(s.list[i]) : "#" for i in 1:n], ",") : "n=$(n)/$(ks)"
    sum = 0
    for i in 1:length(s.idxs)
        sum += Int(s.idxs[i]) * i
    end
    println(name, " ", keys, " ", eltype(s.idxs), " ", length(s.idxs), " ", sum, " ", s.count, " ", s.max)
end

# 300 keys: the index outgrows UInt8
let s = Base.IdSet{Any}()
    for i in 1:300
        push!(s, i)
    end
    dump("grow", s)
end

# removals leave tombstones; the keys are then compacted by the next put_key
let s = Base.IdSet{Any}()
    for i in 1:40
        push!(s, i)
    end
    for i in 5:5:40
        pop!(s, i)
    end
    println("has5 ", 5 in s, " has6 ", 6 in s, " has5000 ", 5000 in s)
    dump("popped", s)
    push!(s, 5)
    dump("reinserted", s)
    for i in 41:60
        push!(s, i)
    end
    dump("compacted", s)
end

# the last keys removed: max falls back, the next key takes the freed slot
let s = Base.IdSet{Any}()
    for i in 1:8
        push!(s, i)
    end
    pop!(s, 8)
    pop!(s, 7)
    dump("tail", s)
    push!(s, 70)
    dump("tailpush", s)
    push!(s, 70)
    dump("again", s)
end

# distinct values that are isequal but not ===
let s = Base.IdSet{Any}()
    push!(s, 1); push!(s, 1.0); push!(s, true); push!(s, "a"); push!(s, :a)
    println("egal ", 1 in s, " ", 1.0 in s, " ", true in s, " ", 2 in s, " ", 1.0f0 in s)
    dump("mixed", s)
end

# an index past UInt16: 70000 keys
let s = Base.IdSet{Any}()
    for i in 1:70000
        push!(s, i)
    end
    dump("wide", s)
end
