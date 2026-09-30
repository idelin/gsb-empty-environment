"""Rigorous interval arithmetic with outward (directed) rounding.

Implemented on ``decimal.Decimal`` with dedicated ROUND_FLOOR / ROUND_CEILING
contexts.  Every elementary operation rounds the lower endpoint toward
-inf and the upper endpoint toward +inf, so the resulting interval always
encloses the exact mathematical result of the same expression.

For transcendental operations (exp / ln) one extra ulp of margin is added
outward, so containment does not rely on libmpdec honouring directed
rounding modes for transcendentals -- a deliberately conservative choice.

``to_float()`` converts a Decimal interval to the tightest enclosing float
interval via one-ulp outward adjustment (``math.nextafter``), which is
rigorous because ``float(Decimal)`` rounds to nearest: the neighbour float
in the outward direction is always on the safe side of the exact value.
"""

from __future__ import annotations

import math
from decimal import ROUND_CEILING, ROUND_FLOOR, Context, Decimal

PRECISION = 60

_CTX_LO = Context(
    prec=PRECISION, rounding=ROUND_FLOOR, Emin=-999_999_999, Emax=999_999_999
)
_CTX_HI = Context(
    prec=PRECISION, rounding=ROUND_CEILING, Emin=-999_999_999, Emax=999_999_999
)

_ZERO = Decimal(0)
_ONE = Decimal(1)
_NEG_INF = Decimal("-Infinity")


def _dec(value: object) -> Decimal:
    """Convert an int/float/str/Decimal to a finite Decimal.

    Floats are converted via ``Decimal(float)``, i.e. to their exact binary
    value, which keeps interval claims rigorous with respect to the actual
    parameters in use.
    """
    if isinstance(value, Decimal):
        result = value
    elif isinstance(value, bool):
        raise TypeError("bool is not a valid interval endpoint")
    elif isinstance(value, (int, float, str)):
        if isinstance(value, float) and (math.isnan(value) or math.isinf(value)):
            raise ValueError(f"non-finite float endpoint: {value!r}")
        result = Decimal(value)
    else:
        raise TypeError(f"cannot convert {type(value).__name__} to Decimal")
    if not result.is_finite():
        raise ValueError(f"non-finite endpoint: {result!r}")
    return result


