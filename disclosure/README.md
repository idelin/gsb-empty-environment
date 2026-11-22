# disclosure

Pre-publication disclosure review for report publishing services. Import path:

```go
import "prescreen.local/ddd-2056-20261122-v2/disclosure"
```

## What it does

The protected source is a non-negative integer grid of at most 4x4 cells.
Each cell has an inclusive `[Min, Max]` interval. Row totals, column totals and
optional subtotals are linear equality `Constraint`s with arbitrary integer
coefficients, so non-totally-unimodular subtotals (the ones that punch holes
in a feasible range) are supported.

A constraint either is always published or carries a `GroupID`. Every
constraint in a `SuppressionGroup` is released together or hidden together.

For each `SensitiveCell`, the reviewer requires at least `K` distinct feasible
integer values under the published constraints. The full feasible set is
enumerated over the small cell domains, so a wide `[Min, Max]` interval with an
infeasible middle value is handled correctly instead of trusting the
max-min spread.

## Decision

`Review(*Request) (*Decision, error)` searches every combination of hidden
groups and returns the minimum total hiding cost that protects every sensitive
cell. Ties compare the lexicographic order of the sorted group ID sequences.

`Decision` reports:

- `Approved`, `HiddenGroupIDs`, `PublishedIDs`, `TotalCost`, `Budget`.
- One `CellReport` per sensitive cell with the ascending `FeasibleValues`, a
  complete `Witness` table for every feasible value, and re-checkable
  `Exclusions` (each cites either a concrete constraint interval or an
  independently re-runnable exhaustive proof).
- A non-empty `RejectedReason` when no within-budget publication set works,
  including budget exhaustion and an actual table that violates an
  always-published constraint.

Enumeration is bounded by `Request.MaxEnumNodes` (default 2,000,000 partial
assignments per publication set). Hitting the cap fails closed: the request is
never approved.

## Tests

```sh
go test ./...
go test ./... -race -cover
```

The test suite contains an independent brute-force enumerator that shares no
pruning code with the package. It cross-checks the four required scenarios:
two marginally safe reports jointly pinning a cell, a subtotal-induced gap, an
actual table violating a public constraint, and a leak that survives hiding
the last group — plus budget exhaustion, the ID-sequence tie-break and the
enumeration fail-closed path.
