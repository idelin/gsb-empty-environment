"""Sturm sequences, exact sign-variation counting, and root bounds over Q.

Applicability domain
--------------------
* ``sturm_sequence(q)`` requires q square-free over Q (characteristic 0),
  so that gcd(q, q') = 1 and the sequence ends in a nonzero constant.
  Then Sturm's theorem applies: for any rationals a < b, the number of
  distinct real roots of q in the half-open interval (a, b] equals
  V(a) - V(b), where V(x) counts sign variations of the sequence at x
  with zeros dropped. The formula holds even when a or b is a root of q:
  a root at the left endpoint is excluded, a root at the right endpoint
  is included. This is the consistent endpoint counting rule used
  throughout the package.
* ``cauchy_bound(q)`` is valid for any nonzero complex polynomial.
"""

from __future__ import annotations

from fractions import Fraction
from typing import Tuple

from .polynomials import (Poly, degree, derivative, divmod_poly, evaluate,
                          is_zero, neg)


def sturm_sequence(q: Poly) -> Tuple[Poly, ...]:
    """Standard negative-remainder Sturm sequence of a square-free q."""
    seq = [q]
    d = derivative(q)
    if not is_zero(d):
        seq.append(d)
    while degree(seq[-1]) > 0:
        _, r = divmod_poly(seq[-2], seq[-1])
        r = neg(r)
        if is_zero(r):
            break
        seq.append(r)
    return tuple(seq)


def sign_variations(seq, x) -> Tuple[int, Tuple[int, ...]]:
    """Return (number of sign variations, nonzero sign tuple) at x."""
    x = Fraction(x)
    signs = []
    for f in seq:
        v = evaluate(f, x)
        if v > 0:
            signs.append(1)
        elif v < 0:
            signs.append(-1)
    changes = 0
    for a, b in zip(signs, signs[1:]):
        if a != b:
            changes += 1
    return changes, tuple(signs)


def cauchy_bound(q: Poly) -> Fraction:
    """Strict rational bound B: every real root r satisfies |r| < B.

    Domain: any nonzero polynomial (Cauchy's bound over C). The endpoints
    +-B are checked exactly and nudged upward if they happen to be roots.
    """
    lc = q[-1]
    peak = Fraction(0)
    for c in q[:-1]:
        if abs(c) > peak:
            peak = abs(c)
    bound = 1 + peak / abs(lc)
    while evaluate(q, bound) == 0 or evaluate(q, -bound) == 0:
        bound += 1
    return bound