class Interval:
    """A closed Decimal interval [lo, hi] with outward-rounded operations."""

    __slots__ = ("lo", "hi")

    def __init__(self, lo: object, hi: object = None) -> None:
        lo_d = _dec(lo)
        hi_d = _dec(hi) if hi is not None else lo_d
        if lo_d > hi_d:
            raise ValueError(f"empty interval: [{lo_d}, {hi_d}]")
        self.lo = lo_d
        self.hi = hi_d

    # -- representation -------------------------------------------------
    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return f"Interval({self.lo}, {self.hi})"

    @property
    def is_point(self) -> bool:
        return self.lo == self.hi

    @staticmethod
    def _coerce(other: object) -> "Interval":
        return other if isinstance(other, Interval) else Interval(other)

    # -- arithmetic ------------------------------------------------------
    def __add__(self, other: object) -> "Interval":
        other = Interval._coerce(other)
        # _unchecked: ln(0)/log1p(-1) legitimately produce a -inf lower
        # bound, which must be allowed to flow through additions until an
        # intersection with finite universal bounds clamps it.
        return Interval._unchecked(
            _CTX_LO.add(self.lo, other.lo), _CTX_HI.add(self.hi, other.hi)
        )

    __radd__ = __add__

    def __neg__(self) -> "Interval":
        # copy_negate flips the sign WITHOUT rounding; the unary ``-``
        # operator on Decimal would round to the *default* 28-digit context.
        return Interval(self.hi.copy_negate(), self.lo.copy_negate())

    def __sub__(self, other: object) -> "Interval":
        return self + (-Interval._coerce(other))

    def __rsub__(self, other: object) -> "Interval":
        return Interval._coerce(other) + (-self)

    def __mul__(self, other: object) -> "Interval":
        other = Interval._coerce(other)
        corners = [
            (a, b)
            for a in (self.lo, self.hi)
            for b in (other.lo, other.hi)
        ]
        lo = min(_CTX_LO.multiply(a, b) for a, b in corners)
        hi = max(_CTX_HI.multiply(a, b) for a, b in corners)
        return Interval(lo, hi)

    __rmul__ = __mul__

    def __truediv__(self, other: object) -> "Interval":
        other = Interval._coerce(other)
        if other.lo <= _ZERO <= other.hi:
            raise ZeroDivisionError("divisor interval contains 0")
        corners = [
            (a, b)
            for a in (self.lo, self.hi)
            for b in (other.lo, other.hi)
        ]
        lo = min(_CTX_LO.divide(a, b) for a, b in corners)
        hi = max(_CTX_HI.divide(a, b) for a, b in corners)
        return Interval(lo, hi)

    # -- transcendental (monotone) --------------------------------------
    def exp(self) -> "Interval":
        lo = _CTX_LO.next_minus(_CTX_LO.exp(self.lo))
        hi = _CTX_HI.next_plus(_CTX_HI.exp(self.hi))
        if lo < _ZERO:
            # exp is strictly positive; clamping the underflowed lower
            # bound (-Etiny from next_minus(0)) to 0 is a rigorous cut.
            lo = _ZERO
        return Interval(lo, hi)

    def ln(self) -> "Interval":
        if self.lo < _ZERO:
            raise ValueError(f"ln of non-positive interval {self!r}")
        lo = (
            _NEG_INF
            if self.lo == _ZERO
            else _CTX_LO.next_minus(_CTX_LO.ln(self.lo))
        )
        hi = _CTX_HI.next_plus(_CTX_HI.ln(self.hi))
        return Interval._unchecked(lo, hi)

    def expm1(self) -> "Interval":
        lo = _CTX_LO.next_minus(_CTX_LO.subtract(_CTX_LO.exp(self.lo), _ONE))
        hi = _CTX_HI.next_plus(_CTX_HI.subtract(_CTX_HI.exp(self.hi), _ONE))
        return Interval(lo, hi)

    def log1p(self) -> "Interval":
        if self.lo < -_ONE:
            raise ValueError(f"log1p of interval below -1: {self!r}")
        lo = (
            _NEG_INF
            if self.lo == -_ONE
            else _CTX_LO.next_minus(_CTX_LO.ln(_CTX_LO.add(_ONE, self.lo)))
        )
        hi = _CTX_HI.next_plus(_CTX_HI.ln(_CTX_HI.add(_ONE, self.hi)))
        return Interval._unchecked(lo, hi)

    @staticmethod
    def _unchecked(lo: Decimal, hi: Decimal) -> "Interval":
        # Internal constructor allowing infinite lower bounds produced by
        # ln(0)/log1p(-1); callers intersect with finite universal bounds.
        obj = object.__new__(Interval)
        obj.lo = lo
        obj.hi = hi
        return obj

    # -- queries ---------------------------------------------------------
    def contains(self, value: object) -> bool:
        value_d = _dec(value)
        return self.lo <= value_d <= self.hi

    def intersect(self, lo: object, hi: object) -> "Interval":
        lo_d = max(self.lo, _dec(lo))
        hi_d = min(self.hi, _dec(hi))
        if lo_d > hi_d:  # pragma: no cover - indicates a bug upstream
            raise ValueError("intersection is empty")
        return Interval._unchecked(lo_d, hi_d)

    def to_float(self, margin_ulp: int = 0) -> tuple[float, float]:
        """Float pair (lo_f, hi_f) enclosing [lo, hi], rounded outward.

        With ``margin_ulp = 0`` this is the tightest rigorous float hull of
        the exact value.  A positive margin widens the hull by that many
        additional ulps on each side, e.g. to absorb the rounding error of
        a separate float evaluation of the same expression.
        """
        lo_f = math.nextafter(float(self.lo), -math.inf)
        hi_f = math.nextafter(float(self.hi), math.inf)
        for _ in range(margin_ulp):
            lo_f = math.nextafter(lo_f, -math.inf)
            hi_f = math.nextafter(hi_f, math.inf)
        return lo_f, hi_f
