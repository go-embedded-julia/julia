package interp

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The shims that reach the VM's filesystem (vfs.go): stat and its buffer
// (sys.c:108-221), access and libuv's requests (jl_uv.c, libuv fs.c),
// getattrlist (what loading.jl's isfile_casesensitive asks of darwin), malloc
// for the requests, errno and the CRC32C that dependency tracking hashes
// files with. The IOStream calls are in ios.go.

// uvReq is a libuv request julia code allocated with malloc (a uv_fs_t,
// _sizeof_uv_fs bytes): what the call left for jl_uv_fs_t_ptr and
// uv_fs_scandir_next to read.
type uvReq struct {
	ptr     string // req->ptr, a path
	hasPtr  bool
	entries []dirent
	next    int
}

// intArg is any integer argument as an int64.
func intArg(v any) (int64, bool) {
	switch x := v.(type) {
	case int8:
		return int64(x), true
	case int16:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint8:
		return int64(x), true
	case uint16:
		return int64(x), true
	case uint32:
		return int64(x), true
	case uint64:
		if x > math.MaxInt64 {
			return 0, false
		}
		return int64(x), true
	}
	if b, _, ok := primBits(v); ok { // a RawFD, a primitive type of the program's
		return int64(int32(b.lo)), true
	}
	return 0, false
}

// putBytes writes b where a Ptr points: into a memory of bytes within its
// length, or as writeBytes does.
func (vm *VM) putBytes(dst any, b []byte) error {
	if p, ok := dst.(*Pointer); ok {
		if m, isMem := p.Target.(*Memory); isMem && m.Type == memUInt8Type {
			if p.Off < 0 || p.Off > int64(len(m.Elems)) || int64(len(b)) > int64(len(m.Elems))-p.Off {
				return vm.errorf("ccall: invalid pointer")
			}
			for i, c := range b {
				m.Elems[p.Off+int64(i)] = c
			}
			return nil
		}
	}
	return vm.writeBytes(dst, b)
}

// room is the bytes that can be written from where a Ptr points, in a memory
// of bytes or a String allocString made; ok false for any other pointer.
func (vm *VM) room(dst any) (int64, bool) {
	p, ok := dst.(*Pointer)
	if !ok || p.Off < 0 {
		return 0, false
	}
	switch t := p.Target.(type) {
	case *Memory:
		if t.Type == memUInt8Type && p.Off <= int64(len(t.Elems)) {
			return int64(len(t.Elems)) - p.Off, true
		}
	case string:
		if w, ok := vm.writableBytes(t); ok && p.Off <= int64(len(w)) {
			return int64(len(w)) - p.Off, true
		}
	}
	return 0, false
}

// cpath is a Cstring argument.
func (vm *VM) cpath(fname string, v any) (string, error) {
	s, ok := vm.cstringArg(v)
	if !ok {
		return "", fmt.Errorf("%w: %s of %s", ErrUnsupported, fname, showValue(v))
	}
	return s, nil
}

// voidPtr is a Ptr{Cvoid} to what p points at, NULL when p is nil.
func (vm *VM) voidPtr(target any) *Pointer {
	return &Pointer{Type: vm.ptrType(core.NothingType), Target: target}
}

// statInto is the common part of jl_stat, jl_lstat and jl_fstat (sys.c:111-147):
// the stat's result, and when it is 0 the uv_stat_t written to the buffer.
func (vm *VM) statInto(st uvStat, code int32, buf any) (any, error) {
	if code == 0 {
		if err := vm.putBytes(buf, st.bytes()); err != nil {
			return nil, err
		}
	}
	return code, nil
}

