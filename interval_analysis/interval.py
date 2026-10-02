"""Interval abstract domain over integers with unbounded endpoints.

Endpoints are integers; ``-math.inf`` / ``math.inf`` denote unbounded
sides.  The empty interval (bottom) is represented canonically by the
``BOTTOM`` singleton (lo=1, hi=0); all operations preserve it.

Division and modulo use C-like truncation toward zero, matching the
concrete semantics in :mod:`interval_analysis.lang`.
"""

from __future__ import annotations

import math

NEG_INF = -math.inf
POS_INF = math.inf


def _trunc_div(a: int, b: int) -> int:
    """Integer division truncating toward zero (C semantics)."""
    q = abs(a) // abs(b)
    if (a < 0) != (b < 0):
        return -q
    return q


def _trunc_mod(a: int, b: int) -> int:
    """Remainder matching truncation-toward-zero division."""
    return a - _trunc_div(a, b) * b


def _mul_bound(a, b):
    """Endpoint product where 0 * inf is treated as 0 (interval convention)."""
    if a == 0 or b == 0:
        return 0
    return a * b


def _div_bound(a, b):
    """Endpoint quotient under truncating division; b != 0 (may be inf)."""
    if a in (NEG_INF, POS_INF):
        if b in (NEG_INF, POS_INF):
            return 0  # inf / inf: any value possible, bounded by 0 here
        if b > 0:
            return a
        return -a
    if b in (NEG_INF, POS_INF):
        return 0
    return _trunc_div(int(a), int(b))


