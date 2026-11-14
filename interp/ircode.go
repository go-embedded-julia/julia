package interp

import (
	"encoding/binary"
	"fmt"
	"math"
	"reflect"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Compressed IR: a port of src/ircode.c (julia 1.12.6), jl_compress_ir and
// jl_uncompress_ir with the encoder and decoder of their values. Type
// inference caches a method's inferred code as this String, and reads it
// back through jl_uncompress_ir and the jl_ir_* accessors (codeinfo.go).
// The bytes are julia's, values whose memory julia writes raw (TAG_GENERAL)
// laid out as structLayout lays them out.

const (
	irSymbol          = 2
	irSSAValue        = 3
	irDatatype        = 4
	irSlotnumber      = 5
	irSvec            = 6
	irNearbySSAValue  = 7
	irNull            = 8
	irExpr            = 9
	irPhinode         = 10
	irPhicnode        = 11
	irLongSymbol      = 12
	irLongSvec        = 13
	irLongExpr        = 14
	irLongPhinode     = 15
	irLongPhicnode    = 16
	irMethodroot      = 17
	irEdge            = 18
	irString          = 19
	irShortInt64      = 20
	irCNull           = 22
	irArray1D         = 23
	irSingleton       = 24
	irModule          = 25
	irTVar            = 26
	irMethodInstance  = 27
	irMethod          = 28
	irCodeInstance    = 29
	irCommonsym       = 30
	irNearbyGlobal    = 31
	irGlobalref       = 32
	irCore            = 33
	irBase            = 34
	irBitypename      = 35
	irNearbyModule    = 36
	irInt32           = 37
	irInt64           = 38
	irUInt8           = 39
	irVectorTy        = 40
	irPtrTy           = 41
	irLongSSAValue    = 42
	irLongMethodroot  = 43
	irLongEdge        = 44
	irShorterInt64    = 45
	irShortInt32      = 46
	irCall1           = 47
	irCall2           = 48
	irShortBackref    = 49
	irBackref         = 50
	irUnionAll        = 51
	irGotonode        = 52
	irQuotenode       = 53
	irGeneral         = 54
	irGotoifnot       = 55
	irReturnnode      = 56
	irArgument        = 57
	irRelocMethodroot = 58
	irBinding         = 59
	irMemoryT         = 60
	irEnternode       = 61
	irLastTag         = 61
)

// irSpecial marks the entries of julia's tag table that are one object by
// identity, which the VM makes anew (an empty svec or tuple) or does not
// have (jl_an_empty_vec_any, the root task).
type irSpecial int

const (
	irEmptySvec irSpecial = iota
	irEmptyTuple
	irEmptyVecAny
	irRootTask
)

// irTables are jl_init_serializer's tables (ircode.c:1586-1688): deser_tag
// and its inverse ser_tag, and the two-byte common symbols.
type irTables struct {
	deser   [256]any
	has     [256]bool
	ser     map[any]uint8
	symTag  map[expr.Symbol]uint8
	symList [256]expr.Symbol
}

// commonSymbols1 and commonSymbols2 are julia's common_symbols1.inc and
// common_symbols2.inc: the first get one-byte tags, the second the two-byte
// TAG_COMMONSYM encoding.
var commonSymbols1 = []expr.Symbol{"=", "getproperty", "getfield", "apply_type", "===", "getindex", "convert",
	"==", "new", "int.jl", "+", "boot.jl", "essentials.jl", "ccall", "foreigncall", "iterate", "not_int",
	"Base.jl", "-", "throw", "promotion.jl", "length", "<", "isa", "operators.jl", "number.jl",
	"unsafe_convert", "tuple.jl", "nothing", "bitcast", "NamedTuple", "indexed_iterate", "bool.jl", "!",
	"isempty", "<=", "cconvert", "add_int", "static_parameter", "array.jl", "slt_int", "!=", "UInt64",
	"range.jl", "sle_int", "size", "&", "abstractarray.jl", "rem", ">", "Int64", "sub_int", "*", "and_int",
	"string", ">=", "Ptr", "toInt64", "last", "pointer.jl", "reinterpret", "first", "pairs", "_apply_iterate",
	"typeassert", ":", "UInt8", "setindex!", "isdefined", "typeof", "promote", "kwcall", "unsigned",
	"_promote", "toUInt64", "map", "lshr_int", "gc_preserve_begin", "gc_preserve_end", "trunc_int",
	"ArgumentError", "axes", "ult_int", "UInt", "zext_int", "strings/string.jl", "ifelse", "Array", "eq_int",
	"throw_inexacterror"}

var commonSymbols2 = []expr.Symbol{"|", "setproperty!", "sext_int", "String", "Int", "iterators.jl", "Colon",
	"unchecked_oneto", "structdiff", "UnitRange", "unitrange_last", "sizeof", "check_sign_bit",
	"is_top_bit_set", "data", "kwerr", "axes1", "eachindex", "or_int", "Bool", "setfield!", "fieldtype", "Ref",
	"pointer", "max", "push!", "lastindex", "reflection.jl", "<<", "similar", "Vector", "UInt32", ">>",
	"dict.jl", "checkbounds", "undef", "jl_string_ptr", "error", "strings/io.jl", "strings/substring.jl",
	"bitarray.jl", "strings/basic.jl", "merge", "TypeError", "keyword argument", "in", "print",
	"macro expansion", "mul_int", "shl_int", "C_NULL", "oftype", "_growend!", "Any", "Tuple", "float.jl",
	"ncodeunits", "Integer", "io.jl", "eltype", "name", "parent", "!==", "iszero", "min", "DimensionMismatch",
	"refvalue.jl", "Union", "BlasInt", "unsafe_load", "indices.jl", "x", "require_one_based_indexing",
	"namedtuple.jl", "tail", "Float64", "head", "Cvoid", "copy", "libblastrampoline", "get", "neg_int", "stop",
	"zero", "add_ptr", "toUInt32", "ptr", "char.jl", "trunc", "not_atomic", "enter", "Pair", "jl_value_ptr",
	"Expr", "broadcasted", "pointerref", "multidimensional.jl", "Generator", "leave", "memoryrefnew",
	"show.jl", "pointer_from_objref", "memoryrefget", "reduce.jl", "stride", "pair.jl", "_string", "cmem.jl",
	"generator.jl", "broadcast.jl", "none", "copyto!", "chkstride1", "value", "write", "identity", "real",
	"start", "Cint", "fill!", "checkindex", "keys", "BoundsError", "vals", "Symbol", "strings/util.jl", "Int32",
	"ht_keyindex", "io", "~", "AssertionError", "abstractdict.jl", "show", "mpfr.jl", "isless", "args", "lock",
	"reducedim.jl", "gmp.jl", "offset", "resize!", "throw_boundserror", "Clong", "_call_latest", "argtail",
	"compiler/ssair/ir.jl", "sub_ptr", "materialize", "checksquare", "LinearIndices", "ule_int", "dict", "div",
	"chklapackerror", "count", "Float32", "genericmemory.jl", "print_to_string", "rethrow", "sort.jl",
	"boundscheck", "println", "loading.jl", "collect", "ashr_int", "_expr", "iobuffer.jl", "DataType", "Dict",
	"unsafe_string", "RefValue", "step", "to_shape", "pop_exception", "Memory", "KeyError", "chunks",
	"refpointer.jl", "llvmcall", "c.jl", "set.jl", "abs", "checked_trunc_uint", "Type", "%", "len",
	"BigFloat", "isequal", "vect", "sprint", "mode", "expr.jl", "Nothing", "Val", "IteratorSize", "=>",
	"haskey", "iddict.jl", "unsafe_write", "val", "flags", "task.jl", "UnionAll", "memset", "xor",
	"jl_alloc_genericmemory", "uplo", "toInt32", "Base", "atomics.jl", "uuid", "one", "math.jl", "position",
	"typemax", "all", "error.jl", "path.jl", "^", "nextind", "include", "the_exception", "ensure_initialized",
	"Const", "UInt128", "codeunit", "stat.jl", "gcutils.jl", "UndefRefError", "diag", "throw_undef_if_not",
	"missing", "isnan", "Enums.jl", "logging.jl", "_deleteend!", "indices", "compiler/utilities.jl", "Pairs",
	"<:", "compiler/tfuncs.jl", "close", "subarray.jl", "fastmath.jl", "invokelatest", "jl_array_del_end",
	"_mod64", "parameters"}

func (vm *VM) coreType(name string) any { return vm.Core.own(expr.Symbol(name)).Value }

// typeNameOf is the TypeName of a type constructor (Array, Ptr,
// GenericMemory).
func typeNameOf(t any) *core.TypeName { return core.UnwrapUnionAll(t).(*core.DataType).Name }

// vectorPush is jl_array_ptr_1d_push: v appended to a Vector{Any} of this
// VM, as jl_array_grow_end does: into a spare slot of its memory, else into
// a new, larger memory. A Memory's length is fixed, and an empty Vector's
// memory is the type's one shared zero-length instance, which growing in
// place would make every other empty Vector's memory.
func (vm *VM) vectorPush(vec *Struct, v any) {
	ref := vec.Fields[0].(*MemoryRef)
	n := vec.Fields[1].(*Tuple).Elems[0].(int64)
	at := ref.Off + int(n)
	if at < len(ref.Mem.Elems) {
		ref.Mem.Elems[at] = v
	} else {
		elems := make([]any, max(2*len(ref.Mem.Elems), at+1, 4))
		copy(elems, ref.Mem.Elems)
		for i := len(ref.Mem.Elems); i < len(elems); i++ {
			elems[i] = undef
		}
		elems[at] = v
		vec.Fields[0] = &MemoryRef{Type: ref.Type, Mem: &Memory{Type: ref.Mem.Type, Elems: elems}, Off: ref.Off}
	}
	vec.Fields[1] = vm.newTuple([]any{n + 1})
}

// irTagTables builds jl_init_serializer's tables once per VM.
func (vm *VM) irTagTables() *irTables {
	if vm.irTags != nil {
		return vm.irTags
	}
	t := &irTables{ser: map[any]uint8{}, symTag: map[expr.Symbol]uint8{}}
	set := func(i int, v any) { t.deser[i], t.has[i] = v, true }
	vec := func(e any) any { return must(core.Apply(core.ArrayType, e, int64(1))) }
	mem := func(e any) any {
		return must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), e, core.CPU))
	}
	vals := []any{irEmptySvec, irEmptyTuple, false, true, nil, core.AnyType,
		expr.Symbol("call"), expr.Symbol("invoke"), expr.Symbol("invoke_modify"), expr.Symbol("gotoifnot"),
		expr.Symbol("return"), expr.Symbol("tuple"), "", irEmptyVecAny}
	for _, s := range commonSymbols1 {
		vals = append(vals, s)
	}
	for i := int32(0); i <= 20; i++ {
		vals = append(vals, i)
	}
	for i := int64(0); i <= 20; i++ {
		vals = append(vals, i)
	}
	vals = append(vals, core.BoolType, vm.tLineNumberNode, vm.coreType("PiNode"), vm.coreType("UpsilonNode"),
		core.TypeType, core.Bottom, core.RefType, core.PtrType, core.AbstractArrayType, core.NothingType,
		core.VarargType, core.DenseArrayType, core.FunctionType, core.TypeNameType, vm.coreType("Builtin"),
		vm.coreType("Task"), core.UnionType, vec(core.AnyType), vm.coreType("IntrinsicFunction"),
		vm.ptrType(core.NothingType), vm.tNewvarNode, core.AbstractStringType, vec(core.SymbolType),
		core.AnyTupleType, core.AnyTupleType.Parameters[0], core.EmptyTupleType, vec(core.UInt8Type),
		vec(core.UInt32Type), vm.tCodeInfo, core.TypeofBottomType, core.TypeofBottomType.Super,
		core.NamedTupleType, vec(core.Int32Type), core.UInt32Type, core.UInt64Type, vm.coreType("OpaqueClosure"),
		mem(core.AnyType), mem(core.UInt8Type), irRootTask)
	for tag, v := range map[int]any{irSymbol: core.SymbolType, irSSAValue: vm.tSSAValue,
		irDatatype: core.DataTypeType, irSlotnumber: vm.tSlotNumber, irSvec: core.SimpleVectorType,
		irArray1D: core.ArrayType, irMemoryT: core.GenericMemoryType, irExpr: vm.tExpr,
		irPhinode: vm.coreType("PhiNode"), irPhicnode: vm.coreType("PhiCNode"), irString: core.StringType,
		irModule: core.ModuleType, irTVar: core.TypeVarType, irMethodInstance: vm.tMethodInstance,
		irMethod: vm.tMethod, irCodeInstance: vm.tCodeInstance, irGlobalref: vm.tGlobalRef,
		irInt32: core.Int32Type, irInt64: core.Int64Type, irUInt8: core.UInt8Type,
		irUnionAll: core.UnionAllType, irGotonode: vm.tGotoNode, irQuotenode: vm.tQuoteNode,
		irGotoifnot: vm.tGotoIfNot, irReturnnode: vm.tReturnNode, irArgument: vm.tArgument} {
		set(tag, v)
	}
	for i, v := range vals {
		set(irLastTag+1+i, v)
	}
	for i := 2; i < 256; i++ {
		if k, ok := irKey(t.deser[i]); ok && t.has[i] {
			if _, special := k.(irSpecial); !special {
				t.ser[k] = uint8(i)
			}
		}
	}
	for i, s := range commonSymbols2 {
		t.symTag[s], t.symList[i+2] = uint8(i+2), s
	}
	vm.irTags = t
	return t
}

