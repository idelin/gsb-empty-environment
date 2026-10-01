"""Monte Carlo permutation tests with an explicit error interval.

The random source is injected by the caller (a ``random.Random``
instance) or built from an explicit seed; the seed and the number of
draws are recorded on the result.  The reported p-value is an
*empirical estimate* labelled as approximate, accompanied by a Wilson
score confidence interval -- never a pseudo-exact value.

The estimate uses the "+1" correction, p_hat = (k + 1) / (B + 1), which
accounts for the observed arrangement itself and keeps p_hat > 0.
"""
from __future__ import annotations

import random
import statistics
from dataclasses import dataclass
from typing import Any, Optional, Tuple

from .exact import ALTERNATIVES, _tolerance, is_extreme


@dataclass
class MonteCarloResult:
    """Approximate p-value from random permutations (not exact)."""

    p_estimate: float
    ci_low: float
    ci_high: float
    confidence: float
    draws: int
    n_extreme: int
    observed_statistic: float
    alternative: str
    design: str
    seed: Any
    method: str = "monte-carlo (approximate)"


def wilson_interval(k: int, n: int, confidence: float = 0.95) -> Tuple[float, float]:
    """Wilson score interval for a binomial proportion."""
    if n <= 0:
        return (0.0, 1.0)
    z = statistics.NormalDist().inv_cdf(0.5 + confidence / 2.0)
    p = k / n
    z2 = z * z
    denom = 1.0 + z2 / n
    center = (p + z2 / (2.0 * n)) / denom
    half = z * ((p * (1.0 - p) / n + z2 / (4.0 * n * n)) ** 0.5) / denom
    return (max(0.0, center - half), min(1.0, center + half))


def monte_carlo_test(
    space,
    draws: int,
    rng: Optional[random.Random] = None,
    seed: Any = None,
    alternative: str = "two-sided",
    confidence: float = 0.95,
) -> MonteCarloResult:
    """Estimate the p-value from ``draws`` uniformly sampled permutations.

    The caller injects the randomness: either a ``random.Random`` (or
    compatible) instance via ``rng``, or a ``seed`` from which one is
    built.  Both the seed and the draw count are recorded on the result.
    """
    if alternative not in ALTERNATIVES:
        raise ValueError(f"alternative must be one of {ALTERNATIVES}")
    if draws < 1:
        raise ValueError("draws must be >= 1")
    if not 0.0 < confidence < 1.0:
        raise ValueError("confidence must be in (0, 1)")
    if rng is None:
        if seed is None:
            raise ValueError("inject a random source: pass rng= or seed=")
        rng = random.Random(seed)

    observed = space.statistic(space.observed_rank())
    tol = _tolerance(observed)
    k = 0
    for _ in range(draws):
        if is_extreme(space.sample_statistic(rng), observed, alternative, tol):
            k += 1

    p_hat = (k + 1) / (draws + 1)
    ci_low, ci_high = wilson_interval(k, draws, confidence)
    return MonteCarloResult(
        p_estimate=p_hat,
        ci_low=ci_low,
        ci_high=ci_high,
        confidence=confidence,
        draws=draws,
        n_extreme=k,
        observed_statistic=observed,
        alternative=alternative,
        design=space.design,
        seed=seed,
    )
