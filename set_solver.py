"""Finite-domain set-variable constraint solver (standard library only).

Each set variable maintains two bounds:
  - lower: elements that MUST be members of the set (required)
  - upper: elements that MAY be members of the set (possible)

A conflict is raised as soon as lower is not a subset of upper, or the
cardinality bounds become inconsistent. Possible members are never
treated as required ones: e.g. for C = A & B, the lower bound of C is
computed as (lower A & lower B), separately from the upper bound
(upper A & upper B).

When solve() returns an assignment, it has been re-verified by an
independent checker (check_solution) against the original constraints.
"""

from __future__ import annotations

from dataclasses import dataclass


class Conflict(Exception):
    """Raised when propagation proves the problem unsatisfiable."""


class SetVar:
    """A set variable with a required-lower and possible-upper bound."""

    def __init__(self, name, upper, lower=(), min_card=0, max_card=None):
        self.name = name
        self.upper = set(upper)
        self.lower = set(lower)
        self.min_card = min_card
        self.max_card = len(self.upper) if max_card is None else max_card
        self.tighten()

    def add_lower(self, elems):
        new = set(elems) - self.lower
        if not new:
            return False
        self.lower |= new
        return True

    def restrict_upper(self, allowed):
        new_upper = self.upper & set(allowed)
        if new_upper == self.upper:
            return False
        self.upper = new_upper
        return True

    def tighten(self):
        """Enforce local consistency; returns True if a bound changed."""
        if not self.lower <= self.upper:
            raise Conflict(
                f"{self.name}: required elements {sorted(self.lower - self.upper)} "
                f"are not possible members (lower not subset of upper)")
        if len(self.lower) > self.max_card:
            raise Conflict(
                f"{self.name}: must contain {len(self.lower)} elements "
                f"but max cardinality is {self.max_card}")
        if len(self.upper) < self.min_card:
            raise Conflict(
                f"{self.name}: only {len(self.upper)} possible elements "
                f"but min cardinality is {self.min_card}")
        changed = False
        if len(self.lower) == self.max_card and self.upper != self.lower:
            self.upper = set(self.lower)  # no room for more elements
            changed = True
        if len(self.upper) == self.min_card and self.lower != self.upper:
            self.lower = set(self.upper)  # every possible element is needed
            changed = True
        return changed

    @property
    def determined(self):
        return self.lower == self.upper


class Constraint:
    def propagate(self):
        raise NotImplementedError

    def check(self, assignment):
        """Independent, direct evaluation on concrete frozensets."""
        raise NotImplementedError


@dataclass
class SubsetOf(Constraint):
    """a is a subset of b."""
    a: SetVar
    b: SetVar

    def propagate(self):
        changed = self.a.restrict_upper(self.b.upper)
        changed |= self.b.add_lower(self.a.lower)
        return changed

    def check(self, assignment):
        return assignment[self.a.name] <= assignment[self.b.name]


@dataclass
class UnionEq(Constraint):
    """c == a | b"""
    a: SetVar
    b: SetVar
    c: SetVar

    def propagate(self):
        a, b, c = self.a, self.b, self.c
        changed = c.add_lower(a.lower | b.lower)
        changed |= c.restrict_upper(a.upper | b.upper)
        changed |= a.restrict_upper(c.upper)
        changed |= b.restrict_upper(c.upper)
        for x in c.lower:
            in_a, in_b = x in a.upper, x in b.upper
            if not in_a and not in_b:
                raise Conflict(
                    f"union {c.name}: required element {x} cannot come "
                    f"from either operand")
            if not in_a:
                changed |= b.add_lower({x})
            elif not in_b:
                changed |= a.add_lower({x})
        return changed

    def check(self, assignment):
        return (assignment[self.c.name]
                == assignment[self.a.name] | assignment[self.b.name])