// irKey is v as a key of ser_tag, julia's pointer hash: values the VM
// compares by value or pointer, false for those a map cannot hold (an svec
// is a slice, a struct of the expr package can hold one).
func irKey(v any) (any, bool) {
	switch v.(type) {
	case nil, bool, int32, int64, string, expr.Symbol, irSpecial:
		return v, true
	}
	return v, reflect.TypeOf(v).Kind() == reflect.Pointer
}

// irEncoder is jl_ircode_state for encoding (ircode.c:83-91).
type irEncoder struct {
	vm     *VM
	b      []byte
	ssaid  int
	method *Struct
	mod    *Module
	edges  SVec
	reloc  uint8
	tables *irTables
}

func (s *irEncoder) u8(x uint8)   { s.b = append(s.b, x) }
func (s *irEncoder) u16(x uint16) { s.b = binary.LittleEndian.AppendUint16(s.b, x) }
func (s *irEncoder) u32(x uint32) { s.b = binary.LittleEndian.AppendUint32(s.b, x) }
func (s *irEncoder) u64(x uint64) { s.b = binary.LittleEndian.AppendUint64(s.b, x) }

// encodeInt32 is jl_encode_int32 (ircode.c:158-168).
func (s *irEncoder) encodeInt32(x int32) {
	if x >= math.MinInt16 && x <= math.MaxInt16 {
		s.u8(irShortInt32)
		s.u16(uint16(x))
	} else {
		s.u8(irInt32)
		s.u32(uint32(x))
	}
}

