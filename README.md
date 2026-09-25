# json-tree-stream

Streaming path-record format for large JSON-compatible trees, with versioned
commits, subtree moves, prefix queries, inverse patches, disk spill and
crash-safe atomic publication. Pure TypeScript on Node.js (>= 22.6, developed
against 23.9) with zero dependencies; tested with the built-in test runner.

## Record format

A tree is described as a stream of path records, one JSON object per line.
Every node of the tree has exactly one record:

```jsonl
{"p":[],"op":"obj"}
{"p":[["k","0"]],"op":"val","v":"x"}
{"p":[["k","a"]],"op":"val","v":null}
{"p":[["k","arr"]],"op":"arr","n":3}
{"p":[["k","arr"],["i",1]],"op":"val","v":1}
```

Explicit encodings:

- **Segments are tagged**: `["k","0"]` is the object key `"0"`, `["i",0]` is
  array index 0. A numeric object key can never silently become an array
  index, so `{"0":"x"}` never turns into `["x"]`.
- **Empty containers** have their own records: `{"op":"obj"}` / `{"op":"arr","n":0}`.
- **`null`** is a value: `{"op":"val","v":null}` — distinct from absence.
- **Missing / deletion** is a tombstone: `{"op":"del"}` (patches only).
- **Array holes**: `arr` records declare the length `n`; an index `< n` with
  no child record is a hole. Holes survive a round-trip.

## Ingestion, ordering and spill

Records may arrive out of order and in arbitrary chunks. `RecordStore`
buffers them and, once `maxBuffered` is exceeded, sorts and spills runs to
`spillDir`; reading k-way merges the runs and the in-memory buffer as a
stream. Processing therefore never waits for full materialization: the
builder consumes the merged stream record-by-record. Paths are hash-consed
(`PathInterner`), so memory and comparisons stay O(depth) per record instead
of O(depth^2) — a 100k-deep chain builds iteratively without recursion.

## Validation and atomic publish

`buildTree` validates the sorted stream in a single pass before anything is
published:

- **parent/child types**: scalar parents reject children
  (`ParentTypeConflict`); object parents require key segments and array
  parents require in-range index segments (`SegmentTypeMismatch`,
  `IndexOutOfRange`);
- **duplicates**: identical re-deliveries merge, conflicting definitions of
  the same path fail (`DuplicateDefinition`);
- **reference integrity**: every non-root record's parent must be defined
  (`MissingParent`), and exactly one root must exist (`MissingRoot`).

Every `PathError` carries the failing `path` and the record's `source`
(e.g. `run-0003.jsonl:17`, `input#42`), also embedded in the message.

`TreeStore.commit` publishes atomically: in memory by swapping the head
reference after validation; on disk by writing `index-<id>.jsonl` + fsync and
then atomically renaming `HEAD.tmp` to `HEAD`. A crash between the index
write and the HEAD publish leaves an orphan index that recovery removes —
the half-written version is never exposed.

## Versions, moves, cursors, patches

- `store.cursor(prefix, version?)` — prefix query pinned to an immutable
  version; cursors created before a move keep reading the old version.
- `store.move(src, dst)` — subtree move as an incremental diff; failures
  (`NotFound`, `TargetExists`, `MoveIntoSelf`, `ParentTypeConflict`, …)
  leave the published tree untouched.
- `store.applyPatch(records)` — `del` removes subtrees (array deletions
  leave holes); a container record replaced by the same kind of container is
  a header update (e.g. array length) and keeps its children.
- Both return an `inverse` patch that restores the previous tree exactly.

## Layout

- `src/path.ts` — tagged segments, hash-consed paths, ordering, formatting
- `src/record.ts` — record model, JSONL codec, iterative tree to records
- `src/node.ts` — committed tree nodes, iterative record/JSON materialization
- `src/store.ts` — buffering + sorted-run spill + streaming k-way merge
- `src/builder.ts` — single-pass validation and iterative tree construction
- `src/treestore.ts` — versions, atomic commit/recovery, cursors, move, patches

## Tests

```
npm test        # node --test test/*.test.ts
```

Actual run (Node v23.9.0, this workspace):

```
✔ scalar parent followed by a child path is a located conflict
✔ an index segment under an object parent cannot turn it into an array
✔ conflicting duplicates fail; identical duplicates are merged
✔ missing parent breaks reference integrity with a located error
✔ array index beyond the declared length is rejected
✔ arbitrary chunking round-trips node-by-node equivalent trees (4 seeds)
✔ depth 100k is built iteratively without recursion (~4.4s)
✔ {'0':'x'} stays an object and never becomes an array
✔ numeric-like keys, null, empty containers and holes round-trip
✔ wire format encodes every record kind explicitly and losslessly
✔ malformed records are rejected with their origin
✔ memory pressure spills sorted runs to disk and merges them streaming
✔ spilled runs carry origins so errors can be located in files
✔ subtree move keeps old cursors on their version and yields an inverse patch
✔ a failed move does not modify the published tree
✔ move into an array appends via a header update and keeps siblings
✔ del patches create missing keys and array holes; inverse restores them
✔ prefix query returns exactly the subtree records
✔ crash after index write but before HEAD publish recovers the old version
tests 23 / pass 23 / fail 0 (about 5s total)
```
