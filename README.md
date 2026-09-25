# Pairwise-Complete Covariance over Sparse Observations

Pure-stdlib Python 3.11+ implementation of covariance with **pairwise
deletion** over sparse data, with mergeable shard statistics and batch
retraction. No NumPy/SciPy.

## Files

- `pairwise_covariance.py` — library (`PairwiseCovariance`, `PairStats`,
  `merge_stats`, `subtract_stats`, `check_psd`)
- `test_pairwise_covariance.py` — 23 unittest cases
- `demo.py` — runnable end-to-end example

## Guarantees

- **Missing is not zero.** `None` marks a missing value; a pair `(i, j)`
  only uses rows where both variables are observed. NaN/inf are rejected.
- **Mergeable central moments.** Each pair keeps `(count, mean_x, mean_y,
  comoment)` updated with Welford/Chan formulas — never the naive
  `E[x²]−E[x]²` form, so a mean of `1e12` with variance `~9.17` stays exact.
- **Explicit denominator.** `ddof=1` (sample) by default, `ddof=0`
  (population) on request. A pair with `count <= ddof` is **undefined**
  (`None`) — a single common observation cannot define a sample covariance.
- **Batch retraction.** Every batch is stored as evidence under an explicit
  id. Re-adding an id is an idempotent no-op; removing an unknown id raises
  `KeyError`; counts can never go negative; retraction order does not matter.
- **Honest PSD diagnostics.** Pairwise deletion can yield a non-PSD matrix.
  `check_psd()` returns a counterexample vector `v` with `vᵀCv < 0`
  (self-written Jacobi eigensolver), or lists undefined pairs — the matrix
  is never silently repaired.
- **Explicit shape/indexing.** Dimension is a constructor argument; row
  width and variable indices are validated.

## Usage

```python
from pairwise_covariance import PairwiseCovariance

pc = PairwiseCovariance(3)                 # explicit dimension, ddof=1
pc.add_batch("shard-1", [[1.0, 10.0, None],
                         [2.0, None, 0.5],
                         [3.0, 30.0, 1.5]])
pc.add_batch("shard-2", [[4.0, 40.0, 2.5]])
pc.count(0, 1)          # 2  (rows where both are observed)
pc.covariance(0, 1)     # 20.0
pc.matrix()             # dense matrix, None for undefined pairs
pc.remove_batch("shard-2")                 # exact retraction
pc.check_psd()          # PSDDiagnostic: psd / not_psd / undefined_pairs
```

## Demo output (`python3 demo.py`)

```
=== 1. Sparse observations (None = missing) ===
pairwise counts:
   [5, 3, 4]
   [3, 4, 2]
   [4, 2, 4]
covariance matrix (None = undefined pair):
   [3.7, 23.3333, 2.1667]
   [23.3333, 291.6667, 5.0]
   [2.1667, 5.0, 1.6667]

=== 2. Sharded merge equals direct computation ===
sharded == direct: True

=== 3. Batch retraction / idempotent ids ===
re-adding 'shard-1' applied? False
counts after retracting shard-2: [[3, 2, 2], [2, 2, 1], [2, 1, 2]]
removing unknown batch rejected: "unknown batch id: 'shard-2'"
counts after retracting everything: [[0, 0, 0], [0, 0, 0], [0, 0, 0]]

=== 4. Huge mean (1e12) + tiny variance ===
var(x) = 9.166666666666666 (exact: 165/18 = 9.166...)
cov(x,y) = -9.166666666666666

=== 5. Non-PSD pairwise covariance -> counterexample, no silent repair ===
status: not_psd
message: min eigenvalue -1.2 < 0; v^T C v = -1.2 for the returned vector
counterexample v = [0.5774, -0.5774, -0.5774]
v^T C v = -1.2 < 0

=== 6. Undefined pairs reported as diagnostic ===
status: undefined_pairs | undefined pairs: [(0, 1), (1, 1)]
count(0,1) = 1 -> sample covariance: None | population (ddof=0): 0.0
```

## Test results (`python3 -m unittest test_pairwise_covariance -v`)

```
Ran 23 tests in 0.002s
OK
```

Coverage: missing-never-zero, per-pair counts, NaN/inf rejection,
sample-vs-population denominators, single-observation undefined, sharded
merge == direct (plus exact `Fraction` reference), retraction restores
direct computation, retraction order independence, idempotent batch ids,
unknown-batch `KeyError`, non-negative counts, huge-mean/tiny-variance
stability (and a demonstration that the naive raw-moment formula fails),
non-PSD counterexample vector, PSD pass, undefined-pair diagnostics,
explicit shape/index validation.
