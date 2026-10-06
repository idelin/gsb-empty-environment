# patchmerge

Go library for merging two sets of text patches that are all expressed in
**baseline byte coordinates**.

## Merge rules

- Identical inserts (same position and text) are deduplicated, keeping the
  lexicographically smaller source id.
- Different inserts at the same point are both kept and applied in source-id
  order, independent of argument order.
- A delete conflicts with any insert from the *other* set whose position is
  strictly inside the deleted range; the conflicted patches are withheld
  from the merge and reported as a conflict range.
- Inserts at either endpoint of a delete do **not** conflict.
- Overlapping deletes are compatible; identical deletes are deduplicated.

## API

- `Merge(a, b PatchSet) (Result, error)` — returns `Result.Merged`
  (mergeable patches, deterministically sorted) and `Result.Conflicts`
  (conflicted baseline byte ranges with the involved patches and sources).
- `Apply(baseline, merged)` — applies mergeable patches to the baseline
  document.
- `MapOffset(merged, off)` — converts a baseline byte offset to the merged
  document coordinate.

## Verification

`merge_test.go` builds a byte-by-byte source model of the document (every
output byte tagged with its baseline origin) and asserts, for every
surviving baseline byte, that `MapOffset` agrees with the model position —
including a 500-trial randomized test over generated patch sets.

Run: `go test ./...`