func (vm *VM) bootFileShims() {
	vm.shims["jl_sizeof_stat"] = func(vm *VM, a []any) (any, error) { return int32(uvStatSize), nil }
	// jl_uv_sizeof_interface_address (jl_uv.c:939-942), sizeof
	// uv_interface_address_t: 96 in julia 1.12.6's darwin/arm64 build, which
	// Sockets reads as it loads (addrinfo.jl:256)
	vm.shims["jl_uv_sizeof_interface_address"] = func(vm *VM, a []any) (any, error) { return int32(96), nil }
	// jl_stat and jl_lstat (sys.c:111-135) name a path, jl_fstat (137-147) a
	// descriptor
	for name, follow := range map[expr.Symbol]bool{"jl_stat": true, "jl_lstat": false} {
		vm.shims[name] = func(vm *VM, a []any) (any, error) {
			p, err := vm.cpath(string(name), a[0])
			if err != nil {
				return nil, err
			}
			st, code := vm.fsys().stat(p, follow)
			return vm.statInto(st, code, a[1])
		}
	}
	vm.shims["jl_fstat"] = func(vm *VM, a []any) (any, error) {
		fd, ok := intArg(a[0])
		if !ok {
			return nil, fmt.Errorf("%w: jl_fstat of %s", ErrUnsupported, showValue(a[0]))
		}
		st, code := vm.fsys().fstat(int32(fd))
		return vm.statInto(st, code, a[1])
	}
	// the fields of a uv_stat_t (sys.c:149-221): the buffer's little-endian
	// words, the integers as the C function returns them, unsigned int
	// truncating the 64-bit field
	word := func(field int) func(vm *VM, buf any) (uint64, error) {
		return func(vm *VM, buf any) (uint64, error) {
			p, ok := buf.(*Pointer)
			if !ok {
				return 0, fmt.Errorf("%w: a stat buffer %s", ErrUnsupported, showValue(buf))
			}
			b, ok := vm.bytesAt(p, uvStatSize)
			if !ok {
				return 0, vm.errorf("ccall: invalid pointer")
			}
			return binary.LittleEndian.Uint64(b[field*8:]), nil
		}
	}
	for name, field := range map[expr.Symbol]int{"jl_stat_dev": 0, "jl_stat_mode": 1, "jl_stat_nlink": 2,
		"jl_stat_uid": 3, "jl_stat_gid": 4, "jl_stat_rdev": 5, "jl_stat_ino": 6} {
		get := word(field)
		vm.shims[name] = func(vm *VM, a []any) (any, error) {
			w, err := get(vm, a[0])
			return uint32(w), err
		}
	}
	for name, field := range map[expr.Symbol]int{"jl_stat_size": 7, "jl_stat_blksize": 8, "jl_stat_blocks": 9} {
		get := word(field)
		vm.shims[name] = func(vm *VM, a []any) (any, error) { return get(vm, a[0]) }
	}
	// jl_stat_mtime and jl_stat_ctime: seconds plus nanoseconds * 1e-9; the
	// timespecs follow the twelve words (atim, mtim, ctim, birthtim)
	for name, field := range map[expr.Symbol]int{"jl_stat_mtime": 14, "jl_stat_ctime": 16} {
		get := word(field)
		nsec := word(field + 1)
		vm.shims[name] = func(vm *VM, a []any) (any, error) {
			s, err := get(vm, a[0])
			if err != nil {
				return nil, err
			}
			n, _ := nsec(vm, a[0])
			return float64(int64(s)) + float64(int64(n))*1e-9, nil
		}
	}
	// jl_fs_access (jl_uv.c:626-632): uv_fs_access's result
	vm.shims["jl_fs_access"] = func(vm *VM, a []any) (any, error) {
		p, err := vm.cpath("jl_fs_access", a[0])
		if err != nil {
			return nil, err
		}
		mode, ok := intArg(a[1])
		if !ok {
			return nil, fmt.Errorf("%w: jl_fs_access of %s", ErrUnsupported, showValue(a[1]))
		}
		return vm.fsys().access(p, int32(mode)), nil
	}
	// getattrlist(path, attrlist, buf, size, options) as isfile_casesensitive
	// asks it (loading.jl:46-74): ATTR_CMN_NAME of path into buf as a u32 total
	// length (aligned to 4), an attrreference_t (offset from itself, length
	// with the NUL) and the name stored in the directory, truncated to the
	// room there is
	vm.shims["getattrlist"] = func(vm *VM, a []any) (any, error) {
		p, err := vm.cpath("getattrlist", a[0])
		if err != nil {
			return nil, err
		}
		size, ok := a[3].(uint64)
		if !ok || size > uint64(vm.MaxMemoryElems) {
			return nil, fmt.Errorf("%w: getattrlist of %s", ErrUnsupported, showValue(a[3]))
		}
		if _, code := vm.fsys().stat(p, false); code != 0 {
			vm.errno = -code
			return int32(-1), nil
		}
		// darwin answers ERANGE below the 4 bytes of the length and truncates
		// the rest to the room there is (as julia 1.12.6's own call answered
		// on darwin)
		const header = 12
		if size < 4 {
			vm.errno = -uvERANGE
			return int32(-1), nil
		}
		name := append([]byte(vm.fsys().caseName(p)), 0)
		total := (header + len(name) + 3) &^ 3 // aligned to 4 bytes
		b := binary.LittleEndian.AppendUint32(nil, uint32(total))
		b = binary.LittleEndian.AppendUint32(b, header-4) // from the attrreference_t at 4
		b = binary.LittleEndian.AppendUint32(b, uint32(len(name)))
		b = append(b, name...)
		b = append(b, make([]byte, total-len(b))...)
		if uint64(len(b)) > size {
			b = b[:size]
		}
		return int32(0), vm.putBytes(a[2], b)
	}
	// malloc and free for libuv's requests (libc.jl:397-410): a memory of
	// bytes, never one past VM.MaxMemoryElems (malloc answers NULL, ENOMEM)
	vm.shims["malloc"] = func(vm *VM, a []any) (any, error) {
		n, ok := intArg(a[0])
		if !ok {
			return nil, fmt.Errorf("%w: malloc of %s", ErrUnsupported, showValue(a[0]))
		}
		if n > int64(vm.MaxMemoryElems) {
			vm.errno = -uvENOMEM
			return vm.voidPtr(nil), nil
		}
		m := &Memory{Type: memUInt8Type, Elems: make([]any, max(n, 1))}
		for i := range m.Elems {
			m.Elems[i] = uint8(0)
		}
		return vm.voidPtr(m), nil
	}
	vm.shims["free"] = func(vm *VM, a []any) (any, error) {
		if p, ok := a[0].(*Pointer); ok {
			if m, isMem := p.Target.(*Memory); isMem {
				delete(vm.reqs, m)
				vm.uvForget(m)
			}
		}
		return nil, nil
	}
	// errno (sys.c's jl_errno, jl_set_errno): the VM's, as the last failed
	// call set it
	vm.shims["jl_errno"] = func(vm *VM, a []any) (any, error) { return vm.errno, nil }
	vm.shims["jl_set_errno"] = func(vm *VM, a []any) (any, error) {
		e, ok := a[0].(int32)
		if !ok {
			return nil, vm.typeError("ccall", "", core.Int32Type, a[0])
		}
		vm.errno = e
		return nil, nil
	}
	// jl_crc32c(crc, buf, len) (crc32c.c): CRC-32C continuing crc, of bytes
	// or of the word a Ref of an integer holds
	vm.shims["jl_crc32c"] = func(vm *VM, a []any) (any, error) {
		crc, ok1 := a[0].(uint32)
		n, ok2 := a[2].(uint64)
		p, ok3 := a[1].(*Pointer)
		if !ok1 || !ok2 || !ok3 {
			return nil, fmt.Errorf("%w: jl_crc32c of %s", ErrUnsupported, showValue(vm.newTuple(a)))
		}
		if st, isRef := p.Target.(*Struct); isRef && len(st.Fields) == 1 && n <= 16 {
			if bits, w, ok := primBits(st.Fields[0]); ok && uint64(w/8) >= n {
				b := binary.LittleEndian.AppendUint64(nil, bits.lo)
				b = binary.LittleEndian.AppendUint64(b, bits.hi)
				return crc32.Update(crc, castagnoli, b[:n]), nil
			}
		}
		b, ok := vm.bytesAt(p, n)
		if !ok {
			return nil, fmt.Errorf("%w: jl_crc32c of %s", ErrUnsupported, showValue(vm.newTuple(a)))
		}
		return crc32.Update(crc, castagnoli, b), nil
	}
	vm.bootUVFS()
}

