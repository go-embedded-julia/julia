package interp

import (
	"context"
	"strings"
	"testing"
)

// === is jl_egal: the bits of isbits values, so NaN === NaN, 0.0 !== -0.0, and
// two NaNs of another payload are two values, in a Float64, a Float32, a
// struct, a tuple and a type parameter; == and isequal are unchanged (NaN == NaN
// is false, 0.0 == -0.0 true: eq_float, isequal, hash). The expected answers
// are julia 1.12.6's, and the test checks them against julia when it is there
// (pkgx julia@1.12.6, or $JLGO_JULIA), so they cannot drift from it. Each case
// fails by assertion if core.Egal compares floats by value again.

type egalCase struct{ src, want string }

const egalSetup = `
const nan64 = Core.Intrinsics.bitcast(Float64, 0x7ff8000000000000)
const nan64b = Core.Intrinsics.bitcast(Float64, 0x7ff8000000000001)
const nz = Core.Intrinsics.bitcast(Float64, 0x8000000000000000)
const nan32 = Core.Intrinsics.bitcast(Float32, 0x7fc00000)
const nan32b = Core.Intrinsics.bitcast(Float32, 0x7fc00001)
const nz32 = Core.Intrinsics.bitcast(Float32, 0x80000000)
struct SF; a::Float64; b::Float32; end
struct V{x}; end
g(::V{0.0}) = 1
g(::V{nz}) = 2
`

var egalBootCases = []egalCase{
	// Float64
	{"nan64 === nan64", "true"},
	{"nan64 === Core.Intrinsics.bitcast(Float64, 0x7ff8000000000000)", "true"},
	{"0.0 === nz", "false"},
	{"nz === nz", "true"},
	{"nan64b === nan64", "false"},
	{"nan64b === nan64b", "true"},
	{"1.5 === 1.5", "true"},
	{"1.5 === 2.5", "false"},
	// Float32
	{"nan32 === nan32", "true"},
	{"0.0f0 === nz32", "false"},
	{"nan32b === nan32", "false"},
	{"1.5f0 === 1.5f0", "true"},
	// a struct, a tuple
	{"SF(nan64, nan32) === SF(nan64, nan32)", "true"},
	{"SF(0.0, 0.0f0) === SF(nz, 0.0f0)", "false"},
	{"SF(0.0, nz32) === SF(0.0, 0.0f0)", "false"},
	{"SF(nan64b, nan32) === SF(nan64, nan32)", "false"},
	{"(nan64, 1) === (nan64, 1)", "true"},
	{"(0.0,) === (nz,)", "false"},
	// == stays by value
	{"Core.Intrinsics.eq_float(nan64, nan64)", "false"},
	{"Core.Intrinsics.eq_float(0.0, nz)", "true"},
	// a float in a type: one type for NaN, two for 0.0 and -0.0, and dispatch
	// tells them apart
	{"V{nan64}() === V{nan64}()", "true"},
	{"V{0.0}() === V{nz}()", "false"},
	{"V{nan64}() === V{nan64b}()", "false"},
	{"g(V{nz}())", "2"},
	{"g(V{0.0}())", "1"},
}

// What is only there on the Base image: ==, isequal, hash, and the tables
// keyed by === or by isequal.
var egalBaseCases = []egalCase{
	{"NaN == NaN", "false"},
	{"isequal(NaN, NaN)", "true"},
	{"0.0 == -0.0", "true"},
	{"isequal(0.0, -0.0)", "false"},
	{"hash(NaN) == hash(NaN)", "true"},
	{"hash(0.0) == hash(-0.0)", "false"},
	{"NaN != NaN", "true"},
	{"d = IdDict(NaN => 1); haskey(d, NaN)", "true"},
	{"d = IdDict(0.0 => 1); haskey(d, -0.0)", "false"},
	{"d = IdDict(0.0 => 1); haskey(d, 0.0)", "true"},
	{"d = IdDict(nan64 => 1); haskey(d, nan64b)", "false"},
	{"haskey(Dict(0.0 => 1), -0.0)", "false"},
	{"haskey(Dict(NaN => 1), NaN)", "true"},
	{"Val{NaN}() === Val{NaN}()", "true"},
	{"Val{0.0}() === Val{-0.0}()", "false"},
	{"typeof(Val(0.0)) == typeof(Val(-0.0))", "false"},
	{"[NaN] == [NaN]", "false"},
	{"isequal([NaN], [NaN])", "true"},
	{"NaN in [NaN]", "false"},
	{"findfirst(isequal(NaN), [1.0, NaN])", "2"},
	{"length(Set([0.0, -0.0]))", "2"},
	{"length(unique([0.0, -0.0, NaN, NaN]))", "3"},
}

// julia's own answers, when julia is there: the expected values are the ones
// it gives.
func egalAgainstJulia(t *testing.T, setup string, cases []egalCase) {
	t.Helper()
	var src strings.Builder
	src.WriteString(setup)
	src.WriteString("\nfor ex in [\n")
	for _, c := range cases {
		src.WriteString("    " + `"` + strings.ReplaceAll(c.src, `"`, `\"`) + `",` + "\n")
	}
	src.WriteString("]\n    println(string(eval(Meta.parse(\"begin\\n\" * ex * \"\\nend\"))))\nend\n")
	out, ok := juliaOracle(t, src.String())
	if !ok {
		t.Log("julia 1.12.6 not found (pkgx, or $JLGO_JULIA): the expected answers are not checked against it")
		return
	}
	lines := strings.Split(out, "\n")
	if len(lines) != len(cases) {
		t.Fatalf("julia answered %d lines for %d cases: %q", len(lines), len(cases), out)
	}
	for i, c := range cases {
		if lines[i] != c.want {
			t.Errorf("julia says %s is %s, the test expects %s", c.src, lines[i], c.want)
		}
	}
}

func egalRun(t *testing.T, k *Kernel, cases []egalCase) {
	t.Helper()
	for _, c := range cases {
		r := k.Eval(context.Background(), c.src)
		if r.Err != nil || r.Value != c.want {
			t.Errorf("%s\n  value %q, error %v; want %q", c.src, r.Value, r.Err, c.want)
		}
	}
}

func TestEgalFloatsBoot(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	if r := k.Eval(context.Background(), egalSetup); r.Err != nil {
		t.Fatal(r.Err)
	}
	egalRun(t, k, egalBootCases)
	egalAgainstJulia(t, egalSetup, egalBootCases)
}

// The Base image (JLGO_BASE=1): ==, isequal, hash, IdDict, Dict, Set, unique.
func TestEgalFloatsOnBase(t *testing.T) {
	k := baseKernel(t)
	if r := k.Eval(context.Background(), egalSetup); r.Err != nil {
		t.Fatal(r.Err)
	}
	egalRun(t, k, egalBootCases)
	egalRun(t, k, egalBaseCases)
	egalAgainstJulia(t, egalSetup, egalBaseCases)
}
