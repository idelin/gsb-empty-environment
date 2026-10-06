# diag — fault-dependency diagnostics

A Go library for root-cause analysis on directed fault-dependency graphs,
plus optimal test sequencing under a shared budget.

## Part 1: minimum root sets (`MinRootSets`)

1. **SCC compression** — Tarjan's algorithm collapses every strongly
   connected component into one unit (`StronglyConnectedComponents`), so a
   dependency cycle can never be counted as several independent roots.
2. **Candidates** — the source components (in-degree 0) of the condensation
   DAG; every fault is reachable from some source, so sources always
   suffice. At most 20 candidates (`MaxCandidates`).
3. **Cover = reachability** — a root covers an observed fault iff the
   fault's component is reachable from the root's component.
4. **Exact minimum** — subsets are enumerated in increasing cardinality;
   the first covering size is returned with *all* minimum solutions.
5. **Evidence** — each solution lists, per root, the faults it covers plus
   a concrete BFS witness path from a root member to the fault.
6. **Cycle check** — `VerifyCyclesSingleRoot` enumerates every member of
   every non-trivial SCC and confirms identical fault coverage, proving
   cycles are soundly treated as one root.

## Part 2: minimum worst-case-cost decision trees (`OptimalStrategy`)

- Worlds (candidate root sets, at most 8 per `MaxWorlds`) are
  distinguishable through finite tests; `Test.Outcome[w]` is the
  deterministic result of the test when world `w` is true.
- Each test consumes `Cost` from the shared budget; costs accumulate along
  a path.
- `OptimalStrategy` runs an exact memoized minimax search over world
  subsets: `f(S) = min_t (cost(t) + max_outcome f(S_outcome))`, yielding a
  non-clairvoyant strategy that depends only on outcomes observed so far.
- `ReplayStrategy` replays every world through the tree using only its own
  outcomes, verifying identification per path and that the worst
  cumulative cost equals the claimed optimum.

## Usage

```sh
go test ./...        # unit tests
go run ./cmd/diagdemo  # end-to-end demo
```
