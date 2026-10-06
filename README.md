# gauge

Exact decimal arithmetic for instrument values and unit conversion,
plus a resumable expression-DAG evaluator with per-node rounding
policies. No floating point is ever involved: every value is a
normalized big-integer fraction.

## decimal

`gauge/decimal` stores values as `num/den` (big integers, `den > 0`,
fully reduced), so multiply/divide chains are computed exactly and
rounding happens once, at the end.

- `Parse("123.45")` — gauge values enter as decimal fixed-point
  literals (exponent notation is rejected).
- `NewRational(127, 5)` — unit conversion factors are exact rationals.
- `Mul` / `Div` — exact; `Div` by zero returns `ErrDivisionByZero`.
- `Round(scale)` — half-even (banker's) rounding to `scale` decimal
  places, returning auditable `RoundEvidence` (truncated value,
  discarded remainder, tie flag, adjustment flag).
- Zero has a unique representation: `-0.000` parses to `0`, and
  rounding `-0.005` to 2 places yields `0`, never `-0`.

```go
v := decimal.MustParse("123.45")        // gauge reading, inches
inToMM, _ := decimal.NewRational(127, 5)
mmToM, _ := decimal.NewRational(1, 1000)
exact := v.Mul(inToMM).Mul(mmToM)       // 3.13563, exact
rounded, ev, _ := exact.Round(4)        // 3.1356
fmt.Println(rounded, ev)                // evidence: tie/adjusted flags
```

## expr

`gauge/expr` evaluates arithmetic DAGs of `decimal.Value`s. Nodes may
be shared by multiple parents; each node declares its own rounding
policy (`Scale`), and only marked nodes are rounded — the rest stay
exact.

- `Graph.Set(id, node)` adds or replaces a node; replacing one
  invalidates the memo caches of the node and all transitive
  successors, leaving unrelated nodes cached.
- `Graph.Eval(id)` evaluates with memoization and returns the value
  plus a `[]Step` trace: every node's exact value, rounded value and
  rounding evidence.
- `Evaluator` runs on an explicit stack and accepts a completion
  budget: `Run(2)` suspends with `ErrSuspended` after two node
  completions and resumes where it stopped on the next call.
- `Graph.ClearCache()` forces from-scratch recomputation to
  cross-check incremental results.

```go
g := expr.NewGraph()
g.Set("a", expr.Node{Op: expr.OpConst, Value: decimal.MustParse("-0.375"), Scale: intPtr(2)})
g.Set("k", expr.Node{Op: expr.OpConst, Value: decimal.MustParse("7")})
g.Set("s", expr.Node{Op: expr.OpMul, A: "a", B: "k"})
g.Set("root", expr.Node{Op: expr.OpMul, A: "s", B: "s"}) // shared subexpression
v, steps, _ := g.Eval("root") // 7.0756; a rounds -0.375 -> -0.38 (tie)
```

## Tests

```sh
GO111MODULE=on go test ./...
```

The `decimal` tests cross-check randomized mul/div chains and rounding
against an independent `big.Rat` reference implementation, covering
positive/negative midpoints, carry propagation and repeated
conversions. The `expr` tests verify negative-midpoint rounding,
shared-subexpression memoization, transitive cache invalidation and
resumed evaluation against from-scratch runs.
