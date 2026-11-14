//go:build plan9

package interp

import "errors"

// Plan 9 has no errno: the errors the VFS returns itself are its own, with
// the libuv codes POSIX hosts give them.
var (
	errBADF   = errors.New("bad file descriptor")
	errSPIPE  = errors.New("illegal seek")
	errISDIR  = errors.New("is a directory")
	errNOTDIR = errors.New("not a directory")
)

var hostErrnos = []errnoCode{
	{errBADF, uvEBADF}, {errSPIPE, uvESPIPE}, {errISDIR, uvEISDIR}, {errNOTDIR, uvENOTDIR},
}
