package interp

import (
	"fmt"
	"hash/crc32"
	"io/fs"
	"path"
	"strings"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Base.jl:410-415 records each Compiler source Base included as a
// dependency, (mod, path, size, crc32c, -1.0), through
// _include_dependency!(_compiler_require_dependencies, true, mod, file,
// true, false) (loading.jl:2258-2285): it stats and reads the files, which
// the sandbox's file system refuses. They are the sources embedded in the
// VM (VM.Sources), so the loop is done here, from them: the same path,
// size and CRC-32C, never reaching a file system.

// isCompilerDependencyLoop reports Base.jl:410's for loop over
// _included_files that fills _compiler_require_dependencies through
// _include_dependency! (not the let's loop after it, Base.jl:418, which
// only rewrites the paths).
func isCompilerDependencyLoop(form any) bool {
	e, ok := form.(*expr.Expr)
	return ok && e.Head == "for" && mentions(e, "_include_dependency!") && mentions(e, "_compiler_require_dependencies")
}

// replaceCompilerDependencyLoop gives form, a top-level form of Base.jl, with
// the loop, wherever it sits (Base.jl:324's `if is_primary_base_module`
// holds it), a call of a function of the VM that does it.
func (vm *VM) replaceCompilerDependencyLoop(form any, base *Module) any {
	e, ok := form.(*expr.Expr)
	if !ok {
		return form
	}
	if isCompilerDependencyLoop(e) {
		fn := vm.newFunction(base, "#record_compiler_dependencies")
		m := must(fn.Table.Define(core.EmptyTupleType, vm.newWorld(), nil))
		m.Def = goMethod(func(vm *VM, args []any) (any, error) { return nil, vm.recordCompilerDependencies(base) })
		return expr.New("call", fn)
	}
	for i, a := range e.Args {
		e.Args[i] = vm.replaceCompilerDependencyLoop(a, base)
	}
	return e
}

// mentions reports that the expression holds the symbol.
func mentions(v any, s expr.Symbol) bool {
	switch x := v.(type) {
	case expr.Symbol:
		return x == s
	case *expr.Expr:
		for _, a := range x.Args {
			if mentions(a, s) {
				return true
			}
		}
	}
	return false
}

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// recordCompilerDependencies is the loop: for each (mod, file) of
// Base._included_files whose module is Compiler, a module of Compiler, or
// whose file is a Compiler.jl, the tuple _include_dependency! pushes: the
// file made absolute against the working directory (abspath, as source_path
// is nothing while Base loads), its size, its CRC-32C (_crc32c,
// util.jl:519-532), and -1.0 for mtime.
func (vm *VM) recordCompilerDependencies(base *Module) error {
	value := func(name expr.Symbol) any {
		if b := base.own(name); b != nil {
			return b.Value
		}
		return nil
	}
	_, _, files, ok1 := arrayOf(value("_included_files"))
	deps, ok2 := value("_compiler_require_dependencies").(*Struct)
	compiler, ok3 := value("Compiler").(*Module)
	if !ok1 || !ok2 || !ok3 {
		return fmt.Errorf("%w: Base's Compiler dependencies without _included_files, _compiler_require_dependencies or Compiler", ErrUnsupported)
	}
	for _, f := range files {
		t, ok := f.(*Tuple)
		if !ok {
			continue // a slot not assigned yet (Base_compiler.jl:10-11)
		}
		mod, _ := t.Elems[0].(*Module)
		file, _ := t.Elems[1].(string)
		if mod != compiler && (mod == nil || mod.Parent != compiler) && !strings.HasSuffix(file, "/Compiler.jl") {
			continue
		}
		data, err := fs.ReadFile(vm.Sources, file)
		if err != nil {
			return fmt.Errorf("%w: the Compiler source %s, not among the VM's", ErrUnsupported, file)
		}
		p := file
		if !path.IsAbs(p) {
			p = path.Join(vm.WorkDir, p)
		}
		vm.vectorPush(deps, vm.newTuple([]any{mod, p, uint64(len(data)), crc32.Checksum(data, castagnoli), -1.0}))
	}
	return nil
}
