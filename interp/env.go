package interp

import (
	"fmt"
	"sort"
	"strings"
)

// The environment Base's ENV reads and writes (env.jl): VM.Env, never the
// host's. getenv, setenv and unsetenv act on it; jl_environ enumerates it,
// in sorted order, as "name=value".

// uvPathOut is libuv's way of returning a path through (buf, &size): the
// path and its NUL when *size holds them, *size then its length, and 0;
// else UV_ENOBUFS (-55 on darwin, uv_constants.jl) with the size needed.
func (vm *VM) uvPathOut(fname, path string, a []any) (any, error) {
	sp, ok := a[1].(*Pointer)
	if !ok {
		return nil, fmt.Errorf("%w: %s of %s", ErrUnsupported, fname, showValue(vm.newTuple(a)))
	}
	sv, err := vm.ptrLoad(fname, sp, 1)
	if err != nil {
		return nil, err
	}
	size, _ := sv.(uint64)
	if uint64(len(path))+1 > size {
		return int32(-55), vm.storeAs(fname, sp, uint64(len(path)+1))
	}
	if err := vm.writeBytes(a[0], append([]byte(path), 0)); err != nil {
		return nil, err
	}
	return int32(0), vm.storeAs(fname, sp, uint64(len(path)))
}

func (vm *VM) bootEnvShims() {
	vm.shims["getenv"] = func(vm *VM, a []any) (any, error) {
		name, ok := vm.cstringArg(a[0])
		if !ok {
			return nil, fmt.Errorf("%w: getenv of %s", ErrUnsupported, showValue(a[0]))
		}
		if v, ok := vm.Env[name]; ok {
			return vm.stringPtrOf(v), nil
		}
		return &Pointer{Type: vm.stringPtrOf("").Type}, nil // NULL
	}
	vm.shims["setenv"] = func(vm *VM, a []any) (any, error) {
		name, ok1 := vm.cstringArg(a[0])
		val, ok2 := vm.cstringArg(a[1])
		overwrite, ok3 := a[2].(int32)
		if !ok1 || !ok2 || !ok3 {
			return nil, fmt.Errorf("%w: setenv of %s", ErrUnsupported, showValue(vm.newTuple(a)))
		}
		// setenv(3) refuses an empty name or one holding '='
		if name == "" || strings.Contains(name, "=") {
			return int32(-1), nil
		}
		if _, set := vm.Env[name]; set && overwrite == 0 {
			return int32(0), nil
		}
		if vm.Env == nil {
			vm.Env = map[string]string{}
		}
		vm.Env[name] = val
		return int32(0), nil
	}
	vm.shims["unsetenv"] = func(vm *VM, a []any) (any, error) {
		name, ok := vm.cstringArg(a[0])
		if !ok {
			return nil, fmt.Errorf("%w: unsetenv of %s", ErrUnsupported, showValue(a[0]))
		}
		if name == "" || strings.Contains(name, "=") {
			return int32(-1), nil
		}
		delete(vm.Env, name)
		return int32(0), nil
	}
	// uv_os_homedir(buf, &size) (libuv): $HOME when set and not empty, else
	// VM.HomeDir (libuv's passwd entry), copied with its NUL when *size holds
	// it, else UV_ENOBUFS (-55 on darwin, uv_constants.jl) and the size
	// needed (path.jl:83-97)
	vm.shims["uv_os_homedir"] = func(vm *VM, a []any) (any, error) {
		home := vm.Env["HOME"]
		if home == "" {
			home = vm.HomeDir
		}
		return vm.uvPathOut("uv_os_homedir", home, a)
	}
	// uv_cwd(buf, &size) (libuv): VM.WorkDir, the same way (path.jl's pwd)
	vm.shims["uv_cwd"] = func(vm *VM, a []any) (any, error) { return vm.uvPathOut("uv_cwd", vm.WorkDir, a) }
	// the host's filesystem is out of reach: every path is absent, UV_ENOENT
	// (-2), which stat.jl's checkstat accepts (stat.jl:186-204); the stat
	// buffer is the official darwin/arm64 build's size
	vm.shims["jl_sizeof_stat"] = func(vm *VM, a []any) (any, error) { return int32(160), nil }
	noPath := func(vm *VM, a []any) (any, error) {
		if _, ok := vm.cstringArg(a[0]); !ok {
			return nil, fmt.Errorf("%w: stat of %s", ErrUnsupported, showValue(a[0]))
		}
		return int32(-2), nil
	}
	vm.shims["jl_stat"], vm.shims["jl_lstat"] = noPath, noPath
	// jl_environ(i) (sys.c): the i-th "name=value", nothing past the last
	vm.shims["jl_environ"] = func(vm *VM, a []any) (any, error) {
		i, ok := a[0].(int32)
		if !ok {
			return nil, fmt.Errorf("%w: jl_environ of %s", ErrUnsupported, showValue(a[0]))
		}
		names := make([]string, 0, len(vm.Env))
		for k := range vm.Env {
			names = append(names, k)
		}
		sort.Strings(names)
		if i < 0 || int(i) >= len(names) {
			return nil, nil
		}
		return names[i] + "=" + vm.Env[names[i]], nil
	}
}
