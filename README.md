# srs — bounded string rewriting analysis

`srs` analyzes string rewriting systems with a finite length bound,
finite control states, and one-shot resources.

## Model

- A **rule** replaces one occurrence of `Left` with `Right` (possibly
  empty, deleting the match). All match positions are enumerated,
  including overlapping ones.
- Rewriting one step also moves a finite **control state** (`From` →
  `To`) and rewrites a **resource vector** (`Need` is consumed, `Gain`
  is produced). Resources are one-shot: firing requires
  `Need[i] <= Res[i]`.
- Successors longer than `MaxLen` are discarded.
- The search state is the triple `(string, control, resources)`. A
  cycle requires all three to repeat — a repeated string with a
  drained resource is **not** a cycle.

## Search

`Searcher` explores the full reachable state graph breadth-first and
is pausable/resumable: each `Search(nodeBudget)` call spends at most
that many node expansions.

- Budget exhausted with frontier remaining → `Unknown`; call `Search`
  again with more budget to resume.
- Graph fully explored, cycle exists → `Cycle` with `Entry` (shortest
  path from the start to the nearest cyclic state) and `Cycle` (a
  concrete step sequence returning the entry state to itself).
- Graph fully explored, acyclic → `NoCycle` with `Longest` (longest
  derivation length) and a witness `LongestPath`.

Cycle detection uses strongly connected components on the fully
explored reachable graph, so overlapping matches, empty right-hand
sides, and diamond re-convergence are all handled exactly.

## Evidence

Every finding is checkable by replay:

- `VerifyCycle(start, entry, cycle)` re-simulates the evidence and
  requires the cycle to return to the exact entry state — string,
  control state, and resources. Evidence that secretly reuses a
  one-shot resource fails the resource precondition during replay.
- `VerifyDerivation(start, steps)` checks an arbitrary derivation.

## Example

```go
sys := &srs.System{
    Rules: []srs.Rule{
        {Name: "ab", Left: "a", Right: "b"},
        {Name: "ba", Left: "b", Right: "a"},
    },
    MaxLen: 8,
}
sr, _ := srs.NewSearcher(sys, srs.State{Str: "a"})
res := sr.Search(1000)
// res.Kind == srs.Cycle, res.Cycle has 2 steps, res.Entry is empty.
err := sys.VerifyCycle(srs.State{Str: "a"}, res.Entry, res.Cycle)
```

## Tests

```
go test ./...
```
