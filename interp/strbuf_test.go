package interp

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/core"
)

// Core.ARGS is Args (jl_set_ARGS), a Vector{String}.
func TestARGS(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "String[]"},
		{[]string{"Base_compiler.jl", "--dataroot", "x/"}, "[\"Base_compiler.jl\", \"--dataroot\", \"x/\"]"},
	} {
		vm := New()
		vm.Args = c.args
		if err := vm.Boot(); err != nil {
			t.Fatal(err)
		}
		v, err := vm.NewSession().Eval("Core.ARGS")
		check(t, "Core.ARGS", v, err, c.want)
	}
}

// jl_alloc_string, then memmove into it: Base_compiler.jl's strcat.
func TestAllocString(t *testing.T) {
	vm := New()
	vm.MaxMemoryElems = 16
	alloc := func(n any) (any, error) { return vm.allocString([]any{n}) }
	for _, c := range []struct {
		name string
		n    any
		want string
	}{
		{"none", int64(0), "\"\""},
		{"three", int64(3), `"\0\0\0"`},
		{"past the limit", int64(17), "!memory limit exceeded: String of 17 bytes"},
		{"a negative size", int64(-1), "!memory limit exceeded: String of 18446744073709551615 bytes"},
		{"a Csize_t size", uint64(2), `"\0\0"`},
		{"a size not an Int", int32(1), "!TypeError(:ccall, \"\", UInt64, 1)"},
	} {
		v, err := alloc(c.n)
		check(t, c.name, v, err, c.want)
	}
	if _, err := alloc(int64(17)); !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("past the limit: %v", err)
	}

	out, _ := alloc(int64(3))
	s := out.(string)
	ptr := func(s string, off int64) *Pointer { p := vm.stringPtrOf(s); p.Off = off; return p }
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"the first bytes", []any{ptr(s, 0), ptr("ab", 0), uint64(2)}, "ok"},
		{"the last byte", []any{ptr(s, 2), ptr("xc", 1), uint64(1)}, "ok"},
		{"nothing at the end", []any{ptr(s, 3), ptr("", 0), uint64(0)}, "ok"},
		{"past the end", []any{ptr(s, 2), ptr("cd", 0), uint64(2)}, "!not ported yet: memmove"},
		{"before the start", []any{ptr(s, -1), ptr("a", 0), uint64(1)}, "!not ported yet: memmove"},
		{"more than the source", []any{ptr(s, 0), ptr("a", 0), uint64(2)}, "!not ported yet: memmove"},
		{"into a string the VM did not allocate", []any{ptr("abc", 0), ptr("x", 0), uint64(1)}, "!not ported yet: memmove"},
		{"into the empty string", []any{ptr("", 0), ptr("", 0), uint64(0)}, "!not ported yet: memmove"},
		{"from no string", []any{ptr(s, 0), &Pointer{Type: vm.ptrType(nil)}, uint64(0)}, "!not ported yet: memmove"},
		{"into no string", []any{&Pointer{Type: vm.ptrType(nil)}, ptr("a", 0), uint64(1)}, "!not ported yet: memmove"},
		{"a size not a Csize_t", []any{ptr(s, 0), ptr("a", 0), int64(1)}, "!not ported yet: memmove"},
	} {
		v, err := vm.memmove(c.args)
		if err == nil && v == c.args[0] {
			v = "ok"
		}
		if c.want == "ok" {
			if v != "ok" || err != nil {
				t.Errorf("%s: %v %v", c.name, v, err)
			}
			continue
		}
		check(t, c.name, v, err, c.want)
	}
	if s != "abc" {
		t.Errorf("the string written: %q", s)
	}
	// a prefix or a suffix of it is another string, not writable
	if _, ok := vm.writableBytes(s[:1]); ok {
		t.Error("a prefix is writable")
	}
	if _, ok := vm.writableBytes(s[1:]); ok {
		t.Error("a suffix is writable")
	}
	// the table entry goes with the string, not before
	if _, err := alloc(int64(8)); err != nil {
		t.Fatal(err)
	}
	gone := false
	for range 100 {
		runtime.GC()
		vm.strMu.Lock()
		gone = len(vm.strBufs) == 1
		vm.strMu.Unlock()
		if gone {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !gone {
		t.Error("a dropped string's entry stayed")
	}
	if _, ok := vm.writableBytes(s); !ok {
		t.Error("a live string's bytes were dropped")
	}
	runtime.KeepAlive(s)
}

// memmove writes only inside a string allocString made, for any offsets
// and size a program can give it.
func FuzzMemmove(f *testing.F) {
	f.Add(int64(4), int64(0), "ab", int64(0), uint64(2))
	f.Add(int64(1), int64(1)<<62, "", int64(-1), uint64(1)<<63)
	f.Fuzz(func(t *testing.T, n, doff int64, src string, soff int64, cnt uint64) {
		vm := New()
		vm.MaxMemoryElems = 64
		v, err := vm.allocString([]any{n})
		if err != nil {
			return
		}
		s := v.(string)
		dp := vm.stringPtrOf(s)
		dp.Off = doff
		sp := vm.stringPtrOf(src)
		sp.Off = soff
		if _, err := vm.memmove([]any{dp, sp, cnt}); err == nil {
			if s[doff:doff+int64(cnt)] != src[soff:soff+int64(cnt)] {
				t.Errorf("memmove(%d, %q@%d, %d) wrote %q", doff, src, soff, cnt, s)
			}
		}
	})
}

// jl_stored_inline, each answer julia 1.12.6's (testdata/storedinline.jl).
func TestStoredInline(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	for _, src := range []string{"mutable struct Mut; x::Int; end", "struct HasPtr; x::Int; s::String; end"} {
		if _, err := s.Eval(src); err != nil {
			t.Fatal(err)
		}
	}
	for ty, want := range map[string]string{
		"UInt8": "1", "Int64": "1", "Float64": "1", "Bool": "1", "Nothing": "1", "Char": "1",
		"Ptr{UInt8}": "1", "Tuple{Int, Float64}": "1", "Tuple{}": "1",
		"Union{Int8, UInt8}": "2", "Union{Nothing, Int64}": "2", "Union{Int64, String}": "0",
		"Union{}": "0", "Any": "0", "Integer": "0", "String": "0", "Symbol": "0", "Mut": "0",
		"DataType": "0", "Array{Int, 1}": "0", "Memory{Int}": "0", "Tuple{Int, String}": "1",
		"Tuple{Any}": "0", "Array": "0", "Type{Int}": "0",
		"NamedTuple{(:a,), Tuple{Int}}": "1", "NamedTuple{(:a,), Tuple{String}}": "1",
		// holding references, inline as it may be (mayinlinealloc) and
		// leaves no field uninitialized (testdata/layout.golden has more)
		"HasPtr": "1",
	} {
		v, err := s.Eval("ccall(:jl_stored_inline, Int32, (Any,), " + ty + ")")
		check(t, ty, v, err, want)
	}

	// a Union of 127 inline members or more is boxed (jl_islayout_inline)
	members := make([]string, 127)
	for i := range members {
		members[i] = fmt.Sprintf("P%d", i)
		if _, err := s.Eval(fmt.Sprintf("primitive type P%d 8 end", i)); err != nil {
			t.Fatal(err)
		}
	}
	for n, want := range map[int]string{126: "126", 127: "0"} {
		v, err := s.Eval("ccall(:jl_stored_inline, Int32, (Any,), Union{" + strings.Join(members[:n], ", ") + "})")
		check(t, fmt.Sprintf("a Union of %d", n), v, err, want)
	}
	// an immutable DataType with free type variables has no layout: boxed
	n, err := storedInline(core.UnwrapUnionAll(core.PairType))
	check(t, "Pair's body", n, err, "0")
}
