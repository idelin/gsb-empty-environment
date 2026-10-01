"""Exact coverage verification for Bernoulli confidence radii.

For a Bernoulli(p) variable the success count K in n trials is
Binomial(n, p), so the coverage probability of any radius rule can be
computed EXACTLY by a recurrence over k = 0..n -- no simulation involved.
This is feasible for small n and gives a strict, distribution-exact
verification (or refutation) of a proposed bound, including the boundary
points p = 0 and p = 1 where coverage is exactly 1.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Callable

from .confidence import check_delta, check_positive_int, hoeffding_radius
from .estimator import ConclusionKind

RadiusFn = Callable[[int], float]


def binomial_pmf(n: int, p: float) -> list[float]:
    """Exact P(K = k) for k = 0..n via the ratio recurrence."""
    check_positive_int(n, "n")
    if not 0.0 <= p <= 1.0:
        raise ValueError(f"p must lie in [0, 1], got {p!r}")
    if p == 0.0:
        return [1.0] + [0.0] * n
    if p == 1.0:
        return [0.0] * n + [1.0]
    pmf = [0.0] * (n + 1)
    pmf[0] = (1.0 - p) ** n
    for k in range(n):
        pmf[k + 1] = pmf[k] * (n - k) / (k + 1) * p / (1.0 - p)
    return pmf


def exact_coverage(n: int, p: float, radius_fn: RadiusFn) -> float:
    """Exact P(|K/n - p| <= radius_fn(n)) for K ~ Binomial(n, p)."""
    pmf = binomial_pmf(n, p)
    radius = radius_fn(n)
    if not radius >= 0.0:
        raise ValueError(f"radius_fn returned a negative radius {radius!r}")
    tol = 1e-12
    return math.fsum(pmf[k] for k in range(n + 1) if abs(k / n - p) <= radius + tol)


@dataclass(frozen=True)
class GridVerification:
    """Exact worst-case coverage of a radius rule on a grid of p values."""

    n: int
    grid_size: int
    worst_p: float
    worst_coverage: float
    target: float
    passed: bool
    kind: ConclusionKind = ConclusionKind.EXACT


def verify_on_grid(
    n: int, radius_fn: RadiusFn, target: float, grid_size: int = 201
) -> GridVerification:
    """Exact minimum coverage of ``radius_fn`` over a uniform grid of p in [0, 1]."""
    check_positive_int(n, "n")
    check_positive_int(grid_size, "grid_size")
    if grid_size < 2:
        raise ValueError("grid_size must be at least 2")
    check_delta(target)
    worst_p = 0.0
    worst_cov = math.inf
    for i in range(grid_size):
        p = i / (grid_size - 1)
        cov = exact_coverage(n, p, radius_fn)
        if cov < worst_cov:
            worst_cov, worst_p = cov, p
    return GridVerification(
        n=n,
        grid_size=grid_size,
        worst_p=worst_p,
        worst_coverage=worst_cov,
        target=target,
        passed=worst_cov >= target,
    )


def hoeffding_bernoulli_radius(delta: float) -> RadiusFn:
    """The Hoeffding radius on [0, 1] as a radius rule for verification."""
    check_delta(delta)

    def radius_fn(n: int) -> float:
        return hoeffding_radius(n, 0.0, 1.0, delta)

    return radius_fn
