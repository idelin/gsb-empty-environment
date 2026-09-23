"""Exact rational interval arithmetic (Python 3.11, standard library only).

Design notes
------------
* Endpoints are ``fractions.Fraction`` -- every operation is exact, no
  floating point error is ever introduced.  "Conservative endpoint rounding"
  is only needed when importing inexact data (floats) or when the caller
  explicitly asks to round outward onto a coarser rational grid
  (:meth:`Interval.round_outward`).
* ``lo is None`` means -infinity, ``hi is None`` means +infinity.  Unbounded
  intervals appear as *results* of splitting a division whose divisor
  crosses 0 -- the singularity is never hidden.
* ``lo_open`` / ``hi_open`` are domain metadata (used when a domain is split
  at a singularity).  Arithmetic is always performed on the closure, which
  is conservative and therefore sound; openness only affects ``contains``
  and the printed form.
* Dependency: plain ``*`` assumes independent operands.  ``x * x`` is
  therefore conservative (e.g. [-1,1]*[-1,1] = [-1,1]); use
  :meth:`Interval.square` (tight, [0,1]) or the affine forms in
  ``interval_arith.affine`` to exploit the correlation.  This is stated
  again by :func:`dependency_note`.
"""
from __future__ import annotations

import math
from fractions import Fraction


class DivisionByZeroInterval(ArithmeticError):
    """Raised when a divisor interval contains 0.

    The zero is reported, never silently absorbed: the quotient is
    undefined at 0 and discontinuous around it.  Use
    :meth:`Interval.reciprocal_split` / :meth:`Interval.div_split`
    to analyse the domain piece by piece instead.
    """

    def __init__(self, divisor: "Interval"):
        self.divisor = divisor
        super().__init__(
            f"divisor interval {divisor} contains 0; the quotient is undefined "
            "at 0 and unbounded around it. Use reciprocal_split()/div_split() "
            "to split the domain at the singularity."
        )


def _to_frac(value):
    if value is None:
        return None
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        # Exact conversion of the binary float -- no rounding at all.
        return Fraction(value)
    return Fraction(value)


class _Inf:
    """Internal signed infinity used for extended-real corner products."""

    __slots__ = ("sign",)

    def __init__(self, sign: int):
        self.sign = sign  # +1 or -1

    def __lt__(self, other):
        if isinstance(other, _Inf):
            return self.sign < other.sign
        return self.sign < 0

    def __le__(self, other):
        return self == other or self < other

    def __gt__(self, other):
        if isinstance(other, _Inf):
            return self.sign > other.sign
        return self.sign > 0

    def __ge__(self, other):
        return self == other or self > other

    def __eq__(self, other):
        return isinstance(other, _Inf) and self.sign == other.sign

    def __hash__(self):
        return hash(("_Inf", self.sign))

    def __repr__(self):
        return "+inf" if self.sign > 0 else "-inf"


_NEG_INF = _Inf(-1)
_POS_INF = _Inf(+1)


def _ext_mul(x, y):
    """Product of two extended reals (Fraction or _Inf), with 0*inf := 0.

    For *interval* operands the min/max of the four corner products under
    this convention equals the true range of the product, because any
    0*infinity corner is dominated by genuine corners (the operand that is
    0 is an interior/endpoint value combined with the whole other interval).
    """
    if isinstance(x, _Inf) and isinstance(y, _Inf):
        return _Inf(x.sign * y.sign)
    if isinstance(x, _Inf):
        if y > 0:
            return x
        if y < 0:
            return _Inf(-x.sign)
        return Fraction(0)
    if isinstance(y, _Inf):
        return _ext_mul(y, x)
    return x * y


def _lo_ext(iv):
    return _NEG_INF if iv.lo is None else iv.lo


def _hi_ext(iv):
    return _POS_INF if iv.hi is None else iv.hi


