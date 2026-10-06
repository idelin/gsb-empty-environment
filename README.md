# archivemerge

Go library that merges generation-ordered archive manifests into
per-snapshot visible trees with source tracking.

## Model

- Each generation is a `Manifest` of `Entry` records: `File`, `Dir`, or
  `Tombstone`, keyed by clean slash-separated relative paths.
- `Merge(manifests)` applies generations in order and returns one
  `Snapshot` per generation; each visible `Node` records the `Source`
  generation and whether it was `Explicit` or implied by a descendant.

## Semantics

- Within a generation, tombstones apply before explicit entries, so a
  directory tombstone shadows the directory and all descendants inherited
  from older generations, but never entries the same (or a later)
  generation explicitly rebuilds beneath it.
- Placing an entry under a file ancestor converts the ancestor to an
  implicit directory (parent type change, file → dir).
- Placing a file where a directory exists removes the directory and its
  whole subtree (dir → file).
- Making an implicit directory explicit keeps its inherited children.

## Test model

`merge_test.go` verifies behavior with a per-generation virtual tree
model: each generation's expected visible tree is declared as a
`path -> {kind, source, explicit}` map and checked against the snapshot,
covering parent type changes, ancestor deletion, and partial rebuilds.

```sh
go test ./...
```
