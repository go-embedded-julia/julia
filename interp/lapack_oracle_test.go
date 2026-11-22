package interp

import (
	"context"
	_ "embed"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/expr"
)

// LAPACK on the Base image: julia's own cases for the routines go-blas serves
// (testdata/lapack_stdlib.jl, from LinearAlgebra's test/lu.jl and
// test/cholesky.jl). Same switches as TestBLASOnBase.

//go:embed testdata/lapack_stdlib.jl
var lapackStdlibCases string

var lapackNames = []string{"dgetrf_64_", "sgetrf_64_", "dgetrs_64_", "sgetrs_64_", "dpotrf_64_", "spotrf_64_", "dpotrs_64_", "spotrs_64_", "dlacpy_64_", "slacpy_64_"}

func TestLapackOnBase(t *testing.T) {
	k := baseKernel(t)
	ctx := context.Background()
	counts := map[string]int{}
	for _, name := range lapackNames {
		name := name
		orig := k.vm.libShims[openblasLib][expr.Symbol(name)]
		if orig == nil {
			t.Fatalf("no shim for %s", name)
		}
		k.vm.libShims[openblasLib][expr.Symbol(name)] = func(vm *VM, a []any) (any, error) {
			counts[name]++
			return orig(vm, a)
		}
	}
	start := time.Now()
	r := k.Eval(ctx, lapackStdlibCases)
	t.Logf("julia's cases: %v", time.Since(start))
	if r.Err != nil {
		t.Fatalf("julia's cases: %v\n%s\n%s", r.Err, r.Stderr, r.Stdout[max(0, len(r.Stdout)-600):])
	}
	m := regexp.MustCompile(`^\((\d+), (.*)\)$`).FindStringSubmatch(r.Value)
	if m == nil || m[2] != "String[]" {
		t.Errorf("julia's cases failed: %s", r.Value)
	}
	if m != nil {
		t.Logf("%s tests, calls through LAPACK: %v", m[1], counts)
	}
	for _, name := range lapackNames {
		if counts[name] == 0 {
			t.Errorf("julia's cases never reached %s", name)
		}
	}

	// trtrs is not in go-blas v0.4.0: a triangular solve stays refused
	r = k.Eval(ctx, `using LinearAlgebra; UpperTriangular([2.0 1.0; 0.0 3.0]) \ [1.0, 2.0]`)
	if r.Err == nil {
		t.Errorf("UpperTriangular \\ b was served: %q", r.Value)
	} else {
		t.Logf("UpperTriangular \\ b: %v", strings.SplitN(r.Err.Error(), "\n", 2)[0])
	}
}
