struct DTL; size::UInt32; nfields::UInt32; npointers::UInt32; firstptr::Int32; alignment::UInt16; flags::UInt16; end

struct Plain; a::Int8; b::Int64; c::Int16; end

struct Sm{T}; value::T; end

struct HasStr; x::Int; s::String; end

struct Nest; p::HasStr; y::Int8; end

struct Nest2; a::Int8; n::Nest; b::Int16; end

struct Self; x::Int; next::Union{Self,Nothing}; end

struct SelfVec; v::Array{SelfVec,1}; end

struct TupleSelf; t::Tuple{Int,Union{Nothing,TupleSelf}}; end

struct NTSelf; nt::NamedTuple{(:a,),Tuple{Union{Nothing,NTSelf}}}; end

struct AF; x::AbstractArray{AF,1}; end

struct RefAF; x::Ref{RefAF}; end

struct PtrSelf; p::Ptr{PtrSelf}; end

struct MemSelf; m::Memory{MemSelf}; end

abstract type AB{T} end

struct SubAB <: AB{SubAB}; x::Int; end

struct SmallU; a::Union{Int8,Int16}; b::Int32; end

struct SmallU2; a::Union{Nothing,Int64}; b::Union{Float32,Int8,Bool}; end

struct BigU; a::Union{Int,String}; end

struct UPtr; a::Union{Nothing,HasStr}; end

struct Miss; end

struct NothingU; a::Union{Nothing,Miss}; end

mutable struct Mut; x::Int; s::String; end

mutable struct MutAtom; @atomic a::Int; @atomic s::String; b::Int8; end

mutable struct MutAtom2; @atomic a::Int8; @atomic b::Tuple{Int,Int}; @atomic c::NTuple{3,Int}; end

mutable struct MutAtom3; @atomic u::Union{Int8,Int16}; end

mutable struct MutAtomH; @atomic h::HasStr; end

mutable struct Mut2; h::HasStr; i::Int8; n::Nest; end

struct Uninit; a::Int; b::String; Uninit(a) = new(a); end

struct UninitInline; a::Int; h::HasStr; UninitInline(a) = new(a); end

struct UninitMid; a::Int; h::HasStr; c::Int8; UninitMid(a) = new(a); end

struct HoldsUninit; u::Uninit; end

struct HoldsUI; x::UninitInline; end

struct HoldsUIField; a::Int8; b::HasStr; c::UninitInline; end

struct Param{T}; x::T; y::Int8; end

struct ParamSelf{T}; x::T; next::Union{Nothing,ParamSelf{T}}; end

struct ParamRef{T}; v::Array{T,1}; end

primitive type P24 24 end

primitive type P48 48 end

struct HasP24; a::Int8; p::P24; b::Int8; end

struct HoldsP48; a::UInt8; p::P48; end

struct UP; u::Union{P24,Int8}; end

struct Empty; end

struct HoldsEmpty; e::Empty; x::Int8; end

struct Zero2; a::Empty; b::Nothing; end

struct UZ; u::Union{Empty,Nothing}; end

struct HoldsTypeT; t::Type{Int}; x::Int8; end

struct HoldsTBot; t::Type{Union{}}; x::Int8; end

struct BigField; a::NTuple{200,Int8}; s::String; end

struct BigNest; b::BigField; end

struct Huge; a::NTuple{60000,UInt8}; b::NTuple{60000,UInt8}; s::String; end

struct HoldsHuge; h::Huge; end

struct SymF; s::Symbol; i::Int; end

struct StrF; s::String; end

struct MutF; m::Mut; end

struct TypF; t::Type{Int}; end

struct KindF; d::DataType; end

struct UnionF; u::Union{Symbol,Nothing}; end

struct SubStr{S}; string::S; offset::Int; ncodeunits::Int; end

struct UnitR{T}; start::T; stop::T; end

struct Cplx{T}; re::T; im::T; end

struct StrHolder; a::String; b::String; end

struct Vec4; t::NTuple{4,VecElement{Float32}}; end

struct Vec2; t::Tuple{VecElement{Float64},VecElement{Float64}}; end

struct Fx{T}; a::Int; b::Array{T,1}; end

struct Fx2{T}; a::Int; q::Fx{T}; end

struct Fx3{T}; a::Int; q::Union{Nothing,Fx{T}}; end
