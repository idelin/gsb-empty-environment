# gaugeconv

Exact decimal/rational arithmetic for gauge readings and unit conversion, plus
a resumable DAG expression evaluator. No floating point is used anywhere: all
values are `math/big.Rat` and rounding is done with big-integer Euclidean
division.

## exact — fixed-point input, exact chains, half-even rounding

- `ParseDecimal("123.456")` parses a decimal fixed-point literal into an exact
  rational (no float detour).
- `EvalChain(start, ops, places)` applies a multiply/divide chain of rational
  factors with full precision, then rounds **once** at the end to `places`
  decimal digits using half-even (banker's) rounding. Pass `places < 0` to
  skip rounding.
- Division by a zero factor fails with `ErrDivideByZero` (see
  `IsDivideByZero`).
- Negative zero is normalized: `-0.001` rounded to 2 places renders `0.00`.
- Every result carries a `RoundEvidence` audit trail: exact pre-round input,
  floor of the scaled value, remainder, tie flag, and resolution direction.

```go
res, err := exact.EvalChainDecimal("2.5", []exact.ChainOp{
    exact.MulBy(big.NewRat(25146, 15625), "mi->km"), // 1.609344 exactly
    exact.MulBy(big.NewRat(1000, 1), "km->m"),
}, 2)
// res.Text == "4023.36"; res.Exact == 100584/25; res.Evidence auditable
```

## dag — resumable expression DAGs with per-node rounding

- Nodes (`Input`, `Const`, `Add`, `Sub`, `Mul`, `Div`, `Neg`) may be shared by
  any number of parents; shared subexpressions are evaluated once.
- Only nodes marked with `WithRound(places)` are rounded; every other node
  keeps its exact rational value.
- Mutating a node (`SetDecimal`, `SetConst`, `SetOp`, `SetArgs`, `SetRound`)
  invalidates the caches of all transitive successors; unrelated nodes keep
  their cached values.
- `Evaluator.Eval(ctx, node)` records one `Step` per node (value, pre-round
  value, rounding evidence, cache-hit flag) and honours context cancellation —
  a cancelled run keeps its partial cache, so a later `Eval` resumes where it
  stopped.
- `EvalFresh(node)` recomputes from scratch without touching caches; the tests
  cross-check every incremental result against it.

## Verification

`go test ./...` cross-checks the implementation against independent big-integer
fraction reference computations, covering positive/negative midpoints, carry
across the decimal point, negative zero, repeated there-and-back conversions,
shared subexpressions, cache invalidation, and resume-after-cancel.
