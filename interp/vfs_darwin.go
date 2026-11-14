package interp

import (
	"io/fs"
	"syscall"
)

// hostStat is uv__to_stat (libuv src/unix/fs.c) of a darwin stat(2): the
// fields as the platform reports them.
func hostStat(fi fs.FileInfo) uvStat {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return genericStat(fi)
	}
	return uvStat{
		dev: uint64(int64(st.Dev)), mode: uint64(st.Mode), nlink: uint64(st.Nlink), uid: uint64(st.Uid),
		gid: uint64(st.Gid), rdev: uint64(int64(st.Rdev)), ino: st.Ino, size: uint64(st.Size),
		blksize: uint64(st.Blksize), blocks: uint64(st.Blocks), flags: uint64(st.Flags), gen: uint64(st.Gen),
		atim: [2]int64{st.Atimespec.Sec, st.Atimespec.Nsec}, mtim: [2]int64{st.Mtimespec.Sec, st.Mtimespec.Nsec},
		ctim: [2]int64{st.Ctimespec.Sec, st.Ctimespec.Nsec}, birthtim: [2]int64{st.Birthtimespec.Sec, st.Birthtimespec.Nsec},
	}
}

// hostAccess is access(2).
func hostAccess(name string, mode int32) int32 {
	if err := syscall.Access(name, uint32(mode)); err != nil {
		return uvErr(err)
	}
	return 0
}
