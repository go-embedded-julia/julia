//go:build !plan9

package interp

import "syscall"

// The errors the VFS returns itself, as the host's errno values, so that a
// host error and one of the tree's map to the same libuv code.
var (
	errBADF   error = syscall.EBADF
	errSPIPE  error = syscall.ESPIPE
	errISDIR  error = syscall.EISDIR
	errNOTDIR error = syscall.ENOTDIR
)

// hostErrnos are the host's errno values and their libuv codes (uvErr).
var hostErrnos = []errnoCode{
	{syscall.ENOENT, uvENOENT}, {syscall.EACCES, uvEACCES}, {syscall.EPERM, uvEPERM},
	{syscall.EEXIST, uvEEXIST}, {syscall.ENOTDIR, uvENOTDIR}, {syscall.EISDIR, uvEISDIR},
	{syscall.EINVAL, uvEINVAL}, {syscall.EMFILE, uvEMFILE}, {syscall.EROFS, uvEROFS},
	{syscall.ESPIPE, uvESPIPE}, {syscall.ENAMETOOLONG, uvENAMETOOLONG},
	{syscall.ELOOP, uvELOOP}, {syscall.ENOTEMPTY, uvENOTEMPTY}, {syscall.EBADF, uvEBADF},
}
