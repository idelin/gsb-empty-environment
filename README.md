# edgecolor — bipartite edge scheduler

Assigns the edges of a bipartite multigraph to rounds so that edges sharing
an endpoint never run in the same round. The number of rounds always equals
the maximum degree Δ (König's line-coloring theorem). Parallel edges are
supported.

## Algorithm

Edges are colored one at a time in ascending edge-ID order with colors
`0..Δ-1`:

- If a color is free at both endpoints, the edge takes it.
- Otherwise let `a` be free at the left endpoint and `b` at the right one.
  The maximal alternating `a/b` path starting at the left endpoint with a
  `b`-edge is flipped (augmenting color swap / Kempe chain). The path
  cannot cycle back (no `a`-edge at the start) nor pass through the right
  endpoint (no `b`-edge there), so after the flip `b` is free at both
  endpoints and the edge takes `b`.

Colors are then normalized to `0..k-1` by first appearance in ascending
edge-ID order, and `Rounds` returns one matching per round.

The scheduler stores no coloring state: `Rounds` recomputes from the live
edge set on every call, so deleting and re-adding edges cannot leave stale
color indices behind. Edge IDs are never reused.

## API

- `New()` — empty scheduler.
- `AddEdge(left, right) int` — insert edge, returns its permanent ID.
- `RemoveEdge(id) bool` — delete edge.
- `MaxDegree() int` — current Δ.
- `Edges() []Edge` — live edges by ID.
- `Rounds() [][]int` — round index -> matching (edge IDs, ascending).
- `Verify(rounds) error` — checks round count == Δ, every edge scheduled
  exactly once, and no per-round endpoint conflicts.

## Testing

`go test ./...` runs unit tests plus a fuzz test that cross-checks random
small multigraphs (including parallel edges and random delete/re-add
sequences) against an exact set-partition DP over matchings
(`bruteForceMinColors` in brute.go).

## Demo

`go run ./cmd/demo`
