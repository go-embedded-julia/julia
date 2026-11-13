# Programs for TestTasksAgainstJulia: each runs after the header of task_test.go
# (Log, note, newtask, setnext, doswitch, run, ...), uses Core and ccall only,
# and ends with its answer. Julia 1.12.6 answers them once (tasks.golden).
#---
# a task starts on its first switch, and a switch back and forth keeps its place
t1 = newtask(() -> begin
    note(:t1_start)
    setnext(root); doswitch()
    note(:t1_resumed)
    setnext(root); doswitch()
end)
note(started(t1))
setnext(t1); doswitch()
note(:back)
note((started(t1), tid(t1)))
setnext(t1); doswitch()
note(:back2)
getfield(LOG, :v)
#---
# a finished task is done, with its value as its result
t = newtask(() -> (note(:body); 42))
note(started(t))
run(t)
note((started(t), state(t), result(t), getfield(t, :_isexception)))
getfield(LOG, :v)
#---
# an exception in the body fails the task, and is its result
t = newtask(() -> throw(ErrorException("boom")))
run(t)
(state(t), result(t), getfield(t, :_isexception))
#---
# an exception that was caught and thrown again leaves the same way
t = newtask(() -> try
    throw(ErrorException("a"))
catch e
    note(:caught)
    ccall(:jl_rethrow, Cvoid, ())
end)
run(t)
(state(t), result(t), getfield(LOG, :v))
#---
# throwto: an exception put in a task before it starts is its result, and the
# body never runs
t = newtask(() -> (note(:never); 1))
setfield!(t, :result, ErrorException("x"))
setfield!(t, :_isexception, true)
run(t)
(started(t), state(t), result(t), getfield(LOG, :v))
#---
# get_next_task is the current task until set_next_task says otherwise
t = newtask(() -> 1)
a = getnext() === root
setnext(t)
b = getnext() === t
setnext(root)
(a, b, getnext() === root)
#---
# a switch to the current task returns at once
setnext(root); doswitch()
note(:still_here)
getfield(LOG, :v)
#---
# a task that finished cannot be switched to again
t = newtask(() -> 1)
run(t)
r = try
    setnext(t); doswitch()
    :switched
catch e
    e
end
(r, state(t))
#---
# a task started by a task runs, and the tasks hand over in turn: when the
# inner one ends the outer one, queued before it switched, runs on
inner = newtask(() -> (note(:inner); 2))
outer = newtask(() -> begin
    note(:outer_start)
    Base.schedule(cur())
    setnext(inner); doswitch()
    note(:outer_end)
    Base.schedule(root)
    3
end)
setnext(outer); doswitch()
(getfield(LOG, :v), result(outer), result(inner), state(outer), state(inner))
#---
# a new task has the state of julia's jl_new_task
t = newtask(() -> 1)
(state(t), getfield(t, :sticky), getfield(t, :priority), getfield(t, :_isexception), getfield(t, :result), getfield(t, :next), getfield(t, :queue), getfield(t, :storage), getfield(t, :donenotify), started(t), tid(t))
#---
# a task that is not sticky is free of its thread once it stops running, and
# the thread of the running task is its tid
t = newtask(() -> (note(ccall(:jl_threadid, Int16, ())); setnext(root); doswitch()))
setfield!(t, :sticky, false)
setnext(t); doswitch()
(ccall(:jl_threadid, Int16, ()), tid(t), getfield(LOG, :v))
#---
# jl_set_task_tid claims a task for a thread once, and answers again for the same thread
t = newtask(() -> 1)
a = ccall(:jl_set_task_tid, Int32, (Any, Int32), t, i32(1))
b = ccall(:jl_set_task_tid, Int32, (Any, Int32), t, i32(0))
(a, b, tid(t))
#---
# a task claimed by another thread cannot be switched to from this one
t = newtask(() -> 1)
ccall(:jl_set_task_tid, Int32, (Any, Int32), t, i32(1))
r = try
    setnext(t); doswitch()
    :switched
catch e
    e
end
(r, started(t), tid(t))
#---
# a task's random state comes from its creator's, and the creator's advances
r0 = (getfield(root, :rngState4), getfield(root, :rngState0))
t = newtask(() -> 1)
r1 = (getfield(root, :rngState4), getfield(root, :rngState0))
(r0 === r1, getfield(t, :rngState4) === getfield(root, :rngState4), getfield(t, :rngState0) === getfield(root, :rngState0))
