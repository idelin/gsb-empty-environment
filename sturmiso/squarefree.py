"""Square-free factorization over Q (Yun's algorithm, exact rational arithmetic).

Domain of applicability: any nonconstant f in Q[x]; Q has characteristic 0,
so gcd(f, f') < f always holds for nonconstant f and Yun's recursion
terminates with f = c * prod_k f_k^k where each f_k is square-free and the
f_k are pairwise coprime.  Repeated roots of f are exactly the roots of the
f_k with k >= 2; the multiplicity k is recorded separately from the root
itself (e.g. x^2 has the single distinct root 0 with multiplicity 2).
"""
from __future__ import annotations

from dataclasses import dataclass

from .poly import (
    Poly,
    degree,
    derivative,
    div_exact,
    gcd_poly,
    is_zero,
    lc,
    mul,
    normalize,
    primitive_integer,
    scale,
    ONE,
)


@dataclass(frozen=True)
class SquareFreeFactor:
    """One square-free factor f_k: roots of f of multiplicity exactly k."""

    index: int  # == multiplicity in f
    multiplicity: int
    poly: Poly  # monic square-free polynomial over Q
    primitive: tuple  # primitive integer form (positive leading coefficient)


def squarefree_factorization(f: Poly) -> list:
    """Return [SquareFreeFactor, ...] with f = c * prod f_k^k over Q.

    Yun's algorithm:
        c_1 = gcd(f, f'),  w_1 = f / c_1
        loop k:  y_k = gcd(w_k, c_k);  f_k = w_k / y_k
                 w_{k+1} = y_k;       c_{k+1} = c_k / y_k
    Valid because over Q (char 0) a root of multiplicity m in f has
    multiplicity m-1 in f', so gcd(f, f') strips exactly one copy of each
    repeated factor.
    """
    f = normalize(f)
    if degree(f) < 1:
        return []
    c = gcd_poly(f, derivative(f))
    w = div_exact(f, c)
    factors = []
    k = 1
    while not is_zero(w) and degree(w) >= 0 and w != ONE:
        y = gcd_poly(w, c)
        z = div_exact(w, y)
        if degree(z) >= 1:
            z = scale(z, 1 / lc(z))  # monic normal form
            factors.append(
                SquareFreeFactor(
                    index=k,
                    multiplicity=k,
                    poly=z,
                    primitive=primitive_integer(z),
                )
            )
        w = y
        c = div_exact(c, y)
        k += 1
    return factors


def radical(factors) -> Poly:
    """Product of the distinct square-free factors (monic): the square-free
    part of f, whose roots are exactly the distinct real roots of f."""
    out = ONE
    for fac in factors:
        out = mul(out, fac.poly)
    return out
