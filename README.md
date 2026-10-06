# patchmerge

Go library that merges two text patch sets, both expressed in **baseline
byte coordinates**, and verifies offset translation with a per-byte
provenance model.

## Merge rules

- Identical insertions (same offset + text) from both sets are deduplicated;
  the surviving edit records both source IDs.
- Different insertions at the same offset are ordered by source ID.
- A deletion conflicts with an insertion from the *other* set whose point
  lies strictly inside the deleted range. Insertions exactly at either
  endpoint do **not** conflict.
- Identical deletions are deduplicated; overlapping deletions merge into
  their union.

## API

- `Merge(a, b PatchSet) *Result` — returns `Merged` (non-conflicting edits,
  sorted, ready to apply) and `Conflicts` (baseline ranges `[Start, End)`
  plus the deletion/insertion pairs involved; conflicting edits are excluded
  from `Merged`).
- `Apply(baseline string, edits []MergedEdit) (string, []Provenance)` —
  applies merged edits and returns, for every output byte, its origin
  (baseline offset or insertion edit + text offset).
- `MapPos(pos)` — arithmetic baseline→output offset translation.
- `VerifyOffsets(...)` — cross-checks the arithmetic mapping against the
  per-byte provenance model for every baseline position.

## Example

```go
a := patchmerge.PatchSet{ID: "a", Edits: []patchmerge.Edit{
    {Kind: patchmerge.Delete, Pos: 6, End: 11},
}}
b := patchmerge.PatchSet{ID: "b", Edits: []patchmerge.Edit{
    {Kind: patchmerge.Insert, Pos: 6, Text: "gopher"},
    {Kind: patchmerge.Insert, Pos: 11, Text: "!"},
}}
res := patchmerge.Merge(a, b)          // no conflicts: 6 and 11 are endpoints
out, prov := patchmerge.Apply("hello world", res.Merged)
// out == "hello gopher!", prov tracks every byte's origin
err := patchmerge.VerifyOffsets("hello world", res.Merged, out, prov)
```

Run tests (Go 1.11+):

```sh
GO111MODULE=on go test ./...
```
