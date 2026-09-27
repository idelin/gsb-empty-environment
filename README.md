# binpower

Explicit binomial-model power analysis: **exact** tail-probability references
plus **labeled** Monte Carlo estimates. Python 3.11, standard library +
`unittest` only.

## Conventions (enforced by the API)

- `kind="exact"` — computed by binomial pmf/tail recursion, never simulated.
- `kind="empirical"` — Monte Carlo estimate, **always** paired with a
  Clopper-Pearson confidence interval, the stream seed and the trial count.
  A point rejection rate is never presented as exact power.
- The test rule (critical value / spending boundaries) is **fixed** before
  any simulation; only the Monte Carlo budget allocation may adapt.
- Zero-trial estimates are refused (`ValueError`); an all-rejection run
  still yields a non-degenerate interval.
- Randomness is injected by the caller via seeded streams; each scenario
  gets an independent stream derived from `(master_seed, label)`, so any
  scenario is reproducible standalone or in any ensemble.
- Budget interruption keeps the pending (undecided) range as a wide
  interval — it is never collapsed to a point.

## Layout

- `binpower/exact.py` — pmf table (mode-anchored recursion, no underflow),
  exact tail probabilities, critical values, exact size/power.
- `binpower/interval.py` — Clopper-Pearson interval (incomplete beta via
  continued fraction + bisection).
- `binpower/simulate.py` — seeded streams, rejection-rate simulation with
  CI/seed/trial-count recording, scenario ensembles.
- `binpower/design.py` — minimum sample size for a target power by exact
  linear scan, returned with adjacent-point evidence
  (`power_below < target <= power`).
- `binpower/spending.py` — pre-declared error spending (O'Brien-Fleming,
  power family), exact boundary recursion, exact design power, simulation
  with recorded stopping conditions.
- `binpower/adaptive.py` — adaptive allocation of simulation budget to the
  widest CI; interruption preserves pending ranges.

## Run

```sh
python3.11 -m unittest discover -s tests -v   # 62 tests
python3.11 examples/run_examples.py           # end-to-end demo
```

## Sample output (abridged)

```
1) EXACT power reference (tail-probability recursion, kind=exact)
   fixed rule: reject H0: p<=0.5 iff X >= 90 out of n=158
   [exact] size = 0.047237 (<= alpha=0.05), power at p1=0.6 = 0.805655
2) EMPIRICAL rejection rates (kind=empirical) with Clopper-Pearson CIs
   alt  p=0.60: rate=0.8097 CI95=[0.7972, 0.8218] (seed=..., trials=4000) [exact ref=0.8057]
3) Minimum n for target power 0.80 (EXACT scan, adjacent evidence)
   [exact] n=158: c=90, power=0.805655 >= 0.80
   [exact] n=157: c=90, power=0.778689 <  0.80   (adjacent-point evidence)
4) Group-sequential design, pre-declared O'Brien-Fleming spending
   look 2 (n=106): reject if S>=65  budget=0.01672 spent=0.01265
   [exact] total size = 0.03766 <= 0.05
   stopping conditions: 101x reject at look 1, 1607x reject at look 2, ...
5) Adaptive budget allocation (test rule stays fixed)
   tight budget -> status=interrupted, pending=['null', 'alt']
     alt: UNDECIDED range CI95=[0.7761, 0.8541] after 400 trials
```
