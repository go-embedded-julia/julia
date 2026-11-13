# Programs for the Base probe (TestBaseProgress): tasks, the scheduler and the
# event loop through julia's own Base, answered once by julia 1.12.6
# (tasks_base.golden) and run after RunModuleInitializers returns. Each is one
# expression that defines no global, so that they can share a module.
#---
let t = Task(() -> 42)
    schedule(t)
    wait(t)
    (istaskstarted(t), istaskdone(t), istaskfailed(t), fetch(t))
end
#---
fetch(@async 1 + 1)
#---
let log = []
    t = @async (push!(log, :a); yield(); push!(log, :c))
    yield()
    push!(log, :b)
    wait(t)
    log
end
#---
let c = Condition(), r = []
    t = @async (wait(c); push!(r, :woke))
    yield()
    notify(c)
    wait(t)
    r
end
#---
let t = @async error("boom")
    r = try
        wait(t)
    catch e
        (e isa TaskFailedException, e.task === t, e.task.result)
    end
    (r, istaskfailed(t), istaskdone(t))
end
#---
let ch = Channel(2)
    @async (for i in 1:5; put!(ch, i); end; close(ch))
    collect(ch)
end
#---
let t = Timer(0.01)
    wait(t)
    isopen(t)
end
#---
(sleep(0.01); :slept)
#---
let n = Ref(0), t = Timer(_ -> n[] += 1, 0.01, interval = 0.01)
    sleep(0.2)
    close(t)
    n[] > 2
end
#---
fetch(Threads.@spawn 1 + 1)
#---
let l = ReentrantLock()
    lock(l)
    t = @async (lock(l); :got)
    yield()
    unlock(l)
    fetch(t)
end
#---
let ScopedValues = Base.ScopedValues, sv = ScopedValues.ScopedValue(1)
    ScopedValues.with(sv => 2) do
        (sv[], fetch(@async sv[]))
    end
end
#---
let t = @async current_task()
    fetch(t) === t
end
#---
let c = Channel{Int}(1)
    t = @async take!(c)
    yield()
    put!(c, 7)
    fetch(t)
end
#---
let ev = Base.Event()
    t = @async (wait(ev); :done)
    yield()
    notify(ev)
    fetch(t)
end
#---
let ts = [@async i^2 for i in 1:4]
    map(fetch, ts)
end
