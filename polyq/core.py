"""Univariate polynomials over Q with exact Fraction coefficients.

Representation: a polynomial is a list of ``fractions.Fraction`` where the
index is the power of x.  Canonical form has no trailing zeros; the zero
polynomial is the empty list ``[]`` (degree -1 by convention).

Domain notes
------------
* Addition, subtraction, multiplication: valid over any ring.
* ``divmod_q``: exact polynomial division, valid over any field (here Q).
* ``pseudo_divmod``: valid over any integral domain (no division needed);
  used as the growth-controlled building block of the subresultant PRS.
"""
from __future__ import annotations

from fractions import Fraction

Poly = list  # list[Fraction], index == power of x


def poly(coeffs) -> Poly:
    """Build a canonical polynomial from an iterable of int/Fraction/str."""
    return trim([Fraction(c) for c in coeffs])


def trim(p) -> Poly:
    p = [Fraction(c) for c in p]
    while p and p[-1] == 0:
        p.pop()
    return p


def is_zero(p: Poly) -> bool:
    return len(p) == 0


def deg(p: Poly) -> int:
    """Degree; the zero polynomial has degree -1."""
    return len(p) - 1


def lc(p: Poly) -> Fraction:
    """Leading coefficient; undefined for the zero polynomial."""
    if not p:
        raise ValueError("zero polynomial has no leading coefficient")
    return p[-1]


def monic(p: Poly) -> Poly:
    """Scale by 1/lc(p).  Valid over a field; the zero polynomial is unchanged."""
    if not p:
        return []
    return scale(p, Fraction(1, 1) / lc(p))


def shift(p: Poly, e: int) -> Poly:
    """Multiply by x**e.  Valid over any ring."""
    if not p:
        return []
    return [Fraction(0)] * e + list(p)


def scale(p: Poly, c) -> Poly:
    """Multiply by a scalar.  Valid over any ring."""
    c = Fraction(c)
    if c == 0 or not p:
        return []
    return trim([c * x for x in p])


def neg(p: Poly) -> Poly:
    return [-x for x in p]


def add(a: Poly, b: Poly) -> Poly:
    n = max(len(a), len(b))
    r = [Fraction(0)] * n
    for i, c in enumerate(a):
        r[i] += c
    for i, c in enumerate(b):
        r[i] += c
    return trim(r)


def sub(a: Poly, b: Poly) -> Poly:
    return add(a, neg(b))


def mul(a: Poly, b: Poly) -> Poly:
    if not a or not b:
        return []
    r = [Fraction(0)] * (len(a) + len(b) - 1)
    for i, x in enumerate(a):
        if x == 0:
            continue
        for j, y in enumerate(b):
            if y:
                r[i + j] += x * y
    return trim(r)


def divmod_q(a: Poly, b: Poly):
    """Exact division over Q[x]: returns (q, r) with a == q*b + r, deg r < deg b.

    Raises ZeroDivisionError for the zero divisor.  Valid over any field.
    """
    b = trim(b)
    if not b:
        raise ZeroDivisionError("polynomial division by the zero polynomial")
    r = trim(a)
    if deg(r) < deg(b):
        return [], r
    q = [Fraction(0)] * (deg(r) - deg(b) + 1)
    nb, lb = deg(b), lc(b)
    while deg(r) >= nb:
        e = deg(r) - nb
        c = lc(r) / lb
        q[e] = c
        r = sub(r, shift(scale(b, c), e))
    return trim(q), r


def pseudo_divmod(a: Poly, b: Poly):
    """Pseudo-division: returns (q, r, factor) such that

        factor * a == q*b + r,   deg r < deg b,   factor == lc(b)**k,

    where k <= deg(a) - deg(b) + 1 is the number of rounds actually
    performed (leading-coefficient cancellation may end the loop early,
    so the caller must use the returned factor, not assume k = m - n + 1).

    Valid over any integral domain (only ring operations are used); here it
    is applied inside Q[x].  Raises ZeroDivisionError for the zero divisor.
    """
    b = trim(b)
    if not b:
        raise ZeroDivisionError("polynomial pseudo-division by the zero polynomial")
    a = trim(a)
    if deg(a) < deg(b):
        return [], a, Fraction(1)
    d = lc(b)
    nb = deg(b)
    q: Poly = []
    r = a
    factor = Fraction(1)
    while deg(r) >= nb:
        e = deg(r) - nb
        s = lc(r)
        # q <- d*q + s*x^e ; r <- d*r - s*x^e*b   (one pseudo-division round)
        q = add(scale(q, d), shift([s], e))
        r = sub(scale(r, d), shift(scale(b, s), e))
        factor *= d
    return trim(q), r, factor


def to_str(p: Poly) -> str:
    """Human-readable form, e.g. '2*x^2 - 1/3*x + 4'."""
    if not p:
        return "0"
    terms = []
    for i in range(len(p) - 1, -1, -1):
        c = p[i]
        if c == 0:
            continue
        if c < 0:
            sign = " - " if terms else "-"
        else:
            sign = " + " if terms else ""
        mag = -c if c < 0 else c
        if i == 0:
            body = str(mag)
        elif i == 1:
            body = "x" if mag == 1 else f"{mag}*x"
        else:
            body = f"x^{i}" if mag == 1 else f"{mag}*x^{i}"
        terms.append(sign + body)
    return "".join(terms) if terms else "0"
