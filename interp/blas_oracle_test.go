package interp

import (
	"bytes"
	"context"
	_ "embed"
	"math"
	"math/big"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/expr"
)

// BLAS on the Base image: julia's own cases for the routines go-blas serves
// (testdata/blas_stdlib.jl, from stdlib/LinearAlgebra/test/blas.jl), and
// `A * x`, `A * B`, `x' * y` of LinearAlgebra compared with julia 1.12.6 and
// its OpenBLAS, to go-blas' stated bound and not to the bit.
//
// The Base image is built (about 6 minutes) when JLGO_BASE=1; JLGO_BASE_IMAGE
// names one built by TestBuildImage with JLGO_IMAGE_VERSION=v0.0.1 (the tag of the other kernel tests) to reuse.

//go:embed testdata/blas_stdlib.jl
var blasStdlibCases string

const baseImageTag = "v0.0.1"

// the level 3 routines julia's cases reach in both precisions
var level3Names = []string{"dtrsm_64_", "strsm_64_", "dtrmm_64_", "strmm_64_", "dsyrk_64_", "ssyrk_64_", "dsymm_64_", "ssymm_64_"}

func baseKernel(t *testing.T) *Kernel {
	t.Helper()
	if os.Getenv("JLGO_BASE") != "1" {
		t.Skip("set JLGO_BASE=1 to build julia's Base (several minutes)")
	}
	var img []byte
	if path := os.Getenv("JLGO_BASE_IMAGE"); path != "" {
		var err error
		if img, err = os.ReadFile(path); err != nil {
			t.Fatal(err)
		}
		fakeReleaseVersion(t, baseImageTag)
	} else {
		img = buildBaseImage(t, baseImageTag)
	}
	k, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{CellOptions: CellOptions{Timeout: 20 * time.Minute, MaxAlloc: -1, MaxOutput: 8 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k.Close)
	return k
}

// the products of exact binary fractions, so that every input is the same in
// julia and here, and the exact result is a sum of products of them
const blasOracleSetup = `
using LinearAlgebra
n = 50
A = [Float64(mod(7i+3j, 11)) / 8 for i in 1:n, j in 1:n]
B = [Float64(mod(5i+2j, 13) - 6) / 16 for i in 1:n, j in 1:n]
x = [Float64(mod(3i, 7)) / 4 for i in 1:n]
y = [Float64(mod(2i+1, 9) - 4) / 8 for i in 1:n]
`

const blasOracleCell = `
bits(v) = join([string(reinterpret(UInt64, e)) for e in v], ",")
println(join([bits(vec(A*B)), bits(vec(A'*B)), bits(vec(A*B')), bits(A*x), bits(A'*x), bits([x'*y])], ";"))`

func oracleInputs() (A, B [][]float64, x, y []float64) {
	n := 50
	mod := func(a, b int) int { return ((a % b) + b) % b }
	A, B = make([][]float64, n), make([][]float64, n)
	x, y = make([]float64, n), make([]float64, n)
	for i := 1; i <= n; i++ {
		A[i-1], B[i-1] = make([]float64, n), make([]float64, n)
		x[i-1] = float64(mod(3*i, 7)) / 4
		y[i-1] = float64(mod(2*i+1, 9)-4) / 8
		for j := 1; j <= n; j++ {
			A[i-1][j-1] = float64(mod(7*i+3*j, 11)) / 8
			B[i-1][j-1] = float64(mod(5*i+2*j, 13)-6) / 16
		}
	}
	return
}

func parseBits(t *testing.T, s string) []float64 {
	t.Helper()
	var out []float64
	for _, f := range strings.Split(s, ",") {
		u, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			t.Fatalf("%q: %v", f, err)
		}
		out = append(out, math.Float64frombits(u))
	}
	return out
}

