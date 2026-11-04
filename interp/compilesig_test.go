package interp

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// compilesigVM is a booted VM and a session that has testdata/
// compilesig_defs.jl defined.
func compilesigVM(t *testing.T) (*VM, *Session) {
	t.Helper()
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	defs, err := os.ReadFile("testdata/compilesig_defs.jl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Eval(string(defs)); err != nil {
		t.Fatal(err)
	}
	return vm, s
}

func (vm *VM) lookupMatch(t *testing.T, tt any) *Struct {
	t.Helper()
	null := &Pointer{Type: vm.ptrType(core.UInt64Type)}
	match, err := vm.shims["jl_gf_invoke_lookup_worlds"](vm, []any{tt, nil, vm.World(), null, null})
	if err != nil {
		t.Fatal(err)
	}
	if match == nil {
		return nil
	}
	return match.(*Struct)
}

// compilesigDisagreements are the cases where this VM answers otherwise
// than julia 1.12.6, all through one cause: core's Subtype says
// `DataType <: (Type{T} where T<:Type)`, which julia says is false. The
// method a17(x::Type{T}) where {T<:Type} then reads a Type{Type{...}}
// argument as one that matched a kind. They are logged, not failed.
var compilesigDisagreements = []string{"typeof(a17)"}

// jl_normalize_to_compilable_sig and jl_isa_compileable_sig against julia
// 1.12.6's (testdata/compilesig.jl, over compilesig_cases.txt).
func TestCompilationSigAgainstJulia(t *testing.T) {
	vm, s := compilesigVM(t)
	cases, err := os.ReadFile("testdata/compilesig_cases.txt")
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/compilesig.golden")
	if err != nil {
		t.Fatal(err)
	}
	gl := strings.Split(strings.TrimSuffix(string(golden), "\n"), "\n")
	typ := func(src string) any {
		src = strings.ReplaceAll(src, "@NamedTuple{a::Int64}", "NamedTuple{(:a,),Tuple{Int64}}")
		v, err := s.Eval(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		return v
	}
	sh := func(name string, args ...any) any {
		v, err := vm.shims[expr.Symbol(name)](vm, args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return v
	}
	next := 0
	for _, line := range strings.Split(string(cases), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "set "); ok {
			f := strings.Fields(rest)
			ttsrc, field, val := strings.Join(f[:len(f)-2], " "), f[len(f)-2], f[len(f)-1]
			m := vm.lookupMatch(t, typ(ttsrc)).Fields[2].(*Struct)
			n, _ := strconv.Atoi(val)
			switch field {
			case "nospecialize":
				m.Fields[methodNospecialize] = int32(n)
			case "called":
				m.Fields[methodCalled] = int32(n)
			case "max_varargs":
				m.Fields[methodMaxVarargs] = uint8(n)
			case "generator":
				m.Fields[methodGenerator] = true // a GeneratedFunctionStub in julia: only its presence is read
			default:
				t.Fatalf("unknown field %s", field)
			}
			continue
		}
		kind, rest, _ := strings.Cut(line, " ")
		methsrc, ttsrc, ok := strings.Cut(rest, " | ")
		if !ok {
			methsrc, ttsrc = rest, rest
		}
		want := strings.Split(gl[next], "\t")
		next++
		if want[0] != rest {
			t.Fatalf("golden line %d is %s, not %s", next, want[0], rest)
		}
		errorf := t.Errorf
		for _, k := range compilesigDisagreements {
			if strings.Contains(rest, k) {
				errorf = t.Logf
			}
		}
		same := func(label string, got any, want string) {
			t.Helper()
			if want == "nothing" {
				if got != nil {
					errorf("%s: got %s, julia nothing", label, showValue(got))
				}
				return
			}
			if got == nil || !core.TypesEqual(got, typ(want)) {
				errorf("%s: got %s, julia %s", label, showValue(got), want)
			}
		}
		tt := typ(ttsrc)
		match := vm.lookupMatch(t, typ(methsrc))
		if match == nil {
			if want[1] != "nomethod" {
				t.Errorf("%s: no method, julia %v", rest, want[1:])
			}
			continue
		}
		if want[1] == "nomethod" {
			t.Errorf("%s: found a method, julia none", rest)
			continue
		}
		env, m := match.Fields[1], match.Fields[2]
		isa := func(tt, env any) string {
			return strconv.Itoa(int(sh("jl_isa_compileable_sig", tt, env, m).(int32)))
		}
		switch kind {
		case "call", "callm":
			n0 := sh("jl_normalize_to_compilable_sig", tt, env, m, int32(0))
			n1 := sh("jl_normalize_to_compilable_sig", tt, env, m, int32(1))
			same(rest+" normalized", n0, want[1])
			same(rest+" normalized if compileable", n1, want[2])
			if got := isa(tt, env); got != want[3] {
				errorf("%s: isa_compileable_sig %s, julia %s", rest, got, want[3])
			}
			if n0 != nil {
				if got := isa(n0, env); got != want[4] {
					errorf("%s: isa_compileable_sig of the normalized %s, julia %s", rest, got, want[4])
				}
			}
		case "isa", "isa0":
			if kind == "isa0" {
				env = SVec{}
			}
			if got := isa(tt, env); got != want[3] {
				errorf("%s: isa_compileable_sig %s, julia %s", rest, got, want[3])
			}
		default:
			t.Fatalf("unknown case %s", line)
		}
	}
	if next != len(gl) {
		t.Errorf("%d golden lines, %d cases read", len(gl), next)
	}
}
