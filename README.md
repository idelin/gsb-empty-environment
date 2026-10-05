# backup-planner

A restore planner for sharded backups. A backup consists of baseline and
incremental **files**; each **shard** describes coverage records as directed
edges `fromVersion -> toVersion` (versions strictly increase along an edge,
baselines start at version 0). One file may serve edges of several shards.
Given a target version per shard, the planner picks a set of files with the
minimum total download bytes — jointly across shards, because a shared file
is charged only once.

Pure Go standard library. Module builds and tests with `go test ./...`.

## CLI

```
planner solve  [-budget N] [-input file]   # plan JSON from input JSON (stdin default)
planner verify [-input file]               # independently check a {"input","solution"} witness
```

### Input

```json
{
  "files":  [{"id": "f1", "bytes": 100, "corrupted": false, "dependencies": ["f0"]}],
  "shards": [{"name": "s1", "target": 30,
              "edges": [{"from": 0, "to": 10, "file": "f1"}]}],
  "budget": 1000
}
```

- `bytes` is a non-negative arbitrary-precision integer; use a JSON string
  for values beyond 64 bits. All costs and bounds are computed with
  `math/big`, so shared-file sums cannot overflow.
- `budget` (input field or `-budget` flag, flag wins) caps the number of
  expanded search nodes; negative/absent means unlimited.

### Output

- `status`: `optimal` | `budget_exhausted` | `infeasible` | `error`.
- `solution`: sorted file IDs and `totalBytes` (decimal string). A file set
  is legal iff it is dependency-closed, contains no corrupted or
  cascade-tainted file, and every shard has a path 0 -> target using only
  edges with `to <= target` (a path may never overshoot the target).
- `lowerBound`: a valid lower bound on the optimum cost. With
  `budget_exhausted` the returned solution is the best known feasible plan
  and is **not** claimed to be optimal.
- `diagnostics`: `corruptedFiles` (explicitly marked) and
  `cascadedUnusableFiles` (unusable because a dependency is corrupted).
- Hard input errors are reported separately by kind: `duplicate_file_id`,
  `missing_id`, `dependency_cycle`, `invalid_edge`, `invalid_target`.

### Optimality ordering

Primary: total bytes. Tie-break: the sorted file ID list, compared
lexicographically (a strict prefix is smaller).

## Search

Deterministic depth-first branch and bound over files sorted by ID
(include branch first). Search states use an explicit multi-word bitmap
(`bitmap.go`), so the file count is not limited to 64. One **node
expansion** = generating the include/exclude children of one undecided
file; the traversal order is fixed, so identical input and budget
reproduce identical output.

Lower bound at a node: current cost plus the maximum over unsatisfied
shards of a per-shard shortest-path bound on the version DAG. Edge weights
charge each file `bytes/share` (exact `big.Rat` arithmetic), where `share`
counts the shard edges whose serving file's dependency closure contains
the file — so files and dependencies shared inside a path are never
double-counted and the bound stays valid. Reported bounds are ceiled to
integers, which keeps them valid because every plan cost is an integer.

## Verification and cross-checking

- `planner verify` re-checks a claimed solution independently (BFS witness
  paths per shard, dependency closure, corruption, recomputed cost).
- `bruteForce` (`brute.go`) enumerates all 2^n subsets for n <= 15 and is
  used by `crosscheck_test.go` to validate the solver on hundreds of
  random instances, under both unlimited and random budgets.

## Layout

- `types.go` — JSON model, big-integer `Nat`, output statuses
- `env.go` — validation, diagnostics, dependency closures, shard graphs
- `bitmap.go` — multi-word bit set
- `solve.go` — branch-and-bound solver with budget and lower bounds
- `verify.go` — independent solution verifier
- `brute.go` — full subset enumeration (test cross-check)
- `main.go` — CLI (`solve` / `verify`)
- `examples/` — sample inputs; `RUNLOG.txt` — real run records
