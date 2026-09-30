"""Backtracking solver with GAC propagation and a fully auditable trail.

Every domain mutation is justified by a recorded event:
  DECIDE     branch on var = value
  PROPAGATE  remove a value that has no support in some constraint
  CONFLICT   a domain became empty
  BACKTRACK  undo the last decision, restore the snapshot, drop the value
"""
from __future__ import annotations

from dataclasses import dataclass
from enum import Enum

from .model import Problem
from .trail import BACKTRACK, CONFLICT, DECIDE, PROPAGATE, Event, Trail
from .verify import check_sat


class Status(Enum):
    SAT = "SAT"
    UNSAT = "UNSAT"
    UNKNOWN = "UNKNOWN"  # e.g. step limit hit; must never be reported as UNSAT


class StepLimitExceeded(Exception):
    pass


@dataclass
class Result:
    status: Status
    assignment: dict | None
    trail: Trail


class Solver:
    def __init__(self, problem: Problem, max_events: int | None = None):
        self.problem = problem
        self.domains: dict[str, set] = {v: set(d) for v, d in problem.domains.items()}
        self.trail = Trail()
        self.max_events = max_events
        self.depth = 0
        self._snapshots: dict[int, dict[str, set]] = {}

    # -- trail + snapshots -------------------------------------------------
    def _record(self, event: Event) -> None:
        self.trail.record(event)
        if self.max_events is not None and len(self.trail) > self.max_events:
            raise StepLimitExceeded

    def _save(self, level: int) -> None:
        self._snapshots[level] = {v: set(d) for v, d in self.domains.items()}

    def _restore(self, level: int) -> None:
        self.domains = {v: set(d) for v, d in self._snapshots[level].items()}
        for deeper in [l for l in self._snapshots if l > level]:
            del self._snapshots[deeper]

    # -- propagation --------------------------------------------------------
    def _propagate(self) -> bool:
        """Enforce generalized arc consistency. Returns False on conflict."""
        changed = True
        while changed:
            changed = False
            for con in self.problem.constraints:
                for var in con.scope:
                    for value in sorted(self.domains[var], key=repr):
                        if not con.has_support(var, value, self.domains):
                            self._record(Event(PROPAGATE, var, value, con.cid, self.depth))
                            self.domains[var].discard(value)
                            changed = True
                            if not self.domains[var]:
                                self._record(Event(CONFLICT, var, None, con.cid, self.depth))
                                return False
        return True

    # -- search ---------------------------------------------------------------
    def _pick_var(self) -> str | None:
        candidates = [v for v in self.problem.variables if len(self.domains[v]) > 1]
        if not candidates:
            return None
        return min(candidates, key=lambda v: len(self.domains[v]))

    def _search(self) -> bool:
        var = self._pick_var()
        if var is None:
            return True
        level = self.depth
        for value in sorted(self.domains[var], key=repr):
            if value not in self.domains[var]:
                continue
            self._save(level)
            self._record(Event(DECIDE, var, value, None, level))
            self.depth += 1
            self.domains[var] = {value}
            if self._propagate() and self._search():
                return True
            self.depth -= 1
            self._record(Event(BACKTRACK, var, value, None, level))
            self._restore(level)
            self.domains[var].discard(value)
        return False

    # -- entry point ----------------------------------------------------------
    def solve(self) -> Result:
        try:
            if not self._propagate():
                return Result(Status.UNSAT, None, self.trail)
            if self._search():
                assignment = {v: next(iter(d)) for v, d in self.domains.items()}
                # A SAT claim is only made after the independent checker
                # validates the complete assignment against the original
                # constraints.
                if not check_sat(self.problem, assignment):
                    raise RuntimeError("internal error: solver produced invalid SAT")
                return Result(Status.SAT, assignment, self.trail)
            return Result(Status.UNSAT, None, self.trail)
        except StepLimitExceeded:
            # Resource exhaustion is UNKNOWN, never UNSAT.
            return Result(Status.UNKNOWN, None, self.trail)
