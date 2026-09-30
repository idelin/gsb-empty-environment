"""Demo: schedule four meetings into one room with hierarchical preferences.

Run from the repository root:

    python3 examples/demo.py

Scenario
--------
Four meetings m1..m4 must each get one of the time slots 0..3 in a single
room. Hard rules: no two meetings share a slot, and m2 must run after m1.
Preferences are soft, on two hierarchy levels:

- level 0 (critical, weight 5 each): m3 should start by slot 1, and the
  VIP meeting m4 should get slot 2 or later.
- level 1 (nice-to-have, weight 1 each): m1 in slot 0, m2 in slot 1.

Because levels are lexicographic, the solver satisfies both critical
preferences even though doing so forces it to violate every nice-to-have
preference -- a single level-0 unit outweighs any number of level-1 units.
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from lexopt import (
    Model,
    Solver,
    all_different,
    check_solution,
    explain,
    linear_constraint,
    verify_result,
)


def build_model() -> Model:
    model = Model()
    for name in ("m1", "m2", "m3", "m4"):
        model.add_variable(name, range(4))

    model.add_constraint(all_different("one_room", ("m1", "m2", "m3", "m4")))
    model.add_constraint(
        linear_constraint("agenda_order", {"m2": 1, "m1": -1}, ">=", 1)
    )

    model.add_constraint(
        linear_constraint(
            "m3_early", {"m3": 1}, "<=", 1, hard=False, weight=5, level=0
        )
    )
    model.add_constraint(
        linear_constraint(
            "vip_m4_late", {"m4": 1}, ">=", 2, hard=False, weight=5, level=0
        )
    )
    model.add_constraint(
        linear_constraint(
            "m1_slot0", {"m1": 1}, "==", 0, hard=False, weight=1, level=1
        )
    )
    model.add_constraint(
        linear_constraint(
            "m2_slot1", {"m2": 1}, "==", 1, hard=False, weight=1, level=1
        )
    )
    return model


def main() -> None:
    model = build_model()
    result = Solver(model).solve()

    print("=== solver output ===")
    print(explain(model, result))
    print()

    print("=== independent checker ===")
    report = verify_result(model, result)
    print(f"verified: {report.ok}")
    for problem in report.problems:
        print(f"  problem: {problem}")
    for note in report.notes:
        print(f"  note: {note}")

    direct = check_solution(model, result.assignment, result.objective)
    print(f"direct re-check of assignment against original constraints: {direct.ok}")


if __name__ == "__main__":
    main()
