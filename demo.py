"""Runnable demo: pairwise covariance with missing values, shards, retraction."""

from pairwise_covariance import PairwiseCovariance, quadratic_form


def show(title):
    print(f"\n=== {title} ===")


# --- 1. Sparse observations: None is missing, never zero ---------------------
show("1. Sparse observations (None = missing)")
rows_shard1 = [
    [1.0, 10.0, None],
    [2.0, None, 0.5],
    [3.0, 30.0, 1.5],
]
rows_shard2 = [
    [4.0, 40.0, 2.5],
    [None, 50.0, None],
    [6.0, None, 3.5],
]
pc = PairwiseCovariance(3)  # explicit dimension, ddof=1 (sample covariance)
pc.add_batch("shard-1", rows_shard1)
pc.add_batch("shard-2", rows_shard2)
print("pairwise counts:")
for i in range(3):
    print("  ", [pc.count(i, j) for j in range(3)])
print("covariance matrix (None = undefined pair):")
for row in pc.matrix():
    print("  ", [None if v is None else round(v, 4) for v in row])

# --- 2. Sharded merge == direct computation ----------------------------------
show("2. Sharded merge equals direct computation")
direct = PairwiseCovariance(3)
direct.add_batch("all", rows_shard1 + rows_shard2)
same = all(
    (pc.covariance(i, j) is None) == (direct.covariance(i, j) is None)
    and (pc.covariance(i, j) is None
         or abs(pc.covariance(i, j) - direct.covariance(i, j)) < 1e-12)
    for i in range(3) for j in range(3)
)
print("sharded == direct:", same)

# --- 3. Batch retraction with idempotent ids ---------------------------------
show("3. Batch retraction / idempotent ids")
print("re-adding 'shard-1' applied?", pc.add_batch("shard-1", rows_shard1))
pc.remove_batch("shard-2")
print("counts after retracting shard-2:",
      [[pc.count(i, j) for j in range(3)] for i in range(3)])
try:
    pc.remove_batch("shard-2")
except KeyError as exc:
    print("removing unknown batch rejected:", exc)
pc.remove_batch("shard-1")
print("counts after retracting everything:",
      [[pc.count(i, j) for j in range(3)] for i in range(3)])

# --- 4. Huge mean, tiny variance (no naive sum-of-squares) -------------------
show("4. Huge mean (1e12) + tiny variance")
big = PairwiseCovariance(2)
big.add_batch("big", [[1e12 + k, 1e12 - k] for k in range(10)])
print("var(x) =", big.covariance(0, 0), "(exact: 165/18 = 9.166...)")
print("cov(x,y) =", big.covariance(0, 1))

# --- 5. Pairwise missingness can break PSD ------------------------------------
show("5. Non-PSD pairwise covariance -> counterexample, no silent repair")
weird = PairwiseCovariance(3)
weird.add_batch("w", [
    [0.0, 0.0, None], [1.0, 1.0, None], [2.0, 2.0, None],   # corr(x,y)=+1
    [0.0, None, 0.0], [1.0, None, 1.0], [2.0, None, 2.0],   # corr(x,z)=+1
    [None, 0.0, 2.0], [None, 1.0, 1.0], [None, 2.0, 0.0],   # corr(y,z)=-1
])
diag = weird.check_psd()
print("status:", diag.status)
print("message:", diag.message)
v = diag.counterexample
print("counterexample v =", [round(t, 4) for t in v])
print("v^T C v =", round(quadratic_form(weird.matrix(), v), 6), "< 0")

# --- 6. Undefined pairs are reported, not hidden ------------------------------
show("6. Undefined pairs reported as diagnostic")
sparse = PairwiseCovariance(2)
sparse.add_batch("s", [[1.0, None], [2.0, None]])
diag = sparse.check_psd()
print("status:", diag.status, "| undefined pairs:", diag.undefined_pairs)
one = PairwiseCovariance(2)
one.add_batch("o", [[1.0, 2.0], [3.0, None]])  # only one common observation
print("count(0,1) =", one.count(0, 1),
      "-> sample covariance:", one.covariance(0, 1),
      "| population (ddof=0):", one.covariance(0, 1, ddof=0))
