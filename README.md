# pct — persistent path-copying AVL tree (Go, stdlib only)

Immutable balanced tree: every `Put`/`Delete`/`BatchPut` returns a new
`Tree` (a root pointer); old versions keep working because nodes are never
mutated — updates copy only the affected root-to-leaf path and share all
untouched subtrees by pointer.

## Node memory layout (64-bit, `unsafe.Sizeof(node{}) == 40`)

| offset | field  | type   | bytes |
|-------:|--------|--------|------:|
| 0      | key    | int64  | 8     |
| 8      | val    | int64  | 8     |
| 16     | height | int32  | 4     |
| 20     | size   | int32  | 4     |
| 24     | left   | *node  | 8     |
| 32     | right  | *node  | 8     |

No padding; Go adds no object header, but the allocator rounds each node to
the 48-byte size class. `Tree.Stats()` reports nodes × 40 B logical payload.

## API

- `Get` / `Put` / `Delete` — point ops, O(log n) path copy; deleting an
  absent key returns the identical root, so `Digest()` is unchanged.
- `Rank(key)` / `Select(i)` — order statistics via the subtree `size` field,
  which is recomputed bottom-up through every rotation.
- `Iterate` / `Range(lo,hi)` / `RangeFrom(lo)` — stack-based iterators over
  immutable nodes; iterators of different versions never interfere.
- `BatchPut(kvs)` — sorts + de-dups (stable, last write wins), then applies
  all updates in one divide-and-conquer sweep: subtrees intersecting no
  update are reused by pointer, so each surviving node is copied at most
  once instead of once per key.
- `Digest()` — FNV-1a over the in-order stream; `SharedNodes(a,b)` counts
  pointer-shared nodes between two versions.

## Run

```sh
go test ./...   # invariant + reference tests
go run ./cmd/demo
```
