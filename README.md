# Subsampled Amplification: Stable Evaluation, Interval Certification, Inversion

Python 3.11+, standard library only (`math`, `decimal`, `dataclasses`,
`unittest`). Tests use `unittest`.

## Definition and scope

Given a base mechanism that is `(eps, 0)`-differentially private, Poisson
subsampling with rate `q` (add/remove adjacency) yields the amplified
parameter

```
eps' = log(1 + q * (exp(eps) - 1)),     q in [0, 1],  eps >= 0
```

This repository computes that formula — and its inverse — with certified
numerics. It does **not** prove privacy: a correctly computed number is
only as valid as the assumptions behind it. All parameters here are
synthetic; any statistical experiment would be evidence, not a formal
privacy proof, and none is offered as one.

The formula is applied **only** when its sampling model holds
(`amplification/assumptions.py`): Poisson sampling, add/remove adjacency,
base `delta = 0`, and a consistent dataset across composed rounds.
Anything else is rejected (`AssumptionError` / `ValueError`) instead of
being silently clamped or stretched.

## Layout

- `amplification/stable.py` — scalar `eps_prime(q, eps)`, never forms
  `exp(eps)` directly. Direct regime `eps <= 700`:
  `log1p(q*expm1(eps))` (no overflow possible, full relative accuracy).
  Log-space regime `eps > 700`: `log_t = log(q) + log(expm1(eps))` with
  `log(expm1(eps)) = eps + log1p(-exp(-eps))`, then
  `eps' = log1p(exp(log_t))` or `log_t + log1p(exp(-log_t))` depending on
  a branch cut — only representable quantities are exponentiated, so
  `eps = 1000` (or 5000) cannot overflow. Boundaries are exact:
  `q = 0 -> 0.0`, `q = 1 -> eps`, `eps = 0 -> 0.0`.
- `amplification/intervals.py` — interval arithmetic with outward
  rounding: every elementary op evaluates its monotone kernel at the
  endpoints and widens one ulp with `math.nextafter` (rigorous under the
  standard `< 1 ulp` libm assumption). `eps_prime` is evaluated
  compositionally (direct / log-space regimes, hulling at branch cuts).
- `amplification/dag.py` — parameter-expression DAG (`Const`, `Param`,
  `EpsPrime`, `Compose`) with point and interval evaluation;
  `compose_rounds` builds multi-round compositions and rejects
  inconsistent sampling models.
- `amplification/invert.py` — given target budget, the analytic
  `q* = expm1(budget)/expm1(eps)` (log-space, clipped to `[0, 1]`) plus a
  **certified enclosing interval** `[q_lo, q_hi]`:
  `eps'_interval(q_lo).hi <= budget` (certified feasible) and
  `eps'_interval(q_hi).lo > budget` (certified infeasible, or `q_hi = 1`
  as the domain cap). Residuals and interval evaluations are returned as
  evidence in `InversionResult`.
- `amplification/reference.py` — independent high-precision (80-digit
  `decimal`) series reference: `expm1` by Taylor series with argument
  halving, `ln` by the atanh series with power-of-two reduction (`ln 2`
  included). Shares no code path with the float implementation and never
  calls libm, so agreement is genuine cross-validation, including at the
  boundaries where naive float code overflows.

## Usage

```python
from amplification import stable, invert, dag
from amplification.assumptions import SamplingAssumption

stable.eps_prime(1e-3, 1000.0)          # 993.0922447210179 (no overflow)
stable.eps_prime(0.0, 5.0)              # 0.0   (exact)
stable.eps_prime(1.0, 5.0)              # 5.0   (exact)

r = invert.max_q_for_budget(eps=3.0, budget=0.2)
r.q_lo, r.q_hi                          # certified bracket around max q
r.residual_lo, r.residual_hi            # residual evidence

a = SamplingAssumption(dataset_size=50_000)
rounds = [dag.EpsPrime(dag.Const(0.02), dag.Const(1.5), a) for _ in range(10)]
comp = dag.compose_rounds(rounds)
comp.eval_point({})                     # 0.6731632952185461
comp.eval_interval({})                  # outward-rounded interval
```

## Sample run (`python3 demo.py`)

```
1. Stable point evaluation  eps' = log(1 + q*(exp(eps) - 1))
  boundary q=0        q=0      eps=3    eps'=0            (series ref: 0)
  boundary q=1        q=1      eps=3    eps'=3            (series ref: 3)
  tiny q              q=1e-12  eps=1    eps'=1.718281828457569e-12
  huge eps            q=0.001  eps=1000 eps'=993.0922447210179  (ref agrees)
  huge eps, tiny q    q=1e-300 eps=800  eps'=109.2244721017863  (ref agrees)

2. DAG, 10-round composition of eps'(0.02, 1.5)
  composed (point)    = 0.6731632952185461
  composed (interval) = [0.67316329521854523, 0.67316329521854712]

3. Inversion, eps=3.0, budget=0.2
  q* analytic        = 0.0116005517188872
  certified bracket  = [0.0116005517188872, 0.01160055171888721]
  series reference   = 0.0116005517188872  (inside bracket: True)
  eps'(q_lo) interval = [0.19999999999999984, 0.20000000000000001]
  eps'(q_hi) interval = [0.20000000000000007, 0.20000000000000023]

4. Model-mismatch rejection
  rejected scheme='without_replacement' / base_delta=1e-05 /
  q=1.5 / eps=-1.0  (formula not applied outside its model)
```

## Tests

`python3 -m unittest discover -s tests -v` — 54 tests, all passing:

- boundaries: `q=0 -> 0`, `q=1 -> eps` (exact), `eps=0 -> 0`;
- stability: tiny `q` (1e-12), tiny `eps` (1e-8), huge `eps` (700…5000,
  no overflow), `q*(e^eps-1)` moderate while `e^eps` overflows;
- intervals: outward rounding, containment of point and reference values,
  branch-cut straddling, domain rejection;
- DAG: multi-round composition (point + interval), rejection of
  inconsistent sampling models;
- inversion: round-trip recovery of `q`, certified-bracket semantics and
  residual signs, clipping at `q = 1`, `budget = 0`;
- reference: series kernels vs `math`, float vs 80-digit reference across
  all regimes, certified bracket encloses the high-precision truth.

## Disclaimers

- Synthetic data only.
- Numerical accuracy (including the certified intervals) is evidence of
  correct *computation*, not a privacy *proof*.
- The formula itself is the Poisson-subsampling, add/remove, pure-DP
  amplification bound; inputs outside that model are rejected.
