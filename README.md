# evlib — snapshot-dispatched event listeners (C++17)

Header-only (`include/event.hpp`), threads via `Threads::Threads`.

## Semantics

- `event<Args...>::emit(args...)` dispatches on a **snapshot** of the listener
  collection taken at the start of the call. No lock is held while listeners
  run, so callbacks may subscribe / unsubscribe / emit re-entrantly.
- After `subscription::unsubscribe()` returns, the listener is **never entered
  by a new emit round** (and is skipped even in a round that has not reached
  it yet); invocations already in flight run to completion.
- `subscription::wait()` blocks until in-flight invocations of the listener
  finish. Called from within the listener's own callback it does not wait on
  the current thread — self-unsubscription never deadlocks.
- `~subscription()` / `reset()` = `unsubscribe()` + `wait()`, so state
  captured by a listener is never destroyed while the listener is executing.

## Example

```cpp
evlib::event<int> ev;

evlib::subscription sub;
sub = ev.subscribe([&](int v) {
    if (v == 42) { sub.unsubscribe(); sub.wait(); }  // self-removal, no deadlock
});

ev.emit(1);          // snapshot dispatch
sub.unsubscribe();   // returns immediately; no new round will enter it
sub.wait();          // blocks until in-flight calls of this listener finish
```

## Correctness notes

- Removal vs. dispatch use a seq-cst protocol (`removed` store vs. `in_flight`
  increment): whichever comes first in the total order decides whether an
  invocation happens, so `wait()` can never return while an invocation may
  still start.
- `wait()` counts the calling thread's own nested invocations via a
  thread-local stack and excludes them from the wait condition.
- Listener state is kept alive by `shared_ptr` (registry, in-flight snapshots,
  handles), so captures stay valid until the last possible invocation ends.

## Build & test

```sh
cmake -S . -B build && cmake --build build
ctest --test-dir build --output-on-failure
```

Tests (`tests/test_event.cpp`) include a barrier-synchronized check of
destruction timing: while a listener is blocked mid-callback, `wait()` provably
blocks and the listener's captured state is destroyed only after the wait
completes and the last handle reference is released.
