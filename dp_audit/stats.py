"""Exact binomial confidence bounds using only the standard library.

The auditor never treats a zero count as a true zero probability. These
helpers provide the Clopper-Pearson bounds (and the "rule of three"
one-sided zero-count bound) used throughout the audit.
"""

from __future__ import annotations

import math


def log_binomial_pmf(k: int, n: int, p: float) -> float:
    """Log of P(X = k) for X ~ Binomial(n, p)."""
    if not 0 <= k <= n:
        return -math.inf
    if p <= 0.0:
        return 0.0 if k == 0 else -math.inf
    if p >= 1.0:
        return 0.0 if k == n else -math.inf
    return (
        math.lgamma(n + 1)
        - math.lgamma(k + 1)
        - math.lgamma(n - k + 1)
        + k * math.log(p)
        + (n - k) * math.log1p(-p)
    )


def binomial_cdf(k: int, n: int, p: float) -> float:
    """P(X <= k) for X ~ Binomial(n, p)."""
    if k < 0:
        return 0.0
    if k >= n:
        return 1.0
    return min(1.0, math.fsum(math.exp(log_binomial_pmf(i, n, p)) for i in range(k + 1)))


def binomial_sf(k: int, n: int, p: float) -> float:
    """P(X >= k) for X ~ Binomial(n, p)."""
    if k <= 0:
        return 1.0
    if k > n:
        return 0.0
    return min(
        1.0, math.fsum(math.exp(log_binomial_pmf(i, n, p)) for i in range(k, n + 1))
    )


def _bisect(target: float, increasing: bool, evaluate, iterations: int = 100) -> float:
    """Solve evaluate(p) == target for p in [0, 1] by bisection."""
    lo, hi = 0.0, 1.0
    for _ in range(iterations):
        mid = 0.5 * (lo + hi)
        if (evaluate(mid) < target) == increasing:
            lo = mid
        else:
            hi = mid
    return 0.5 * (lo + hi)


def clopper_pearson(k: int, n: int, alpha: float) -> tuple[float, float]:
    """Two-sided Clopper-Pearson interval at confidence level 1 - alpha.

    Exact (conservative) interval: coverage is guaranteed to be at least
    1 - alpha for any true probability. Edge cases:
      - k == 0 -> (0, 1 - (alpha/2)**(1/n)); the upper bound is NOT 0.
      - k == n -> ((alpha/2)**(1/n), 1).
    """
    if n <= 0:
        raise ValueError("n must be positive")
    if not 0 <= k <= n:
        raise ValueError("k must satisfy 0 <= k <= n")
    if not 0.0 < alpha < 1.0:
        raise ValueError("alpha must be in (0, 1)")
    half = alpha / 2.0
    lower = 0.0 if k == 0 else _bisect(half, True, lambda p: binomial_sf(k, n, p))
    upper = 1.0 if k == n else _bisect(half, False, lambda p: binomial_cdf(k, n, p))
    return lower, upper


def one_sided_zero_upper(n: int, alpha: float) -> float:
    """Exact one-sided upper bound when 0 events are seen in n trials.

    Equals 1 - alpha**(1/n), approximately 3/n for alpha = 0.05 (the
    classic "rule of three"). A zero count must never be reported as a
    true probability of zero; use this bound instead.
    """
    if n <= 0:
        raise ValueError("n must be positive")
    if not 0.0 < alpha < 1.0:
        raise ValueError("alpha must be in (0, 1)")
    return 1.0 - alpha ** (1.0 / n)
