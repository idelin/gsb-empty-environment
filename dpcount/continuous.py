"""Classic continuous Laplace mechanism for a subject-level count.

*** NUMERICAL APPROXIMATION ONLY ***
This implementation uses IEEE-754 floating point throughout (inverse CDF in
particular).  It preserves the original textbook Laplace interface but must
NOT be treated as an exact privacy mechanism: floating-point rounding can make
theoretical tails unreachable.  For the exact, formally verifiable mechanism
use dpcount.discrete.DiscreteLaplaceCount.

Definition (continuous, theoretical)
------------------------------------
Laplace noise Z ~ Laplace(scale = sensitivity / epsilon), density
f(z) = epsilon/(2*sensitivity) * exp(-epsilon*|z|/sensitivity).
Inverse CDF for uniform u in (0, 1)::

    z =  scale * ln(2u)             if u <  1/2
    z = -scale * ln(2(1 - u))       if u >= 1/2

epsilon <= 0 is rejected.  A uniform draw of exactly 0 or 1 (a measure-zero
event, but representable in floating point) raises instead of being silently
redrawn: the raw output is never corrected by resampling.  Negative noise is
kept in the raw record; the clamped value is recorded separately.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Any


class ParameterError(ValueError):
    """Invalid mechanism parameter, e.g. epsilon <= 0."""


@dataclass(frozen=True)
class LaplaceRecord:
    """One numeric (approximate) Laplace run; raw and post-processed separate."""

    mechanism: str
    true_count: float
    uniform_input: float
    raw_noise: float
    raw_noisy_count: float
    postprocessed_count: float
    postprocessing: str


def _check_params(sensitivity: float, epsilon: float) -> None:
    if not epsilon > 0:
        raise ParameterError("epsilon must be > 0 (got %r)" % (epsilon,))
    if sensitivity < 0:
        raise ParameterError("sensitivity must be >= 0")


def laplace_inverse_cdf(u: float, scale: float) -> float:
    """Inverse Laplace CDF: map uniform u in (0, 1) to a float noise value."""
    if not (0.0 < u < 1.0):
        raise ParameterError(
            "uniform input must be in the open interval (0, 1); got %r "
            "(no silent resampling)" % (u,)
        )
    if scale < 0:
        raise ParameterError("scale must be >= 0")
    if scale == 0:
        return 0.0
    if u < 0.5:
        return scale * math.log(2.0 * u)
    return -scale * math.log(2.0 * (1.0 - u))


def add_laplace_noise(
    count: float,
    sensitivity: float,
    epsilon: float,
    rng: Any,
    floor: float = 0.0,
) -> LaplaceRecord:
    """Add Laplace(scale=sensitivity/epsilon) to a subject-level count.

    ``rng`` is any object with ``random() -> float`` (e.g. random.Random).
    The same deterministic rng input reproduces the same output via the inverse
    CDF (see laplace_inverse_cdf).
    """
    _check_params(sensitivity, epsilon)
    u = rng.random()
    if not (0.0 < u < 1.0):
        raise ParameterError(
            "rng.random() returned %r, outside (0, 1); refusing to resample"
            % (u,)
        )
    scale = sensitivity / epsilon
    raw_noise = laplace_inverse_cdf(u, scale)
    raw_noisy = count + raw_noise
    return LaplaceRecord(
        mechanism="continuous-laplace(float, NUMERICAL APPROXIMATION)",
        true_count=count,
        uniform_input=u,
        raw_noise=raw_noise,
        raw_noisy_count=raw_noisy,
        postprocessed_count=max(floor, raw_noisy),
        postprocessing=("raw float kept as-is; postprocessed = max(%r, raw), "
                        "no resampling" % floor),
    )
