"""Branch-and-bound solver over finite integer domains.

Design guarantees
-----------------
* Finite domains only; pure standard library; no SAT/SMT calls.
* Maintains a feasible upper bound (the incumbent) and a *reliable*
  lower bound.  The lower bound of a node is the objective value of the
  best constraint-ignoring completion of that node's partial
  assignment, so it can never exceed the true optimum of the subtree.
  The global lower bound is the minimum over all unexplored frontier
  nodes, hence it never exceeds the true optimum either.
* Budget interruption (node limit and/or wall-clock limit) returns the
  best known solution plus the current gap; status is FEASIBLE or
  UNKNOWN, never OPTIMAL unless the search space was exhausted.
* "No feasible solution found yet" (UNKNOWN) is strictly distinguished
  from "proven infeasible" (INFEASIBLE, search exhausted).
* Ties on the objective are broken deterministically by variable order:
  among optimal solutions the lexicographically smallest assignment
  (x[0], x[1], ...) is returned.
* Any solution the solver claims is re-checked against the original
  constraints by the independent `checker` module before being
  returned; a failed check raises an internal error instead of
  returning a bogus solution.
"""
from __future__ import annotations

import time
from dataclasses import dataclass
from enum import Enum
from typing import Optional

from checker import verify_solution
from model import Problem, Sense


class Status(Enum):
    OPTIMAL = "optimal"        # search exhausted, best solution is proven optimal
    INFEASIBLE = "infeasible"  # search exhausted, no feasible solution exists
    FEASIBLE = "feasible"      # budget exhausted, feasible incumbent available
    UNKNOWN = "unknown"        # budget exhausted, nothing proven, no incumbent


@dataclass(frozen=True)
class SolveResult:
    status: Status
    solution: Optional[tuple[int, ...]]
    objective: Optional[int]       # objective of `solution`, if any
    lower_bound: Optional[int]     # proven lower bound on the optimum
    upper_bound: Optional[int]     # = objective of the incumbent, if any
    nodes_explored: int
    interrupted: bool

    @property
    def gap(self) -> Optional[int]:
        """Absolute gap upper_bound - lower_bound (None if either is missing)."""
        if self.lower_bound is None or self.upper_bound is None:
            return None
        return self.upper_bound - self.lower_bound

    @property
    def relative_gap(self) -> Optional[float]:
        if self.lower_bound is None or self.upper_bound is None:
            return None
        denom = max(1, abs(self.upper_bound))
        return (self.upper_bound - self.lower_bound) / denom


class _Node:
    """A frontier node: partial assignment of the first `depth` variables."""

    __slots__ = ("depth", "values", "cost", "activities", "bound")

    def __init__(self, depth, values, cost, activities, bound):
        self.depth = depth
        self.values = values          # tuple of assigned values (length == depth)
        self.cost = cost              # objective contribution of assigned part
        self.activities = activities  # per-constraint activity of assigned part
        self.bound = bound            # lower bound on any completion


