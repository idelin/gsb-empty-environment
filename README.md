# iqp — bounded integer quadratic optimizer

Minimizes `f(x) = xᵀQx + cᵀx + const` over integer vectors in a box
`lo ≤ x ≤ hi`. `Q` may be indefinite (cross terms and negative curvature
fully supported). All costs use `math/big.Int`, so results are exact for
arbitrarily large coefficients.

## Algorithm

Exact best-first branch and bound:

- **Branching**: split the widest coordinate of the current sub-box.
- **Lower bound**: interval arithmetic over each node's sub-box — every
  term `q_ii x_i²`, `2q_ij x_i x_j`, `c_i x_i` is minimized independently
  over the box. This bound is valid for *any* symmetric `Q`; it never
  assumes positive definiteness.
- **Ties**: among equal-cost optima the lexicographically smallest point
  (coordinate order) is returned; nodes whose bound equals the incumbent
  are kept so no lexicographically smaller optimum is pruned.
- **Budget**: if the node budget runs out, the result reports the
  incumbent cost as `Upper` and the minimum bound over all open nodes as
  `Lower`, bracketing the optimum.

## Usage

Library:

```go
res := (&iqp.Problem{Q: q, C: c, Const: k, Lo: lo, Hi: hi}).Solve(maxNodes)
// res.Status, res.X, res.Upper, res.Lower, res.Nodes
```

CLI (integers as decimal strings):

```sh
echo '{"Q":[["1","2"],["2","-1"]],"c":["3","-2"],"const":"5",
       "lo":["-4","-4"],"hi":["4","4"],"budget":-1}' | go run cmd/iqp/main.go
```

## Tests

`go test ./...` cross-checks the solver against full enumeration on random
small boxes (indefinite and negative-definite `Q`, degenerate single-point
boxes, boundary optima, lexicographic ties, ~2¹⁰⁰ coefficients), verifies
budget-interrupted bounds bracket the optimum, and asserts via a node hook
that every pruning bound ever generated is valid.
