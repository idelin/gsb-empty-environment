"""Exact coverage verification for Bernoulli means on finite domains.

For a *finite* domain of success probabilities (a declared grid or a
genuinely small discrete set), the coverage of any radius rule
``|k/n - p| <= radius`` is computed exactly by a binomial recurrence --
no simulation, no approximation.  These are ``exact`` conclusions about
the declared grid (as opposed to ``empirical`` simulation estimates).

Strict boundary facts used below (derivations in docstrings):
* p = 0: the sample mean is 0 with probability 1, coverage is exactly 1.
* p = 1: the sample mean is 1 with probability 1, coverage is exactly 1.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .bounds import hoeffding_radius, validate_delta
from .evidence import EXACT

__all__ = [
    "binomial_pmf",
    "bernoulli_coverage_exact",
    "CoverageReport",
    "verify_radius_bernoulli",
    "verify_hoeffding_bernoulli",
]


def binomial_pmf(n: int, p: float) -> list[float]:
    """Exact Binomial(n, p) pmf via the recurrence pmf[k+1]/pmf[k] =
    ((n-k)/(k+1)) * (p/(1-p)).  Boundary cases p in {0, 1} are degenerate."""
    if isinstance(n, bool) or not isinstance(n, int) or n < 0:
        raise ValueError(f"n must be a non-negative integer, got {n!r}")
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


def bernoulli_coverage_exact(n: int, p: float, radius: float) -> float:
    """Exact P(|k/n - p| <= radius) for k ~ Binomial(n, p)."""
    if radius < 0.0:
        raise ValueError("radius must be non-negative")
    pmf = binomial_pmf(n, p)
    tol = 1e-12
    return sum(pr for k, pr in enumerate(pmf) if abs(k / n - p) <= radius + tol)


@dataclass
class CoverageReport:
    conclusion_kind: str
    n: int
    radius: float
    delta: float
    min_coverage: float
    worst_p: float
    ok: bool
    grid_size: int
    note: str = ""
    per_point: list = field(default_factory=list)

    def to_dict(self) -> dict:
        from dataclasses import asdict

        return asdict(self)


def verify_radius_bernoulli(
    n: int,
    radius: float,
    delta: float,
    p_grid: list[float] | None = None,
    grid_points: int = 201,
) -> CoverageReport:
    """Verify, exactly on a finite domain, that ``radius`` covers at 1-delta.

    The check is exact at every declared grid point (binomial recursion).
    The conclusion is labelled ``exact`` *for the declared finite domain*;
    it is not a proof over the continuum [0, 1] unless the domain itself
    is the full (finite) support of the problem.
    """
    validate_delta(delta)
    if p_grid is None:
        if grid_points < 2:
            raise ValueError("grid_points must be >= 2")
        p_grid = [i / (grid_points - 1) for i in range(grid_points)]
    if not p_grid:
        raise ValueError("p_grid must be non-empty")
    per_point, min_cov, worst_p = [], float("inf"), None
    for p in p_grid:
        cov = bernoulli_coverage_exact(n, p, radius)
        per_point.append({"p": p, "coverage": cov})
        if cov < min_cov:
            min_cov, worst_p = cov, p
    return CoverageReport(
        conclusion_kind=EXACT,
        n=n,
        radius=radius,
        delta=delta,
        min_coverage=min_cov,
        worst_p=worst_p,
        ok=min_cov >= 1.0 - delta,
        grid_size=len(p_grid),
        note=(
            "exact binomial-recursion coverage on the declared finite grid; "
            "boundary points p=0 and p=1 have coverage exactly 1"
        ),
        per_point=per_point,
    )


def verify_hoeffding_bernoulli(n: int, delta: float, **kwargs) -> CoverageReport:
    """Exactly verify the fixed-sample Hoeffding radius for Bernoulli(1-width)."""
    return verify_radius_bernoulli(n, hoeffding_radius(n, 1.0, delta), delta, **kwargs)
