"""Runnable walk-through of the transactional sparse slice assignment."""

from sparse_assign import (
    DenseReference,
    IndexBoundaryError,
    MemoryQuota,
    QuotaExceededError,
    ReadOnlyError,
    SparseMatrix,
    WITHDRAW,
)


def show(title, matrix):
    print(f"-- {title}")
    for row in matrix.to_dense():
        print("   ", row)


print("== 1. non-contiguous fancy selectors; repeated targets, last wins ==")
m = SparseMatrix(2, 4, [(0, 0, 1), (1, 3, 9)])
stats = m.assign([0, 1], [2, 2], 7)
print("   paired fancy ->", m.to_dense(), "stats:", stats)
stats = m.assign([0], [0, 0, 1], [10, 20, 30])
print("   repeated last-wins ->", m.to_dense(), "repeated:",
      stats.repeated_targets)

print("\n== 2. overlapping RHS shares storage: pre-assignment snapshot ==")
m = SparseMatrix(1, 5, [(0, 0, 1), (0, 1, 2), (0, 2, 3), (0, 3, 4),
                        (0, 4, 5)])
m[0:1, 1:5] = m.select(slice(0, 1), slice(0, 4))
show("right shift reads the pre-state", m)

print("\n== 3. broadcast + explicit zero vs. withdraw ==")
m = SparseMatrix(3, 3)
m[::, ::] = [[1], [2], [3]]
m[0, 0] = 0          # explicit zero is stored
m[1, 1] = WITHDRAW   # structural removal
print("   stored entries:", m.stored_entries, "(9 grid cells, 1 withdrawn)")
show("broadcast rows", m)

print("\n== 4. memory quota refuses before any partial write ==")
m = SparseMatrix(4, 4, [(r, r, 1) for r in range(4)])
quota = MemoryQuota(2)
before = m.to_dense()
try:
    m.assign([0, 1, 2, 3], 0, 8, quota=quota)
except QuotaExceededError as exc:
    print("   rejected:", exc)
print("   untouched:", m.to_dense() == before, "| quota.used:", quota.used)
stats = m.assign([0, 1], 0, 8, quota=quota)
print("   accepted, row blocks actually copied:", stats.row_blocks_copied,
      "| quota.used:", quota.used)

print("\n== 5. out-of-range rejected before mutation ==")
try:
    m.assign(4, 0, 1)
except IndexBoundaryError as exc:
    print("   rejected:", exc)

print("\n== 6. history snapshot stays read-only and immutable ==")
m = SparseMatrix(2, 2, [(0, 0, 1)])
old = m.snapshot()
m[:, :] = 9
print("   current:", m.to_dense())
print("   history:", old.to_dense(), "version:", old.version)
try:
    old[0, 0] = 2
except ReadOnlyError as exc:
    print("   history rejects writes:", exc)

print("\n== 7. dense snapshot reference agrees on a random overlap ==")
import random

rng = random.Random(7)
s = SparseMatrix(4, 4, [(r, 0, r) for r in range(4)])
d = DenseReference(4, 4, [(r, 0, r) for r in range(4)])
for _ in range(20):
    rows = [rng.randrange(4) for _ in range(rng.randrange(1, 4))]
    cols = [rng.randrange(4) for _ in range(len(rows))]
    value = rng.choice([0, 1, 6, WITHDRAW])
    s.assign(rows, cols, value)
    d.assign(rows, cols, value)
print("   sparse == dense:", s.to_dense() == d.to_dense())
show("final state", s)
