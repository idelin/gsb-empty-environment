"""Runnable demo of sptrisolve (Python 3.11, standard library only).

Run from the repository root:

    python3.11 examples/demo.py
"""

import os
import random
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), os.pardir))

from sptrisolve import TriangularMatrix, ZeroPivotError
from sptrisolve import dense


def show(title):
    print(f"\n=== {title} ===")


def main():
    rng = random.Random(42)

    show("1. Build a lower-triangular matrix (explicit n and COO indices)")
    n = 6
    rows = [0, 1, 2, 3, 4, 5, 1, 2, 3, 3, 4, 5, 5]
    cols = [0, 1, 2, 3, 4, 5, 0, 1, 0, 2, 3, 0, 2]
    vals = [2, 1, 3, 1, 2, 4, 1, -1, 2, 1, 3, -2, 1]
    m = TriangularMatrix.from_coo(n, rows, cols, vals, lower=True)
    print(f"n={n}, lower=True, nnz={len(rows)}")

    show("2. Dependency analysis: levels, parallel widths, evidence")
    ana = m.analysis()
    for depth, level in enumerate(ana.levels):
        print(f"level {depth}: nodes {level}  "
              f"(parallel width {len(level)})")
    print("dependency evidence (node <- unknowns it needs):")
    for node, deps in ana.dependency_evidence().items():
        print(f"  x[{node}] <- {deps if deps else '[] (independent)'}")

    show("3. Solve one right-hand side, verify against dense reference")
    b = [1.0, 2.0, 3.0, 4.0, 5.0, 6.0]
    x = m.solve(b)
    a = dense.dense_from_coo(n, rows, cols, vals)
    ref = dense.dense_solve_lower(a, b)
    print("x      =", [round(v, 6) for v in x])
    print("dense  =", [round(v, 6) for v in ref])
    print("residual A*x - b =",
          [round(v, 12) for v in (ai - bi for ai, bi in
                                  zip(dense.matvec(a, x), b))])

    show("4. Multiple right-hand sides share one compiled analysis")
    rhs = [[rng.uniform(-5, 5) for _ in range(n)] for _ in range(5)]
    xs = m.solve_many(rhs)
    ok = all(
        max(abs(got - want) for got, want in zip(xi, dense.dense_solve_lower(a, bi))) < 1e-9
        for xi, bi in zip(xs, rhs))
    print(f"solved {len(rhs)} RHS with one analysis, all match dense: {ok}")

    show("5. Transpose view: lower matrix solved as upper")
    xt = m.T.solve(b)
    a_t = dense.transpose(a)
    ref_t = dense.dense_solve_upper(a_t, b)
    print("A.T solve matches dense upper solve:",
          all(abs(p - q) < 1e-9 for p, q in zip(xt, ref_t)))
    print(f"view: lower={m.T.lower}, unit_diagonal={m.T.unit_diagonal}")

    show("6. Structural change -> partial (local) re-analysis")
    stats0 = m.stats()
    m.insert(4, 0, 1.5)
    stats1 = m.stats()
    delta = stats1["nodes_recomputed"] - stats0["nodes_recomputed"]
    print(f"nodes re-analysed after insert(4,0): {delta} of {2 * n} "
          f"(both orientations; only node 4 per orientation)")
    print("affected nodes:", m.analysis().last_recomputed)
    x2 = m.solve(b)
    a2 = dense.dense_from_coo(n, rows + [4], cols + [0], vals + [1.5])
    print("result after insert matches dense:",
          all(abs(p - q) < 1e-9
              for p, q in zip(x2, dense.dense_solve_lower(a2, b))))
    m.remove(4, 0)
    print("entry removed again; structure version:", m.stats()["structure_version"])

    show("7. Zero pivot: numerical failure, no partial solution published")
    bad = TriangularMatrix.from_coo(3, [0, 1, 2, 2], [0, 1, 2, 0],
                                    [1.0, 1.0, 0.0, 7.0], lower=True)
    try:
        bad.solve([1.0, 2.0, 3.0])
    except ZeroPivotError as exc:
        print(f"raised ZeroPivotError: {exc} (no half-solution returned)")

    show("8. Analysis reuse statistics")
    for key, value in m.stats().items():
        print(f"  {key}: {value}")


if __name__ == "__main__":
    main()
