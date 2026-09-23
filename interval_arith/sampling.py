"""Rational grid sampling -- COUNTEREXAMPLE SEARCH ONLY.

Sampling can falsify a claimed bound (exhibit a point whose image lies
outside it) but can never prove inclusion: a finite grid may simply miss
the violating point.  Proofs come from the certificate's exact interval
re-evaluation (interval_arith.certificate).
"""
from __future__ import annotations

from fractions import Fraction

from .interval import Interval


def rational_grid(domain: Interval, n: int) -> list[Fraction]:
    """n+1 exactly-represented rational sample points on [lo, hi]."""
    if not domain.is_bounded():
        raise ArithmeticError("cannot sample an unbounded domain")
    if n < 1:
        raise ValueError("need at least one grid cell")
    step = (domain.hi - domain.lo) / n
    return [domain.lo + k * step for k in range(n + 1)]


def find_counterexample(f_exact, domain: Interval, claimed: Interval, n: int = 16):
    """Evaluate the exact rational function ``f_exact`` on a rational grid.

    Returns ``(x, f(x))`` for the first sample outside ``claimed``, else
    None.  None means "no counterexample found on this grid" -- NOT a proof
    that the bound holds.
    """
    for x in rational_grid(domain, n):
        y = f_exact(x)
        if not claimed.contains(y):
            return (x, y)
    return None
