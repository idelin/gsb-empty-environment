"""Backtracking CSP solver with GAC (generalized arc consistency)
propagation that records a fully justified event trace.

No SAT/SMT libraries; plain Python 3.11 standard library only.
"""

from __future__ import annotations

from dataclasses import dataclass

from csp import CSP, has_support
from trace import Event

SAT = "SAT"
UNSAT = "UNSAT"
UNKNOWN = "UNKNOWN"


@dataclass
class SolveResult:
    status: str                 # SAT | UNSAT | UNKNOWN
    assignment: dict | None     # complete assignment iff status == SAT
    trace: list[Event]


class _BudgetExhausted(Exception):
    pass


class Solver:
    def __init__(self, csp: CSP, max_events: int = 100_000):
        self.csp = csp
        self.max_events = max_events

    def solve(self) -> SolveResult:
        self.trace: list[Event] = []
        self.domains: dict[str, set] = {
            v: set(d) for v, d in self.csp.domains.items()
        }
        self.checkpoints: list[dict[str, set]] = []
        try:
            sat = self._search()
        except _BudgetExhausted:
            # Inconclusive must NEVER be reported as UNSAT.
            self.trace.append(Event("unknown", len(self.trace),
                                    {"reason": "event budget exhausted"}))
            return SolveResult(UNKNOWN, None, self.trace)
        if sat:
            assignment = {v: next(iter(d)) for v, d in self.domains.items()}
            self._emit("solution", {"assignment": assignment})
            return SolveResult(SAT, assignment, self.trace)
        self._emit("unsat", {})
        return SolveResult(UNSAT, None, self.trace)

    # -- internals ---------------------------------------------------------

    def _emit(self, kind: str, payload: dict) -> None:
        if len(self.trace) >= self.max_events:
            raise _BudgetExhausted
        self.trace.append(Event(kind, len(self.trace), payload))

    def _search(self) -> bool:
        conflict_var = self._propagate()
        if conflict_var is not None:
            self._emit("conflict", {"var": conflict_var})
            return False
        if all(len(d) == 1 for d in self.domains.values()):
            return True
        var = min(
            (v for v in self.domains if len(self.domains[v]) > 1),
            key=lambda v: (len(self.domains[v]), v),
        )
        branch_domain = sorted(self.domains[var])
        for value in branch_domain:
            snapshot = {v: set(d) for v, d in self.domains.items()}
            self.checkpoints.append(snapshot)
            self._emit("decide", {"var": var, "value": value})
            self.domains[var] = {value}
            if self._search():
                return True
            restored = self.checkpoints.pop()
            self.domains = restored
            self._emit("backtrack", {
                "level": len(self.checkpoints),
                "domains": {v: sorted(d) for v, d in restored.items()},
            })
        self._emit("exhausted", {"var": var})
        return False

    def _propagate(self) -> str | None:
        """GAC fixpoint. Every deleted value is logged with the constraint
        that justifies the deletion. Returns the variable whose domain
        became empty, or None."""
        changed = True
        while changed:
            changed = False
            for ci, con in enumerate(self.csp.constraints):
                for var in con.scope:
                    for value in sorted(self.domains[var]):
                        if not has_support(con, self.domains, var, value):
                            self._emit("propagate", {
                                "var": var, "value": value, "constraint": ci,
                            })
                            self.domains[var].discard(value)
                            changed = True
                            if not self.domains[var]:
                                return var
        return None
