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
#---
# jl_task_get_next asks trypoptask for a task, asks checkempty when none comes,
# asks again while the queue does not look empty, and makes the task its own
mutable struct Q; t::Any; n::Int; end
q = Q(nothing, 0)
t = newtask(() -> 1)
trypop(q) = (setfield!(q, :n, Core.Intrinsics.add_int(getfield(q, :n), 1)); getfield(q, :n) === 2 ? t : nothing)
notempty() = false
got = ccall(:jl_task_get_next, Ref{Core.Task}, (Any, Any, Any), trypop, q, notempty)
(got === t, getfield(q, :n), tid(t))
#---
# a timer: its callback runs in the loop that jl_task_get_next runs while no task
# is runnable, a one-shot timer stops, and the loop goes on until a task is
mutable struct Flag; v::Bool; end
const FLAG = Flag(false)
t = newtask(() -> 1)
function tick(h::Ptr{Cvoid})
    note(:tick)
    setfield!(FLAG, :v, true)
    nothing
end
h = ccall(:malloc, Ptr{Cvoid}, (UInt,), u64(512))
ccall(:jl_uv_disassociate_julia_struct, Cvoid, (Ptr{Cvoid},), h)
loop = ccall(:jl_global_event_loop, Ptr{Cvoid}, ())
a = ccall(:uv_timer_init, Int32, (Ptr{Cvoid}, Ptr{Cvoid}), loop, h)
ccall(:uv_update_time, Cvoid, (Ptr{Cvoid},), loop)
b = ccall(:uv_timer_start, Int32, (Ptr{Cvoid}, Ptr{Cvoid}, UInt64, UInt64), h, Base.@cfunction(tick, Cvoid, (Ptr{Cvoid},)), u64(5), u64(0))
c = ccall(:uv_is_active, Int32, (Ptr{Cvoid},), h)
trypop(q) = getfield(FLAG, :v) ? t : nothing
got = ccall(:jl_task_get_next, Ref{Core.Task}, (Any, Any, Any), trypop, nothing, () -> true)
(a, b, c, got === t, getfield(LOG, :v), ccall(:uv_is_active, Int32, (Ptr{Cvoid},), h), ccall(:uv_timer_get_repeat, UInt64, (Ptr{Cvoid},), h))
#---
# a repeating timer runs until its callback stops it
mutable struct Count; n::Int; end
const COUNT = Count(0)
t = newtask(() -> 1)
function tick(h::Ptr{Cvoid})
    setfield!(COUNT, :n, Core.Intrinsics.add_int(getfield(COUNT, :n), 1))
    note(getfield(COUNT, :n))
    getfield(COUNT, :n) === 3 && ccall(:uv_timer_stop, Int32, (Ptr{Cvoid},), h)
    nothing
end
h = ccall(:malloc, Ptr{Cvoid}, (UInt,), u64(512))
ccall(:jl_uv_disassociate_julia_struct, Cvoid, (Ptr{Cvoid},), h)
loop = ccall(:jl_global_event_loop, Ptr{Cvoid}, ())
ccall(:uv_timer_init, Int32, (Ptr{Cvoid}, Ptr{Cvoid}), loop, h)
ccall(:uv_timer_start, Int32, (Ptr{Cvoid}, Ptr{Cvoid}, UInt64, UInt64), h, Base.@cfunction(tick, Cvoid, (Ptr{Cvoid},)), u64(3), u64(3))
r = ccall(:uv_timer_get_repeat, UInt64, (Ptr{Cvoid},), h)
trypop(q) = getfield(COUNT, :n) === 3 ? t : nothing
got = ccall(:jl_task_get_next, Ref{Core.Task}, (Any, Any, Any), trypop, nothing, () -> true)
(r, got === t, getfield(LOG, :v), ccall(:uv_is_active, Int32, (Ptr{Cvoid},), h))
#---
# an async handle: a send runs its callback in the next pass of the loop
t = newtask(() -> 1)
function woke(h::Ptr{Cvoid})
    note(:async)
    nothing
end
h = ccall(:malloc, Ptr{Cvoid}, (UInt,), u64(512))
ccall(:jl_uv_disassociate_julia_struct, Cvoid, (Ptr{Cvoid},), h)
loop = ccall(:jl_global_event_loop, Ptr{Cvoid}, ())
a = ccall(:uv_async_init, Int32, (Ptr{Cvoid}, Ptr{Cvoid}, Ptr{Cvoid}), loop, h, Base.@cfunction(woke, Cvoid, (Ptr{Cvoid},)))
s = ccall(:uv_async_send, Int32, (Ptr{Cvoid},), h)
trypop(q) = getfield(LOG, :v) === () ? nothing : t
got = ccall(:jl_task_get_next, Ref{Core.Task}, (Any, Any, Any), trypop, nothing, () -> true)
(a, s, got === t, getfield(LOG, :v), ccall(:uv_is_active, Int32, (Ptr{Cvoid},), h))
#---
# jl_process_events runs what is due without waiting, and answers 1
mutable struct Count; n::Int; end
const COUNT = Count(0)
function tick(h::Ptr{Cvoid})
    setfield!(COUNT, :n, Core.Intrinsics.add_int(getfield(COUNT, :n), 1))
    nothing
end
h = ccall(:malloc, Ptr{Cvoid}, (UInt,), u64(512))
ccall(:jl_uv_disassociate_julia_struct, Cvoid, (Ptr{Cvoid},), h)
loop = ccall(:jl_global_event_loop, Ptr{Cvoid}, ())
ccall(:uv_timer_init, Int32, (Ptr{Cvoid}, Ptr{Cvoid}), loop, h)
ccall(:uv_timer_start, Int32, (Ptr{Cvoid}, Ptr{Cvoid}, UInt64, UInt64), h, Base.@cfunction(tick, Cvoid, (Ptr{Cvoid},)), u64(0), u64(0))
a = ccall(:jl_process_events, Int32, ())
(a, getfield(COUNT, :n), ccall(:jl_process_events, Int32, ()), getfield(COUNT, :n))
#---
# a handle that is closed stops, and nothing runs for it
mutable struct Count; n::Int; end
const COUNT = Count(0)
function tick(h::Ptr{Cvoid})
    setfield!(COUNT, :n, Core.Intrinsics.add_int(getfield(COUNT, :n), 1))
    nothing
end
h = ccall(:malloc, Ptr{Cvoid}, (UInt,), u64(512))
ccall(:jl_uv_disassociate_julia_struct, Cvoid, (Ptr{Cvoid},), h)
loop = ccall(:jl_global_event_loop, Ptr{Cvoid}, ())
ccall(:uv_timer_init, Int32, (Ptr{Cvoid}, Ptr{Cvoid}), loop, h)
ccall(:uv_timer_start, Int32, (Ptr{Cvoid}, Ptr{Cvoid}, UInt64, UInt64), h, Base.@cfunction(tick, Cvoid, (Ptr{Cvoid},)), u64(1), u64(0))
ccall(:jl_close_uv, Cvoid, (Ptr{Cvoid},), h)
ccall(:jl_process_events, Int32, ())
ccall(:jl_process_events, Int32, ())
getfield(COUNT, :n)
