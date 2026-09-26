"""A self-contained DPLL SAT solver with unit propagation and two-watched literals.

Pure Python (3.11+), standard library only.  No SAT/SMT library is used.

Features
--------
- DPLL search with chronological backtracking over a finite (Boolean) domain.
- Unit propagation driven by the two-watched-literal scheme.
- Assumption-based solving: ``solve(assumptions=[...])`` temporarily asserts
  literals; every call retracts them before returning (the solver is always
  back at decision level 0), so "solve / retract / solve again" works.
- Immediate UNSAT when an empty clause is added; tautological clauses are
  ignored; an empty formula is trivially SAT.
- Every SAT claim is verified by an independent checker against the original
  clauses exactly as they were added.
- UNSAT results carry a human-readable explanation stating that the search
  space was exhausted (with statistics).

Watch-state invariant and backtracking
--------------------------------------
Each clause watches two of its literals (kept at positions 0 and 1).  The
maintained invariant is: *a watched literal is never false unless the clause
is already unit or conflicting under the current assignment*.  Un-assigning a
variable can only turn a false literal into an unassigned one, never the
other way around, so the invariant survives backtracking **without touching
the watch lists**.  That is why ``_backtrack`` only clears trail assignments
and clamps the propagation queue head: no watch restoration is needed, and no
propagation can be missed, because every future assignment re-scans exactly
the watch lists of the literals it falsifies.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from typing import Iterable, Optional


class Status(Enum):
    SAT = "SAT"
    UNSAT = "UNSAT"


@dataclass
class Stats:
    """Search statistics for a single ``solve`` call."""

    decisions: int = 0
    propagations: int = 0
    conflicts: int = 0
    backtracks: int = 0
    max_depth: int = 0


@dataclass
class SolveResult:
    status: Status
    model: Optional[dict[int, bool]]
    verified: bool  # True iff the model passed the independent checker
    stats: Stats
    explanation: str

    @property
    def sat(self) -> bool:
        return self.status is Status.SAT

    def __str__(self) -> str:  # pragma: no cover - cosmetic
        return f"{self.status.value}: {self.explanation}"


# ---------------------------------------------------------------------------
# Independent checker (deliberately separate from the solver internals).
# ---------------------------------------------------------------------------

def check_model(clauses: Iterable[Iterable[int]], model: dict[int, bool]) -> bool:
    """Return True iff ``model`` satisfies every clause in ``clauses``.

    This is a plain, direct evaluation of the original constraints; it shares
    no state with the solver, so it can be trusted to validate a SAT claim.
    """
    for clause in clauses:
        if not any(model[abs(lit)] == (lit > 0) for lit in clause):
            return False
    return True


def first_violated(clauses: Iterable[Iterable[int]], model: dict[int, bool]) -> Optional[int]:
    """Return the index of the first clause falsified by ``model``, or None."""
    for idx, clause in enumerate(clauses):
        if not any(model[abs(lit)] == (lit > 0) for lit in clause):
            return idx
    return None


# ---------------------------------------------------------------------------
# Solver
# ---------------------------------------------------------------------------

class Solver:
    """DPLL solver with two-watched literals and retractable assumptions."""

    def __init__(self) -> None:
        self.num_vars = 0
        self.original_clauses: list[list[int]] = []  # exactly as added
        self.clauses: list[list[int]] = []           # working set (no tautologies)
        self.watches: dict[int, list[int]] = {}      # literal -> clause ids watching it
        self.assign: list[int] = [0]                 # 1-based: 0 unset, 1 true, -1 false
        self.level: list[int] = [0]                  # decision level per variable
        self.trail: list[int] = []                   # assigned literals in order
        self.trail_lim: list[int] = []               # trail offsets per decision level
        self.qhead = 0                               # propagation queue head into trail
        self.decision_level = 0
        self.root_unsat = False                      # empty clause / contradictory units
        self.stats = Stats()

    # -- problem construction ------------------------------------------------

    def new_var(self) -> int:
        self.num_vars += 1
        self.assign.append(0)
        self.level.append(0)
        return self.num_vars

    def _ensure_var(self, var: int) -> None:
        while self.num_vars < var:
            self.new_var()

    def add_clause(self, literals: Iterable[int]) -> Optional[int]:
        """Add a clause.  Returns the working clause id, or None if ignored.

        - An empty clause makes the whole formula UNSAT immediately.
        - Tautological clauses (containing both ``x`` and ``-x``) are ignored.
        - Duplicate literals are removed.
        """
        literals = list(literals)
        for lit in literals:
            if lit == 0:
                raise ValueError("literal 0 is not a valid variable")
        self.original_clauses.append(list(literals))  # kept for the checker
        if self.root_unsat:
            return None
        clause: list[int] = []
        seen: set[int] = set()
        tautology = False
        for lit in literals:
            self._ensure_var(abs(lit))
            if -lit in seen:
                tautology = True
            elif lit not in seen:
                seen.add(lit)
                clause.append(lit)
        if tautology:
            return None  # tautologies are always satisfied: safe to ignore
        if not clause:
            self.root_unsat = True  # empty clause -> immediate UNSAT
            return None
        if self.decision_level != 0:
            raise RuntimeError("clauses may only be added at decision level 0")
        cid = len(self.clauses)
        self.clauses.append(clause)
        if len(clause) == 1:
            lit = clause[0]
            self.watches.setdefault(lit, []).append(cid)
            if not self._enqueue(lit):  # contradictory unit clauses at level 0
                self.root_unsat = True
        else:
            self.watches.setdefault(clause[0], []).append(cid)
            self.watches.setdefault(clause[1], []).append(cid)
        return cid

    @property
    def n_working_clauses(self) -> int:
        return len(self.clauses)

    # -- assignment primitives ------------------------------------------------

    def _lit_value(self, lit: int) -> int:
        """1 if true, -1 if false, 0 if unassigned."""
        val = self.assign[abs(lit)]
        if val == 0:
            return 0
        return 1 if (val > 0) == (lit > 0) else -1

    def _enqueue(self, lit: int) -> bool:
        var = abs(lit)
        wanted = 1 if lit > 0 else -1
        current = self.assign[var]
        if current != 0:
            return current == wanted
        self.assign[var] = wanted
        self.level[var] = self.decision_level
        self.trail.append(lit)
        return True

    def _new_decision_level(self) -> None:
        self.decision_level += 1
        self.trail_lim.append(len(self.trail))
        self.stats.decisions += 1
        self.stats.max_depth = max(self.stats.max_depth, self.decision_level)

    def _backtrack(self, target: int) -> None:
        """Undo everything above ``target``.  Watch lists are intentionally
        left untouched: un-assignment preserves the watch invariant (see the
        module docstring), so no propagation is lost."""
        if self.decision_level <= target:
            return
        self.stats.backtracks += 1
        cut = self.trail_lim[target]
        for lit in self.trail[cut:]:
            self.assign[abs(lit)] = 0
            self.level[abs(lit)] = 0
        del self.trail[cut:]
        del self.trail_lim[target:]
        self.decision_level = target
        self.qhead = min(self.qhead, len(self.trail))

    # -- unit propagation with two-watched literals ---------------------------

    def _propagate(self) -> Optional[int]:
        """Propagate until quiescence.  Returns a conflicting clause id or None."""
        while self.qhead < len(self.trail):
            lit = self.trail[self.qhead]
            self.qhead += 1
            self.stats.propagations += 1
            false_lit = -lit  # clauses watching false_lit may have changed state
            watch_list = self.watches.get(false_lit)
            if not watch_list:
                continue
            i = 0
            while i < len(watch_list):
                cid = watch_list[i]
                clause = self.clauses[cid]
                if len(clause) == 1:
                    # The only watched literal just became false.
                    self.stats.conflicts += 1
                    return cid
                # Keep the false watched literal at position 1.
                if clause[0] == false_lit:
                    clause[0], clause[1] = clause[1], clause[0]
                other = clause[0]
                if self._lit_value(other) == 1:
                    i += 1  # clause already satisfied by its other watch
                    continue
                # Try to move the watch to a non-false literal.
                moved = False
                for k in range(2, len(clause)):
                    if self._lit_value(clause[k]) != -1:
                        clause[1], clause[k] = clause[k], clause[1]
                        self.watches.setdefault(clause[1], []).append(cid)
                        watch_list[i] = watch_list[-1]
                        watch_list.pop()
                        moved = True
                        break
                if moved:
                    continue  # re-examine the clause swapped into slot i
                # No replacement: the clause is unit or conflicting.
                if self._lit_value(other) == -1:
                    self.stats.conflicts += 1
                    return cid
                if not self._enqueue(other):
                    self.stats.conflicts += 1
                    return cid
                i += 1
        return None

    # -- DPLL search -----------------------------------------------------------

    def _pick_branching_var(self) -> Optional[int]:
        for var in range(1, self.num_vars + 1):
            if self.assign[var] == 0:
                return var
        return None

    def _dpll(self) -> bool:
        if self._propagate() is not None:
            return False
        var = self._pick_branching_var()
        if var is None:
            return True  # every variable assigned, no clause violated
        base_level = self.decision_level
        for sign in (1, -1):
            self._new_decision_level()
            self._enqueue(sign * var)
            if self._dpll():
                return True
            self._backtrack(base_level)
        return False

    # -- public solving API -----------------------------------------------------

    def solve(self, assumptions: Iterable[int] = ()) -> SolveResult:
        """Solve the current formula, optionally under temporary assumptions.

        Assumptions are asserted only for this call and are always retracted
        before returning, so the solver is reusable afterwards.
        """
        self.stats = Stats()
        if self.root_unsat:
            return SolveResult(
                Status.UNSAT, None, False, self.stats,
                "UNSAT at decision level 0: an empty clause or contradictory "
                "unit clauses were added; no search is needed.")
        if self.decision_level != 0:
            raise RuntimeError("solve() must be called at decision level 0")

        assumptions = list(assumptions)
        for lit in assumptions:
            if lit == 0:
                raise ValueError("literal 0 is not a valid assumption")
            self._ensure_var(abs(lit))

        failed = False
        if assumptions:
            self._new_decision_level()
            for lit in assumptions:
                if not self._enqueue(lit):
                    failed = True
                    break
            if not failed and self._propagate() is not None:
                failed = True
        if failed:
            self._backtrack(0)
            return SolveResult(
                Status.UNSAT, None, False, self.stats,
                f"UNSAT under assumptions {assumptions}: unit propagation "
                f"derives a contradiction at the assumption level "
                f"({self.stats.propagations} propagations); the search tree "
                f"is empty, so no satisfying assignment exists under these "
                f"assumptions.")

        sat = self._dpll()
        if sat:
            model = {v: self.assign[v] == 1 for v in range(1, self.num_vars + 1)}
            verified = check_model(self.original_clauses, model)
            self._backtrack(0)  # retract assumptions and search state
            if not verified:
                bad = first_violated(self.original_clauses, model)
                raise RuntimeError(
                    f"internal error: solver produced a model that fails the "
                    f"independent checker (clause {bad}: "
                    f"{self.original_clauses[bad]})")
            return SolveResult(
                Status.SAT, model, True, self.stats,
                f"SAT: assignment found and independently verified against "
                f"all {len(self.original_clauses)} original clauses "
                f"({self.stats.decisions} decisions, "
                f"{self.stats.propagations} propagations).")

        self._backtrack(0)  # retract assumptions and search state
        scope = (f" under assumptions {assumptions}" if assumptions else "")
        return SolveResult(
            Status.UNSAT, None, False, self.stats,
            f"UNSAT{scope}: exhaustive DPLL search completed - "
            f"{self.stats.decisions} decisions, {self.stats.conflicts} "
            f"conflicts, {self.stats.propagations} unit propagations, "
            f"{self.stats.backtracks} backtracks, maximum depth "
            f"{self.stats.max_depth}. Every branch of the search tree ends "
            f"in a conflict, so no satisfying assignment exists{scope}.")


# ---------------------------------------------------------------------------
# Finite-domain variables encoded into CNF (one-hot / exactly-one encoding).
# ---------------------------------------------------------------------------

class FiniteDomainEncoder:
    """Encode finite-domain variables as exactly-one groups of Boolean vars."""

    def __init__(self, solver: Solver) -> None:
        self.solver = solver
        self.variables: dict[str, dict[object, int]] = {}

    def new_variable(self, name: str, domain: Iterable[object]) -> dict[object, int]:
        domain = list(domain)
        if not domain:
            raise ValueError("domain must be non-empty")
        if name in self.variables:
            raise ValueError(f"variable {name!r} already defined")
        lits = {value: self.solver.new_var() for value in domain}
        # at least one value
        self.solver.add_clause([lits[value] for value in domain])
        # at most one value (pairwise)
        for i in range(len(domain)):
            for j in range(i + 1, len(domain)):
                self.solver.add_clause([-lits[domain[i]], -lits[domain[j]]])
        self.variables[name] = lits
        return lits

    def literal(self, name: str, value: object) -> int:
        return self.variables[name][value]

    def decode(self, name: str, model: dict[int, bool]) -> object:
        for value, lit in self.variables[name].items():
            if model[abs(lit)] == (lit > 0):
                return value
        return None  # pragma: no cover - cannot happen for a verified model
