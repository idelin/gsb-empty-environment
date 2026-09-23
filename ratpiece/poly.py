"""Exact rational-coefficient polynomials.

Only :mod:`fractions` is used; no symbolic-algebra libraries are involved.
A polynomial is stored as a tuple of :class:`~fractions.Fraction` coefficients
in ascending degree order with no trailing zero.  Every arithmetic operation
below is valid over the whole coefficient ring Q[x] (exact division is
additionally documented where divisibility is required).
"""

from __future__ import annotations

from fractions import Fraction
from math import gcd


class Poly:
    """Polynomial with exact :class:`~fractions.Fraction` coefficients."""

    __slots__ = ("coeffs",)

    def __init__(self, coeffs=(0,)):
        clean = [Fraction(c) for c in coeffs]
        while len(clean) > 1 and clean[-1] == 0:
            clean.pop()
        self.coeffs: tuple[Fraction, ...] = tuple(clean)

    @staticmethod
    def const(value) -> "Poly":
        return Poly((Fraction(value),))

    @staticmethod
    def x() -> "Poly":
        return Poly((0, 1))

    @property
    def degree(self) -> int:
        return len(self.coeffs) - 1

    def is_zero(self) -> bool:
        return len(self.coeffs) == 1 and self.coeffs[0] == 0

    def leading(self) -> Fraction:
        return self.coeffs[-1]

    def __eq__(self, other) -> bool:
        return isinstance(other, Poly) and self.coeffs == other.coeffs

    def __hash__(self) -> int:
        return hash(self.coeffs)

    def __add__(self, other: "Poly") -> "Poly":
        n = max(len(self.coeffs), len(other.coeffs))
        out = [Fraction(0)] * n
        for i, c in enumerate(self.coeffs):
            out[i] += c
        for i, c in enumerate(other.coeffs):
            out[i] += c
        return Poly(out)

    def __sub__(self, other: "Poly") -> "Poly":
        return self + other.neg()

    def __neg__(self) -> "Poly":
        return Poly(tuple(-c for c in self.coeffs))

    def __mul__(self, other: "Poly") -> "Poly":
        if self.is_zero() or other.is_zero():
            return Poly((0,))
        out = [Fraction(0)] * (self.degree + other.degree + 1)
        for i, a in enumerate(self.coeffs):
            for j, b in enumerate(other.coeffs):
                out[i + j] += a * b
        return Poly(out)

    def scale(self, k) -> "Poly":
        k = Fraction(k)
        return Poly(tuple(c * k for c in self.coeffs))

    def eval(self, t) -> Fraction:
        """Exact Horner evaluation at a rational point; valid on all of Q."""
        t = Fraction(t)
        acc = Fraction(0)
        for c in reversed(self.coeffs):
            acc = acc * t + c
        return acc

    def sign_at(self, t) -> int:
        value = self.eval(t)
        return 1 if value > 0 else (-1 if value < 0 else 0)

    def deriv(self) -> "Poly":
        if self.degree == 0:
            return Poly((0,))
        return Poly(tuple(i * c for i, c in enumerate(self.coeffs) if i > 0))

    def monic(self) -> "Poly":
        """Divide by the leading coefficient.  Valid for nonzero polynomials."""
        if self.is_zero():
            raise ZeroDivisionError("cannot make the zero polynomial monic")
        return self.scale(Fraction(1, 1) / self.leading())

    def factor_linear(self, root) -> tuple["Poly", int]:
        """Divide out ``(x - root)`` as many times as it divides exactly.

        Returns ``(quotient, multiplicity)``.  Synthetic division is exact;
        remainder zero is checked after every division, so the returned
        quotient is an exact cofactor whenever multiplicity > 0.
        """
        r = Fraction(root)
        poly = self
        mult = 0
        while poly.degree >= 1:
            out = [Fraction(0)] * poly.degree
            carry = Fraction(0)
            for i in range(poly.degree, 0, -1):
                carry = poly.coeffs[i] + carry
                out[i - 1] = carry
                carry *= r
            remainder = poly.coeffs[0] + carry
            if remainder != 0:
                break
            poly = Poly(out)
            mult += 1
        return poly, mult

    def to_int_tuple(self) -> tuple[int, ...]:
        """Return a primitive integer coefficient tuple, positive leading sign."""
        if self.is_zero():
            return (0,)
        denoms = [c.denominator for c in self.coeffs if c != 0]
        common = 1
        for d in denoms:
            common = common * d // gcd(common, d)
        ints = [int(c * common) for c in self.coeffs]
        scale = 0
        for v in ints:
            scale = gcd(scale, abs(v))
        if scale == 0:
            return (0,)
        ints = [v // scale for v in ints]
        if ints[-1] < 0:
            ints = [-v for v in ints]
        return tuple(ints)

    def __repr__(self) -> str:
        return f"Poly({self.to_human()})"

    def to_human(self, variable: str = "x") -> str:
        if self.is_zero():
            return "0"
        terms: list[str] = []
        for power in range(self.degree, -1, -1):
            c = self.coeffs[power]
            if c == 0:
                continue
            if power == 0:
                body = str(c.numerator) if c.denominator == 1 else f"({c})"
            elif power == 1:
                base = variable
                body = base if c == 1 else f"-{base}" if c == -1 else f"{c}*{base}"
            else:
                base = f"{variable}^{power}"
                body = base if c == 1 else f"-{base}" if c == -1 else f"{c}*{base}"
            if terms and c > 0:
                terms.append(f"+ {body}")
            elif c < 0 and not body.startswith("-"):
                terms.append(f"- {body}")
            else:
                terms.append(body)
        return " ".join(terms)


def poly_gcd(a: Poly, b: Poly) -> Poly:
    """Monic greatest common divisor in Q[x] via the Euclidean algorithm."""
    a = a if not a.is_zero() else b
    if b.is_zero():
        return a.monic() if not a.is_zero() else Poly((0,))
    if a.is_zero():
        return b.monic()
    r0, r1 = a, b
    while not r1.is_zero():
        _, rem = divmod_poly(r0, r1)
        r0, r1 = r1, rem
    return r0.monic()


def squarefree_part(poly: Poly) -> Poly:
    """Monic square-free part: ``poly / gcd(poly, poly')``.

    Two polynomials have exactly the same (real and complex) zero sets iff
    their square-free parts coincide, so this is the exact domain fingerprint
    of a denominator.
    """
    if poly.is_zero():
        raise ValueError("zero polynomial has no square-free part")
    if poly.degree == 0:
        return Poly.const(1)
    q, _ = divmod_poly(poly, poly_gcd(poly, poly.deriv()))
    return q.monic()


def divmod_poly(a: Poly, b: Poly) -> tuple[Poly, Poly]:
    """Exact polynomial long division over Q; valid for nonzero ``b``."""
    if b.is_zero():
        raise ZeroDivisionError("polynomial division by zero")
    if a.degree < b.degree:
        return Poly((0,)), a
    remainder = list(a.coeffs)
    quotient = [Fraction(0)] * (a.degree - b.degree + 1)
    for shift in range(a.degree - b.degree, -1, -1):
        factor = remainder[shift + b.degree] / b.leading()
        quotient[shift] = factor
        if factor != 0:
            for j, c in enumerate(b.coeffs):
                remainder[shift + j] -= factor * c
    return Poly(quotient), Poly(remainder)


def sturm_remainder(a: Poly, b: Poly) -> Poly:
    """Return ``-(a mod b)``, the standard Sturm recurrence step."""
    _, rem = divmod_poly(a, b)
    return -rem


def sturm_sequence(poly: Poly) -> list[Poly]:
    """Sturm chain ``[p, p', ...]`` with positive normalisation only.

    Elements are scaled by 1/|leading coefficient| (a positive factor), which
    keeps every sign — and hence every variation count — intact.  Scaling by
    the signed leading coefficient (monic normalisation) would be invalid
    here because it can flip signs and corrupt the root count.
    """
    if poly.is_zero():
        raise ValueError("Sturm sequence requires a nonzero polynomial")
    chain = [_positive_normal(poly), _positive_normal(poly.deriv())]
    while not chain[-1].is_zero():
        nxt = sturm_remainder(chain[-2], chain[-1])
        if nxt.is_zero():
            break
        chain.append(_positive_normal(nxt))
    if chain[-1].is_zero():
        chain.pop()
    return chain


def _positive_normal(poly: Poly) -> Poly:
    if poly.is_zero():
        return poly
    return poly.scale(Fraction(1, 1) / abs(poly.leading()))


def sign_variations(chain: list[Poly], t) -> int:
    """Sign changes of the Sturm chain evaluated at ``t``.

    Zero entries are skipped; this matches the classical convention for
    endpoints that are exact roots, which root isolation excludes anyway.
    """
    signs = [p.sign_at(t) for p in chain]
    signs = [s for s in signs if s != 0]
    return sum(1 for a, b in zip(signs, signs[1:]) if a != b)


def cauchy_bound(poly: Poly) -> int:
    """Integer Cauchy bound: every real root has |x| <= 1 + max|a_i/a_n|."""
    if poly.degree == 0:
        return 0
    lead = poly.leading()
    bound = max(abs(c / lead) for c in poly.coeffs[:-1])
    return int(bound) + 2


def integer_divisors(value: int) -> list[int]:
    value = abs(value)
    if value == 0:
        return []
    divisors = {1, value}
    factor = 2
    while factor * factor <= value:
        if value % factor == 0:
            divisors.add(factor)
            divisors.add(value // factor)
        factor += 1
    return sorted(divisors)
