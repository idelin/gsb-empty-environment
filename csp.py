"""Finite-domain CSP over tuple-table constraints, solved without SAT/SMT.

The solver is plain depth-first backtracking with generalized arc
consistency (GAC) maintained on positive tables:

* assigning or pruning a value synchronously invalidates the tuples that
  use it (see `TupleTable.remove_value`);
* a domain value with no supporting valid tuple is pruned;
* pruning cascades until a fixpoint or a domain wipeout;
* a trail records every change so backtracking restores state exactly.

Semantics of edge cases:

* an empty POSITIVE table allows nothing  -> the model is infeasible;
* an empty NEGATIVE table forbids nothing -> it imposes no restriction.
"""

from __future__ import annotations

from checker import verify_solution
from tuple_table import TupleTable


class TableConstraint:
    """A tuple-table constraint over an ordered scope of variables."""

    def __init__(self, scope, tuples, polarity: str = TupleTable.POSITIVE):
        self.scope = tuple(scope)
        if len(set(self.scope)) != len(self.scope):
            raise ValueError(f"duplicate variable in scope {self.scope!r}")
        self.polarity = polarity
        self.table = TupleTable(len(self.scope), tuples, polarity)
        # Original data kept verbatim for the independent checker.
        self.original = frozenset(tuple(t) for t in tuples)

    def position(self, var) -> int:
        return self.scope.index(var)


class CSP:
    def __init__(self) -> None:
        self.domains: dict = {}
        self.constraints: list[TableConstraint] = []

    def add_variable(self, name, domain) -> None:
        domain = set(domain)
        if not domain:
            raise ValueError(f"empty domain for variable {name!r}")
        if name in self.domains:
            raise ValueError(f"duplicate variable {name!r}")
        self.domains[name] = domain

    def add_constraint(self, scope, tuples,
                       polarity: str = TupleTable.POSITIVE) -> TableConstraint:
        for var in scope:
            if var not in self.domains:
                raise KeyError(f"unknown variable {var!r}")
        con = TableConstraint(scope, tuples, polarity)
        self.constraints.append(con)
        return con


class Solver:
    """Backtracking search with incremental GAC support maintenance."""

    def __init__(self, csp: CSP) -> None:
        self.csp = csp
        self.domains = {v: set(d) for v, d in csp.domains.items()}
        self.trail: list = []
        self.by_var = {v: [] for v in csp.domains}
        for con in csp.constraints:
            for var in con.scope:
                self.by_var[var].append(con)
        # Tuples mentioning values outside the initial domains can never
        # be used; invalidate them once, before search starts.
        for con in csp.constraints:
            for pos, var in enumerate(con.scope):
                for value in con.table.values_at(pos):
                    if value not in self.domains[var]:
                        con.table.remove_value(pos, value, self.trail)

    # ------------------------------------------------------------------ #
    # trail
    # ------------------------------------------------------------------ #
    def checkpoint(self) -> int:
        return len(self.trail)

    def restore(self, cp: int) -> None:
        while len(self.trail) > cp:
            entry = self.trail.pop()
            kind = entry[0]
            if kind == "dom":
                _, var, value = entry
                self.domains[var].add(value)
            else:  # "tuple"
                _, table, tid = entry
                table.restore_tuple(tid)

    # ------------------------------------------------------------------ #
    # domain pruning + propagation
    # ------------------------------------------------------------------ #
    def prune(self, var, value) -> None:
        """Remove `value` from var's domain and invalidate its tuples."""
        if value not in self.domains[var]:
            return
        self.domains[var].discard(value)
        self.trail.append(("dom", var, value))
        for con in self.by_var[var]:
            con.table.remove_value(con.position(var), value, self.trail)

    def propagate(self) -> bool:
        """Enforce GAC on positive tables; check forced negative tuples.

        Returns False on domain wipeout / violated negative constraint.
        """
        changed = True
        while changed:
            changed = False
            for con in self.csp.constraints:
                if con.polarity == TupleTable.POSITIVE:
                    if len(con.table) == 0:
                        return False  # empty positive table: infeasible
                    for pos, var in enumerate(con.scope):
                        for value in sorted(self.domains[var]):
                            if not con.table.has_support(pos, value):
                                self.prune(var, value)
                                changed = True
                                if not self.domains[var]:
                                    return False
                else:  # NEGATIVE
                    if len(con.table) == 0:
                        continue  # empty negative table: no restriction
                    if all(len(self.domains[v]) == 1 for v in con.scope):
                        proj = tuple(next(iter(self.domains[v]))
                                     for v in con.scope)
                        if proj in con.table:
                            return False
        return True

    # ------------------------------------------------------------------ #
    # search
    # ------------------------------------------------------------------ #
    def _pick_var(self):
        best = None
        for var, dom in self.domains.items():
            if len(dom) > 1 and (best is None
                                 or len(dom) < len(self.domains[best])):
                best = var
        return best

    def _backtrack(self):
        var = self._pick_var()
        if var is None:
            return {v: next(iter(s)) for v, s in self.domains.items()}
        cp = self.checkpoint()
        for value in sorted(self.domains[var]):
            for other in sorted(self.domains[var]):
                if other != value:
                    self.prune(var, other)
            if self.propagate():
                solution = self._backtrack()
                if solution is not None:
                    return solution
            self.restore(cp)
        return None

    def search(self):
        if not self.propagate():
            return None
        return self._backtrack()


def solve(csp: CSP, verify: bool = True):
    """Return a solution dict, or None if the CSP is infeasible.

    Whenever a solution is claimed, it is first validated by the
    independent checker against the original constraint data.
    """
    solution = Solver(csp).search()
    if solution is not None and verify and not verify_solution(csp, solution):
        raise RuntimeError(
            f"internal error: solver produced invalid solution {solution!r}")
    return solution
