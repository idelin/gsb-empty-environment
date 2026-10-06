# tdeq — bounded equivalence of finite transducers

`tdeq` checks whether two finite state transducers produce the same
concatenated output for every input string of a public, finite length
bound. Transitions may emit arbitrary strings, **including the empty
string**, so equivalence cannot be decided by comparing per-edge outputs:
two machines may emit different pieces on each edge and still produce
identical overall output once the pieces are concatenated.

## Model

- A `Transducer` has states, one initial state, and transitions
  `(state, symbol) -> (next state, output string)`.
- A **missing transition is an error output**: running off the transition
  function yields a distinguished error value, distinct from every regular
  output. Two machines that both error on the same input are equal on it.
- Equivalence at bound `n`: for every input string of length exactly `n`
  over the joint alphabet, the concatenated outputs of complete executions
  are equal (error == error). Shorter prefixes are *not* judged
  independently — empty outputs create timing slack that only resolves at
  the horizon. This is what distinguishes the check from a per-edge Mealy
  comparison, and it deliberately compares outputs, not final states.

## Algorithm

`Checker` explores the reachable product graph breadth-first. Each
frontier entry carries:

- the state pair `(qA, qB)` and sticky error flags,
- the pending output difference `rem`/`ahead` (which side's cumulative
  output is ahead, and by what string),
- a `diverged` flag once the cumulative outputs are known to differ
  (including exactly one side erroring).

Per-edge output differences are absorbed into the pending difference
instead of being judged immediately. A branch is pruned when both sides
have errored (all extensions stay equal); a diverged branch only survives
to the horizon if the lagging side never errors. Terminal entries at the
bounded depth with a non-empty pending difference (or diverged) yield a
counterexample candidate, which is **verified by complete execution** of
both machines on the witness before being reported.

## Budget, unknown, and frontier recovery

`Run(budget)` expands at most `budget` frontier entries (negative =
unlimited) and returns `Equivalent`, `Inequivalent` (verified witness), or
`Unknown` when the budget is exhausted. The exploration is incremental:
call `Run` again to continue, or export the frontier with `Snapshot()` and
recover it later — even in another process — with `Resume`.

```go
a, _ := tdeq.New(1, 0)
a.AddEdge(0, 'x', 0, "ab")

b, _ := tdeq.New(2, 0)
b.AddEdge(0, 'x', 1, "")      // empty output: timing slack
b.AddEdge(1, 'x', 0, "abab")

c, _ := tdeq.NewChecker(a, b, 2)
res := c.Run(-1)              // Equivalent at bound 2 (Inequivalent at 3)

snap := c.Snapshot()          // frontier recovery
restored, _ := tdeq.Resume(a, b, snap)
res = restored.Run(100)       // resume with a fresh budget
```

## Cross-validation

`CheckExhaustive` is a reference implementation that enumerates every
input string of the bounded length and compares complete outputs. The
tests cross-validate the product-graph checker against it on randomly
generated transducers (empty outputs, missing transitions), verify every
reported witness by re-execution, and check that budget slicing and
snapshot/resume reach the same conclusions as unlimited runs.

## Layout

- `transducer.go` — transducer type, construction, complete execution.
- `checker.go` — product-graph checker, budget, snapshot/resume.
- `brute.go` — exhaustive reference checker for cross-validation.
- `checker_test.go`, `crossval_test.go` — deterministic and randomized tests.
