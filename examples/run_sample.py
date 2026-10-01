"""Sample: shift scheduling with hard rules and hierarchical soft goals.

Run from the repository root:
    python3.11 examples/run_sample.py

Scenario: 5 shifts, 3 nurses (0, 1, 2).
Hard rules (must hold, cannot be bought off):
  - consecutive shifts must be staffed by different nurses (rest rule).
Soft goals, level 0 (critical, one constraint):
  - shift 0 should be staffed by nurse 1.
Soft goals, level 1 (preferences, five constraints, weight 1 each):
  - each shift prefers nurse 0.

A weighted-sum solver with weights (1, 1x5) would violate the level-0 goal
to collect more level-1 gains. Lexicographic optimization never does that:
one unit of level-0 penalty outweighs any amount of level-1 gain.
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from cspsolver import Model, Solver, Variable  # noqa: E402


def build_model() -> Model:
    m = Model()
    shifts = [f"shift{i}" for i in range(5)]
    for s in shifts:
        m.add_variable(Variable(s, domain=(0, 1, 2)))

    # Hard: rest rule between consecutive shifts.
    for i in range(4):
        m.add_hard(f"rest_{i}", (shifts[i], shifts[i + 1]), lambda a, b: a != b)

    # Soft level 0 (critical): shift 0 staffed by nurse 1.
    m.add_soft("critical_first_shift", ("shift0",), lambda a: a == 1, weight=1, level=0)

    # Soft level 1 (preferences): every shift prefers nurse 0.
    for s in shifts:
        m.add_soft(f"prefer_nurse0_{s}", (s,), lambda a: a == 0, weight=1, level=1)
    return m


def main() -> None:
    print("=== shift scheduling: lexicographic solve ===")
    result = Solver(build_model()).solve()
    print(result.explain())

    print()
    print("=== negative weights are rejected ===")
    try:
        build_model().add_soft("bad", ("shift0",), lambda a: True, weight=-3)
    except ValueError as exc:
        print(f"ValueError: {exc}")

    print()
    print("=== hard conflicts cannot be bought off ===")
    m = Model()
    m.add_variable(Variable("x", domain=(0, 1)))
    m.add_hard("x_must_be_0", ("x",), lambda a: a == 0)
    m.add_hard("x_must_be_1", ("x",), lambda a: a == 1)
    m.add_soft("x_likes_1", ("x",), lambda a: a == 1, weight=10**9, level=0)
    print(Solver(m).solve().explain())


if __name__ == "__main__":
    main()
