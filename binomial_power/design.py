"""Minimum sample size for a target power, with adjacent-point evidence.

The inversion is done on the *exact* power function (deterministic
binomial tail recursion), never on noisy simulation point estimates:
binary-searching Monte Carlo estimates would mistake RNG noise for
signal. Because exact power is not strictly monotone in n (the discrete
critical value jumps), the search brackets exponentially and then scans
linearly, and the result is always reported together with the two
adjacent grid points (n* - 1, n*) so the claim "minimal" is checkable.
"""
from __future__ import annotations

from dataclasses import dataclass

from .exact import exact_power


@dataclass(frozen=True)
class MinSampleSizeResult:
    n_min: int
    power_at_n_min: float          # EXACT power at the selected n
    n_below: int                   # adjacent point n_min - 1
    power_below: float             # EXACT power at n_min - 1 (< target)
    target_power: float
    critical: int
    actual_size: float
    evidence: str                  # human-readable two-point evidence


def min_sample_size(
    p0: float,
    p1: float,
    alpha: float = 0.05,
    target_power: float = 0.8,
    n_max: int = 100_000,
) -> MinSampleSizeResult:
    """Smallest n whose exact power reaches target_power.

    Raises ValueError if even n_max does not reach the target.
    """
    if not 0.0 < target_power < 1.0:
        raise ValueError("target_power must be in (0, 1)")
    if not p0 < p1:
        raise ValueError("need p1 > p0 for a one-sided upper test")

    def power_at(n: int) -> float:
        return exact_power(n, p0, p1, alpha)[2]

    # Exponential bracketing on the deterministic exact power curve.
    lo, hi = 1, 2
    while power_at(hi) < target_power:
        lo = hi
        hi *= 2
        if hi > n_max:
            if power_at(n_max) < target_power:
                raise ValueError(
                    f"target power {target_power} not reached by n_max={n_max}"
                )
            hi = n_max
            break

    # Linear scan inside the bracket: exact power is not strictly
    # monotone in n, so bisection alone could miss the true minimum.
    n_min = hi
    for n in range(lo, hi + 1):
        if power_at(n) >= target_power:
            n_min = n
            break

    critical, size, power_here = exact_power(n_min, p0, p1, alpha)
    power_below = power_at(n_min - 1) if n_min > 1 else 0.0
    evidence = (
        f"EXACT power({n_min - 1}) = {power_below:.6f} < target "
        f"{target_power} <= EXACT power({n_min}) = {power_here:.6f}"
    )
    return MinSampleSizeResult(
        n_min=n_min,
        power_at_n_min=power_here,
        n_below=n_min - 1,
        power_below=power_below,
        target_power=target_power,
        critical=critical,
        actual_size=size,
        evidence=evidence,
    )
