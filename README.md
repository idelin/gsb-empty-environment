# binsolve

Bin packing with integer item sizes, a fixed bin capacity, and a pairwise
conflict graph: conflicting items may never share a bin.

## Semantics

- **Objective**: minimize the number of bins. Ties are broken by the
  lexicographically smallest *normalized grouping*: items within each bin
  sorted ascending, bins sorted lexicographically.
- **Search**: instances with `n <= 12` items are solved by exhaustive set
  partition enumeration (restricted growth strings, natural index order);
  larger instances use branch and bound over size-descending order with a
  capacity-based pruning bound. Bins are unlabeled, so bin-creation symmetry
  is broken and each partition is visited once.
- **Budget**: `Solve(p, nodeBudget)` counts expanded search nodes. A greedy
  first-fit incumbent is always available, but it is only reported with
  `Optimal == true` when the search completes within budget; otherwise the
  best incumbent is returned with `BudgetExhausted == true`.
- **Lower bound**: `max(ceil(totalSize/capacity), maxClique(conflictGraph))`.
  The clique term keeps the bound valid for zero-size conflicting items.
- **Evidence**: `Solution.Evidence` lists per-bin items and loads;
  `Solution.Verify(p)` re-checks capacity, conflicts, coverage, canonical
  form, and the lower bound.
- **Infeasibility**: an item larger than the capacity (or invalid input)
  yields `Feasible == false` with a `Reason`.

## Usage

```go
sol := binsolve.Solve(binsolve.Problem{
    Sizes:     []int{3, 3, 2, 2, 4},
    Capacity:  7,
    Conflicts: [][2]int{{0, 1}},
}, -1) // negative budget = unlimited nodes
```

## Test

```sh
go test ./...
```
