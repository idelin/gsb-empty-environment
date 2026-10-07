# eventgraph/hb

Go library that builds a happens-before partial order from distributed
trace events and derives a deterministic topological order plus Lamport
logical clocks.

## Model

- Each `Event` has a unique `ID`, a `Node`, and a local sequence number
  `Seq`. `Recv` events link to their message source via `SendID`.
- Wall-clock timestamps (`Wall`) are carried for display only and never
  influence causality.
- Edges come from two sources only: local order (consecutive `Seq` on the
  same node) and message order (send -> recv).

## Semantics

- **Out-of-order import**: edges resolve lazily at analysis time, so a
  `Recv` may be added before its `Send`.
- **Duplicates**: re-adding an identical event merges (idempotent);
  a contradictory duplicate or a claimed `(Node, Seq)` slot returns
  `*ConflictError`.
- **Contradictions**: links that contradict local order produce a cycle;
  `TopoSort`/`LogicalClocks` return `*CycleError` with the closed evidence
  path and a per-edge reason.
- **Determinism**: `TopoSort` is Kahn's algorithm with ties broken by
  smallest event ID; the result is independent of import order.
- **Logical clocks**: `LogicalClocks` assigns each event one more than the
  maximum of its causal predecessors (Lamport).
- **Invariants**: `Validate` checks kind/field consistency, link targets,
  single-consumer, per-node sequence contiguity, and acyclicity, and
  returns all violations in deterministic order.

## Usage

```go
g := hb.New()
g.Add(hb.Event{ID: "a1", Node: "A", Seq: 1, Kind: hb.Send})
g.Add(hb.Event{ID: "b1", Node: "B", Seq: 1, Kind: hb.Recv, SendID: "a1"})

if errs := g.Validate(); len(errs) > 0 { /* handle */ }
order, _ := g.TopoSort()
clocks, _ := g.LogicalClocks()
```

## Test

```sh
go test ./hb/
```
