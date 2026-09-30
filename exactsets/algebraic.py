"""Exact real algebraic numbers.

An :class:`AlgebraicNumber` is a real root of a square-free rational
polynomial isolated in an open interval with rational endpoints containing
exactly one root.  All comparisons are exact:

* equality of roots of *different* polynomials is decided by algebraic
  evidence -- the gcd of the two polynomials must have a root inside the
  intersection of the isolating intervals (checked by Sturm counting);
* ordering is decided by refining isolating intervals until they are
  disjoint -- never by floating-point approximation.

The canonical output form is :class:`CanonicalRoot`: a primitive integer
square-free polynomial, the index of the root among all its real roots
(ascending), and a rational isolating interval.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from functools import cmp_to_key

from . import polys as P
from .budget import Budget, BudgetExceeded


class RationalRootEncountered(Exception):
    """Bisection collapsed onto an exactly rational root."""

    def __init__(self, value):
        super().__init__(f"isolating interval collapsed onto rational root {value}")
        self.value = value


class AlgebraicNumber:
    """The unique real root of square-free ``poly`` inside ``(lo, hi)``."""

    __slots__ = ("poly", "lo", "hi", "_sign_lo")

    def __init__(self, poly, lo, hi):
        self.poly = P.norm(poly)
        self.lo = Fraction(lo)
        self.hi = Fraction(hi)
        assert self.lo < self.hi, "empty isolating interval"
        self._sign_lo = None

    def __repr__(self):
        return f"Alg({P.poly_to_str(self.poly)}, ({self.lo}, {self.hi}))"

    def sign_lo(self):
        if self._sign_lo is None:
            s = P.sign_of_value(P.evaluate(self.poly, self.lo))
            if s == 0:
                raise ValueError("isolating endpoint is a root")
            self._sign_lo = s
        return self._sign_lo

    def refine(self, budget=None):
        """Halve the isolating interval, keeping the root inside."""
        if budget is not None:
            budget.tick()
        m = (self.lo + self.hi) / 2
        v = P.evaluate(self.poly, m)
        if v == 0:
            raise RationalRootEncountered(m)
        if (v > 0) == (self.sign_lo() > 0):
            self.lo = m
        else:
            self.hi = m

    def compare_rational(self, r):
        """Return sign(self - r) for rational r."""
        r = Fraction(r)
        if r <= self.lo:
            return 1
        if r >= self.hi:
            return -1
        v = P.evaluate(self.poly, r)
        if v == 0:
            return 0
        return 1 if (v > 0) == (self.sign_lo() > 0) else -1

    def equals(self, other, budget=None):
        """Exact equality via gcd + Sturm count on the interval intersection."""
        g = P.gcd(self.poly, other.poly)
        if P.deg(g) < 1:
            return False
        lo = max(self.lo, other.lo)
        hi = min(self.hi, other.hi)
        if lo >= hi:
            return False
        return P.count_roots(g, lo, hi, budget=budget) == 1

    def compare(self, other, budget=None):
        """Return sign(self - other); intervals are refined until disjoint."""
        if self.equals(other, budget):
            return 0
        while True:
            if self.hi <= other.lo:
                return -1
            if other.hi <= self.lo:
                return 1
            wider_is_self = (self.hi - self.lo) >= (other.hi - other.lo)
            try:
                if wider_is_self:
                    self.refine(budget)
                else:
                    other.refine(budget)
            except RationalRootEncountered as e:
                if wider_is_self:
                    return -other.compare_rational(e.value)
                return self.compare_rational(e.value)

    def sign_of_poly_at(self, q, budget=None):
        """Exact sign of q(self): 0 detected via gcd, else Sturm refinement."""
        q = P.norm(q)
        if P.is_zero(q):
            return 0
        g = P.gcd(self.poly, q)
        if P.deg(g) >= 1 and P.count_roots(g, self.lo, self.hi, budget=budget) == 1:
            return 0
        qs = P.squarefree_part(q)
        if P.deg(qs) < 1:
            return P.sign_of_value(P.evaluate(q, self.lo))
        seq = P.sturm_sequence(qs, budget)
        while True:
            try:
                if P.evaluate(qs, self.lo) == 0 or P.evaluate(qs, self.hi) == 0:
                    self.refine(budget)
                    continue
                if P.count_roots(qs, self.lo, self.hi, seq=seq, budget=budget) == 0:
                    return P.sign_of_value(P.evaluate(q, self.lo))
                self.refine(budget)
            except RationalRootEncountered as e:
                return P.sign_of_value(P.evaluate(q, e.value))

    def canonical(self, budget=None):
        """Canonical form: (primitive square-free poly, root index, interval)."""
        budget = budget or Budget()
        ip = P.to_primitive_integer(self.poly)
        roots = isolate_all([Fraction(c) for c in ip], budget)
        for i, r in enumerate(roots):
            if isinstance(r, Fraction):
                if self.compare_rational(r) == 0:
                    return CanonicalRoot(tuple(ip), i, r, r)
            elif r.equals(self, budget):
                return CanonicalRoot(tuple(ip), i, r.lo, r.hi)
        raise AssertionError("root not found among its polynomial's roots")


@dataclass(frozen=True)
class CanonicalRoot:
    poly: tuple   # primitive integer coefficients of a square-free polynomial
    index: int    # index among all real roots of poly, ascending
    lo: Fraction  # rational isolating interval
    hi: Fraction


def compare_points(a, b, budget=None):
    """Total order on Fraction | AlgebraicNumber."""
    if isinstance(a, Fraction) and isinstance(b, Fraction):
        return (a > b) - (a < b)
    if isinstance(a, Fraction):
        return -b.compare_rational(a)
    if isinstance(b, Fraction):
        return a.compare_rational(b)
    return a.compare(b, budget)


def sort_points(points, budget=None):
    return sorted(points, key=cmp_to_key(lambda a, b: compare_points(a, b, budget)))


def rational_between(a, b, budget=None):
    """A rational r with a < r < b; a/b may be None (unbounded side)."""
    if a is None and b is None:
        return Fraction(0)
    if a is None:
        return (b - 1) if isinstance(b, Fraction) else b.lo - 1
    if b is None:
        return (a + 1) if isinstance(a, Fraction) else a.hi + 1
    if isinstance(a, Fraction) and isinstance(b, Fraction):
        return (a + b) / 2
    if isinstance(a, AlgebraicNumber):
        while True:
            if isinstance(b, Fraction):
                if a.hi < b:
                    return a.hi
            elif a.hi < b.lo:
                return a.hi
            a.refine(budget)
    # a rational, b algebraic
    while b.lo <= a:
        b.refine(budget)
    return b.lo


# ---------------------------------------------------------------------------
# Root isolation
# ---------------------------------------------------------------------------

_MAX_EXTRACT_BITS = 42   # skip rational-root extraction for huge coefficients
_MAX_CANDIDATES = 4000


def _divisors(n):
    small, big = [], []
    i = 1
    while i * i <= n:
        if n % i == 0:
            small.append(i)
            if i != n // i:
                big.append(n // i)
        i += 1
    return small + big[::-1]


def _extract_rational_roots(p, budget):
    """Split off all rational roots via the rational-root theorem.

    Returns (sorted list of rational roots, deflated polynomial).  If the
    coefficients are too large for exhaustive divisor enumeration, extraction
    is skipped entirely -- correctness does not depend on it (bisection still
    isolates every root, and dyadic rational roots are detected exactly).
    """
    ip = P.to_primitive_integer(p)
    if P.deg(ip) < 1:
        return [], p
    if ip[0] != 0 and max(abs(ip[0]), abs(ip[-1])).bit_length() > _MAX_EXTRACT_BITS:
        return [], p
    q = [Fraction(c) for c in ip]
    found = []
    while P.deg(q) >= 1 and q[0] == 0:
        found.append(Fraction(0))
        q = P.norm(q[1:])
    if P.deg(q) >= 1:
        iq = [int(c) for c in q]
        divs0 = _divisors(abs(iq[0]))
        divsn = _divisors(abs(iq[-1]))
        if len(divs0) * len(divsn) * 2 > _MAX_CANDIDATES:
            return [], p
        cands = set()
        for u in divs0:
            for v in divsn:
                cands.add(Fraction(u, v))
                cands.add(Fraction(-u, v))
        for r in sorted(cands):
            budget.tick()
            while P.deg(q) >= 1 and P.evaluate(q, r) == 0:
                found.append(r)
                q = P.exact_div(q, [-r, Fraction(1)])
    return found, q


def _isolate_irrational(q, budget):
    """Isolate all real roots of square-free ``q`` by Sturm-guided bisection."""
    if P.deg(q) < 1:
        return []
    B = P.cauchy_bound(q)
    while P.evaluate(q, B) == 0 or P.evaluate(q, -B) == 0:
        B += 1
    found = []      # AlgebraicNumber with a 1-root interval
    rationals = []  # exact dyadic rational roots hit by bisection
    stack = [(-B, B, None)]
    try:
        seq = P.sturm_sequence(q, budget)
        stack[0] = (-B, B, P.count_roots(q, -B, B, seq=seq))
        while stack:
            budget.tick()
            lo, hi, c = stack.pop()
            if c == 0:
                continue
            if c == 1:
                found.append(AlgebraicNumber(q, lo, hi))
                continue
            m = (lo + hi) / 2
            if P.evaluate(q, m) == 0:
                rationals.append(m)
                cl = P.count_roots(q, lo, m, seq=seq)
                cr = P.count_roots(q, m, hi, seq=seq)
            else:
                cl = P.count_roots(q, lo, m, seq=seq)
                cr = c - cl
            if cl:
                stack.append((lo, m, cl))
            if cr:
                stack.append((m, hi, cr))
    except BudgetExceeded as e:
        e.frontier = list(stack)
        e.found = found + rationals
        raise
    return found + rationals


def isolate_all(p, budget=None):
    """All real roots of ``p``, sorted: Fractions and AlgebraicNumbers."""
    budget = budget or Budget()
    p = P.norm(p)
    if P.is_zero(p):
        raise ValueError("the zero polynomial has infinitely many roots")
    if P.deg(p) < 1:
        return []
    p = P.squarefree_part(p)
    rationals, q = _extract_rational_roots(p, budget)
    pts = list(rationals)
    if P.deg(q) >= 1:
        pts.extend(_isolate_irrational(q, budget))
    return sort_points(pts, budget)
