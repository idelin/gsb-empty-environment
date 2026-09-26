"""Exact rational arithmetic helpers.

The discrete mechanism is defined with *exact rational* probabilities so that
no output on the finite domain ever receives probability zero because of a
floating-point tail.  The only transcendental quantity we need is the base
``r = exp(-epsilon / sensitivity)`` of the (truncated) discrete Laplace
distribution.  To keep everything exact we do not evaluate ``exp`` at all:
we compute a *rational upper bound* ``r >= exp(-x)`` and define the mechanism
with that rational ``r``.  Because ``r >= exp(-epsilon/sensitivity)`` implies
``r**-sensitivity <= exp(epsilon)``, the epsilon-DP guarantee proved against
the rational ``r`` is at least as strong as the target ``epsilon``.
"""

from __future__ import annotations

from fractions import Fraction

DEFAULT_EXP_TOL = Fraction(1, 10**24)


def exp_neg_upper_bound(x: Fraction, tol: Fraction = DEFAULT_EXP_TOL) -> Fraction:
    """Return a rational ``r`` with ``exp(-x) <= r <= exp(-x) + tol``.

    Uses the Taylor partial sum ``S_n(x) = sum_{k=0}^{n} x^k / k!``.  All terms
    are positive, so ``S_n(x) <= exp(x)`` for every ``n`` and therefore
    ``1 / S_n(x) >= exp(-x)``: the result is a *provable* upper bound, not an
    approximation that might fall below the true value.  The summation stops
    once the exact geometric tail bound drops below ``tol``.

    Raises:
        ValueError: if ``x`` is negative or ``tol`` is not positive.
    """
    x = Fraction(x)
    tol = Fraction(tol)
    if x < 0:
        raise ValueError("x must be non-negative")
    if tol <= 0:
        raise ValueError("tol must be positive")

    term = Fraction(1)  # x^k / k!
    partial = Fraction(1)  # running sum S_k
    k = 0
    while True:
        k += 1
        term *= x / k
        partial += term
        if x < k + 1:
            # Remaining terms shrink by at least the factor ratio = x/(k+1)
            # each step, so the tail is bounded by a geometric series.
            ratio = x / (k + 1)
            tail_bound = term * ratio / (1 - ratio)
            if tail_bound <= tol:
                return 1 / partial
