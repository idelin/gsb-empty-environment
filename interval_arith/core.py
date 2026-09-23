"""Exact rational interval arithmetic.

All endpoints are exact rationals (``fractions.Fraction``); unbounded
endpoints use the sentinels ``float("-inf")`` / ``float("inf")``.
Because every operation is exact, no rounding ever occurs inside the
library.  The only place rounding appears is when importing inexact data
(floats), where we round *outward* (conservatively), so the resulting
interval always contains the real value being approximated.

Division by an interval straddling 0 returns a *disconnected domain*
(two rays); division by exactly [0, 0] is *undefined*.  The zero of the
divisor is never silently hidden.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from math import inf as POS_INF, nextafter
from typing import Callable, Optional

NEG_INF = -POS_INF
ZERO = Fraction(0)
ONE = Fraction(1)


def _as_num(value):
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool is not a valid endpoint")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        if value in (POS_INF, NEG_INF):
            return value
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"unsupported endpoint {value!r}")


def _mul_end(a, b):
    # Convention 0 * inf = 0: sound here because a zero-width single-sign
    # piece contributes only the value 0 to the product set.
    if a == ZERO or b == ZERO:
        return ZERO
    return a * b


def _recip(e):
    if e == POS_INF or e == NEG_INF:
        return ZERO
    return ONE / e


def _fmt(e):
    if e == POS_INF:
        return "inf"
    if e == NEG_INF:
        return "-inf"
    return str(e)


@dataclass(frozen=True)
class Interval:
    """Closed interval [lo, hi]; lo/hi are Fraction or +/-inf."""

    lo: object = ZERO
    hi: object = ZERO

    def __post_init__(self):
        lo = _as_num(self.lo)
        hi = _as_num(self.hi)
        if not lo <= hi:
            raise ValueError(f"empty interval [{_fmt(lo)}, {_fmt(hi)}]")
        object.__setattr__(self, "lo", lo)
        object.__setattr__(self, "hi", hi)

    # ---------------------------------------------------------- constructors
    @classmethod
    def point(cls, v) -> "Interval":
        return cls(v, v)

    @classmethod
    def from_float(cls, x: float) -> "Interval":
        """Conservative outward rounding: widen by one ulp on each side so the
        interval always contains the real value the float approximates."""
        return cls(Fraction(nextafter(x, NEG_INF)), Fraction(nextafter(x, POS_INF)))

    @classmethod
    def from_decimal(cls, s: str) -> "Interval":
        """Exact conversion of a decimal string such as "0.1" (no rounding)."""
        return cls.point(Fraction(s))

    # ------------------------------------------------------------- queries
    def is_bounded(self) -> bool:
        return self.lo != NEG_INF and self.hi != POS_INF

    def contains(self, v) -> bool:
        return self.lo <= v <= self.hi

    def contains_zero(self) -> bool:
        return self.lo <= ZERO <= self.hi

    def is_subset(self, other: "Interval") -> bool:
        return other.lo <= self.lo and self.hi <= other.hi

    def width(self):
        if not self.is_bounded():
            return POS_INF
        return self.hi - self.lo

    def hull(self, other: "Interval") -> "Interval":
        return Interval(min(self.lo, other.lo), max(self.hi, other.hi))

    def __repr__(self) -> str:
        return f"[{_fmt(self.lo)}, {_fmt(self.hi)}]"

    # ----------------------------------------------------------- arithmetic
    def __neg__(self) -> "Interval":
        return Interval(-self.hi, -self.lo)

    def __add__(self, other: "Interval") -> "Interval":
        return Interval(self.lo + other.lo, self.hi + other.hi)

    def __sub__(self, other: "Interval") -> "Interval":
        return self + (-other)

    def _sign_pieces(self):
        """Split into pieces each entirely >= 0 or <= 0 (for exact product)."""
        pieces = []
        if self.lo < ZERO:
            pieces.append((self.lo, min(self.hi, ZERO)))
        if self.hi > ZERO:
            pieces.append((max(self.lo, ZERO), self.hi))
        if not pieces:  # [0, 0]
            pieces.append((ZERO, ZERO))
        return pieces

    def __mul__(self, other: "Interval") -> "Interval":
        out = None
        for a, b in self._sign_pieces():
            for c, d in other._sign_pieces():
                prods = [_mul_end(a, c), _mul_end(a, d),
                         _mul_end(b, c), _mul_end(b, d)]
                piece = Interval(min(prods), max(prods))
                out = piece if out is None else out.hull(piece)
        return out

    def square(self) -> "Interval":
        """Exact image of x -> x^2 (not the same as self * self: the two
        factors are correlated, and we exploit that here)."""
        if self.contains_zero():
            hi = max(_mul_end(self.lo, self.lo), _mul_end(self.hi, self.hi))
            return Interval(ZERO, hi)
        a = _mul_end(self.lo, self.lo)
        b = _mul_end(self.hi, self.hi)
        return Interval(min(a, b), max(a, b))

    def apply_monotone(self, fn: Callable, increasing: bool = True) -> "Interval":
        """Apply an exactly-computed monotone rational function.

        Domain precondition: ``fn`` must be monotone on the whole of
        ``self``; the image is then exactly [fn(lo), fn(hi)] (swapped when
        decreasing).  Example: fn = lambda x: x**3 is increasing everywhere.
        """
        a, b = fn(self.lo), fn(self.hi)
        return Interval(a, b) if increasing else Interval(b, a)


@dataclass(frozen=True)
class Domain:
    """A finite union of disjoint closed intervals (possibly unbounded)."""

    parts: tuple = ()

    @staticmethod
    def merge(intervals) -> "Domain":
        ordered = sorted(intervals, key=lambda i: (i.lo, i.hi))
        merged = []
        for iv in ordered:
            if merged and iv.lo <= merged[-1].hi:
                merged[-1] = Interval(merged[-1].lo, max(merged[-1].hi, iv.hi))
            else:
                merged.append(iv)
        return Domain(tuple(merged))

    def contains(self, v) -> bool:
        return any(p.contains(v) for p in self.parts)

    def __repr__(self) -> str:
        return " ∪ ".join(str(p) for p in self.parts) if self.parts else "∅"


@dataclass(frozen=True)
class DivResult:
    """Result of interval division.

    ``domain`` is None when the division is undefined (divisor == [0, 0]).
    ``note`` records the applicable domain of the transformation.
    """

    domain: Optional[Domain]
    note: str = ""

    @property
    def undefined(self) -> bool:
        return self.domain is None

    def __repr__(self) -> str:
        if self.undefined:
            return f"undefined ({self.note})"
        return f"{self.domain}   [{self.note}]"


def reciprocal(y: Interval) -> DivResult:
    """Exact reciprocal 1 / y as a (possibly disconnected) domain."""
    if y.lo == ZERO and y.hi == ZERO:
        return DivResult(None, "divisor is exactly [0, 0]: division undefined")
    if not y.contains_zero():
        iv = Interval(_recip(y.hi), _recip(y.lo))
        return DivResult(Domain.merge([iv]),
                         "divisor excludes 0: ordinary interval division")
    parts = []
    if y.lo < ZERO:
        parts.append(Interval(NEG_INF, _recip(y.lo)))
    if y.hi > ZERO:
        parts.append(Interval(_recip(y.hi), POS_INF))
    return DivResult(
        Domain.merge(parts),
        "divisor straddles 0: result is a disconnected domain; "
        "the point 0 itself is excluded from the domain of definition",
    )


def divide(x: Interval, y: Interval) -> DivResult:
    """x / y with exact rational endpoints.

    * divisor exactly [0, 0]  -> undefined (the zero is never hidden)
    * divisor straddling 0    -> disconnected domain (one or two rays)
    * otherwise               -> ordinary interval division
    """
    rec = reciprocal(y)
    if rec.undefined:
        return rec
    domain = Domain.merge([x * p for p in rec.domain.parts])
    return DivResult(domain, rec.note)