// exactProducts is the 300-bit value of each element of the five products and
// the bound gamma_{k+2} * (|op(A)| |op(B)|) of go-blas' contract for it.
func exactProducts(t *testing.T) (want [][]*big.Float, bound [][]*big.Float) {
	A, B, x, y := oracleInputs()
	n := len(x)
	u := math.Ldexp(1, -53)
	ku := float64(n+2) * u
	gamma := new(big.Float).SetPrec(300).SetFloat64(ku / (1 - ku))
	bf := func(f float64) *big.Float { return new(big.Float).SetPrec(300).SetFloat64(f) }
	dot := func(get func(l int) (float64, float64)) (*big.Float, *big.Float) {
		s, a := bf(0), bf(0)
		for l := 0; l < n; l++ {
			p, q := get(l)
			pr := new(big.Float).SetPrec(300).Mul(bf(p), bf(q))
			s.Add(s, pr)
			a.Add(a, pr.Abs(pr))
		}
		return s, new(big.Float).SetPrec(300).Mul(gamma, a)
	}
	var w, b []*big.Float
	add := func(s, e *big.Float) { w, b = append(w, s), append(b, e) }
	// column-major: element (i, j) at i + n*j
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			add(dot(func(l int) (float64, float64) { return A[i][l], B[l][j] }))
		}
	}
	W := [][]*big.Float{w}
	Bd := [][]*big.Float{b}
	w, b = nil, nil
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			add(dot(func(l int) (float64, float64) { return A[l][i], B[l][j] }))
		}
	}
	W, Bd = append(W, w), append(Bd, b)
	w, b = nil, nil
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			add(dot(func(l int) (float64, float64) { return A[i][l], B[j][l] }))
		}
	}
	W, Bd = append(W, w), append(Bd, b)
	w, b = nil, nil
	for i := 0; i < n; i++ {
		add(dot(func(l int) (float64, float64) { return A[i][l], x[l] }))
	}
	W, Bd = append(W, w), append(Bd, b)
	w, b = nil, nil
	for i := 0; i < n; i++ {
		add(dot(func(l int) (float64, float64) { return A[l][i], x[l] }))
	}
	W, Bd = append(W, w), append(Bd, b)
	w, b = nil, nil
	add(dot(func(l int) (float64, float64) { return x[l], y[l] }))
	return append(W, w), append(Bd, b)
}

