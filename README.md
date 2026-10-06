# petrinet

Go library for reachability checking of integer-token Petri nets with
per-place capacity bounds.

## Semantics

- A transition **consumes first, then produces**: tokens are removed from
  input places, then added to output places.
- A transition is **disabled** when an input place lacks enough tokens, or
  when the resulting marking would **exceed a place capacity**.
- Self-loop arcs (a place that is both input and output) and
  zero-consumption transitions follow the same rule; e.g. a self-loop on a
  full place stays enabled because consumption happens before production.
- Capacities bound every place, so the reachable state space is finite and
  exhaustive exploration is a valid unreachability proof.

## API

- `Net.Reachable(initial, target)` — BFS over the state space. Returns a
  shortest firing sequence when the target is reachable, plus the number
  of explored (expanded) states. When the target is unreachable, the
  complete reachable state space has been explored, proving
  unreachability.
- `Net.Check(initial, target)` — same search, but additionally
  **re-executes the found sequence step by step** (`Net.Verify`) as an
  independent confirmation; a mismatch is reported as an error.
- `Net.Verify(initial, target, seq)` — standalone step-by-step replay:
  every step must be enabled and the final marking must equal the target.

## Example

```go
net := &petrinet.Net{
    Places:     []string{"P", "Q"},
    Capacities: []int{1, 1},
    Transitions: []petrinet.Transition{
        {Name: "selfloop", Consume: []int{1, 0}, Produce: []int{1, 0}},
        {Name: "P->Q",    Consume: []int{1, 0}, Produce: []int{0, 1}},
    },
}
res, err := net.Check([]int{1, 0}, []int{0, 1})
// res.Reachable == true, res.Sequence == []int{1}, res.Explored == 1
```

## Tests

```
GOCACHE=/private/tmp/gocache go test ./...
```

Covers shortest sequences, capacity-disabled transitions,
consume-before-produce self-loops, zero-consumption transitions,
complete-state-space unreachability proofs, and rejection of invalid
replay sequences.
