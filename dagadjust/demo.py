"""Sample runs on synthetic data.  Run:  python3.11 -m dagadjust.demo"""

from __future__ import annotations

from .problem import AdjustmentProblem, CountDAG
from .solver import solve


def fmt_vec(v):
    return "[" + ", ".join(f"{x} (~{float(x):.3f})" for x in v) + "]"


def show(title, result):
    print("=" * 74)
    print(title)
    print("=" * 74)
    print(f"status: {result.status}    iterations: {result.iterations}")
    print(f"message: {result.message}")
    if result.zero_weight_variables:
        print(f"zero-weight variables (observation ignored): {result.zero_weight_variables}")
    if result.redundant_constraints:
        print(f"redundant constraint rows: {result.redundant_constraints}")
    if result.x is not None:
        print(f"x:         {fmt_vec(result.x)}")
        print(f"residuals: {fmt_vec(result.residuals)}")
        print(f"objective: {result.objective} (~{float(result.objective):.4f})")
        if result.active_set:
            print(f"active set (x_i = 0): {result.active_set}")
    if result.certificates is not None:
        print(f"certificates: {result.certificates}")
    print()


def main():
    # 1. Plain tree WLS: two leaves per internal node, parent = sum of children.
    tree = CountDAG.from_children(7, {0: (1, 2), 1: (3, 4), 2: (5, 6)})
    p1 = AdjustmentProblem.create(
        tree, [105, 52, 55, 25, 28, 27, 26], [1, 1, 1, 2, 2, 2, 2]
    )
    r1 = solve(p1)
    show("1. Tree WLS (equality only), leaves upweighted", r1)
    print(f"   check: x1+x2 = {r1.x[1] + r1.x[2]} == x0 = {r1.x[0]}\n")

    # 2. Non-negativity: one observation is negative, active set kicks in.
    small = CountDAG.from_children(3, {0: (1, 2)})
    p2 = AdjustmentProblem.create(small, [1, 5, -2], [1, 1, 1], nonneg=True)
    show("2. Non-negative WLS, y = [1, 5, -2]", solve(p2))

    # 3. Counting DAG with a shared leaf (node 4 feeds both 1 and 2).
    dag = CountDAG.from_children(6, {0: (1, 2), 1: (3, 4), 2: (4, 5)})
    p3 = AdjustmentProblem.create(
        dag, [23, 12, 10, 5, 7, 4], [1, 2, 1, 1, 3, 1], nonneg=True
    )
    show("3. Shared-leaf DAG, non-negative", solve(p3))

    # 4. Redundant constraints: every count fixed consistently makes the
    #    DAG balance rows redundant; the solver detects and reports them.
    p4 = AdjustmentProblem.create(
        small,
        [0, 0, 0],
        [1, 1, 1],
        fixed={0: 10, 1: 4, 2: 6},
    )
    show("4. All counts fixed consistently (redundant balance row)", solve(p4))

    # 5. Infeasible fixed counts: 7 + 8 != 10.
    p5 = AdjustmentProblem.create(
        small, [0, 0, 0], [1, 1, 1], fixed={0: 10, 1: 7, 2: 8}
    )
    show("5. Infeasible fixed counts (7 + 8 != 10)", solve(p5))

    # 6. Fixed counts feasible as equalities but not under x >= 0.
    p6 = AdjustmentProblem.create(
        small, [0, 0, 0], [1, 1, 1], fixed={0: 1, 1: 5}, nonneg=True
    )
    show("6. Fixed counts infeasible under non-negativity", solve(p6))

    # 7. Zero-weight singularity: node 3 is isolated and unobserved.
    dag7 = CountDAG.from_children(4, {0: (1, 2)})
    p7 = AdjustmentProblem.create(dag7, [10, 4, 5, 0], [1, 1, 1, 0])
    show("7. Zero-weight isolated variable -> singular", solve(p7))

    # 8. Local observation revision with warm start (certificates re-verified).
    r8a = solve(p2)
    p8 = p2.with_observation(1, 6)
    cold = solve(p8)
    warm = solve(p8, warm_start=r8a)
    show("8a. Revised problem y = [1, 6, -2], cold start", cold)
    show("8b. Same problem, warm start from previous active set", warm)
    print(f"   warm start agrees with cold start: {warm.x == cold.x}")
    print(f"   both certificates verified: {cold.is_optimal and warm.is_optimal}")


if __name__ == "__main__":
    main()
