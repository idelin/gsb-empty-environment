"""Depth-first branch & bound over finite domains, lexicographic objective.

Search: variables are assigned in a static order (most constrained first).
Hard constraints are checked with a limited lookahead (existence of an
extension over current domains). Soft constraints contribute a per-level
lower bound: an unassigned soft constraint contributes 0 if some extension
of the current partial assignment can still satisfy it, else its weight.

Lexicographic guarantee: the objective is a tuple of per-level penalties
compared lexicographically (level 0 first). A node is pruned when its lower
bound is >= the incumbent vector, so no amount of low-level gain can ever
compensate a single unit of higher-level penalty.
"""
from __future__ import annotations

import itertools
from dataclasses import dataclass
from enum import Enum
from typing import Dict, List, Optional, Tuple

from .checker import check_assignment
from .model import Model

# Cap on the Cartesian-product lookahead per constraint. Above this we stay
# conservative (assume satisfiable / zero penalty), which keeps bounds valid.
EXTENSION_LIMIT = 100_000


class Status(Enum):
    OPTIMAL = "optimal"
    UNSAT = "unsat"
    UNKNOWN = "unknown"  # node limit reached before proving anything


@dataclass(frozen=True)
class PenaltyTerm:
    """One violated soft constraint, for explanation purposes."""

    constraint: str
    level: int
    weight: int
    scope_values: Dict[str, int]


@dataclass(frozen=True)
class LevelSummary:
    """Per-level penalty with its certified lower bound.

    For an OPTIMAL solve, ``certified_lower_bound`` equals ``penalty``:
    branch & bound has exhaustively proven that no assignment can do better
    on this level while respecting all higher levels and hard constraints.
    For UNKNOWN (node limit hit), the bound is the best proven so far.
    """

    level: int
    penalty: int
    certified_lower_bound: Optional[int]
    terms: List[PenaltyTerm]


@dataclass(frozen=True)
class SolveResult:
    status: Status
    assignment: Optional[Dict[str, int]]
    objective: Optional[Tuple[int, ...]]
    levels: List[LevelSummary]
    nodes: int
    checked: bool  # True iff the independent checker verified the assignment

    def explain(self) -> str:
        lines: List[str] = []
        if self.status is Status.UNSAT:
            lines.append(
                "status: unsat -- the hard constraints are infeasible; "
                "hard conflicts cannot be bought off with penalties"
            )
            lines.append(f"search nodes: {self.nodes}")
            return "\n".join(lines)
        if self.assignment is None:
            lines.append("status: unknown -- node limit reached, no incumbent")
            lines.append(f"search nodes: {self.nodes}")
            return "\n".join(lines)

        proven = "proven optimal" if self.status is Status.OPTIMAL else "incumbent (not proven)"
        checked = "verified by independent checker" if self.checked else "NOT VERIFIED"
        lines.append(f"status: {self.status.value} ({proven}, {checked})")
        lines.append(
            f"lexicographic objective (level 0 = highest priority): {self.objective}"
        )
        lines.append(f"search nodes: {self.nodes}")
        lines.append("assignment: " + ", ".join(
            f"{k}={v}" for k, v in sorted(self.assignment.items())
        ))
        for ls in self.levels:
            bound = (
                str(ls.certified_lower_bound)
                if ls.certified_lower_bound is not None
                else "unproven"
            )
            lines.append(
                f"level {ls.level}: penalty={ls.penalty}, "
                f"certified lower bound={bound}"
            )
            if not ls.terms:
                lines.append("  (no violations)")
            for t in ls.terms:
                scope = ", ".join(f"{k}={v}" for k, v in sorted(t.scope_values.items()))
                lines.append(
                    f"  - {t.constraint}: weight {t.weight}, violated with {{{scope}}}"
                )
        return "\n".join(lines)


class _NodeLimitHit(Exception):
    pass


