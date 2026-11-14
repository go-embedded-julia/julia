package interp

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/syntax"
)

// julia's own sources that Core and Base include, unmodified (boot/README.md),
// at their paths under julia's base/: the directory julia builds its system
// image from, so the names the include calls give resolve as they do there.
//
//go:embed boot
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
	// jl_parse_eval_all (toplevel.c:1101-1172)
	top := syntax.ParseAll(src, name).(*expr.Expr)
	if top.Head != "toplevel" {
		return nil, vm.syntaxError(top)
	}
	s := vm.sessionFor(m)
	var v any
	line := 0
	for _, form := range top.Args {
		if l, ok := form.(expr.LineNumberNode); ok {
			line = l.Line
			continue
		}
		// JuliaSyntax, the parser Base includes from its Julia source
		// (Base.jl:317), is package syntax here: Base gets a native module
		// in its place (juliasyntax.go)
		if m == vm.baseModule && isJuliaSyntaxInclude(form) {
			vm.defineJuliaSyntax(m)
			continue
		}
		// Base.jl:410-415 reads the Compiler's sources to record them as
		// dependencies: done from the embedded ones (compilerdeps.go)
		if m == vm.baseModule {
			form = vm.replaceCompilerDependencyLoop(form, m)
		}
		if v, err = s.evalForm(form); err != nil {
			// a Julia exception comes out as LoadError(file, line, error)
			// once post_boot_hooks has found LoadError; before that, as is
			if t, ok := err.(*Thrown); ok && vm.tLoadError != nil {
				// the LoadError keeps the backtrace of what it wraps
				return nil, &Thrown{Value: &Struct{Type: vm.tLoadError, Fields: []any{name, int64(line), t.Value}}, Trace: t.Trace}
			}
			if _, ok := err.(*Thrown); ok {
				return nil, err
			}
			// a refusal of the VM's says where, as a LoadError would
			return nil, fmt.Errorf("%s:%d: %w", name, line, err)
		}
	}
	return v, nil
}

// isCompilerBootstrap reports the form Compiler.bootstrap!() (see
// VM.RunCompilerBootstrap).
func isCompilerBootstrap(form any) bool {
	call, ok := form.(*expr.Expr)
	if !ok || call.Head != "call" || len(call.Args) != 1 {
		return false
	}
	dot, ok := call.Args[0].(*expr.Expr)
	if !ok || dot.Head != "." || len(dot.Args) != 2 || dot.Args[0] != any(expr.Symbol("Compiler")) {
		return false
	}
	q, ok := dot.Args[1].(expr.QuoteNode)
	return ok && q.Value == any(expr.Symbol("bootstrap!"))
}
