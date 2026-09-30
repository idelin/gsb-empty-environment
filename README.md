# Consistent Noisy Bucket Release (synthetic data only)

Python 3.11, standard library only (`fractions`, `random`, `json`,
`hashlib`, `math`, `unittest`). All post-processing arithmetic is exact
rational arithmetic (`fractions.Fraction`), so every certificate can be
re-checked independently with zero numerical tolerance.

## Files

- `qp.py` — exact rational QP: simplex projection, iterative active-set
  solver, active-set enumeration reference solver, KKT certificate checker.
- `noise.py` — discrete Laplace (two-sided geometric) noise, drawn exactly
  once per measurement key and persisted in a `NoiseLedger`.
- `pipeline.py` — public bucket schema, contribution-bounded synthetic
  data, `publish()` (noisy release), `reconcile()` (consistent projection),
  `simple_release()` (single-level convenience path).
- `demo.py` — runnable end-to-end example.
- `tests/` — 23 `unittest` tests.

## Definitions

- **Leaf buckets**: a public, fixed partition; counts are the variables
  `x = (x_0, ..., x_{n-1})`.
- **Groups**: public, possibly overlapping subsets of leaf indices whose
  sums are also published.
- **Contribution bound**: each synthetic user adds +1 to at most
  `max_contribution` distinct leaf buckets, so each published count query
  has L1 sensitivity `max_contribution`.
- **Noise**: discrete Laplace, `noise = G1 - G2` with
  `Gi ~ Geometric(1 - exp(-1/scale))`, `scale = sensitivity / epsilon_each`.
  `epsilon` is split evenly over all measurements (basic composition
  bookkeeping only).
- **Total target**: the `sum(x) = T` constraint. `T` comes either from an
  explicitly public exact count (`total_source="public"`) or from a
  separately noised measurement with its own budget share
  (`total_source="noised"`). It is never silently derived from other noisy
  values. `T < 0` is infeasible with `x >= 0` and is explicitly rejected.
- **Post-processing**: deterministic projection

  ```
  minimize  sum_j w_j (a_j . x - v_j)^2
  subject to sum_i x_i = T,  x_i >= 0
  ```

  over publications `j` with rational weights `w_j = 1/scale^2`. No
  randomness is consumed during projection, so it cannot resample until a
  pleasing result appears.

## Guarantees and failure modes

- **Reproducibility**: noise depends only on `(master_seed, key)`; the
  ledger persists every draw. A repeated publication reuses stored noise
  (`reused=True`) and never re-draws.
- **KKT evidence**: `reconcile()` returns `{x, lambda, mu}` and
  `check_kkt()` independently verifies primal feasibility, dual
  feasibility, complementary slackness, and stationarity — exactly, in
  rational arithmetic.
- **Reference verification**: `solve_by_enumeration()` enumerates all
  active sets (small `n`) and the tests cross-check it against the
  iterative solver on random instances.
- **Infeasible total**: `rejected_infeasible`, no post-processed output.
- **Iteration budget exhausted**: `not_converged`, no post-processed
  output.
- **Zero buckets / negative noise**: negative noisy values are projected
  to exactly `0`; `T = 0` yields the all-zero vector with a valid
  certificate.
- **Separation**: raw noisy values (`raw_noisy`) and post-processed values
  (`postprocessed`) are always separate fields.

## Scope and disclaimers

- Synthetic data only; no real personal data is processed.
- The epsilon split is basic composition for parameter bookkeeping. This
  code and its statistical behaviour are **not** a formal privacy proof.
- The iterative solver assumes every leaf is published with positive
  weight, which makes the objective strictly convex and the KKT systems
  non-singular.

## Run

```
python3.11 demo.py                              # end-to-end example
python3.11 demo.py                              # 2nd run reuses demo_ledger.json noise
python3.11 -m unittest discover -s tests -t .   # test suite
```

## Sample output (`python3.11 demo.py`, first run)

```
================================================================
1) Single-level release: noisy counts -> simplex projection
  true counts         : [19, 19, 20, 18, 13]
  raw noisy (separate): [28, -14, 14, 40, 9]
  total target        : 89 (source: public)
  status              : optimal
  postprocessed       : ['55/2', '0', '27/2', '79/2', '17/2']
  sum(postprocessed)  : 89
================================================================
2) Overlapping groups: weighted QP + KKT certificate
  raw noisy measurements:
        leaf:a: 40
        leaf:b: 37
        leaf:c: 8
        leaf:d: 16
    group:left: 30
     group:mid: 4
    group:right: 26
         total: 83
  true counts        : [14, 17, 14, 16]
  total target       : 83 (source: noised)
  status             : optimal  (iterations: 1)
  postprocessed      : ['106/3', '49/3', '11/3', '83/3']
  kkt violations     : []
  certificate lambda : -17/128
  certificate mu     : ['0', '0', '0', '0']
  active-set enumeration reference matches: True
================================================================
3) Failure modes
  negative total -> status=rejected_infeasible reason=total -3 < 0 is infeasible with x >= 0
  zero iteration budget -> status=not_converged
  ledger entries reused this run: 0 (>0 means noise was NOT re-drawn)
```

Second run: identical numbers, `ledger entries reused this run: 8`.

## Test results

```
$ python3.11 -m unittest discover -s tests -t .
Ran 23 tests in 0.090s
OK
```

Coverage: simplex projection (incl. zero/negative entries), infeasible
total rejection, zero total, negative noise, overlapping-group KKT
validity, tampered-certificate rejection, iteration-budget
`not_converged`, iterative-vs-enumeration agreement on 30 random
instances, noise reproducibility, ledger no-redraw persistence, and
end-to-end pipeline behaviour for both total sources.
