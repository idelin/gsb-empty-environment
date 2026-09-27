"""DPLL SAT solver with unit propagation and two-watched literals.

Literals are signed integers: variable *v* is ``v`` and its negation is
``-v`` (v >= 1).  A clause is a list of literals (their disjunction), a
formula is a list of clauses (their conjunction).

Features
--------
* Classic DPLL search (chronological backtracking over a finite Boolean
  variable domain 1..n).
* Unit propagation driven by the two-watched-literal scheme.  Watches only
  move forward when a watched literal becomes false, so they never need to be
  undone on backtrack; the trail/propagation frontier are restored instead.
* Assumption solving with automatic retraction: ``solve(assumptions=[...])``
  never mutates the persistent state, so the same solver can be queried
  repeatedly under different hypotheses.
* Every SAT answer is checked against the *original* constraints with an
  independent evaluator (``verify_model``).

Standard library only; Python 3.11.
"""

from __future__ import annotations

import sys
from collections import defaultdict


# ---------------------------------------------------------------------------
# Independent model checker
# ---------------------------------------------------------------------------


def verify_model(clauses, model):
    """Evaluate every clause of the original CNF directly against ``model``.

    ``model`` maps a variable id (positive int) to a plain Python bool and
    must be total for every variable that appears in ``clauses``.

    Returns ``True`` iff every clause has at least one true literal.
    An empty clause makes the formula immediately false; the empty formula
    (no clauses at all) is trivially true.
    """
    for clause in clauses:
        satisfied = False
        for lit in clause:
            value = model.get(abs(lit))
            if value is None:
                raise ValueError(f"incomplete model: variable {abs(lit)} is unassigned")
            if value == (lit > 0):
                satisfied = True
                break
        if not satisfied:
            return False
    return True


def brute_force_sat(clauses, num_vars):
    """Exhaustive reference solver: try all 2**num_vars total assignments.

    Returns a satisfying assignment ``{var: bool}`` or ``None``.  Used only
    by the test-suite to cross-validate the DPLL solver on small instances.
    """
    for bits in range(1 << num_vars):
        model = {v: bool((bits >> (v - 1)) & 1) for v in range(1, num_vars + 1)}
        if verify_model(clauses, model):
            return model
    return None


# ---------------------------------------------------------------------------
# DPLL solver
# ---------------------------------------------------------------------------


