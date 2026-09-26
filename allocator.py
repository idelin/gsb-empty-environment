"""Integer quota allocation over a tree of budget constraints.

Model
-----
- Internal (branch) nodes carry lower/upper bounds on their subtree total.
- Leaf nodes carry a demand (cap) and a weight used for proportional splits.
- Constraint data are exact numbers: ``int``, ``Fraction`` or strings like
  ``"7/2"``.  Floats are rejected to keep arithmetic exact.
- Leaf allocations are integers.  Effective integer bounds are derived by
  rounding: ``ceil(lower)`` / ``floor(upper)`` / ``floor(demand)``.
- Conservation: every internal node's total equals the sum of its children's
  totals, hence also the sum of the leaf allocations in its subtree.

Statuses (kept strictly separate)
---------------------------------
- ``OPTIMAL``: root bounds exist and every leaf demand is fully satisfied.
- ``FEASIBLE``: all bounds hold but the budget binds, so some demand is unmet.
- ``BUDGET_UNDECIDED``: the root carries no bounds at all; the budget was
  never decided, so full demand is allocated and reported.
- ``INFEASIBLE``: bounds conflict; the result carries the minimal conflicting
  subtree (all of its proper subtrees are internally consistent).

Rounding
--------
Each split uses exact rational ideal shares proportional to subtree weights,
floors them, then hands out the remaining units by largest fractional
remainder.  Equal remainders are resolved by ascending stable node id.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from math import ceil, floor
from typing import Optional, Union

Number = Union[int, str, Fraction]

__all__ = [
    "Node",
    "Status",
    "ConflictInfo",
    "AllocationResult",
    "allocate",
    "verify",
]


def _to_fraction(value) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("boolean is not a valid constraint number")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(
        f"constraint numbers must be int, str or Fraction, "
        f"got {type(value).__name__}"
    )


class Status(Enum):
    OPTIMAL = "optimal"
    FEASIBLE = "feasible"
    BUDGET_UNDECIDED = "budget_undecided"
    INFEASIBLE = "infeasible"


class Node:
    """A tree node: an internal budget node or a leaf with demand/weight."""

    __slots__ = ("node_id", "children", "demand", "weight", "lower", "upper")

    def __init__(self, node_id, *, children=None, demand=None, weight=None,
                 lower=None, upper=None):
        self.node_id = str(node_id)
        self.children = list(children) if children else []
        self.demand = _to_fraction(demand) if demand is not None else None
        self.weight = _to_fraction(weight) if weight is not None else None
        self.lower = _to_fraction(lower) if lower is not None else None
        self.upper = _to_fraction(upper) if upper is not None else None
        if self.is_leaf:
            if self.demand is None or self.weight is None:
                raise ValueError(
                    f"leaf {self.node_id!r} needs both demand and weight")
            if self.demand < 0:
                raise ValueError(f"leaf {self.node_id!r} has negative demand")
            if self.weight < 0:
                raise ValueError(f"leaf {self.node_id!r} has negative weight")
        else:
            if self.demand is not None or self.weight is not None:
                raise ValueError(
                    f"branch {self.node_id!r} must not carry demand/weight")
            for bound, name in ((self.lower, "lower"), (self.upper, "upper")):
                if bound is not None and bound < 0:
                    raise ValueError(
                        f"branch {self.node_id!r} has negative {name} bound")

    @classmethod
    def leaf(cls, node_id, demand, weight) -> "Node":
        return cls(node_id, demand=demand, weight=weight)

    @classmethod
    def branch(cls, node_id, children, *, lower=None, upper=None) -> "Node":
        return cls(node_id, children=children, lower=lower, upper=upper)

    @property
    def is_leaf(self) -> bool:
        return not self.children

    def __repr__(self) -> str:
        kind = "leaf" if self.is_leaf else "branch"
        return f"Node.{kind}({self.node_id!r})"


@dataclass(frozen=True)
class ConflictInfo:
    """Description of a minimal conflicting subtree."""

    node_id: str
    effective_lower: int
    effective_upper: int
    reason: str
    subtree: dict


@dataclass(frozen=True)
class AllocationResult:
    status: Status
    allocations: dict
    totals: dict
    conflict: Optional[ConflictInfo]

    @property
    def total(self) -> int:
        return sum(self.allocations.values())


class _Conflict(Exception):
    def __init__(self, info: ConflictInfo):
        super().__init__(info.reason)
        self.info = info


def _snapshot(node: Node) -> dict:
    snap = {"id": node.node_id}
    if node.is_leaf:
        snap["demand"] = str(node.demand)
        snap["weight"] = str(node.weight)
    else:
        if node.lower is not None:
            snap["lower"] = str(node.lower)
        if node.upper is not None:
            snap["upper"] = str(node.upper)
        snap["children"] = [_snapshot(child) for child in node.children]
    return snap


def _analyze(node: Node, ranges: dict, weights: dict):
    """Post-order pass filling effective integer ranges and subtree weights.

    Raises ``_Conflict`` at the first inconsistent node; because children are
    processed first, that node's proper subtrees are all consistent, so the
    reported subtree is minimal.
    """
    if node.is_leaf:
        lo, hi = 0, floor(node.demand)
        weights[node.node_id] = node.weight
    else:
        child_ranges = [_analyze(child, ranges, weights)
                        for child in node.children]
        children_lo = sum(pair[0] for pair in child_ranges)
        children_hi = sum(pair[1] for pair in child_ranges)
        lo = children_lo
        hi = children_hi
        if node.lower is not None:
            lo = max(lo, ceil(node.lower))
        if node.upper is not None:
            hi = min(hi, floor(node.upper))
        if lo > hi:
            reason = (
                f"node {node.node_id!r}: effective lower {lo} exceeds "
                f"effective upper {hi} (own bounds "
                f"[{node.lower}, {node.upper}], children allow "
                f"[{children_lo}, {children_hi}])"
            )
            raise _Conflict(ConflictInfo(
                node_id=node.node_id,
                effective_lower=lo,
                effective_upper=hi,
                reason=reason,
                subtree=_snapshot(node),
            ))
        weights[node.node_id] = sum(
            (weights[child.node_id] for child in node.children), Fraction(0))
    ranges[node.node_id] = (lo, hi)
    return lo, hi


def _check_unique_ids(root: Node) -> None:
    seen = set()
    stack = [root]
    while stack:
        node = stack.pop()
        if node.node_id in seen:
            raise ValueError(f"duplicate node id {node.node_id!r}")
        seen.add(node.node_id)
        stack.extend(node.children)


def _split(target: int, items: list) -> list:
    """Split integer ``target`` among ``items``.

    Each item is ``(stable_id, lower, upper, weight)`` with
    ``sum(lower) <= target <= sum(upper)``.  Every item first receives its
    lower bound; the rest is distributed proportionally to weights with
    largest-remainder rounding, respecting upper caps.  Equal remainders are
    resolved by ascending stable id.
    """
    allocs = [lo for (_, lo, _, _) in items]
    headroom = [hi - lo for (_, lo, hi, _) in items]
    rem = target - sum(allocs)
    while rem > 0:
        active = [i for i in range(len(items)) if headroom[i] > 0]
        total_w = sum((items[i][3] for i in active), Fraction(0))
        if total_w > 0:
            weight_of = {i: items[i][3] for i in active}
            w_sum = total_w
        else:
            weight_of = {i: Fraction(1) for i in active}
            w_sum = Fraction(len(active))
        ideals = {i: Fraction(rem) * weight_of[i] / w_sum for i in active}
        for i in active:
            give = min(floor(ideals[i]), headroom[i])
            allocs[i] += give
            headroom[i] -= give
            rem -= give
        order = sorted(
            active,
            key=lambda i: (-(ideals[i] - floor(ideals[i])), items[i][0]),
        )
        for i in order:
            if rem == 0:
                break
            if headroom[i] > 0:
                allocs[i] += 1
                headroom[i] -= 1
                rem -= 1
    return allocs


def _distribute(node: Node, target: int, ranges: dict, weights: dict,
                allocations: dict, totals: dict) -> None:
    totals[node.node_id] = target
    if node.is_leaf:
        allocations[node.node_id] = target
        return
    items = [
        (child.node_id,
         ranges[child.node_id][0],
         ranges[child.node_id][1],
         weights[child.node_id])
        for child in node.children
    ]
    shares = _split(target, items)
    for child, share in zip(node.children, shares):
        _distribute(child, share, ranges, weights, allocations, totals)


def _iter_leaves(node: Node):
    if node.is_leaf:
        yield node
    else:
        for child in node.children:
            yield from _iter_leaves(child)


def allocate(root: Node) -> AllocationResult:
    """Allocate integer quotas to the leaves of ``root``."""
    _check_unique_ids(root)
    ranges: dict = {}
    weights: dict = {}
    try:
        _analyze(root, ranges, weights)
    except _Conflict as exc:
        return AllocationResult(Status.INFEASIBLE, {}, {}, exc.info)

    _, root_hi = ranges[root.node_id]
    target = root_hi
    if root.lower is None and root.upper is None:
        status = Status.BUDGET_UNDECIDED
    else:
        total_demand = sum(floor(node.demand) for node in _iter_leaves(root))
        status = Status.OPTIMAL if target >= total_demand else Status.FEASIBLE

    allocations: dict = {}
    totals: dict = {}
    _distribute(root, target, ranges, weights, allocations, totals)
    return AllocationResult(status, allocations, totals, None)


def verify(root: Node, result: AllocationResult) -> list:
    """Check a result against the tree; return a list of violations."""
    errors: list = []
    if result.status is Status.INFEASIBLE:
        return errors
    ranges: dict = {}
    weights: dict = {}
    try:
        _analyze(root, ranges, weights)
    except _Conflict as exc:
        return [f"tree is infeasible: {exc.info.reason}"]

    def visit(node: Node) -> None:
        total = result.totals.get(node.node_id)
        if total is None:
            errors.append(f"missing total for node {node.node_id!r}")
            return
        lo, hi = ranges[node.node_id]
        if not lo <= total <= hi:
            errors.append(
                f"node {node.node_id!r} total {total} outside "
                f"effective bounds [{lo}, {hi}]")
        if node.is_leaf:
            alloc = result.allocations.get(node.node_id)
            if alloc != total:
                errors.append(
                    f"leaf {node.node_id!r} allocation {alloc} != total "
                    f"{total}")
            if alloc is not None and not 0 <= alloc <= floor(node.demand):
                errors.append(
                    f"leaf {node.node_id!r} allocation {alloc} outside "
                    f"[0, {floor(node.demand)}]")
        else:
            child_sum = sum(result.totals.get(child.node_id, 0)
                            for child in node.children)
            if child_sum != total:
                errors.append(
                    f"conservation violated at {node.node_id!r}: total "
                    f"{total} != children sum {child_sum}")
            for child in node.children:
                visit(child)

    visit(root)
    leaf_sum = sum(result.allocations.values())
    if leaf_sum != result.totals.get(root.node_id):
        errors.append(
            f"leaf allocations sum {leaf_sum} != root total "
            f"{result.totals.get(root.node_id)}")
    return errors