class Solver:
    def __init__(self, model: Model, node_limit: int = 1_000_000):
        self.model = model
        self.node_limit = node_limit
        self.levels = model.num_levels
        degree = {name: 0 for name in model.variables}
        for c in itertools.chain(model.hard, model.soft):
            for v in c.var_names:
                degree[v] += 1
        self.order = sorted(model.variables, key=lambda v: (-degree[v], v))

    # -- feasibility / bound helpers -------------------------------------

    def _exists_extension(self, c, assignment: Dict[str, int]) -> bool:
        """True if some completion of the constraint's scope satisfies it."""
        unassigned = [v for v in c.var_names if v not in assignment]
        domains = [self.model.variables[v].domain for v in unassigned]
        size = 1
        for d in domains:
            size *= len(d)
        if size > EXTENSION_LIMIT:
            return True  # conservative: assume satisfiable
        for combo in itertools.product(*domains):
            ext = dict(zip(unassigned, combo))
            vals = tuple(
                assignment[v] if v in assignment else ext[v] for v in c.var_names
            )
            if c.predicate(*vals):
                return True
        return False

    def _hard_ok(self, assignment: Dict[str, int]) -> bool:
        for c in self.model.hard:
            if all(v in assignment for v in c.var_names):
                vals = tuple(assignment[v] for v in c.var_names)
                if not c.predicate(*vals):
                    return False
            elif not self._exists_extension(c, assignment):
                return False
        return True

    def _soft_lower_bound(self, assignment: Dict[str, int]) -> Tuple[int, ...]:
        lb = [0] * self.levels
        for c in self.model.soft:
            if all(v in assignment for v in c.var_names):
                vals = tuple(assignment[v] for v in c.var_names)
                if not c.predicate(*vals):
                    lb[c.level] += c.weight
            elif not self._exists_extension(c, assignment):
                lb[c.level] += c.weight
        return tuple(lb)

    def _penalty_vector(self, assignment: Dict[str, int]) -> Tuple[int, ...]:
        vec = [0] * self.levels
        for c in self.model.soft:
            vals = tuple(assignment[v] for v in c.var_names)
            if not c.predicate(*vals):
                vec[c.level] += c.weight
        return tuple(vec)

    # -- search ------------------------------------------------------------

    def solve(self) -> SolveResult:
        assignment: Dict[str, int] = {}
        best_vec: Optional[Tuple[int, ...]] = None
        best_assign: Optional[Dict[str, int]] = None
        nodes = 0
        limit_hit = False

        def dfs(idx: int) -> None:
            nonlocal nodes, best_vec, best_assign
            nodes += 1
            if nodes > self.node_limit:
                raise _NodeLimitHit
            lb = self._soft_lower_bound(assignment)
            if best_vec is not None and lb >= best_vec:
                return  # lexicographic prune: cannot strictly improve
            if idx == len(self.order):
                vec = self._penalty_vector(assignment)
                if best_vec is None or vec < best_vec:
                    best_vec = vec
                    best_assign = dict(assignment)
                return
            var = self.order[idx]
            for value in self.model.variables[var].domain:
                assignment[var] = value
                if self._hard_ok(assignment):
                    dfs(idx + 1)
                del assignment[var]

        try:
            dfs(0)
        except _NodeLimitHit:
            limit_hit = True

        if best_assign is None:
            status = Status.UNKNOWN if limit_hit else Status.UNSAT
            return SolveResult(status, None, None, [], nodes, checked=False)

        # Independent verification against the original constraints before
        # we are allowed to claim this solution.
        check = check_assignment(self.model, best_assign)
        if not check.hard_ok or check.penalties != best_vec:
            raise RuntimeError(
                "internal error: independent checker rejected solver output "
                f"(hard_ok={check.hard_ok}, checker penalties={check.penalties}, "
                f"solver objective={best_vec})"
            )

        status = Status.UNKNOWN if limit_hit else Status.OPTIMAL
        terms_by_level: List[List[PenaltyTerm]] = [[] for _ in range(self.levels)]
        for v in check.violations:
            terms_by_level[v.level].append(
                PenaltyTerm(
                    constraint=v.constraint,
                    level=v.level,
                    weight=v.weight,
                    scope_values=v.scope_values,
                )
            )
        levels = [
            LevelSummary(
                level=i,
                penalty=best_vec[i],
                certified_lower_bound=best_vec[i] if status is Status.OPTIMAL else None,
                terms=terms_by_level[i],
            )
            for i in range(self.levels)
        ]
        return SolveResult(
            status=status,
            assignment=best_assign,
            objective=best_vec,
            levels=levels,
            nodes=nodes,
            checked=True,
        )
