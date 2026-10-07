# gcstore

Content-addressed block store in Go with snapshot-based garbage collection.

## Design

- Blocks (`Block{Refs, Data}`) are addressed by SHA-256 and appended to
  segment files (`seg-NNNNNN.seg`). A hash→location mapping and the
  snapshot table are persisted atomically (write temp, fsync, rename).
- Snapshots pin root hashes; blocks form a graph through `Refs`.
- `Put` never deduplicates: identical content may occupy several
  physical copies. GC collapses them.

## Garbage collection

GC runs in four explicit phases (`GC()` runs them in sequence):

1. **Mark** (`GCMark`) — reachability traversal from the union of all
   snapshot roots; every sealed segment becomes a candidate.
2. **Copy** (`GCCopy`) — live blocks in candidate segments are copied
   into one new segment, each hash copied at most once (duplicates keep
   a single copy). Aborts with `ErrNoSpace` if the space budget would
   be exceeded, removing partial output and leaving the store untouched.
3. **Persist** (`GCPersist`) — the cut point: relocated locations are
   applied, dead entries in candidate segments are dropped, and the new
   mapping is atomically persisted.
4. **Sweep** (`GCSweep`) — candidate segments are deleted. A segment is
   only ever deleted after the cut point has succeeded.

## Crash recovery

`Open` removes segment files not referenced by the persisted mapping
and stale temp files:

- Crash before the cut point → old mapping and old segments intact,
  orphan GC output removed; GC can be rerun.
- Crash after the cut point → new mapping recovered, unreferenced old
  segments dropped, completing the sweep.

See `gc_test.go` for tests covering multi-root reachability, old
snapshot reads during/after GC, mapping recovery on both sides of the
cut point, and out-of-space abort.
