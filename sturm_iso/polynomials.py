"""Exact arithmetic for univariate polynomials over the rationals Q.

Representation: a polynomial is a tuple of ``Fraction`` coefficients in
ascending order of powers, with no trailing zeros. The zero polynomial is
the empty tuple.

Applicability domain: every operation here is exact over Q
(characteristic 0). Division, gcd and Yun's square-free factorization are
valid because Q[x] is a Euclidean domain over a perfect field of
characteristic 0. No floating point and no symbolic algebra library is
used anywhere.
"""

from __future__ import annotations

from fractions import Fraction
from typing import Tuple

Poly = Tuple[Fraction, ...]

ZERO: Poly = ()
ONE: Poly = (Fraction(1),)


def poly(coeffs) -> Poly:
    """Normalize coefficients into the canonical tuple form."""
    out = tuple(Fraction(c) for c in coeffs)
    n = len(out)
    while n and out[n - 1] == 0:
        n -= 1
    return out[:n]


def is_zero(p: Poly) -> bool:
    return len(p) == 0


def degree(p: Poly) -> int:
    """Degree of p; the zero polynomial has degree -1."""
    return len(p) - 1


def leading_coeff(p: Poly) -> Fraction:
    return p[-1]


def neg(a: Poly) -> Poly:
    return tuple(-c for c in a)


def add(a: Poly, b: Poly) -> Poly:
    n = max(len(a), len(b))
    out = [Fraction(0)] * n
    for i, c in enumerate(a):
        out[i] += c
    for i, c in enumerate(b):
        out[i] += c
    return poly(out)


def sub(a: Poly, b: Poly) -> Poly:
    return add(a, neg(b))


def scale(a: Poly, s) -> Poly:
    s = Fraction(s)
    return poly([s * c for c in a])


def mul(a: Poly, b: Poly) -> Poly:
    if not a or not b:
        return ZERO
    out = [Fraction(0)] * (len(a) + len(b) - 1)
    for i, ca in enumerate(a):
        if ca == 0:
            continue
        for j, cb in enumerate(b):
            if cb:
                out[i + j] += ca * cb
    return poly(out)


def divmod_poly(a: Poly, b: Poly) -> Tuple[Poly, Poly]:
    """Exact polynomial division over Q: a = b*q + r, deg r < deg b."""
    if is_zero(b):
        raise ZeroDivisionError("polynomial division by zero")
    rem = list(a)
    quo = [Fraction(0)] * max(0, len(a) - len(b) + 1)
    inv_lc = 1 / b[-1]
    while len(rem) >= len(b) and rem:
        shift = len(rem) - len(b)
        coeff = rem[-1] * inv_lc
        quo[shift] = coeff
        for i, cb in enumerate(b):
            rem[shift + i] -= coeff * cb
        while rem and rem[-1] == 0:
            rem.pop()
    return poly(quo), poly(rem)


def gcd(a: Poly, b: Poly) -> Poly:
    """Monic greatest common divisor; valid in the PID Q[x]."""
    while not is_zero(b):
        _, r = divmod_poly(a, b)
        a, b = b, r
    if is_zero(a):
        return ZERO
    return scale(a, 1 / a[-1])


def monic(p: Poly) -> Poly:
    if is_zero(p):
        raise ValueError("zero polynomial has no monic form")
    return scale(p, 1 / p[-1])


def derivative(p: Poly) -> Poly:
    """Formal derivative; exact in characteristic 0."""
    return poly([i * p[i] for i in range(1, len(p))])


def evaluate(p: Poly, x) -> Fraction:
    """Exact Horner evaluation at a rational point."""
    x = Fraction(x)
    acc = Fraction(0)
    for c in reversed(p):
        acc = acc * x + c
    return acc


def squarefree_factorization(p: Poly) -> Tuple[Tuple[Poly, int], ...]:
    """Yun's algorithm: p = lc(p) * prod f_i**i.

    Each f_i is monic, square-free, nonconstant, and the f_i are pairwise
    coprime. Valid over Q (perfect field of characteristic 0). Returns
    ((f_i, i), ...) sorted by multiplicity index i.
    """
    p = monic(p)
    result = []
    a = gcd(p, derivative(p))
    b, _ = divmod_poly(p, a)
    c, _ = divmod_poly(derivative(p), a)
    d = sub(c, derivative(b))
    i = 1
    while b != ONE:
        ai = gcd(b, d)
        if degree(ai) > 0:
            result.append((ai, i))
        b, _ = divmod_poly(b, ai)
        c, _ = divmod_poly(d, ai)
        i += 1
        d = sub(c, derivative(b))
    return tuple(result)


def poly_to_str(p: Poly) -> str:
    if not p:
        return "0"
    terms = []
    for power in range(len(p) - 1, -1, -1):
        c = p[power]
        if c == 0:
            continue
        sign = "-" if c < 0 else "+"
        mag = abs(c)
        if power == 0:
            body = str(mag)
        elif power == 1:
            body = "x" if mag == 1 else f"{mag}x"
        else:
            body = f"x^{power}" if mag == 1 else f"{mag}x^{power}"
        terms.append((sign, body))
    if not terms:
        return "0"
    first_sign, first_body = terms[0]
    head = first_body if first_sign == "+" else f"-{first_body}"
    rest = "".join(f" {s} {b}" for s, b in terms[1:])
    return head + rest
