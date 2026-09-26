"""Structural insufficient-capacity diagnostics.

These checks are *necessary* conditions for feasibility: when any of them
fails, no valid placement can exist, and the returned messages explain
exactly which object / rack / capacity bound is short.
"""

from __future__ import annotations

from fractions import Fraction

from .model import Problem


def diagnose(problem: Problem) -> list[str]:
    """Return human-readable diagnostics; empty list means checks passed."""
    diags: list[str] = []

    total_capacity = sum((n.capacity for n in problem.nodes), Fraction(0))
    total_demand = sum((o.size * o.replicas for o in problem.objects), Fraction(0))
    if total_demand > total_capacity:
        diags.append(
            f"total capacity insufficient: replicas need {total_demand}, "
            f"nodes provide {total_capacity}, deficit {total_demand - total_capacity}"
        )

    racks: dict[str, list] = {}
    for n in problem.nodes:
        racks.setdefault(n.rack, []).append(n)

    for obj in problem.objects:
        ok_racks = sorted(
            rack for rack, members in racks.items() if any(m.capacity >= obj.size for m in members)
        )
        if len(ok_racks) < obj.replicas:
            diags.append(
                f"object {obj.obj_id}: needs {obj.replicas} distinct rack(s), but only "
                f"{len(ok_racks)} rack(s) {ok_racks} have a node with capacity >= {obj.size}"
            )
    return diags
