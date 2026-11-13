package interp

import (
	"path"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The libraries the VM serves open as julia 1.12.6 opens them on
// darwin/arm64 (dl_oracle: dlpath of each _jll library, the errors of an
// unknown library and symbol), by any of their names; the same library
// gives the same handle; a library elsewhere, or not one of them, does
// not open.
func TestLoadDynamicLibrary(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	load := vm.shims["jl_load_dynamic_library"]
	path := vm.shims["jl_pathname_for_handle"]
	open := func(name string, flags uint32, throw int32) (any, error) {
		return load(vm, []any{vm.stringPtrOf(name), flags, throw})
	}
	for _, c := range []struct{ name, want string }{
		{"@rpath/libgfortran.5.dylib", "/julia/lib/julia/libgfortran.5.dylib"},
		{"@rpath/libgcc_s.1.1.dylib", "/julia/lib/julia/libgcc_s.1.1.dylib"},
		{"@rpath/libstdc++.6.dylib", "/julia/lib/julia/libstdc++.6.dylib"},
		{"@rpath/libgomp.1.dylib", "/julia/lib/julia/libgomp.1.dylib"},
		{"@rpath/libopenblas64_.dylib", "/julia/lib/julia/libopenblas64_.dylib"},
		{"@rpath/libblastrampoline.5.dylib", "/julia/lib/julia/libblastrampoline.5.dylib"},
		{"libdSFMT", "/julia/lib/julia/libdSFMT.dylib"},
		{"@loader_path/libssp.0.dylib", "/julia/lib/julia/libssp.0.dylib"},
		{"/julia/lib/julia/libmpfr.6.dylib", "/julia/lib/julia/libmpfr.6.dylib"},
		{"@executable_path/../lib/julia/libpcre2-8.dylib", "/julia/lib/julia/libpcre2-8.dylib"},
	} {
		h, err := open(c.name, 0, 1)
		if err != nil {
			t.Errorf("dlopen(%q): %v", c.name, err)
			continue
		}
		p, err := path(vm, []any{h})
		if err != nil {
			t.Fatal(err)
		}
		s, err := vm.cstrToString([]any{p})
		check(t, c.name, s, err, `"`+c.want+`"`)
	}
	// once open, a library is the same handle, and its first path stays
	h1, _ := open("libgfortran", 0, 1)
	h2, _ := open("/julia/lib/julia/libgfortran.dylib", 0, 1)
	if h1 != h2 {
		t.Error("libgfortran opened twice gives two handles")
	}
	// RTLD_NOLOAD finds an open library only
	if h, err := open("libgmp", rtldNoload, 1); err == nil {
		t.Errorf("RTLD_NOLOAD of a library not yet open: %s", showValue(h))
	}
	if h, err := open("libgfortran", rtldNoload, 1); err != nil || h != h1 {
		t.Errorf("RTLD_NOLOAD of an open library: %v %v", h, err)
	}
	for _, name := range []string{"libnope", "/abs/libnope.dylib", "/usr/lib/libgfortran.5.dylib",
		"@rpath/sub/libgfortran.5.dylib", "libgfortran.7.dylib", "lib/julia/libgfortran.dylib", ""} {
		_, err := open(name, 0, 1)
		check(t, name, nil, err, `!could not load library \"`+name+`\"`)
		v, err := open(name, 0, 0)
		if p, ok := v.(*Pointer); err != nil || !ok || p.Target != nil {
			t.Errorf("dlopen(%q; throw_error=false) = %v, %v", name, v, err)
		}
	}
	_, err := load(vm, []any{int64(1), uint32(0), int32(1)})
	check(t, "a name that is no C string", nil, err, "!could not load library: a name that is no C string")
	// the path of anything that is no library is NULL
	p, err := path(vm, []any{int64(0)})
	if q, ok := p.(*Pointer); err != nil || !ok || q.Target != nil {
		t.Errorf("jl_pathname_for_handle(0) = %v, %v", p, err)
	}
	// dlclose: -1 for what is no handle, 0 for a library
	cl := vm.shims["jl_dlclose"]
	v, _ := cl(vm, []any{h1})
	check(t, "dlclose", v, nil, "0")
	v, _ = cl(vm, []any{&Pointer{Type: vm.ptrType(core.NothingType)}})
	check(t, "dlclose(C_NULL)", v, nil, "-1")
}

// jl_dlsym stores the address of a function the library has, which a
// ccall then calls; one it has not is NULL, or julia's error.
func TestDlsym(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	box, err := s.Eval("mutable struct PBox; p::Ptr{Nothing}; end; PBox(Core.Intrinsics.bitcast(Ptr{Nothing}, 0))")
	if err != nil {
		t.Fatal(err)
	}
	out := &Pointer{Type: vm.ptrType(vm.ptrType(core.NothingType)), Target: box}
	h, err := vm.shims["jl_load_dynamic_library"](vm, []any{vm.stringPtrOf("@rpath/libdSFMT.dylib"), uint32(0), int32(1)})
	if err != nil {
		t.Fatal(err)
	}
	sym := vm.shims["jl_dlsym"]
	v, err := sym(vm, []any{h, vm.stringPtrOf("dsfmt_get_min_array_size"), out, int32(1)})
	check(t, "a function the library has", v, err, "1")
	fn := box.(*Struct).Fields[0]
	if err := vm.declareConst(expr.GlobalRef{Mod: s.Mod.Name, Name: "FN", Obj: s.Mod}, fn); err != nil {
		t.Fatal(err)
	}
	v, err = s.Eval("ccall(FN, Int32, ())")
	check(t, "a ccall through it", v, err, "382")
	// a library known by a file name only: ccall((f, "@rpath/libgmp.10.dylib")) as
	// gmp.jl names it, and the same library by another file name
	v, err = s.Eval(`ccall((:dsfmt_get_min_array_size, "@rpath/libdSFMT.dylib"), Int32, ())`)
	check(t, "a library named by a file", v, err, "382")
	v, err = sym(vm, []any{h, vm.stringPtrOf("nope"), out, int32(0)})
	check(t, "a function it has not", v, err, "0")
	if p := box.(*Struct).Fields[0].(*Pointer); p.Target != nil {
		t.Errorf("dlsym of a missing function stored %s", showValue(p))
	}
	_, err = sym(vm, []any{h, vm.stringPtrOf("nope"), out, int32(1)})
	check(t, "and the error", nil, err, `!could not load symbol \"nope\":\ndlsym(libdSFMT, nope): symbol not found`)
	_, err = sym(vm, []any{int64(0), vm.stringPtrOf("nope"), out, int32(1)})
	check(t, "no handle", nil, err, "!could not load symbol: invalid arguments")
	_, err = sym(vm, []any{h, vm.stringPtrOf("dsfmt_get_min_array_size"), &Pointer{Type: out.Type}, int32(1)})
	check(t, "nowhere to store it", nil, err, "!ccall: invalid pointer")
	// a ccall through another pointer is refused
	if err := vm.declareConst(expr.GlobalRef{Mod: s.Mod.Name, Name: "NOFN", Obj: s.Mod}, &Pointer{Type: vm.ptrType(core.NothingType), Target: cFunc{"libdSFMT", "nope"}}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Eval("ccall(NOFN, Int32, ())")
	check(t, "a pointer to no function", nil, err, "!not ported yet: a ccall through")
	_, err = s.Eval("ccall(Core.Intrinsics.bitcast(Ptr{Nothing}, 0), Int32, ())")
	check(t, "C_NULL", nil, err, "!not ported yet: a ccall through")
}

// Whatever name a program gives, a library it opens is a file of the
// library directory the VM serves, and that file's library.
func FuzzLibFile(f *testing.F) {
	for _, s := range []string{"libgfortran", "@rpath/libgmp.10.dylib", "/julia/lib/julia/libssp.dylib",
		"@executable_path/../lib/julia/libgomp.dylib", "/julia/lib/julia/../julia/libgomp.dylib", "@rpath/", "/", ".dylib"} {
		f.Add(s)
	}
	vm := New()
	f.Fuzz(func(t *testing.T, name string) {
		file, lib, ok := vm.libFile(name)
		if !ok {
			return
		}
		dir, base := path.Split(file)
		if path.Clean(dir) != vm.libDir() || dynLibFiles[base] != lib {
			t.Errorf("libFile(%q) = %q, %q", name, file, lib)
		}
	})
}

// jl_effective_threads is the processor count, as Sys.CPU_THREADS.
func TestEffectiveThreads(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	a, _ := vm.shims["jl_effective_threads"](vm, nil)
	b, _ := vm.shims["jl_cpu_threads"](vm, nil)
	if a != b {
		t.Errorf("jl_effective_threads %v, jl_cpu_threads %v", a, b)
	}
}
