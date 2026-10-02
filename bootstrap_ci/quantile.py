"""Explicit quantile definitions.

Two definitions are used, each documented at its call site:

* Monte Carlo bootstrap distributions -> ``quantile_type7``
  (Hyndman & Fan type 7, linear interpolation of the empirical CDF).
* Exact enumerated (discrete, weighted) distributions ->
  ``weighted_quantile_inverse_cdf`` (Hyndman & Fan type 1, inverse CDF).
"""
from __future__ import annotations

import math
from fractions import Fraction


def quantile_type7(sorted_values, p: float) -> float:
    """Hyndman & Fan type 7 quantile of an already-sorted sequence.

    With h = (n - 1) * p:
        Q(p) = x[floor(h)] + (h - floor(h)) * (x[ceil(h)] - x[floor(h)])
    """
    if not 0.0 <= p <= 1.0:
        raise ValueError(f"p must be in [0, 1], got {p!r}")
    n = len(sorted_values)
    if n == 0:
        raise ValueError("quantile of an empty sequence")
    if n == 1:
        return float(sorted_values[0])
    h = (n - 1) * p
    lo = math.floor(h)
    hi = math.ceil(h)
    if lo == hi:
        return float(sorted_values[lo])
    frac = h - lo
    lower = float(sorted_values[lo])
    return lower + frac * (float(sorted_values[hi]) - lower)


def weighted_quantile_inverse_cdf(sorted_support, p: float) -> float:
    """Inverse-CDF (Hyndman & Fan type 1) quantile of a discrete distribution.

    ``sorted_support`` is a sorted sequence of ``(value, Fraction weight)``
    pairs whose weights sum to 1.  Returns Q(p) = min { x : F(x) >= p },
    computed with exact rational arithmetic.
    """
    if not 0.0 <= p <= 1.0:
        raise ValueError(f"p must be in [0, 1], got {p!r}")
    if not sorted_support:
        raise ValueError("quantile of an empty support")
    cumulative = Fraction(0)
    for value, weight in sorted_support:
        cumulative += weight
        if cumulative >= p:
            return float(value)
    return float(sorted_support[-1][0])
