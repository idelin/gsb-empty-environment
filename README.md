# Coordinated Config Rollback Planner

Each node reports its currently applied config version. Versions form a
finite DAG of rollback edges; an edge undoes part of the features and
consumes shared maintenance budget.

## Phase 1 — common target selection (`RankCandidates`)

Full candidate traversal over the finite version graph: every version
reachable from all nodes is ranked by

1. retained feature count (descending),
2. total rollback steps across nodes (ascending).

Per-node shortest-step paths (BFS) are emitted for the chosen target.

## Phase 2 — constrained trajectory planning (`PlanTrajectory`)

Exact state-space search (Dijkstra over joint version tuples, ≤ 5 nodes,
finite versions). The plan executes strictly in node order and every
intermediate joint state must satisfy:

- shared maintenance budget (sum of edge costs ≤ `Budget`),
- per-node forbidden versions (irreversible operations),
- pairwise version compatibility relation.

`Solve` tries candidates in ranking order and returns the first one with a
legal trajectory.

## Independent verification (`verify.go`)

`VerifyCandidate` and `VerifyPlan` re-check solver output without reusing
solver internals: edges are looked up in the flat edge list, optimal
ranking keys are re-derived by exhaustive DFS, and the trajectory is
replayed step by step (order, edge costs, budget, forbidden versions,
compatibility, arrival at target).

## Run

```sh
go test ./...
go run .
```
