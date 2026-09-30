"""Branch-and-bound solver for finite-domain integer minimization.

Design guarantees
-----------------
* Only exact integer arithmetic is used; no floating point, no SAT/SMT libs.
* The lower bound of a node is a *relaxation* (constraints ignored, each
  objective term minimised independently over the current domain), so a
  reported lower bound never exceeds the true optimum.
* Domain propagation is bound-consistent and sound: it only removes values
  that cannot appear in *any* feasible completion, so it never changes the
  set of feasible solutions (needed for a correct "proven infeasible").
* Search enumerates leaves in lexicographic order of the variable order, so
  among all optimal solutions the lexicographically smallest one (by the
  declared variable order) is returned -- tie-breaking is stable.
* A budget (node count and/or wall-clock seconds) may interrupt the search.
  The result then reports the best known feasible solution together with a
  proven lower bound and the gap, with status FEASIBLE or UNKNOWN -- it
  never claims optimality without a proof.
* Every incumbent is validated by an independent checker (checker.py)
  against the *original* constraints before it is accepted/returned.
"""
from __future__ import annotations

import time
from dataclasses import dataclass
from enum import Enum
from typing import Iterable, Mapping, Optional, Sequence

import checker


class Status(str, Enum):
    OPTIMAL = "optimal"        # incumbent proven optimal (search exhausted)
    FEASIBLE = "feasible"      # feasible solution known, optimality NOT proven
    INFEASIBLE = "infeasible"  # PROVEN: no feasible solution exists
    UNKNOWN = "unknown"        # no feasible solution found, infeasibility NOT proven


_VALID_SENSES = ("<=", ">=", "==")


@dataclass(frozen=True)
class Constraint:
    """Linear constraint  sum(coeffs[v] * x_v)  <sense>  rhs."""
    coeffs: tuple  # tuple of (name, int coefficient), sorted by name
    sense: str
    rhs: int

    @staticmethod
    def make(coeffs: Mapping[str, int], sense: str, rhs: int) -> "Constraint":
        if sense not in _VALID_SENSES:
            raise ValueError(f"invalid sense {sense!r}; expected one of {_VALID_SENSES}")
        items = tuple(sorted(((str(k), _as_int(v)) for k, v in coeffs.items()),
                             key=lambda kv: kv[0]))
        return Constraint(items, sense, _as_int(rhs))


def _as_int(value) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise ValueError(f"expected int, got {value!r}")
    return value


class Problem:
    """Finite-domain integer minimization problem.

    domains:   ordered mapping variable name -> iterable of allowed ints.
               Insertion order defines the variable order used for
               lexicographic tie-breaking.
    objective: mapping variable name -> integer cost coefficient
               (variables absent from it have coefficient 0).
    constraints: iterable of Constraint or (coeffs, sense, rhs) triples.
    """

    def __init__(self,
                 domains: Mapping[str, Iterable[int]],
                 objective: Mapping[str, int],
                 constraints: Iterable = ()):
        if not domains:
            raise ValueError("problem needs at least one variable")
        self.domains: dict[str, tuple[int, ...]] = {}
        for name, values in domains.items():
            vals = tuple(sorted({_as_int(v) for v in values}))
            self.domains[str(name)] = vals  # empty tuple allowed -> trivially infeasible
        self.variable_names: tuple[str, ...] = tuple(self.domains.keys())
        self.objective = {str(k): _as_int(v) for k, v in objective.items()}
        for name in self.objective:
            if name not in self.domains:
                raise ValueError(f"objective references unknown variable {name!r}")
        self.constraints: list[Constraint] = []
        for con in constraints:
            if not isinstance(con, Constraint):
                coeffs, sense, rhs = con
                con = Constraint.make(coeffs, sense, rhs)
            for name, _ in con.coeffs:
                if name not in self.domains:
                    raise ValueError(f"constraint references unknown variable {name!r}")
            self.constraints.append(con)


