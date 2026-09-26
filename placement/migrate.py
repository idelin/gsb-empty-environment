"""Migration planning between two valid placements.

The plan adds every new replica before removing any old one, so the replica
count of every object stays >= its minimum at every single step.
"""

from __future__ import annotations

from dataclasses import dataclass

from .model import Problem, validate_placement


@dataclass(frozen=True)
class Step:
    action: str  # "add" or "remove"
    obj_id: str
    node_id: str

    def __str__(self) -> str:
        verb = "place" if self.action == "add" else "evict"
        return f"{self.action:6s} {verb} replica of {self.obj_id} on {self.node_id}"


def build_migration_plan(
    problem: Problem,
    current: dict[str, tuple[str, ...]],
    target: dict[str, tuple[str, ...]],
) -> list[Step]:
    """Order the transition current -> target as add-steps then remove-steps.

    Both placements must be fully valid (rack isolation, capacity,
    anti-affinity, minimum replicas); a ValueError is raised otherwise.
    """
    for label, placement in (("current", current), ("target", target)):
        errors = validate_placement(problem, placement)
        if errors:
            raise ValueError(f"{label} placement is invalid: {errors}")

    adds: list[Step] = []
    removes: list[Step] = []
    for obj in problem.objects:
        cur = set(current[obj.obj_id])
        tgt = set(target[obj.obj_id])
        adds.extend(Step("add", obj.obj_id, nid) for nid in sorted(tgt - cur))
        removes.extend(Step("remove", obj.obj_id, nid) for nid in sorted(cur - tgt))
    plan = adds + removes

    # Defensive verification: the minimum replica count must hold at every step.
    counts = {o.obj_id: len(set(current[o.obj_id])) for o in problem.objects}
    minimum = {o.obj_id: o.replicas for o in problem.objects}
    for step in plan:
        counts[step.obj_id] += 1 if step.action == "add" else -1
        if counts[step.obj_id] < minimum[step.obj_id]:
            raise RuntimeError(
                f"migration plan violates minimum replica count for {step.obj_id}"
            )
    return plan


def apply_steps(
    current: dict[str, tuple[str, ...]], steps: list[Step]
) -> dict[str, tuple[str, ...]]:
    """Replay a plan, returning the resulting placement."""
    state = {oid: list(nids) for oid, nids in current.items()}
    for step in steps:
        if step.action == "add":
            state[step.obj_id].append(step.node_id)
        else:
            state[step.obj_id].remove(step.node_id)
    return {oid: tuple(sorted(nids)) for oid, nids in state.items()}