def solve(
    problem: Problem,
    max_nodes: Optional[int] = None,
    time_limit: Optional[float] = None,
) -> SolveResult:
    """Minimize ``problem.objective`` subject to ``problem.constraints``.

    Args:
        max_nodes: node-expansion budget; ``None`` means unlimited.
        time_limit: wall-clock budget in seconds; ``None`` means unlimited.

    Returns a :class:`SolveResult`.  When the budget is exhausted before
    the search space is, ``interrupted`` is True and the status is
    FEASIBLE (incumbent found) or UNKNOWN (nothing found, nothing proven).
    """
    n = problem.num_vars
    objective = problem.objective
    constraints = problem.constraints
    num_cons = len(constraints)

    # Suffix sums of the cheapest possible contribution of variables
    # i, i+1, ... to the objective.  suffix_obj_min[k] is a lower bound
    # on the objective contribution of variables k..n-1.
    suffix_obj_min = [0] * (n + 1)
    for i in range(n - 1, -1, -1):
        coef = objective[i]
        suffix_obj_min[i] = suffix_obj_min[i + 1] + min(
            coef * v for v in problem.domains[i]
        )

    # Per-constraint suffix min/max activity of variables k..n-1, used
    # for a cheap necessary feasibility check on partial assignments.
    suffix_con_min = [[0] * (n + 1) for _ in range(num_cons)]
    suffix_con_max = [[0] * (n + 1) for _ in range(num_cons)]
    for j, con in enumerate(constraints):
        for i in range(n - 1, -1, -1):
            coef = con.coeffs[i]
            lo = min(coef * v for v in problem.domains[i])
            hi = max(coef * v for v in problem.domains[i])
            suffix_con_min[j][i] = suffix_con_min[j][i + 1] + lo
            suffix_con_max[j][i] = suffix_con_max[j][i + 1] + hi

    def constraints_can_hold(depth: int, activities) -> bool:
        """Necessary condition: some completion could satisfy all constraints."""
        for j, con in enumerate(constraints):
            lo = activities[j] + suffix_con_min[j][depth]
            hi = activities[j] + suffix_con_max[j][depth]
            if con.sense is Sense.LE:
                if lo > con.rhs:
                    return False
            elif con.sense is Sense.GE:
                if hi < con.rhs:
                    return False
            else:  # EQ
                if lo > con.rhs or hi < con.rhs:
                    return False
        return True

    def fully_feasible(activities) -> bool:
        for j, con in enumerate(constraints):
            act = activities[j]
            if con.sense is Sense.LE and act > con.rhs:
                return False
            if con.sense is Sense.GE and act < con.rhs:
                return False
            if con.sense is Sense.EQ and act != con.rhs:
                return False
        return True

    root_activities = (0,) * num_cons
    root_bound = suffix_obj_min[0]
    if not constraints_can_hold(0, root_activities):
        # Provably infeasible without any search.
        return SolveResult(
            status=Status.INFEASIBLE,
            solution=None,
            objective=None,
            lower_bound=None,
            upper_bound=None,
            nodes_explored=0,
            interrupted=False,
        )

    stack: list[_Node] = [_Node(0, (), 0, root_activities, root_bound)]

    incumbent_values: Optional[tuple[int, ...]] = None
    incumbent_obj: Optional[int] = None

    nodes = 0
    start = time.monotonic()
    interrupted = False

    while stack:
        if max_nodes is not None and nodes >= max_nodes:
            interrupted = True
            break
        if time_limit is not None and (nodes & 0x3F) == 0:
            if time.monotonic() - start >= time_limit:
                interrupted = True
                break

        node = stack.pop()
        nodes += 1

        # Bound pruning: strictly worse than the incumbent is useless.
        # (Equal bounds are kept: they may hide a lexicographically
        # smaller solution with the same objective.)
        if incumbent_obj is not None and node.bound > incumbent_obj:
            continue

        if node.depth == n:
            if fully_feasible(node.activities):
                better = (
                    incumbent_obj is None
                    or node.cost < incumbent_obj
                    or (node.cost == incumbent_obj and node.values < incumbent_values)
                )
                if better:
                    incumbent_obj = node.cost
                    incumbent_values = node.values
            continue

        depth = node.depth
        coef = objective[depth]
        con_coefs = [con.coeffs[depth] for con in constraints]
        # Push children in reverse value order so the smallest domain
        # value is explored first (deterministic, lex-friendly order).
        for value in reversed(problem.domains[depth]):
            child_cost = node.cost + coef * value
            child_bound = child_cost + suffix_obj_min[depth + 1]
            if incumbent_obj is not None and child_bound > incumbent_obj:
                continue
            child_activities = tuple(
                act + ccoef * value
                for act, ccoef in zip(node.activities, con_coefs)
            )
            if not constraints_can_hold(depth + 1, child_activities):
                continue
            stack.append(
                _Node(depth + 1, node.values + (value,), child_cost,
                      child_activities, child_bound)
            )

    # ---- assemble the result -------------------------------------------
    if interrupted:
        # Reliable lower bound: cheapest possible completion among all
        # unexplored frontier nodes (each node bound is a valid lower
        # bound for its subtree).
        frontier_lb = min(node.bound for node in stack)
        if incumbent_obj is not None:
            lower = min(frontier_lb, incumbent_obj)
        else:
            lower = frontier_lb
        status = Status.FEASIBLE if incumbent_obj is not None else Status.UNKNOWN
        upper = incumbent_obj
    else:
        # Search space exhausted: the incumbent (if any) is proven optimal.
        status = Status.OPTIMAL if incumbent_obj is not None else Status.INFEASIBLE
        lower = incumbent_obj  # == upper when optimal; None when infeasible
        upper = incumbent_obj

    if incumbent_values is not None:
        # Independent verification of the claimed solution against the
        # original constraints.  Never return an unchecked solution.
        ok, checked_obj = verify_solution(problem, incumbent_values)
        if not ok or checked_obj != incumbent_obj:
            raise RuntimeError(
                "internal error: incumbent failed independent verification"
            )

    return SolveResult(
        status=status,
        solution=incumbent_values,
        objective=incumbent_obj,
        lower_bound=lower,
        upper_bound=upper,
        nodes_explored=nodes,
        interrupted=interrupted,
    )
