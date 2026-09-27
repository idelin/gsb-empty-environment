"""Minimum sample size for a target power, with adjacent-point evidence.

The search is a deterministic linear scan over EXACT powers (never a
bisection over noisy Monte Carlo point estimates).  Because exact power is
not strictly monotone in ``n`` (the discrete critical value changes in
steps), scanning from ``n = 1`` upward is what guarantees minimality.  The
returned evidence pair ``(n_below, power_below)`` / ``(n, power)`` brackets
the target with exact numbers.
"""

from __future__ import annotations

from dataclasses import dataclass

from .exact import exact_power

__all__ = ["MinSampleSize", "min_sample_size"]


@dataclass(frozen=True)
class MinSampleSize:
    """Exact adjacent-point evidence for the minimum sample size."""

    p0: float
    p1: float
    alpha: float
    target_power: float
    n: int                 # minimal n with exact power >= target
    critical: int
    size: float            # exact size at n
    power: float           # exact power at n  (>= target_power)
    n_below: int           # n - 1
    critical_below: int | None
    power_below: float     # exact power at n - 1 (< target_power); 0.0 if n == 1
    kind: str = "exact"


def min_sample_size(
    p0: float,
    p1: float,
    alpha: float,
    target_power: float,
    *,
    n_max: int = 10_000,
) -> MinSampleSize:
    """Smallest ``n`` whose EXACT power reaches ``target_power``.

    Raises ``RuntimeError`` if no ``n <= n_max`` qualifies.
    """
    if not 0.0 < target_power < 1.0:
        raise ValueError("target_power must be in (0, 1)")
    if not 0.0 < alpha < 1.0:
        raise ValueError("alpha must be in (0, 1)")
    if p1 <= p0:
        raise ValueError("p1 must exceed p0 for a power target")
    if n_max < 1:
        raise ValueError("n_max must be >= 1")
    for n in range(1, n_max + 1):
        hit = exact_power(n, p0, p1, alpha)
        if hit.power >= target_power:
            if n > 1:
                below = exact_power(n - 1, p0, p1, alpha)
                critical_below: int | None = below.critical
                power_below = below.power
            else:
                critical_below = None
                power_below = 0.0
            return MinSampleSize(
                p0=p0,
                p1=p1,
                alpha=alpha,
                target_power=target_power,
                n=n,
                critical=hit.critical,
                size=hit.size,
                power=hit.power,
                n_below=n - 1,
                critical_below=critical_below,
                power_below=power_below,
            )
    raise RuntimeError(
        f"no n <= {n_max} reaches target power {target_power} "
        f"(p0={p0}, p1={p1}, alpha={alpha})"
    )
