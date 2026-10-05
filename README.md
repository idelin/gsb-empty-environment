# bayes

Exact Bayesian evidence updating over a finite hypothesis set, using
rational arithmetic (`math/big.Rat`) throughout — no floating-point error.

## Design

- **Rational priors**: `NewModel` accepts non-negative rational priors
  (unnormalized is fine; they are normalized internally).
- **Evidence groups with joint likelihood**: `AddEvidence(id, likelihood)`
  registers a *group* of observations. `likelihood[h]` is the joint
  probability of the whole group under hypothesis `h`. Observations within
  a group are never multiplied together as independent events — the caller
  supplies the joint value directly.
- **Exact posterior**: `Posterior()` returns `P(H_h | evidence)` as exact
  rationals, `prior[h] * prod_g L_g[h]` normalized.
- **Contradiction**: if the combined evidence has zero total likelihood,
  `Posterior` (and `BestObservation`) return `ErrContradiction`.
- **Retraction**: `Retract(id)` removes a group; the posterior reverts
  exactly. Duplicate `AddEvidence` ids are ignored — repeated evidence
  counts only once.
- **Next-observation selection**: `BestObservation(candidates)` picks the
  candidate minimizing the expected misjudgment rate
  `1 - sum_o max_h post[h] * P(o|h)` (pick the MAP hypothesis after seeing
  the outcome). Ties break to the lowest candidate index.

## Verification

Tests cross-check the posterior and the expected error against independent
joint-probability enumeration (`enumeratePosterior`, `enumerateError` in
`bayes_test.go`), including fractions with no exact float representation.

## Example

```go
m, _ := bayes.NewModel([]*big.Rat{bayes.Rat(1, 2), bayes.Rat(1, 2)})
m.AddEvidence("lab-results", []*big.Rat{bayes.Rat(1, 2), bayes.Rat(1, 4)})
post, err := m.Posterior() // (2/3, 1/3); err == bayes.ErrContradiction on conflict
m.Retract("lab-results")   // back to the prior

best, minErr, err := m.BestObservation([]bayes.Observation{...})
```

Run tests with `go test ./...`.
