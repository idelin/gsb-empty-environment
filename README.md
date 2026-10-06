# pageindex

Immutable, copy-on-write B-tree index over explicitly numbered pages, for
workloads where long-running analysis reads old versions while a writer
continuously replaces tree nodes.

## Design

- **Explicit page numbers** — every tree node lives in exactly one page
  (`PageID`). The allocator hands out fresh numbers or recycles numbers
  from a free list.
- **Immutability / CoW** — a published node is never mutated. `Put` and
  `Delete` copy every node on the affected path into newly allocated
  pages and atomically swap the root. Old roots stay fully intact.
- **Snapshots pin a root version** — `Store.Snapshot()` captures the
  current root page and version. Its pages remain allocated and
  unmodified until `Close`, regardless of writer activity.
- **Idempotent, concurrency-safe Close** — `Snapshot.Close` may be called
  any number of times from any number of goroutines; only the first call
  takes effect.
- **Reachability-gated reclamation** — the GC recycles a page only when
  it is unreachable from the current root *and* from every active
  snapshot. Recycled pages go to the free list, so reallocation can never
  pollute an old reader.
- **Reference-graph checker** — `Store.CheckRefGraph()` verifies over
  explicit page numbers: no leaks (allocated ⇒ reachable), no dangling
  references (reachable ⇒ allocated), and a clean free list (no
  duplicates, nothing reachable or allocated).

## Verification

`pageindex/index_test.go` covers:

- forked snapshots reading divergent versions, with page-level assertions
  that closing one snapshot recycles exactly the pages no remaining root
  can reach, and that later reallocation does not disturb survivors;
- deletes that hide keys from the current version while a pinned snapshot
  still sees them;
- repeated and concurrent `Close` under a racing writer and readers
  (run with `-race`);
- a randomized model-based test cross-checking every live snapshot, the
  B-tree structural invariants and the reference graph.

## Run

```sh
GO111MODULE=on go test -race ./pageindex/
```
