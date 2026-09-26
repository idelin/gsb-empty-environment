"""Bound-consistent set-variable constraint solver (Python 3.11, stdlib only).

Each set variable maintains:
  - ``lb``: elements that MUST be in the set (required lower bound)
  - ``ub``: elements that MAY be in the set (possible upper bound)
  - ``min_card`` / ``max_card``: cardinality bounds

Propagation is monotone (lb only grows, ub only shrinks, bounds only
tighten), so the fixpoint loop always terminates.  A :class:`Conflict`
is raised as soon as ``lb`` is not a subset of ``ub`` or the cardinality
bounds become inconsistent.

No SAT/SMT library is used: search is plain backtracking over undecided
elements.  Whenever :meth:`Solver.solve` claims a solution, the original
constraints are re-checked by the independent :func:`check_solution`.
"""

from __future__ import annotations


class Conflict(Exception):
    """Raised when constraint propagation detects an inconsistency."""


class SetVar:
    """A finite set variable with a required lower bound and a possible
    upper bound, plus cardinality bounds."""

    def __init__(self, name, universe, *, required=(), possible=None,
                 min_card=0, max_card=None):
        self.name = name
        self.universe = frozenset(universe)
        self.lb = set(required)
        self.ub = set(self.universe if possible is None else possible)
        self.min_card = min_card
        self.max_card = len(self.ub) if max_card is None else max_card
        # Original (declared) constraints, kept immutable so the
        # independent checker can verify solutions against them.
        self.declared_lb = frozenset(self.lb)
        self.declared_ub = frozenset(self.ub)
        self.declared_min = self.min_card
        self.declared_max = self.max_card

    # -- monotone mutation helpers (return True if anything changed) --

    def require_many(self, elems):
        new = set(elems) - self.lb
        if new:
            self.lb |= new
            return True
        return False

    def restrict_ub(self, allowed):
        new_ub = self.ub & set(allowed)
        if new_ub != self.ub:
            self.ub = new_ub
            return True
        return False

    def forbid_many(self, elems):
        return self.restrict_ub(self.ub - set(elems))

    def raise_min(self, k):
        if k > self.min_card:
            self.min_card = k
            return True
        return False

    def lower_max(self, k):
        if k < self.max_card:
            self.max_card = k
            return True
        return False

    # -- consistency / tightening --

    def check(self):
        if not self.lb <= self.ub:
            raise Conflict(
                f"{self.name}: required lower bound {sorted(self.lb, key=repr)} "
                f"is not a subset of possible upper bound "
                f"{sorted(self.ub, key=repr)}")
        if len(self.lb) > self.max_card:
            raise Conflict(
                f"{self.name}: lower bound has {len(self.lb)} required "
                f"elements but max cardinality is {self.max_card}")
        if self.min_card > len(self.ub):
            raise Conflict(
                f"{self.name}: min cardinality {self.min_card} exceeds the "
                f"{len(self.ub)} possible elements in the upper bound")
        if self.min_card > self.max_card:
            raise Conflict(
                f"{self.name}: min cardinality {self.min_card} exceeds max "
                f"cardinality {self.max_card}")

    def tighten(self):
        """Cardinality-driven bound updates."""
        changed = False
        if len(self.ub) == self.min_card and self.lb != self.ub:
            # Every possible element is needed to reach the minimum size.
            self.lb = set(self.ub)
            changed = True
        if len(self.lb) == self.max_card and self.ub != self.lb:
            # No room for further elements.
            self.ub = set(self.lb)
            changed = True
        return changed

    def __repr__(self):
        return (f"SetVar({self.name!r}, lb={sorted(self.lb, key=repr)}, "
                f"ub={sorted(self.ub, key=repr)}, "
                f"card=[{self.min_card},{self.max_card}])")


class Subset:
    """Constraint ``a`` subset-of ``b``."""

    def __init__(self, a, b):
        self.a, self.b = a, b

    def propagate(self, V):
        A, B = V[self.a], V[self.b]
        changed = False
        changed |= B.require_many(A.lb)      # required in A => required in B
        changed |= A.restrict_ub(B.ub)       # impossible in B => impossible in A
        changed |= B.raise_min(A.min_card)   # |B| >= |A|
        changed |= A.lower_max(B.max_card)
        return changed

    def check(self, sol):
        return sol[self.a] <= sol[self.b]

    def __repr__(self):
        return f"{self.a} ⊆ {self.b}"


class Union:
    """Constraint ``c`` == ``a`` ∪ ``b``."""

    def __init__(self, c, a, b):
        self.c, self.a, self.b = c, a, b

    def propagate(self, V):
        C, A, B = V[self.c], V[self.a], V[self.b]
        changed = False
        changed |= C.require_many(A.lb | B.lb)
        changed |= C.restrict_ub(A.ub | B.ub)
        changed |= A.restrict_ub(C.ub)
        changed |= B.restrict_ub(C.ub)
        # An element required in C must come from A or B.
        changed |= A.require_many(C.lb - B.ub)
        changed |= B.require_many(C.lb - A.ub)
        changed |= C.raise_min(max(A.min_card, B.min_card))
        changed |= C.lower_max(A.max_card + B.max_card)
        changed |= A.raise_min(C.min_card - B.max_card)
        changed |= B.raise_min(C.min_card - A.max_card)
        changed |= A.lower_max(C.max_card)
        changed |= B.lower_max(C.max_card)
        return changed

    def check(self, sol):
        return sol[self.c] == sol[self.a] | sol[self.b]

    def __repr__(self):
        return f"{self.c} = {self.a} ∪ {self.b}"


