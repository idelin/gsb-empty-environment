# cycle-gc

Explicit batch cycle collector for strongly-linked node graphs (C++17, header-only).

## Model

- Nodes derive from `gc::Node` and are created via `Collector::create<T>(name)`. The
  collector owns all nodes.
- Graph edges are **strong** references (`gc::Ptr<T>` members). They are never weak
  pointers: reachability from the external root set is what keeps a node alive, and
  unreachable cycles are reclaimed by tracing, not by refcount collapse.
- External roots are `gc::Root<T>` RAII handles; the root set may change at any time.
- `gc::Handle<T>` is a non-owning observer; reading a retired/destroyed node yields
  `nullptr`.

## Phase boundaries

```cpp
auto cycle = c.startCycle();       // begin boundary: pending roots adopted, root set frozen
cycle.retireUnreachable();         // mark from roots, retire unreachable nodes to quarantine
// ... retired nodes exist but are unreadable; new roots may be registered (deferred)
cycle.finish();                    // end boundary: quarantine destroyed, destructors logged
```

`c.collect()` runs a full cycle and returns the number of reclaimed nodes.

Semantics at the boundaries:

- Roots registered while a cycle is open are **deferred** to the next begin boundary;
  they cannot rescue nodes in the in-flight cycle.
- Retired nodes become unreadable immediately (handles/roots return `nullptr`) but are
  destroyed only at the end boundary.
- Nodes created during an open cycle are allocated-live and survive that cycle.
- Every destruction is appended to `Collector::destructorLog()`, which tests use to
  verify the survivor set.

## Build & test

```sh
cmake -B build && cmake --build build && ctest --test-dir build
```

Single-threaded by design; phase boundaries are the synchronization points.
