//go:build !darwin

package interp

import (
	"io/fs"
	"os"
)

// hostStat is what package os knows of a file, on a platform whose stat(2)
// the VM does not model: darwin's is vfs_darwin.go.
func hostStat(fi fs.FileInfo) uvStat { return genericStat(fi) }

// hostAccess is access(2) from the file's permission bits: the owner's
// when the process owns it, else the others'.
func hostAccess(name string, mode int32) int32 {
	fi, err := os.Stat(name)
	if err != nil {
		return uvErr(err)
	}
	perm := fi.Mode().Perm()
	for bit, want := range map[int32]fs.FileMode{rOK: 0o444, wOK: 0o222, xOK: 0o111} {
		if mode&bit != 0 && perm&want == 0 {
			return uvEACCES
		}
	}
	return 0
}
