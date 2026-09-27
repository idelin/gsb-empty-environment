"""Adaptive Monte Carlo budget allocation across scenarios.

Only the ALLOCATION of simulation trials adapts (batches go to the scenario
with the widest confidence interval); the test rule and critical values stay
fixed throughout.  If the budget is exhausted before every interval is
narrower than ``tolerance``, the run is ``interrupted`` and the pending
scenarios keep their wide intervals as the undecided range -- no point
estimate is promoted to a conclusion.
"""

from __future__ import annotations

from bisect import bisect_left
from dataclasses import dataclass

from .exact import binomial_cdf_table, exact_power
from .interval import clopper_pearson
from .simulate import RejectionRateEstimate, Scenario, spawn_streams

__all__ = ["AdaptiveAllocation", "adaptive_allocate"]


@dataclass(frozen=True)
class AdaptiveAllocation:
    """Result of an adaptive allocation run."""

    estimates: dict[str, RejectionRateEstimate]
    budget: int
    spent: int
    tolerance: float
    status: str               # "converged" | "interrupted"
    pending: tuple[str, ...]  # scenarios whose CI is still wider than tolerance
    master_seed: int


def adaptive_allocate(
    scenarios: list[Scenario],
    *,
    master_seed: int,
    total_budget: int,
    initial_trials: int,
    batch_trials: int,
    tolerance: float,
    ci_alpha: float = 0.05,
) -> AdaptiveAllocation:
    """Allocate ``total_budget`` trials adaptively until all CIs are narrow.

    Every scenario first receives ``initial_trials`` (>= 1, so a zero-trial
    "estimate" can never occur).  Each further batch goes to the scenario
    with the widest Clopper-Pearson interval.  Streams persist across
    batches, so the whole run is reproducible from ``master_seed``.
    """
    if not scenarios:
        raise ValueError("at least one scenario is required")
    if initial_trials < 1:
        raise ValueError("initial_trials must be >= 1: zero-trial starts are refused")
    if batch_trials < 1:
        raise ValueError("batch_trials must be >= 1")
    if tolerance <= 0.0:
        raise ValueError("tolerance must be positive")
    if total_budget < initial_trials * len(scenarios):
        raise ValueError(
            "total_budget cannot fund the initial round for every scenario; "
            "refusing to leave any scenario at zero trials"
        )

    names = [s.name for s in scenarios]
    streams = spawn_streams(master_seed, names)
    refs = {s.name: exact_power(s.n, s.p0, s.p, s.alpha) for s in scenarios}
    cdfs = {s.name: binomial_cdf_table(s.n, s.p) for s in scenarios}
    trials = {name: 0 for name in names}
    rejections = {name: 0 for name in names}

    def run_batch(name: str, count: int) -> None:
        cdf = cdfs[name]
        crit = refs[name].critical
        rng = streams[name].rng
        hits = 0
        for _ in range(count):
            if bisect_left(cdf, rng.random()) >= crit:
                hits += 1
        trials[name] += count
        rejections[name] += hits

    def width(name: str) -> float:
        lo, hi = clopper_pearson(rejections[name], trials[name], ci_alpha)
        return hi - lo

    spent = 0
    for name in names:  # initial round: nobody stays at zero trials
        run_batch(name, initial_trials)
        spent += initial_trials

    status = "converged"
    while any(width(name) > tolerance for name in names):
        if spent + batch_trials > total_budget:
            status = "interrupted"
            break
        target = max(names, key=width)
        run_batch(target, batch_trials)
        spent += batch_trials

    pending = tuple(name for name in names if width(name) > tolerance)
    estimates: dict[str, RejectionRateEstimate] = {}
    for s in scenarios:
        lo, hi = clopper_pearson(rejections[s.name], trials[s.name], ci_alpha)
        estimates[s.name] = RejectionRateEstimate(
            scenario=s.name,
            n=s.n,
            critical=refs[s.name].critical,
            p=s.p,
            trials=trials[s.name],
            rejections=rejections[s.name],
            rate=rejections[s.name] / trials[s.name],
            ci_low=lo,
            ci_high=hi,
            ci_alpha=ci_alpha,
            seed=streams[s.name].seed,
            exact_power=refs[s.name].power,
            status="completed" if s.name not in pending else "interrupted",
        )
    return AdaptiveAllocation(
        estimates=estimates,
        budget=total_budget,
        spent=spent,
        tolerance=tolerance,
        status=status,
        pending=pending,
        master_seed=master_seed,
    )