@dataclass(frozen=True)
class SolveResult:
    status: Status
    assignment: Optional[dict]        # verified incumbent, if any
    objective_value: Optional[int]    # == upper bound on the optimum
    lower_bound: Optional[int]        # proven lower bound on the optimum
    upper_bound: Optional[int]
    absolute_gap: Optional[int]       # upper_bound - lower_bound
    relative_gap: Optional[float]
    nodes_explored: int
    verified: bool                       # incumbent passed the independent checker
    message: str

    def __str__(self) -> str:
        lines = [f"status          : {self.status.value}",
                 f"objective (UB)  : {self.objective_value}",
                 f"lower bound     : {self.lower_bound}",
                 f"absolute gap    : {self.absolute_gap}",
                 f"relative gap    : {self.relative_gap}",
                 f"nodes explored  : {self.nodes_explored}",
                 f"checker verified: {self.verified}",
                 f"assignment      : {self.assignment}",
                 f"message         : {self.message}"]
        return "\n".join(lines)


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------

def _normalize_rows(problem: Problem) -> list:
    """Rewrite every constraint as  sum(a_i * x_i) <= rhs  rows."""
    rows = []
    for con in problem.constraints:
        if con.sense == "<=":
            rows.append((con.coeffs, con.rhs))
        elif con.sense == ">=":
            rows.append((tuple((n, -a) for n, a in con.coeffs), -con.rhs))
        else:  # "==" -> two rows
            rows.append((con.coeffs, con.rhs))
            rows.append((tuple((n, -a) for n, a in con.coeffs), -con.rhs))
    return rows


