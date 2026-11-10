package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// IOStream's ios_* calls (src/support/ios.c) for a stream of a file opened
// for reading. Base allocates sizeof(ios_t) bytes (jl_sizeof_ios_t) and hands
// the pointer to ios_file; the C struct's bytes are never read by Julia code,
// so the state lives here, keyed by that memory, and follows ios.c's: a
// buffer of IOS_BUFSIZE bytes refilled from the descriptor, a position in it,
// the end-of-file flag and the descriptor's position. Writing a stream
// (ios_write, ios_putc, ios_trunc, a file opened for writing) is not ported.

const iosBufSize = 32768 // IOS_BUFSIZE (ios.h:23)

// iosState is the part of an ios_t a reading stream uses (ios.h:60-90).
type iosState struct {
	fd      int32 // the descriptor in the VM's table, -1 when closed
	rd, wr  bool  // readable, writable
	buf     []byte
	bpos    int   // the next unread byte of buf
	reading bool  // state == bst_rd
	eof     bool  // _eof
	fpos    int64 // the descriptor's position, -1 when unknown
	maxsize int   // the buffer's capacity
}

// mostOf is MOST_OF (ios.c): what "fits comfortably" in the buffer.
func mostOf(n int) int { return n - n>>4 }

// ios is the state of the ios_t at a pointer, a closed stream for one Base
// allocated and never opened.
func (vm *VM) iosOf(fname string, v any) (*iosState, error) {
	if p, ok := v.(*Pointer); ok {
		if m, isMem := p.Target.(*Memory); isMem && p.Off == 0 {
			if vm.ios == nil {
				vm.ios = map[*Memory]*iosState{}
			}
			s := vm.ios[m]
			if s == nil {
				s = &iosState{fd: -1, fpos: -1}
				vm.ios[m] = s
			}
			return s, nil
		}
	}
	return nil, fmt.Errorf("%w: %s of the stream %s", ErrUnsupported, fname, showValue(v))
}

// readprep is ios_readprep (ios.c:343-373) for a stream that is only read:
// at least n bytes buffered when the file has them. The buffer is never
// grown, as no caller asks for more than it holds (the data moves back when
// n bytes would pass its end).
func (vm *VM) iosReadprep(s *iosState, n int) int {
	space := len(s.buf) - s.bpos
	s.reading = true
	if space >= n || s.fd == -1 {
		return space
	}
	if s.maxsize < s.bpos+n {
		s.buf = append(s.buf[:0], s.buf[s.bpos:]...)
		s.bpos = 0
	}
	s.fpos = -1
	b, code := vm.fsys().read(s.fd, s.maxsize-len(s.buf))
	if code != 0 {
		return space
	}
	s.buf = append(s.buf, b...)
	return len(s.buf) - s.bpos
}

// readAt reads up to n bytes from the descriptor, all of them when all.
func (vm *VM) iosDirect(s *iosState, n int, all bool) []byte {
	var got []byte
	for len(got) < n {
		b, code := vm.fsys().read(s.fd, n-len(got))
		if code != 0 || len(b) == 0 {
			break
		}
		got = append(got, b...)
		if !all {
			break
		}
	}
	return got
}

// iosRead is _ios_read (ios.c:263-331): up to n bytes, all of them (reading
// until the end of the file) when all.
func (vm *VM) iosRead(s *iosState, n int, all bool) []byte {
	var out []byte
	s.reading = true
	didread := false
	for n > 0 {
		avail := len(s.buf) - s.bpos
		if avail > 0 {
			ncopy := min(avail, n)
			out = append(out, s.buf[s.bpos:s.bpos+ncopy]...)
			s.bpos += ncopy
			if ncopy >= n {
				return out
			}
		}
		if s.fd == -1 {
			// can't get any more data
			if avail == 0 {
				s.eof = true
			}
			return out
		}
		n -= avail
		if didread && !all {
			return out
		}
		s.buf, s.bpos, s.fpos = s.buf[:0], 0, -1
		if n > mostOf(s.maxsize) {
			// doesn't fit comfortably in buffer; go direct
			got := vm.iosDirect(s, n, all)
			if len(got) == 0 {
				s.eof = true
			}
			return append(out, got...)
		}
		// refill buffer
		b, code := vm.fsys().read(s.fd, s.maxsize)
		if code != 0 || len(b) == 0 {
			s.eof = true
			return out
		}
		s.buf = append(s.buf[:0], b...)
		didread = true
	}
	return out
}

// seekTo is lseek on the stream's descriptor, errno set on failure.
func (vm *VM) iosLseek(s *iosState, off int64, whence int32) (int64, bool) {
	pos, code := vm.fsys().seek(s.fd, off, whence)
	if code != 0 {
		vm.errno = -code
		return -1, false
	}
	return pos, true
}

