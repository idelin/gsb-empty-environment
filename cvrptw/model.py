"""Problem data model for the asymmetric CVRPTW.

All constraint data are exact rationals: ``int`` or ``fractions.Fraction``
(a ``(num, den)`` tuple is also accepted as a convenience).  Floats are
rejected on purpose so the solver never mixes inexact arithmetic into
feasibility or optimality claims.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from typing import Union

Number = Union[int, Fraction]


def as_fraction(value) -> Fraction:
    """Coerce an int / Fraction / (num, den) tuple to ``Fraction``."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("booleans are not valid constraint data")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, tuple) and len(value) == 2:
        return Fraction(as_fraction(value[0]), as_fraction(value[1]))
    raise TypeError(f"expected int, Fraction or (num, den) tuple, got {type(value).__name__}")


@dataclass(frozen=True)
class Customer:
    """A single demand point.  Node index in the distance matrix is its
    position in ``Problem.customers`` plus one (node 0 is the depot)."""

    name: str
    demand: Fraction
    ready: Fraction    # earliest service start (time window open)
    due: Fraction      # latest service start (time window close)
    service: Fraction  # service duration

    def __post_init__(self):
        object.__setattr__(self, "demand", as_fraction(self.demand))
        object.__setattr__(self, "ready", as_fraction(self.ready))
        object.__setattr__(self, "due", as_fraction(self.due))
        object.__setattr__(self, "service", as_fraction(self.service))
        if self.demand < 0:
            raise ValueError(f"{self.name}: negative demand")
        if self.ready > self.due:
            raise ValueError(f"{self.name}: ready time {self.ready} after due time {self.due}")
        if self.service < 0:
            raise ValueError(f"{self.name}: negative service time")


@dataclass(frozen=True)
class Problem:
    """Capacitated VRP with time windows on a synthetic, possibly
    asymmetric distance matrix.

    ``distances[i][j]`` is the travel cost from node i to node j; node 0 is
    the depot.  No symmetry is assumed: ``distances[i][j]`` and
    ``distances[j][i]`` are independent inputs.
    """

    capacity: Fraction
    n_vehicles: int
    customers: tuple
    distances: tuple
    horizon: Fraction  # latest return time at the depot

    def __post_init__(self):
        object.__setattr__(self, "capacity", as_fraction(self.capacity))
        object.__setattr__(self, "horizon", as_fraction(self.horizon))
        object.__setattr__(self, "customers", tuple(self.customers))
        matrix = tuple(tuple(as_fraction(v) for v in row) for row in self.distances)
        n = len(self.customers) + 1
        if len(matrix) != n or any(len(row) != n for row in matrix):
            raise ValueError(f"distance matrix must be {n}x{n} (depot + {n - 1} customers)")
        for i, row in enumerate(matrix):
            for j, v in enumerate(row):
                if v < 0:
                    raise ValueError(f"negative distance d({i},{j})")
        object.__setattr__(self, "distances", matrix)
        if self.capacity <= 0:
            raise ValueError("capacity must be positive")
        if self.n_vehicles < 1:
            raise ValueError("need at least one vehicle")

    def node_name(self, node: int) -> str:
        if node == 0:
            return "DEPOT"
        return self.customers[node - 1].name


class Status(Enum):
    """Outcome of a solve call.  Feasible / optimal / budget-undecided are
    kept strictly separate, and a heuristic miss is never reported as
    infeasibility."""

    OPTIMAL = "optimal"                # exact search finished, objective proven minimal
    FEASIBLE = "feasible"              # solution found, optimality not claimed
    INFEASIBLE = "infeasible"          # exact proof that no solution exists
    NOT_FOUND = "not_found"            # heuristic failed; says nothing about feasibility
    BUDGET_EXHAUSTED = "budget_exhausted"  # exact search ran out of node budget, undecided


@dataclass(frozen=True)
class Result:
    """Solver output.  ``objective`` is always the *actual* total distance
    of the returned routes (never a bound), or ``None`` when no routes are
    returned."""

    status: Status
    objective: object   # Fraction or None
    routes: tuple       # tuple of tuples of customer indices (0-based), or ()
    nodes_explored: int
    message: str = ""

    @property
    def proven_optimal(self) -> bool:
        return self.status is Status.OPTIMAL