// literalValID is literal_val_id (ircode.c:132-156): v's index among the
// method's roots or the edges, a new root when neither holds it. It
// returns the index and whether it is an edge's.
func (s *irEncoder) literalValID(v any) (int, bool) {
	roots := s.method.Fields[methodRoots].(*Struct)
	_, _, rs, _ := arrayOf(roots)
	byIdentity := false
	if _, ok := v.(expr.Symbol); ok || core.IsConcrete(v) {
		byIdentity = true
	}
	for i, r := range rs {
		if (byIdentity && sameObject(r, v)) || (!byIdentity && s.vm.egal(r, v)) {
			return s.taggedRoot(i), false
		}
	}
	for i, e := range s.edges {
		if sameObject(e, v) {
			return i, true
		}
	}
	// jl_add_method_root with no module: no root blocks, so the root is not
	// relocatable (get_root_reference, method.c:1561-1572; nroots_sysimg 0)
	s.vm.vectorPush(roots, v)
	return s.taggedRoot(len(rs)), false
}

func (s *irEncoder) taggedRoot(i int) int {
	if nsys, _ := s.method.Fields[methodNrootsSysimg].(int32); i >= int(nsys) {
		s.reloc = 0
	}
	return i
}

// sameObject is julia's pointer identity for VM values: == where Go can
// compare them, never true for those it cannot.
func sameObject(a, b any) bool {
	ka, ok1 := irKey(a)
	kb, ok2 := irKey(b)
	return ok1 && ok2 && ka == kb
}

// encodeAsIndexedRoot is jl_encode_as_indexed_root (ircode.c:170-203).
func (s *irEncoder) encodeAsIndexedRoot(v any) {
	id, edge := s.literalValID(v)
	switch {
	case edge && id <= math.MaxUint8:
		s.u8(irEdge)
		s.u8(uint8(id))
	case edge:
		s.u8(irLongEdge)
		s.u32(uint32(id))
	case id <= math.MaxUint8:
		s.u8(irMethodroot)
		s.u8(uint8(id))
	default:
		s.u8(irLongMethodroot)
		s.u32(uint32(id))
	}
}

// encodeTag writes v as its tag when it has one: in ser_tag, or a common
// symbol (jl_encode_value_'s first cases, ircode.c:253-268).
func (s *irEncoder) encodeTag(v any) bool {
	if k, ok := irKey(v); ok {
		if t8, ok := s.tables.ser[k]; ok {
			if t8 <= irLastTag {
				s.u8(0)
			}
			s.u8(t8)
			return true
		}
	}
	if sym, ok := v.(expr.Symbol); ok {
		if tag, ok := s.tables.symTag[sym]; ok {
			s.u8(irCommonsym)
			s.u8(tag)
			return true
		}
	}
	return false
}

// The values whose encoding cannot fail, encoded as encode would.

func (s *irEncoder) encodeNothing() { s.encodeTag(nil) }

func (s *irEncoder) encodeSymbol(sym expr.Symbol) {
	if !s.encodeTag(sym) {
		s.encodeAsIndexedRoot(sym)
	}
}

func (s *irEncoder) encodeModule(m *Module) {
	switch {
	case s.encodeTag(m):
	case m == s.vm.Core:
		s.u8(irCore)
	case s.vm.baseModule != nil && m == s.vm.baseModule:
		s.u8(irBase)
	case m == s.mod:
		s.u8(irNearbyModule)
	default:
		s.encodeAsIndexedRoot(m)
	}
}

func (s *irEncoder) encodeInt64(x int64) {
	switch {
	case s.encodeTag(x):
	case x >= math.MinInt16 && x <= math.MaxInt16:
		s.u8(irShorterInt64)
		s.u16(uint16(x))
	case x >= math.MinInt32 && x <= math.MaxInt32:
		s.u8(irShortInt64)
		s.u32(uint32(x))
	default:
		s.u8(irInt64)
		s.u64(uint64(x))
	}
}

func (s *irEncoder) encodeInt32Value(x int32) {
	if !s.encodeTag(x) {
		s.encodeInt32(x)
	}
}

// encodeLiteralString is a String encoded as a literal.
func (s *irEncoder) encodeLiteralString(str string) {
	if !s.encodeTag(str) {
		s.u8(irString)
		s.u32(uint32(len(str)))
		s.b = append(s.b, str...)
	}
}