class Interval:
    """A (possibly unbounded) integer interval [lo, hi], or bottom."""

    __slots__ = ("lo", "hi")

    def __init__(self, lo=NEG_INF, hi=POS_INF):
        if lo > hi:
            # Canonicalize any empty interval to BOTTOM's representation.
            lo, hi = 1, 0
        self.lo = lo
        self.hi = hi

    # -- constructors ----------------------------------------------------
    @classmethod
    def bottom(cls) -> "Interval":
        return cls(1, 0)

    @classmethod
    def top(cls) -> "Interval":
        return cls(NEG_INF, POS_INF)

    @classmethod
    def const(cls, value: int) -> "Interval":
        return cls(value, value)

    @classmethod
    def of(cls, lo: int, hi: int) -> "Interval":
        return cls(lo, hi)

    # -- predicates ------------------------------------------------------
    @property
    def is_bottom(self) -> bool:
        return self.lo > self.hi

    def is_singleton(self) -> bool:
        return not self.is_bottom and self.lo == self.hi

    def contains(self, value: int) -> bool:
        return not self.is_bottom and self.lo <= value <= self.hi

    def contains_zero(self) -> bool:
        return self.contains(0)

    # -- lattice ---------------------------------------------------------
    def join(self, other: "Interval") -> "Interval":
        if self.is_bottom:
            return other
        if other.is_bottom:
            return self
        return Interval(min(self.lo, other.lo), max(self.hi, other.hi))

    def meet(self, other: "Interval") -> "Interval":
        if self.is_bottom or other.is_bottom:
            return BOTTOM
        return Interval(max(self.lo, other.lo), min(self.hi, other.hi))

    def widen(self, other: "Interval") -> "Interval":
        """Standard widening: drop bounds that are still growing."""
        if self.is_bottom:
            return other
        if other.is_bottom:
            return self
        lo = self.lo if other.lo >= self.lo else NEG_INF
        hi = self.hi if other.hi <= self.hi else POS_INF
        return Interval(lo, hi)

    def narrow(self, other: "Interval") -> "Interval":
        """Standard narrowing: reintroduce bounds lost by widening."""
        if self.is_bottom or other.is_bottom:
            return BOTTOM
        lo = other.lo if self.lo == NEG_INF else max(self.lo, other.lo)
        hi = other.hi if self.hi == POS_INF else min(self.hi, other.hi)
        return Interval(lo, hi)

    # -- arithmetic ------------------------------------------------------
    def neg(self) -> "Interval":
        if self.is_bottom:
            return BOTTOM
        lo = -self.hi if self.hi != POS_INF else NEG_INF
        hi = -self.lo if self.lo != NEG_INF else POS_INF
        return Interval(lo, hi)

    def add(self, other: "Interval") -> "Interval":
        if self.is_bottom or other.is_bottom:
            return BOTTOM
        lo = NEG_INF if NEG_INF in (self.lo, other.lo) else self.lo + other.lo
        hi = POS_INF if POS_INF in (self.hi, other.hi) else self.hi + other.hi
        return Interval(lo, hi)

    def sub(self, other: "Interval") -> "Interval":
        return self.add(other.neg())

    def mul(self, other: "Interval") -> "Interval":
        if self.is_bottom or other.is_bottom:
            return BOTTOM
        products = [
            _mul_bound(self.lo, other.lo),
            _mul_bound(self.lo, other.hi),
            _mul_bound(self.hi, other.lo),
            _mul_bound(self.hi, other.hi),
        ]
        return Interval(min(products), max(products))

    def _divisor_parts(self, other: "Interval"):
        """Split a divisor into non-zero parts; None flags a zero divisor."""
        parts = []
        if other.lo < 0:
            parts.append(Interval(other.lo, min(other.hi, -1)))
        if other.hi > 0:
            parts.append(Interval(max(other.lo, 1), other.hi))
        return parts

    def div(self, other: "Interval") -> "Interval":
        """Truncating division; divisor zero is excluded (see analyzer)."""
        if self.is_bottom or other.is_bottom:
            return BOTTOM
        result = BOTTOM
        for part in self._divisor_parts(other):
            bounds = [
                _div_bound(self.lo, part.lo),
                _div_bound(self.lo, part.hi),
                _div_bound(self.hi, part.lo),
                _div_bound(self.hi, part.hi),
            ]
            result = result.join(Interval(min(bounds), max(bounds)))
        return result

    def mod(self, other: "Interval") -> "Interval":
        """C-like remainder; result sign follows the dividend."""
        if self.is_bottom or other.is_bottom:
            return BOTTOM
        result = BOTTOM
        for part in self._divisor_parts(other):
            mag = max(abs(part.lo), abs(part.hi)) - 1
            if mag == math.inf:
                mag = POS_INF
            lo = -mag if self.lo < 0 else 0
            hi = mag if self.hi > 0 else 0
            lo = max(lo, self.lo) if self.lo != NEG_INF else lo
            hi = min(hi, self.hi) if self.hi != POS_INF else hi
            result = result.join(Interval(lo, hi))
        return result

    # -- comparisons (three-valued) --------------------------------------
    def maybe_eq(self, other: "Interval") -> bool:
        return not self.meet(other).is_bottom

    def must_eq(self, other: "Interval") -> bool:
        return (
            self.is_singleton()
            and other.is_singleton()
            and self.lo == other.lo
        )

    def maybe_lt(self, other: "Interval") -> bool:
        if self.is_bottom or other.is_bottom:
            return False
        return self.lo < other.hi

    def must_lt(self, other: "Interval") -> bool:
        if self.is_bottom or other.is_bottom:
            return False
        return self.hi < other.lo

    # -- refinement (branch contraction) ---------------------------------
    def assume_eq(self, other: "Interval"):
        """Refine self under ``self == other``.  Returns (interval, precise)."""
        return self.meet(other), True

    def assume_ne(self, other: "Interval"):
        """Refine self under ``self != other``.

        When ``other`` is a singleton strictly inside ``self`` the exact
        result is a union of two intervals, which this domain cannot
        represent.  We then keep ``self`` unchanged (sound) and report
        ``precise=False`` -- we never pretend the hole was removed.
        """
        if not other.is_singleton():
            return self, True  # no sound refinement available beyond meet
        v = other.lo
        if self.is_bottom or not self.contains(v):
            return self, True
        if self.lo == self.hi == v:
            return BOTTOM, True
        if self.lo == v:
            return Interval(v + 1, self.hi), True
        if self.hi == v:
            return Interval(self.lo, v - 1), True
        # v is strictly inside: exact refinement is non-convex.
        return self, False

    def assume_lt(self, other: "Interval"):
        """Refine self under ``self < other`` (sound, may be imprecise)."""
        if other.is_bottom:
            return BOTTOM, True
        if other.hi == POS_INF:
            return self, True
        return self.meet(Interval(NEG_INF, other.hi - 1)), True

    def assume_le(self, other: "Interval"):
        if other.is_bottom:
            return BOTTOM, True
        return self.meet(Interval(NEG_INF, other.hi)), True

    def assume_gt(self, other: "Interval"):
        if other.is_bottom:
            return BOTTOM, True
        if other.lo == NEG_INF:
            return self, True
        return self.meet(Interval(other.lo + 1, POS_INF)), True

    def assume_ge(self, other: "Interval"):
        if other.is_bottom:
            return BOTTOM, True
        return self.meet(Interval(other.lo, POS_INF)), True

    # -- dunder ------------------------------------------------------------
    def __eq__(self, other):
        if not isinstance(other, Interval):
            return NotImplemented
        if self.is_bottom and other.is_bottom:
            return True
        return self.lo == other.lo and self.hi == other.hi

    def __hash__(self):
        if self.is_bottom:
            return hash("bottom")
        return hash((self.lo, self.hi))

    def __repr__(self):
        if self.is_bottom:
            return "Interval(bottom)"

        def fmt(v, neg):
            if v == NEG_INF:
                return "-inf"
            if v == POS_INF:
                return "+inf"
            return str(int(v))

        return "Interval([%s, %s])" % (fmt(self.lo, True), fmt(self.hi, False))


BOTTOM = Interval.bottom()
TOP = Interval.top()
