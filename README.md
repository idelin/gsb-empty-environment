# reachnet/petri

Go library for reachability analysis of integer token Petri nets with
per-place capacity bounds.

## Semantics

- A transition **consumes first, then produces**: it is enabled when every
  input place holds enough tokens, and the *resulting* marking stays within
  every place capacity. A transition whose result would exceed a capacity
  is disabled.
- Self-loop arcs (a place that is both input and output) and
  zero-consumption transitions (no input arcs) are supported.
- Every place has a finite capacity, so the state space is finite.

## API

- `Solve(net, initial, target) (Result, error)` — breadth-first search over
  the complete state space. Returns a shortest firing sequence
  (`Result.Sequence`, transition indices) and the number of distinct
  markings examined (`Result.Explored`). If the search exhausts the state
  space without hitting the target, `Result.Reachable` is false and
  `Explored` equals the total number of reachable markings, proving
  unreachability. Every returned sequence is independently verified by
  step-by-step replay before `Solve` returns.
- `Net.Fire(m, t)` — independent single-step executor (consume then
  produce, capacity checked on the result).
- `Net.Replay(initial, seq)` — independent step-by-step verifier; fails if
  any step is disabled.

## Example

```go
net := petri.Net{
    Capacity: []int{2, 2},
    Transitions: []petri.Transition{
        {Name: "move", Consume: []int{1, 0}, Produce: []int{0, 1}},
    },
}
res, err := petri.Solve(net, petri.Marking{2, 0}, petri.Marking{0, 2})
// res.Reachable == true, res.Sequence == []int{0, 0}
```

## Test

```sh
GO111MODULE=on go test ./...
```