// req is the request a uv_fs_t's memory stands for, made on first use.
func (vm *VM) req(fname string, v any) (*uvReq, error) {
	if p, ok := v.(*Pointer); ok {
		if m, isMem := p.Target.(*Memory); isMem && p.Off == 0 {
			if vm.reqs == nil {
				vm.reqs = map[*Memory]*uvReq{}
			}
			r := vm.reqs[m]
			if r == nil {
				r = &uvReq{}
				vm.reqs[m] = r
			}
			return r, nil
		}
	}
	return nil, fmt.Errorf("%w: %s of the request %s", ErrUnsupported, fname, showValue(v))
}

// bootUVFS are the libuv requests the synchronous calls of file.jl and
// path.jl make: the loop and callback arguments are NULL, the result is the
// call's own (the count of entries, 0, or a negative error).
func (vm *VM) bootUVFS() {
	// uv_fs_realpath(loop, req, path, cb) (libuv fs.c) leaves the path in
	// req->ptr, which jl_uv_fs_t_ptr returns (sys.c:105)
	vm.shims["uv_fs_realpath"] = func(vm *VM, a []any) (any, error) {
		r, err := vm.req("uv_fs_realpath", a[1])
		if err != nil {
			return nil, err
		}
		p, err := vm.cpath("uv_fs_realpath", a[2])
		if err != nil {
			return nil, err
		}
		real, code := vm.fsys().realpath(p)
		if code != 0 {
			return code, nil
		}
		r.ptr, r.hasPtr = real, true
		return int32(0), nil
	}
	vm.shims["jl_uv_fs_t_ptr"] = func(vm *VM, a []any) (any, error) {
		r, err := vm.req("jl_uv_fs_t_ptr", a[0])
		if err != nil {
			return nil, err
		}
		if !r.hasPtr {
			return &Pointer{Type: vm.ptrType(core.UInt8Type)}, nil
		}
		return vm.stringPtrOf(r.ptr), nil
	}
	// uv_fs_scandir(loop, req, path, flags, cb): the number of entries; they
	// wait in the request for uv_fs_scandir_next
	vm.shims["uv_fs_scandir"] = func(vm *VM, a []any) (any, error) {
		r, err := vm.req("uv_fs_scandir", a[1])
		if err != nil {
			return nil, err
		}
		p, err := vm.cpath("uv_fs_scandir", a[2])
		if err != nil {
			return nil, err
		}
		es, code := vm.fsys().readdir(p)
		if code != 0 {
			return code, nil
		}
		r.entries, r.next = es, 0
		return int32(len(es)), nil
	}
	// uv_fs_scandir_next(req, ent): the next entry as a uv_dirent_t (a name
	// and a type) stored through ent, UV_EOF after the last
	vm.shims["uv_fs_scandir_next"] = func(vm *VM, a []any) (any, error) {
		r, err := vm.req("uv_fs_scandir_next", a[0])
		if err != nil {
			return nil, err
		}
		if r.next >= len(r.entries) {
			return uvEOF, nil
		}
		ent, ok := a[1].(*Pointer)
		if !ok {
			return nil, fmt.Errorf("%w: uv_fs_scandir_next of %s", ErrUnsupported, showValue(a[1]))
		}
		d, ok := ent.Type.Parameters[0].(*core.DataType)
		if !ok {
			return nil, fmt.Errorf("%w: uv_fs_scandir_next of %s", ErrUnsupported, showValue(a[1]))
		}
		e := r.entries[r.next]
		v, err := vm.newStruct(d, []any{vm.stringPtrOf(e.name), e.typ})
		if err != nil {
			return nil, err
		}
		if err := vm.ptrStore("uv_fs_scandir_next", ent, v, 1); err != nil {
			return nil, err
		}
		r.next++
		return int32(0), nil
	}
	// uv_fs_req_cleanup(req) releases what the request holds
	vm.shims["uv_fs_req_cleanup"] = func(vm *VM, a []any) (any, error) {
		r, err := vm.req("uv_fs_req_cleanup", a[0])
		if err != nil {
			return nil, err
		}
		*r = uvReq{}
		return nil, nil
	}
}
