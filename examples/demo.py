"""Runnable demos for the sparse QR least-squares solver.

Run from the repository root:  python3.11 examples/demo.py
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from sparseqr import SparseMatrix, qr_least_squares


def show(title, res):
    print(f"--- {title} ---")
    print("  x (original order) :", [round(v, 6) for v in res.x])
    print("  rank               :", res.rank)
    print("  rank deficient     :", res.rank_deficient)
    print("  dependent columns  :", res.dependent_columns)
    print("  column permutation :", res.column_permutation)
    print("  residual norm      : %.6e" % res.residual_norm)
    print()


def demo_overdetermined():
    # Least-squares line fit y = c0 + c1*t to noisy measurements.
    ts = [0.0, 1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0]
    y = [1.1, 2.9, 5.2, 6.8, 9.3, 10.7, 13.4, 14.6]
    entries = [(i, 0, 1.0) for i in range(len(ts))]
    entries += [(i, 1, t) for i, t in enumerate(ts)]
    a = SparseMatrix(len(ts), 2, entries)
    show("Overdetermined line fit (8 points, 2 unknowns)",
         qr_least_squares(a, y))


def demo_duplicate_columns():
    # Column 2 duplicates column 0 -> rank deficient, must be reported.
    entries = [
        (0, 0, 1.0), (1, 0, 2.0), (2, 0, 3.0), (3, 0, 4.0),
        (0, 1, 2.0), (1, 1, 0.0), (2, 1, 1.0), (3, 1, -1.0),
        (0, 2, 1.0), (1, 2, 2.0), (2, 2, 3.0), (3, 2, 4.0),
    ]
    a = SparseMatrix(4, 3, entries)
    show("Duplicate column (col 2 == col 0)",
         qr_least_squares(a, [1.0, 2.0, 3.0, 4.0]))


def demo_permutation():
    # Tiny first column forces column pivoting; x is returned in the
    # original variable order regardless.
    entries = [
        (0, 0, 1e-3),
        (0, 1, 1.0), (1, 1, 1.0),
        (1, 2, 1.0), (2, 2, 1.0),
    ]
    a = SparseMatrix(3, 3, entries)
    b = [1.0, 2.0, 3.0]
    res = qr_least_squares(a, b)
    show("Column pivoting with order restoration", res)
    print("  check A x          :",
          [round(v, 6) for v in a.matvec(res.x)], "(b =", b, ")")
    print()


def demo_sparse_random():
    import random
    rng = random.Random(42)
    m, n = 200, 60
    entries = []
    for j in range(n):
        for _ in range(8):  # ~8 nonzeros per column
            entries.append((rng.randrange(m), j, rng.uniform(-1, 1)))
    x_true = [rng.uniform(-1, 1) for _ in range(n)]
    a = SparseMatrix(m, n, entries)
    b = a.matvec(x_true)
    res = qr_least_squares(a, b)
    show(f"Sparse random {m}x{n} (nnz={a.nnz}), consistent rhs", res)


if __name__ == "__main__":
    demo_overdetermined()
    demo_duplicate_columns()
    demo_permutation()
    demo_sparse_random()
