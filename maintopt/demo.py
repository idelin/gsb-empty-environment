"""Runnable sample: python3.11 -m maintopt.demo

Builds one synthetic instance and solves it under three search budgets to
show the OPTIMAL / FEASIBLE / BUDGET_UNDECIDED statuses separately.
"""

from __future__ import annotations

from fractions import Fraction

from .model import Device, Instance
from .planner import solve
from .verify import check_feasible, plan_cost, segments_of


def sample_instance() -> Instance:
    return Instance(
        horizon=6,
        group_limits={"mech": 1, "elec": 2},
        budget=6,
        devices=(
            Device("pump",    "mech", 2, Fraction(5),     2),
            Device("valve",   "mech", 1, Fraction(2),     1),
            Device("motor",   "elec", 3, Fraction(7),     2),
            Device("sensor",  "elec", 2, Fraction(9, 2),  1),
            Device("battery", "elec", 1, Fraction(3, 2),  1),
        ),
        blocked_slots=frozenset({2}),  # slot 2 closed: forces split decisions
    )


def render(instance: Instance, result) -> str:
    lines = [
        f"status={result.status}  nodes={result.nodes_explored}/{result.node_budget}",
    ]
    if result.plan is None:
        lines.append("plan=<undecided>")
    else:
        lines.append(f"score={result.score}  cost={result.cost}  bound={result.bound}")
        for name in sorted(result.plan):
            slots = result.plan[name]
            lines.append(
                f"  {name:<8} slots={slots} segments={segments_of(slots)}"
            )
        errors = check_feasible(instance, result.plan)
        lines.append(f"verifier: {'OK' if not errors else errors}")
    return "\n".join(lines)


def main() -> None:
    instance = sample_instance()
    print("instance: horizon=6, limits={mech:1, elec:2}, budget=6, blocked={2}")
    for device in instance.devices:
        print(
            f"  {device.name:<8} group={device.group:<5} duration={device.duration}"
            f" score={device.score} startup={device.startup_cost}"
        )
    for label, budget in [("full search", 1_000_000), ("tight node budget", 4), ("no node budget", 0)]:
        print(f"\n--- {label} (node_budget={budget}) ---")
        print(render(instance, solve(instance, node_budget=budget)))


if __name__ == "__main__":
    main()
