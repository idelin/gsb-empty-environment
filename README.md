# Pairwise-complete covariance over sparse observations

Pure Python 3.11 standard library (no NumPy/SciPy).

## Files
- `pairwise_cov.py` — core library
  - `PairMoment`: per-pair mergeable Welford/Chan central moment (`n`,
    pairwise means, co-moment `C`); `merged` / `without` (un-merge).
  - `ShardAccumulator`: explicit `dim`; `None`/`NaN` are missing and never
    coerced to zero; per-pair counts `n_ij`; covariance = `C/(n-ddof)`,
    NaN when `n-ddof <= 0` (e.g. one common observation -> undefined).
  - `CovarianceLedger`: named batches; idempotent re-add (digest-checked),
    conflicting payloads raise, retracting unknown ids raises
    (`UnknownBatchError`); retraction keeps audit evidence and never makes
    counts negative; `verify()` cross-checks forward/reverse merges.
  - `dense_pairwise_covariance`: self-written dense two-pass reference.
  - `psd_check` / `_jacobi_eigen`: self-written dense Jacobi eigensolver;
    returns a witness vector with `v^T S v < 0` when the pairwise-complete
    matrix is non-PSD (it never alters the matrix); NaN entries reported
    as indeterminate with the undefined pair list.
- `test_pairwise_cov.py` — 22 unittest cases.
- `demo.py` — runnable example.

## Run
```
python3.11 -m unittest discover -v
python3.11 demo.py
```

## Key properties covered by tests
- Missing values counted pairwise, never as zero; `NaN` missing; types checked.
- Shard merge equals direct computation; merge order cross-checked.
- Huge mean / tiny variance: Welford stays exact where naive
  `sum(x^2)-n*mean^2` cancels (demo prints `0.0` vs the true `2.5`).
- One common observation => sample covariance undefined (NaN); ddof=0 works.
- Batch add idempotent; conflicting re-add and unknown-batch retraction raise.
- Retraction equals fresh recompute, is order-independent, counts stay >= 0.
- Non-PSD pairwise example: min eigenvalue -0.526453, witness vector
  reported, matrix left unchanged.