class Interval:
    """A closed (or half-open) interval with exact rational endpoints."""

    __slots__ = ("lo", "hi", "lo_open", "hi_open")

    def __init__(self, lo, hi, lo_open: bool = False, hi_open: bool = False):
        lo = _to_frac(lo)
        hi = _to_frac(hi)
        if lo is not None and hi is not None and lo > hi:
            raise ValueError(f"empty interval: [{lo}, {hi}]")
        if lo is not None and hi is not None and lo == hi and (lo_open or hi_open):
            raise ValueError("empty interval: open endpoints coincide")
        self.lo = lo
        self.hi = hi
        self.lo_open = bool(lo_open)
        self.hi_open = bool(hi_open)

    # ------------------------------------------------------------------ #
    # basics
    # ------------------------------------------------------------------ #
    def is_bounded(self) -> bool:
        return self.lo is not None and self.hi is not None

    def contains(self, x) -> bool:
        x = _to_frac(x)
        if self.lo is not None:
            if x < self.lo or (x == self.lo and self.lo_open):
                return False
        if self.hi is not None:
            if x > self.hi or (x == self.hi and self.hi_open):
                return False
        return True

    def subset_of(self, other: "Interval") -> bool:
        """Sound (conservative) subset test on closures."""
        lo_ok = other.lo is None or (self.lo is not None and self.lo >= other.lo)
        hi_ok = other.hi is None or (self.hi is not None and self.hi <= other.hi)
        return lo_ok and hi_ok

    def width(self):
        if not self.is_bounded():
            return None
        return self.hi - self.lo

    def hull(self, other: "Interval") -> "Interval":
        lo = self.lo if other.lo is None or (self.lo is not None and self.lo <= other.lo) else other.lo
        hi = self.hi if other.hi is None or (self.hi is not None and self.hi >= other.hi) else other.hi
        return Interval(lo, hi)

    # ------------------------------------------------------------------ #
    # linear operations (exact)
    # ------------------------------------------------------------------ #
    def __add__(self, other):
        other = as_interval(other)
        lo = None if self.lo is None or other.lo is None else self.lo + other.lo
        hi = None if self.hi is None or other.hi is None else self.hi + other.hi
        return Interval(lo, hi)

    __radd__ = __add__

    def __neg__(self):
        return Interval(
            None if self.hi is None else -self.hi,
            None if self.lo is None else -self.lo,
        )

    def __sub__(self, other):
        return self + (-as_interval(other))

    def __rsub__(self, other):
        return as_interval(other) + (-self)

    def scale(self, k) -> "Interval":
        """Multiply by an exact rational constant (linear, exact)."""
        k = _to_frac(k)
        if k == 0:
            return Interval(0, 0)
        a, b = _ext_mul(k, _lo_ext(self)), _ext_mul(k, _hi_ext(self))
        lo, hi = (a, b) if k > 0 else (b, a)
        return Interval(
            None if isinstance(lo, _Inf) else lo,
            None if isinstance(hi, _Inf) else hi,
        )

    # ------------------------------------------------------------------ #
    # multiplication / powers
    # ------------------------------------------------------------------ #
    def __mul__(self, other):
        """Interval product.  WARNING (dependency): operands are treated as
        independent.  ``x * x`` is conservative; see :func:`dependency_note`.
        """
        other = as_interval(other)
        corners = [
            _ext_mul(_lo_ext(self), _lo_ext(other)),
            _ext_mul(_lo_ext(self), _hi_ext(other)),
            _ext_mul(_hi_ext(self), _lo_ext(other)),
            _ext_mul(_hi_ext(self), _hi_ext(other)),
        ]
        lo, hi = min(corners), max(corners)
        if isinstance(lo, _Inf) and lo.sign > 0 or isinstance(hi, _Inf) and hi.sign < 0:
            raise ArithmeticError("empty product of unbounded intervals")
        return Interval(
            None if isinstance(lo, _Inf) else lo,
            None if isinstance(hi, _Inf) else hi,
        )

    __rmul__ = __mul__

    def square(self) -> "Interval":
        """Tight square: x and x are the *same* quantity, so the result is
        [0, max(lo^2, hi^2)] when 0 is in the interval -- e.g. [-1,1]^2 = [0,1].
        """
        if not self.is_bounded():
            raise ArithmeticError("square of unbounded interval is not represented")
        a, b = self.lo ** 2, self.hi ** 2
        lo = Fraction(0) if self.contains(0) else min(a, b)
        return Interval(lo, max(a, b))

    def pow(self, n: int) -> "Interval":
        """x**n for integer n >= 0.  Odd n: monotone, endpoints suffice.
        Even n: tight via :meth:`square`-style reasoning."""
        if n < 0:
            raise ValueError("negative exponents need reciprocal_split (singularity at 0)")
        if n == 0:
            return Interval(1, 1)
        if n % 2 == 1:
            return Interval(self.lo ** n, self.hi ** n)
        if self.contains(0):
            return Interval(0, max(self.lo ** n, self.hi ** n))
        a, b = self.lo ** n, self.hi ** n
        return Interval(min(a, b), max(a, b))

    # ------------------------------------------------------------------ #
    # division -- the singularity at 0 is never hidden
    # ------------------------------------------------------------------ #
    def reciprocal(self) -> "Interval":
        """1/x, monotone decreasing on x>0 and on x<0 separately.

        Valid when the closure excludes 0, or when 0 is only an *open*
        endpoint (the value 0 is then not part of the domain, and the
        honest image is unbounded towards the singularity).  A closed 0
        raises :class:`DivisionByZeroInterval` -- never hidden.
        """
        if self.lo is None or self.hi is None:
            raise ArithmeticError("reciprocal of unbounded interval: split first")
        if self.lo == 0 and self.lo_open and self.hi > 0:
            return Interval(Fraction(1) / self.hi, None)
        if self.hi == 0 and self.hi_open and self.lo < 0:
            return Interval(None, Fraction(1) / self.lo)
        if self.lo <= 0 <= self.hi:
            raise DivisionByZeroInterval(self)
        return Interval(Fraction(1) / self.hi, Fraction(1) / self.lo)

    def reciprocal_split(self):
        """Split at the singularity 0.

        Returns a list of ``(subdomain, image)`` pairs.  The subdomains
        exclude 0 (open endpoint); images may be unbounded, which is the
        honest answer -- 1/x diverges at 0.
        """
        if self.lo is None or self.hi is None:
            raise ArithmeticError("split an unbounded domain first")
        if self.lo > 0 or self.hi < 0:
            return [(self, self.reciprocal())]
        pieces = []
        if self.lo < 0:
            sub = Interval(self.lo, 0, hi_open=True)
            pieces.append((sub, Interval(None, Fraction(1) / self.lo)))
        if self.hi > 0:
            sub = Interval(0, self.hi, lo_open=True)
            pieces.append((sub, Interval(Fraction(1) / self.hi, None)))
        if not pieces:  # self == [0, 0]
            raise DivisionByZeroInterval(self)
        return pieces

    def __truediv__(self, other):
        other = as_interval(other)
        if other.lo is not None and other.hi is not None and other.lo <= 0 <= other.hi:
            raise DivisionByZeroInterval(other)
        return self * other.reciprocal()

    def div_split(self, other):
        """self / other with the divisor split at 0.

        Returns ``(subdomain_of_divisor, quotient)`` pairs; quotients may be
        unbounded.  Nothing about the singularity is discarded.
        """
        other = as_interval(other)
        return [(sub, self * rec) for sub, rec in other.reciprocal_split()]

    # ------------------------------------------------------------------ #
    # monotonic functions
    # ------------------------------------------------------------------ #
    def apply_monotone(self, f, increasing: bool = True, name: str = "f") -> "Interval":
        """Image under a function that is *monotone on this whole interval*.

        ``f`` must map Fraction -> Fraction exactly and be monotonic on the
        closure of ``self``; that precondition is the caller's
        responsibility and should be recorded in the certificate.
        """
        if not self.is_bounded():
            raise ArithmeticError("apply_monotone needs a bounded interval")
        a, b = f(self.lo), f(self.hi)
        lo, hi = (a, b) if increasing else (b, a)
        if lo > hi:
            raise ArithmeticError(f"{name} does not appear monotone as claimed")
        return Interval(lo, hi)

    def cube(self) -> "Interval":
        """x^3 -- monotone increasing on all of Q."""
        return self.apply_monotone(lambda t: t ** 3, increasing=True, name="x^3")

    # ------------------------------------------------------------------ #
    # conservative outward rounding
    # ------------------------------------------------------------------ #
    @classmethod
    def from_float(cls, x: float) -> "Interval":
        """Exact interval for a float (the float itself, no hidden rounding)."""
        return cls(Fraction(x), Fraction(x))

    @classmethod
    def from_float_pair(cls, lo: float, hi: float) -> "Interval":
        """Conservative interval for a float pair: the binary floats are
        converted exactly, so the true decimal values are always enclosed."""
        return cls(Fraction(lo), Fraction(hi))

    def round_outward(self, step) -> "Interval":
        """Round endpoints *outward* onto the grid ``k * step`` (step > 0).

        lo is rounded down, hi is rounded up, so the result always contains
        the original interval -- conservative by construction.
        """
        step = _to_frac(step)
        if step <= 0:
            raise ValueError("step must be positive")
        if not self.is_bounded():
            return Interval(self.lo, self.hi)
        lo = Fraction(math.floor(self.lo / step)) * step
        hi = Fraction(math.ceil(self.hi / step)) * step
        return Interval(lo, hi)

    # ------------------------------------------------------------------ #
    # dunder cosmetics
    # ------------------------------------------------------------------ #
    def __eq__(self, other):
        return (
            isinstance(other, Interval)
            and (self.lo, self.hi, self.lo_open, self.hi_open)
            == (other.lo, other.hi, other.lo_open, other.hi_open)
        )

    def __repr__(self):
        lo = "-inf" if self.lo is None else str(self.lo)
        hi = "+inf" if self.hi is None else str(self.hi)
        left = "(" if self.lo_open else "["
        right = ")" if self.hi_open else "]"
        return f"{left}{lo}, {hi}{right}"


def as_interval(x) -> Interval:
    if isinstance(x, Interval):
        return x
    f = _to_frac(x)
    return Interval(f, f)


def dependency_note(a: Interval, b: Interval) -> str | None:
    """Explain the dependency caveat when two correlated intervals are
    multiplied directly.  Returns None when there is nothing to note."""
    if a is b or a == b:
        return (
            f"dependency: computing {a} * {b} with plain interval "
            "multiplication treats the two factors as independent and is "
            "conservative (e.g. [-1, 1]*[-1, 1] = [-1, 1] while the tight "
            "square is [0, 1]). Use Interval.square() or affine forms "
            "(interval_arith.affine) to exploit the correlation."
        )
    return None
