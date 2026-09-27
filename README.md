# Importance Sampling Estimators

Python 3.11, standard library only. Tests use `unittest`.

## Files

- `importance_sampling.py` — core module: ordinary & self-normalized IS
  estimators, ESS, log-stabilized weights, diagnostics, mixture proposals,
  adaptive multi-round sampling with checkpoints, exact discrete references.
- `test_importance_sampling.py` — 17 unittest cases.
- `examples.py` — runnable demo (`python3.11 examples.py`).

## Conventions

- `[EXACT]` — analytic conclusions (no Monte Carlo error).
- `[EMPIRICAL]` — finite-sample simulation results.

## Estimator properties (stated honestly)

- Ordinary estimator `(1/n) Σ wᵢ f(xᵢ)`, `wᵢ = p(xᵢ)/q(xᵢ)`: **[EXACT] unbiased**
  for `E_p[f]` when `p` is normalized and `q` covers `p`.
- Self-normalized estimator `Σ wᵢ fᵢ / Σ wᵢ`: works with an **unnormalized**
  target; **[EXACT] biased for every finite n** (ratio of random variables,
  `E[A/B] ≠ E[A]/E[B]` in general) but **[EXACT] consistent** — bias and
  variance vanish as `n → ∞`. The bias is demonstrated empirically in
  `TestBiasProperties`.

## Key design points

- **Log-stabilized weights**: `log w = log p − log q`, shifted by the max
  before exponentiation. ESS and the self-normalized estimate are
  shift-invariant; the ordinary estimate is rescaled by `exp(max)`.
- **Random source injected by caller** (`random.Random`); seed and sample
  size are recorded on every `ISResult`.
- **Mixture proposal**: `q(x) = Σ πₖ qₖ(x)`; weights use the mixture density,
  which is the true generating density.
- **Adaptive multi-round IS**: each round's weights are computed with the
  proposal density in force when the samples were generated, then frozen.
  Checkpoint restore (`AdaptiveIS.load_checkpoint`) reuses stored weights
  verbatim and never recomputes them with a newer proposal density.

## Diagnostics

| Code | Severity | Trigger |
|---|---|---|
| `ZERO_COVERAGE` | error | finite-domain point with target prob > 0, proposal prob = 0 → IS unavailable |
| `INFINITE_WEIGHT` | error | a weight is +inf/NaN (proposal 0 where target positive) → estimates unavailable |
| `ALL_WEIGHTS_ZERO` | error | all observed weights zero (empirical zero coverage) |
| `EMPTY_SAMPLE` | error | n = 0 |
| `LOW_ESS` | warning | ESS below 10% (configurable) of n |

Equal weights give ESS = n exactly (verified in tests).

## Run

```
python3.11 examples.py     # demo
python3.11 -m unittest -v  # tests
```

## Sample output (`python3.11 examples.py`)

```
[EXACT] E_target[X] on finite domain = 6.857143

--- 1. Ordinary vs self-normalized (single run, n=2000, seed=42) ---
  [EMPIRICAL]: ordinary=6.9433  self_normalized=6.8440  ESS=1192.8/2000  seed=42

--- 2. Bias comparison over 5000 replications, n=20 ---
  [EXACT]     ordinary estimator is unbiased; self-normalized is biased for finite n (ratio of random variables) but consistent
  [EMPIRICAL] mean ordinary       = 6.8271 (exact 6.8571)
  [EMPIRICAL] mean self-normalized= 6.7715 (bias -0.0856 at n=20)

--- 3. Mixture proposal (0.3 uniform + 0.7 increasing) ---
  [EMPIRICAL]: ordinary=6.8423  self_normalized=6.8542  ESS=4028.5/5000  seed=7

--- 4. Adaptive rounds with checkpoint/restore ---
  checkpoint saved: adaptive_ckpt.json (round0 weights frozen with uniform proposal)
  [EMPIRICAL] pooled (weights frozen per round): ordinary=6.7524  self_normalized=6.8310  ESS=746.5/1000  seed=100

--- 5. Diagnostics ---
  [EXACT] zero coverage -> error: Proposal assigns zero probability to 5 point(s) with positive target probability (e.g. [5, 6, 7, 8, 9]). Importance sampling is unavailable: weights would be infinite and the estimators are not defined.
  [EMPIRICAL] skewed proposal (decaying 0.5^x): ordinary=6.4365  self_normalized=6.4859  ESS=8.7/500  seed=9
    [WARNING] LOW_ESS: Effective sample size 8.7 is below 10% of n=500: a few weights dominate and the estimates are unreliable.
```

## Test results (`python3.11 -m unittest`)

```
Ran 17 tests in 1.229s

OK
```

Coverage of the suite:

- [EXACT] identities: `logsumexp`, ESS = n for equal weights, exact discrete
  expectation, scale invariance of the self-normalized estimator / rescaling
  of the ordinary one.
- [EMPIRICAL] bias properties: ordinary estimator unbiased (z-test at 4 SE);
  self-normalized bias detectable at n=5 and shrinking with n; unnormalized
  target recovered by self-normalized (ordinary off by the scale factor).
- Diagnostics: zero coverage → unavailable; infinite weight; all-zero
  weights; empty sample; low ESS warning; seed/n recorded.
- Mixture proposal: density matches manual mixture exactly; estimates
  converge to the exact mean.
- Adaptive IS: checkpoint restore preserves frozen weights (proven not to be
  recomputed with the new proposal density); non-finite weights round-trip
  through JSON and still surface as errors.
