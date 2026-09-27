# Binomial SPRT (Python 3.11, stdlib only)

Sequential probability ratio test for H0: p = p0 vs H1: p = p1 with
log-accumulated likelihood ratios and exact Wald bounds

    accept H1 when log_lr >= log((1-beta)/alpha)
    accept H0 when log_lr <= log(beta/(1-alpha))
    UNDECIDED when n_max is reached without a crossing

## Files
- `sprt.py` — library: `BinomialSPRT`, `SharedBudget`, `EvidenceProcess`,
  `exact_operating_characteristics`, `simulate`
- `test_sprt.py` — unittest suite (16 tests)
- `examples.py` — runnable demos

## Design guarantees
- Boundaries are compared exactly (`>=` / `<=`); no epsilon shifts anywhere.
- Zero-likelihood outcomes (p0/p1 in {0, 1}) decide immediately via
  +-inf log-LR increments; never smoothed.
- `SharedBudget` is shared across streams; a failed consume leaves the
  stream untouched, so budget interruption/resume never double-counts.
- `snapshot()` / `from_snapshot()` checkpoint and restore the accumulated
  log-LR verbatim; a resumed stream is bit-identical to the original.
- `simulate` takes a caller-injected RNG and records the seed and the
  total number of samples drawn.
- Exact results (discrete (n, k)-lattice recursion, absorbing boundaries)
  are labelled `exact (discrete recursion)`; Monte Carlo estimates are
  labelled `empirical (Monte Carlo)`. The two are always reported separately.
- Error guarantees (alpha, beta) and the e-value coverage of
  `EvidenceProcess` hold only under the declared stopping rule (boundary
  crossing, n_max, or budget exhaustion). Selective stopping outside the
  declared rule voids the guarantees, and a fixed-sample p-value is never
  a substitute for the rule.

## Run
    python3 -m unittest -v   # tests
    python3 examples.py      # demos
