"""Runnable sample: ``python3.11 -m planrepair``.

Scenario: resources A and B (capacity 1, window [0, 24)).  Task t1 already
executed on A (frozen prefix).  At now=4 resource A fails and a new task n1
arrives.  t2 and t4 are forced to move; t3 is unaffected and stays put.
"""

from fractions import Fraction

from .model import Disruption, Plan, Placement, Resource, Task, TimeWindow
from .repair import Status, repair


def _show(title, result):
    print(f"--- {title} ---")
    print(f"status      : {result.status.value}")
    print(f"moved tasks : {result.moved_count}")
    print(f"total cost  : {result.total_cost}")
    print(f"search nodes: {result.stats.nodes} (budget {result.stats.node_budget})")
    if result.changes:
        print("change list :")
        for c in result.changes:
            old = "-" if c.old is None else f"{c.old.rid}@{c.old.start}"
            new = "-" if c.new is None else f"{c.new.rid}@{c.new.start}"
            print(f"  [{c.kind:6s}] {c.tid}: {old} -> {new}")
    if result.conflicts:
        print("conflicts   :")
        for ev in result.conflicts:
            for reason in ev.reasons:
                print(f"  - {ev.tid}: {reason}")
    print()


def main():
    resources = [
        Resource("A", (TimeWindow(0, 24),)),
        Resource("B", (TimeWindow(0, 24),)),
    ]
    tasks = [
        Task("t1", 3, {"A": 2}),
        Task("t2", 3, {"A": 1, "B": 4}),
        Task("t3", 4, {"B": 2}),
        Task("t4", 2, {"A": 1, "B": 3}),
    ]
    plan = Plan(
        {
            "t1": Placement("A", 0),
            "t2": Placement("A", 5),
            "t3": Placement("B", 6),
            "t4": Placement("A", 10),
        },
        executed={"t1"},
    )

    disruption = Disruption(
        added_tasks=(Task("n1", 2, {"A": 1, "B": 2}),),
        failed_resources=frozenset({"A"}),
    )
    _show("resource A fails at now=4, task n1 added",
          repair(resources, tasks, plan, disruption, now=4))

    # Same scenario but the new task can only run on the failed resource.
    bad = Disruption(
        added_tasks=(Task("n2", 2, {"A": 1}),),
        failed_resources=frozenset({"A"}),
    )
    _show("infeasible: n2 only eligible for failed resource A",
          repair(resources, tasks, plan, bad, now=4))

    # Same scenario with a zero search budget: nothing can be decided.
    _show("budget undecided: node_budget=0",
          repair(resources, tasks, plan, disruption, now=4, node_budget=0))

    # Rational (fractional) data: durations and window on a 1/4 grid.
    frac_resources = [Resource("R", (TimeWindow(0, 2),))]
    frac_tasks = [Task("u1", Fraction(1, 2), {"R": 1})]
    frac_plan = Plan({"u1": Placement("R", 0)}, executed={"u1"})
    frac_disruption = Disruption(added_tasks=(
        Task("u2", "1/2", {"R": 1}),
        Task("u3", "3/4", {"R": 1}),
    ))
    _show("rational data: pack u2 (1/2) and u3 (3/4) after frozen u1",
          repair(frac_resources, frac_tasks, frac_plan, frac_disruption,
                 now=Fraction(1, 2)))


if __name__ == "__main__":
    main()
