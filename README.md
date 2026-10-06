# rewrite

Go library for analyzing bounded string rewriting systems with control
states and one-time resources.

## Model

- A **rule** replaces one occurrence of `LHS` with `RHS` (possibly empty)
  at one match position. All positions are considered, including
  overlapping ones.
- Rules are gated by a finite **control state** (`From`/`To`) and carry a
  **resource delta**. A full state is `(string, control, resources)`;
  cycle detection uses the whole triple, never the string alone.
- Successors longer than `MaxLen` are discarded. Resources must stay
  within `[0, MaxResource]`; `MaxResource` is required if any rule
  produces resources, keeping the state space finite.

## Usage

```go
s, err := rewrite.NewSearcher(cfg)
res := s.Run(budget) // call again with more budget while res.Status == rewrite.Unknown
```

- `FoundCycle`: `res.Entry` is the shortest path from the initial state
  to the cycle; `res.Cycle` lists rules and positions that return to the
  exact same state. Verify with `rewrite.Replay(cfg, res.Entry, res.Cycle)`.
- `NoCycle`: `res.Longest` is the longest derivation length.
- `Unknown`: node budget exhausted; resume by calling `Run` again.

`Searcher.Snapshot()` exposes the full explored reachable graph (states
and labeled edges) for auditing. `Replay` re-validates every step —
match position, control state, resource bounds — and requires the cycle
to close on the complete state, so evidence that secretly reuses
one-time resources is rejected.
