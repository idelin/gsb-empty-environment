# Block mapping admission

`prescreen.local/ddd-10548-v2` is an importable Go package for validating and
certifying layered virtual-volume mappings. It does not expand mappings sector
by sector: every operation works on intervals or arithmetic classes.

```go
import admission "prescreen.local/ddd-10548-v2"
```

## Model

- A `Volume` has non-overlapping logical `Segment`s; compilation sorts them.
- A segment is linear to one child volume, striped between two child volumes
  with a power-of-two stripe width, or a physical leaf.
- `PhysicalRange`s are the tenant-owned physical sectors. They may be
  non-contiguous and carry the tenant's maximum permission.
- Effective permission is the bitwise intersection of every segment permission,
  the physical leaf permission, and the covering tenant range. It can only
  narrow.
- A path may contain at most eight reachable volume layers including the root
  and physical leaf, and a volume may be at most `2^48` sectors.

Only volumes reachable from `Graph.Roots` are analysed. If `Roots` is empty,
every declared volume is treated as a root. Reachable cycles, logical overlap,
invalid stripe widths, and every offset/stride overflow are rejected.

## Admission and proof

```go
snap, err := admission.Compile(graph)
if err != nil {
    // Nothing was published.
}
handle, err := snap.Handle("root")
proof, err := handle.Proof(0, 1<<20)
_, writable, err := handle.CheckWrite(0, 1<<20)
```

A `Proof` is an exact partition of the requested virtual range. Each `Claim`
contains:

- a virtual `Class` and its physical `Class`;
- the ordered path through the mapping graph;
- each segment's complete `Mapping`, declared permission, permission before and
  after that layer, and whether it narrowed;
- the effective `Access`, leaf permission, and tenant permission.

`Class` represents equal runs at an arithmetic stride:

```text
for each repeat combination (Cartesian product):
    [Start + repeatOffsets + i*Stride, +Run), 0 <= i < Count
```

`Repeats` make nested stripe intersections compact even when a stripe width is
one. Sector counts, not width ratios, determine storage; a mapping is never
unrolled by sector.

`CheckWrite` succeeds only if the whole range is mapped and every claim has
`PermWrite`.

## Isolation and versions

`Graph.Isolations` (and equal non-empty `Volume.IsolationGroup` values) declare
volume pairs that must not expose any physical sector through an
effective-writable logical location. `Compile` checks all such pairs before
returning a snapshot.

`Store.Publish` compiles and checks the complete graph first, then atomically
switches the current immutable snapshot. A failed update leaves the old version
current. Existing `Handle`s keep their old snapshot and proofs:

```go
store := admission.NewStore()
v1, err := store.Publish(graphV1)
old, _ := v1.Handle("root")

v2, err := store.Publish(graphV2) // validated as one multi-volume update
// old.Proof(...) still uses graphV1; v2.Handle(...) uses graphV2.
```

## Tests

```sh
go test ./...
go test -race ./...
```

The suite exhaustively checks every sector and write permission in a small
domain, and exercises `2^48`-adjacent stripe tails, overflow, read-only aliases,
last-stripe isolation overlap, and old-handle version retention.
