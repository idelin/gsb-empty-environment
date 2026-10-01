# graphindex

Compressed static directed-graph index (Go, stdlib only).

## Encoding layout

Per node, neighbors are sorted (unique ids; self-loops kept) and stored as:

```
uvarint deg
uvarint nskips                      = ceil(deg / BlockSize)   (BlockSize = 8)
nskips x { uvarint absID-delta,     absolute id of block head (delta from prev head)
           uvarint offset-delta }   byte offset of block's deltas in payload
payload: uvarint deltas             ids 2..8 of each block, delta from previous id
```

Parallel-edge multiplicities live in a separate sidecar section per node
(`uvarint count`, then `(neighbor-index delta, extra = count-1)` pairs), so
the main index stays a pure simple-graph layout and semantics are unchanged.

Two `uint64` offset arrays (`n+1` each) index the adjacency blob and the
sidecar blob. `Marshal`/`Parse` serialize the whole thing with uvarint
delta-encoded offsets; `Parse` fully validates on load.

## Queries

- `Degree(u)` — distinct neighbors (header only)
- `WeightedDegree(u)` — including parallel copies
- `HasEdge(u,v)` — skip-table binary search + walk of <= BlockSize deltas
- `Multiplicity(u,v)` — sidecar lookup (0 if no edge)
- `Neighbors(u)` — full decode
- `Intersect(u,v)` — merge join of decoded lists
- `Validate()` / `Parse()` reject corruption: out-of-range ids from broken
  deltas, bad skip offsets, non-increasing ids, bad sidecar entries
  (all wrap `ErrCorrupt`)

## Run

```
go test ./...
go run ./cmd/demo
```
