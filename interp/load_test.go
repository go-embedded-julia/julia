package interp

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// jl_load_ reads VM.Sources only, evaluates each form in the module given,
// and answers missing files, bad arguments, syntax errors and a file that
// includes itself as julia does.
func TestLoad(t *testing.T) {
	vm := New()
	vm.Sources = fstest.MapFS{
		"two.jl":  {Data: []byte("x = 1\ny = (x, 2)\n")},
		"bad.jl":  {Data: []byte("x = (\n")},
		"self.jl": {Data: []byte("ccall(:jl_load_, Any, (Any, Any), Core, \"self.jl\")\n")},
		"none.jl": {Data: []byte("# nothing\n")},
		"deep.jl": {Data: []byte(strings.Repeat("(", 5000) + "1" + strings.Repeat(")", 5000))},
	}
	m := vm.modules["Main"]
	cases := []struct {
		name string
		args []any
		want string
	}{
		{"the last value", []any{m, "two.jl"}, "(1, 2)"},
		{"an empty file", []any{m, "none.jl"}, "nothing"},
		{"a missing file", []any{m, "nope.jl"}, "!File \\\"nope.jl\\\" not found"},
		{"a host path", []any{m, "/etc/passwd"}, "!not found"},
		{"a syntax error", []any{m, "bad.jl"}, "!syntax:"},
		{"a file over the parser's limits", []any{m, "deep.jl"}, "!syntax:"},
		{"a non-module", []any{int64(1), "two.jl"}, "!TypeError(:include, \"\", Module, 1)"},
		{"a non-string", []any{m, int64(1)}, "!TypeError(:include, \"\", String, 1)"},
	}
	for _, c := range cases {
		v, err := vm.jlLoad(c.args)
		check(t, c.name, v, err, c.want)
	}
	vm.MaxDepth = 20
	v, err := vm.jlLoad([]any{vm.Core, "self.jl"})
	check(t, "a file that includes itself", v, err, "!StackOverflowError()")
	if vm.depth != 0 {
		t.Errorf("depth after the overflow: %d", vm.depth)
	}
	// julia's own sources are there by default
	for _, name := range []string{"boot.jl", "optimized_generics.jl"} {
		if _, err := fs.Stat(New().Sources, name); err != nil {
			t.Errorf("default sources: %v", err)
		}
	}
}
