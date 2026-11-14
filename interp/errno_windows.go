//go:build windows

package interp

import "syscall"

// The errors the VFS returns itself, as the errno values package syscall
// invents for Windows; hostErrnos maps them like the system's own.
var (
	errBADF   error = syscall.EBADF
	errSPIPE  error = syscall.ESPIPE
	errISDIR  error = syscall.EISDIR
	errNOTDIR error = syscall.ENOTDIR
)

// The Win32 error codes libuv translates (uv_translate_sys_error, libuv
// src/win/error.c). Package syscall's own ENOENT is not ERROR_FILE_NOT_FOUND,
// so a Win32 error is named by its number.
const (
	errorFileNotFound     syscall.Errno = 2
	errorPathNotFound     syscall.Errno = 3
	errorTooManyOpenFiles syscall.Errno = 4
	errorAccessDenied     syscall.Errno = 5
	errorInvalidHandle    syscall.Errno = 6
	errorWriteProtect     syscall.Errno = 19
	errorFileExists       syscall.Errno = 80
	errorInvalidParameter syscall.Errno = 87
	errorInvalidName      syscall.Errno = 123
	errorNegativeSeek     syscall.Errno = 131
	errorDirNotEmpty      syscall.Errno = 145
	errorBadPathname      syscall.Errno = 161
	errorAlreadyExists    syscall.Errno = 183
	errorFilenameExcedRng syscall.Errno = 206
	errorDirectory        syscall.Errno = 267
	errorCantResolveFile  syscall.Errno = 1921
)

// hostErrnos are the Win32 errors and the libuv codes libuv gives them, then
// the errno values the VFS returns itself (uvErr). They come before the
// portable errors: libuv answers UV_EPERM for ERROR_ACCESS_DENIED, where
// fs.ErrPermission would say EACCES.
var hostErrnos = []errnoCode{
	{errorFileNotFound, uvENOENT}, {errorPathNotFound, uvENOENT}, {errorBadPathname, uvENOENT},
	{errorDirectory, uvENOENT}, {errorInvalidName, uvENOENT},
	{errorAccessDenied, uvEPERM}, {errorWriteProtect, uvEPERM},
	{errorFileExists, uvEEXIST}, {errorAlreadyExists, uvEEXIST},
	{errorInvalidParameter, uvEINVAL}, {errorNegativeSeek, uvEINVAL},
	{errorTooManyOpenFiles, uvEMFILE}, {errorInvalidHandle, uvEBADF},
	{errorFilenameExcedRng, uvENAMETOOLONG}, {errorDirNotEmpty, uvENOTEMPTY},
	{errorCantResolveFile, uvELOOP},
	{syscall.ENOENT, uvENOENT}, {syscall.EACCES, uvEACCES}, {syscall.EPERM, uvEPERM},
	{syscall.EEXIST, uvEEXIST}, {syscall.ENOTDIR, uvENOTDIR}, {syscall.EISDIR, uvEISDIR},
	{syscall.EINVAL, uvEINVAL}, {syscall.EMFILE, uvEMFILE}, {syscall.EROFS, uvEROFS},
	{syscall.ESPIPE, uvESPIPE}, {syscall.ENAMETOOLONG, uvENAMETOOLONG},
	{syscall.ELOOP, uvELOOP}, {syscall.ENOTEMPTY, uvENOTEMPTY}, {syscall.EBADF, uvEBADF},
}
