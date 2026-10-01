# bounded-estimation

Bounded random-variable mean estimation in pure Python 3.11 (standard
library only, tests with `unittest`).  All guarantees are **exact and
distribution-free**; empirical numbers are always labeled as such.

## What it provides

- **Staged checking with exact confidence sequences.**  Repeated peeking
  may not reuse a single fixed-sample bound, so checks happen only at a
  pre-declared geometric schedule `n_0, n_1, ...` and stage `k` is
  allocated `delta / 2**(k+1)` of the failure budget (peeling / union
  bound).  The estimator stops **only** when the radius meets `epsilon`;
  if the budget runs out first it reports `Status.BUDGET_EXHAUSTED` and
  never claims convergence.  Every stage check is kept as stopping
  evidence (`result.evidence`).
- **Adaptive multi-variable estimation.**  `AdaptiveMultiEstimator`
  estimates several bounded variables at once and adaptively picks the
  next variable to sample (largest radius/epsilon ratio).  The overall
  failure probability is controlled by a pre-declared Bonferroni split
  `delta_i = delta * weight_i / sum(weights)`.  Variables whose target is
  provably unreachable within the remaining budget are left unconverged
  and the decision is logged as evidence.
- **Correlated shared samples are handled honestly.**  A `joint_sampler`
  may feed all variables from one correlated draw.  Coverage rests on the
  union bound only — per-variable coverages are never multiplied, so no
  independence assumption is made.
- **Finite Bernoulli exact verification.**  `bernoulli.exact_coverage`
  computes coverage by an exact binomial recurrence (no simulation) for
  small `n`, including the strict boundary points `p = 0` and `p = 1`
  where coverage is exactly 1; `verify_on_grid` reports worst-case
  coverage over a grid of `p`.
- **Constant variables converge quickly.**  If all `n` observations are
  identical, an exact degenerate bound
  `radius = (high - low) * (1 - delta**(1/n))` (derivation in
  `confidence.constant_observation_radius`) shrinks like `1/n` instead of
  `1/sqrt(n)`.
- **Caller-injected randomness.**  The caller passes `rng` (or a `seed`);
  both the seed and the number of samples drawn are recorded in every
  result.
- **Labeled conclusions.**  `ConclusionKind.EXACT` marks distribution-free
  guarantees (radii, grid verification); `ConclusionKind.EMPIRICAL` marks
  estimates and simulation-based cross-checks.
- **Parameter validation.**  `delta in (0, 1)`, `epsilon > 0`,
  `low < high`, positive budgets/weights, `growth > 1`, and runtime
  range-checking of every sample all raise `ValueError`.

## Layout

- `bounded_estimation/confidence.py` — exact radii and stage schedules
- `bounded_estimation/estimator.py` — `StagedTracker`, `estimate_mean`
- `bounded_estimation/multi.py` — `AdaptiveMultiEstimator`
- `bounded_estimation/bernoulli.py` — exact binomial coverage verification
- `examples/run_demo.py` — runnable end-to-end demo
- `tests/` — `unittest` suite

## Quick start

```python
import random
from bounded_estimation import estimate_mean

rng = random.Random(20261001)
result = estimate_mean(
    sampler=lambda r: 1.0 if r.random() < 0.3 else 0.0,
    low=0.0, high=1.0, epsilon=0.02, delta=0.05,
    max_samples=200_000, rng=rng, seed=20261001,
)
assert result.converged
print(result.estimate, result.radius, result.n)  # 0.2939 0.0197 16384
```

## Running

```bash
python3 examples/run_demo.py                 # demo (output below)
python3 -m unittest discover -s tests -t .   # test suite
```

## Demo output (abridged)

```
1. Single bounded variable: Bernoulli(0.3), staged checking
  status          : converged
  estimate        : 0.293884  [empirical]
  radius          : 0.019687  (guarantee: exact)
  samples used    : 16384
  stage evidence (all checks kept):
    stage=0  n=     8 mean=0.3750 radius=0.5000 met=False
    ...
    stage=11 n= 16384 mean=0.2939 radius=0.0197 met=True

2. Constant variable: converged with n=1024 (Hoeffding alone needs ~26k)

3. Budget exhausted: status=budget_exhausted, converged=False,
   radius=0.129157 > epsilon=0.0001, 6 evidence records saved

4. Adaptive multi-variable: all 3 variables converged,
   total draws=18432, delta split 0.0167/0.0167/0.0167

5. Correlated shared samples (up = 1 - down): both converged,
   coverage via union bound only

6. Bernoulli EXACT coverage (recursion):
  n= 10: worst coverage=0.9918  target=0.95  passed=True  [exact]
  n= 30: worst coverage=0.9906  target=0.95  passed=True  [exact]
  n= 60: worst coverage=0.9935  target=0.95  passed=True  [exact]

7. EMPIRICAL cross-check: 300 trials, 0 failures (delta=0.2)  [empirical]
```

## Test results

```
$ python3 -m unittest discover -s tests -t .
Ran 43 tests in ~1.5s
OK
```

The suite covers: exact radius formulas, stage schedules, convergence and
honest budget exhaustion (with saved evidence), constant-variable fast
convergence, repeated-peeking empirical coverage within `delta`, adaptive
selection behavior, hopeless-variable evidence, correlated joint sampling
coverage, exact Bernoulli grid verification (including boundaries), and
parameter-range validation.
