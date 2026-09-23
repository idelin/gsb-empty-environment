"""Exact univariate polynomials over Q (rational coefficients).

Representation: a polynomial is a tuple of ``fractions.Fraction`` coefficients
in ascending degree order with no trailing zeros.  The zero polynomial is the
empty tuple.  Only the Python standard library is used; no symbolic algebra
packages are involved anywhere.
"""
from __future__ import annotations

from fractions import Fraction
from math import gcd
from typing import Iterable, Union

Number = Union[int, Fraction]
Poly = tuple  # tuple[Fraction, ...], ascending degree, no trailing zeros

ZERO: Poly = ()
ONE: Poly = (Fraction(1),)


def as_fraction(value) -> Fraction:
    """Convert an exact rational-like value to Fraction; reject floats."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        return Fraction(int(value))
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        raise TypeError("float coefficients are not exact; use Fraction or int")
    return Fraction(value)


def normalize(coeffs: Iterable) -> Poly:
    """Build a canonical polynomial tuple from any iterable of coefficients."""
    out = tuple(as_fraction(c) for c in coeffs)
    last = -1
    for i, c in enumerate(out):
        if c != 0:
            last = i
    return out[: last + 1]


def degree(p: Poly) -> int:
    """Degree of p; the zero polynomial has degree -1."""
    return len(p) - 1


def is_zero(p: Poly) -> bool:
    return len(p) == 0


def lc(p: Poly) -> Fraction:
    """Leading coefficient; undefined for the zero polynomial."""
    if not p:
        raise ValueError("the zero polynomial has no leading coefficient")
    return p[-1]


def neg(p: Poly) -> Poly:
    return tuple(-c for c in p)


def add(p: Poly, q: Poly) -> Poly:
    n = max(len(p), len(q))
    out = []
    for i in range(n):
        a = p[i] if i < len(p) else Fraction(0)
        b = q[i] if i < len(q) else Fraction(0)
        out.append(a + b)
    return normalize(out)


def sub(p: Poly, q: Poly) -> Poly:
    return add(p, neg(q))


def scale(p: Poly, c) -> Poly:
    c = as_fraction(c)
    if c == 0:
        return ZERO
    return tuple(c * a for a in p)


def mul(p: Poly, q: Poly) -> Poly:
    if is_zero(p) or is_zero(q):
        return ZERO
    out = [Fraction(0)] * (len(p) + len(q) - 1)
    for i, a in enumerate(p):
        if a == 0:
            continue
        for j, b in enumerate(q):
            if b != 0:
                out[i + j] += a * b
    return normalize(out)


def power(p: Poly, k: int) -> Poly:
    if k < 0:
        raise ValueError("negative exponent")
    result = ONE
    base = p
    while k:
        if k & 1:
            result = mul(result, base)
        base = mul(base, base)
        k >>= 1
    return result


def derivative(p: Poly) -> Poly:
    """Formal derivative; valid for any p in Q[x] (characteristic 0)."""
    return tuple(Fraction(i) * p[i] for i in range(1, len(p)))


def evaluate(p: Poly, x) -> Fraction:
    """Horner evaluation at an exact rational point."""
    x = as_fraction(x)
    acc = Fraction(0)
    for c in reversed(p):
        acc = acc * x + c
    return acc


def divmod_poly(p: Poly, q: Poly) -> tuple:
    """Polynomial division with remainder over Q: p = quo*q + rem."""
    if is_zero(q):
        raise ZeroDivisionError("polynomial division by zero")
    rem = list(p)
    quo = [Fraction(0)] * max(0, len(p) - len(q) + 1)
    dq = degree(q)
    lq = lc(q)
    while rem and len(rem) - 1 >= dq:
        k = len(rem) - 1 - dq
        c = rem[-1] / lq
        quo[k] = c
        for i in range(dq + 1):
            rem[k + i] -= c * q[i]
        while rem and rem[-1] == 0:
            rem.pop()
    return normalize(quo), normalize(rem)


def div_exact(p: Poly, q: Poly) -> Poly:
    """Exact division; raises if the remainder is nonzero."""
    quo, rem = divmod_poly(p, q)
    if not is_zero(rem):
        raise ValueError("polynomial division is not exact")
    return quo


def gcd_poly(a: Poly, b: Poly) -> Poly:
    """Monic greatest common divisor over Q (Euclidean algorithm)."""
    a, b = normalize(a), normalize(b)
    while not is_zero(b):
        _, r = divmod_poly(a, b)
        a, b = b, r
    if is_zero(a):
        return ZERO
    return scale(a, 1 / lc(a))


def primitive_integer(p: Poly) -> tuple:
    """Primitive integer-coefficient form: clear denominators, divide by the
    content, and make the leading coefficient positive.  Returns a tuple of
    ints in ascending degree order (empty for the zero polynomial)."""
    if is_zero(p):
        return ()
    den = 1
    for c in p:
        den = den * c.denominator // gcd(den, c.denominator)
    ints = [int(c * den) for c in p]
    cont = 0
    for v in ints:
        cont = gcd(cont, abs(v))
    ints = [v // cont for v in ints]
    if ints[-1] < 0:
        ints = [-v for v in ints]
    return tuple(ints)


def to_string(p: Poly, var: str = "x") -> str:
    """Human-readable form, e.g. ``x^2 - 2`` or ``3/2*x - 1``."""
    if is_zero(p):
        return "0"
    terms = []
    for i in range(len(p) - 1, -1, -1):
        c = p[i]
        if c == 0:
            continue
        sign = "-" if c < 0 else "+"
        mag = abs(c)
        if i == 0:
            body = str(mag)
        elif i == 1:
            body = var if mag == 1 else f"{mag}*{var}"
        else:
            body = f"{var}^{i}" if mag == 1 else f"{mag}*{var}^{i}"
        terms.append((sign, body))
    first_sign, first_body = terms[0]
    out = ("-" if first_sign == "-" else "") + first_body
    for sign, body in terms[1:]:
        out += f" {sign} {body}"
    return out
