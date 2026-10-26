package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/internal/gen"
)

func TestBench(t *testing.T) {
	withFake(t, fakeOracle{})
	out, err := run(t, "bench", "-n", "1")
	if err != nil || !strings.Contains(out, "ratio go/julia") {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, fail := range []string{"corpus", "readfile", "benchparse"} {
		withFake(t, fakeOracle{fail: fail})
		if _, err := run(t, "bench"); err == nil {
			t.Errorf("bench with %s failing succeeded", fail)
		}
	}
	saved := startOracle
	defer func() { startOracle = saved }()
	startOracle = func() (gen.Oracle, func() error, error) { return nil, nil, errBoom }
	if _, err := run(t, "bench"); !errors.Is(err, errBoom) {
		t.Errorf("oracle start failure in bench: %v", err)
	}
}
