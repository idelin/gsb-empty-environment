# permtest

Exact permutation (randomization) tests in Go. All enumeration is over
sample **positions**, never over distinct values, so duplicate observations
are treated as separate samples. Statistics are computed in exact rational /
integer arithmetic, so tail probabilities are exact fractions.

## Two-sample exact permutation test

`TwoSample(x, y)` enumerates every choice of `len(x)` positions out of the
pooled samples (each group at most 12 samples) and counts the assignments
whose absolute mean difference is greater than or equal to the observed one
(two-sided tail).

```go
f, _ := permtest.TwoSample([]float64{-1, -2}, []float64{3, 4})
fmt.Println(f)            // 2/6 = 1/3 (~0.333333)
fmt.Println(f.Reduced())  // {1 3}
```

The returned `Fraction` reports the numerator (tail count), the denominator
(`C(n1+n2, n1)`), the reduced fraction and the float approximation.

## Stratified cluster randomization

`StratifiedCluster(strata)` handles designs where **clusters** of samples
switch arms as whole units and each stratum keeps a fixed number of treated
clusters. Only legal assignments are enumerated (product of `C(M_s, K_s)`
over strata). The statistic is the sum of per-stratum weighted mean
differences with rational weights (default: stratum sample share).

Within each stratum, the first `Treated` clusters are the observed treated
ones; all legal reassignments are enumerated against that observation.

```go
strata := []permtest.Stratum{
    {Clusters: []permtest.Cluster{{Values: []float64{0}}, {Values: []float64{3}}, {Values: []float64{6}}}, Treated: 1},
    {Clusters: []permtest.Cluster{{Values: []float64{10}}, {Values: []float64{20}}}, Treated: 1},
}
res, _ := permtest.StratifiedCluster(strata)
fmt.Println(res.P)        // 2/6 = 1/3 (~0.333333)
fmt.Println(res.Observed) // -67/10
fmt.Println(res.Strata)   // per-stratum assignment counts (3 and 2)
```

`StratifiedResult.Strata` provides the stratification count evidence:
clusters, treated clusters, samples and legal-assignment counts per stratum,
whose product equals `Total` (and `P.Den`).

## Tests

`go test ./...` runs hand-computed cases (duplicate values, negative values,
unequal group sizes, custom rational weights) plus cross-checks against
independent brute-force enumerators over randomized small designs.
