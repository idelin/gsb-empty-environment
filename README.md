# binomial-power

Binomial test power analysis with an **exact reference** and an
**honestly labelled simulation**. Python 3.11, standard library only,
tests with `unittest`.

## Design rules

- **Exact first.** Power references come from exact binomial tail
  probabilities via a mode-anchored PMF recursion
  (`binomial_power/exact.py`). Simulation output is always labelled
  `[EMPIRICAL]` and reported with a Clopper-Pearson confidence interval
  — a point rate is never passed off as exact power.
- **Fixed test rule.** The rejection threshold `c` is computed once
  (smallest `c` with `P_{p0}(X >= c) <= alpha`) and never changes during
  a simulation.
- **Injected randomness.** Every simulator takes a caller-supplied
  `random.Random`. `scenario_streams(base_seed, names)` derives one
  independent, reproducible stream per scenario; base seed, child seeds
  and trial counts are recorded on every result.
- **Pre-declared error spending.** Adaptive stopping inspects the CI
  only at looks fixed in advance (`plan_looks`, `t**rho` spending
  family, budget sums exactly to `total_alpha`). If the trial budget
  runs out first, the result is `budget_exhausted` and the still-
  undecided (pending) interval is reported — never declared converged.
- **Adjacent-point evidence.** `min_sample_size` inverts the *exact*
  power curve (never noisy simulation estimates) and reports both
  `power(n*-1) < target <= power(n*)` so minimality is checkable.
- **Edge cases.** Zero trials raises `ValueError`; an all-rejection run
  still yields a CI of positive width (`lo = (alpha/2)**(1/n) < 1`).

## Layout

- `binomial_power/exact.py` — exact PMF/tail recursion, critical values, exact power
- `binomial_power/interval.py` — Clopper-Pearson (exact) and Wilson intervals
- `binomial_power/spending.py` — pre-declared look plans and error spending
- `binomial_power/streams.py` — per-scenario independent reproducible RNG streams
- `binomial_power/simulate.py` — rejection-rate simulation with adaptive stopping
- `binomial_power/design.py` — minimum sample size with adjacent-point evidence
- `run_example.py` — end-to-end demo
- `tests/` — 38 unittest cases

## Run

```
python3.11 run_example.py
python3.11 -m unittest discover -s tests -v
```

## Sample output (`python3.11 run_example.py`)

```
== 1. Exact power reference (binomial tail recursion) ==
  n=100, H0: p<=0.5, H1: p=0.6, alpha=0.05
  fixed rule: reject iff X >= 59 (actual size 0.0443)
  [EXACT] power = 0.622533

== 2. Simulation with pre-declared error spending ==
  base_seed=20260929; look plan: trials=(5000, 10000, 15000, 20000), per-look error=[0.00313, 0.00938, 0.01562, 0.02188]
  scenario='p1=0.55' seed=9551349652619906393 n_per_trial=100 critical=59
    [EXACT]     power reference      = 0.241491
    [EMPIRICAL] rejections/trials    = 3593/15000 = 0.239533
    [EMPIRICAL] 98.4% Clopper-Pearson CI = [0.231150, 0.248064] (half-width 0.008457)
    status=converged after 3 look(s): CI half-width 0.00845725 <= target 0.01 at pre-declared look 3
  scenario='p1=0.60' seed=5361022901226606224 n_per_trial=100 critical=59
    [EXACT]     power reference      = 0.622533
    [EMPIRICAL] rejections/trials    = 9302/15000 = 0.620133
    [EMPIRICAL] 98.4% Clopper-Pearson CI = [0.610487, 0.629712] (half-width 0.009613)
    status=converged after 3 look(s): CI half-width 0.0096128 <= target 0.01 at pre-declared look 3
  scenario='p1=0.65' seed=8541367147625302243 n_per_trial=100 critical=59
    [EXACT]     power reference      = 0.912322
    [EMPIRICAL] rejections/trials    = 9150/10000 = 0.915000
    [EMPIRICAL] 99.1% Clopper-Pearson CI = [0.907502, 0.922093] (half-width 0.007296)
    status=converged after 2 look(s): CI half-width 0.00729567 <= target 0.01 at pre-declared look 2

== 3. Reproducibility: same base seed, fresh streams ==
  run A rejections=9302, run B rejections=9302 -> identical: True

== 4. Budget interruption keeps the pending range ==
  scenario='p1=0.55/tiny-budget' seed=9551349652619906393 n_per_trial=100 critical=59
    [EXACT]     power reference      = 0.241491
    [EMPIRICAL] rejections/trials    = 42/200 = 0.210000
    [EMPIRICAL] 95.0% Clopper-Pearson CI = [0.155740, 0.273069] (half-width 0.058664)
    status=budget_exhausted after 1 look(s): trial budget exhausted before target precision was reached
    [PENDING] budget interrupted; CI still 0.058564 wider than target -- undecided range kept, not declared converged

== 5. Minimum n for target power (adjacent-point evidence) ==
  n_min=158 (critical=90, actual size=0.0472)
  EXACT power(157) = 0.778689 < target 0.8 <= EXACT power(158) = 0.805655
```

## Test result (`python3.11 -m unittest discover -s tests`)

```
Ran 38 tests in 6.792s

OK
```