// encode is jl_encode_value_ (ircode.c:243-548); lit is as_literal.
func (s *irEncoder) encode(v any, lit bool) error {
	vm := s.vm
	if v == undef {
		s.u8(irNull)
		return nil
	}
	if s.encodeTag(v) {
		return nil
	}
	switch x := v.(type) {
	case SVec:
		if len(x) == 0 {
			s.u8(irLastTag + 1 + uint8(irEmptySvec))
			return nil
		}
	case *Tuple:
		if len(x.Elems) == 0 {
			s.u8(irLastTag + 1 + uint8(irEmptyTuple))
			return nil
		}
	case expr.Symbol:
		s.encodeSymbol(x)
		return nil
	case *Module:
		s.encodeModule(x)
		return nil
	}
	if d, ok := v.(*core.DataType); ok && !d.HasFreeTypeVars {
		if d.Name == typeNameOf(core.ArrayType) && len(d.Parameters) == 2 && d.Parameters[1] == any(int64(1)) {
			s.u8(irVectorTy)
			return s.encode(d.Parameters[0], false)
		}
		if d.Name == typeNameOf(core.PtrType) {
			s.u8(irPtrTy)
			return s.encode(d.Parameters[0], false)
		}
	}
	switch x := v.(type) {
	case SVec:
		if len(x) <= 255 {
			s.u8(irSvec)
			s.u8(uint8(len(x)))
		} else {
			s.u8(irLongSvec)
			s.u32(uint32(len(x)))
		}
		return s.encodeAll(x)
	case expr.GlobalRef:
		m := vm.refModule(s.mod, x)
		if m == nil {
			return fmt.Errorf("%w: jl_compress_ir of a GlobalRef into %s, a module this VM does not name", ErrUnsupported, x.Mod)
		}
		if m == s.mod {
			s.u8(irNearbyGlobal)
			return s.encode(x.Name, false)
		}
		s.u8(irGlobalref)
		s.encodeModule(m)
		s.encodeSymbol(x.Name)
		return nil
	case expr.SSAValue:
		id := int(x)
		switch d := s.ssaid - id; {
		case d >= 0 && d < 256:
			s.u8(irNearbySSAValue)
			s.u8(uint8(d))
			return nil
		case id >= 0 && id < 256:
			s.u8(irSSAValue)
			s.u8(uint8(id))
			return nil
		case id >= 0 && id <= math.MaxUint16:
			s.u8(irLongSSAValue)
			s.u16(uint16(id))
			return nil
		}
	case expr.SlotNumber:
		if x >= 0 && x <= math.MaxUint16 {
			s.u8(irSlotnumber)
			s.u16(uint16(x))
			return nil
		}
	case *expr.Expr:
		l := len(x.Args)
		if x.Head == "call" && (l == 2 || l == 3) {
			s.u8(irCall1 + uint8(l-2))
			return s.encodeAll(x.Args)
		}
		if l <= 255 {
			s.u8(irExpr)
			s.u8(uint8(l))
		} else {
			s.u8(irLongExpr)
			s.u32(uint32(l))
		}
		s.encodeSymbol(x.Head)
		return s.encodeAll(x.Args)
	case *Struct:
		switch any(x.Type) {
		case vm.coreType("PhiNode"):
			_, _, edges, _ := arrayOf(x.Fields[0])
			_, _, values, _ := arrayOf(x.Fields[1])
			if len(edges) <= 255 && len(values) == len(edges) {
				s.u8(irPhinode)
				s.u8(uint8(len(edges)))
			} else {
				s.u8(irLongPhinode)
				s.u32(uint32(len(edges)))
				s.u32(uint32(len(values)))
			}
			for _, e := range edges {
				// julia tests e <= 0 && e <= 20 for its one-byte encodings
				// (ircode.c:373); either way a 0..20 is one byte
				if e := e.(int32); e <= 0 {
					s.encodeInt32Value(e)
				} else {
					s.encodeInt32(e)
				}
			}
			return s.encodeAll(values)
		case vm.coreType("PhiCNode"):
			_, _, values, _ := arrayOf(x.Fields[0])
			if len(values) <= 255 {
				s.u8(irPhicnode)
				s.u8(uint8(len(values)))
			} else {
				s.u8(irLongPhicnode)
				s.u32(uint32(len(values)))
			}
			return s.encodeAll(values)
		}
	case expr.GotoNode:
		s.u8(irGotonode)
		return s.encode(int64(x), false)
	case expr.GotoIfNot:
		s.u8(irGotoifnot)
		if err := s.encode(x.Cond, false); err != nil {
			return err
		}
		return s.encode(int64(x.Dest), false)
	case expr.EnterNode:
		s.u8(irEnternode)
		s.encodeInt64(int64(x.Catch))
		if !x.HasScope {
			return s.encode(undef, false)
		}
		return s.encode(x.Scope, false)
	case expr.Argument:
		s.u8(irArgument)
		return s.encode(int64(x), false)
	case expr.ReturnNode:
		s.u8(irReturnnode)
		return s.encode(x.Val, false)
	case expr.Unreachable:
		s.u8(irReturnnode)
		return s.encode(undef, false)
	case expr.QuoteNode:
		s.u8(irQuotenode)
		// codegen may reference the exact object: it gets a root
		if inner, ok := x.Value.(*Struct); ok && (any(inner.Type) == vm.coreType("PhiNode") || any(inner.Type) == vm.coreType("PhiCNode")) {
			s.encodeAsIndexedRoot(inner)
			return nil
		}
		if _, ok := x.Value.(*expr.Expr); ok {
			s.encodeAsIndexedRoot(x.Value)
			return nil
		}
		return s.encode(x.Value, false)
	case int64:
		s.encodeInt64(x)
		return nil
	case int32:
		s.encodeInt32(x)
		return nil
	case uint8:
		s.u8(irUInt8)
		s.u8(x)
		return nil
	}
	t, _ := vm.typeOf(v).(*core.DataType)
	if t != nil && !t.Name.Mutable && t.IsConcrete && vm.typeInstance(t) != undef {
		s.u8(irSingleton)
		return s.encode(t, false)
	}
	if str, ok := v.(string); ok && lit {
		s.encodeLiteralString(str)
		return nil
	}
	if lit {
		if _, _, elems, ok := arrayOf(v); ok {
			if st, ok := v.(*Struct); ok && st.Type.Name == typeNameOf(core.ArrayType) {
				s.u8(irArray1D)
				s.encodeInt64(int64(len(elems)))
				if err := s.encode(st.Type, false); err != nil {
					return err
				}
				return s.encodeSlice(st.Type.Parameters[0], elems)
			}
		}
		if m, ok := v.(*Memory); ok {
			s.u8(irMemoryT)
			if err := s.encode(m.Type, false); err != nil {
				return err
			}
			s.encodeInt64(int64(len(m.Elems)))
			return s.encodeSlice(m.Type.Parameters[1], m.Elems)
		}
	}
	general := lit
	switch x := v.(type) {
	case *core.Union, expr.NewvarNode, expr.LineNumberNode, expr.SlotNumber, expr.SSAValue:
		general = true
	case *Struct:
		general = general || any(x.Type) == vm.coreType("UpsilonNode") || any(x.Type) == vm.coreType("PiNode")
	}
	if !general && t != nil && t.IsBits() {
		if size, _, err := dataLayout(t); err == nil && size <= 64 {
			general = true
		}
	}
	if general && t != nil {
		s.u8(irGeneral)
		if err := s.encode(t, false); err != nil {
			return err
		}
		return s.encodeImage(t, v)
	}
	s.encodeAsIndexedRoot(v)
	return nil
}

func (s *irEncoder) encodeAll(vs []any) error {
	for _, v := range vs {
		if err := s.encode(v, false); err != nil {
			return err
		}
	}
	return nil
}

// encodeSlice is jl_encode_memory_slice (ircode.c:205-241) for boxed
// elements and inline ones without pointers.
func (s *irEncoder) encodeSlice(eltype any, elems []any) error {
	size, _, boxed, union := memElem(eltype)
	switch {
	case boxed:
		return s.encodeAll(elems)
	case union || size == 0:
		return fmt.Errorf("%w: jl_compress_ir of a Memory{%s}", ErrUnsupported, showValue(eltype))
	}
	d := eltype.(*core.DataType)
	for _, e := range elems {
		raw, ptrs, err := s.vm.image(d, e)
		if err != nil {
			return err
		}
		if len(ptrs) > 0 {
			return fmt.Errorf("%w: jl_compress_ir of inline elements with pointers", ErrUnsupported)
		}
		s.b = append(s.b, raw...)
	}
	return nil
}

// imagePtr is a pointer field of an image: its offset and value.
type imagePtr struct {
	off int
	val any
}

