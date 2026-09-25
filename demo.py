"""Runnable demonstration for the dependency-scheduled sparse triangular solver.

Run with:

    python3.11 demo.py

It shows, in order:
  1. a hand-built lower matrix with per-level parallel nodes and the
     dependency evidence behind every edge;
  2. a transposed solve handled as an upper-triangular view;
  3. many right-hand sides sharing one symbolic analysis and the resulting
     analysis-reuse rate;
  4. value-only update (analysis reused) vs structure change (local
     invalidation + re-analysis);
  5. explicit rejection of missing pivots, out-of-triangle entries,
     duplicate coordinates merged up front, and zero pivots (no half answer);
  6. a randomized cross-check against the built-in dense reference with
     permuted storage order, for lower/upper x unit/non-unit x transpose.
"""

import random

from trisolve import (
    LOWER,
    UPPER,
    MissingPivotError,
    StructureError,
    TriangularMatrix,
    TriangularSolver,
    ZeroPivotError,
)
from trisolve.dense import dense_solve, residual_norm


HAND = [
    (0, 0, 2.0),
    (1, 0, 1.0), (1, 1, 4.0),
    (2, 1, 2.0), (2, 2, -1.0),
    (3, 0, -1.0), (3, 2, 3.0), (3, 3, 5.0),
]


def make(entries, n=4, **kw):
    return TriangularMatrix(
        n, [e[0] for e in entries], [e[1] for e in entries], [e[2] for e in
        entries], **kw
    )


def hr(title):
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


def show_schedule(solver, transpose=False):
    sched = solver.analyze(transpose=transpose)
    tag = "L^T (upper view)" if transpose else "L (lower)"
    print(f"schedule {tag}: {sched.summary()}")
    for lvl, nodes in enumerate(sched.levels):
        print(f"  level {lvl}: parallel nodes {nodes}")
        for node in nodes:
            evidence = sched.dependency_evidence(node)
            if evidence:
                edges = ", ".join(
                    f"x_{d.depends_on}*A{d.coefficient_position}" for d in evidence
                )
                print(f"      node {node} depends on {edges}")


def section_hand_and_transpose():
    hr("1. Dependency levels + evidence, and a transposed (upper-view) solve")
    m = make(HAND)
    solver = TriangularSolver(m)
    show_schedule(solver, transpose=False)
    b = [2.0, 5.0, 0.0, 8.0]
    x = solver.solve(b)
    print(f"  L x = b, b = {b}")
    print(f"  x         = {[round(v, 10) for v in x]}")
    print(f"  max |Lx-b| = {residual_norm(m, x, b):.3e}")

    show_schedule(solver, transpose=True)
    xt = solver.solve(b, transpose=True)
    print(f"  L^T x = b")
    print(f"  x         = {[round(v, 10) for v in xt]}")
    print(f"  max |L^T x - b| = {residual_norm(m, xt, b, transpose=True):.3e}")


def section_multi_rhs():
    hr("2. Multiple right-hand sides share one symbolic analysis")
    m = make(HAND)
    solver = TriangularSolver(m)
    rhs = [[1.0, 0.0, 0.0, 0.0], [0.0, 1.0, 0.0, 0.0],
           [0.0, 0.0, 1.0, 0.0], [0.0, 0.0, 0.0, 1.0]]
    X = solver.solve_multi(rhs)
    for b, x in zip(rhs, X):
        print(f"  b={b} -> x={[round(v, 6) for v in x]} "
              f"residual={residual_norm(m, x, b):.1e}")
    print("  stats:", solver.stats.as_dict())


def section_reuse_and_invalidation():
    hr("3. Value update reuses analysis; structure change locally invalidates")
    m = make(HAND)
    solver = TriangularSolver(m)
    solver.solve([1.0, 2.0, 3.0, 4.0])
    token_before = m.structure_token

    m.set_value(3, 2, 9.0)  # numeric-only
    solver.solve([4.0, 3.0, 2.0, 1.0])
    print("  after set_value(3,2,9.0): token unchanged =",
          m.structure_token == token_before)
    print("  stats:", solver.stats.as_dict())

    m.add_entry(2, 0, 0.5)  # new dependency edge
    b = [1.0, 1.0, 1.0, 1.0]
    x = solver.solve(b)
    print("  after add_entry(2,0,0.5): token changed =",
          m.structure_token != token_before)
    print("  new level 2 dependencies:",
          [d.depends_on for d in solver.analyze().dependency_evidence(2)])
    print(f"  solve still correct, max|Ax-b| = {residual_norm(m, x, b):.1e}")
    print("  stats:", solver.stats.as_dict())


def section_rejections():
    hr("4. Explicit rejection (missing pivot vs zero pivot; no half answer)")
    try:
        make([(0, 0, 1.0), (1, 0, 1.0), (2, 2, 1.0)], n=3)
    except MissingPivotError as exc:
        print("  missing pivot ->", type(exc).__name__, exc)

    try:
        make([(0, 0, 1.0), (0, 1, 1.0), (1, 1, 1.0)], uplo=LOWER)
    except StructureError as exc:
        print("  non-triangular ->", type(exc).__name__, str(exc)[:80], "...")

    merged = make([
        (0, 0, 1.0), (0, 0, 2.0),
        (1, 0, 0.5), (1, 0, 0.25), (1, 1, 3.0),
    ], n=2)
    print("  duplicate coords merged -> A00 =", merged.value(0, 0),
          " A10 =", merged.value(1, 0))

    bad = make([(0, 0, 2.0), (1, 0, 1.0), (1, 1, 0.0),
                (2, 1, 1.0), (2, 2, 4.0)], n=3)
    rhs = [8.0, 3.0, 7.0]
    snapshot = list(rhs)
    try:
        TriangularSolver(bad).solve_into(rhs)
    except ZeroPivotError as exc:
        print("  zero pivot ->", type(exc).__name__, exc)
    print("  rhs untouched after failed solve:", rhs == snapshot)


def section_random():
    hr("5. Randomized cross-check vs dense reference (permuted storage order)")
    rng = random.Random(20260925)
    worst = 0.0
    cases = 0
    for n in (1, 3, 6, 10):
        for uplo in (LOWER, UPPER):
            for unit in (False, True):
                for transpose in (False, True):
                    entries = []
                    for i in range(n):
                        if not unit:
                            entries.append(
                                (i, i, rng.choice((-1.0, 1.0)) * rng.uniform(0.5, 5.0))
                            )
                        for j in range(i):
                            if rng.random() < 0.45:
                                r, c = (i, j) if uplo == LOWER else (j, i)
                                entries.append((r, c, rng.uniform(-3.0, 3.0)))
                    rng.shuffle(entries)
                    m = make(entries, n=n, uplo=uplo, unit_diagonal=unit)
                    b = [rng.uniform(-5.0, 5.0) for _ in range(n)]
                    got = TriangularSolver(m).solve(b, transpose=transpose)
                    want = dense_solve(m, b, transpose=transpose)
                    err = max(abs(a - z) for a, z in zip(got, want))
                    res = residual_norm(m, got, b, transpose=transpose)
                    worst = max(worst, err, res)
                    cases += 1
    print(f"  {cases} configurations (n in 1,3,6,10 x L/U x unit/non x /T)")
    print(f"  worst sparse-vs-dense error and residual: {worst:.3e}")
    print("  (storage order was shuffled for every matrix)")


if __name__ == "__main__":
    section_hand_and_transpose()
    section_multi_rhs()
    section_reuse_and_invalidation()
    section_rejections()
    section_random()
    print("\nAll demonstration sections completed.")