// juliaOracle runs the same cell in julia 1.12.6 (OPENBLAS_NUM_THREADS=1).
func juliaOracle(t *testing.T, src string) (string, bool) {
	t.Helper()
	args := []string{"--startup-file=no", "-e", src}
	var cmd *exec.Cmd
	if j := os.Getenv("JLGO_JULIA"); j != "" {
		cmd = exec.Command(j, args...)
	} else if p, err := exec.LookPath("pkgx"); err == nil {
		cmd = exec.Command(p, append([]string{"julia@1.12.6"}, args...)...)
	} else {
		return "", false
	}
	cmd.Env = append(os.Environ(), "OPENBLAS_NUM_THREADS=1")
	out, err := cmd.Output()
	if err != nil {
		t.Logf("julia: %v", err)
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func TestBLASOnBase(t *testing.T) {
	k := baseKernel(t)
	ctx := context.Background()

	// the library LinearAlgebra's init sees
	r := k.Eval(ctx, "using LinearAlgebra; string(LinearAlgebra.BLAS.get_config())")
	if r.Err != nil || !strings.Contains(r.Value, "libopenblas64_") {
		t.Fatalf("BLAS.get_config(): %q, %v", r.Value, r.Err)
	}

	// julia's cases
	counts := map[string]int{}
	for _, name := range append([]string{"dgemm_64_", "sgemm_64_", "dgemv_64_", "sgemv_64_", "cblas_ddot64_", "cblas_sdot64_", "daxpy_64_", "dscal_64_", "dnrm2_64_", "dasum_64_", "idamax_64_"}, level3Names...) {
		name := name
		orig := k.vm.libShims[openblasLib][expr.Symbol(name)]
		k.vm.libShims[openblasLib][expr.Symbol(name)] = func(vm *VM, a []any) (any, error) {
			counts[name]++
			return orig(vm, a)
		}
	}
	start := time.Now()
	r = k.Eval(ctx, blasStdlibCases)
	t.Logf("julia's cases: %v", time.Since(start))
	if r.Err != nil {
		t.Fatalf("julia's cases: %v\n%s\n%s", r.Err, r.Stderr, r.Stdout[max(0, len(r.Stdout)-600):])
	}
	m := regexp.MustCompile(`^\((\d+), (.*)\)$`).FindStringSubmatch(r.Value)
	if m == nil || m[2] != "String[]" {
		t.Errorf("julia's cases failed: %s", r.Value)
	}
	t.Logf("%s tests, calls through the BLAS: %v", m[1], counts)
	for _, name := range append([]string{"dgemm_64_", "sgemm_64_", "dgemv_64_", "sgemv_64_", "cblas_ddot64_", "daxpy_64_", "dscal_64_", "dnrm2_64_", "dasum_64_", "idamax_64_"}, level3Names...) {
		if counts[name] == 0 {
			t.Errorf("julia's cases never reached %s", name)
		}
	}

	// the products
	if r = k.Eval(ctx, blasOracleSetup); r.Err != nil {
		t.Fatal(r.Err)
	}
	counts = map[string]int{}
	r = k.Eval(ctx, blasOracleCell)
	if r.Err != nil {
		t.Fatalf("the products: %v", r.Err)
	}
	for _, name := range []string{"dgemm_64_", "dgemv_64_", "cblas_ddot64_"} {
		if counts[name] == 0 {
			t.Errorf("the products never reached %s", name)
		}
	}
	ours := strings.Split(strings.TrimSpace(r.Stdout), ";")
	want, bound := exactProducts(t)
	if len(ours) != len(want) {
		t.Fatalf("%d products, want %d", len(ours), len(want))
	}
	var theirs []string
	if out, ok := juliaOracle(t, blasOracleSetup+`
bits(v) = join([string(reinterpret(UInt64, e)) for e in v], ",")
println(join([bits(vec(A*B)), bits(vec(A'*B)), bits(vec(A*B')), bits(A*x), bits(A'*x), bits([x'*y])], ";"))`); ok {
		theirs = strings.Split(out, ";")
	} else {
		t.Log("julia 1.12.6 not found (pkgx, or $JLGO_JULIA): compared with the exact value only")
	}
	names := []string{"A*B", "A'*B", "A*B'", "A*x", "A'*x", "x'*y"}
	for p := range want {
		got := parseBits(t, ours[p])
		var jl []float64
		if theirs != nil {
			jl = parseBits(t, theirs[p])
		}
		worst := 0.0
		for e := range want[p] {
			diff := new(big.Float).SetPrec(300).Sub(new(big.Float).SetPrec(300).SetFloat64(got[e]), want[p][e])
			if diff.Abs(diff).Cmp(bound[p][e]) > 0 {
				t.Errorf("%s[%d]: %v is off by more than the bound %v", names[p], e, got[e], bound[p][e].Text('g', 5))
			}
			if jl != nil {
				if d := math.Abs(got[e] - jl[e]); d > worst {
					worst = d
				}
				df := new(big.Float).SetPrec(300).Sub(new(big.Float).SetPrec(300).SetFloat64(jl[e]), want[p][e])
				if df.Abs(df).Cmp(bound[p][e]) > 0 {
					t.Errorf("julia's %s[%d] is off by more than the bound: the bound is wrong", names[p], e)
				}
			}
		}
		t.Logf("%s: %d elements within the bound of the exact value; largest difference with julia's OpenBLAS %g", names[p], len(got), worst)
	}
}

// A first 2x2 product on a fresh Base kernel stays within 20M statements, the
// browser playground's limit (#119): the product itself is about 20K, the
// display of its value was 69M of inference until the image carried it.
func TestFirstMatrixProductOnBase(t *testing.T) {
	k := baseKernel(t)
	ctx := context.Background()
	if r := k.Eval(ctx, "1"); r.Err != nil {
		t.Fatal(r.Err)
	}
	r := k.EvalWith(ctx, `[1.0 2.0; 3.0 4.0] * [5.0 6.0; 7.0 8.0]`, CellOptions{MaxSteps: 20_000_000, Timeout: -1})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if want := "2×2 Matrix{Float64}:\n 19.0  22.0\n 43.0  50.0"; r.Value != want {
		t.Errorf("value %q, want %q", r.Value, want)
	}
}
