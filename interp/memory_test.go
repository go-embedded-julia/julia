package interp

import (
	"errors"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The memory builtins answer as julia 1.12.6 does: each want is julia's
// repr of the same program (the first element of a fresh Memory{Int64} is
// unspecified in julia; 0 here, and it read 0 there).
func TestMemory(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.CoreSession()
	for _, c := range []struct{ src, want string }{
		{"m = Core.memorynew(Memory{Int64}, 3); r = Core.memoryrefnew(m); r2 = Core.memoryrefnew(r, 2, true); Core.memoryrefset!(r2, 7, :not_atomic, true); (Core.memoryrefget(r2, :not_atomic, true), Core.memoryrefoffset(r2), getfield(m, :length), Core.memoryrefget(r, :not_atomic, true))", "(7, 2, 3, 0)"},
		{"m = Core.memorynew(Memory{Any}, 2); r = Core.memoryrefnew(m); (Core.memoryref_isassigned(r, :not_atomic, true), try Core.memoryrefget(r, :not_atomic, true) catch e; e end)", "(false, UndefRefError())"},
		{"m = Core.memorynew(Memory{Int64}, 3); try Core.memoryrefnew(Core.memoryrefnew(m), 4, true) catch e; (typeof(e), e.i) end", "(BoundsError, 4)"},
		{"m = Core.memorynew(Memory{Int64}, 3); try Core.memoryrefnew(Core.memoryrefnew(m), 0, true) catch e; (typeof(e), e.i) end", "(BoundsError, 0)"},
		{"try Core.memorynew(Memory{Int64}, -1) catch e; e end", `ArgumentError("invalid GenericMemory size: the number of elements is either negative or too large for system address width")`},
		{"m = Core.memorynew(Memory{Int64}, 1); try Core.memoryrefset!(Core.memoryrefnew(m), \"s\", :not_atomic, true) catch e; e end", `TypeError(:memoryrefset!, "", Int64, "s")`},
		{"m = Core.memorynew(Memory{Int64}, 1); try Core.memoryrefget(Core.memoryrefnew(m), :monotonic, true) catch e; e end", `ConcurrencyViolationError("memoryrefget: non-atomic memory cannot be accessed atomically")`},
		{"m = Core.memorynew(Memory{Int64}, 1); try Core.memoryrefget(Core.memoryrefnew(m), :bogus, true) catch e; e end", `ConcurrencyViolationError("invalid atomic ordering")`},
		{"m = Core.memorynew(Memory{Int64}, 1); try Core.memoryrefset!(Core.memoryrefnew(m), 1, :acquire, true) catch e; e end", `ConcurrencyViolationError("invalid atomic ordering")`},
		{"Core.memorynew(Memory{Int64}, 0) === Core.memorynew(Memory{Int64}, 0)", "true"},
		{"getfield(Core.memorynew(Memory{Union{}}, 1), :length)", "1"},
		{"m = Core.memorynew(Memory{Int64}, 0); (Core.memoryref_isassigned(Core.memoryrefnew(m), :not_atomic, true), try Core.memoryrefget(Core.memoryrefnew(m), :not_atomic, true) catch e; typeof(e) end)", "(false, BoundsError)"},
		{"m = Core.memorynew(Memory{Nothing}, 2); Core.memoryrefget(Core.memoryrefnew(m), :not_atomic, true)", "nothing"},
		{"m = Core.memorynew(Memory{Int64}, 2); Core.memoryrefnew(m) === Core.memoryrefnew(m)", "true"},
		{"try Core.memorynew(Int64, 1) catch e; e end", "TypeError(:memorynew, \"\", DataType, Int64)"},
		{"try Core.memoryrefoffset(1) catch e; e end", "TypeError(:memoryrefoffest, \"\", GenericMemoryRef, 1)"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
}

// The argument checks of the memory builtins, and the bound an embedder
// sets on one allocation.
func TestMemoryChecks(t *testing.T) {
	vm := New()
	mt := must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.Int64Type, core.CPU)).(*core.DataType)
	at := must(core.Apply(core.GenericMemoryType, expr.Symbol("atomic"), core.Int64Type, core.CPU)).(*core.DataType)
	bad := must(core.Apply(core.GenericMemoryType, expr.Symbol("weird"), core.Int64Type, core.CPU)).(*core.DataType)
	far := must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.Int64Type, int64(1))).(*core.DataType)
	free := must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"),
		&core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}, core.CPU)).(*core.DataType)
	m := must(biMemorynew(vm, []any{mt, int64(2)})).(*Memory)
	empty := must(biMemoryrefnew(vm, []any{must(biMemorynew(vm, []any{mt, int64(0)}))})).(*MemoryRef)
	r := must(biMemoryrefnew(vm, []any{m})).(*MemoryRef)
	for _, c := range []struct {
		name string
		f    func(*VM, []any) (any, error)
		args []any
		want string
	}{
		{"memorynew arity", biMemorynew, nil, "!memorynew: too few arguments"},
		{"memorynew of a non-Int", biMemorynew, []any{mt, "2"}, "!TypeError(:memorynew, \"\", Int64, \"2\")"},
		{"memorynew of another kind", biMemorynew, []any{bad, int64(1)}, "!GenericMemory kind must be :not_atomic or :atomic"},
		{"memorynew of another address space", biMemorynew, []any{far, int64(1)}, "!GenericMemory addrspace must be Core.CPU"},
		{"memorynew atomic", biMemorynew, []any{at, int64(1)}, "!not ported yet: atomic memory"},
		{"memorynew of a free element type", biMemorynew, []any{free, int64(1)}, "!TypeError(:GenericMemory, \"element type\", Type, T)"},
		{"memoryrefset! of an empty memory", biMemoryrefset, []any{empty, int64(1), expr.Symbol("not_atomic"), true}, "!BoundsError("},
		{"memorynew past MaxMemoryElems", biMemorynew, []any{mt, int64(DefaultMaxMemoryElems + 1)}, "!memory limit exceeded"},
		{"memorynew of a huge size", biMemorynew, []any{mt, int64(1) << 62}, "!invalid GenericMemory size"},
		{"memoryrefnew arity", biMemoryrefnew, nil, "!memoryrefnew: too few arguments"},
		{"memoryrefnew of a non-memory", biMemoryrefnew, []any{int64(1)}, "!TypeError(:memoryrefnew, \"\", GenericMemory, 1)"},
		{"memoryrefnew of a non-ref", biMemoryrefnew, []any{m, int64(1)}, "!TypeError(:memoryrefnew, \"\", GenericMemoryRef"},
		{"memoryrefnew of a non-Int", biMemoryrefnew, []any{r, "1"}, "!TypeError(:memoryrefnew, \"\", Int64, \"1\")"},
		{"memoryrefnew of a non-Bool", biMemoryrefnew, []any{r, int64(1), int64(1)}, "!TypeError(:memoryrefnew, \"\", Bool, 1)"},
		{"memoryrefnew", biMemoryrefnew, []any{r, int64(2), true}, ""},
		{"memoryrefoffset arity", biMemoryrefoffset, nil, "!memoryrefoffset: too few arguments"},
		{"memoryrefget arity", biMemoryrefget, []any{r}, "!memoryrefget: too few arguments"},
		{"memoryrefget of a non-ref", biMemoryrefget, []any{int64(1), expr.Symbol("not_atomic"), true}, "!TypeError(:memoryrefget, \"\", GenericMemoryRef"},
		{"memoryrefget of a non-symbol", biMemoryrefget, []any{r, int64(1), true}, "!TypeError(:memoryrefget, \"\", Symbol, 1)"},
		{"memoryrefget of a non-Bool", biMemoryrefget, []any{r, expr.Symbol("not_atomic"), int64(1)}, "!TypeError(:memoryrefget, \"\", Bool, 1)"},
		{"memoryrefset! of Any", biMemoryrefset, []any{r, int64(5), expr.Symbol("not_atomic"), true}, "5"},
		{"memoryrefset! arity", biMemoryrefset, []any{r}, "!memoryrefset!: too few arguments"},
		{"memoryref_isassigned arity", biMemoryrefIsassigned, nil, "!memoryref_isassigned: too few arguments"},
		{"memoryrefget unordered", biMemoryrefget, []any{r, expr.Symbol("unordered"), true}, "!non-atomic memory cannot be accessed atomically"},
		{"memoryrefset! release", biMemoryrefset, []any{r, int64(1), expr.Symbol("release"), true}, "!non-atomic memory cannot be written atomically"},
		{"memoryrefset! acquire_release", biMemoryrefset, []any{r, int64(1), expr.Symbol("acquire_release"), true}, "!invalid atomic ordering"},
		{"memoryref_isassigned seq_cst", biMemoryrefIsassigned, []any{r, expr.Symbol("sequentially_consistent"), true}, "!non-atomic memory cannot be accessed atomically"},
		{"memoryref_isassigned", biMemoryrefIsassigned, []any{r, expr.Symbol("not_atomic"), true}, "true"},
	} {
		v, err := c.f(vm, c.args)
		check(t, c.name, v, err, c.want)
	}
	if !validAtomicOrder("acquire_release", true, true) || validAtomicOrder("unordered", true, true) {
		t.Error("validAtomicOrder")
	}
	// an unbooted VM has no ConcurrencyViolationError: an ErrorException
	if err := vm.atomicError("m"); err.Error() != `julia exception: ErrorException("m")` {
		t.Errorf("atomicError before boot: %v", err)
	}
	vm.MaxMemoryElems = 1
	if _, err := biMemorynew(vm, []any{mt, int64(2)}); !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("MaxMemoryElems: %v", err)
	}
	// a slot's zero: undef for a reference, the instance of a singleton
	if vm.zeroElem(core.AnyType) != undef || vm.zeroElem(core.NumberType) != undef {
		t.Error("zeroElem of an abstract type")
	}
	if _, ok := vm.zeroElem(vm.tDivide).(*Struct); !ok {
		t.Error("zeroElem of a singleton")
	}
	if vm.zeroElem(vm.tError) != undef {
		t.Error("zeroElem of a struct with fields")
	}
	// getfield of a memory and a reference
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"length", []any{m, expr.Symbol("length")}, "2"},
		{"ptr", []any{m, expr.Symbol("ptr")}, "!not ported yet: the field ptr"},
		{"mem", []any{r, expr.Symbol("mem")}, ""},
		{"ptr_or_offset", []any{r, int64(1)}, "!not ported yet: the field ptr_or_offset"},
	} {
		v, err := biGetfield(vm, c.args)
		check(t, c.name, v, err, c.want)
	}
	// egal: a memory is mutable, a reference compares its parts
	r2 := must(biMemoryrefnew(vm, []any{r, int64(2), true})).(*MemoryRef)
	if vm.egal(r, r2) || vm.egal(m, r) || vm.egal(r, m) || !vm.egal(m, m) {
		t.Error("egal of memories and references")
	}
	if vm.typeOf(m) != any(mt) || vm.typeOf(r) != any(r.Type) {
		t.Error("typeOf of a memory or a reference")
	}
	for _, name := range []expr.Symbol{"memoryrefswap!", "memoryrefmodify!", "memoryrefreplace!", "memoryrefsetonce!"} {
		_, err := vm.apply(coreValue(t, vm, name), nil)
		check(t, string(name), nil, err, "!not ported yet: "+string(name))
	}
}
