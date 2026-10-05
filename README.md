# distconv

Exact discrete probability convolution for independent random variables
with finite integer support and rational (`math/big.Rat`) probabilities.

## Features

- **Exact arithmetic**: all probabilities are `*big.Rat`; input masses
  must sum to exactly 1 or construction fails.
- **Normalization**: duplicate values are merged, zero-mass entries
  removed, negative masses rejected.
- **Sum distributions**: `Convolve` (pairwise), `ConvolveAll` (balanced
  tree over a slice).
- **Incremental updates**: `Convolver` caches every convolution subtree
  in a binary tree; after `Set(i, d)` replaces one variable, `Sum`
  recomputes only the O(log n) nodes on the leaf-to-root path.
- **Queries**: `CDF` P(X≤t), `Less` P(X<t), `SF` P(X>t), `Geq` P(X≥t),
  `Prob` P(X=v), `Quantile` (smallest v with CDF(v) ≥ p, exact on
  cumulative boundaries), `Mean`.

## Example

```go
a := distconv.Must([]distconv.Pair{
    {Value: -2, Prob: big.NewRat(1, 3)},
    {Value: 1, Prob: big.NewRat(2, 3)},
})
b := distconv.Degenerate(-5)

c, _ := distconv.NewConvolver([]*distconv.Dist{a, b})
sum := c.Sum()
q, _ := sum.Quantile(big.NewRat(1, 2)) // exact median boundary
tail := sum.Geq(0)                     // P(X >= 0)

c.Set(1, distconv.Degenerate(3))       // reuse unchanged subtrees
sum2 := c.Sum()
```

## Tests

`go test ./...` verifies convolution, quantiles, and tail probabilities
against full combinatorial enumeration of all value tuples, covering
negative values, degenerate variables, and quantile probabilities that
land exactly on cumulative boundaries.
