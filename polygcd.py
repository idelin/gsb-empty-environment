"""Exact univariate polynomial arithmetic over the rationals Q.

Representation: a polynomial is a little-endian list of Fraction,
p[i] = coefficient of x**i, with no trailing zeros. The zero polynomial
is the empty list []. All arithmetic is exact (fractions.Fraction);
no symbolic algebra libraries are used.
"""
from __future__ import annotations

from fractions import Fraction


def as_poly(p):
    """Coerce an iterable of int/Fraction to a trimmed polynomial."""
    return trim([Fraction(c) for c in p])


def trim(p):
    while p and p[-1] == 0:
        p.pop()
    return p


def deg(p):
    """Degree; deg(0) = -1 by convention."""
    return len(p) - 1


def lc(p):
    """Leading coefficient; lc(0) = 0."""
    return p[-1] if p else Fraction(0)


def neg(p):
    return [-c for c in p]


def add(a, b):
    n = max(len(a), len(b))
    out = []
    for i in range(n):
        x = a[i] if i < len(a) else Fraction(0)
        y = b[i] if i < len(b) else Fraction(0)
        out.append(x + y)
    return trim(out)


def sub(a, b):
    return add(a, neg(b))


def mul(a, b):
    if not a or not b:
        return []
    out = [Fraction(0)] * (len(a) + len(b) - 1)
    for i, ai in enumerate(a):
        if ai:
            for j, bj in enumerate(b):
                if bj:
                    out[i + j] += ai * bj
    return trim(out)


def scale(p, c):
    c = Fraction(c)
    if c == 0 or not p:
        return []
    return trim([c * x for x in p])


def shift(p, k):
    """Multiply by x**k."""
    if not p:
        return []
    return [Fraction(0)] * k + list(p)


def scalar_div(p, c):
    """Exact division by a nonzero rational scalar (Q is a field)."""
    c = Fraction(c)
    if c == 0:
        raise ZeroDivisionError("scalar division by zero")
    return trim([x / c for x in p])


def monic(p):
    """Monic normalization; monic(0) = 0."""
    if not p:
        return []
    return scalar_div(p, p[-1])


def divmod_poly(f, g):
    """Polynomial division with remainder in Q[x].

    Domain note: exact in Q[x] because Q is a field (leading coefficient
    of the divisor is invertible in Q).

    Returns (q, r) with f == q*g + r and deg r < deg g.
    Raises ZeroDivisionError if g is the zero polynomial.
    """
    f = as_poly(f)
    g = as_poly(g)
    if not g:
        raise ZeroDivisionError("polynomial division by the zero polynomial")
    if deg(f) < deg(g):
        return [], f
    q = [Fraction(0)] * (deg(f) - deg(g) + 1)
    r = list(f)
    inv_lc = 1 / lc(g)
    while r and len(r) >= len(g):
        k = len(r) - len(g)
        c = r[-1] * inv_lc
        q[k] = c
        for i, gc in enumerate(g):
            r[i + k] -= c * gc
        trim(r)
    return trim(q), r


def xgcd(f, g):
    """Extended Euclidean algorithm in Q[x].

    Returns (d, s, t, steps) where d is the monic gcd and the Bezout
    identity s*f + t*g == d holds exactly. `steps` logs every
    transformation together with the domain in which it is valid.

    Zero-polynomial boundaries:
      gcd(0, 0) = 0 with s = t = 0;
      gcd(f, 0) = monic(f); gcd(0, g) = monic(g).
    """
    f = as_poly(f)
    g = as_poly(g)
    steps = []
    if not f and not g:
        steps.append({
            "kind": "degenerate",
            "note": "gcd(0, 0) = 0 by convention; Bezout 0*f + 0*g == 0",
            "domain": "Q[x]",
        })
        return [], [], [], steps
    if not g:
        c = lc(f)
        steps.append({
            "kind": "degenerate",
            "note": "g == 0: gcd(f, 0) = monic(f)",
            "domain": "Q[x]; exact scaling by 1/lc(f) in Q (field)",
        })
        return monic(f), [Fraction(1) / c], [], steps
    if not f:
        c = lc(g)
        steps.append({
            "kind": "degenerate",
            "note": "f == 0: gcd(0, g) = monic(g)",
            "domain": "Q[x]; exact scaling by 1/lc(g) in Q (field)",
        })
        return monic(g), [], [Fraction(1) / c], steps

    r0, r1 = f, g
    s0, s1 = [Fraction(1)], []
    t0, t1 = [], [Fraction(1)]
    i = 0
    while r1:
        q, r = divmod_poly(r0, r1)
        steps.append({
            "kind": "euclidean-division",
            "equation": f"r{i} = q*r{i + 1} + r{i + 2}",
            "dividend": r0,
            "divisor": r1,
            "quotient": q,
            "remainder": r,
            "domain": "exact division in Q[x] (Q is a field); deg remainder < deg divisor",
        })
        r0, r1 = r1, r
        s0, s1 = s1, sub(s0, mul(q, s1))
        t0, t1 = t1, sub(t0, mul(q, t1))
        i += 1
    c = lc(r0)
    d = scalar_div(r0, c)
    s = scalar_div(s0, c)
    t = scalar_div(t0, c)
    steps.append({
        "kind": "monic-normalization",
        "note": "scale gcd and Bezout cofactors by 1/lc(gcd)",
        "domain": "exact scalar division in Q (field)",
    })
    return d, s, t, steps
