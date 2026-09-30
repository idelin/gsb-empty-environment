"""Exact arithmetic on univariate polynomials with rational coefficients.

Polynomials are represented as lists of :class:`fractions.Fraction` where
``p[i]`` is the coefficient of ``x**i``.  The zero polynomial is ``[0]``.

Includes Sturm sequences for exact real-root counting, square-free
factorization (Yun's algorithm, giving root multiplicities) and a Cauchy root
bound.  No floating point is used anywhere.
"""
from __future__ import annotations

import math
from fractions import Fraction
from math import gcd as _igcd

INF = math.inf
NINF = -math.inf


def norm(p):
    q = [Fraction(c) for c in p]
    while len(q) > 1 and q[-1] == 0:
        q.pop()
    return q


def is_zero(p):
    return len(p) == 1 and p[0] == 0


def is_one(p):
    return len(p) == 1 and p[0] == 1


def deg(p):
    return len(p) - 1


def lc(p):
    return p[-1]


def add(a, b):
    n = max(len(a), len(b))
    r = [Fraction(0)] * n
    for i in range(n):
        if i < len(a):
            r[i] += a[i]
        if i < len(b):
            r[i] += b[i]
    return norm(r)


def neg(a):
    return [-c for c in a]


def sub(a, b):
    return add(a, neg(b))


def mul(a, b):
    if is_zero(a) or is_zero(b):
        return [Fraction(0)]
    r = [Fraction(0)] * (len(a) + len(b) - 1)
    for i, ai in enumerate(a):
        if ai == 0:
            continue
        for j, bj in enumerate(b):
            if bj:
                r[i + j] += ai * bj
    return norm(r)


def scale(a, s):
    return norm([c * s for c in a])


def divmod_poly(a, b):
    if is_zero(b):
        raise ZeroDivisionError("polynomial division by zero")
    a = list(a)
    if len(a) < len(b):
        return [Fraction(0)], norm(a)
    q = [Fraction(0)] * (len(a) - len(b) + 1)
    bl = b[-1]
    while len(a) >= len(b) and not (len(a) == 1 and a[0] == 0):
        k = len(a) - len(b)
        c = a[-1] / bl
        q[k] = c
        for i in range(len(b)):
            a[i + k] -= c * b[i]
        while len(a) > 1 and a[-1] == 0:
            a.pop()
    return norm(q), norm(a)


def exact_div(a, b):
    q, r = divmod_poly(a, b)
    if not is_zero(r):
        raise ValueError("not an exact polynomial division")
    return q


def monic(p):
    if is_zero(p):
        return [Fraction(0)]
    return scale(p, 1 / p[-1])


def gcd(a, b):
    if is_zero(a) and is_zero(b):
        return [Fraction(0)]
    if is_zero(a):
        return monic(b)
    if is_zero(b):
        return monic(a)
    while not is_zero(b):
        _, r = divmod_poly(a, b)
        a, b = b, r
    return monic(a)


def derivative(p):
    if len(p) <= 1:
        return [Fraction(0)]
    return norm([p[i] * i for i in range(1, len(p))])


def compose(p, q):
    """p(q(x)) by Horner."""
    r = [Fraction(0)]
    for c in reversed(p):
        r = add(mul(r, q), [c])
    return norm(r)


def evaluate(p, x):
    x = Fraction(x)
    r = Fraction(0)
    for c in reversed(p):
        r = r * x + c
    return r


def sign_of_value(v):
    return (v > 0) - (v < 0)


def sign_at(p, x):
    """Sign of p at x, where x is a Fraction, INF, or NINF."""
    if x == INF:
        return sign_of_value(p[-1])
    if x == NINF:
        s = sign_of_value(p[-1])
        return s if deg(p) % 2 == 0 else -s
    return sign_of_value(evaluate(p, x))


def to_primitive_integer(p):
    """Primitive integer polynomial (positive leading coeff), as list[int]."""
    p = norm(p)
    l = 1
    for c in p:
        l = l * c.denominator // _igcd(l, c.denominator)
    ints = [int(c * l) for c in p]
    g = 0
    for v in ints:
        g = _igcd(g, abs(v))
    if g > 1:
        ints = [v // g for v in ints]
    if ints and ints[-1] < 0:
        ints = [-v for v in ints]
    return ints


def squarefree_part(p):
    """Monic square-free part: product of the distinct irreducible factors."""
    if is_zero(p):
        return [Fraction(0)]
    if deg(p) == 0:
        return [Fraction(1)]
    g = gcd(p, derivative(p))
    q = exact_div(p, g) if deg(g) >= 1 else p
    return monic(q)


def squarefree_factorization(p):
    """Yun's algorithm: return [(factor, multiplicity), ...], factors monic.

    ``p == lc(p) * prod(f_i ** m_i)`` and each ``f_i`` is square-free.  Only
    factors of degree >= 1 are listed.
    """
    if is_zero(p):
        return []
    result = []
    g = gcd(p, derivative(p))
    w = exact_div(p, g) if deg(g) >= 1 else monic(p)
    i = 1
    while not is_one(w):
        y = gcd(g, w)
        z = exact_div(w, y)
        if deg(z) >= 1:
            result.append((monic(z), i))
        w = y
        g = exact_div(g, y) if deg(y) >= 1 else [Fraction(1)]
        i += 1
    return result


def sturm_sequence(p, budget=None):
    p = norm(p)
    if budget is not None:
        budget.check_poly(p)
    seq = [p]
    if deg(p) >= 1:
        d = derivative(p)
        if budget is not None:
            budget.check_poly(d)
        seq.append(d)
        while True:
            _, r = divmod_poly(seq[-2], seq[-1])
            if is_zero(r):
                break
            r = neg(r)
            if budget is not None:
                budget.check_poly(r)
            seq.append(r)
    return seq


def sign_variations(seq, x):
    signs = [sign_at(s, x) for s in seq]
    signs = [s for s in signs if s != 0]
    return sum(1 for i in range(1, len(signs)) if signs[i] != signs[i - 1])


def count_roots(p, a, b, seq=None, budget=None):
    """Number of real roots of square-free ``p`` in the open interval (a, b).

    ``a``/``b`` may be Fractions, INF or NINF.  Finite endpoints must not be
    roots of ``p``.
    """
    if is_zero(p):
        raise ValueError("zero polynomial")
    if deg(p) == 0:
        return 0
    if seq is None:
        seq = sturm_sequence(p, budget)
    if a != INF and a != NINF:
        assert evaluate(p, a) != 0, "left endpoint is a root"
    if b != INF and b != NINF:
        assert evaluate(p, b) != 0, "right endpoint is a root"
    return sign_variations(seq, a) - sign_variations(seq, b)


def cauchy_bound(p):
    """Rational B with |root| < B for every real root of p."""
    if deg(p) < 1:
        return Fraction(1)
    l = abs(p[-1])
    m = max((abs(c) / l for c in p[:-1]), default=Fraction(0))
    return Fraction(1) + m


def poly_to_str(p, var="x"):
    p = norm(p)
    if is_zero(p):
        return "0"
    out = ""
    for i in range(len(p) - 1, -1, -1):
        c = p[i]
        if c == 0:
            continue
        if out:
            out += " - " if c < 0 else " + "
        elif c < 0:
            out += "-"
        a = abs(c)
        if i == 0:
            out += str(a)
        elif i == 1:
            out += var if a == 1 else f"{a}*{var}"
        else:
            out += (var if a == 1 else f"{a}*{var}") + f"^{i}"
    return out or "0"
