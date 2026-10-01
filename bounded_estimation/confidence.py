"""Exact, distribution-free confidence bounds for bounded random variables.

Every radius computed here is an EXACT guarantee: for i.i.d. variables
supported on ``[low, high]`` the stated coverage holds for *every* such
distribution, at every sample size, with no asymptotic approximation.

Repeated peeking cannot reuse a single fixed-sample bound.  We therefore
use a pre-declared *peeling* schedule: checks happen only at geometric
sample sizes ``n_0, n_1, ...`` and stage ``k`` is allocated
``delta / 2**(k+1)`` of the total failure probability.  The union over all
stages costs at most ``delta`` no matter when the procedure stops.
"""

from __future__ import annotations

import math
from collections.abc import Iterator

__all__ = [
    "check_bounds",
    "check_delta",
    "check_epsilon",
    "check_positive_int",
    "hoeffding_radius",
    "constant_observation_radius",
    "trivial_radius",
    "stage_sample_sizes",
    "stage_delta",
]


def check_bounds(low: float, high: float) -> None:
    if not (math.isfinite(low) and math.isfinite(high)):
        raise ValueError(f"bounds must be finite, got [{low}, {high}]")
    if not low < high:
        raise ValueError(f"require low < high, got [{low}, {high}]")


def check_delta(delta: float) -> None:
    if not (0.0 < delta < 1.0):
        raise ValueError(f"delta must lie in the open interval (0, 1), got {delta!r}")


def check_epsilon(epsilon: float) -> None:
    if not math.isfinite(epsilon) or epsilon <= 0.0:
        raise ValueError(f"epsilon must be positive and finite, got {epsilon!r}")


def check_positive_int(value: int, name: str) -> None:
    if isinstance(value, bool) or not isinstance(value, int) or value < 1:
        raise ValueError(f"{name} must be a positive integer, got {value!r}")


def hoeffding_radius(n: int, low: float, high: float, delta: float) -> float:
    """Exact Hoeffding radius: ``P(|mean_hat - mean| > r) <= delta``."""
    check_positive_int(n, "n")
    check_bounds(low, high)
    check_delta(delta)
    return (high - low) * math.sqrt(math.log(2.0 / delta) / (2.0 * n))


def constant_observation_radius(n: int, low: float, high: float, delta: float) -> float:
    """Exact radius usable on the event that all ``n`` observations are identical.

    Derivation: let ``q = P(X != c)`` for the observed constant ``c``.
    Observing ``n`` copies of ``c`` in a row has probability ``(1 - q)**n``,
    which is below ``delta`` whenever ``q > 1 - delta**(1/n)``.  Hence, on
    the event "all equal", ``q <= 1 - delta**(1/n)`` with confidence
    ``1 - delta``, and ``|E[X] - c| <= q * (high - low)``.  This shrinks
    like ``(high - low) * ln(1/delta) / n`` -- much faster than Hoeffding's
    ``1/sqrt(n)`` -- so constant (degenerate) variables converge quickly.
    """
    check_positive_int(n, "n")
    check_bounds(low, high)
    check_delta(delta)
    return (high - low) * (1.0 - delta ** (1.0 / n))


def trivial_radius(low: float, high: float) -> float:
    """Radius that always holds: both estimate and mean lie in [low, high]."""
    check_bounds(low, high)
    return (high - low) / 2.0


def stage_sample_sizes(initial: int, growth: float) -> Iterator[int]:
    """Pre-declared geometric check schedule: ``initial, initial*growth, ...``."""
    check_positive_int(initial, "initial")
    if not growth > 1.0:
        raise ValueError(f"growth must exceed 1, got {growth!r}")
    n = initial
    while True:
        yield n
        n = max(n + 1, math.ceil(n * growth))


def stage_delta(delta: float, stage: int) -> float:
    """Failure probability allocated to one stage; sums to ``delta`` over stages."""
    check_delta(delta)
    if not isinstance(stage, int) or stage < 0:
        raise ValueError(f"stage must be a non-negative integer, got {stage!r}")
    return delta / (2.0 ** (stage + 1))
