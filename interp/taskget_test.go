package interp

import (
	"errors"
	"strings"
	"testing"
)

// jl_task_get_next refuses, with ErrDeadlock naming the waiting task, a wait
// that nothing can end: julia would sleep forever there.
func TestTaskGetNextDeadlock(t *testing.T) {
	vm := taskVM(t)
	s := vm.NewSession()
	if _, err := s.Eval(taskHeader + `
trypop(q) = nothing
isempty_() = true
get() = ccall(:jl_task_get_next, Ref{Core.Task}, (Any, Any, Any), trypop, nothing, isempty_)
`); err != nil {
		t.Fatal(err)
	}
	_, err := s.Eval(`get()`)
	if !errors.Is(err, ErrDeadlock) || !strings.Contains(err.Error(), "the root task waits forever") {
		t.Errorf("the root task: %v", err)
	}
	// in another task, which the error names by its number and its code
	_, err = s.Eval(`setnext(newtask(get)); doswitch()`)
	if !errors.Is(err, ErrDeadlock) || !strings.Contains(err.Error(), "task #2 (") {
		t.Errorf("a started task: %v", err)
	}
	// a long code is cut
	_, err = s.Eval(`
a_function_with_a_name_much_longer_than_sixty_characters_so_that_it_is_cut_when_shown() = get()
setnext(newtask(a_function_with_a_name_much_longer_than_sixty_characters_so_that_it_is_cut_when_shown)); doswitch()`)
	if !errors.Is(err, ErrDeadlock) || !strings.Contains(err.Error(), "…) waits forever") {
		t.Errorf("a task with a long name: %v", err)
	}
}

// Whatever trypoptask or checkempty raise comes out of jl_task_get_next, and
// a Task that jl_new_task did not make is no task.
func TestTaskGetNextErrors(t *testing.T) {
	vm := taskVM(t)
	s := vm.NewSession()
	if _, err := s.Eval(taskHeader + `
trypop_throws(q) = throw(ErrorException("trypop"))
trypop_none(q) = nothing
empty_throws() = throw(ErrorException("checkempty"))
isempty_() = true
`); err != nil {
		t.Fatal(err)
	}
	forged := &Struct{Type: vm.tTask, Fields: newTaskFields(nil, nil, nil, [5]uint64{})}
	vm.Main.declare("forged", 0).Value = forged
	for _, c := range []struct{ src, want string }{
		{`ccall(:jl_task_get_next, Ref{Core.Task}, (Any, Any, Any), trypop_throws, nothing, isempty_)`, "!ErrorException(\"trypop\")"},
		{`ccall(:jl_task_get_next, Ref{Core.Task}, (Any, Any, Any), trypop_none, nothing, empty_throws)`, "!ErrorException(\"checkempty\")"},
		// a Task without a record is not popped: the queue is asked if it is empty
		{`ccall(:jl_task_get_next, Ref{Core.Task}, (Any, Any, Any), q -> Main.forged, nothing, empty_throws)`, "!ErrorException(\"checkempty\")"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
}