// image is v of type t as julia lays it out in memory, its pointer fields'
// slots left zero and listed, as TAG_GENERAL writes and reads it.
func (vm *VM) image(t *core.DataType, v any) ([]byte, []imagePtr, error) {
	if t.Name.Bits > 0 {
		bits, w, ok := primBits(v)
		if p, isPtr := v.(*Pointer); isPtr {
			// a pointer's bits are its address in the VM's address space,
			// which decoding maps back to what it points into
			addr, err := vm.ptrAddress(p)
			if err != nil {
				return nil, nil, err
			}
			bits, w, ok = from64(addr), 64, true
		}
		if !ok || int(w) != t.Name.Bits {
			return nil, nil, fmt.Errorf("%w: the bits of a %s", ErrUnsupported, showValue(t))
		}
		raw := binary.LittleEndian.AppendUint64(nil, bits.lo)
		raw = binary.LittleEndian.AppendUint64(raw, bits.hi)
		return raw[:t.Name.Bits/8], nil, nil
	}
	if t == core.UnionType {
		// jl_uniontype_t is two pointers, a and b (julia.h), a layout core's
		// Union does not record
		u := v.(*core.Union)
		return make([]byte, 16), []imagePtr{{0, u.A}, {8, u.B}}, nil
	}
	l, err := structLayout(t)
	if err != nil {
		return nil, nil, err
	}
	ft, _ := t.FieldTypes()
	raw := make([]byte, l.size)
	var ptrs []imagePtr
	for i := range l.offsets {
		f, err := vm.fieldValue(v, i)
		if err != nil {
			return nil, nil, err
		}
		if l.pointers[i] {
			ptrs = append(ptrs, imagePtr{l.offsets[i], f})
			continue
		}
		fd, isDT := ft[i].(*core.DataType)
		if !isDT {
			return nil, nil, fmt.Errorf("%w: the image of an inline Union field of %s", ErrUnsupported, showValue(t))
		}
		// an inline field's own pointers lie within it
		sub, subptrs, err := vm.image(inlineType(fd), f)
		if err != nil {
			return nil, nil, err
		}
		copy(raw[l.offsets[i]:], sub)
		for _, p := range subptrs {
			ptrs = append(ptrs, imagePtr{l.offsets[i] + p.off, p.val})
		}
	}
	return raw, ptrs, nil
}

// fieldValue is field i (0-based) of v, a union's a and b among them.
func (vm *VM) fieldValue(v any, i int) (any, error) {
	if u, ok := v.(*core.Union); ok {
		return []any{u.A, u.B}[i], nil
	}
	return biGetfield(vm, []any{v, int64(i + 1)})
}

// encodeImage writes v's memory, its pointer fields encoded in place
// (ircode.c:511-543).
func (s *irEncoder) encodeImage(t *core.DataType, v any) error {
	raw, ptrs, err := s.vm.image(t, v)
	if err != nil {
		return err
	}
	last := 0
	for _, p := range ptrs {
		s.b = append(s.b, raw[last:p.off]...)
		if err := s.encode(p.val, false); err != nil {
			return err
		}
		last = p.off + 8
	}
	s.b = append(s.b, raw[last:]...)
	return nil
}

// Method's fields the compression reads and writes (methodFields).
const (
	methodModule       = 1
	methodSlotSyms     = 10
	methodSource       = 12
	methodDebuginfo    = 13
	methodGenerator    = 15
	methodRoots        = 16
	methodNrootsSysimg = 18
	methodNargs        = 22
	methodIsOpaque     = 27
)

// ciField is a CodeInfo's field by name.
func (vm *VM) ciField(code *Struct, name expr.Symbol) any {
	for i, n := range vm.tCodeInfo.FieldNames() {
		if n == name {
			return code.Fields[i]
		}
	}
	panic("interp: CodeInfo has no field " + string(name))
}

// irFlagBits are jl_code_info_flags_bitfield_t's (julia_internal.h:654-
// 664), packed from bit 0 as the compilers lay them out.
const (
	irfPropagateInbounds  = 1 << 0
	irfHasFcall           = 1 << 1
	irfHasImageGlobalref  = 1 << 2
	irfNospecializeinfer  = 1 << 3
	irfIsva               = 1 << 4
	irfNargsmatchesmethod = 1 << 5
	irfInliningShift      = 6
	irfConstpropShift     = 8
	irfHasSSAFlags        = 1 << 10
)

// compressIR is jl_compress_ir(m, code) (ircode.c:1012-1106); code nil is
// the method's own source.
func (vm *VM) compressIR(m *Struct, codev any) (string, error) {
	isdef := codev == nil
	if isdef {
		codev = m.Fields[methodSource]
	}
	if c, ok := codev.(*expr.CodeInfo); ok {
		codev = vm.juliaCodeInfo(c)
	}
	code, ok := codev.(*Struct)
	if !ok || code.Type != vm.tCodeInfo {
		return "", vm.typeError("ccall", "", vm.tCodeInfo, codev)
	}
	if m.Fields[methodRoots] == undef {
		m.Fields[methodRoots] = vm.newVector(core.AnyType, nil)
	}
	mod, _ := m.Fields[methodModule].(*Module)
	s := &irEncoder{vm: vm, method: m, mod: mod, edges: SVec{}, reloc: 1, tables: vm.irTagTables()}
	if e, ok := vm.ciField(code, "edges").(SVec); ok && !isdef {
		s.edges = e
	}
	b := func(name expr.Symbol) bool { x, _ := vm.ciField(code, name).(bool); return x }
	u8 := func(name expr.Symbol) uint16 { x, _ := vm.ciField(code, name).(uint8); return uint16(x) }
	nargs, _ := vm.ciField(code, "nargs").(uint64)
	mnargs, _ := m.Fields[methodNargs].(int32)
	_, _, ssaflags, _ := arrayOf(vm.ciField(code, "ssaflags"))
	var flags uint16
	for name, bit := range map[expr.Symbol]uint16{"propagate_inbounds": irfPropagateInbounds, "has_fcall": irfHasFcall,
		"has_image_globalref": irfHasImageGlobalref, "nospecializeinfer": irfNospecializeinfer, "isva": irfIsva} {
		if b(name) {
			flags |= bit
		}
	}
	matches := nargs == uint64(mnargs)
	if matches {
		flags |= irfNargsmatchesmethod
	}
	flags |= (u8("inlining")&3)<<irfInliningShift | (u8("constprop")&3)<<irfConstpropShift
	for _, f := range ssaflags {
		if f != any(uint32(0)) {
			flags |= irfHasSSAFlags
			break
		}
	}
	s.u16(flags)
	purity, _ := vm.ciField(code, "purity").(uint16)
	cost, _ := vm.ciField(code, "inlining_cost").(uint16)
	s.u16(purity)
	s.u16(cost)
	_, _, slotflags, _ := arrayOf(vm.ciField(code, "slotflags"))
	s.u32(uint32(len(slotflags)))
	for _, f := range slotflags {
		s.u8(f.(uint8))
	}
	if !matches {
		s.u32(uint32(nargs))
	}
	_, _, stmts, _ := arrayOf(vm.ciField(code, "code"))
	s.u64(uint64(len(stmts)))
	for i, st := range stmts {
		s.ssaid = i
		if err := s.encode(st, false); err != nil {
			return "", err
		}
	}
	s.ssaid = 0
	if err := s.encode(vm.ciField(code, "ssavaluetypes"), true); err != nil {
		return "", err
	}
	if flags&irfHasSSAFlags != 0 {
		for _, f := range ssaflags {
			s.u32(f.(uint32))
		}
	}
	if m.Fields[methodIsOpaque] == true {
		if err := s.encode(vm.ciField(code, "slottypes"), true); err != nil {
			return "", err
		}
	}
	if g := m.Fields[methodGenerator]; g != undef {
		// generated functions keep their slot names
		names := vm.ciField(code, "slotnames")
		_, _, ns, _ := arrayOf(names)
		s.encodeLiteralString(compressArgnames(ns))
	} else {
		s.encodeNothing()
	}
	s.u8(s.reloc)
	if _, _, rs, _ := arrayOf(m.Fields[methodRoots]); len(rs) == 0 {
		m.Fields[methodRoots] = undef
	}
	return string(s.b), nil
}

