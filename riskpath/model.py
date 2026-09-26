"""Graph model: directed edges with cost/delay and shared risk groups.

Constraint data (costs, delays, budgets) are exact integers or rationals
(``fractions.Fraction``); no floats and no external solvers are used.
Risk-group success probabilities are floats in ``[0, 1]``; products of
probabilities are represented as sums of natural logs for numerical
stability (see :mod:`riskpath.search`).
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from fractions import Fraction
from typing import Dict, Hashable, List, Optional

NEG_INF = float("-inf")


def to_fraction(value) -> Fraction:
    """Coerce exact constraint data (int or Fraction) to Fraction."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool is not valid constraint data")
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(
        f"constraint data must be int or Fraction, got {type(value).__name__}"
    )


@dataclass(frozen=True)
class Edge:
    tail: Hashable
    head: Hashable
    cost: Fraction
    delay: Fraction
    group: str  # name of the risk group whose failure kills this edge


class Graph:
    """Directed multigraph whose edges belong to named risk groups.

    Edges added without an explicit ``group`` get a private group, which
    models the classical assumption of *independent* per-edge failures.
    Edges sharing a group name fail together (one common failure event),
    so the group probability is counted once per path, not once per edge.
    """

    def __init__(self) -> None:
        self._adj: Dict[Hashable, List[Edge]] = {}
        self._group_success: Dict[str, float] = {}
        self._auto = 0

    def add_edge(self, tail, head, cost=0, delay=0, *, success=1.0, group=None) -> Edge:
        success = float(success)
        if not 0.0 <= success <= 1.0:
            raise ValueError(f"success probability must be in [0, 1], got {success}")
        if group is None:
            self._auto += 1
            group = f"__edge_{self._auto}"
        known = self._group_success.setdefault(group, success)
        if known != success:
            raise ValueError(
                f"risk group {group!r} already registered with success {known}"
            )
        edge = Edge(tail, head, to_fraction(cost), to_fraction(delay), group)
        self._adj.setdefault(tail, []).append(edge)
        self._adj.setdefault(head, self._adj.get(head, []))
        return edge

    def out_edges(self, node) -> List[Edge]:
        return self._adj.get(node, [])

    @property
    def nodes(self) -> List[Hashable]:
        return list(self._adj)

    @property
    def risk_groups(self) -> Dict[str, float]:
        return dict(self._group_success)

    def group_success(self, group: str) -> float:
        return self._group_success[group]

    def group_log(self, group: str) -> float:
        """log of the group success probability; -inf for p = 0, 0.0 for p = 1."""
        p = self._group_success[group]
        if p == 0.0:
            return NEG_INF
        return math.log(p)
