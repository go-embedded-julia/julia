//go:build unix

package interp

import (
	"os"
	"runtime"
	"syscall"
)

// peakRSS is the peak resident set of a finished child process, in bytes, or
// -1 when it is not known.
func peakRSS(ps *os.ProcessState) int64 {
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok {
		return -1
	}
	if runtime.GOOS == "darwin" {
		return int64(ru.Maxrss) // bytes
	}
	return int64(ru.Maxrss) << 10 // kilobytes
}
