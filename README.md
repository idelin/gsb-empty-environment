# Subset Exponential Mechanism (synthetic data only)

Python 3.11, standard library only (`math`, `random`, `decimal`, `bisect`,
`itertools`, `dataclasses`, `unittest`). No third-party dependencies.

## Definitions

- **Candidate**: `(cid, score, group)` — unique string id, finite **integer**
  score, group label. Duplicate ids are rejected with `ValueError`.
- **Constraints**: `(subset_size, group_quotas, mutex_pairs)`
  - `subset_size = k`: every legal subset contains **exactly** `k` candidates
    (the selection budget).
  - `group_quotas`: `group -> max members` allowed per subset.
  - `mutex_pairs`: pairs of candidate ids that may never co-occur.
- **Domain**: the set of *legal subsets* — all size-`k` combinations satisfying
  every quota and mutex rule. The distribution is defined **on legal subsets,
  not on individual items**.
- **Utility**: `u(S) = sum of member scores` (exact integer arithmetic).
- **Distribution** (exponential mechanism):

  ```
  P(S) = exp(epsilon * u(S) / (2 * sensitivity)) / Z
  Z    = sum over legal S' of exp(epsilon * u(S') / (2 * sensitivity))
  ```

- **Parameters**: `epsilon >= 0` (finite float), `sensitivity >= 0` (finite
  float). For the fixed-size sum utility, `max(score) - min(score)` is a valid
  global sensitivity; `utility_sensitivity_fixed_size()` computes it. Choosing a
  sensitivity below the true one invalidates any privacy interpretation.

## Explicit degenerate rules (no division by zero, ever)

- `sensitivity == 0` → **uniform** over legal subsets (scores carry no
  information by definition). This is a deterministic special case, not a
  numerical accident.
- `epsilon == 0` → uniform (falls out of the formula: all log-weights are 0).
- All scores equal → uniform (verified by test).
- `subset_size == 0` → single legal subset `()` with probability 1.

## Numerical policy and error

- Float64 path uses **log-sum-exp stabilisation** (subtract max log-weight
  before `exp`), so `epsilon * u / (2 * sensitivity)` of arbitrary magnitude
  (e.g. 5e5) cannot overflow. Verified with scores of ±10⁶ and ±10⁹.
- **Exact small-domain reference**: `probabilities_decimal(precision=50)`
  recomputes the distribution with `decimal.Decimal` at 50 significant digits
  (same stabilisation). `exp` is transcendental, so this reference is itself
  approximate, but its error is ~1e-50 — far below float64 rounding — making
  it a practical exact reference for small domains.
- `numerical_error()` reports the **explicit** gap between the float64 and
  Decimal results. On the demo domain: max abs error ≈ 3.5e-18, max rel error
  ≈ 1.6e-16 (i.e. float64 machine precision; nothing is hidden).

## Feasibility — no silent truncation

If the constraints admit **no** legal subset (budget larger than the candidate
pool, quotas too small for the budget, all candidates ruled out, mutex rules
blocking every combination), the constructor raises `InfeasibleError`. The
mechanism **never** drops candidates or shrinks `k` to regain feasibility and
then claims the original distribution. Domains larger than `max_enumerated`
(default 2,000,000 legal subsets) raise `OverflowError` rather than being
truncated.

## Auditable sampling

`sample(n, seed)` performs inverse-CDF sampling over the enumerated legal
subsets with a seeded `random.Random`. It returns a `SamplingEvidence`
recording the seed, the full normalised distribution used, every raw uniform
variate consumed, and the chosen subset per draw. `evidence.replay()`
re-runs the seed and verifies every draw bit-for-bit (tampering is detected
and tested).

## Scope and disclaimers

- **Synthetic data only.** No real personal data is used or expected.
- The frequency test and the demo's 20,000-draw check are **statistical
  sanity experiments, not a formal privacy proof**. Nothing here proves
  differential privacy of any deployment; that requires a sensitivity
  analysis of the real utility function and threat model.

## Files

- `expmech/model.py` — candidates, constraints, legal-subset enumeration.
- `expmech/mechanism.py` — stable probabilities, Decimal reference, error
  reporting, auditable sampling.
- `expmech/demo.py` — runnable synthetic example.
- `tests/test_mechanism.py` — 26 unittest cases.

## Run

```
python3.11 -m expmech.demo                  # sample run
python3.11 -m unittest discover -s tests -v # test suite
```

## Sample run (abridged)

```
epsilon           = 1.2
sensitivity       = 11.0  (max-min score range, fixed-size sum utility)
legal subsets     = 42
   0. {b1,c1,c3} u= 18  P=0.03496399  ref=0.03496399
  ...
  sum(P) = 0.9999999999999999
max abs error = 3.469e-18   max rel error = 1.621e-16
draw 0: u=0.906107814900 -> subset#38 {b1,c2,c3} (P=0.02140040)
replay verifies: True
max |empirical freq - exact P| = 0.0033   (20000 draws, NOT a privacy proof)
```

## Test result

```
Ran 26 tests in 0.139s
OK
```

Covers: uniform on equal scores, ε=0, sensitivity=0 explicit rule, duplicate
id rejection, non-integer score rejection, unknown mutex id, negative
parameters, all-candidates-illegal, budget-exceeds-pool, budget-exceeds-quota
(no truncation), fully-mutexed domain, quota+mutex domain shape, extreme-score
overflow safety (±10⁶, ±10⁹), float64 vs Decimal-50 reference error < 1e-12,
normalisation, monotonicity in utility, seed reproducibility, evidence replay
and tamper detection, legality of every drawn subset, and a fixed-seed
statistical frequency check (explicitly not a privacy proof).
