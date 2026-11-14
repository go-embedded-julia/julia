package interp

import (
	"path"
	"strings"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Dynamic libraries (src/dlload.c). The libraries a julia program can open
// are those the interpreter implements in Go: their functions are the
// VM's libShims. They live, as julia's bundled libraries do, in the
// lib/julia directory beside Sys.BINDIR, under the file names julia
// 1.12.6's darwin/arm64 build installs there. A library that is not one
// of them cannot be opened, in the sandbox or on the host: loading a C
// library is a stated divergence (README).

// dynLibFiles maps each file of lib/julia the VM serves to its library.
var dynLibFiles = map[string]string{
	"libblastrampoline.5.15.0.dylib": "libblastrampoline", "libblastrampoline.5.dylib": "libblastrampoline", "libblastrampoline.dylib": "libblastrampoline",
	"libdSFMT.dylib":      "libdSFMT",
	"libgcc_s.1.1.dylib":  "libgcc_s",
	"libgfortran.5.dylib": "libgfortran", "libgfortran.dylib": "libgfortran",
	"libgmp.10.dylib": "libgmp", "libgmp.dylib": "libgmp",
	"libgomp.1.dylib": "libgomp", "libgomp.dylib": "libgomp",
	"libmpfr.6.dylib": "libmpfr", "libmpfr.dylib": "libmpfr",
	"libopenblas64_.0.3.29.dylib": "libopenblas64_", "libopenblas64_.0.dylib": "libopenblas64_", "libopenblas64_.dylib": "libopenblas64_",
	"libpcre2-8.0.dylib": "libpcre2-8", "libpcre2-8.dylib": "libpcre2-8",
	"libssp.0.dylib": "libssp", "libssp.dylib": "libssp",
	"libstdc++.6.dylib": "libstdc++", "libstdc++.dylib": "libstdc++",
}

// dynLib is an opened library: its name in dynLibFiles and the path it was
// first opened by, which jl_pathname_for_handle answers.
type dynLib struct {
	name, path string
}

// rtldNoload is JL_RTLD_NOLOAD (julia.h, libdl.jl:29).
const rtldNoload = 0x20

// libDir is the directory julia's bundled libraries are in.
func (vm *VM) libDir() string { return path.Join(vm.BinDir, "..", "lib", "julia") }

// libFile is the library a name designates, as jl_load_dynamic_library
// (dlload.c:273-404) and the dynamic linker find it on darwin: an absolute
// path, or one under @executable_path/, names a file; an @rpath/ or
// @loader_path/ one, or a bare name (DL_LOAD_PATH is "@loader_path", the
// library directory), names a file of the library directory; without the
// .dylib extension, the name is tried as is and with it.
func (vm *VM) libFile(modname string) (file, name string, ok bool) {
	p := modname
	if rest, at := strings.CutPrefix(p, "@executable_path/"); at {
		p = path.Join(vm.BinDir, rest)
	}
	dir := ""
	switch {
	case strings.HasPrefix(p, "/"):
		dir, p = path.Split(path.Clean(p))
		if path.Clean(dir) != vm.libDir() {
			return "", "", false
		}
	case strings.HasPrefix(p, "@rpath/"):
		p = p[len("@rpath/"):]
	case strings.HasPrefix(p, "@loader_path/"):
		p = p[len("@loader_path/"):]
	}
	if strings.Contains(p, "/") {
		return "", "", false
	}
	candidates := []string{p}
	if !strings.HasSuffix(p, ".dylib") {
		candidates = append(candidates, p+".dylib")
	}
	for _, c := range candidates {
		if name, ok := dynLibFiles[c]; ok {
			return path.Join(vm.libDir(), c), name, true
		}
	}
	return "", "", false
}

// loadDynamicLibrary is jl_load_dynamic_library(modname, flags, throw_err):
// a handle, the same one each time a library is opened; NULL, or julia's
// error when throw_err is set, for a library the VM does not have, or one
// not yet open under RTLD_NOLOAD.
func (vm *VM) loadDynamicLibrary(a []any) (any, error) {
	modname, ok := vm.cstringArg(a[0])
	if !ok {
		return nil, vm.errorf("could not load library: a name that is no C string")
	}
	flags, _ := a[1].(uint32)
	throw, _ := a[2].(int32)
	file, name, found := vm.libFile(modname)
	h := vm.dynLibs[name]
	if found && h == nil && flags&rtldNoload == 0 {
		if vm.dynLibs == nil {
			vm.dynLibs = map[string]*Pointer{}
		}
		h = &Pointer{Type: vm.ptrType(core.NothingType), Target: &dynLib{name: name, path: file}}
		vm.dynLibs[name] = h
	}
	if found && h != nil {
		return h, nil
	}
	if throw != 0 {
		return nil, vm.errorf("could not load library \"%s\"\n%s: no library of that name in this interpreter (%s)", modname, modname, vm.libDir())
	}
	return &Pointer{Type: vm.ptrType(core.NothingType)}, nil
}

// handleLib is the library a handle names.
func handleLib(v any) (*dynLib, bool) {
	p, ok := v.(*Pointer)
	if !ok || p.Off != 0 {
		return nil, false
	}
	l, ok := p.Target.(*dynLib)
	return l, ok
}

// dlsym is jl_dlsym(handle, symbol, value, throw_err) (dlload.c:406-444):
// the address of a function of the library in *value, and whether it has
// one; julia's error when it has not and throw_err is set.
func (vm *VM) dlsym(a []any) (any, error) {
	l, ok := handleLib(a[0])
	sym, isStr := vm.cstringArg(a[1])
	out, isPtr := a[2].(*Pointer)
	if !ok || !isStr || !isPtr {
		return nil, vm.errorf("could not load symbol: invalid arguments")
	}
	throw, _ := a[3].(int32)
	voidPtr := vm.ptrType(core.NothingType)
	var v *Pointer
	if vm.libShims[l.name][expr.Symbol(sym)] != nil {
		v = &Pointer{Type: voidPtr, Target: cFunc{l.name, expr.Symbol(sym)}}
	}
	if v == nil {
		if throw != 0 {
			return nil, vm.errorf("could not load symbol \"%s\":\ndlsym(%s, %s): symbol not found", sym, l.name, sym)
		}
		v = &Pointer{Type: voidPtr}
	}
	if err := vm.ptrStore("ccall", out, any(v), 1); err != nil {
		return nil, err
	}
	if v.Target == nil {
		return int32(0), nil
	}
	return int32(1), nil
}

// bootDynLibs registers the calls of dlload.c and gives each library of
// dynLibFiles that has Go functions its name in libShims too, so a ccall
// naming it by any of its file names, and dlsym, reach them.
func (vm *VM) bootDynLibs() {
	for lib, fns := range vm.libShims {
		if _, name, ok := vm.libFile(lib); ok && vm.libShims[name] == nil {
			vm.libShims[name] = fns
		}
	}
	vm.shims["jl_load_dynamic_library"] = func(vm *VM, a []any) (any, error) { return vm.loadDynamicLibrary(a) }
	vm.shims["jl_dlsym"] = func(vm *VM, a []any) (any, error) { return vm.dlsym(a) }
	// jl_dlclose (dlload.c:227-241): -1 for NULL, else dlclose's 0; a
	// library here stays loaded, as one dlopen opened twice does
	vm.shims["jl_dlclose"] = func(vm *VM, a []any) (any, error) {
		if _, ok := handleLib(a[0]); !ok {
			return int32(-1), nil
		}
		return int32(0), nil
	}
	// jl_pathname_for_handle (dlload.c): the path a library was opened by,
	// NULL for anything else
	vm.shims["jl_pathname_for_handle"] = func(vm *VM, a []any) (any, error) {
		if l, ok := handleLib(a[0]); ok {
			return vm.stringPtrOf(l.path), nil
		}
		return &Pointer{Type: vm.ptrType(core.UInt8Type)}, nil
	}
}

// libShimsOf is the Go functions of the library a ccall names: by the name
// it was registered under, else by the library that name designates.
func (vm *VM) libShimsOf(lib string) map[expr.Symbol]shim {
	if fns := vm.libShims[lib]; fns != nil {
		return fns
	}
	if _, name, ok := vm.libFile(lib); ok {
		return vm.libShims[name]
	}
	return nil
}
