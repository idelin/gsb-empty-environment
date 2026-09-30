"""Canonical exact sets of real numbers.

A :class:`RealSet` is a finite tuple of disjoint, sorted, non-mergeable
:class:`Component` intervals.  Endpoints are ``None`` (unbounded),
:class:`fractions.Fraction`, or :class:`AlgebraicNumber`.  Isolated points are
closed components ``[p, p]``.

Union, intersection, difference and complement are computed by sweeping a
common exact cell decomposition of the line; rational affine substitutions
``x = a*t + b`` map endpoints exactly (algebraic endpoints get the substituted
square-free polynomial ``p(a*t + b)``).
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from functools import cmp_to_key

from . import polys as P
from .algebraic import AlgebraicNumber, compare_points, rational_between
from .budget import Budget


@dataclass(frozen=True)
class Component:
    lo: object         # None (-oo) | Fraction | AlgebraicNumber
    lo_closed: bool
    hi: object         # None (+oo) | Fraction | AlgebraicNumber
    hi_closed: bool


def _is_empty(c, budget):
    if c.lo is None or c.hi is None:
        return False
    cmp = compare_points(c.lo, c.hi, budget)
    if cmp > 0:
        return True
    return cmp == 0 and not (c.lo_closed and c.hi_closed)


def _touches(c1, c2, budget):
    """Whether two sorted components overlap or adjoin into one interval."""
    if c1.hi is None or c2.lo is None:
        return True
    cmp = compare_points(c1.hi, c2.lo, budget)
    if cmp > 0:
        return True
    return cmp == 0 and (c1.hi_closed or c2.lo_closed)


def _merge2(c1, c2, budget):
    lo, lo_closed = c1.lo, c1.lo_closed
    if lo is not None and c2.lo is not None:
        cmp = compare_points(lo, c2.lo, budget)
        if cmp > 0:
            lo, lo_closed = c2.lo, c2.lo_closed
        elif cmp == 0:
            lo_closed = lo_closed or c2.lo_closed
    hi, hi_closed = c1.hi, c1.hi_closed
    if c2.hi is None:
        hi, hi_closed = None, False
    elif hi is not None:
        cmp = compare_points(hi, c2.hi, budget)
        if cmp < 0:
            hi, hi_closed = c2.hi, c2.hi_closed
        elif cmp == 0:
            hi_closed = hi_closed or c2.hi_closed
    return Component(lo, lo_closed, hi, hi_closed)


def _unique_sorted(pts, budget):
    pts = sorted(pts, key=cmp_to_key(lambda a, b: compare_points(a, b, budget)))
    out = []
    for p in pts:
        if out and compare_points(out[-1], p, budget) == 0:
            if isinstance(out[-1], AlgebraicNumber) and isinstance(p, Fraction):
                out[-1] = p  # prefer the rational representative
            continue
        out.append(p)
    return out


def build_set(points, cell_flags, point_flags, budget=None):
    """Assemble a canonical RealSet from per-cell / per-point booleans.

    ``points`` is the sorted list of n distinct finite boundary points;
    ``cell_flags`` has n+1 entries (rays included), ``point_flags`` has n.
    """
    n = len(points)
    assert len(cell_flags) == n + 1 and len(point_flags) == n
    comps = []
    length = 2 * n + 1

    def flag(pos):
        return cell_flags[pos // 2] if pos % 2 == 0 else point_flags[pos // 2]

    i = 0
    while i < length:
        if not flag(i):
            i += 1
            continue
        j = i
        while j + 1 < length and flag(j + 1):
            j += 1
        if i % 2 == 1:
            lo, lo_closed = points[i // 2], True
        else:
            k = i // 2
            lo, lo_closed = (None, False) if k == 0 else (points[k - 1], False)
        if j % 2 == 1:
            hi, hi_closed = points[j // 2], True
        else:
            k = j // 2
            hi, hi_closed = (None, False) if k == n else (points[k], False)
        comps.append(Component(lo, lo_closed, hi, hi_closed))
        i = j + 1
    return RealSet(tuple(comps))


class RealSet:
    __slots__ = ("components",)

    def __init__(self, components=()):
        self.components = tuple(components)

    # -- constructors ------------------------------------------------------
    @staticmethod
    def empty():
        return RealSet(())

    @staticmethod
    def reals():
        return RealSet((Component(None, False, None, False),))

    @staticmethod
    def point(p):
        return RealSet((Component(p, True, p, True),))

    @staticmethod
    def from_components(comps, budget=None):
        budget = budget or Budget()
        comps = [c for c in comps if not _is_empty(c, budget)]
        comps.sort(key=cmp_to_key(lambda c1, c2: _cmp_lo(c1.lo, c2.lo, budget)))
        merged = []
        for c in comps:
            if merged and _touches(merged[-1], c, budget):
                merged[-1] = _merge2(merged[-1], c, budget)
            else:
                merged.append(c)
        return RealSet(tuple(merged))

    # -- predicates --------------------------------------------------------
    def is_empty(self):
        return not self.components

    def __bool__(self):
        return bool(self.components)

    def contains_point(self, pt, budget=None):
        budget = budget or Budget()
        for c in self.components:
            if c.lo is not None:
                cmp = compare_points(pt, c.lo, budget)
                if cmp < 0:
                    return False
                if cmp == 0:
                    return c.lo_closed
            if c.hi is not None:
                cmp = compare_points(pt, c.hi, budget)
                if cmp > 0:
                    continue
                if cmp == 0:
                    return c.hi_closed
            return True
        return False

    # -- boolean operations -------------------------------------------------
    def _contains_cell(self, k, pts, budget):
        lo = pts[k - 1] if k > 0 else None
        hi = pts[k] if k < len(pts) else None
        return self.contains_point(rational_between(lo, hi, budget), budget)

    def _combine(self, other, op, budget=None):
        budget = budget or Budget()
        pts = []
        for c in self.components + other.components:
            for e in (c.lo, c.hi):
                if e is not None:
                    pts.append(e)
        pts = _unique_sorted(pts, budget)
        n = len(pts)
        cell_flags = [
            op(self._contains_cell(k, pts, budget), other._contains_cell(k, pts, budget))
            for k in range(n + 1)
        ]
        point_flags = [
            op(self.contains_point(p, budget), other.contains_point(p, budget))
            for p in pts
        ]
        return build_set(pts, cell_flags, point_flags, budget)

    def union(self, other, budget=None):
        return self._combine(other, lambda a, b: a or b, budget)

    def intersection(self, other, budget=None):
        return self._combine(other, lambda a, b: a and b, budget)

    def difference(self, other, budget=None):
        return self._combine(other, lambda a, b: a and not b, budget)

    def complement(self, budget=None):
        return RealSet.reals().difference(self, budget)

    # -- affine substitution -------------------------------------------------
    def affine(self, a, b, budget=None):
        """Substitute ``x = a*t + b`` (a, b rational, a != 0).

        Returns the set of ``t`` with ``a*t + b`` in ``self``; endpoints are
        mapped exactly and the result is again canonical.
        """
        budget = budget or Budget()
        a, b = Fraction(a), Fraction(b)
        if a == 0:
            raise ValueError("affine coefficient a must be nonzero")
        new_comps = []
        for c in self.components:
            nlo = _map_endpoint(c.lo, a, b)
            nhi = _map_endpoint(c.hi, a, b)
            if a > 0:
                new_comps.append(Component(nlo, c.lo_closed, nhi, c.hi_closed))
            else:
                new_comps.append(Component(nhi, c.hi_closed, nlo, c.lo_closed))
        return RealSet.from_components(new_comps, budget)

    # -- comparison / display -------------------------------------------------
    def __eq__(self, other):
        if not isinstance(other, RealSet):
            return NotImplemented
        if len(self.components) != len(other.components):
            return False
        budget = Budget()
        for c1, c2 in zip(self.components, other.components):
            if (c1.lo_closed, c1.hi_closed) != (c2.lo_closed, c2.hi_closed):
                return False
            if (c1.lo is None) != (c2.lo is None) or (c1.hi is None) != (c2.hi is None):
                return False
            if c1.lo is not None and compare_points(c1.lo, c2.lo, budget) != 0:
                return False
            if c1.hi is not None and compare_points(c1.hi, c2.hi, budget) != 0:
                return False
        return True

    __hash__ = None

    def canonical_str(self, budget=None):
        budget = budget or Budget()
        if not self.components:
            return "∅"
        parts = []
        for c in self.components:
            if (
                c.lo is not None
                and c.hi is not None
                and compare_points(c.lo, c.hi, budget) == 0
            ):
                parts.append("{" + _fmt_point(c.lo, budget) + "}")
                continue
            lb = "[" if c.lo_closed else "("
            rb = "]" if c.hi_closed else ")"
            lo = "-∞" if c.lo is None else _fmt_point(c.lo, budget)
            hi = "+∞" if c.hi is None else _fmt_point(c.hi, budget)
            parts.append(f"{lb}{lo}, {hi}{rb}")
        return " ∪ ".join(parts)

    def __repr__(self):
        return f"RealSet({self.canonical_str()})"


def _cmp_lo(a, b, budget):
    if a is None and b is None:
        return 0
    if a is None:
        return -1
    if b is None:
        return 1
    return compare_points(a, b, budget)


def _map_endpoint(e, a, b):
    if e is None:
        return None
    if isinstance(e, Fraction):
        return (e - b) / a
    q = P.squarefree_part(P.compose(e.poly, [b, a]))  # p(a*t + b), square-free
    t1 = (e.lo - b) / a
    t2 = (e.hi - b) / a
    lo, hi = (t1, t2) if t1 < t2 else (t2, t1)
    return AlgebraicNumber(q, lo, hi)


def _fmt_point(p, budget):
    if isinstance(p, Fraction):
        return str(p)
    cr = p.canonical(budget)
    poly_str = P.poly_to_str([Fraction(c) for c in cr.poly])
    return f"RootOf({poly_str}, #{cr.index}, ({cr.lo}, {cr.hi}))"