@dataclass
class InterEq(Constraint):
    """c == a & b"""
    a: SetVar
    b: SetVar
    c: SetVar

    def propagate(self):
        a, b, c = self.a, self.b, self.c
        # Lower bound: only elements required in BOTH are required in c.
        changed = c.add_lower(a.lower & b.lower)
        # Upper bound: only elements possible in BOTH are possible in c.
        changed |= c.restrict_upper(a.upper & b.upper)
        changed |= a.add_lower(c.lower)
        changed |= b.add_lower(c.lower)
        # x required in a but impossible in c => x impossible in b (and vice versa).
        changed |= b.restrict_upper(b.upper - {x for x in a.lower if x not in c.upper})
        changed |= a.restrict_upper(a.upper - {x for x in b.lower if x not in c.upper})
        return changed

    def check(self, assignment):
        return (assignment[self.c.name]
                == assignment[self.a.name] & assignment[self.b.name])


def check_solution(constraints, domains, assignment):
    """Independently verify an assignment against the ORIGINAL constraints.

    Uses no propagation state: domain bounds recorded at variable creation
    and each constraint's own check() on concrete frozensets.
    """
    for name, (lower0, upper0, min_card, max_card) in domains.items():
        val = assignment.get(name)
        if val is None:
            return False
        if not (lower0 <= val <= upper0):
            return False
        if not (min_card <= len(val) <= max_card):
            return False
    return all(c.check(assignment) for c in constraints)


class Solver:
    def __init__(self):
        self.vars = {}
        self.constraints = []
        self._domains = {}

    def add_var(self, name, upper, lower=(), min_card=0, max_card=None):
        var = SetVar(name, upper, lower, min_card, max_card)
        self.vars[name] = var
        self._domains[name] = (frozenset(var.lower), frozenset(var.upper),
                               var.min_card, var.max_card)
        return var

    def add_constraint(self, constraint):
        self.constraints.append(constraint)

    def set_card(self, name, min_card=None, max_card=None):
        var = self.vars[name]
        if min_card is not None:
            var.min_card = max(var.min_card, min_card)
        if max_card is not None:
            var.max_card = min(var.max_card, max_card)
        lo0, up0, _, _ = self._domains[name]
        self._domains[name] = (lo0, up0, var.min_card, var.max_card)
        var.tighten()

    def propagate(self):
        changed = True
        while changed:
            changed = False
            for var in self.vars.values():
                changed |= var.tighten()
            for constraint in self.constraints:
                changed |= constraint.propagate()

    def solve(self):
        """Return {name: frozenset} if satisfiable, else None.

        Any returned assignment is verified by the independent checker
        against the original constraints before being handed out.
        """
        try:
            assignment = self._search()
        except Conflict:
            return None
        if assignment is None:
            return None
        if not check_solution(self.constraints, self._domains, assignment):
            raise AssertionError("internal error: solver produced an "
                                 "assignment rejected by the checker")
        return assignment

    def _search(self):
        self.propagate()
        undecided = [v for v in self.vars.values() if not v.determined]
        if not undecided:
            return {n: frozenset(v.lower) for n, v in self.vars.items()}
        var = min(undecided, key=lambda v: len(v.upper - v.lower))
        elem = min(var.upper - var.lower)
        snapshot = self._snapshot()
        for decide in (self._require, self._exclude):
            try:
                decide(var, elem)
                result = self._search()
                if result is not None:
                    return result
            except Conflict:
                pass
            self._restore(snapshot)
        return None

    @staticmethod
    def _require(var, elem):
        var.add_lower({elem})

    @staticmethod
    def _exclude(var, elem):
        var.restrict_upper(var.upper - {elem})

    def _snapshot(self):
        return {n: (set(v.lower), set(v.upper), v.min_card, v.max_card)
                for n, v in self.vars.items()}

    def _restore(self, snapshot):
        for name, (lower, upper, min_card, max_card) in snapshot.items():
            var = self.vars[name]
            var.lower, var.upper = lower, upper
            var.min_card, var.max_card = min_card, max_card
