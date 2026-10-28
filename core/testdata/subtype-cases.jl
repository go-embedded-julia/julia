# One subtyping question per line, A <: B, in Julia syntax. Most are
# translated from julia 1.12.6 test/subtype.jl (lines 20-330; @UnionAll T X
# is written X where T); julia answers them, so the translation only picks
# the questions. The rest target branches of subtype.c random draws miss.
Int <: Integer
Integer <: Int
Array{Int,1} <: AbstractArray{Int,1}
Tuple{Int,Int} <: Tuple{Integer,Integer}
Tuple{Integer,Integer} <: Tuple{Int,Int}
Tuple{Array{Int,1}} <: Tuple{AbstractArray{Int,1}}
Tuple{Int,Int} <: Tuple{Int}
Tuple{Int} <: Tuple{Integer,Integer}
Array{Int,1} <: Array{Integer,1}
Tuple{Int,Int} <: Tuple{Vararg{Int}}
Tuple{Vararg{Int}} <: Tuple{Int,Int}
Tuple{Int,Int} <: Tuple{Int,Vararg{Int}}
Tuple{Int,Int} <: Tuple{Int,Vararg{Integer}}
Tuple{Int,Int} <: Tuple{Int,Int,Vararg{Integer}}
Tuple{Int,Vararg{Int}} <: Tuple{Vararg{Int}}
Tuple{Vararg{Int}} <: Tuple{Int,Vararg{Int}}
Tuple{Int,Int,Int} <: Tuple{Integer,Vararg{Int}}
Tuple{} <: Tuple{Vararg{Any}}
Tuple{} <: Tuple{Int, Vararg{Int}}
Tuple{Int} <: Tuple{Int, Int, Vararg{Int}}
Tuple{Int, Tuple{Real, Integer}} <: Tuple{Vararg{Int}}
Tuple{Int,Int} <: Tuple{Vararg{Int,2}}
Tuple{Vararg{Int,2}} <: Tuple{Int,Int}
Tuple{Int,Vararg{Int,2}} <: Tuple{Int,Int,Int,Vararg{Int,1}}
(Tuple{Int,Vararg{Int,N}} where N) <: (Tuple{Int,Vararg{Int,N}} where N)
Tuple{Tuple{Int,Int},Tuple{Int,Int}} <: (Tuple{NTuple{N,Int},NTuple{N,Int}} where N)
(Tuple{NTuple{N,Int},NTuple{N,Int}} where N) <: Tuple{Tuple{Int,Int},Tuple{Int,Int}}
Tuple{Tuple{Int,Int},Tuple{Int,}} <: (Tuple{NTuple{N,Int},NTuple{N,Int}} where N)
Tuple{Val{3}, Vararg{Val{3}}} <: (Tuple{Vararg{Val{N}, N}} where N)
Tuple{Int,Int} <: (Tuple{Int,Int,Vararg{Int,N}} where N)
(Tuple{Int,Int,Vararg{Int,N}} where N) <: Tuple{Int,Int}
Tuple{Int,Int} <: (Tuple{E,E,Vararg{E,N}} where E where N)
(Tuple{E,E,Vararg{E,N}} where E where N) <: Tuple{Int,Int}
Type{Tuple{Vararg{Int,N}} where N} <: Type{Tuple{Vararg{Int}}}
Type{Tuple{Vararg{Int}}} <: Type{Tuple{Vararg{Int,N}} where N}
Tuple{Integer,Integer} <: (Tuple{T,T} where T)
Tuple{Integer,Int} <: (Tuple{T,S} where S<:T where T)
Tuple{Integer,Int} <: (Tuple{T,S} where T<:S<:T where T)
Tuple{Integer,Int,Int} <: (Tuple{T,S,S} where T<:S<:T where T)
(Tuple{R,R} where R) <: (Tuple{T,S} where S where T)
(Tuple{T,S} where S where T) <: (Tuple{R,R} where R)
(Tuple{R,R} where R) <: (Tuple{T,S} where S<:T where T)
(Tuple{T,S} where S<:T where T) <: (Tuple{R,R} where R)
(Tuple{R,R} where R) <: (Tuple{T,S} where T<:S<:T where T)
(Tuple{R,R} where R) <: (Tuple{T,S} where S>:T where T)
(Tuple{T,S} where S>:T where T) <: (Tuple{R,R} where R)
Tuple{Real,Real} <: (Tuple{T,T} where T<:Real)
(Tuple{S,R,Vector{Any}} where R<:AbstractString where S<:Int) <: (Tuple{T, T, Array{T,1}} where T)
Tuple{String, Real, Ref{Number}} <: (Tuple{Union{T,String}, T, Ref{T}} where T)
(Tuple{Union{T,String}, T, Ref{T}} where T) <: Tuple{String, Real, Ref{Number}}
Tuple{String, Real} <: (Tuple{Union{T,String}, T} where T)
Tuple{Real, Real} <: (Tuple{Union{T,String}, T} where T)
Tuple{Int, Int} <: (Tuple{Union{T,String}, T} where T)
(Tuple{Vararg{A}} where A>:Integer) <: (Tuple{Vararg{A}} where A>:Integer)
(Tuple{T, T, Ref{T}} where T) <: (Tuple{S, S, Ref{Q} where Q} where S)
(Tuple{T, T, Ref{T}} where T) <: (Tuple{S, S, Ref{Q} where Q} where S<:Integer)
(Tuple{T, T, Ref{T}} where T) <: (Tuple{S, S, Ref{Q} where Q} where S<:Int)
(Tuple{T, T, Ref{T}} where T<:Int) <: (Tuple{S, S, Ref{Q} where Q} where S)
(Tuple{T, T, Ref{T}} where T>:Int) <: (Tuple{S, S, Ref{Q} where Q} where S)
(Tuple{T, T, Ref{T}} where T>:Integer) <: (Tuple{S, S, Ref{Q} where Q} where S)
(Tuple{T, T, Ref{T}} where T>:Any) <: (Tuple{S, S, Ref{Q} where Q} where S)
(Tuple{T, T} where Int<:T<:Int) <: (Tuple{T, T} where Int<:T<:Int)
(Tuple{T, T} where T>:Int) <: (Tuple{T, T} where T>:Int)
Tuple{Tuple{T, T} where T>:Int} <: Tuple{Tuple{T, T} where T>:Int}
Tuple{Tuple{T, T} where T>:Int} <: (Tuple{Tuple{T, T}} where T>:Int)
(Tuple{Tuple{T, T}} where T>:Int) <: Tuple{Tuple{T, T} where T>:Int}
Vector{Tuple{T, T} where Number<:T<:Number} <: Vector{Tuple{Number, Number}}
Type{Tuple{T,Any} where T} <: (Type{Tuple{T,T}} where T)
Type{Tuple{T,Any,T} where T} <: (Type{Tuple{T,T,T}} where T)
Type{Tuple{T} where T} <: (Type{Tuple{T}} where T)
Ref{Tuple{T} where T} <: (Ref{Tuple{T}} where T)
Type{Tuple{T,T} where T} <: (Type{Tuple{T,T}} where T)
Type{Tuple{T,T,T} where T} <: (Type{Tuple{T,T,T}} where T)
Ref{Tuple{T, T} where Int<:T<:Int} <: (Ref{Tuple{S, S}} where Int<:S<:Int)
(Ref{Tuple{S, S}} where Int<:S<:Int) <: Ref{Tuple{T, T} where Int<:T<:Int}
(Tuple{X, X} where {X<:Union{}}) <: (Tuple{X, X, Vararg{Any}} where {Int<:X<:Int})
(Tuple{Integer, X, Vararg{X}} where {X<:Int}) <: (Tuple{Any, Vararg{X}} where {X>:Int})
(Tuple{Any, X, Vararg{X}} where {X<:Int}) <: (Tuple{Vararg{X}} where X>:Integer)
Tuple{Integer, Integer, Any, Vararg{Any}} <: (Tuple{Vararg{X}} where X>:Integer)
(Tuple{T,T} where {T<:Int}) <: (Tuple{T,T} where {T>:Int})
Tuple{Int,Int8,Vector{Integer}} <: (Tuple{T,T,Vector{T}} where T>:Integer)
(Tuple{T,T,Vector{T}} where T>:Integer) <: (Tuple{T,T,Vector{Union{Integer,T}}} where T)
(Tuple{T,T,Vector{Union{Integer,T}}} where T) <: (Tuple{T,T,Vector{T}} where T>:Integer)
Tuple{Int,Int8,Vector{Integer}} <: (Tuple{T,T,Vector{Union{Integer,T}}} where T)
Tuple{Int,Int8,Vector{Any}} <: (Tuple{T,T,Vector{Union{Integer,T}}} where T)
(Tuple{T, T, Array{T, 1}} where T) <: (Tuple{T, T, Any} where T)
(Union{Tuple{Int,Bool}, Tuple{P,Bool}} where P) <: (Tuple{Union{T,Int}, T} where T)
(Tuple{Ref{Tuple{N,N}}, Ref{N}} where N) <: (Tuple{Ref{Tuple{N1,N1}}, Ref{N2}} where {N1, N2})
(Tuple{Ref{Tuple{N1,N1}}, Ref{N2}} where {N1, N2}) <: (Tuple{Ref{Tuple{N,N}}, Ref{N}} where N)
Tuple{Type{Tuple{Vararg{T}} where T <: Integer}, Tuple{Float64, Int}} <: (Tuple{Type{Tuple{Vararg{T}}}, Tuple{Vararg{T}}} where T)
Tuple{3,3} <: NTuple
NTuple <: Tuple{3,3}
Tuple{3,4} <: NTuple
Array{Int,1} <: (Vector{T} where T)
(Pair{T,T} where T) <: Pair
Pair <: (Pair{T,T} where T)
Pair{Int,Int8} <: Pair
Pair{Int,Int8} <: (Pair{Int,S} where S)
(T where T<:Real) <: (T where T<:Integer)
(Tuple{T,T} where T) <: (Tuple{R,R} where R)
(Tuple{T,S} where S<:Number where T<:Integer) <: (Tuple{S,T} where S<:Number where T<:Integer)
(Tuple{Array{T},Array{T}} where T) <: Tuple{Array, Array}
Tuple{Array, Array} <: (Tuple{Array{T},Array{T}} where T)
Array{(Array{T,1} where T), 1} <: (Array{Array{T,1}, 1} where T)
(Array{Array{T,1}, 1} where T) <: Array{(Array{T,1} where T), 1}
(Int where T) <: (Integer where T<:Integer)
(Tuple{T, Tuple{S}} where S where T) <: (Tuple{T, Tuple{S} where S} where T)
(Tuple{T, Tuple{S} where S} where T) <: (Tuple{T, Tuple{S}} where S where T)
(Pair{T,T} where T) <: Pair{Int,Int8}
(Pair{T,T} where T) <: Pair{Int,Int}
(Tuple{T} where T) <: Tuple{Any}
Tuple{Any} <: (Tuple{T} where T)
(Tuple{T} where T<:Real) <: Tuple{Real}
Tuple{Array{Integer,1}, Int} <: (Tuple{Array{T,1},S} where S<:T where T<:Integer)
Tuple{Array{Integer,1}, Real} <: (Tuple{Array{T,1},T} where T<:Integer)
Tuple{Int,String,Vector{Integer}} <: (Tuple{T, T, Array{T,1}} where T)
Tuple{String,Int,Vector{Integer}} <: (Tuple{T, T, Array{T,1}} where T)
Tuple{Int,String,Vector{Tuple{Integer}}} <: (Tuple{T,T,Array{Tuple{T},1}} where T)
Tuple{Int,String,Vector{Any}} <: (Tuple{T, T, Array{T,1}} where T)
Array{Int,1} <: Array{(T where T<:Int), 1}
Array{Tuple{Any},1} <: Array{(Tuple{T} where T), 1}
Array{Tuple{Int,Int},1} <: Array{(Tuple{T,T} where T<:Int), 1}
Array{(Tuple{T,T} where T<:Int), 1} <: Array{Tuple{Int,Int},1}
Array{Tuple{Int,Integer},1} <: Array{(Tuple{T,T} where T<:Integer), 1}
Pair{Int,Int8} <: (Pair{T,T} where T)
Tuple{Array{Int,1}, Integer} <: (Tuple{Array{T,1},T} where T<:Integer)
Tuple{Integer, Array{Int,1}} <: (Tuple{T, Array{T,1}} where T<:Integer)
Pair{Array{Int,1},Integer} <: (Pair{Array{T,1},T} where T)
Pair{Array{Int,1},Int} <: (Pair{Array{T,1},T} where T)
Tuple{Integer,Int} <: (Tuple{T,S} where S<:T where T<:Integer)
Tuple{Integer,Int} <: (Tuple{T,S} where S<:T where T<:Int)
Tuple{Integer,Int} <: (Tuple{T,S} where S<:T where T<:String)
Tuple{Float32,Array{Float32,1}} <: (Tuple{T,S} where S<:AbstractArray{T,1} where T<:Real)
Tuple{Float32,Array{Float64,1}} <: (Tuple{T,S} where S<:AbstractArray{T,1} where T<:Real)
Tuple{Float32,Array{Real,1}} <: (Tuple{T,S} where S<:AbstractArray{T,1} where T<:Real)
Tuple{Number,Array{Real,1}} <: (Tuple{T,S} where S<:AbstractArray{T,1} where T<:Real)
(T where Int<:T<:Integer) <: (T where T<:Real)
(Array{T,1} where Int<:T<:Integer) <: (Array{T,1} where T<:Real)
(T where Int<:T<:Integer) <: (T where Integer<:T<:Real)
(Array{T,1} where Int<:T<:Integer) <: (Array{T,1} where Integer<:T<:Real)
(Tuple{T,S} where S<:AbstractArray{T,1} where T<:Real) <: (Tuple{A,B} where B<:AbstractArray{A,1} where A<:Real)
(Tuple{T,S} where S<:AbstractArray{T,1} where T<:Real) <: (Tuple{Real,B} where B<:AbstractArray{A,1} where A<:Real)
(Tuple{Real,B} where B<:AbstractArray{A,1} where A<:Real) <: (Tuple{T,S} where S<:AbstractArray{T,1} where T<:Real)
(Pair{T,S} where S<:T where T) <: (Pair{T,S} where S where T)
(Pair{T,S} where S where T) <: (Pair{T,S} where S<:T where T)
(Pair{T,S} where S>:T where T) <: (Pair{T,S} where S where T)
(Tuple{Ref{T}, T} where T) <: (Tuple{Ref{T},S} where T<:S<:T where T)
(Tuple{Ref{T},S} where T<:S<:T where T) <: (Tuple{Ref{T}, T} where T)
(Tuple{Ref{T}, T} where T) <: (Tuple{Ref{T}, S} where S>:T where T)
(Tuple{T,Ptr{T}} where T) <: (Tuple{Ptr{T},T} where T)
(Tuple{Ptr{T},T} where T) <: (Tuple{T,Ptr{T}} where T)
(Tuple{Ptr{T},Ptr{S}} where S>:Ptr where T>:Ptr) <: (Tuple{T,Ptr{T}} where T)
(Tuple{Ptr{T},Ptr{S}} where S>:Ptr where T>:Ptr) <: (Tuple{Ptr{T},T} where T)
(Tuple{Ptr{T},Ptr{S}} where S>:Ptr where T>:Ptr) <: (Tuple{Ptr{T},Ptr{S}} where S>:Ptr{T} where T>:Ptr)
Union{(Tuple{Ptr{T},Ptr{S}} where S>:Ptr{T} where T>:Ptr), (Tuple{Ptr{S},Ptr{T}} where S>:Ptr{T} where T>:Ptr)} <: (Tuple{T,Ptr{T}} where T)
(Tuple{Ptr{T},Ptr{S}} where Ptr<:S<:Ptr where T>:Ptr) <: (Tuple{Ptr{T},Ptr{S}} where S>:Ptr{T} where T>:Ptr)
(Tuple{Ptr{T},Ptr{S}} where S>:Ptr where T>:Ptr) <: (Tuple{Ptr{T},Ptr{S}} where Ptr{T}<:S<:Ptr where T>:Ptr)
(Tuple{Ptr{T},Ptr{S}} where S>:Ptr where T>:Integer) <: (Tuple{Ptr{T},T} where T)
(Tuple{Ptr{T},Ptr{S}} where S>:Integer where T>:Ptr) <: (Tuple{Ptr{T},T} where T)
(Type{AbstractArray{Array{T}} where T}) <: (Type{AbstractArray{S}} where S)
(Val{AbstractArray{Array{T}} where T}) <: (Val{AbstractArray{T}} where T)
(Array{Array{Array{T}} where T}) <: (Array{Array{T}} where T)
(Array{Array{T, 1}, 1} where T) <: AbstractArray{Vector}
(Ref{Pair{Pair{T, R}, R} where R} where T) <: (Ref{Pair{A, B} where B} where A)
(Ref{Pair{Pair{A, B}, B} where B} where A) <: (Ref{Pair{A, B2} where B2 <: B} where A where B)
Val{1} <: Val{1}
Val{1} <: Val{2}
Val{:a} <: (Val{T} where T)
Val{true} <: (Val{T} where T)
Val{:a} <: Val{:b}
Union{} <: Union{}
Union{Int,Union{}} <: Int
DataType <: Type
Union <: Type
UnionAll <: Type
Core.TypeofBottom <: Type
Type{Int} <: DataType
Type{Union{Int,String}} <: Union
Type{Vector} <: UnionAll
Type{Union{}} <: Core.TypeofBottom
Core.TypeofBottom <: Type{Union{}}
DataType <: (Type{T} where T)
DataType <: (Type{T} where T<:Integer)
Union <: (Type{T} where T)
(Type{T} where T<:Integer) <: DataType
Type{Int} <: (Type{T} where T<:Integer)
Type{Integer} <: (Type{T} where T<:Integer)
Type{Real} <: (Type{T} where T<:Integer)
Type{Type{Int}} <: (Type{Type{T}} where T)
Union{Int,String} <: Union{Integer,AbstractString}
Union{Int,String} <: Union{Int8,String}
Union{Int,Float64,String} <: Union{Real,String}
Tuple{Union{Int,Float64}} <: Union{Tuple{Int},Tuple{Float64}}
Union{Tuple{Int},Tuple{Float64}} <: Tuple{Union{Int,Float64}}
Tuple{Union{Int,String},Union{Int,String}} <: (Tuple{T,T} where T)
Tuple{Union{Int,String},Union{Int,String}} <: Union{Tuple{Int,Int},Tuple{String,String},Tuple{Int,String},Tuple{String,Int}}
Union{Tuple{Int,Int},Tuple{String,String}} <: (Tuple{T,T} where T)
Ref{Union{Int,String}} <: (Ref{T} where T>:Int)
Ref{Union{Int,String}} <: (Ref{Union{T,String}} where T)
Ref{Union{Int,String}} <: (Ref{Union{T,S}} where {T,S})
Ref{Union{Int,Float64,String}} <: (Ref{Union{T,String}} where T<:Real)
(Ref{Union{T,Int}} where T) <: (Ref{Union{S,Int}} where S)
(Union{T,Int} where T<:Real) <: Real
(Tuple{Vararg{Int,N}} where N) <: (Tuple{Vararg{T,N}} where {T,N})
(Tuple{Vararg{T,N}} where {T,N}) <: (Tuple{Vararg{Int,N}} where N)
(Tuple{Vararg{Int,N}} where N) <: (Tuple{Int, Vararg{Int,N}} where N)
(Tuple{Int, Vararg{Int,N}} where N) <: (Tuple{Vararg{Int,N}} where N)
(Tuple{Vararg{Int,N}} where N) <: Tuple{Int,Int}
(Tuple{Vararg{Int,N}} where N) <: Tuple{Vararg{Int}}
Tuple{Vararg{Int}} <: (Tuple{Vararg{Int,N}} where N)
Tuple{Int,Int,Int} <: (Tuple{Vararg{T,N}} where {T,N})
Tuple{Int,String} <: (Tuple{Vararg{T,N}} where {T,N})
Tuple{Int,Vararg{Int}} <: (Tuple{Vararg{T,N}} where {T,N})
(Tuple{Int,Vararg{Int,N}} where N) <: (Tuple{Vararg{Int,M}} where M)
(Tuple{Vararg{Int,N}} where N) <: (Tuple{Int,Int,Vararg{Int,M}} where M)
(Tuple{Int,Vararg{Int,N}} where N) <: (Tuple{Int,Int,Vararg{Int,M}} where M)
(Tuple{NTuple{N,Int},NTuple{N,Int}} where N) <: (Tuple{NTuple{N,Int},NTuple{M,Int}} where {N,M})
(Tuple{NTuple{N,Int},NTuple{M,Int}} where {N,M}) <: (Tuple{NTuple{N,Int},NTuple{N,Int}} where N)
(Tuple{Vararg{Any,N}} where N) <: (Tuple{Vararg{Any,N}} where N)
Tuple{Int,Vararg{Int,2}} <: (Tuple{Vararg{Int,N}} where N)
(Tuple{Int,Vararg{Int,N}} where N) <: Tuple{Int,Vararg{Int,2}}
(Tuple{Vararg{T}} where T<:Integer) <: (Tuple{Vararg{T}} where T)
Tuple{Int,Int8} <: (Tuple{Vararg{T}} where T<:Integer)
Tuple{Int,Int8} <: (Tuple{T,Vararg{T}} where T)
Tuple{Int,Int,Int} <: (Tuple{T,Vararg{T}} where T)
Tuple{Int,Vararg{Int}} <: (Tuple{T,Vararg{T}} where T)
Tuple{Int,Vararg{Integer}} <: (Tuple{T,Vararg{T}} where T)
(Tuple{Int,Vararg{Int,N}} where N) <: (Tuple{T,Vararg{T}} where T)
Tuple{Type{Int},Type{Int8}} <: (Tuple{T,T} where T)
Tuple{Type{Int},DataType} <: (Tuple{T,T} where T)
Tuple{DataType,DataType} <: (Tuple{T,T} where T)
Tuple{Type{Int},Type{Int}} <: (Tuple{Type{T},Type{T}} where T)
Tuple{Int,Vararg{Type{Int}}} <: (Tuple{T,Vararg{T}} where T)
Tuple{Type{Int},Vararg{Type{Int8}}} <: (Tuple{T,Vararg{T}} where T)
Tuple{Union{},Int} <: Tuple{Int,Int}
Tuple{Union{Int,Nothing},Nothing} <: (Tuple{Union{T,Nothing},T} where T)
(Union{Ref{T},T} where T) <: (Union{Ref{S},S} where S)
(Ref{T} where T<:Union{Int,String}) <: Ref{<:Union{Int,String,Nothing}}
(Ref{T} where T>:Union{Int,String}) <: (Ref{T} where T>:Int)
(Ref{S} where S<:T where T<:Integer) <: (Ref{U} where U<:Real)
(Tuple{T,S} where S<:Ref{T} where T) <: (Tuple{A,B} where B<:Ref{A} where A)
(Tuple{T,Ref{S}} where S<:T where T) <: (Tuple{A,Ref{B}} where B where A)
Tuple{Ref{Int},Int} <: (Tuple{Ref{S},T} where S<:T where T)
Tuple{Ref{Integer},Int} <: (Tuple{Ref{S},T} where S<:T where T)
Pair{Int,Union{}} <: (Pair{T,S} where S<:T where T)
(Pair{T,S} where S>:T where T>:Int) <: (Pair{A,B} where B>:Integer where A)
Ref{Ref{Int}} <: (Ref{Ref{T}} where T<:Integer)
(Ref{Ref{T}} where T) <: (Ref{S} where S<:Ref)
Ref{Ref} <: (Ref{S} where S<:Ref)
Ref{Ref{T} where T} <: (Ref{Ref{T}} where T)
Ref{Union{Ref{Int},Ref{String}}} <: (Ref{Union{Ref{T},Ref{String}}} where T)
(Ref{Union{T,Ref{T}}} where T) <: (Ref{Union{S,Ref{S}}} where S)
Tuple{Int,Int} <: Tuple{Int,Int,Vararg{Int,0}}
Tuple{Int,Vararg{Int,0}} <: Tuple{Int}
(Tuple{Int,Vararg{Int,N}} where N) <: Tuple{Int}
Tuple{Int} <: (Tuple{Int,Vararg{Int,N}} where N)
Tuple{Int,Int} <: (Tuple{Vararg{Int,N}} where N<:Int)
Tuple{Vararg{Union{Int,String}}} <: Tuple{Vararg{Union{Int,String,Nothing}}}
Tuple{Vararg{Union{Int,String}}} <: (Tuple{Vararg{T}} where T)
(Tuple{Vararg{Union{T,Int}}} where T) <: Tuple{Vararg{Any}}
Union{Tuple{Int},Tuple{Int,Int}} <: Tuple{Vararg{Int}}
Union{Tuple{},Tuple{Int},Tuple{Int,Int,Vararg{Int}}} <: Tuple{Vararg{Int}}
Tuple{Vararg{Int}} <: Union{Tuple{},Tuple{Int},Tuple{Int,Int,Vararg{Int}}}
Union{Tuple{},Tuple{Int},Tuple{Int,Int},Tuple{Int,Int,Int,Vararg{Int}}} <: Tuple{Vararg{Int}}
Union{Tuple{Int,Vararg{Int}},Tuple{}} <: Tuple{Vararg{Int}}
Union{Vector{Int},Vector{String}} <: (Vector{T} where T)
(Vector{T} where T) <: Union{Vector{Int},Vector{String}}
Union{Ref{Int},Int} <: (Union{Ref{T},T} where T)
Union{Ref{Int},String} <: (Union{Ref{T},T} where T)
# isa (jl_isa, subtype.c:2477-2529)
isa(Int, DataType)
isa(Int, Type{Int})
isa(Int, Type{Integer})
isa(Vector, UnionAll)
isa(Union{Int,String}, Union)
isa(Int, Type{<:Integer})
isa(Int, Type{<:AbstractString})
isa(Integer, Type{T} where T>:Signed)
isa(Int, Type{T} where T>:Signed)
isa(Int, Type{T} where Int<:T<:Real)
isa(Int, Type{Type{T}} where T)
isa(1, Int)
isa(1, Integer)
isa(1, Real)
isa(1, String)
isa(1, Union{Int,String})
isa(:a, Symbol)
isa(true, Bool)
isa(true, Integer)
isa(Union{}, Core.TypeofBottom)
isa(Union{}, Type{Union{}})
isa(Union{}, Type{Int})
isa(Int, Type)
isa(Int, Any)
isa(Vector{Int}, Type{<:AbstractVector})
isa(Vector{Int}, Type{<:Vector})
isa(Int, Union{Type{Int},Int})
isa(Int, Union{DataType,Int})
isa(Int, Union{Type{T} where T<:Integer, String})
isa(Pair{Int,T} where T, Type{<:Pair})
isa(Pair{Int,T} where T, UnionAll)
isa(Vector{T} where T, Type{Vector{T} where T})
isa(Union{Int,String}, Type{<:Union{Int,String,Nothing}})
isa(Union{Int,String}, DataType)
isa(Int, Ref{Int})
isa(1, Type{Int})
isa(1, Val{1})
isa(Val{1}, Type{Val{T}} where T)
isa(Tuple{Int}, Type{Tuple{T}} where T<:Integer)
isa(Int, Type{T} where T<:Union{Int,String})
isa(Int, Type{T} where T>:Union{Int,String})
isa(Int, Union{Type{T} where T>:Int, Nothing})
# type equality (jl_types_equal, subtype.c:2319-2399)
Int == Int
Int == Integer
Integer == Int
Vector{Int} == Array{Int,1}
(Vector{T} where T) == (Array{T,1} where T)
(Vector{T} where T) == (Array{T,N} where {T,N})
Union{Int,String} == Union{String,Int}
Union{Int,String} == Union{Int,String,Nothing}
Union{Int,Integer} == Integer
Tuple{Int,Int} == Tuple{Vararg{Int,2}}
(Tuple{Vararg{Int,N}} where N) == Tuple{Vararg{Int}}
Tuple{Vararg{Int}} == (Tuple{Vararg{Int,N}} where N)
(Tuple{T} where T<:Int) == Tuple{Int}
Tuple{Int} == (Tuple{T} where T<:Int)
Type{Int} == DataType
DataType == Type{Int}
Type{Union{}} == Core.TypeofBottom
Core.TypeofBottom == Type{Union{}}
Union{} == Union{}
Any == Union{Int,Any}
(Ref{T} where T) == Ref
Ref{Int} == (Ref{T} where T<:Int)
(Ref{T} where Int<:T<:Int) == Ref{Int}
(Pair{T,T} where T) == (Pair{S,S} where S)
(Pair{T,S} where {T,S}) == Pair
Tuple{Int,String} == Tuple{Int,AbstractString}
Union{Int8,Int} == Union{Int,Float64}
(Tuple{Int,Vararg{Int}}) == Tuple{Vararg{Int}}
Union{Tuple{},Tuple{Int,Vararg{Int}}} == Tuple{Vararg{Int}}
Tuple{3} == Tuple{3}
Tuple{3} == Tuple{4}
Tuple{:a} == Tuple{:b}
Val{1} == Val{1}
Ref{Union{}} == Ref{Union{}}
# subtyping environments (jl_subtype_env, subtype.c:2246-2297)
env(Vector{Int}, Vector{T} where T)
env(Vector{Int}, Vector{T} where T<:Real)
env(Vector{String}, Vector{T} where T<:Real)
env(Pair{Int,String}, Pair{A,B} where {A,B})
env(Pair{Int,Int}, Pair{A,A} where A)
env(Tuple{Int,Int}, Tuple{T,T} where T)
env(Tuple{Int,Int8}, Tuple{T,T} where T)
env(Tuple{Int,Int8}, Tuple{T,S} where {T,S})
env(Tuple{Int,Vararg{Int}}, Tuple{Vararg{T,N}} where {T,N})
env(Tuple{Int,Int,Int}, Tuple{Vararg{T,N}} where {T,N})
env(Tuple{}, Tuple{Vararg{T,N}} where {T,N})
env(Tuple{Int}, Tuple{T} where T>:Int)
env(Tuple{Integer}, Tuple{T} where T)
env(Tuple{Union{Int,String}}, Tuple{T} where T)
env(Union{Tuple{Int},Tuple{String}}, Tuple{T} where T)
env(Tuple{Type{Int}}, Tuple{T} where T)
env(Tuple{Type{Int}}, Tuple{Type{T}} where T)
env(Tuple{Vector{Int}, Int}, Tuple{Vector{T}, T} where T)
env(Tuple{Vector{T}} where T, Tuple{Vector{S}} where S)
env(Vector{T} where T, Vector{S} where S)
env(Tuple{Vector{T}} where T, Tuple{S} where S)
env(Tuple{Ref{T}} where T<:Integer, Tuple{Ref{S}} where S<:Real)
env(Ref{Vector{T}} where T, Ref{Vector{S}} where S)
env(Tuple{Int,Vararg{Int,N}} where N, Tuple{Vararg{Int,M}} where M)
env(Int, Any)
env(Union{}, Vector{T} where T)
env(Tuple{Pair{Int,T}} where T, Tuple{Pair{A,B}} where {A,B})
env(Tuple{Union{Int,Nothing}}, Tuple{Union{T,Nothing}} where T)
env(Pair{Vector{Int},Int}, Pair{Vector{T},T} where T)
env(Tuple{Int,String}, Tuple{Union{T,String},T} where T)
# constructions julia refuses (the answer is E on both sides)
Tuple{Union{}} <: Any
Tuple{Int,Union{}} <: Any
Array{Int,1,2} <: Any
Int{Int} <: Any
Vararg{Int,-1} <: Any
Tuple{Vararg{Int,-1}} <: Any
Tuple{Vararg{Int,:a}} <: Any
Tuple{Vararg{Vararg{Int}}} <: Any
Union{1} <: Any
Union{Int,1} <: Any
Array{Int,Int} <: Any
(Ref{T} where T<:1) <: Any
(Ref{T} where T>:1) <: Any
Type{Int,Int} <: Any
Tuple{Vararg{Union{}}} <: Tuple{}
Tuple{Int,Vararg{Union{}}} <: Tuple{Int}
Tuple{Vararg{Union{},0}} <: Tuple{}
Tuple{Vararg{Union{},2}} <: Any
Tuple{Vararg{Int,0}} <: Tuple{}
NTuple{2,Int} <: Tuple{Int,Int}
NTuple{0,Int} <: Tuple{}
NTuple{3,Union{Int,String}} <: Tuple{Vararg{Union{Int,String}}}
NTuple{2,T} where T <: (Tuple{T,T} where T)
(NTuple{N,Int} where N) <: Tuple{Vararg{Int}}
Tuple{Int,Vararg{Int,0}} <: Tuple{Int}
Tuple{Int,Vararg{T,2}} where T <: Tuple{Int,Any,Any}
(Tuple{Int,Vararg{T,2}} where T) <: Tuple{Int,Any,Any}
Tuple{Vararg{Vector{T} where T,2}} <: Tuple{Vector,Vector}
Tuple{Vararg{(Vector{T} where T),2}} <: Tuple{Vector,Vector}
Union{Tuple{},Tuple{Int},Tuple{Int,Int},Tuple{Int,Int,Vararg{Int}}} == Tuple{Vararg{Int}}
Union{Tuple{Int},Tuple{Int,Int},Tuple{Int,Int,Int,Vararg{Int}}} == Tuple{Int,Vararg{Int}}
Union{Tuple{},Tuple{String,Vararg{String}}} == Tuple{Vararg{String}}
Union{Tuple{Int,Int},Tuple{Int,Int,Int,Vararg{Int}}} <: Tuple{Int,Int,Vararg{Int}}
Union{Tuple{Int8},Tuple{Int8,Int8,Vararg{Int8}},Tuple{}} == Tuple{Vararg{Int8}}
Union{Vector{T} where T, Int} <: Any
Union{(Vector{T} where T), (Ref{T} where T)} <: (Union{Vector{T},Ref{T}} where T)
(Union{Vector{T},Ref{T}} where T) <: Union{(Vector{T} where T), (Ref{T} where T)}
Union{Union{Vector{T},Ref{T}} where T, Int} <: Any
Union{(Union{Vector{T},Ref{T}} where T), Int} <: Union{Int,(Vector{T} where T),(Ref{T} where T)}
Union{Type{Int},Type{Int8}} <: DataType
Union{Type{Int},DataType} <: DataType
Union{DataType,Type{Int}} == DataType
Union{Core.TypeofBottom,Type{Union{}}} == Core.TypeofBottom
Union{Type{Union{}},Type{Int}} <: DataType
Union{Type{Int},Type{Vector}} <: Union{DataType,UnionAll}
(Ref{Union{T,Int}} where T<:Int8) <: (Ref{Union{S,Int}} where S<:Integer)
(Tuple{T,Union{T,Nothing}} where T<:Int) <: (Tuple{S,Union{S,Nothing}} where S)
(Ref{T} where T<:Union{Int,Nothing}) <: (Ref{S} where S<:Union{Integer,Nothing})
Ref{Union{Int,Nothing}} <: (Ref{Union{T,Nothing}} where T<:Int)
(Ref{Union{T,Nothing}} where T) <: (Ref{S} where S>:Nothing)
(Tuple{T} where T<:Union{Int,String}) <: Union{Tuple{Int},Tuple{String}}
Union{Tuple{Int},Tuple{String}} <: (Tuple{T} where T<:Union{Int,String})
(Tuple{T,T} where T<:Union{Int,String}) <: Union{Tuple{Int,Int},Tuple{String,String}}
Tuple{Union{Int,Nothing},Union{Int,Nothing}} <: (Tuple{T,T} where T>:Nothing)
(Tuple{Union{T,Nothing},T} where T) <: (Tuple{Union{S,Nothing},S} where S)
(Tuple{Union{Int,T}} where T) <: (Tuple{Union{Int,S}} where S<:Real)
(Tuple{T,S} where {T<:Integer, S<:T}) <: (Tuple{T,T} where T<:Integer)
(Tuple{T,Ref{S}} where {T, S<:T}) <: (Tuple{A,Ref{A}} where A)
(Tuple{Ref{T},Ref{S}} where {T, S<:T}) <: (Tuple{Ref{A},Ref{B}} where {A, B<:A})
(Tuple{Ref{T},Ref{S}} where {T, S>:T}) <: (Tuple{Ref{A},Ref{B}} where {A, B<:A})
(Tuple{T,Ref{T}} where T<:Integer) <: (Tuple{Integer,Ref{S}} where S<:Integer)
(Tuple{Vector{T},T} where T<:Integer) <: (Tuple{Vector{S},Integer} where S)
(Ref{T} where T<:Vector{S} where S<:Integer) <: (Ref{U} where U<:Vector)
(Ref{Ref{T}} where T) <: (Ref{U} where U<:(Ref{S} where S))
(Tuple{T,Vararg{T}} where T<:Integer) <: (Tuple{Vararg{T}} where T<:Integer)
(Tuple{Vararg{T,N}} where {T,N}) <: (Tuple{Vararg{S,M}} where {S,M})
(Tuple{Vararg{T,N}} where {T<:Integer,N}) <: (Tuple{Vararg{Integer,M}} where M)
(Tuple{Vararg{Int,N}} where N>:Int) <: Any
(Tuple{Vararg{Int,N}} where N) <: (Tuple{Int,Vararg{Int,N}} where N)
(Tuple{Int,Int,Vararg{Int,N}} where N) <: (Tuple{Vararg{Int,M}} where M)
(Tuple{Vararg{Int,N}} where N) <: (Tuple{Int,Int,Vararg{Int,M}} where M)
(Tuple{Int,Vararg{Int,N}} where N) <: (Tuple{Int,Int,Vararg{Int,N}} where N)
(Tuple{NTuple{N,Int},Vararg{Int,N}} where N) <: (Tuple{NTuple{M,Int},Vararg{Int,M}} where M)
(Tuple{NTuple{N,Int},Vararg{Int,N}} where N) <: (Tuple{Tuple,Vararg{Int}})
Tuple{Tuple{Int,Int},Int,Int} <: (Tuple{NTuple{N,Int},Vararg{Int,N}} where N)
Tuple{Tuple{Int,Int},Int} <: (Tuple{NTuple{N,Int},Vararg{Int,N}} where N)
Tuple{Tuple{},Vararg{Int}} <: (Tuple{NTuple{N,Int},Vararg{Int,N}} where N)
(Tuple{NTuple{N,Int},Vararg{Int}} where N) <: (Tuple{NTuple{M,Int},Vararg{Int,M}} where M)
(Tuple{Val{N},Vararg{Int,N}} where N) <: (Tuple{Val{M},Vararg{Int,M}} where M)
Tuple{Val{2},Int,Int} <: (Tuple{Val{N},Vararg{Int,N}} where N)
Tuple{Val{2},Int} <: (Tuple{Val{N},Vararg{Int,N}} where N)
Tuple{Val{1},Vararg{Int}} <: (Tuple{Val{N},Vararg{Int,N}} where N)
(Tuple{Vararg{Int,N}} where N) <: (Tuple{Vararg{Int,N}} where N<:Integer)
(Tuple{Vararg{Int,N}} where N) <: Tuple{Vararg{Any,2}}
Tuple{Int,Int} <: (Tuple{Int,Vararg{Int,N}} where N)
Tuple{Int,Int,Int} <: (Tuple{Int,Vararg{T,N}} where {T,N})
Tuple{Int,Vararg{Int,2}} <: (Tuple{Vararg{T,N}} where {T,N})
(Tuple{Vararg{Int,2}}) <: (Tuple{Vararg{Int,N}} where N>:Int)
Tuple{Vararg{Type{Int}}} <: (Tuple{Vararg{T}} where T)
Tuple{Type{Int},Type{Int}} <: (Tuple{Vararg{T}} where T)
Tuple{Type{Int},Type{Int8}} <: (Tuple{Vararg{T}} where T)
Tuple{Type{Int},Type{Int},Type{Int}} <: (Tuple{T,Vararg{T}} where T<:DataType)
Tuple{Int,Int,Union{Int,String}} <: (Tuple{Vararg{T}} where T)
Tuple{Union{Int,String},Vararg{Int}} <: (Tuple{Vararg{T}} where T)
Tuple{Int,Vararg{Union{Int,String}}} <: (Tuple{T,Vararg{T}} where T)
Tuple{Vararg{Any}} <: (Tuple{T,Vararg{T}} where T)
Tuple{Integer,Vararg{Int}} <: (Tuple{T,Vararg{T}} where T)
Tuple{Vector{Int},Vararg{Vector{Int}}} <: (Tuple{Vararg{Vector{T}}} where T)
Tuple{Vector{Int},Vector{Int8}} <: (Tuple{Vararg{Vector{T}}} where T)
Tuple{Int,Int} <: (Tuple{Vararg{T}} where T<:Union{Int,String})
Tuple{Int,String} <: (Tuple{Vararg{T}} where T<:Union{Int,String})
Tuple{Int,Int,Int} <: (Tuple{Vararg{T,3}} where T)
Tuple{Int,Int,Int8} <: (Tuple{Vararg{T,3}} where T)
Tuple{Int,Vararg{Int}} <: Tuple{Vararg{Int,3}}
Tuple{Vararg{Int}} <: Tuple{Int,Vararg{Int,2}}
(Tuple{Vararg{T}} where T) <: Tuple{Vararg{Any,2}}
Tuple{Int,Int} <: Tuple{Vararg{Union{Int,String}}}
Tuple{Int,Union{Int,String},Vararg{Union{Int,String}}} <: Tuple{Vararg{Union{Int,String}}}
Tuple{Vararg{Union{Int,String}}} <: Tuple{Union{Int,String},Vararg{Union{Int,String}}}
Union{} <: (Tuple{T} where T)
(Tuple{T} where T<:Union{}) <: Tuple{Int}
(Ref{T} where T<:Union{}) <: Ref{Union{}}
(T where T<:Union{}) <: Int
(Ref{T} where Int<:T<:Int) <: Ref{Int}
(Ref{T} where Int<:T<:Integer) <: (Ref{S} where S<:Integer)
(Ref{T} where T>:Int) <: (Ref{S} where S>:Integer)
(Ref{T} where T>:Integer) <: (Ref{S} where S>:Int)
Union{Ref{Int}, Ref{Integer}} <: (Ref{T} where T>:Int)
Union{Ref{Int}, Ref{Integer}} <: (Ref{T} where Int<:T<:Integer)
(Union{Ref{T}, Vector{T}} where T>:Int) <: (Union{Ref{S}, Vector{S}} where S>:Int)
Val{Union{}} <: (Val{T} where T)
Val{Int} <: (Val{T} where T<:Integer)
Val{Int} <: (Val{T} where T<:Type{Int})
Val{1} <: (Val{T} where T<:Integer)
Val{true} <: (Val{T} where T<:Bool)
(Val{T} where T<:Int) <: Val
Type{Val{1}} <: (Type{Val{T}} where T)
Type{Int} <: Type{<:Type}
Type{DataType} <: Type{<:Type}
Type{Type{Int}} <: Type{<:Type}
Type{Vector} <: Type{<:UnionAll}
Type{Type{T} where T} <: Type{<:UnionAll}
Type{Type} <: (Type{Type{T}} where T)
Type{Union{Int,String}} <: Type{<:Union}
Type{Union{Int,String}} <: (Type{Union{T,String}} where T)
(Type{T} where T<:Union{Int,String}) <: (Type{T} where T<:Union{Int,String,Nothing})
(Type{T} where T<:Int) <: (Type{T} where T<:Integer)
DataType <: (Type{T} where T<:Any)
UnionAll <: (Type{T} where T>:Int)
Core.TypeofBottom <: (Type{T} where T)
Core.TypeofBottom <: (Type{T} where T<:Int)
Core.TypeofBottom <: DataType
Type{Union{}} <: DataType
Type{Union{}} <: (Type{T} where T<:Int)
Type{Core.TypeofBottom} <: (Type{T} where T)
(Type{T} where T>:Int) <: Type
(Type{T} where T>:Int) <: DataType
Tuple{DataType} <: (Tuple{Type{T}} where T)
Tuple{UnionAll} <: (Tuple{Type{T}} where T)
Tuple{Union} <: (Tuple{Type{T}} where T<:Union{Int,String})
Ref{DataType} <: (Ref{T} where T<:Type)
Tuple{Type{Int}, Int} <: (Tuple{Type{T}, T} where T)
Tuple{Type{Integer}, Int} <: (Tuple{Type{T}, T} where T)
Tuple{Type{Int}, Integer} <: (Tuple{Type{T}, T} where T)
Tuple{Type{Vector{Int}}, Int} <: (Tuple{Type{Vector{T}}, T} where T)
Tuple{Type{Vector{T}} where T, Int} <: (Tuple{Type{Vector{T}}, T} where T)
Type{Vector{T}} where T <: (Type{Vector{T}} where T)
(Type{Vector{T}} where T) <: (Type{Vector{T}} where T)
(Type{Vector{T}} where T) <: Type{Vector}
Type{Vector} <: (Type{Vector{T}} where T)
Type{Vector} <: Type{<:Vector}
Type{Array{T,1} where T} <: Type{Vector}
Union{Int8,Union{Int16,Int}} <: Union{Union{Int,Int8},Int16}
Union{Nothing,Missing,Int} <: Union{Int,Missing,Nothing}
Union{Int,Missing} <: Union{Int8,Missing,Int}
Union{Tuple{Nothing},Tuple{Missing}} <: Tuple{Union{Nothing,Missing}}
Union{Pair{Nothing,Missing},Pair{Int,Int}} <: Pair
Union{Pair{Int,Int},Pair{Nothing,Nothing}} <: (Pair{T,T} where T)
Union{Val{1},Val{2},Val{:a}} <: (Val{T} where T)
Union{Val{1},Val{2},Val{:a}} <: Union{Val{1},Val{:a}}
Union{Some{Int},Some{Nothing}} <: (Some{T} where T)
Union{Some{Int},Nothing} <: Union{Some{T},Nothing} where T
Union{Ptr{Int},Ptr{Nothing}} <: Ptr
Union{Ptr{Int},Ptr{Nothing}} <: (Ptr{T} where T<:Union{Int,Nothing})
Union{Missing,Nothing,Tuple{},Val{1}} <: Union{Missing,Val{1},Nothing,Tuple{}}
Union{Char,Bool,Missing,Int8,Nothing} <: Union{Nothing,Missing,Int8,Bool,Char}
Union{Ref{Int},Vector{Int},Pair{Int,Int},Int} <: Union{Int,Pair{Int,Int},Vector{Int},Ref{Int}}
Union{Union{Vector{T},Ref{T}} where T, Nothing} <: Any
# applying UnionAll aliases (jl_apply_type, jltypes.c:1412-1473)
(Vector{T} where T<:Real){String} <: Any
(Vector{T} where T<:Real){Int} <: Vector{Int}
(Ref{T} where T<:Integer){Int} <: Ref{Int}
(Ref{T} where T>:Integer){Int} <: Any
(Pair{A,B} where B<:A where A){Int,String} <: Any
(Pair{A,B} where B<:A where A){Integer,Int} <: Pair{Integer,Int}
(Ref{T} where T){Int,Int} <: Any
(NTuple{0,T} where T){Int} <: Tuple{}
NTuple{2}{Int} <: Tuple{Int,Int}
NTuple{3}{Union{Int,String}} <: Tuple{Vararg{Union{Int,String}}}
(NTuple{N,T} where {N,T}){2,Int} <: Tuple{Int,Int}
(NTuple{N,T} where {N,T}){0,Int} <: Tuple{}
(NTuple{N,T} where {N,T}){-1,Int} <: Any
(NTuple{N,T} where {N,T}){:a,Int} <: Any
(Tuple{Vararg{T,N}} where {T,N}){Int,2} <: Tuple{Int,Int}
(Tuple{Vararg{T,N}} where {T,N}){Int} <: Tuple{Vararg{Int}}
(Tuple{T,Vararg{T,N}} where {T,N}){Int,2} <: Tuple{Int,Int,Int}
(Tuple{Vararg{T,N}} where {N,T}){2} <: Any
Array{Int}{1} <: Vector{Int}
Array{Int}{1,2} <: Any
Array{String}{2} <: Array{String,2}
(Array{T,1} where T){Int} <: Vector{Int}
(Union{Vector{T},Ref{T}} where T){Int} <: Union{Vector{Int},Ref{Int}}
(Union{Vector{T},T} where T){Int} <: Union{Vector{Int},Int}
(Union{T,Nothing} where T){Union{}} <: Nothing
(Union{T,Nothing} where T){Int} <: Union{Int,Nothing}
(Type{T} where T){Int} <: Type{Int}
(Type{T} where T){Union{}} <: Core.TypeofBottom
(Type{T} where T){Type{Union{}}} <: Type{Core.TypeofBottom}
Type{Type{Union{}}} <: Type{Core.TypeofBottom}
Type{Core.TypeofBottom} <: Type{Type{Union{}}}
(Ref{T} where T){Vector} <: Ref{Vector}
(Ref{T} where T){Array{T,1} where T} <: Ref{Vector}
Ref{Array{T,1} where T} <: Ref{Vector}
(Val{T} where T){1} <: Val{1}
(Tuple{T} where T<:Real){Int} <: Tuple{Int}
(Tuple{T} where T<:Real){String} <: Any
(Tuple{Ref{T}} where T<:Real){Int} <: Tuple{Ref{Int}}
(Tuple{T,S} where S<:T where T){Integer,Int} <: Tuple{Integer,Int}
(Tuple{T,S} where S<:T where T){Int,Integer} <: Any
(Ref{T} where T<:Ref{S} where S){Int8} <: Any
(Ref{T} where T<:(Ref{S} where S<:Integer)){Ref{Int}} <: Ref{Ref{Int}}
(Pair{T,Vector{T}} where T){Int} <: Pair{Int,Vector{Int}}
(Pair{Vector{T},T} where T<:Real){Int} <: Pair{Vector{Int},Int}
(Pair{Tuple{T},Vararg{T}} where T){Int} <: Any
(Tuple{Pair{T,S} where S<:T} where T){Integer} <: Tuple{Pair{Integer,S} where S<:Integer}
(Union{Tuple{T},Tuple{T,T}} where T<:Integer){Int} <: Union{Tuple{Int},Tuple{Int,Int}}
# Core.apply_type's checks (jl_f_apply_type, builtins.c:1547-1595)
Tuple{Vararg{Int},Int} <: Any
Vararg{Int,2,3} <: Any
Tuple{Vararg{Int}{2}} <: Tuple{Int,Int}
Tuple{Vararg{Int}{1,2}} <: Any
Tuple{Vararg{Int,2}{3}} <: Any
Vector{Vararg{Int}} <: Any
Int{1} <: Any
Tuple{Vararg{Int,Union{Int,String}}} <: Any
Tuple{Vararg{Union{Tuple{T} where T<:Int, Nothing}}} <: Tuple{Vararg{Union{Tuple{Int},Nothing}}}
Tuple{Vararg{(Tuple{T} where T<:Int)}} <: Tuple{Vararg{Tuple{Int}}}
Tuple{Vararg{(Tuple{T,Ref{T}} where T<:Int)}} <: Any
Tuple{Vararg{(Tuple{Ref{S} where S<:T} where T<:Int)}} <: Any
Tuple{Vararg{(Tuple{T, Vararg{T}} where T<:Int)}} <: Any
Tuple{Vararg{(Tuple{Vararg{T, 2}} where T<:Int)}} <: Any
Tuple{Vararg{(Tuple{Vararg{Int, N}} where N)}} <: Any
Tuple{Vararg{(Tuple{Union{T, Nothing}} where T<:Int)}} <: Any
Tuple{Vararg{(Tuple{Union{Ref{T}, Nothing}} where T<:Int)}} <: Any
Tuple{Vararg{(Tuple{T} where Int<:T<:Int)}} <: Tuple{Vararg{Tuple{Int}}}
Ref{Union{(Tuple{T} where T<:Int), Nothing}} <: Ref{Union{Tuple{Int},Nothing}}
Type{1} <: Any
Type{:a} <: Type
isa(1, Type{1})
Tuple{Type{1}} <: Tuple{Any}
# free type variables (tolerated, subtype.c:19-20)
let T = TypeVar(:T); T <: T end
let T = TypeVar(:T); T <: Any end
let T = TypeVar(:T); Int <: T end
let T = TypeVar(:T, Integer); T <: Real end
let T = TypeVar(:T, Integer); T <: Int end
let T = TypeVar(:T, Int, Integer); Int <: T end
let T = TypeVar(:T, Int, Integer); Signed <: T end
let T = TypeVar(:T, Integer), S = TypeVar(:S, Real); T <: S end
let T = TypeVar(:T, Integer), S = TypeVar(:S, Real); S <: T end
let T = TypeVar(:T, Int, Integer), S = TypeVar(:S, Int, Real); T <: S end
let T = TypeVar(:T); Vector{T} <: Vector end
let T = TypeVar(:T); Vector{T} <: Vector{T} end
let T = TypeVar(:T), S = TypeVar(:S); Vector{T} <: Vector{S} end
let T = TypeVar(:T); Vector{T} <: (Vector{S} where S) end
let T = TypeVar(:T); Tuple{T,T} <: (Tuple{S,S} where S) end
let T = TypeVar(:T, Integer); Tuple{T} <: Tuple{Real} end
let T = TypeVar(:T); Union{T,Int} <: Union{T,Integer} end
let T = TypeVar(:T); Union{T,Int} <: Any end
let T = TypeVar(:T); Union{T,Int} <: T end
let T = TypeVar(:T); Ref{Union{T,Int}} <: (Ref{Union{S,Int}} where S) end
let T = TypeVar(:T); isa(Vector{T}, UnionAll) end
let T = TypeVar(:T); isa(Vector{T}, DataType) end
let T = TypeVar(:T); isa(Vector{T}, Type{<:Vector}) end
let T = TypeVar(:T); Vector{T} == Vector{T} end
let T = TypeVar(:T), S = TypeVar(:S); Vector{T} == Vector{S} end
let T = TypeVar(:T); env(Tuple{Vector{T}}, Tuple{S} where S) end
let T = TypeVar(:T); env(Tuple{Vector{T}, T}, Tuple{S, R} where {S, R}) end
let T = TypeVar(:T, Integer); env(Tuple{T}, Tuple{S} where S) end
let T = TypeVar(:T, Integer); env(Tuple{T}, Tuple{S} where S<:Real) end
let T = TypeVar(:T); env(Ref{Vector{T}}, Ref{S} where S) end
let T = TypeVar(:T); env(Ref{Pair{T,Int}}, Ref{S} where S) end
let T = TypeVar(:T), S = TypeVar(:S); env(Tuple{Pair{T,S}}, Tuple{R} where R) end
let N = TypeVar(:N); Tuple{Vararg{Int,N}} <: Tuple{Vararg{Int}} end
let N = TypeVar(:N); Tuple{Vararg{Int,N}} <: (Tuple{Vararg{Int,M}} where M) end
let N = TypeVar(:N); Tuple{Int,Int} <: Tuple{Vararg{Int,N}} end
# bound Vararg lengths (subtype_tuple_varargs, subtype.c:1070-1218)
(Tuple{Int,Vararg{T,3}} where T) <: (Tuple{Vararg{Int,N}} where N)
(Tuple{Vararg{T,3}} where T) <: (Tuple{Int,Vararg{Int,N}} where N)
(Tuple{Vararg{T,2}} where T) <: (Tuple{Vararg{S,N}} where {S,N})
(Tuple{Int,Vararg{T,2}} where T) <: (Tuple{Vararg{S,N}} where {S,N})
(Tuple{Vararg{T,2}} where T) <: Tuple{Any,Any}
(Tuple{Vararg{T,2}} where T) <: Tuple{Any,Any,Any}
(Tuple{Vararg{T,0}} where T) <: Tuple{}
(Tuple{Vararg{T,1}} where T<:Int) <: Tuple{Int}
Tuple{Int,Int} <: (Tuple{Vararg{T,2}} where T)
Tuple{Int,Int8} <: (Tuple{Vararg{T,2}} where T)
Tuple{Int,Int,Int} <: (Tuple{Int,Vararg{T,2}} where T)
(Tuple{Vararg{Int,N}} where N) <: (Tuple{Vararg{T,2}} where T)
Tuple{Tuple{Int,Int},Int,Int,Int} <: (Tuple{NTuple{N,Int},Vararg{Int,N}} where N)
(Tuple{Tuple{Int,Int},Int,Vararg{Int,M}} where M) <: (Tuple{NTuple{N,Int},Vararg{Int,N}} where N)
(Tuple{Tuple{Int,Int},Vararg{Int,M}} where M) <: (Tuple{NTuple{N,Int},Int,Vararg{Int,N}} where N)
(Tuple{Tuple{Int,Int,Int},Int,Vararg{Int,M}} where M) <: (Tuple{NTuple{N,Int},Int,Int,Vararg{Int,N}} where N)
Tuple{Tuple{Int,Int},Int,Int} <: (Tuple{NTuple{N,Int},Int,Vararg{Int,N}} where N)
Tuple{Tuple{Int,Int},Vararg{Int}} <: (Tuple{NTuple{N,Int},Vararg{Int,N}} where N)
Tuple{Tuple{},Vararg{Int}} <: (Tuple{NTuple{N,Int},Int,Vararg{Int,N}} where N)
Tuple{Val{2},Vararg{Int,2}} <: (Tuple{Val{N},Vararg{Int,N}} where N)
(Tuple{Vararg{Int,N}} where N) <: (Tuple{Vararg{Int,N}} where N>:Int)
(Ref{Tuple{Int,Int,Vararg{Int,M}}} where M) <: (Ref{Tuple{Vararg{Int,N}}} where N)
(Ref{Tuple{Vararg{Int,N}}} where N) <: (Ref{Tuple{Int,Int,Vararg{Int,M}}} where M)
(Ref{Tuple{Int,Vararg{Int,M}}} where M) <: (Ref{Tuple{Int,Vararg{Int,N}}} where N)
(Ref{Tuple{Int,Vararg{Int,M}}} where M) <: (Ref{Tuple{Vararg{Int,N}}} where N)
(Ref{Tuple{Vararg{Int,M}}} where M) <: (Ref{Tuple{Int,Vararg{Int,N}}} where N)
(Pair{Tuple{Vararg{Int,M}},Tuple{Vararg{Int,M}}} where M) <: (Pair{Tuple{Vararg{Int,N}},Tuple{Int,Vararg{Int,K}}} where {N,K})
(Pair{Tuple{Int,Vararg{Int,M}},Tuple{Vararg{Int,M}}} where M) <: (Pair{Tuple{Vararg{Int,N}},Tuple{Int,Vararg{Int,N}}} where N)
(Pair{Tuple{Int,Int,Vararg{Int,M}},Tuple{Vararg{Int,M}}} where M) <: (Pair{Tuple{Vararg{Int,N}},Tuple{Int,Int,Vararg{Int,N}}} where N)
(Tuple{Tuple{Vararg{Int,N}},Tuple{Vararg{Int,N}}} where N) <: (Tuple{Tuple{Int,Vararg{Int,M}},Tuple{Int,Vararg{Int,M}}} where M)
(Tuple{Ref{Tuple{Vararg{Int,N}}},Tuple{Vararg{Int,N}}} where N) <: (Tuple{Ref{Tuple{Int,Vararg{Int,M}}},Tuple{Int,Vararg{Int,M}}} where M)
(Ref{Tuple{Vararg{Int,N}}} where N) <: (Ref{Tuple{Vararg{Int,M}}} where M<:Int)
Tuple{Int,Int} <: (Tuple{Vararg{Int,N}} where N>:2)
# the diagonal tail of obvious_subtype (subtype.c:2165-2227)
Tuple{Type{Int},Type{Int},Type{Int}} <: (Tuple{Vararg{T}} where T)
Tuple{Type{Int},Type{Int8},Type{Int}} <: (Tuple{Vararg{T}} where T)
Tuple{Int,Type{Int},Type{Int}} <: (Tuple{Vararg{T}} where T)
Tuple{Type{Int},DataType,DataType} <: (Tuple{Vararg{T}} where T)
Tuple{Int,Int,Int8} <: (Tuple{Vararg{T}} where T)
Tuple{Int,Integer,Int} <: (Tuple{Vararg{T}} where T)
Tuple{Integer,Int,Int} <: (Tuple{Vararg{T}} where T)
Tuple{Union{Int,String},Int,Int} <: (Tuple{Vararg{T}} where T)
Tuple{Int,Int,Union{Int,String}} <: (Tuple{Vararg{T}} where T)
Tuple{Ref{Int},Int,Int} <: (Tuple{Ref{T},Vararg{T}} where T)
Tuple{Int,Int,Int} <: (Tuple{Int,Vararg{T}} where T)
Tuple{Int,Int,Int8} <: (Tuple{Int,Vararg{T}} where T)
Tuple{Int,Vararg{Int}} <: (Tuple{Vararg{T}} where T)
Tuple{Vararg{Int}} <: (Tuple{Int,Vararg{T}} where T)
Tuple{Int,Vararg{Int8}} <: (Tuple{Vararg{T}} where T)
Tuple{Vector{Int},Vector{Int},Vector{Int8}} <: (Tuple{Vararg{Vector{T}}} where T)
Tuple{Int,Int,Int} <: (Tuple{Vararg{T}} where T<:Integer)
Tuple{Int,Int,Int} <: (Tuple{Vararg{T}} where T>:Int)
Tuple{Type{Vector},Type{Vector}} <: (Tuple{Vararg{T}} where T)
Tuple{(Type{T} where T<:Int),Int} <: (Tuple{Vararg{T}} where T)
(Tuple{T,T,Vararg{T}} where T) <: (Tuple{Vararg{S}} where S)
(Tuple{Int,Vararg{T}} where T) <: (Tuple{Vararg{S}} where S)
# specificity (jl_type_morespecific, subtype.c:4909-5527), from test/specificity.jl
ms(Tuple{Type{T1}, T1} where T1<:Integer, Tuple{Type{T2}, Integer} where T2<:Integer)
ms(Tuple{Type{T2}, Integer} where T2<:Integer, Tuple{Type{T1}, T1} where T1<:Integer)
ms(Tuple{Type{T1}, Ptr{T1}} where T1<:Integer, Tuple{Type{T2}, Ptr{Integer}} where T2<:Integer)
ms(Tuple{Type{T2}, Ptr{Integer}} where T2<:Integer, Tuple{Type{T1}, Ptr{T1}} where T1<:Integer)
ms(Tuple{Array{T,N}, Vararg{Int,N}} where T where N, Tuple{Array,Int})
ms(Tuple{Array,Int}, Tuple{Array{T,N}, Vararg{Int,N}} where T where N)
ms(Tuple{Array, Vararg{Int,N}} where N, Tuple{Array,Int})
ms(Tuple{Array,Int}, Tuple{Array, Vararg{Int,N}} where N)
ms(Tuple{T,Vararg{T}} where T<:Number, Tuple{Number,Number,Vararg{Number}})
ms(Tuple{Number,Number,Vararg{Number}}, Tuple{T,Vararg{T}} where T<:Number)
ms(Tuple{Array{T} where T<:Union{Float32,Float64}, Any}, Tuple{Array{T} where T<:Real, Any})
ms(Tuple{1,T} where T, Tuple{Any})
ms(Tuple{T} where T, Tuple{T,T} where T)
ms(Type{T} where T<:Integer, Type{Any})
ms(Tuple{Type{Pair{A,B} where B}} where A, Tuple{DataType})
ms(Tuple{Union{Int,String},Type{Pair{A,B} where B}} where A, Tuple{Integer,UnionAll})
ms(Tuple{Any, Tuple{Vararg{Integer}}}, Tuple{Any, Tuple{Any}})
ms(Tuple{Any, Tuple{}}, Tuple{Any, Tuple{Vararg{Integer}}})
ms(Tuple{Any, Tuple{}}, Tuple{Any, Tuple{Any}})
ms(Tuple{Array}, Tuple{AbstractVector})
ms(Tuple{Matrix}, Tuple{AbstractVector})
ms(Tuple{AbstractVector}, Tuple{Array})
ms(Tuple{Type{Any}, Any}, Tuple{Type{T}, Any} where T<:Real)
ms(Tuple{Type{T}, Tuple{Any, Vararg{Any}}} where T<:Tuple{Any, Vararg{Any}}, Tuple{Type{Any}, Any})
ms(Tuple{Type{T}, Tuple{Any, Vararg{Any}}} where T<:Tuple{Any, Vararg{Any}}, Tuple{Type{Tuple}, Tuple})
ms(Tuple{Any, Tuple{}, Tuple{}}, Tuple{Any, Tuple{Any}})
ms(Tuple{Any, Tuple{Any}, Tuple{Any}}, Tuple{Any, Tuple{Any, Any}})
ms(Tuple{Any, Vararg{Tuple{}}}, Tuple{Any, Tuple{Any}})
ms(Tuple{T, T} where T<:AbstractFloat, Tuple{T, T, T} where T<:AbstractFloat)
ms(Tuple{T, Real, T} where T<:AbstractFloat, Tuple{T, T} where T<:Real)
ms(Tuple{Real, Real}, Tuple{T, T, T} where T <: Real)
ms(Tuple{Real, Real, Real}, Tuple{T, T, T} where T <: Real)
ms(Tuple{Real, Real, Vararg{Real}}, Tuple{T, T, T} where T <: Real)
ms(Tuple{Real, Real, Vararg{Int}}, Tuple{T, T, T} where T <: Real)
ms(Tuple{Type{Some{T}}} where T, Tuple{Type{T}, Any} where T)
ms(Tuple{Type{Some{T}}, T} where T, Tuple{Type{Some{T}}} where T)
ms(Tuple{Type{Tuple}, Any, Any}, Tuple{Type{Tuple{Vararg{E}}}, Any, Any} where E)
ms(Tuple{Type{Tuple{}}, Tuple{}}, Tuple{Type{T}, T} where T<:Tuple{Any, Vararg{Any}})
ms(Tuple{Type{Pair}, Pair}, Tuple{Type{D},D} where D<:Pair)
ms(Tuple{Type{D},D} where D<:Pair, Tuple{Type{Any}, Any})
ms(Tuple{Type{Pair}, Pair}, Tuple{Type{Any}, Any})
ms(Tuple{Ref{T}, Tuple{T}} where T, Tuple{Ref, Tuple{T}} where T)
ms(Tuple{Ref{T}, Tuple{T}} where T, Tuple{Ref{T}, Tuple{Vararg{T}}} where T)
ms(Tuple{Ref, Tuple{T}} where T, Tuple{Ref{T}, Tuple{Vararg{T}}} where T)
ms(Tuple{Ref{T}, Tuple{Vararg{T}}} where T, Tuple{Ref, Tuple{T}} where T)
ms(Tuple{T} where T, Tuple{T, Array{T, 1}} where T)
ms(Tuple{T} where T<:AbstractFloat, Tuple{T} where T)
ms(Tuple{T} where T<:AbstractFloat, Tuple{T, Array{T, 1}} where T)
ms(Type{Union{Tuple{T}, Tuple{Ptr{T}, Ptr{T}, Any}} where T}, Type{Union{Tuple{T}, Tuple{Array{T, N} where N, Any, Array{T, N} where N, Any, Any}} where T})
ms(Union{Tuple{T}, Tuple{Ptr{T}, Ptr{T}, Any}} where T, Union{Tuple{T}, Tuple{Array{T, N} where N, Any, Array{T, N} where N, Any, Any}} where T)
ms(Tuple{Array{T,N}, Vararg{Int,N}} where {T,N}, Tuple{Array, Int})
ms(Tuple{Array, Int}, Tuple{AbstractArray, Int, Array})
ms(Tuple{Array{T,N}, Vararg{Int,N}} where {T,N}, Tuple{AbstractArray, Int, Array})
ms(Tuple{DataType}, Tuple{Core.TypeofBottom})
ms(Tuple{Core.TypeofBottom}, Tuple{Type{<:Tuple}})
ms(Tuple{Type{Any}, Type}, Tuple{Type{T}, Type{T}} where T)
ms(Tuple{Type{Any}, Type}, Tuple{Type{T}, Type{T}} where T<:Union{})
ms(Union{Vector,Ref}, Union{Vector,AbstractArray})
ms(Tuple{Type{T},T} where T<:AbstractArray, Tuple{Type{Union{Nothing, T}}, Union{Nothing, T}} where T)
ms(Tuple{Array,Vararg{Int64}}, Tuple{AbstractArray, Array})
ms(Tuple{Array,Vararg{Int64,N}} where N, Tuple{AbstractArray, Array})
ms(Tuple{Array,Int64}, Tuple{Array,Vararg{Int64,N}} where N)
ms(Tuple{Array,Int64}, Tuple{Array,Vararg{Int64}})
ms(Tuple{Array,Int64}, Tuple{AbstractArray, Array})
ms(Tuple{Type{Missing},Any}, Tuple{Type{Union{Nothing, T}},Any} where T)
ms(Tuple{Type{Union{}}, Any}, Tuple{Any, Type{Union{}}})
ms(Tuple{Core.TypeofBottom, Any}, Tuple{Any, Type{Union{}}})
ms(Tuple{Type{Union{}}, Type{Union{}}, Any}, Tuple{Type{Union{}}, Any, Type{Union{}}})
ms(Tuple{Type{Union{}}, Type{Union{}}, Any, Type{Union{}}}, Tuple{Type{Union{}}, Any, Type{Union{}}, Type{Union{}}})
ms(Tuple{Int,Int}, Tuple{T,T} where T)
ms(Tuple{T,T} where T, Tuple{Any,Any})
ms(Tuple{Any,Any}, Tuple{T,T} where T)
ms(Tuple{T,T} where T<:Integer, Tuple{Integer,Integer})
ms(Tuple{Integer,Integer}, Tuple{T,T} where T<:Integer)
ms(Tuple{T,T} where T<:Integer, Tuple{T,S} where {T<:Integer,S<:Integer})
ms(Tuple{Vector{T},T} where T, Tuple{Vector,Any})
ms(Tuple{Vector,Any}, Tuple{Vector{T},T} where T)
ms(Tuple{Vector{T},T} where T, Tuple{AbstractVector{T},T} where T)
ms(Tuple{Ref{T}} where T<:Integer, Tuple{Ref{T}} where T<:Real)
ms(Tuple{Ref{Int}}, Tuple{Ref{T}} where T<:Integer)
ms(Tuple{Ref{T}} where T<:Integer, Tuple{Ref{Int}})
ms(Ref{T} where T<:Integer, Ref{T} where T>:Int)
ms(Ref{T} where T>:Int, Ref{T} where T<:Integer)
ms(Tuple{Union{Int,Nothing}}, Tuple{Union{T,Nothing}} where T)
ms(Tuple{Union{T,Nothing}} where T, Tuple{Union{Int,Nothing}})
ms(Tuple{Union{T,Nothing}} where T, Tuple{Any})
ms(Tuple{Vararg{Int,N}} where N, Tuple{Vararg{Int}})
ms(Tuple{Vararg{Int}}, Tuple{Vararg{Int,N}} where N)
ms(Tuple{Int,Vararg{Int,N}} where N, Tuple{Vararg{Int,N}} where N)
ms(Tuple{Vararg{T,N}} where {T,N}, Tuple{Vararg{Any}})
ms(Tuple{Vararg{T,N}} where {T,N}, Tuple{Int,Int})
ms(Tuple{Int,Int}, Tuple{Vararg{T,N}} where {T,N})
ms(Tuple{Val{N},Vararg{Int,N}} where N, Tuple{Val,Int,Int})
ms(Tuple{Val,Int,Int}, Tuple{Val{N},Vararg{Int,N}} where N)
ms(Tuple{Type{T}} where T<:Union{Int,String}, Tuple{Union})
ms(Tuple{Type{Union{Int,String}}}, Tuple{Union})
ms(Tuple{Type{Vector}}, Tuple{UnionAll})
ms(Tuple{Type{T}} where T, Tuple{DataType})
ms(Tuple{DataType}, Tuple{Type{T}} where T)
ms(Tuple{Type{Union{}}}, Tuple{DataType})
ms(Tuple{Union}, Tuple{Type{Union{}}})
ms(Type{Union{}}, Type)
ms(Tuple{Type{Int},Int}, Tuple{Type{T},T} where T<:Integer)
ms(Union{Int,String}, Union{Int,AbstractString})
ms(Union{Int,AbstractString}, Union{Int,String})
ms(Union{Int,String}, Any)
ms(Union{Int,String}, Union{Int,Nothing})
ms(Union{Vector{T},Ref{T}} where T, Union{Vector,Ref})
ms(Union{Vector,Ref}, Union{Vector{T},Ref{T}} where T)
ms(Tuple{T,Vararg{T}} where T, Tuple{T,T,Vararg{T}} where T)
ms(Tuple{T,T,Vararg{T}} where T, Tuple{T,Vararg{T}} where T)
ms(Tuple{Int,Vararg{Int,N}} where N, Tuple{Int,Int})
ms(Tuple{Int,Int}, Tuple{Int,Vararg{Int,N}} where N)
ms(Tuple{Array{T,N},Vararg{Int,N}} where {T,N}, Tuple{Array{T,1},Int} where T)
ms(Tuple{Array{T,1},Int} where T, Tuple{Array{T,N},Vararg{Int,N}} where {T,N})
ms(Tuple{Pair{A,A}} where A, Tuple{Pair{A,B}} where {A,B})
ms(Tuple{Pair{A,B} where B} where A, Tuple{Pair})
ms(Tuple{Type{Pair{A,B}}} where {A,B}, Tuple{Type{Pair{A,A}}} where A)
ms(Tuple{1}, Tuple{Any})
ms(Tuple{1}, Tuple{Int})
ms(Tuple{:a}, Tuple{Symbol})
ms(Tuple{Ref{1}}, Tuple{Ref{T}} where T)
ms(Tuple{Ref{T}} where T, Tuple{Ref{1}})
ms(Union{Tuple{Ref{T},T} where T<:Vector, Nothing}, Tuple{Ref{Vector{S}},Any} where S)
ms(Union{Tuple{Ref{T}} where T<:Vector, Nothing}, Tuple{Ref{Vector{S}}} where S)
ms(Union{Tuple{Ref{T},T,T} where T<:Vector, Nothing}, Tuple{Ref{Vector{S}},Any,Any} where S)
ms(Union{Tuple{Ref{T},T} where T<:Ref, Nothing}, Tuple{Ref{Ref{S}},Any} where S)
ms(Union{Tuple{Ref{T}} where T<:Ref, Nothing}, Tuple{Ref{Ref{S}}} where S)
ms(Tuple{Ref{Vector{S}},Any} where S, Union{Tuple{Ref{T},T} where T<:Vector, Nothing})
ms(Tuple{Ref{Vector{S}}} where S, Union{Tuple{Ref{T}} where T<:Vector, Nothing})
let T = TypeVar(:T); ms(Vector{T}, Vector) end
ms(Union{Core.TypeofBottom, Nothing}, DataType)
ms(Union{Core.TypeofBottom, Nothing}, Type{Int})
ms(Union{Type{Int}, Nothing}, Core.TypeofBottom)
ms(Union{Type{Union{}}, Nothing}, DataType)
ms(Union{Type{Union{}}, Nothing}, Int)
ms(Union{Type{T} where T<:Integer, Nothing}, DataType)
ms(Union{Type{T} where T, Nothing}, DataType)
ms(Union{Type{Int}, Nothing}, UnionAll)
ms(Union{Type{T} where T>:Int, Nothing}, Type{Integer})
ms(Union{Ref{Vector{T}} where T, Nothing}, Ref{Vector{Int}})
ms(Union{Ref{Vector{Int}}, Nothing}, Ref{Vector{T}} where T)
ms(Union{Ref{T} where T<:Vector, Nothing}, Ref{Vector{S}} where S)
ms(Union{Pair{T,Vector{T}} where T, Nothing}, Pair{S,Vector{R}} where {S,R})
ms(Union{Pair{S,Vector{R}} where {S,R}, Nothing}, Pair{T,Vector{T}} where T)
ms(Tuple{Vector{T}, Vararg{T,N}} where {T,N}, Tuple{Vector{Int},Int})
ms(Tuple{Vector{Int},Int}, Tuple{Vector{T}, Vararg{T,N}} where {T,N})
ms(Tuple{Array{T,N}, Vararg{Int,N}} where {T,N}, Tuple{Array{Int,2},Int,Int})
ms(Tuple{Array{Int,2},Int,Int}, Tuple{Array{T,N}, Vararg{Int,N}} where {T,N})
ms(Tuple{Array{T,N}, Vararg{Int,N}} where {T,N}, Tuple{Matrix,Vararg{Int,2}})
ms(Tuple{Array{T,N}, Vararg{Int,N}} where {T,N}, Tuple{Array{T,2},Vararg{Int,2}} where T)
ms(Tuple{Array{T,2},Vararg{Int,2}} where T, Tuple{Array{T,N}, Vararg{Int,N}} where {T,N})
ms(Tuple{Array{T,N}, Vararg{Int,N}} where {T,N}, Tuple{Array{T,2},Int} where T)
ms(Tuple{Array{T,N}, Vararg{Int,N}} where {T,N}, Tuple{Array,Vararg{Int,2}})
ms(Tuple{Tuple{Vararg{T,2}},T} where T, Tuple{Tuple{Int,Int},Any})
ms(Union{Tuple{Tuple{T,Vararg{T}}} where T, Nothing}, Tuple{Tuple{Any,Any}})
ms(Union{Tuple{Vector{T},Pair{T,S}} where {T,S}, Nothing}, Tuple{Vector{R},Pair{R,R}} where R)
ms(Union{Tuple{Ref{T},Ref{S}} where {T,S<:T}, Nothing}, Tuple{Ref{R},Ref{R}} where R)
