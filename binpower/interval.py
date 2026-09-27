"""Clopper-Pearson exact confidence interval for a binomial proportion.

Implemented from scratch on the standard library only: the regularized
incomplete beta function is evaluated by continued fraction and inverted by
bisection.  Zero-trial input is refused; an all-rejection run still yields a
non-degenerate interval (its width never collapses to a point).
"""

from __future__ import annotations

from math import exp, lgamma, log, log1p

__all__ = ["regularized_beta", "clopper_pearson"]

_MAX_ITER = 200
_EPS = 3.0e-14
_FPMIN = 1.0e-300


def _betacf(a: float, b: float, x: float) -> float:
    """Continued-fraction evaluation for the incomplete beta function."""
    qab = a + b
    qap = a + 1.0
    qam = a - 1.0
    c = 1.0
    d = 1.0 - qab * x / qap
    if abs(d) < _FPMIN:
        d = _FPMIN
    d = 1.0 / d
    h = d
    for m in range(1, _MAX_ITER + 1):
        m2 = 2 * m
        aa = m * (b - m) * x / ((qam + m2) * (a + m2))
        d = 1.0 + aa * d
        if abs(d) < _FPMIN:
            d = _FPMIN
        c = 1.0 + aa / c
        if abs(c) < _FPMIN:
            c = _FPMIN
        d = 1.0 / d
        h *= d * c
        aa = -(a + m) * (qab + m) * x / ((a + m2) * (qap + m2))
        d = 1.0 + aa * d
        if abs(d) < _FPMIN:
            d = _FPMIN
        c = 1.0 + aa / c
        if abs(c) < _FPMIN:
            c = _FPMIN
        d = 1.0 / d
        delta = d * c
        h *= delta
        if abs(delta - 1.0) < _EPS:
            break
    return h


def regularized_beta(x: float, a: float, b: float) -> float:
    """Regularized incomplete beta function I_x(a, b)."""
    if a <= 0.0 or b <= 0.0:
        raise ValueError("a and b must be positive")
    if x <= 0.0:
        return 0.0
    if x >= 1.0:
        return 1.0
    bt = exp(lgamma(a + b) - lgamma(a) - lgamma(b) + a * log(x) + b * log1p(-x))
    if x < (a + 1.0) / (a + b + 2.0):
        return bt * _betacf(a, b, x) / a
    return 1.0 - bt * _betacf(b, a, 1.0 - x) / b


def _beta_ppf(prob: float, a: float, b: float) -> float:
    """Inverse of ``regularized_beta`` via bisection (100 iterations)."""
    lo, hi = 0.0, 1.0
    for _ in range(100):
        mid = 0.5 * (lo + hi)
        if regularized_beta(mid, a, b) < prob:
            lo = mid
        else:
            hi = mid
    return 0.5 * (lo + hi)


def clopper_pearson(k: int, m: int, alpha: float = 0.05) -> tuple[float, float]:
    """Exact two-sided Clopper-Pearson interval for ``k`` successes in ``m`` trials.

    Raises ``ValueError`` for ``m < 1``: a zero-trial "estimate" is refused
    rather than silently reported as a degenerate interval.
    """
    if m < 1:
        raise ValueError("m must be >= 1: zero-trial intervals are refused")
    if not 0 <= k <= m:
        raise ValueError("k must satisfy 0 <= k <= m")
    if not 0.0 < alpha < 1.0:
        raise ValueError("alpha must be in (0, 1)")
    if k == 0:
        lo = 0.0
    else:
        lo = _beta_ppf(alpha / 2.0, k, m - k + 1)
    if k == m:
        hi = 1.0
    else:
        hi = _beta_ppf(1.0 - alpha / 2.0, k + 1, m - k)
    return lo, hi