class DPLLSolver:
    """DPLL solver with watched-literal unit propagation."""

    def __init__(self, num_vars=0):
        self.num_vars = num_vars
        self.original_clauses = []          # clauses exactly as added
        self.clauses = []                   # live clauses (tautologies skipped)
        self.wpos = []                      # watched positions per live clause
        self.watchlist = defaultdict(list)  # literal -> clause indices
        self.assign = [None] * (num_vars + 1)
        self.trail = []                     # literals made true, in order
        self.trail_lim = []                 # trail start of each decision level
        self.qhead = 0                      # propagation frontier in trail
        self._root_unsat = False            # permanent contradiction at level 0

    # -- clause management -------------------------------------------------

    def add_clause(self, literals):
        """Add one clause (iterable of nonzero literals).

        Tautological clauses (containing both v and -v) are ignored because
        they are always true; an empty clause marks the whole formula UNSAT.
        Unit clauses are immediately propagated at decision level 0.
        """
        clause = list(dict.fromkeys(literals))  # deduplicate, keep order
        if any(lit == 0 for lit in clause):
            raise ValueError("0 is not a valid literal (use v / -v with v >= 1)")
        self.original_clauses.append(list(clause))

        max_var = max((abs(lit) for lit in clause), default=0)
        if max_var > self.num_vars:
            self._grow(max_var)

        signs = set(clause)
        if any(-lit in signs for lit in signs):
            return                          # tautology: always satisfied

        if not clause:
            self._root_unsat = True         # empty clause -> immediate UNSAT
            return

        if self._root_unsat:
            return

        if len(clause) == 1:
            if not self._enqueue(clause[0]):
                self._root_unsat = True
            return

        idx = len(self.clauses)
        self.clauses.append(clause)

        # Prefer watching literals that are not currently false.  This keeps
        # the watched-literal invariant valid even when clauses are added
        # incrementally after some level-0 units were already propagated.
        not_false = [
            pos for pos, lit in enumerate(clause)
            if self._lit_value(lit) is not False
        ]
        if len(not_false) >= 2:
            watches = [not_false[0], not_false[1]]
        elif len(not_false) == 1:
            other = 1 if not_false[0] == 0 else 0
            watches = [not_false[0], other]
        else:
            watches = [0, 1]
        self.wpos.append(watches)
        self.watchlist[clause[watches[0]]].append(idx)
        self.watchlist[clause[watches[1]]].append(idx)

        if not not_false:
            self._root_unsat = True
        elif len(not_false) == 1 and self._lit_value(clause[not_false[0]]) is None:
            if not self._enqueue(clause[not_false[0]]):
                self._root_unsat = True

    def _grow(self, new_size):
        self.num_vars = new_size
        self.assign.extend([None] * (new_size + 1 - len(self.assign)))

    # -- assignment / trail -----------------------------------------------

    def _lit_value(self, lit):
        """Value of a literal under the current partial assignment."""
        value = self.assign[abs(lit)]
        if value is None:
            return None
        return value if lit > 0 else not value

    def _enqueue(self, lit):
        """Make literal ``lit`` true.  Returns False on direct contradiction."""
        var = abs(lit)
        value = lit > 0
        current = self.assign[var]
        if current is not None:
            return current == value
        self.assign[var] = value
        self.trail.append(lit)
        return True

    def _backtrack(self, level):
        """Undo every assignment above decision level ``level``.

        Watch state is intentionally left untouched: two-watched literals
        only move while a literal becomes false, and unassigning can only
        turn false literals back into unassigned ones, so every moved watch
        remains valid.  Resetting ``qhead`` to the surviving trail length is
        sound because all surviving (level <= ``level``) literals were fully
        propagated before the abandoned decision was made.
        """
        if level < len(self.trail_lim):
            start = self.trail_lim[level]
            for lit in self.trail[start:]:
                self.assign[abs(lit)] = None
            del self.trail[start:]
            del self.trail_lim[level:]
        self.qhead = len(self.trail)

    # -- unit propagation with two watched literals -----------------------

    def _propagate(self):
        """Propagate pending trail literals.

        Returns ``None`` on success or the conflicting clause when a clause
        becomes fully false.
        """
        while self.qhead < len(self.trail):
            true_lit = self.trail[self.qhead]
            self.qhead += 1
            false_lit = -true_lit       # it just became false
            wl = self.watchlist[false_lit]

            i = 0
            while i < len(wl):
                cidx = wl[i]
                clause = self.clauses[cidx]
                w = self.wpos[cidx]

                # Put the false watched literal at w[1]; inspect the other.
                if clause[w[0]] == false_lit:
                    w[0], w[1] = w[1], w[0]
                other = clause[w[0]]

                if self._lit_value(other) is True:
                    i += 1              # clause already satisfied
                    continue

                # Look for a fresh literal that is not false to watch.
                replacement = None
                for pos, lit in enumerate(clause):
                    if pos == w[0] or pos == w[1]:
                        continue
                    if self._lit_value(lit) is not False:
                        replacement = pos
                        break

                if replacement is not None:
                    w[1] = replacement
                    self.watchlist[clause[replacement]].append(cidx)
                    wl[i] = wl[-1]     # swap-remove; do not advance i
                    wl.pop()
                    continue

                if self._lit_value(other) is False:
                    return clause       # conflict: clause fully false

                # Unit: the other watched literal is forced true.
                if not self._enqueue(other):
                    return clause
                i += 1

        return None

    # -- search ------------------------------------------------------------

    def _pick_var(self):
        for var in range(1, self.num_vars + 1):
            if self.assign[var] is None:
                return var
        return None

    def _dpll(self):
        conflict = self._propagate()
        if conflict is not None:
            return False

        var = self._pick_var()
        if var is None:
            return True                 # all variables assigned, no conflict

        base_level = len(self.trail_lim)
        for value in (True, False):
            self.trail_lim.append(len(self.trail))
            self._enqueue(var if value else -var)
            if self._dpll():
                return True
            self._backtrack(base_level)  # restore trail + propagation state
        return False

    # -- public API --------------------------------------------------------

    def solve(self, assumptions=()):
        """Solve under temporary assumptions.

        ``assumptions`` is an iterable of literals that are asserted true for
        this query only; they are retracted when the call returns, leaving
        the solver ready for the next query.

        Returns a total assignment ``{var: bool}`` satisfying the original
        constraints (verified by ``verify_model``), or ``None`` once search
        proves the problem UNSAT under the assumptions.
        """
        needed_limit = 4 * (self.num_vars + 100) + 100
        if sys.getrecursionlimit() < needed_limit:
            sys.setrecursionlimit(needed_limit)

        if self._root_unsat:
            return None

        # Level-0 propagation once: a conflict here permanently kills the
        # formula (empty clause / contradictory units), so it is remembered.
        if self._propagate() is not None:
            self._root_unsat = True
            return None

        base_level = len(self.trail_lim)
        self.trail_lim.append(len(self.trail))
        satisfiable = True
        for lit in assumptions:
            if abs(lit) > self.num_vars:
                self._grow(abs(lit))
            if not self._enqueue(lit):
                satisfiable = False
                break

        if satisfiable:
            satisfiable = self._dpll()

        model = None
        if satisfiable:
            # Complete the (possibly partial) solution: unassigned variables
            # are don't-cares and may take either value.
            model = {
                var: self.assign[var] if self.assign[var] is not None else False
                for var in range(1, self.num_vars + 1)
            }
            if not verify_model(self.original_clauses, model):
                raise AssertionError("internal error: returned model fails independent check")

        self._backtrack(base_level)       # retract assumptions + search work
        return model
