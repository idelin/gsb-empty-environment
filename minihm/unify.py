"""Unification with occurs check and a provenance graph for error chains.

Every successful (and every failing) unification records an edge between the
two type nodes involved, labelled with the reason they had to be equal.  When
a mismatch is reported, the minimal explainable constraint chain is the
shortest path through this graph between the two conflicting types.
"""
from __future__ import annotations

from collections import deque

from .errors import InfiniteType, TypeMismatch
from .types import Reason, TCon, TVar, Type, prune


def occurs(var: TVar, t: Type) -> bool:
    """Occurs check: does `var` appear anywhere inside `t`?"""
    t = prune(t)
    if t is var:
        return True
    if isinstance(t, TCon):
        return any(occurs(var, arg) for arg in t.args)
    return False


def _link(a: Type, b: Type, reason: Reason) -> None:
    a.links.append((b, reason))
    b.links.append((a, reason))


def _bind(var: TVar, t: Type, reason: Reason) -> None:
    if occurs(var, t):
        raise InfiniteType(var, t, reason)
    var.instance = t
    _link(var, t, reason)


def unify(t1: Type, t2: Type, reason: Reason) -> None:
    t1 = prune(t1)
    t2 = prune(t2)
    if t1 is t2:
        return
    if isinstance(t1, TVar):
        _bind(t1, t2, reason)
        return
    if isinstance(t2, TVar):
        _bind(t2, t1, reason)
        return
    if t1.name != t2.name or len(t1.args) != len(t2.args):
        # Record the failing constraint too, so the reported chain can show
        # exactly which requirement forced the two sides together.
        _link(t1, t2, reason)
        raise TypeMismatch(t1, t2, reason)
    _link(t1, t2, reason)
    for a, b in zip(t1.args, t2.args):
        unify(a, b, reason)


def find_path(start: Type, goal: Type):
    """Shortest provenance path between two types (BFS over unify links).

    Returns a list alternating Type and Reason: [t0, reason, t1, ...], or
    None if the two types are not connected.
    """
    prev: dict[int, tuple[Type, Reason] | None] = {id(start): None}
    queue = deque([start])
    while queue:
        node = queue.popleft()
        if node is goal:
            break
        for nxt, reason in node.links:
            if id(nxt) not in prev:
                prev[id(nxt)] = (node, reason)
                queue.append(nxt)
    if id(goal) not in prev:
        return None
    path: list = [goal]
    cur = goal
    while prev[id(cur)] is not None:
        node, reason = prev[id(cur)]
        path.append(reason)
        path.append(node)
        cur = node
    path.reverse()
    return path
