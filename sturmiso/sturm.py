"""Sturm chains and exact counting of distinct real roots over Q.

Counting rule (classical Sturm theorem, endpoint-exact variant).
For a SQUARE-FREE polynomial p and its Sturm chain S = (p_0, ..., p_m):

  * V(x) = number of sign changes in S(x), zeros simply ignored.
  * Crossing a root x0 of p from left to right decreases V by exactly 1
    (p is square-free, so p'(x0) != 0 and p_0, p_1 have opposite signs on
    the left of x0).  Zeros of interior chain members never change V.
  * If x0 is an exact root of p, dropping the zero entries of S(x0) yields
    the RIGHT-side variation V(x0+); the LEFT-side variation is
    V(x0-) = V(x0+) + 1.  No epsilon is ever guessed: the one-sided values
    are computed exactly from the dropped sign vector.
  * The count of distinct real roots in an interval is
        [a, b] : V(a-) - V(b+)
        (a, b] : V(a+) - V(b+)
        [a, b) : V(a-) - V(b-)
        (a, b) : V(a+) - V(b-)
    so a root sitting exactly on a closed endpoint is counted exactly once
    and a root on an open endpoint is not counted.
  * Unbounded endpoints use the leading-term sign at -inf / +inf, where no
    chain member vanishes.

Domain of applicability: p must be square-free over Q (guaranteed here by
running the chain on the square-free factors / radical produced by
squarefree_factorization).  All arithmetic is exact rational arithmetic.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from .poly import Poly, degree, derivative, divmod_poly, evaluate, is_zero, lc, neg


def sturm_chain(p: Poly) -> list:
    """Standard Sturm chain of a square-free polynomial p over Q.

    p_0 = p, p_1 = p', p_{i+1} = -rem(p_{i-1}, p_i).  For square-free p the
    chain ends with a nonzero constant.  Domain: deg(p) >= 1 and p
    square-free (gcd(p, p') = 1).
    """
    if degree(p) < 1:
        raise ValueError("Sturm chain needs a nonconstant polynomial")
    chain = [p, derivative(p)]
    while degree(chain[-1]) >= 1:
        _, r = divmod_poly(chain[-2], chain[-1])
        if is_zero(r):
            raise ValueError("polynomial is not square-free: Sturm chain "
                             "terminated with a zero remainder of degree >= 0")
        chain.append(neg(r))
    return chain


def _variations(signs) -> int:
    """Sign changes with zeros ignored (skipped, not counted)."""
    count = 0
    prev = 0
    for s in signs:
        if s == 0:
            continue
        if prev and s != prev:
            count += 1
        prev = s
    return count


@dataclass(frozen=True)
class EndpointSign:
    """Sign vector of a Sturm chain at one interval endpoint.

    side: "exact"  (endpoint is not a root of p; signs used as-is),
          "left" / "right" (endpoint IS an exact root of p; the zero
          entries were dropped, giving V(x+) for "right" and V(x+) + 1
          for "left", since crossing a root of a square-free p decreases
          the variation by exactly one).
    """

    point: str  # decimal/fraction string of the endpoint, "-inf" or "+inf"
    side: str
    signs: tuple
    variation: int
    zeros_dropped: tuple = field(default_factory=tuple)


def endpoint_sign(chain: list, bound, side: str = "exact") -> EndpointSign:
    """Signs of the Sturm chain at an endpoint.

    bound: None means -inf; the string "+inf" or the sentinel value
    sturm.POS_INF means +inf; otherwise an exact Fraction.
    side:  "exact"  -> evaluate as-is (endpoint must not be a root of p_0);
           "left"/"right" -> endpoint may be an exact root of p_0; zero
           entries are dropped to obtain the one-sided variation.
    """
    if bound is None or bound == "-inf":
        signs = tuple(
            (1 if lc(p) > 0 else -1) * (1 if degree(p) % 2 == 0 else -1)
            for p in chain
        )
        return EndpointSign("-inf", "exact", signs, _variations(signs))
    if bound == "+inf":
        signs = tuple(1 if lc(p) > 0 else -1 for p in chain)
        return EndpointSign("+inf", "exact", signs, _variations(signs))
    x = Fraction(bound)
    values = [evaluate(p, x) for p in chain]
    if side == "exact":
        signs = tuple(1 if v > 0 else (-1 if v < 0 else 0) for v in values)
        return EndpointSign(str(x), "exact", signs, _variations(signs))
    if side not in ("left", "right"):
        raise ValueError("side must be 'exact', 'left' or 'right'")
    if values[0] != 0:
        raise ValueError(
            "one-sided endpoint sign requested but the endpoint is not a "
            "root of the polynomial; use side='exact'"
        )
    signs = []
    dropped = []
    for i, v in enumerate(values):
        if v == 0:
            dropped.append(i)
        else:
            signs.append(1 if v > 0 else -1)
    variation = _variations(signs)
    if side == "left":
        # p_0 contributes exactly one extra sign change on the left of a
        # root of a square-free polynomial (p_0 and p_1 = p' have opposite
        # signs there), so V(x-) = V(x+) + 1.
        variation += 1
    return EndpointSign(
        str(x), side, tuple(signs), variation, tuple(dropped)
    )


def count_distinct_roots(
    chain: list,
    lower,
    upper,
    lower_closed: bool = False,
    upper_closed: bool = False,
) -> int:
    """Number of distinct real roots of the (square-free) chain polynomial in
    the interval (lower, upper) with the given endpoint inclusions.

    lower/upper: None = -inf, "+inf" = +inf, otherwise exact Fraction.
    Endpoint roots are counted exactly via the one-sided rule documented in
    the module docstring, so a root sitting exactly on a closed endpoint is
    counted, and on an open endpoint is not.
    """
    # [a, b]: V(a-) - V(b+);  (a, b]: V(a+) - V(b+);
    # [a, b): V(a-) - V(b-);  (a, b): V(a+) - V(b-).
    lower_side = "left" if lower_closed else "right"
    upper_side = "right" if upper_closed else "left"

    def _side(bound, closed_side):
        if bound is None or bound == "+inf":
            return endpoint_sign(chain, bound)
        if evaluate(chain[0], Fraction(bound)) == 0:
            return endpoint_sign(chain, bound, closed_side)
        return endpoint_sign(chain, bound, "exact")

    v_lower = _side(lower, lower_side)
    v_upper = _side(upper, upper_side)
    return v_lower.variation - v_upper.variation
