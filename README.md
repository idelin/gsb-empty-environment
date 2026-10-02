# physprop — Physical Property Enforcement (Sort / Order)

Bottom-up tracking and enforcement of the physical **ordering** property,
in the style of Cascades enforcers, using only the Go standard library.

## Design

- `plan.go` — logical plan nodes (`Scan`, `LogicalSort`, `GroupBy`,
  `LogicalMergeJoin`) and `Order` / `Satisfies` (prefix semantics).
- `physical.go` — physical operators. Each reports `Ordering()`, the order
  it guarantees on its output: `IndexScan` provides its index columns,
  `Sort` provides its keys, `StreamAggregate` preserves grouping keys,
  `MergeJoin` preserves the left input's order, `SeqScan`/`HashAggregate`
  provide nothing.
- `optimizer.go` — `Optimizer.best(node, required)` optimizes every
  subplan under the ordering requirement pushed down from its parent.
  A `Sort` enforcer is inserted **only** when no candidate already
  satisfies the requirement; cost (external sort = O(n log n)) decides
  between alternatives.

## Acceptance boundary

`ORDER BY u.id` over `IndexScan(u.id)`: the optimizer recognizes the data
is already ordered and elides the physical `Sort` operator entirely —
verified by `TestOrderByCoveredByIndexScan_SortElided`.

## Run

```sh
go test ./... -v   # 9 tests
go run ./cmd/demo  # prints optimized plans for 5 scenarios
```
