//go:build js || wasip1

package interp

import "runtime"

// A WebAssembly Go program runs on one thread and the runtime cannot preempt a
// loop that makes no call, so a cell that spins would starve the goroutine
// that watches its context and the interrupt would never be raised: the
// interpreter yields now and then (yieldTick).
func init() { yielder = runtime.Gosched }