func (vm *VM) bootIOS() {
	voidPtr := func(vm *VM, v any) *Pointer {
		p, _ := v.(*Pointer)
		return &Pointer{Type: vm.ptrType(core.NothingType), Target: p.Target, Off: p.Off}
	}
	// ios_file(s, fname, rd, wr, create, trunc) (ios.c:948-989): s, or NULL
	// with errno set. Opening for writing is refused in the julia tree
	// (EROFS) and not ported on the host.
	vm.shims["ios_file"] = func(vm *VM, a []any) (any, error) {
		s, err := vm.iosOf("ios_file", a[0])
		if err != nil {
			return nil, err
		}
		name, err := vm.cpath("ios_file", a[1])
		if err != nil {
			return nil, err
		}
		var f [4]int64
		for i := range f {
			var ok bool
			if f[i], ok = intArg(a[2+i]); !ok {
				return nil, fmt.Errorf("%w: ios_file of %s", ErrUnsupported, showValue(a[2+i]))
			}
		}
		rd, wr, create, trunc := f[0] != 0, f[1] != 0, f[2] != 0, f[3] != 0
		s.fd = -1
		if !(rd || wr) {
			vm.errno = -uvEINVAL
			return &Pointer{Type: vm.ptrType(core.NothingType)}, nil
		}
		flags := int32(0)
		if wr {
			flags = 1 // O_WRONLY
			if rd {
				flags = 2 // O_RDWR
			}
		}
		if create {
			flags |= 0x200 // O_CREAT
		}
		if trunc {
			flags |= 0x400 // O_TRUNC
		}
		fd, code := vm.fsys().open(name, flags)
		if code == uvENOSYS {
			return nil, fmt.Errorf("%w: ios_file of %q for writing on the host", ErrUnsupported, name)
		}
		if code != 0 {
			vm.errno = -code
			return &Pointer{Type: vm.ptrType(core.NothingType)}, nil
		}
		*s = iosState{fd: fd, rd: rd, wr: wr, fpos: 0, maxsize: iosBufSize}
		return voidPtr(vm, a[0]), nil
	}
	// ios_close(s) (ios.c:685-700): the descriptor closed, the buffer gone
	vm.shims["ios_close"] = func(vm *VM, a []any) (any, error) {
		s, err := vm.iosOf("ios_close", a[0])
		if err != nil {
			return nil, err
		}
		rc := int32(0)
		if s.fd != -1 {
			rc = cbool(vm.fsys().close(s.fd) != 0)
		}
		s.fd, s.buf, s.bpos, s.maxsize = -1, nil, 0, 0
		return rc, nil
	}
	// the stream's flags and descriptor
	for name, get := range map[expr.Symbol]func(*iosState) any{
		"ios_isopen":       func(s *iosState) any { return cbool(s.fd != -1) },
		"ios_get_readable": func(s *iosState) any { return cbool(s.rd) },
		"ios_get_writable": func(s *iosState) any { return cbool(s.wr) },
		// jl_nb_available (sys.c:98-101): size - bpos
		"jl_nb_available": func(s *iosState) any { return int32(len(s.buf) - s.bpos) },
		// jl_ios_fd (sys.c:96): the descriptor in the VM's table
		"jl_ios_fd": func(s *iosState) any { return int64(s.fd) },
	} {
		vm.shims[name] = func(vm *VM, a []any) (any, error) {
			s, err := vm.iosOf(string(name), a[0])
			if err != nil {
				return nil, err
			}
			return get(s), nil
		}
	}
	// ios_read and ios_readall (ios.c:333-341): the bytes written to the
	// buffer, which must hold them
	for name, all := range map[expr.Symbol]bool{"ios_read": false, "ios_readall": true} {
		vm.shims[name] = func(vm *VM, a []any) (any, error) {
			s, err := vm.iosOf(string(name), a[0])
			if err != nil {
				return nil, err
			}
			n, ok := intArg(a[2])
			room, ok2 := vm.room(a[1])
			if !ok || !ok2 || n > room {
				return nil, fmt.Errorf("%w: %s of %s bytes into %s", ErrUnsupported, name, showValue(a[2]), showValue(a[1]))
			}
			b := vm.iosRead(s, int(n), all)
			return uint64(len(b)), vm.putBytes(a[1], b)
		}
	}
	// ios_fillbuf(s) (ios.c:377-384): the bytes available when the whole file
	// is buffered, else -1
	vm.shims["ios_fillbuf"] = func(vm *VM, a []any) (any, error) {
		s, err := vm.iosOf("ios_fillbuf", a[0])
		if err != nil {
			return nil, err
		}
		nb := s.maxsize - s.bpos
		if got := vm.iosReadprep(s, nb); got < nb {
			return int64(got), nil
		}
		return int64(-1), nil
	}
	// ios_eof_blocking(s) (ios.c:626-638)
	vm.shims["ios_eof_blocking"] = func(vm *VM, a []any) (any, error) {
		s, err := vm.iosOf("ios_eof_blocking", a[0])
		if err != nil {
			return nil, err
		}
		switch {
		case s.reading && s.bpos < len(s.buf):
			return int32(0), nil
		case s.fd == -1 || vm.iosReadprep(s, 1) < 1:
			return int32(1), nil
		}
		return int32(0), nil
	}
	// ios_getc and ios_peekc (ios.c:1095-1118): a byte, or IOS_EOF (-1)
	vm.shims["ios_getc"] = func(vm *VM, a []any) (any, error) {
		s, err := vm.iosOf("ios_getc", a[0])
		if err != nil {
			return nil, err
		}
		if s.reading && s.bpos < len(s.buf) {
			s.bpos++
			return int32(s.buf[s.bpos-1]), nil
		}
		if s.eof {
			return int32(-1), nil
		}
		if b := vm.iosRead(s, 1, false); len(b) == 1 {
			return int32(b[0]), nil
		}
		return int32(-1), nil
	}
	vm.shims["ios_peekc"] = func(vm *VM, a []any) (any, error) {
		s, err := vm.iosOf("ios_peekc", a[0])
		if err != nil {
			return nil, err
		}
		if s.bpos >= len(s.buf) {
			if s.eof || vm.iosReadprep(s, 1) == 0 {
				return int32(-1), nil
			}
		}
		return int32(s.buf[s.bpos]), nil
	}
	// ios_seek(s, pos) and ios_seek_end(s) (ios.c:457-491): 0, or -1 with
	// errno set
	vm.shims["ios_seek"] = func(vm *VM, a []any) (any, error) {
		s, err := vm.iosOf("ios_seek", a[0])
		if err != nil {
			return nil, err
		}
		pos, ok := intArg(a[1])
		if !ok {
			return nil, fmt.Errorf("%w: ios_seek of %s", ErrUnsupported, showValue(a[1]))
		}
		s.eof = false
		fdpos, ok := vm.iosLseek(s, pos, 0)
		if !ok {
			return int64(-1), nil
		}
		s.fpos, s.buf, s.bpos = fdpos, s.buf[:0], 0
		return int64(0), nil
	}
	vm.shims["ios_seek_end"] = func(vm *VM, a []any) (any, error) {
		s, err := vm.iosOf("ios_seek_end", a[0])
		if err != nil {
			return nil, err
		}
		s.eof = true
		fdpos, ok := vm.iosLseek(s, 0, 2)
		if !ok {
			return int64(-1), nil
		}
		s.fpos, s.buf, s.bpos = fdpos, s.buf[:0], 0
		return int64(0), nil
	}
	// ios_skip(s, offs) (ios.c:496-532)
	vm.shims["ios_skip"] = func(vm *VM, a []any) (any, error) {
		s, err := vm.iosOf("ios_skip", a[0])
		if err != nil {
			return nil, err
		}
		offs, ok := intArg(a[1])
		if !ok {
			return nil, fmt.Errorf("%w: ios_skip of %s", ErrUnsupported, showValue(a[1]))
		}
		if offs == 0 {
			return int64(0), nil
		}
		if offs > 0 && offs <= int64(len(s.buf)-s.bpos) {
			s.bpos += int(offs)
			return int64(0), nil
		}
		if offs < 0 && -offs <= int64(s.bpos) {
			s.bpos += int(offs)
			s.eof = false
			return int64(0), nil
		}
		if s.reading {
			offs -= int64(len(s.buf) - s.bpos)
		}
		fdpos, ok := vm.iosLseek(s, offs, 1)
		if !ok {
			return int64(-1), nil
		}
		s.fpos, s.buf, s.bpos, s.eof = fdpos, s.buf[:0], 0, false
		return int64(0), nil
	}
	// ios_pos(s) (ios.c:534-552): where the next read starts
	vm.shims["ios_pos"] = func(vm *VM, a []any) (any, error) {
		s, err := vm.iosOf("ios_pos", a[0])
		if err != nil {
			return nil, err
		}
		fdpos := s.fpos
		if fdpos == -1 {
			var ok bool
			if fdpos, ok = vm.iosLseek(s, 0, 1); !ok {
				return int64(-1), nil
			}
			s.fpos = fdpos
		}
		if s.reading {
			fdpos -= int64(len(s.buf) - s.bpos)
		}
		return fdpos, nil
	}
	// ios_filesize(s) (ios.c:554-566)
	vm.shims["ios_filesize"] = func(vm *VM, a []any) (any, error) {
		s, err := vm.iosOf("ios_filesize", a[0])
		if err != nil {
			return nil, err
		}
		fdpos := s.fpos
		if fdpos == -1 {
			var ok bool
			if fdpos, ok = vm.iosLseek(s, 0, 1); !ok {
				return int64(-1), nil
			}
			s.fpos = fdpos
		}
		sz, _ := vm.iosLseek(s, 0, 2)
		vm.iosLseek(s, fdpos, 0)
		return sz, nil
	}
}
