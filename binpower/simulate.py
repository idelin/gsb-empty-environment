"""Monte Carlo simulation of rejection rates under the FIXED test rule.

The random source is injected by the caller (a ``StreamHandle`` built from
an explicit seed); every result records its seed and trial count.  Empirical
rates are always paired with a Clopper-Pearson interval and are labeled
``kind="empirical"`` -- a point estimate is never presented as exact power.
Exact references (labeled ``kind="exact"``) come from :mod:`binpower.exact`.
"""

from __future__ import annotations

import hashlib
import random
from bisect import bisect_left
from dataclasses import dataclass

from .exact import binomial_cdf_table, exact_power
from .interval import clopper_pearson

__all__ = [
    "Scenario",
    "StreamHandle",
    "child_seed",
    "spawn_streams",
    "RejectionRateEstimate",
    "simulate_rejection_rate",
    "run_scenarios",
]


@dataclass(frozen=True)
class Scenario:
    """One binomial testing scenario with a fixed rejection rule."""

    name: str
    n: int
    p0: float
    p: float
    alpha: float = 0.05


@dataclass
class StreamHandle:
    """An independently seeded, reproducible random stream for one scenario."""

    label: str
    seed: int
    rng: random.Random


def child_seed(master_seed: int, label: str) -> int:
    """Deterministic per-scenario seed depending only on (master_seed, label).

    Sibling scenarios neither share nor perturb each other's stream, so each
    scenario is reproducible standalone or in any ensemble.
    """
    digest = hashlib.sha256(f"{master_seed}|{label}".encode("utf-8")).digest()
    return int.from_bytes(digest[:8], "big")


def spawn_streams(master_seed: int, labels: list[str]) -> dict[str, StreamHandle]:
    """Create one independent stream per label from a single master seed."""
    handles: dict[str, StreamHandle] = {}
    for label in labels:
        seed = child_seed(master_seed, label)
        handles[label] = StreamHandle(label=label, seed=seed, rng=random.Random(seed))
    return handles


@dataclass(frozen=True)
class RejectionRateEstimate:
    """Empirical rejection-rate estimate -- NOT exact power.

    ``rate`` is a Monte Carlo point estimate; ``ci_low``/``ci_high`` are its
    exact Clopper-Pearson interval.  ``exact_power`` (when attached) is the
    exact reference from tail-probability recursion, labeled separately.
    ``status == "interrupted"`` means the trial budget ran out; the interval
    is then the still-undecided range, not a converged answer.
    """

    scenario: str
    n: int
    critical: int
    p: float
    trials: int
    rejections: int
    rate: float
    ci_low: float
    ci_high: float
    ci_alpha: float
    seed: int
    exact_power: float | None
    status: str = "completed"  # "completed" | "interrupted"
    kind: str = "empirical"

    @property
    def ci_width(self) -> float:
        return self.ci_high - self.ci_low


def simulate_rejection_rate(
    *,
    scenario: str,
    n: int,
    critical: int,
    p: float,
    trials: int,
    stream: StreamHandle,
    ci_alpha: float = 0.05,
    exact: float | None = None,
    trial_budget: int | None = None,
) -> RejectionRateEstimate:
    """Simulate the fixed rule "reject iff X >= critical" under Bin(n, p).

    ``trial_budget`` caps how many of the requested ``trials`` may run; if
    the cap binds, the result is marked ``interrupted`` and its interval is
    the pending (undecided) range.
    """
    if trials < 1:
        raise ValueError("trials must be >= 1: zero-trial estimates are refused")
    if not 1 <= critical <= n + 1:
        raise ValueError("critical must be in [1, n + 1]")
    cdf = binomial_cdf_table(n, p)
    budget = trials if trial_budget is None else max(0, min(trials, trial_budget))
    if budget < 1:
        raise ValueError("trial_budget leaves zero trials: estimates are refused")
    rejections = 0
    for _ in range(budget):
        count = bisect_left(cdf, stream.rng.random())
        if count >= critical:
            rejections += 1
    status = "completed" if budget == trials else "interrupted"
    lo, hi = clopper_pearson(rejections, budget, ci_alpha)
    return RejectionRateEstimate(
        scenario=scenario,
        n=n,
        critical=critical,
        p=p,
        trials=budget,
        rejections=rejections,
        rate=rejections / budget,
        ci_low=lo,
        ci_high=hi,
        ci_alpha=ci_alpha,
        seed=stream.seed,
        exact_power=exact,
        status=status,
    )


def run_scenarios(
    scenarios: list[Scenario],
    *,
    master_seed: int,
    trials: int,
    ci_alpha: float = 0.05,
) -> dict[str, RejectionRateEstimate]:
    """Run every scenario on its own independent, seed-recorded stream.

    Each estimate is paired with the exact power reference for its fixed
    rule; the two are labeled ``empirical`` vs ``exact`` and never merged.
    """
    streams = spawn_streams(master_seed, [s.name for s in scenarios])
    results: dict[str, RejectionRateEstimate] = {}
    for spec in scenarios:
        ref = exact_power(spec.n, spec.p0, spec.p, spec.alpha)
        results[spec.name] = simulate_rejection_rate(
            scenario=spec.name,
            n=spec.n,
            critical=ref.critical,
            p=spec.p,
            trials=trials,
            stream=streams[spec.name],
            ci_alpha=ci_alpha,
            exact=ref.power,
        )
    return results
