"""Runnable sample: three separated scenarios + dependency-cycle policies.

Run from the repo root:  python3 examples/demo.py
"""

from __future__ import annotations

import os
import sys
from fractions import Fraction

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from project_select import Instance, Project, check_selection, solve  # noqa: E402


def show(title, inst, **kw):
    res = solve(inst, **kw)
    print(f"--- {title} ---")
    print(f"  status           : {res.status}")
    if res.status == "optimal":
        print(f"  selected         : {sorted(res.selected) or '(nothing)'}")
        print(f"  total cost       : {res.total_cost}  (budget {res.budget}, "
              f"remaining {res.budget_remaining})")
        print(f"  total profit     : {res.total_profit}")
        decisive = "decisive (budget binding)" if res.budget_binding \
            else "undecided (slack: same optimum without a budget)"
        print(f"  budget role      : {decisive}")
        errs = check_selection(inst, res.selected)
        print(f"  feasibility check: {'OK' if not errs else errs}")
    else:
        print(f"  reason           : {res.reason}")
    print()
    return res


def main():
    print("=" * 64)
    print("Scenario 1: FEASIBLE / OPTIMAL — shared dependency paid once,")
    print("negative-profit dependency pulled in by a profitable child")
    print("=" * 64)
    inst1 = Instance(
        projects=[
            Project("core-lib", 3, -1),                    # shared, negative
            Project("app-a", 2, 6, deps=["core-lib"]),
            Project("app-b", 2, 5, deps=["core-lib"]),
            Project("toy", 9, 1),                          # too expensive
        ],
        budget=8)
    show("optimal under budget 8", inst1)

    print("=" * 64)
    print("Scenario 2: BUDGET DECISIVE — mutex pair, budget forces a choice")
    print("=" * 64)
    # Unlimited budget would take rewrite+docs (profit 14, cost 8);
    # budget 6 forces the cheaper patch+docs combo (profit 12).
    inst2 = Instance(
        projects=[
            Project("rewrite", 6, 11),
            Project("patch", 4, 9),
            Project("docs", 2, 3),
        ],
        mutex=[("rewrite", "patch")],
        budget=6)
    show("optimal under budget 6", inst2)

    print("=" * 64)
    print("Scenario 3: BUDGET UNDECIDED — leftover budget cannot buy anything")
    print("=" * 64)
    inst3 = Instance(
        projects=[
            Project("main", 5, 9),
            Project("side", Fraction(3, 2), Fraction(5, 2)),
        ],
        budget=Fraction(13, 2))
    show("optimal under budget 13/2", inst3)

    print("=" * 64)
    print("Scenario 4: DEPENDENCY CYCLE — explicit 'group' vs 'reject'")
    print("=" * 64)
    inst4 = Instance(
        projects=[
            Project("auth", 2, 4, deps=["session"]),
            Project("session", 3, 5, deps=["auth"]),   # cycle auth<->session
            Project("extra", 1, 1),
        ],
        budget=10)
    show("on_cycle='group' (atomic group, cost summed)", inst4,
         on_cycle="group")
    show("on_cycle='reject' (instance refused, cycle reported)", inst4,
         on_cycle="reject")

    print("=" * 64)
    print("Scenario 5: INFEASIBLE — required projects exceed the budget")
    print("=" * 64)
    inst5 = Instance(
        projects=[Project("must", 10, 100)],
        budget=5, required=["must"])
    show("required 'must' costs 10 > budget 5", inst5)


if __name__ == "__main__":
    main()
