# One UnionAll a line, for TestWidenDiagonalCases and widen.jl:
# jl_widen_diagonal(unwrap_unionall(ua), ua), the diagonal variables (those
# occurring only covariantly and more than once) made non-diagonal.
Tuple{T,T} where T
Tuple{T} where T
Tuple{T,T,T} where T<:Integer
Tuple{Vararg{T}} where T
Tuple{T,Vector{T}} where T
Tuple{T,Ref{T}} where T
Tuple{T,T} where {S,T<:S}
Tuple{S,T,T} where {S<:Integer,T<:S}
Union{Tuple{T,T},Tuple{T}} where T
Union{Tuple{T},Tuple{T,T}} where T
Tuple{Union{T,Int},Union{T,Int8}} where T
Tuple{Union{T,Int},Union{T,Int8},T} where T
Tuple{Vararg{T,2}} where T
Tuple{Vararg{T,1}} where T
Tuple{Vararg{T,0}} where T
NTuple{N,T} where {T,N}
Tuple{Tuple{T,T}} where T
Tuple{T,T} where T>:Int
Tuple{T,Vararg{T}} where T
Tuple{T,T,Vararg{T}} where T
Tuple{T,Vararg{T,2}} where T
Tuple{T,Vararg{T,1}} where T
Tuple{Vararg{Tuple{T,T}}} where T
Tuple{Ref{T},T,T} where T
Tuple{T,T,Ref{T}} where T
Tuple{T,T,Ref{Union{T,Int}}} where T
Tuple{T,T,Vector{T}} where T
Tuple{S,S} where S<:T where T
Tuple{T,T,Ref{S} where S<:T} where T
Tuple{T,T,Ref{S} where S>:T} where T
Tuple{T,T,Tuple{S,S} where S} where T
Tuple{T,T,Ref{S} where S} where T
Tuple{T,T,Union{S,Int} where S<:T} where T
Tuple{T,T,Array{S,1} where S<:T} where T
Tuple{T,T,Ref{Tuple{S,S}} where S<:T} where T
Tuple{T,Union{T,Int}} where T
Tuple{Int,Int} where T
Tuple{T,T} where T<:Union{Int,String}
Tuple{T,T} where T<:Tuple{S,S} where S
Tuple{T,U,T,U} where {T,U}
Tuple{T,U,T,U} where {T,U<:T}
Tuple{T,Vararg{U},T,U} where {T,U}
Tuple{Vararg{T,N},T} where {T,N}
Tuple{T,T,Vararg{T,N}} where {T,N}
Tuple{T,T} where {T,N}
Array{T,1} where T
Ref{Tuple{T,T}} where T
Pair{T,T} where T
Tuple{T,Val{T}} where T
Tuple{Union{Ref{T},Int},T,T} where T
Tuple{Union{Int,Ref{T}},T,T} where T
Tuple{Union{Tuple{T,T},Int},T} where T
Tuple{Union{Int,Tuple{T,T}},T} where T
Tuple{S,S,T,Vararg{T}} where {S,T<:S}
Tuple{T,T,Tuple{S,T,T} where S} where T
Tuple{T,T,Tuple{S,T} where S<:T} where T
Tuple{T,T,Tuple{S,T,Vararg{T}} where S} where T
Tuple{T,T,Tuple{Ref{S},T} where S>:T} where T
Tuple{Union{Ref{T},Float64},T,T} where T
Tuple{Union{Ref{T},Symbol},T,T} where T
Tuple{Union{Ref{T},Bool},T,T} where T
Tuple{Union{Ref{T},Char},T,T} where T
Tuple{Union{Ref{T},Nothing},T,T} where T
Tuple{Union{Ref{T},String},T,T} where T
Tuple{Union{Ref{T},Number},T,T} where T
Tuple{Union{Ref{T},Integer},T,T} where T
Tuple{Union{Tuple{T,T},Float64},T} where T
Tuple{Union{Tuple{T,T},Symbol},T} where T
Tuple{Union{Tuple{T,T},Char},T} where T
Tuple{Union{Tuple{T,T},Nothing},T} where T
Tuple{Union{Tuple{T,T},String},T} where T
Tuple{Union{Tuple{T,T},Number},T} where T
Tuple{Union{Tuple{T,T},Integer},T} where T
