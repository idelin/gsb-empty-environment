# gcstore

Segment-based block store in Go with snapshot-rooted, copying garbage
collection.

## Model

- Blocks are stored in append-only segment files under `segments/` and
  may link to other blocks, forming a graph.
- Named snapshots pin root block IDs (`Snapshot`/`DropSnapshot`).
- The block→segment mapping and snapshot roots live in `manifest.json`,
  written atomically (temp file + rename + directory fsync).

## GC ordering

`Store.GC` runs in strictly ordered phases:

1. **Mark** — BFS from every snapshot root over block links.
2. **Copy** — live blocks are copied into a fresh `gc-*` segment; each
   block ID is copied exactly once even if reachable via many paths.
3. **Cutpoint** — the new segment is fsynced, renamed to `seg-*`, and
   the new mapping is atomically persisted. Old segments may only be
   deleted after this point.
4. **Sweep** — unreferenced old segments are deleted.

If copying would exceed `MaxBytes`, GC aborts before the cutpoint:
the partial segment is removed, old segments and the old mapping stay
intact, and `ErrNoSpace` is returned. A crash before the cutpoint leaves
a `gc-*` file that the next `Open` removes; a crash after the cutpoint
is already consistent because the durable mapping points at the new
segment.

## Tests

`go test ./...` covers reachability marking, copy dedup, old-snapshot
reads after GC (including across reopen), manifest/mapping recovery,
cutpoint ordering, crash recovery on both sides of the cutpoint, and
out-of-space abort.
