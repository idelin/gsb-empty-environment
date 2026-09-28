"""Exact binomial probabilities via PMF recursion (stdlib only).

All quantities here are *exact* (up to floating point rounding): they are
computed from the binomial PMF recurrence, not from simulation.
"""
from __future__ import annotations

import math


def _check_n_p(n: int, p: float) -> None:
    if n < 0:
        raise ValueError("n must be >= 0")
    if not 0.0 <= p <= 1.0:
        raise ValueError("p must be in [0, 1]")


def binom_pmf_all(n: int, p: float) -> list[float]:
    """Return [P(X = k) for k in 0..n] for X ~ Bin(n, p).

    Mode-anchored two-way recursion, normalised at the end, so it stays
    numerically stable even when the endpoint probabilities underflow.
    """
    _check_n_p(n, p)
    if n == 0:
        return [1.0]
    if p == 0.0:
        return [1.0] + [0.0] * n
    if p == 1.0:
        return [0.0] * n + [1.0]
    pmf = [0.0] * (n + 1)
    mode = min(int((n + 1) * p), n)
    pmf[mode] = 1.0
    for k in range(mode, 0, -1):
        pmf[k - 1] = pmf[k] * (k / (n - k + 1)) * ((1.0 - p) / p)
    for k in range(mode, n):
        pmf[k + 1] = pmf[k] * ((n - k) / (k + 1)) * (p / (1.0 - p))
    total = math.fsum(pmf)
    return [x / total for x in pmf]


def binom_sf(k: int, n: int, p: float) -> float:
    """Exact P(X >= k) for X ~ Bin(n, p)."""
    _check_n_p(n, p)
    if k <= 0:
        return 1.0
    if k > n:
        return 0.0
    return math.fsum(binom_pmf_all(n, p)[k:])


def binom_cdf(k: int, n: int, p: float) -> float:
    """Exact P(X <= k) for X ~ Bin(n, p)."""
    _check_n_p(n, p)
    if k < 0:
        return 0.0
    if k >= n:
        return 1.0
    return math.fsum(binom_pmf_all(n, p)[: k + 1])


def critical_value(n: int, p0: float, alpha: float) -> tuple[int, float]:
    """Smallest rejection threshold c with P_{p0}(X >= c) <= alpha.

    Returns (c, actual_size). The test rule "reject iff X >= c" is fixed
    once computed; it does not depend on any simulated data.
    """
    if n < 1:
        raise ValueError("n must be >= 1")
    if not 0.0 < alpha < 1.0:
        raise ValueError("alpha must be in (0, 1)")
    pmf = binom_pmf_all(n, p0)
    suffix = 0.0
    for c in range(n, -1, -1):
        suffix += pmf[c]
        if suffix > alpha:
            return c + 1, suffix - pmf[c]
    return 0, 1.0  # only reachable when alpha >= 1


def exact_power(n: int, p0: float, p1: float, alpha: float) -> tuple[int, float, float]:
    """Exact power of the fixed level-alpha test at the alternative p1.

    Returns (critical_value, actual_size, exact_power). This is the exact
    reference against which any simulation estimate must be compared.
    """
    c, size = critical_value(n, p0, alpha)
    return c, size, binom_sf(c, n, p1)
