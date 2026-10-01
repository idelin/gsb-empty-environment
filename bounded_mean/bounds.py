"""Confidence bounds for means of bounded random variables.

Two families of bounds are provided:

* :func:`hoeffding_radius` -- the classical fixed-sample Hoeffding bound.
  It is valid only at a single, pre-fixed sample size.  Re-using it while
  "peeking" at accumulating data invalidates the guarantee.

* :func:`cs_radius` -- a time-uniform confidence sequence obtained by
  geometric (dyadic) stitching: a union bound over epochs
  ``[2**k, 2**(k+1))`` with error budget ``delta * 6 / (pi^2 (k+1)^2)``.
  The radii are valid simultaneously for all ``n >= 1``, so adaptive
  stopping and adaptive choice of which variable to sample next are safe.

All bounds are distribution-free.  When several variables are bounded
simultaneously, a plain union bound over per-variable failure events is
used, which requires *no* independence assumption: arbitrarily correlated
or shared samples are handled correctly.
"""
from __future__ import annotations

import math

__all__ = [
    "validate_delta",
    "validate_epsilon",
    "validate_width",
    "hoeffding_radius",
    "cs_radius",
    "stage_delta",
    "bernoulli_endpoint_radius",
]

#: sum_{j>=1} 1/j^2 -- used for zeta-2 error allocation across stages/epochs.
ZETA_2 = math.pi**2 / 6.0


def validate_delta(delta: float) -> float:
    """Confidence parameter must lie in the open interval (0, 1)."""
    if isinstance(delta, bool) or not isinstance(delta, (int, float)):
        raise TypeError(f"delta must be a real number, got {type(delta).__name__}")
    if not math.isfinite(delta) or not 0.0 < delta < 1.0:
        raise ValueError(f"delta must lie in the open interval (0, 1), got {delta!r}")
    return float(delta)


def validate_epsilon(epsilon: float) -> float:
    if isinstance(epsilon, bool) or not isinstance(epsilon, (int, float)):
        raise TypeError(f"epsilon must be a real number, got {type(epsilon).__name__}")
    if not math.isfinite(epsilon) or epsilon <= 0.0:
        raise ValueError(f"epsilon must be positive, got {epsilon!r}")
    return float(epsilon)


def validate_width(low: float, high: float) -> float:
    if not (math.isfinite(low) and math.isfinite(high)):
        raise ValueError("bounds must be finite")
    if low > high:
        raise ValueError(f"low ({low!r}) must not exceed high ({high!r})")
    return float(high) - float(low)


def _validate_n(n: int) -> int:
    if isinstance(n, bool) or not isinstance(n, int) or n < 1:
        raise ValueError(f"n must be a positive integer, got {n!r}")
    return n


def hoeffding_radius(n: int, width: float, delta: float) -> float:
    """Fixed-sample Hoeffding radius for a variable in an interval of ``width``.

    P(|sample_mean - mean| > radius) <= delta, valid only at the single
    pre-fixed sample size ``n``.  Do NOT reuse at multiple sample sizes.
    """
    _validate_n(n)
    validate_delta(delta)
    if width < 0.0:
        raise ValueError("width must be non-negative")
    if width == 0.0:
        return 0.0
    return width * math.sqrt(math.log(2.0 / delta) / (2.0 * n))


def stage_delta(delta: float, stage: int) -> float:
    """Error budget for 1-based ``stage`` under zeta-2 allocation.

    ``sum_{j>=1} stage_delta(delta, j) == delta``, so any pre-declared
    (finite or infinite) sequence of stages spends at most ``delta`` overall.
    """
    validate_delta(delta)
    if stage < 1:
        raise ValueError("stage is 1-based")
    return delta / (ZETA_2 * stage * stage)


def cs_radius(n: int, width: float, delta: float) -> float:
    """Time-uniform confidence-sequence radius (geometric stitching).

    With probability >= 1 - delta, |sample_mean(n) - mean| <= cs_radius(n)
    holds *simultaneously for all n >= 1*.  Safe under arbitrary adaptive
    stopping rules and adaptive variable-selection policies.
    """
    _validate_n(n)
    validate_delta(delta)
    if width < 0.0:
        raise ValueError("width must be non-negative")
    if width == 0.0:
        return 0.0
    epoch = n.bit_length() - 1  # floor(log2(n)); n in [2**epoch, 2**(epoch+1))
    delta_epoch = delta / (ZETA_2 * (epoch + 1) ** 2)
    n_base = 1 << epoch  # smallest n in the epoch -> valid for the whole epoch
    return width * math.sqrt(math.log(2.0 / delta_epoch) / (2.0 * n_base))


def bernoulli_endpoint_radius(n: int, width: float, delta: float) -> float:
    """One-sided radius valid when all ``n`` observations equal one endpoint.

    For a variable in ``[low, high]`` with every sample equal to ``low``
    (resp. ``high``): with probability >= 1 - delta the mean lies within
    ``width * (1 - delta**(1/n))`` of that endpoint.  Derivation (Bernoulli
    case, width 1): if p > 1 - delta**(1/n) then P(all zeros) = (1-p)**n
    < delta.  Scales linearly with ``width``.  This lets (near-)constant
    variables converge far faster than Hoeffding's O(1/sqrt(n)).
    """
    _validate_n(n)
    validate_delta(delta)
    if width < 0.0:
        raise ValueError("width must be non-negative")
    return width * (1.0 - delta ** (1.0 / n))
