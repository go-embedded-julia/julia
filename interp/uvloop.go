package interp

import "errors"

// The libuv loop, in Go (issue #35, step 4). This first version is the loop
// of a program that has made no handle: nothing is active, so uv_run returns
// at once and says so.

// uvRunMode is a uv_run_mode of uv.h.
type uvRunMode int

const (
	uvRunOnce uvRunMode = iota
	uvRunNoWait
)

// errLoopForever is what uv_run(ONCE) answers instead of blocking when no
// timer is armed and nothing is pending: libuv would wait for a signal that
// nothing in the VM can send.
var errLoopForever = errors.New("interp: libuv would wait forever")

// uvRun is uv_run (libuv unix/core.c): whether handles remain that keep the
// loop alive.
func (vm *VM) uvRun(mode uvRunMode) (bool, error) {
	return false, nil
}
