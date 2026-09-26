"""Domain model and placement validation.

All constraint data are synthetic integers or rationals.  Rationals are
represented exactly with ``fractions.Fraction``; callers may pass ``int``,
``Fraction`` or strings such as ``"3/2"`` and they are normalised via
:func:`to_fraction`.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


def to_fraction(value) -> Fraction:
    """Normalise an int / Fraction / rational string to ``Fraction``."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool is not valid constraint data")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"unsupported numeric value: {value!r}")


@dataclass(frozen=True)
class Node:
    node_id: str
    rack: str
    capacity: Fraction


@dataclass(frozen=True)
class Obj:
    obj_id: str
    size: Fraction
    replicas: int  # required (minimum) replica count


@dataclass
class Problem:
    nodes: list[Node]
    objects: list[Obj]
    costs: dict[tuple[str, str], Fraction]  # (obj_id, node_id) -> cost per replica
    anti_affinity: dict[str, frozenset[str]]  # obj -> objs that must not share a node

    @classmethod
    def build(cls, nodes, objects, costs, anti_affinity=None) -> "Problem":
        """Build a problem from raw synthetic data.

        nodes:         iterable of (node_id, rack, capacity)
        objects:       iterable of (obj_id, size, replicas)
        costs:         {(obj_id, node_id): number-or-rational-string}
        anti_affinity: iterable of (obj_id, obj_id) pairs, symmetrised here
        """
        node_list = [Node(nid, rack, to_fraction(cap)) for nid, rack, cap in nodes]
        obj_list = [Obj(oid, to_fraction(size), int(rep)) for oid, size, rep in objects]
        cost_map = {(o, n): to_fraction(c) for (o, n), c in costs.items()}
        sym: dict[str, set[str]] = {o.obj_id: set() for o in obj_list}
        for a, b in (anti_affinity or []):
            sym.setdefault(a, set()).add(b)
            sym.setdefault(b, set()).add(a)
        return cls(node_list, obj_list, cost_map, {k: frozenset(v) for k, v in sym.items()})


def validate_placement(problem: Problem, placement: dict[str, tuple[str, ...]]) -> list[str]:
    """Return a list of constraint violations (empty means fully valid).

    Checks: every object placed with >= its minimum replica count, no replica
    counted twice on the same node, rack isolation (two nodes in the same rack
    do NOT count as isolated), node capacity, and object anti-affinity.
    """
    errors: list[str] = []
    node_by_id = {n.node_id: n for n in problem.nodes}
    obj_ids = {o.obj_id for o in problem.objects}

    for oid in placement:
        if oid not in obj_ids:
            errors.append(f"unknown object in placement: {oid}")

    for obj in problem.objects:
        node_ids = placement.get(obj.obj_id)
        if not node_ids:
            errors.append(f"object {obj.obj_id}: no replicas placed (minimum is {obj.replicas})")
            continue
        unknown = [nid for nid in node_ids if nid not in node_by_id]
        if unknown:
            errors.append(f"object {obj.obj_id}: unknown node(s) {unknown}")
            continue
        if len(set(node_ids)) != len(node_ids):
            errors.append(f"object {obj.obj_id}: replica counted twice on the same node")
        distinct = sorted(set(node_ids))
        if len(distinct) < obj.replicas:
            errors.append(
                f"object {obj.obj_id}: {len(distinct)} replica(s) placed, minimum is {obj.replicas}"
            )
        racks = [node_by_id[nid].rack for nid in distinct]
        if len(set(racks)) != len(racks):
            errors.append(
                f"object {obj.obj_id}: replicas on nodes of the same rack do not satisfy rack isolation"
            )

    load = {n.node_id: Fraction(0) for n in problem.nodes}
    for obj in problem.objects:
        for nid in dict.fromkeys(placement.get(obj.obj_id, ())):
            if nid in load:
                load[nid] += obj.size
    for n in problem.nodes:
        if load[n.node_id] > n.capacity:
            errors.append(f"node {n.node_id}: load {load[n.node_id]} exceeds capacity {n.capacity}")

    for obj in problem.objects:
        mine = set(placement.get(obj.obj_id, ()))
        for foe in problem.anti_affinity.get(obj.obj_id, frozenset()):
            if foe <= obj.obj_id:
                continue  # report each pair once
            shared = mine & set(placement.get(foe, ()))
            if shared:
                errors.append(
                    f"anti-affinity violated: {obj.obj_id} and {foe} share node(s) {sorted(shared)}"
                )
    return errors


def placement_cost(problem: Problem, placement: dict[str, tuple[str, ...]]) -> Fraction:
    """Total transmission cost of a placement (exact rational arithmetic)."""
    total = Fraction(0)
    for obj in problem.objects:
        for nid in dict.fromkeys(placement.get(obj.obj_id, ())):
            total += problem.costs[(obj.obj_id, nid)]
    return total