// irDecoder is jl_ircode_state for decoding.
type irDecoder struct {
	vm     *VM
	b      string
	at     int
	ssaid  int
	method *Struct
	mod    *Module
	edges  SVec
	tables *irTables
}

var errMalformedIR = fmt.Errorf("jl_uncompress_ir: malformed compressed IR")

func (d *irDecoder) need(n int) error {
	if n < 0 || d.at+n > len(d.b) {
		return errMalformedIR
	}
	return nil
}

func (d *irDecoder) u8() (uint8, error) {
	if err := d.need(1); err != nil {
		return 0, err
	}
	d.at++
	return d.b[d.at-1], nil
}

func (d *irDecoder) bytes(n int) (string, error) {
	if err := d.need(n); err != nil {
		return "", err
	}
	d.at += n
	return d.b[d.at-n : d.at], nil
}

func (d *irDecoder) u16() (uint16, error) {
	s, err := d.bytes(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16([]byte(s)), nil
}

func (d *irDecoder) u32() (uint32, error) {
	s, err := d.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32([]byte(s)), nil
}

func (d *irDecoder) u64() (uint64, error) {
	s, err := d.bytes(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64([]byte(s)), nil
}

// count reads a length that must fit what remains, one byte at least per
// element: a malformed length is refused before anything is allocated.
func (d *irDecoder) count(n uint64) (int, error) {
	if n > uint64(len(d.b)-d.at) {
		return 0, errMalformedIR
	}
	return int(n), nil
}

func (d *irDecoder) decodeN(n int) ([]any, error) {
	out := make([]any, n)
	for i := range out {
		v, err := d.decode()
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// special makes an identity entry of the tag table.
func (d *irDecoder) special(x irSpecial) (any, error) {
	switch x {
	case irEmptySvec:
		return SVec{}, nil
	case irEmptyTuple:
		return d.vm.newTuple(nil), nil
	case irEmptyVecAny:
		return d.vm.newVector(core.AnyType, nil), nil
	}
	return nil, fmt.Errorf("%w: jl_uncompress_ir of the root task", ErrUnsupported)
}

func (d *irDecoder) tagged(tag uint8) (any, error) {
	if !d.tables.has[tag] {
		return nil, errMalformedIR
	}
	if x, ok := d.tables.deser[tag].(irSpecial); ok {
		return d.special(x)
	}
	return d.tables.deser[tag], nil
}

func (d *irDecoder) root(i uint32) (any, error) {
	_, _, rs, _ := arrayOf(d.method.Fields[methodRoots])
	if int64(i) >= int64(len(rs)) {
		return nil, errMalformedIR
	}
	return rs[i], nil
}

func (d *irDecoder) edge(i uint32) (any, error) {
	if int64(i) >= int64(len(d.edges)) {
		return nil, errMalformedIR
	}
	return d.edges[i], nil
}

// decode is jl_decode_value (ircode.c:782-950).
func (d *irDecoder) decode() (any, error) {
	vm := d.vm
	tag, err := d.u8()
	if err != nil {
		return nil, err
	}
	if tag > irLastTag {
		return d.tagged(tag)
	}
	switch tag {
	case irNull:
		return undef, nil
	case 0:
		t, err := d.u8()
		if err != nil {
			return nil, err
		}
		return d.tagged(t)
	case irRelocMethodroot:
		// keys are module build ids; with no root blocks every key is 0
		key, err := d.u64()
		if err != nil {
			return nil, err
		}
		t, err := d.u8()
		if err != nil || key != 0 || (t != irMethodroot && t != irLongMethodroot) {
			return nil, errMalformedIR
		}
		d.at--
		return d.decode()
	case irMethodroot:
		i, err := d.u8()
		if err != nil {
			return nil, err
		}
		return d.root(uint32(i))
	case irLongMethodroot:
		i, err := d.u32()
		if err != nil {
			return nil, err
		}
		return d.root(i)
	case irEdge:
		i, err := d.u8()
		if err != nil {
			return nil, err
		}
		return d.edge(uint32(i))
	case irLongEdge:
		i, err := d.u32()
		if err != nil {
			return nil, err
		}
		return d.edge(i)
	case irSvec, irLongSvec:
		n, err := d.length(tag == irSvec)
		if err != nil {
			return nil, err
		}
		vs, err := d.decodeN(n)
		return SVec(vs), err
	case irCommonsym:
		i, err := d.u8()
		if err != nil {
			return nil, err
		}
		if d.tables.symList[i] == "" {
			return nil, errMalformedIR
		}
		return d.tables.symList[i], nil
	case irSSAValue:
		i, err := d.u8()
		return expr.SSAValue(i), err
	case irNearbySSAValue:
		i, err := d.u8()
		return expr.SSAValue(d.ssaid - int(i)), err
	case irLongSSAValue:
		i, err := d.u16()
		return expr.SSAValue(i), err
	case irSlotnumber:
		i, err := d.u16()
		return expr.SlotNumber(i), err
	case irArray1D:
		return d.array1d()
	case irMemoryT:
		mt, err := d.decode()
		if err != nil {
			return nil, err
		}
		n, err := d.decode()
		if err != nil {
			return nil, err
		}
		t, ok1 := mt.(*core.DataType)
		k, ok2 := n.(int64)
		if !ok1 || !ok2 || k < 0 || t.Name != typeNameOf(core.GenericMemoryType) {
			return nil, errMalformedIR
		}
		elems, err := d.slice(t.Parameters[1], k)
		return &Memory{Type: t, Elems: elems}, err
	case irExpr, irLongExpr, irCall1, irCall2:
		return d.expr(tag)
	case irPhinode, irLongPhinode:
		return d.phi(tag)
	case irPhicnode, irLongPhicnode:
		n, err := d.length(tag == irPhicnode)
		if err != nil {
			return nil, err
		}
		vs, err := d.decodeN(n)
		if err != nil {
			return nil, err
		}
		return vm.newStruct(vm.coreType("PhiCNode"), []any{vm.newVector(core.AnyType, vs)})
	case irGotonode, irQuotenode, irArgument, irReturnnode:
		v, err := d.decode()
		if err != nil {
			return nil, err
		}
		t := map[uint8]any{irGotonode: vm.tGotoNode, irQuotenode: vm.tQuoteNode, irArgument: vm.tArgument, irReturnnode: vm.tReturnNode}[tag]
		if v == undef {
			return vm.newStruct(t, nil)
		}
		return vm.newStruct(t, []any{v})
	case irGotoifnot, irEnternode:
		vs, err := d.decodeN(2)
		if err != nil {
			return nil, err
		}
		t := vm.tGotoIfNot
		if tag == irEnternode {
			t = vm.tEnterNode
		}
		if vs[1] == undef {
			vs = vs[:1]
		}
		return vm.newStruct(t, vs)
	case irShorterInt64:
		i, err := d.u16()
		return int64(int16(i)), err
	case irShortInt64:
		i, err := d.u32()
		return int64(int32(i)), err
	case irInt64:
		i, err := d.u64()
		return int64(i), err
	case irShortInt32:
		i, err := d.u16()
		return int32(int16(i)), err
	case irInt32:
		i, err := d.u32()
		return int32(i), err
	case irUInt8:
		return d.u8()
	case irNearbyGlobal:
		name, err := d.decode()
		if err != nil {
			return nil, err
		}
		sym, ok := name.(expr.Symbol)
		if !ok || d.mod == nil {
			return nil, errMalformedIR
		}
		return gref(d.mod, sym), nil
	case irNearbyModule:
		if d.mod == nil {
			return nil, errMalformedIR
		}
		return d.mod, nil
	case irGlobalref:
		vs, err := d.decodeN(2)
		if err != nil {
			return nil, err
		}
		m, ok1 := vs[0].(*Module)
		sym, ok2 := vs[1].(expr.Symbol)
		if !ok1 || !ok2 {
			return nil, errMalformedIR
		}
		return gref(m, sym), nil
	case irSingleton:
		v, err := d.decode()
		if err != nil {
			return nil, err
		}
		t, ok := v.(*core.DataType)
		if !ok {
			return nil, errMalformedIR
		}
		if inst := vm.typeInstance(t); inst != undef {
			return inst, nil
		}
		return nil, errMalformedIR
	case irCore:
		return vm.Core, nil
	case irBase:
		if vm.baseModule == nil {
			return nil, errMalformedIR
		}
		return vm.baseModule, nil
	case irVectorTy, irPtrTy:
		v, err := d.decode()
		if err != nil {
			return nil, err
		}
		// julia applies the type unchecked (ircode.c); malformed IR is
		// refused here rather than reaching apply_type
		if !core.ValidTypeParam(v) {
			return nil, errMalformedIR
		}
		if tag == irVectorTy {
			return core.Apply(core.ArrayType, v, int64(1))
		}
		return core.Apply(core.PtrType, v)
	case irString:
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		return d.bytes(int(n))
	case irGeneral:
		v, err := d.decode()
		if err != nil {
			return nil, err
		}
		t, ok := v.(*core.DataType)
		if !ok {
			return nil, errMalformedIR
		}
		return d.fromImage(t)
	}
	return nil, errMalformedIR
}

// length reads a one-byte or four-byte element count.
func (d *irDecoder) length(short bool) (int, error) {
	if short {
		n, err := d.u8()
		return int(n), err
	}
	n, err := d.u32()
	if err != nil {
		return 0, err
	}
	return d.count(uint64(n))
}

// expr is jl_decode_value_expr (ircode.c:658-688).
func (d *irDecoder) expr(tag uint8) (any, error) {
	var n int
	var head any
	var err error
	switch tag {
	case irCall1, irCall2:
		n, head = int(tag-irCall1)+2, expr.Symbol("call")
	default:
		if n, err = d.length(tag == irExpr); err != nil {
			return nil, err
		}
		if head, err = d.decode(); err != nil {
			return nil, err
		}
	}
	sym, ok := head.(expr.Symbol)
	if !ok {
		return nil, errMalformedIR
	}
	args, err := d.decodeN(n)
	if err != nil {
		return nil, err
	}
	return &expr.Expr{Head: sym, Args: args}, nil
}

// phi is jl_decode_value_phi (ircode.c:690-718).
func (d *irDecoder) phi(tag uint8) (any, error) {
	var ne, nv int
	var err error
	if tag == irPhinode {
		if ne, err = d.length(true); err != nil {
			return nil, err
		}
		nv = ne
	} else {
		if ne, err = d.length(false); err != nil {
			return nil, err
		}
		if nv, err = d.length(false); err != nil {
			return nil, err
		}
	}
	edges, err := d.decodeN(ne)
	if err != nil {
		return nil, err
	}
	for _, e := range edges {
		if _, ok := e.(int32); !ok {
			return nil, errMalformedIR
		}
	}
	values, err := d.decodeN(nv)
	if err != nil {
		return nil, err
	}
	return d.vm.newStruct(d.vm.coreType("PhiNode"), []any{d.vm.newVector(core.Int32Type, edges), d.vm.newVector(core.AnyType, values)})
}

// array1d is jl_decode_value_array1d (ircode.c:636-656).
func (d *irDecoder) array1d() (any, error) {
	n, err := d.decode()
	if err != nil {
		return nil, err
	}
	at, err := d.decode()
	if err != nil {
		return nil, err
	}
	k, ok1 := n.(int64)
	t, ok2 := at.(*core.DataType)
	if !ok1 || !ok2 || k < 0 || t.Name != typeNameOf(core.ArrayType) {
		return nil, errMalformedIR
	}
	elems, err := d.slice(t.Parameters[0], k)
	if err != nil {
		return nil, err
	}
	return d.vm.newVector(t.Parameters[0], elems), nil
}

// slice is jl_decode_value_memory's reading of n elements (ircode.c:591-
// 632), for boxed elements and inline ones without pointers.
func (d *irDecoder) slice(eltype any, n int64) ([]any, error) {
	k, err := d.count(uint64(n))
	if err != nil {
		return nil, err
	}
	size, _, boxed, union := memElem(eltype)
	switch {
	case boxed:
		return d.decodeN(k)
	case union || size == 0:
		return nil, fmt.Errorf("%w: jl_uncompress_ir of a Memory{%s}", ErrUnsupported, showValue(eltype))
	}
	out := make([]any, k)
	for i := range out {
		if out[i], err = d.fromImage(eltype.(*core.DataType)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// fromImage is jl_decode_value_any (ircode.c:749-780): a value of type t
// read as its memory, its pointer fields decoded in place.
func (d *irDecoder) fromImage(t *core.DataType) (any, error) {
	vm := d.vm
	if t.Name.Bits > 0 {
		raw, err := d.bytes(t.Name.Bits / 8)
		if err != nil {
			return nil, err
		}
		var buf [16]byte
		copy(buf[:], raw)
		if t.Name == typeNameOf(core.PtrType) {
			return biBitcast(vm, []any{t, binary.LittleEndian.Uint64(buf[:8])})
		}
		// an intrinsic's bits are its id
		if t == vm.intrinsicType {
			return vm.intrinsicOf(int32(binary.LittleEndian.Uint32(buf[:4]))), nil
		}
		v, ok := bitsValue(t, u128{binary.LittleEndian.Uint64(buf[8:]), binary.LittleEndian.Uint64(buf[:8])})
		if !ok {
			return nil, fmt.Errorf("%w: jl_uncompress_ir of a %s", ErrUnsupported, showValue(t))
		}
		return v, nil
	}
	if t == core.UnionType {
		ab, err := d.decodeN(2)
		if err != nil {
			return nil, err
		}
		return &core.Union{A: ab[0], B: ab[1]}, nil
	}
	l, err := structLayout(t)
	if err != nil {
		return nil, err
	}
	ft, _ := t.FieldTypes()
	fields := make([]any, len(l.offsets))
	start := 0
	for i, off := range l.offsets {
		if off > start {
			if _, err := d.bytes(off - start); err != nil { // padding
				return nil, err
			}
		}
		if l.pointers[i] {
			if fields[i], err = d.decode(); err != nil {
				return nil, err
			}
			start = off + 8
			continue
		}
		mark := d.at
		fd, isDT := ft[i].(*core.DataType)
		if !isDT {
			return nil, fmt.Errorf("%w: jl_uncompress_ir of an inline Union field of %s", ErrUnsupported, showValue(t))
		}
		if fields[i], err = d.fromImage(inlineType(fd)); err != nil {
			return nil, err
		}
		start = off + (d.at - mark)
		if !fd.IsBits() {
			// a struct with pointers inline: its pointers took the bytes
			// they were encoded in, not their slots, and its own padding
			// was read
			start = off + l.sizes[i]
		}
	}
	if l.size > start {
		if _, err := d.bytes(l.size - start); err != nil {
			return nil, err
		}
	}
	return vm.newStruct(t, fields)
}

// uncompressIR is jl_uncompress_ir(m, metadata, data) (ircode.c:1108-
// 1211): the CodeInfo data encodes; metadata is the CodeInstance it was
// inferred for, or nothing.
func (vm *VM) uncompressIR(m *Struct, metadata, data any) (any, error) {
	if c, ok := data.(*Struct); ok && c.Type == vm.tCodeInfo {
		return c, nil
	}
	src, ok := data.(string)
	if !ok {
		return nil, vm.typeError("ccall", "", core.StringType, data)
	}
	ci, _ := metadata.(*Struct)
	if ci != nil && ci.Type != vm.tCodeInstance {
		return nil, vm.typeError("ccall", "", vm.tCodeInstance, metadata)
	}
	mod, _ := m.Fields[methodModule].(*Module)
	d := &irDecoder{vm: vm, b: src, method: m, mod: mod, tables: vm.irTagTables()}
	if ci != nil {
		d.edges, _ = ci.Fields[ciEdges].(SVec)
	}
	flags, err := d.u16()
	if err != nil {
		return nil, err
	}
	purity, err := d.u16()
	if err != nil {
		return nil, err
	}
	cost, err := d.u16()
	if err != nil {
		return nil, err
	}
	ns, err := d.u32()
	if err != nil {
		return nil, err
	}
	nslots, err := d.count(uint64(ns))
	if err != nil {
		return nil, err
	}
	sf, _ := d.bytes(nslots)
	slotflags := make([]any, nslots)
	for i := range slotflags {
		slotflags[i] = sf[i]
	}
	nargs := uint64(0)
	if flags&irfNargsmatchesmethod != 0 {
		n, _ := m.Fields[methodNargs].(int32)
		nargs = uint64(n)
	} else {
		n, err := d.u32()
		if err != nil {
			return nil, err
		}
		nargs = uint64(int32(n))
	}
	nl, err := d.u64()
	if err != nil {
		return nil, err
	}
	l, err := d.count(nl)
	if err != nil {
		return nil, err
	}
	stmts := make([]any, l)
	for i := range stmts {
		d.ssaid = i
		if stmts[i], err = d.decode(); err != nil {
			return nil, err
		}
	}
	d.ssaid = 0
	ssavaluetypes, err := d.decode()
	if err != nil {
		return nil, err
	}
	ssaflags := make([]any, l)
	for i := range ssaflags {
		ssaflags[i] = uint32(0)
		if flags&irfHasSSAFlags != 0 {
			f, err := d.u32()
			if err != nil {
				return nil, err
			}
			ssaflags[i] = f
		}
	}
	var slottypes any // jl_new_code_info_uninit's nothing
	if m.Fields[methodIsOpaque] == true {
		if slottypes, err = d.decode(); err != nil {
			return nil, err
		}
	}
	names, err := d.decode()
	if err != nil {
		return nil, err
	}
	if _, ok := names.(string); !ok {
		names = m.Fields[methodSlotSyms]
	}
	slotnames, err := vm.uncompressArgnames(names)
	if err != nil {
		return nil, err
	}
	debuginfo := m.Fields[methodDebuginfo]
	if ci != nil {
		debuginfo = ci.Fields[ciDebuginfo]
	}
	if _, err := d.u8(); err != nil { // relocatability
		return nil, err
	}
	if d.at != len(d.b) {
		return nil, errMalformedIR
	}
	// jl_new_code_info_uninit's defaults (method.c:616-646), then what the
	// data and metadata set
	var parent, rettype, edges any = nil, core.AnyType, SVec{}
	minWorld, maxWorld := uint64(1), ^uint64(0)
	if ci != nil {
		parent = vm.ciMethodInstance(ci)
		rettype, edges = ci.Fields[ciRettype], d.edges
		minWorld, _ = ci.Fields[ciMinWorld].(uint64)
		maxWorld, _ = ci.Fields[ciMaxWorld].(uint64)
		if d.edges == nil {
			edges = SVec{}
		}
	}
	vals := map[expr.Symbol]any{
		"code": vm.newVector(core.AnyType, stmts), "debuginfo": debuginfo, "ssavaluetypes": ssavaluetypes,
		"ssaflags": vm.newVector(core.UInt32Type, ssaflags), "slotnames": slotnames,
		"slotflags": vm.newVector(core.UInt8Type, slotflags), "slottypes": slottypes, "rettype": rettype,
		"parent": parent, "edges": edges, "min_world": minWorld, "max_world": maxWorld,
		"method_for_inference_limit_heuristics": nil, "nargs": nargs,
		"propagate_inbounds": flags&irfPropagateInbounds != 0, "has_fcall": flags&irfHasFcall != 0,
		"has_image_globalref": flags&irfHasImageGlobalref != 0, "nospecializeinfer": flags&irfNospecializeinfer != 0,
		"isva": flags&irfIsva != 0, "inlining": uint8(flags >> irfInliningShift & 3),
		"constprop": uint8(flags >> irfConstpropShift & 3), "purity": purity, "inlining_cost": cost,
	}
	names2 := vm.tCodeInfo.FieldNames()
	f := make([]any, len(names2))
	for i, n := range names2 {
		f[i] = vals[n]
	}
	return &Struct{Type: vm.tCodeInfo, Fields: f}, nil
}

// ciMethodInstance is jl_get_ci_mi: a CodeInstance's MethodInstance, an
// ABIOverride's own.
func (vm *VM) ciMethodInstance(ci *Struct) any {
	def := ci.Fields[ciDef]
	if s, ok := def.(*Struct); ok && any(s.Type) == vm.coreType("ABIOverride") {
		for i, n := range s.Type.FieldNames() {
			if n == "def" {
				return s.Fields[i]
			}
		}
	}
	return def
}

// uncompressArgnames is jl_uncompress_argnames (ircode.c:1305-1329): the
// NUL-ended names of a String as a Vector{Symbol}.
func (vm *VM) uncompressArgnames(v any) (any, error) {
	s, ok := v.(string)
	if !ok {
		return nil, vm.typeError("ccall", "", core.StringType, v)
	}
	var names []any
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			names = append(names, expr.Symbol(s[start:i]))
			start = i + 1
		}
	}
	return vm.newVector(core.SymbolType, names), nil
}
