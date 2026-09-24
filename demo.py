"""Runnable sample for the incremental weighted least-squares solver.

Run with:  python3.11 demo.py
"""

from ils_reference import (
    exact_normal_equations,
    exact_solve,
    max_relative_error,
)
from ils_solver import (
    IncrementalLeastSquares,
    InvalidWeightError,
    UnknownSampleError,
    UpdateError,
)


def banner(title):
    print()
    print("=" * 70)
    print(title)
    print("=" * 70)


def exact_of(solver):
    obs = list(solver.observations.values())
    M, z = exact_normal_equations(obs, solver.n, solver.regularization)
    return exact_solve(M, z)


def fmt(vec, digits=10):
    return "[" + ", ".join("%.*g" % (digits, v) for v in vec) + "]"


def demo_basic():
    banner("1. Incremental weighted solve (rank-one Cholesky updates)")
    solver = IncrementalLeastSquares(3, regularization=1e-8,
                                     verify_interval=8)
    data = [
        ("d1", [1.0, 0.5, -0.25], 2.0, 1.0),
        ("d2", [0.0, 1.0, 0.75], -1.0, 2.0),
        ("d3", [1.5, -0.5, 0.5], 0.5, 0.5),
        ("d4", [0.25, 0.25, 1.0], 1.5, 1.0),
        ("d5", [-1.0, 0.5, 0.5], -0.5, 1.5),
    ]
    for sid, row, target, weight in data:
        solver.add(sid, row, target, weight=weight)
        print("added   %-4s weight=%.1f" % (sid, weight))
    sol = solver.solve()
    err = max_relative_error(sol.x, exact_of(solver))
    print("solution:", fmt(sol.x))
    print("status  : %s (rank %d), rel. error vs exact reference: %.3e"
          % (sol.status, sol.rank, err))
    report = solver.verify()
    print("verify  : ok=%s factor_dev=%.2e rhs_dev=%.2e residual=%.2e"
          % (report.ok, report.factor_deviation, report.rhs_deviation,
             report.solution_residual))


def demo_sliding_window():
    banner("2. Weighted sliding window (add / retract, window = 4)")
    solver = IncrementalLeastSquares(2, regularization=1e-6,
                                     verify_interval=4)
    stream = [
        ("w1", [1.0, 0.0], 1.0, 1.0),
        ("w2", [0.0, 1.0], 2.0, 1.0),
        ("w3", [1.0, 1.0], 3.5, 2.0),
        ("w4", [2.0, 1.0], 4.0, 0.5),
        ("w5", [1.0, -1.0], -1.0, 1.0),
        ("w6", [0.5, 0.5], 1.5, 1.0),
        ("w7", [1.0, 2.0], 5.0, 1.0),
    ]
    window = []
    for sid, row, target, weight in stream:
        solver.add(sid, row, target, weight=weight)
        window.append(sid)
        action = "add %-3s" % sid
        if len(window) > 4:
            old = window.pop(0)
            solver.retract(old)
            action += " / retract %-3s" % old
        sol = solver.solve()
        print("%-24s -> x = %s" % (action, fmt(sol.x, 8)))
    report = solver.verify()
    print("final verify: ok=%s residual=%.2e (checked against raw "
          "observations)" % (report.ok, report.solution_residual))


def demo_rank_deficiency():
    banner("3. Retraction causing a rank drop -> solution space")
    solver = IncrementalLeastSquares(2)  # no regularization
    solver.add("a", [1.0, 0.0], 3.0)
    solver.add("b", [0.0, 1.0], 4.0)
    print("full rank     :", fmt(solver.solve().x, 6))
    solver.retract("b")
    sol = solver.solve()
    print("after retract : status=%s rank=%d" % (sol.status, sol.rank))
    print("particular    :", fmt(sol.x, 6))
    print("null space    :", [fmt(v, 6) for v in sol.null_space])
    print("=> all solutions: x = %s + t * %s"
          % (fmt(sol.x, 6), fmt(sol.null_space[0], 6)))

    reg = IncrementalLeastSquares(2, regularization=1e-4)
    reg.add("a", [1.0, 0.0], 3.0)
    reg.add("b", [0.0, 1.0], 4.0)
    reg.retract("b")
    sol = reg.solve()
    print("regularized   : status=%s x=%s (unique)"
          % (sol.status, fmt(sol.x, 6)))


def demo_robustness():
    banner("4. Validation and atomic rollback")
    solver = IncrementalLeastSquares(2, regularization=1.0)
    solver.add("a", [1.0, 0.0], 1.0)
    solver.add("b", [0.0, 1.0], 2.0)

    try:
        solver.retract("ghost")
    except UnknownSampleError as exc:
        print("retract unknown id -> %s: %s"
              % (type(exc).__name__, exc))
    try:
        solver.add("neg", [1.0, 1.0], 0.0, weight=-0.5)
    except InvalidWeightError as exc:
        print("negative weight    -> %s: %s"
              % (type(exc).__name__, exc))

    before = solver.normal_matrix()
    solver.add("z", [7.0, 7.0], 9.0, weight=0.0)
    print("zero-weight sample registered: %s, matrix unchanged: %s"
          % ("z" in solver, solver.normal_matrix() == before))

    fragile = IncrementalLeastSquares(2, rank_tol=0.9)  # forced failure
    fragile.add("a", [1.0, 0.0], 1.0)
    fragile.add("b", [0.0, 1.0], 2.0)
    fragile.add("c", [1.0, 1.0], 3.0)
    before = fragile.normal_matrix()
    try:
        fragile.retract("c")
    except UpdateError as exc:
        print("failed downdate    -> %s: %s" % (type(exc).__name__, exc))
    print("state intact after rollback: %s (rollbacks=%d)"
          % (fragile.normal_matrix() == before,
             fragile.stats["rollbacks"]))


def demo_stress():
    banner("5. Numerical stress report (vs exact rational reference)")
    rows = []

    # large offsets in the targets
    s = IncrementalLeastSquares(2, regularization=1e-8)
    s.add("p", [1.0, 0.5], 1.0e6 + 1.0)
    s.add("q", [0.5, 1.0], 1.0e6 - 2.0)
    s.add("r", [1.0, 1.0], 1.0e6 + 0.5)
    rows.append(("large offsets (b ~ 1e6)",
                 max_relative_error(s.solve().x, exact_of(s))))

    # near-correlated columns
    eps = 1e-6
    s = IncrementalLeastSquares(2, regularization=1e-6)
    s.add("r1", [1.0, 1.0], 3.0)
    s.add("r2", [1.0, 1.0 + eps], 3.0 + 2 * eps)
    s.add("r3", [1.0, 1.0 - eps], 3.0 - 2 * eps)
    rows.append(("near-correlated columns (1e-6)",
                 max_relative_error(s.solve().x, exact_of(s))))

    # tiny weights next to unit weights
    s = IncrementalLeastSquares(2)
    s.add("a", [1.0, 0.0], 1.0)
    s.add("b", [0.0, 1.0], 2.0, weight=1e-12)
    s.add("c", [1.0, 1.0], 3.0)
    s.retract("b")  # tiny-weight downdate
    rows.append(("tiny weights (1e-12) + downdate",
                 max_relative_error(s.solve().x, exact_of(s))))

    print("%-38s %s" % ("scenario", "max relative error"))
    print("-" * 58)
    for name, err in rows:
        print("%-38s %.3e" % (name, err))


def main():
    demo_basic()
    demo_sliding_window()
    demo_rank_deficiency()
    demo_robustness()
    demo_stress()
    print()


if __name__ == "__main__":
    main()