class Intersection:
    """Constraint ``c`` == ``a`` ∩ ``b``.

    Lower and upper bounds are computed separately:
      - lb(C) grows from lb(A) ∩ lb(B) (required in BOTH => required in C);
        an element merely *possible* in both is NOT required in C.
      - ub(C) shrinks to ub(A) ∩ ub(B).
    """

    def __init__(self, c, a, b):
        self.c, self.a, self.b = c, a, b

    def propagate(self, V):
        C, A, B = V[self.c], V[self.a], V[self.b]
        changed = False
        changed |= C.require_many(A.lb & B.lb)
        changed |= C.restrict_ub(A.ub & B.ub)
        changed |= A.require_many(C.lb)      # C ⊆ A and C ⊆ B
        changed |= B.require_many(C.lb)
        # Required in A but impossible in B => impossible in C (and dually).
        changed |= C.forbid_many(A.lb - B.ub)
        changed |= C.forbid_many(B.lb - A.ub)
        changed |= C.lower_max(min(A.max_card, B.max_card))
        # |A ∩ B| >= |A| + |B| - |A ∪ B|, and |A ∪ B| <= |ub(A) ∪ ub(B)|.
        changed |= C.raise_min(A.min_card + B.min_card - len(A.ub | B.ub))
        changed |= A.raise_min(C.min_card)
        changed |= B.raise_min(C.min_card)
        return changed

    def check(self, sol):
        return sol[self.c] == sol[self.a] & sol[self.b]

    def __repr__(self):
        return f"{self.c} = {self.a} ∩ {self.b}"


class Solver:
    """Constraint store + fixpoint propagation + backtracking search."""

    def __init__(self):
        self.vars = {}
        self.constraints = []

    def add_var(self, name, universe, **kwargs):
        var = SetVar(name, universe, **kwargs)
        self.vars[name] = var
        return var

    def add_subset(self, a, b):
        self.constraints.append(Subset(a, b))

    def add_union(self, c, a, b):
        self.constraints.append(Union(c, a, b))

    def add_intersection(self, c, a, b):
        self.constraints.append(Intersection(c, a, b))

    # -- propagation --

    def propagate(self):
        changed = True
        while changed:
            changed = False
            for var in self.vars.values():
                var.check()
                changed |= var.tighten()
            for con in self.constraints:
                changed |= con.propagate(self.vars)
        for var in self.vars.values():
            var.check()

    # -- search --

    def solve(self):
        """Return {name: frozenset} satisfying all constraints, or None.

        Any claimed solution is verified against the original constraints
        by the independent checker before being returned.
        """
        solution = self._search()
        if solution is not None and not check_solution(self, solution):
            raise RuntimeError(
                "internal error: proposed solution failed independent check")
        return solution

    def _search(self):
        try:
            self.propagate()
        except Conflict:
            return None
        undecided = [v for v in self.vars.values() if v.lb != v.ub]
        if not undecided:
            return {n: frozenset(v.lb) for n, v in self.vars.items()}
        var = min(undecided, key=lambda v: len(v.ub - v.lb))
        element = sorted(var.ub - var.lb, key=repr)[0]
        for include in (True, False):
            snapshot = self._snapshot()
            if include:
                var.lb.add(element)
            else:
                var.ub.discard(element)
            result = self._search()
            if result is not None:
                return result
            self._restore(snapshot)
        return None

    def _snapshot(self):
        return {n: (set(v.lb), set(v.ub), v.min_card, v.max_card)
                for n, v in self.vars.items()}

    def _restore(self, snapshot):
        for n, (lb, ub, mn, mx) in snapshot.items():
            v = self.vars[n]
            v.lb, v.ub, v.min_card, v.max_card = set(lb), set(ub), mn, mx


def check_solution(solver, assignment):
    """Independent checker: re-evaluate the ORIGINAL constraints.

    Does not reuse any propagation internals; it verifies, directly from
    the declared bounds and the constraint semantics, that ``assignment``
    (mapping variable name -> concrete set) is a genuine solution.
    """
    if set(assignment) != set(solver.vars):
        return False
    for name, var in solver.vars.items():
        s = assignment[name]
        if not isinstance(s, (set, frozenset)):
            return False
        if not s <= var.universe:            # finite domain respected
            return False
        if not var.declared_lb <= s:         # declared required elements
            return False
        if not s <= var.declared_ub:         # declared possible elements
            return False
        if not var.declared_min <= len(s) <= var.declared_max:
            return False
    return all(con.check(assignment) for con in solver.constraints)
