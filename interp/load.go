package interp

import (
	"embed"
	"io/fs"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/syntax"
)

// julia's own sources that Core and Base include, unmodified (boot/README.md),
// at their paths under julia's base/: the directory julia builds its system
// image from, so the names the include calls give resolve as they do there.
//
//go:embed boot/*.jl
var bootFiles embed.FS

// baseSources is bootFiles rooted at boot/, as julia's base/ is.
var baseSources = must(fs.Sub(bootFiles, "boot"))

// jlLoad is jl_load_ (toplevel.c:1194-1201): the file's forms evaluated in
// module m one by one, the value of the last returned. The file comes from
// vm.Sources, never from the host's filesystem: include in a notebook cell
// reaches julia's own sources only.
func (vm *VM) jlLoad(a []any) (any, error) {
	m, ok := a[0].(*Module)
	if !ok {
		return nil, vm.typeError("include", "", core.ModuleType, a[0])
	}
	name, ok := a[1].(string)
	if !ok {
		return nil, vm.typeError("include", "", core.StringType, a[1])
	}
	// jl_file_content_as_string (toplevel.c:1175-1190)
	src, err := fs.ReadFile(vm.Sources, name)
	if err != nil {
		return nil, vm.errorf("File \"%s\" not found", name)
	}
	// a file that includes itself nests like a call
	if vm.depth >= vm.MaxDepth {
		return nil, vm.exception(vm.tStackOverflow)
	}
	vm.depth++
	defer func() { vm.depth-- }()
	// jl_parse_eval_all (toplevel.c:1101-1172). The error is rethrown as is:
	// jl_loaderror_type is set by post_boot_hooks, after boot.jl.
	top := syntax.ParseAll(src, name).(*expr.Expr)
	if top.Head != "toplevel" {
		return nil, vm.syntaxError(top)
	}
	s := vm.sessionFor(m)
	var v any
	for _, form := range top.Args {
		if _, ok := form.(expr.LineNumberNode); ok {
			continue
		}
		if v, err = s.evalForm(form); err != nil {
			return nil, err
		}
	}
	return v, nil
}
