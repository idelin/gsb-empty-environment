"""Affine-arithmetic core: shared noise symbols and affine forms.

An affine form is  x = center + sum_i terms[sid_i] * eps_i  where each
eps_i is a noise symbol with |eps_i| <= radius(sid_i) held in a shared
NoiseSymbols registry.  Because noise symbols are shared across forms,
correlations between quantities derived from the same source are
preserved (e.g. x - x is exactly 0), while independent variables use
distinct symbols and never cancel.

Non-linear operations use the standard *min-range* (Chebyshev) affine
approximations with conservative remainder bounds, as published in:

  - J. Stolfi & L. H. de Figueiredo, "Self-Validated Numerical Methods
    and Applications", IMPA monograph, 1997.
  - L. H. de Figueiredo & J. Stolfi, "Affine Arithmetic: Concepts and
    Applications", Numerical Algorithms 37(1-4), 2004.

Only the Python standard library is used.
"""

from __future__ import annotations

import math


class IntervalContainsZero(ArithmeticError):
    """Raised when a reciprocal/division operand interval contains 0."""


class NoiseSymbols:
    """Registry mapping noise-symbol ids to their radii (>= 0)."""

    def __init__(self) -> None:
        self._radii: dict[int, float] = {}
        self._next_id = 0

    def new(self, radius: float) -> int:
        radius = float(radius)
        if radius < 0.0 or math.isnan(radius):
            raise ValueError("noise radius must be a non-negative number")
        sid = self._next_id
        self._next_id += 1
        self._radii[sid] = radius
        return sid

    def set_radius(self, sid: int, radius: float) -> None:
        radius = float(radius)
        if radius < 0.0 or math.isnan(radius):
            raise ValueError("noise radius must be a non-negative number")
        if sid not in self._radii:
            raise KeyError(f"unknown noise symbol id {sid}")
        self._radii[sid] = radius

    def radius(self, sid: int) -> float:
        return self._radii[sid]

    def snapshot(self) -> dict[int, float]:
        return dict(self._radii)


class AffineForm:
    """center + sum(terms[sid] * eps_sid)."""

    __slots__ = ("center", "terms")

    def __init__(self, center: float = 0.0, terms: dict[int, float] | None = None):
        self.center = float(center)
        self.terms: dict[int, float] = dict(terms) if terms else {}

    def copy(self) -> "AffineForm":
        return AffineForm(self.center, self.terms)

    def radius(self, symbols: NoiseSymbols) -> float:
        return sum(abs(c) * symbols.radius(s) for s, c in self.terms.items())

    def interval(self, symbols: NoiseSymbols) -> tuple[float, float]:
        r = self.radius(symbols)
        return (self.center - r, self.center + r)

    def num_terms(self) -> int:
        return len(self.terms)

    def __eq__(self, other: object) -> bool:
        return (
            isinstance(other, AffineForm)
            and self.center == other.center
            and self.terms == other.terms
        )

    def __repr__(self) -> str:
        return f"AffineForm(center={self.center!r}, terms={self.terms!r})"


def const(value: float) -> AffineForm:
    return AffineForm(float(value))


def add(a: AffineForm, b: AffineForm) -> AffineForm:
    terms = dict(a.terms)
    for sid, coef in b.terms.items():
        terms[sid] = terms.get(sid, 0.0) + coef
        if terms[sid] == 0.0:
            del terms[sid]
    return AffineForm(a.center + b.center, terms)


def sub(a: AffineForm, b: AffineForm) -> AffineForm:
    terms = dict(a.terms)
    for sid, coef in b.terms.items():
        terms[sid] = terms.get(sid, 0.0) - coef
        if terms[sid] == 0.0:
            del terms[sid]
    return AffineForm(a.center - b.center, terms)


def scale(a: AffineForm, factor: float) -> AffineForm:
    factor = float(factor)
    if factor == 0.0:
        return AffineForm(0.0)
    return AffineForm(a.center * factor, {s: c * factor for s, c in a.terms.items()})


def mul(a: AffineForm, b: AffineForm, symbols: NoiseSymbols) -> tuple[AffineForm, float]:
    """Affine product.  Returns (linear_part, remainder_radius).

    The linear part keeps all first-order terms exactly; the quadratic
    error  (sum |a_i| r_i) * (sum |b_j| r_j)  is returned as the radius of
    a fresh remainder noise symbol (standard AA multiplication).
    """
    terms: dict[int, float] = {}
    for sid, coef in a.terms.items():
        v = coef * b.center
        if v != 0.0:
            terms[sid] = terms.get(sid, 0.0) + v
    for sid, coef in b.terms.items():
        v = coef * a.center
        if v != 0.0:
            terms[sid] = terms.get(sid, 0.0) + v
    remainder = a.radius(symbols) * b.radius(symbols)
    return AffineForm(a.center * b.center, terms), remainder


def recip(a: AffineForm, symbols: NoiseSymbols) -> tuple[AffineForm, float]:
    """Min-range affine approximation of 1/x.  Returns (form, remainder_radius).

    Raises IntervalContainsZero if the input interval straddles or touches 0.
    """
    lo, hi = a.interval(symbols)
    if lo <= 0.0 <= hi:
        raise IntervalContainsZero(
            f"reciprocal undefined: operand interval [{lo!r}, {hi!r}] contains 0"
        )
    alpha = -1.0 / (lo * hi)
    # Critical point of f(x) - alpha*x inside [lo, hi] (same sign interval).
    xc = math.sqrt(lo * hi) if lo > 0.0 else -math.sqrt(lo * hi)

    def dev(x: float) -> float:
        return 1.0 / x - alpha * x

    deviations = (dev(lo), dev(hi), dev(xc))
    dmin = min(deviations)
    dmax = max(deviations)
    delta = (dmax - dmin) / 2.0
    zeta = (dmax + dmin) / 2.0
    form = scale(a, alpha)
    form.center += zeta
    return form, delta
