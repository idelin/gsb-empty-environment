# timefit — exact piecewise-affine clock mapping

Fits a piecewise-affine mapping from a **local** clock to a **reference**
clock based on anchors with error intervals, under a maximum drift-slope
constraint. Outlier rejection minimises the *number of removed anchors*
(exact search), not an arbitrary residual threshold. Query answers are exact
bounds over **all** feasible mappings. Python 3.11, standard library only
(`fractions.Fraction` everywhere — no floats in the time representation).

## Concepts

- **Time types** (`timefit/timebase.py`): `WallClock` (calendar reading,
  UTC, microsecond-exact), `Instant` (point on the timeline), `Duration`
  (length of time). Mixing them wrongly raises `TimeTypeError`; all
  arithmetic is exact rational arithmetic.
- **Anchors** (`Anchor`): at `local`, the reference clock read
  `ref ± error`. Two anchors at the *same local time* constrain the same
  knot variable, so disjoint error intervals there are a direct conflict.
- **Mapping**: knots at the distinct local times of the kept anchors;
  affine per segment; **C0 continuity is declared and enforced explicitly**
  (adjacent segments share the knot variable); each segment slope lies in
  `[1 - max_drift, 1 + max_drift]`.

## Algorithms

- **Feasibility** (`timefit/diffconstraints.py`): the constraints are
  difference constraints `x_v - x_u <= c`; feasibility is negative-cycle
  detection (Bellman-Ford) with exact `Fraction` arithmetic.
- **Outlier rejection** (`PiecewiseAffineFitter.solve`): exact search over
  removal cardinalities `k = 0, 1, 2, ...` — the first feasible `k` is the
  provable minimum. Returns conflict cores (minimal infeasible anchor
  subsets) and an optimal witness mapping. With `budget=B`, infeasibility
  within the budget is reported separately as `Status.BUDGET_UNDECIDED`
  (the exact optimum is *not* claimed in that case).
- **Query bounds** (`PiecewiseAffineFitter.query`): the projection of a
  difference-constraint polytope onto the two knot variables of the query
  segment is computed exactly via shortest paths; the min/max of the
  interpolation objective over that 2-D polygon gives the exact range over
  all feasible mappings. Extrapolation past the first/last knot is allowed
  only up to `max_extrapolation`, otherwise `ExtrapolationError`.
- **Cross-check** (`timefit/verify.py`): independent rational vertex
  enumeration (exact Gaussian elimination over all `n`-subsets of active
  constraints); the test suite checks feasibility and query bounds of the
  fast machinery against it on random instances.

## Layout

- `timefit/timebase.py` — `WallClock` / `Instant` / `Duration`, exact arithmetic
- `timefit/diffconstraints.py` — exact difference-constraint solver
- `timefit/piecewise.py` — fitter: feasibility, min-removal search, cores, queries
- `timefit/verify.py` — rational vertex-enumeration cross-check
- `timefit/synth.py` — synthetic offset conversion table (deterministic)
- `timefit/cli.py` — demo (`python -m timefit`)
- `tests/` — unittest suite (42 tests)

## Run

```console
$ python3 -m unittest discover -s tests
..........................................
----------------------------------------------------------------------
Ran 42 tests in 3.202s

OK
```

```console
$ python3 -m timefit
== synthetic offset conversion table ==
  true offset: 5 s base, +2 ms/s drift up to t=300 s, then -1 ms/s
  noise: uniform within +/-50 ms (anchor error interval)
  injected outliers: #3 shifted +2 s, #8 shifted -1.5 s
  idx          local (wall clock)                measured ref       err
    0   2026-01-01T00:00:00+00:00 2026-01-01T00:00:04.991000+00:00 50.000 ms
    ...
   11   2026-01-01T00:11:00+00:00 2026-01-01T00:11:05.254000+00:00 50.000 ms

== exact minimum-removal fit (max drift 5 ms/s) ==
  status:        exact
  min removals:  2
  removed:       [3, 8]
  conflict cores: [[0, 3], [1, 3], [2, 3], [3, 4], ..., [8, 10], [8, 11]]
  witness (local s -> ref s, exact):
       0 -> 5041/1000
      60 -> 65139/1000
      ...

== corrected queries (bounds over ALL feasible mappings) ==
  local 2026-01-01T00:01:30+00:00 [interpolation]
    ref in [2026-01-01T00:01:35.114500+00:00, 2026-01-01T00:01:35.214500+00:00]
    corrected 2026-01-01T00:01:35.164500+00:00  +/- 50.000 ms
  ...

== extrapolation policy (allowed: 30 s past the last knot) ==
  last knot +20 s: accepted [extrapolation], ref in [2055509/3000 s (exact, ...), ...]
  last knot +45 s: rejected (query is 45s beyond the mapped range, allowed is 30s)

== removal budget too small (budget = 1) ==
  status: budget_undecided  (cannot fit within budget; exact optimum reported separately)
```

## Usage

```python
from fractions import Fraction
from timefit import Anchor, Duration, Instant, PiecewiseAffineFitter

anchors = [
    Anchor(Instant(0),   Instant(5),  Duration(milliseconds=100)),
    Anchor(Instant(60),  Instant(65), Duration(milliseconds=100)),
    Anchor(Instant(120), Instant(999), Duration(milliseconds=100)),  # outlier
]
fitter = PiecewiseAffineFitter(anchors, max_drift=Fraction(5, 1000),
                               max_extrapolation=Duration(30))
result = fitter.solve()                 # exact minimum-removal fit
assert result.removed == (2,)
qr = fitter.query(Instant(30), removed=result.removed)
qr.corrected, qr.uncertainty            # midpoint and width of feasible range
```
