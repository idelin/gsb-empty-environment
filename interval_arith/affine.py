"""Affine arithmetic: correlation-aware range analysis.

A quantity is represented as      x = c0 + sum_i ci * eps_i
with independent noise symbols eps_i in [-1, 1].  Because two forms derived
from the same variable share the same eps_i, repeated references to one
variable keep their correlation instead of being treated as independent.

* Linear operations (add, subtract, scale) are *exact* over the rationals.
* Nonlinear operations (mul, square) keep the linear part exact and push
  the nonlinear remainder into a fresh noise symbol whose magnitude is a
  verifiable conservative bound:
      |(sum ai eps_i)(sum bi eps_i)| <= (sum|ai|)(sum|bi|)   since |eps_i| <= 1
      E in [-r, r]  =>  E^2 in [0, r^2]                        (for square)
  Both bounds are checkable by pure rational arithmetic -- no symbolic
  algebra package is involved.
"""
from __future__ import annotations

from fractions import Fraction

from .interval import Interval


class NoiseRegistry:
    """Hands out fresh noise-symbol ids."""

    def __init__(self):
        self._next = 0

    def fresh(self) -> int:
        nid = self._next
        self._next += 1
        return nid


class AffineForm:
    __slots__ = ("const", "terms")

    def __init__(self, const, terms: dict[int, Fraction] | None = None):
        self.const = Fraction(const)
        self.terms = {k: Fraction(v) for k, v in (terms or {}).items() if v != 0}

    # ------------------------------------------------------------------ #
    @classmethod
    def from_interval(cls, iv: Interval, reg: NoiseRegistry, cert=None) -> "AffineForm":
        """x in [lo, hi]  =>  x = (lo+hi)/2 + (hi-lo)/2 * eps_k, eps_k in [-1,1].
        Exact: the form represents precisely the original interval."""
        if not iv.is_bounded():
            raise ArithmeticError("affine forms need a bounded interval")
        center = (iv.lo + iv.hi) / 2
        radius = (iv.hi - iv.lo) / 2
        nid = reg.fresh()
        if cert is not None:
            cert.add(
                f"introduce noise symbol eps_{nid} in [-1,1]: "
                f"x = {center} + {radius}*eps_{nid} represents {iv} exactly",
                domain=str(iv),
            )
        return cls(center, {nid: radius})

    # ------------------------------------------------------------------ #
    def radius(self) -> Fraction:
        """sum |ci| -- the total excursion of the noise part (|eps_i| <= 1)."""
        return sum((abs(c) for c in self.terms.values()), Fraction(0))

    def to_interval(self) -> Interval:
        r = self.radius()
        return Interval(self.const - r, self.const + r)

    # ------------------------------------------------------------------ #
    # exact linear part
    # ------------------------------------------------------------------ #
    def __add__(self, other):
        other = as_affine(other)
        terms = dict(self.terms)
        for nid, c in other.terms.items():
            terms[nid] = terms.get(nid, Fraction(0)) + c
        return AffineForm(self.const + other.const, terms)

    __radd__ = __add__

    def __neg__(self):
        return AffineForm(-self.const, {k: -c for k, c in self.terms.items()})

    def __sub__(self, other):
        return self + (-as_affine(other))

    def __rsub__(self, other):
        return as_affine(other) + (-self)

    def scale(self, k) -> "AffineForm":
        k = Fraction(k)
        return AffineForm(self.const * k, {i: c * k for i, c in self.terms.items()})

    # ------------------------------------------------------------------ #
    # nonlinear operations: exact linear part + verifiable remainder
    # ------------------------------------------------------------------ #
    def mul(self, other, reg: NoiseRegistry, cert=None) -> "AffineForm":
        """(a0 + A)(b0 + B) = a0*b0 + (a0*B + b0*A)  +  A*B.

        The linear part a0*B + b0*A is exact.  The remainder satisfies
        |A*B| <= (sum|ai|)(sum|bi|) because |eps_i| <= 1; it is enclosed in
        one fresh noise symbol of that magnitude (conservative, and the
        bound is re-checkable by pure rational arithmetic).
        """
        other = as_affine(other)
        const = self.const * other.const
        terms: dict[int, Fraction] = {}
        for nid, c in self.terms.items():
            terms[nid] = terms.get(nid, Fraction(0)) + c * other.const
        for nid, c in other.terms.items():
            terms[nid] = terms.get(nid, Fraction(0)) + c * self.const
        ra, rb = self.radius(), other.radius()
        remainder = ra * rb
        if remainder:
            nid = reg.fresh()
            terms[nid] = terms.get(nid, Fraction(0)) + remainder
            if cert is not None:
                cert.add(
                    f"product remainder |A*B| <= (sum|ai|)(sum|bi|) = "
                    f"{ra}*{rb} = {remainder} enclosed in eps_{nid} "
                    f"(valid since |eps_i| <= 1)",
                    domain="all of Q (bound is uniform)",
                )
        if cert is not None:
            cert.add(
                "linear part a0*B + b0*A kept exactly",
                domain="all of Q",
            )
        return AffineForm(const, terms)

    def square(self, reg: NoiseRegistry, cert=None) -> "AffineForm":
        """(c0 + E)^2 = c0^2 + 2*c0*E + E^2  with E in [-r, r], r = sum|ci|.

        Linear part 2*c0*E is exact; E^2 in [0, r^2] is enclosed as
        r^2/2 + (r^2/2)*eps_new.  For x in [-1,1] (c0=0, r=1) this gives
        exactly [0, 1] -- the correlation x*x is exploited, unlike plain
        interval multiplication.
        """
        c0 = self.const
        r = self.radius()
        terms = {nid: 2 * c0 * coef for nid, coef in self.terms.items()}
        half = r * r / 2
        const = c0 * c0 + half
        if half:
            nid = reg.fresh()
            terms[nid] = terms.get(nid, Fraction(0)) + half
            if cert is not None:
                cert.add(
                    f"square remainder E^2 in [0, r^2] = [0, {r*r}] with "
                    f"r = sum|ci| = {r}; enclosed as {half} + {half}*eps_{nid}",
                    domain="all of Q (bound is uniform)",
                )
        if cert is not None:
            cert.add("linear part 2*c0*E kept exactly", domain="all of Q")
        return AffineForm(const, terms)

    # ------------------------------------------------------------------ #
    def __repr__(self):
        body = " + ".join(f"{c}*eps_{k}" for k, c in sorted(self.terms.items()))
        return f"AffineForm({self.const}{' + ' if body else ''}{body})"


def as_affine(x) -> AffineForm:
    if isinstance(x, AffineForm):
        return x
    return AffineForm(Fraction(x))
