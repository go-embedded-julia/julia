//go:build !unix

package interp

import "os"

// peakRSS is not known here.
func peakRSS(*os.ProcessState) int64 { return -1 }
