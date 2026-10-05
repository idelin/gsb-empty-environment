# restoreplanner

Restore planner for sharded backups built from baseline and incremental
files. The same file may serve several shards, so the planner minimizes the
total download bytes of the *union* of selected files — summing per-shard
optima is not valid. Everything uses only the Go standard library.

## Usage

```
restoreplanner [-budget N] [-input file] [-pretty]   # solve (input default: stdin)
restoreplanner -verify solution.json [-input file]   # independently check a plan
go test ./...                                        # run all tests
```

Exit codes: `0` optimal/feasible/unknown, `1` infeasible or failed verify,
`2` invalid input.

## Input JSON

```json
{
  "files":  [{"id": "f1", "bytes": 100, "intact": true, "deps": ["f0"]}],
  "shards": [{"name": "s0", "target": 2,
              "edges": [{"from": 0, "to": 1, "file": "f0"}]}]
}
```

- `bytes` is a non-negative arbitrary-precision integer (JSON number or
  string); `intact` defaults to `true`; `deps` lists dependency IDs.
- Each edge is a coverage record `from -> to` served by one file. Versions
  strictly increase along an edge; baselines start at version 0. A restore
  path must end exactly at the shard target and can never overshoot it.

## Output JSON

```json
{"status": "optimal", "files": ["f0", "f1"], "cost": "100",
 "lowerBound": "100", "expansions": 4, "diagnostics": []}
```

Statuses:

- `optimal` — search completed; `files` is a minimum-cost plan. Ties are
  broken by the lexicographically smallest sorted file-ID list.
- `feasible` — node-expansion budget exhausted; `files` is the best known
  plan and `lowerBound` a valid lower bound on the optimum. Never claims
  optimality.
- `unknown` — budget exhausted before any feasible plan was found; only a
  lower bound is reported.
- `infeasible` — search completed; no plan exists (see `diagnostics`).
- `error` — invalid input (see `diagnostics`).

Diagnostics distinguish `dep_cycle`, `missing_id`, `corrupted`,
`corrupted_cascade` (intact files whose transitive dependencies are
corrupted), `gap` (no version path 0 -> target) and `unrecoverable`
(every path needs a corrupted file).

## Design notes

- **Exact search**: branch-and-bound over per-shard path choices. The
  selected file set is an explicit multi-word bitmap (`bitset.go`); no
  64-file assumption. Costs and bounds are `math/big` integers, so shared
  files charged once cannot overflow a sum.
- **Budget**: counts expanded search nodes; one node is one
  (shard level, selected-set) state. Traversal order is fixed (shards in
  input order, paths in sorted-edge DFS order, files sorted by ID), so the
  same input and budget always reproduce the same output.
- **Lower bound**: current cost plus the maximum, over remaining shards, of
  each shard's minimum marginal cost — valid because the final set must
  contain every remaining shard's path. When the budget cuts the search,
  the reported bound is `min(bestKnown, min cut-node bound)`.
- **Verifier** (`verify.go`): re-checks a plan directly from the raw input
  with its own graph walks (existence, integrity, dependency closure,
  per-shard reachability, cost with each file charged once).
- **Cross-check** (`brute_test.go`): for instances with at most 15 files
  the solver is compared against enumeration of all 2^n subsets, and
  budgeted runs are checked for bound validity against the brute-force
  optimum.
