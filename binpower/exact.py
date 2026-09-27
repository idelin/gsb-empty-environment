"""Exact binomial calculations via pmf/tail-probability recursion.

Everything in this module is EXACT (up to floating-point rounding): it is
computed from the binomial pmf recurrence, never from simulation.  Results
carry ``kind="exact"`` so they cannot be confused with empirical estimates.
"""

from __future__ import annotations

from dataclasses import dataclass
from math import exp, lgamma, log, log1p

__all__ = [
    "binomial_pmf_table",
    "binomial_cdf_table",
    "binomial_tail",
    "critical_value",
    "exact_power",
    "ExactPower",
]


def binomial_pmf_table(n: int, p: float) -> list[float]:
    """Return ``[P(X = k) for k in 0..n]`` for ``X ~ Bin(n, p)``.

    The table is anchored at the mode (whose probability never underflows)
    and extended in both directions with the pmf recurrence, so it stays
    accurate even for large ``n``.
    """
    if n < 0:
        raise ValueError("n must be >= 0")
    if not 0.0 <= p <= 1.0:
        raise ValueError("p must be in [0, 1]")
    if p == 0.0:
        return [1.0] + [0.0] * n
    if p == 1.0:
        return [0.0] * n + [1.0]
    mode = min(int((n + 1) * p), n)
    table = [0.0] * (n + 1)
    log_pmf_mode = (
        lgamma(n + 1)
        - lgamma(mode + 1)
        - lgamma(n - mode + 1)
        + mode * log(p)
        + (n - mode) * log1p(-p)
    )
    table[mode] = exp(log_pmf_mode)
    for k in range(mode, 0, -1):
        table[k - 1] = table[k] * k / (n - k + 1) * (1.0 - p) / p
    for k in range(mode, n):
        table[k + 1] = table[k] * (n - k) / (k + 1) * p / (1.0 - p)
    return table


def binomial_cdf_table(n: int, p: float) -> list[float]:
    """Return ``[P(X <= k) for k in 0..n]``; the last entry is exactly 1.0."""
    cdf: list[float] = []
    running = 0.0
    for value in binomial_pmf_table(n, p):
        running += value
        cdf.append(min(running, 1.0))
    cdf[-1] = 1.0
    return cdf


def binomial_tail(n: int, p: float, c: int) -> float:
    """Exact ``P(X >= c)`` for ``X ~ Bin(n, p)`` by tail-probability recursion."""
    if c <= 0:
        return 1.0
    if c > n:
        return 0.0
    return min(sum(binomial_pmf_table(n, p)[c:]), 1.0)


def critical_value(n: int, p0: float, alpha: float) -> int:
    """Smallest rejection threshold ``c`` with ``P_{p0}(X >= c) <= alpha``.

    The test rule is fixed once declared: reject ``H0: p <= p0`` iff the
    observed count ``X >= c``.  ``c = n + 1`` means the test never rejects
    (size 0, which trivially respects ``alpha``).
    """
    if n < 1:
        raise ValueError("n must be >= 1")
    if not 0.0 < alpha < 1.0:
        raise ValueError("alpha must be in (0, 1)")
    pmf = binomial_pmf_table(n, p0)
    tail = 0.0
    for c in range(n, -1, -1):
        tail += pmf[c]
        if c == 0:
            # tail(0) == 1 > alpha mathematically; guard float rounding.
            return 1
        if tail > alpha:
            return c + 1
    raise AssertionError("unreachable")


@dataclass(frozen=True)
class ExactPower:
    """Exact operating characteristics of the fixed rule ``reject iff X >= c``."""

    n: int
    critical: int
    p0: float
    p1: float
    alpha: float
    size: float   # exact P_{p0}(reject)
    power: float  # exact P_{p1}(reject)
    kind: str = "exact"


def exact_power(n: int, p0: float, p1: float, alpha: float) -> ExactPower:
    """Exact size and power of the fixed binomial test at alternative ``p1``."""
    c = critical_value(n, p0, alpha)
    return ExactPower(
        n=n,
        critical=c,
        p0=p0,
        p1=p1,
        alpha=alpha,
        size=binomial_tail(n, p0, c),
        power=binomial_tail(n, p1, c),
    )
