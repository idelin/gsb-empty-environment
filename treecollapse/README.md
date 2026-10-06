# treecollapse

Collapsed tree-view planner for grouped nodes, in two parts.

## Model

- `Group`: collapsible tree node with expand/collapse costs, direct
  leaves, and shared data blocks that stay in memory while expanded.
- `Target`: a group or leaf that must become visible. A node is visible
  iff every ancestor group is expanded (plus its own parent for leaves).

## Part 1 — cheapest expanded set (`part1.go`)

Enumerates all 2^n expanded sets (n <= 20), keeps ancestor-closed sets
that expose every target, and minimizes:

- shared expansion cost: each expanded group paid once regardless of how
  many targets sit beneath it, plus
- penalty for every exposed leaf that is not a target.

Cross-checked against the structural optimum `RequiredMask` (union of
target ancestor chains), which is exact because costs are non-negative.

## Part 2 — expand/collapse plan under a memory budget (`part2.go`)

State search (Dijkstra) over `(expanded set, done targets)` for n <= 12
groups. A target is "done" once it has been visible; the plan may
collapse groups afterwards to free memory. Shared blocks are reference
counted: collapsing a group only frees blocks no other expanded group
still references. Every state with live memory above the budget is
pruned. The solver minimizes total action cost and is cross-checked by
`VerifyPart2` (Bellman-style fixpoint relaxation). `Replay` renders the
step-by-step reference accounting.

## Run

```sh
go run .        # demo instance with trace
go test ./...   # brute-force vs structural and solver vs verifier checks
```
