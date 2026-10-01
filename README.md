# permtest

Two-sample statistics and permutation calibration for discrete samples.
Python 3.11, standard library only, tested with `unittest`.

## Features

- **Statistics**: `frequencies`, `ecdf_points`, `total_variation`,
  `ks_statistic` (max ECDF gap). ECDF ties jump simultaneously by full
  multiplicity; optional per-observation weights; every observation keeps
  its own identity even when values repeat.
- **Calibration without normal approximation**:
  - *Exact*: full enumeration of the permutation reference for small
    designs (`method="exact"`).
  - *Monte Carlo*: caller-injected random source for large designs, with
    an exact Clopper-Pearson interval for the exceedance probability
    (`method="monte_carlo"`).
- **Designs**: grouped (label permutation) and paired (within-pair sign
  flips). Exchangeability is asserted by the caller via
  `exchangeable=True`; otherwise calibration is refused with
  `ExchangeabilityError` and no p-value is produced.
- **Rank-sharded enumeration**: combinations are enumerated by
  lexicographic rank ranges (`num_shards`), so parallel shards cover each
  assignment exactly once — no duplicates, no omissions (verified in
  tests against `itertools.combinations` and against the full
  label-permutation reference).
- **Determinism**: pooled values are canonically sorted, so reordering
  input labels never changes results; `seed` and sample `sizes` are
  recorded in every `CalibrationResult`.
- **Validation**: empty samples raise `ValueError`.

## Usage

```python
from permtest import grouped_calibration, paired_calibration, ks_statistic

ks_statistic([1, 1, 2], [1, 2, 2])            # 0.333...

res = grouped_calibration([1, 2], [3, 4], "ks")          # exact, p = 2/6
res = grouped_calibration(big_a, big_b, "ks", seed=42)   # Monte Carlo + CI
res = paired_calibration(before, after, "mean_diff")     # sign-flip exact
```

## Run

```sh
python3.11 examples/run_examples.py   # runnable examples
python3.11 -m unittest discover -s tests -v   # test suite
```

## Layout

- `permtest/statistics.py` — frequencies, ECDF, TV, KS.
- `permtest/combinatorics.py` — rank/unrank and sharded combination
  enumeration.
- `permtest/calibration.py` — grouped/paired calibration, exact and
  Monte Carlo, Clopper-Pearson interval.
- `examples/run_examples.py` — runnable demo.
- `tests/` — `unittest` suite.
