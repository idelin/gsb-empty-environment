"""Monte Carlo simulation of the rejection rate of a *fixed* binomial test.

Design rules encoded here:

* The test rule (critical value) is computed once, exactly, and never
  changes during the simulation.
* The RNG is injected by the caller; the seed is recorded on the result.
* The empirical rejection rate is always reported together with an exact
  Clopper-Pearson confidence interval -- never as a bare point estimate
  masquerading as exact power. The exact power is carried alongside as a
  separately labelled reference.
* Stopping is governed by a pre-declared LookPlan (error spending). If
  the trial budget is exhausted before the target precision is reached,
  the result is flagged "budget_exhausted" and the still-undecided
  (pending) interval is reported instead of pretending convergence.
"""
from __future__ import annotations

import random
from dataclasses import dataclass, field

from .exact import binom_pmf_all, critical_value, binom_sf
from .interval import clopper_pearson
from .spending import LookPlan, plan_looks


@dataclass(frozen=True)
class SimulationResult:
    scenario: str
    seed: int | None
    n_per_trial: int
    critical: int
    alpha: float
    p_alt: float
    trials_done: int
    rejections: int
    estimate: float                 # EMPIRICAL point estimate (not exact power)
    ci: tuple[float, float]         # EMPIRICAL exact-CP interval for the rate
    ci_confidence: float            # confidence of the reported CI
    status: str                     # "converged" | "budget_exhausted"
    stop_reason: str
    pending_half_width: float | None  # remaining gap to target, if interrupted
    exact_power: float              # EXACT reference (binomial tail recursion)
    looks_taken: int = field(default=1)


def _binomial_sampler(n: int, p: float):
    """Inverse-CDF sampler for Bin(n, p); one uniform draw per trial."""
    pmf = binom_pmf_all(n, p)
    cdf: list[float] = []
    acc = 0.0
    for prob in pmf:
        acc += prob
        cdf.append(acc)

    def sample(rng: random.Random) -> int:
        u = rng.random()
        lo, hi = 0, n
        while lo < hi:
            mid = (lo + hi) // 2
            if cdf[mid] < u:
                lo = mid + 1
            else:
                hi = mid
        return lo

    return sample


def simulate_rejection_rate(
    n: int,
    p0: float,
    p1: float,
    alpha: float = 0.05,
    *,
    rng: random.Random,
    max_trials: int = 10_000,
    target_half_width: float | None = None,
    look_plan: LookPlan | None = None,
    seed: int | None = None,
    scenario: str = "",
) -> SimulationResult:
    """Simulate the rejection rate of the fixed level-alpha test.

    n: per-trial sample size; p0: null; p1: alternative used to generate
    data. `rng` is injected by the caller. If `look_plan` is omitted, a
    single final look at `max_trials` is used (no adaptation).
    """
    if max_trials < 1:
        raise ValueError("zero trials requested: nothing to simulate")
    if target_half_width is not None and target_half_width <= 0:
        raise ValueError("target_half_width must be positive")
    critical, _size = critical_value(n, p0, alpha)
    exact = binom_sf(critical, n, p1)
    if look_plan is None:
        look_plan = plan_looks(max_trials, n_looks=1, total_alpha=alpha)
    if look_plan.looks[-1] > max_trials:
        raise ValueError("look plan exceeds the trial budget")

    sampler = _binomial_sampler(n, p1)
    rejections = 0
    trials_done = 0
    ci = (0.0, 1.0)
    confidence = 1.0 - look_plan.errors[-1]
    status = "budget_exhausted"
    stop_reason = "trial budget exhausted before target precision was reached"
    looks_taken = 0

    for look_trials, look_error in zip(look_plan.looks, look_plan.errors):
        looks_taken += 1
        while trials_done < look_trials:
            if sampler(rng) >= critical:
                rejections += 1
            trials_done += 1
        ci = clopper_pearson(rejections, trials_done, alpha=look_error)
        confidence = 1.0 - look_error
        half_width = 0.5 * (ci[1] - ci[0])
        if target_half_width is not None and half_width <= target_half_width:
            status = "converged"
            stop_reason = (
                f"CI half-width {half_width:.6g} <= target "
                f"{target_half_width:.6g} at pre-declared look {looks_taken}"
            )
            break

    pending = None
    if status != "converged" and target_half_width is not None:
        pending = 0.5 * (ci[1] - ci[0]) - target_half_width

    return SimulationResult(
        scenario=scenario,
        seed=seed,
        n_per_trial=n,
        critical=critical,
        alpha=alpha,
        p_alt=p1,
        trials_done=trials_done,
        rejections=rejections,
        estimate=rejections / trials_done,
        ci=ci,
        ci_confidence=confidence,
        status=status,
        stop_reason=stop_reason,
        pending_half_width=pending,
        exact_power=exact,
        looks_taken=looks_taken,
    )