def _propagate(domains: dict, rows) -> Optional[dict]:
    """Sound bound-consistency filtering.

    For  sum(a_i x_i) <= rhs  a value v of x_i is removed only if
    a_i * v > rhs - (min contribution of all other terms), i.e. v cannot
    appear in ANY satisfying assignment of that single constraint.  This
    never removes a value of a feasible solution.  Returns None iff some
    domain becomes empty (node provably infeasible).
    """
    domains = {k: list(v) for k, v in domains.items()}
    changed = True
    while changed:
        changed = False
        for coeffs, rhs in rows:
            mins = []
            total_min = 0
            for name, coef in coeffs:
                d = domains[name]
                m = coef * d[0] if coef >= 0 else coef * d[-1]
                mins.append(m)
                total_min += m
            if total_min > rhs:
                return None
            for (name, coef), m in zip(coeffs, mins):
                if coef == 0:
                    continue
                limit = rhs - (total_min - m)
                d = domains[name]
                if coef > 0:
                    hi = limit // coef  # floor division: exact for ints
                    nd = [v for v in d if v <= hi]
                else:
                    lo = -((-limit) // coef)  # ceil(limit / coef)
                    nd = [v for v in d if v >= lo]
                if not nd:
                    return None
                if len(nd) != len(d):
                    domains[name] = nd
                    changed = True
    return domains


def _lower_bound(domains: dict, objective: Mapping[str, int]) -> int:
    """Relaxation bound: minimise each objective term independently.

    Valid because every completion's objective is >= the sum of the
    per-term minima, hence never exceeds the true optimum of the subtree.
    """
    total = 0
    for name, coef in objective.items():
        d = domains[name]
        total += coef * d[0] if coef >= 0 else coef * d[-1]
    return total


# ---------------------------------------------------------------------------
# Solver
# ---------------------------------------------------------------------------

def solve(problem: Problem,
          max_nodes: Optional[int] = None,
          max_seconds: Optional[float] = None) -> SolveResult:
    """Minimise the objective.  Budgets may interrupt the search.

    The returned status distinguishes all four cases:
    OPTIMAL / FEASIBLE (incumbent, no proof) / INFEASIBLE (proven) /
    UNKNOWN (nothing found, no proof).
    """
    start = time.monotonic()
    names = problem.variable_names
    rows = _normalize_rows(problem)

    def result(status, assignment, lb, nodes, verified, message):
        ub = None if assignment is None else sum(
            problem.objective.get(n, 0) * assignment[n] for n in names)
        abs_gap = rel_gap = None
        if ub is not None and lb is not None:
            abs_gap = ub - lb
            rel_gap = abs_gap / max(1, abs(ub))
        return SolveResult(status=status,
                           assignment=assignment,
                           objective_value=ub,
                           lower_bound=lb,
                           upper_bound=ub,
                           absolute_gap=abs_gap,
                           relative_gap=rel_gap,
                           nodes_explored=nodes,
                           verified=verified,
                           message=message)

    if any(len(problem.domains[n]) == 0 for n in names):
        return result(Status.INFEASIBLE, None, None, 0, False,
                      "empty domain: proven infeasible")

    root = _propagate({n: list(problem.domains[n]) for n in names}, rows)
    if root is None:
        return result(Status.INFEASIBLE, None, None, 0, False,
                      "root propagation proved infeasibility")
    root_lb = _lower_bound(root, problem.objective)

    best_obj: Optional[int] = None
    best_key: Optional[tuple] = None
    best_assign: Optional[dict] = None
    verified = False

    # Stack entries: (domains, lb) where lb is a valid lower bound for the
    # whole subtree (inherited from the parent; recomputed tighter on pop).
    stack: list = [(root, root_lb)]
    nodes = 0
    interrupted = False

    while stack:
        if (max_nodes is not None and nodes >= max_nodes) or \
           (max_seconds is not None and time.monotonic() - start >= max_seconds):
            interrupted = True
            break
        domains, _inherited_lb = stack.pop()
        nodes += 1
        domains = _propagate(domains, rows)
        if domains is None:
            continue
        lb = _lower_bound(domains, problem.objective)
        # Prune: no completion can beat (or lexicographically tie-beat) the
        # incumbent.  Leaves are enumerated in lexicographic order, so the
        # first optimal leaf found is already the lexicographically smallest
        # optimal solution; equal-objective leaves found later can be skipped.
        if best_obj is not None and lb >= best_obj:
            continue
        if all(len(domains[n]) == 1 for n in names):
            cand = {n: domains[n][0] for n in names}
            obj = sum(problem.objective.get(n, 0) * cand[n] for n in names)
            key = tuple(cand[n] for n in names)
            if best_obj is None or obj < best_obj or (obj == best_obj and key < best_key):
                report = checker.check_assignment(problem, cand)
                if not report.ok or report.objective != obj:
                    raise RuntimeError(
                        "internal error: solver candidate rejected by independent "
                        f"checker: {report.violations}")
                best_obj, best_key, best_assign = obj, key, cand
                verified = True
            continue
        # Branch on the first non-fixed variable (fixed order -> lexicographic
        # leaf order); push reversed so smaller values are explored first.
        var = next(n for n in names if len(domains[n]) > 1)
        for v in reversed(domains[var]):
            child = dict(domains)
            child[var] = [v]
            stack.append((child, lb))

    if not interrupted:
        if best_obj is None:
            return result(Status.INFEASIBLE, None, None, nodes, False,
                          "search exhausted: proven infeasible")
        return result(Status.OPTIMAL, best_assign, best_obj, nodes, verified,
                      "search exhausted: proven optimal")

    # Budget exhausted: proven global lower bound = min over open nodes,
    # capped by the incumbent (optimum <= incumbent always holds).
    open_lb = min(entry_lb for _, entry_lb in stack)
    lb_out = open_lb if best_obj is None else min(best_obj, open_lb)
    if best_obj is None:
        return result(Status.UNKNOWN, None, lb_out, nodes, False,
                      "budget exhausted: no feasible solution found, "
                      "infeasibility not proven")
    return result(Status.FEASIBLE, best_assign, lb_out, nodes, verified,
                  "budget exhausted: returning best known solution; "
                  "optimality NOT proven")
