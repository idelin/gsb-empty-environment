"""Confidence intervals for a binomial proportion (simulation rejection rate).

Clopper-Pearson is the "exact" interval: it inverts exact binomial tail
probabilities from `exact.py`. Crucially, even when every trial rejects
(x == n) the interval still has positive width: the lower bound is
(alpha/2)^(1/n) < 1, so an all-rejection run is never reported as a
point estimate of power = 1.
"""
from __future__ import annotations

from .exact import binom_cdf, binom_sf


def _bisect(lo: float, hi: float, target: float, fn, increasing: bool) -> float:
    for _ in range(80):
        mid = 0.5 * (lo + hi)
        val = fn(mid)
        if (val < target) == increasing:
            lo = mid
        else:
            hi = mid
    return 0.5 * (lo + hi)


def clopper_pearson(x: int, n: int, alpha: float = 0.05) -> tuple[float, float]:
    """Exact (Clopper-Pearson) two-sided 1-alpha CI for a proportion.

    Raises ValueError for n < 1: zero trials is an error, not a CI.
    """
    if n < 1:
        raise ValueError("cannot build a CI from zero trials")
    if not 0 <= x <= n:
        raise ValueError("x must be in [0, n]")
    if not 0.0 < alpha < 1.0:
        raise ValueError("alpha must be in (0, 1)")
    half = alpha / 2.0
    if x == 0:
        lo = 0.0
    else:
        # lower bound: p such that P_p(X >= x) = alpha/2 (increasing in p)
        lo = _bisect(0.0, 1.0, half, lambda p: binom_sf(x, n, p), True)
    if x == n:
        hi = 1.0
    else:
        # upper bound: p such that P_p(X <= x) = alpha/2 (decreasing in p)
        hi = _bisect(0.0, 1.0, half, lambda p: binom_cdf(x, n, p), False)
    return lo, hi


def wilson(x: int, n: int, z: float = 1.959963984540054) -> tuple[float, float]:
    """Wilson score interval (kept as a lightweight cross-check)."""
    if n < 1:
        raise ValueError("cannot build a CI from zero trials")
    phat = x / n
    denom = 1.0 + z * z / n
    centre = (phat + z * z / (2 * n)) / denom
    margin = (z / denom) * ((phat * (1 - phat) + z * z / (4 * n)) / n) ** 0.5
    return max(0.0, centre - margin), min(1.0, centre + margin)
