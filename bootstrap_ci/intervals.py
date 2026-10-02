"""Confidence interval constructors.

Percentile and BCa are deliberately separate implementations; BCa fails
explicitly whenever the jackknife is degenerate or the bias-correction /
acceleration constants are not estimable.
"""
from __future__ import annotations

from statistics import NormalDist

from .errors import BcaNotEstimableError, DegenerateJackknifeError
from .quantile import quantile_type7

_NORM = NormalDist()


def percentile_interval(sorted_boot, alpha: float):
    """Percentile interval: type-7 quantiles at alpha/2 and 1 - alpha/2."""
    if not 0.0 < alpha < 1.0:
        raise ValueError(f"alpha must be in (0, 1), got {alpha!r}")
    return (
        quantile_type7(sorted_boot, alpha / 2.0),
        quantile_type7(sorted_boot, 1.0 - alpha / 2.0),
    )


def jackknife_acceleration(jackknife_stats) -> float:
    """BCa acceleration constant a from delete-one jackknife replicates.

    a = sum((mean - j_i)^3) / (6 * (sum((mean - j_i)^2))^1.5)

    Raises DegenerateJackknifeError when the denominator vanishes (all
    jackknife replicates identical) — the acceleration is then not estimable.
    """
    n = len(jackknife_stats)
    if n < 2:
        raise DegenerateJackknifeError(
            f"need at least 2 jackknife replicates, got {n}")
    mean = sum(jackknife_stats) / n
    diffs = [mean - j for j in jackknife_stats]
    sum_sq = sum(d * d for d in diffs)
    if sum_sq == 0.0:
        raise DegenerateJackknifeError(
            "all jackknife replicates are identical; "
            "the BCa acceleration constant is not estimable")
    return sum(d ** 3 for d in diffs) / (6.0 * sum_sq ** 1.5)


def bca_interval(sorted_boot, observed: float, jackknife_stats, alpha: float):
    """BCa interval (Efron & Tibshirani, 1993), separate from percentile.

    Raises BcaNotEstimableError if z0 is not finite (no replicate on one side
    of the observed statistic) or the adjusted level is not computable, and
    DegenerateJackknifeError if the acceleration is not estimable.
    """
    if not 0.0 < alpha < 1.0:
        raise ValueError(f"alpha must be in (0, 1), got {alpha!r}")
    accel = jackknife_acceleration(jackknife_stats)
    n_boot = len(sorted_boot)
    n_less = sum(1 for b in sorted_boot if b < observed)
    prop = n_less / n_boot
    if prop <= 0.0 or prop >= 1.0:
        raise BcaNotEstimableError(
            f"bias-correction z0 is not finite: {n_less}/{n_boot} bootstrap "
            "replicates fall strictly below the observed statistic")
    z0 = _NORM.inv_cdf(prop)

    def adjusted_level(p: float) -> float:
        z = _NORM.inv_cdf(p)
        denom = 1.0 - accel * (z0 + z)
        if denom == 0.0:
            raise BcaNotEstimableError(
                "BCa adjusted level is not computable "
                f"(denominator 1 - a*(z0+z) == 0 at p={p})")
        return _NORM.cdf(z0 + (z0 + z) / denom)

    return (
        quantile_type7(sorted_boot, adjusted_level(alpha / 2.0)),
        quantile_type7(sorted_boot, adjusted_level(1.0 - alpha / 2.0)),
    )
