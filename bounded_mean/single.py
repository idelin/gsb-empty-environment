"""Staged mean estimation for a single bounded random variable.

The estimator checks the error target only at *pre-declared* checkpoints
and spends a pre-allocated share of the failure probability at each stage
(zeta-2 allocation, summing to ``delta`` over any number of stages).  It
stops only when the certified radius meets ``epsilon``; if the sample
budget is exhausted first, the result is explicitly ``NOT_MET`` and the
full stopping evidence is preserved.  A plain fixed-sample Hoeffding
bound is never reused across peeks.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .bounds import (
    bernoulli_endpoint_radius,
    hoeffding_radius,
    stage_delta,
    validate_delta,
    validate_epsilon,
    validate_width,
)
from .evidence import EMPIRICAL, EXACT, MET, NOT_MET

__all__ = ["StageRecord", "EstimationResult", "StagedEstimator"]


@dataclass
class StageRecord:
    stage: int
    n: int
    estimate: float
    radius: float
    delta_stage: float
    rule: str
    met: bool


@dataclass
class EstimationResult:
    name: str
    status: str  # MET or NOT_MET
    conclusion_kind: str  # EXACT or EMPIRICAL
    estimate: float | None
    radius: float | None
    epsilon: float
    delta: float
    n_samples: int
    seed: object
    rng_draws: int | None
    stages: list = field(default_factory=list)
    guarantee: str = ""

    def to_dict(self) -> dict:
        from dataclasses import asdict

        return asdict(self)


class StagedEstimator:
    """Estimate the mean of one variable in ``[low, high]`` to within epsilon."""

    GUARANTEE = (
        "P(|estimate - mean| <= epsilon) >= 1 - delta: staged Hoeffding at "
        "pre-declared checkpoints with zeta-2 error allocation (union bound "
        "over stages; no independence assumption on the checks)."
    )
    ENDPOINT_NOTE = (
        "bernoulli_endpoint_rule enabled: each stage splits its budget "
        "evenly between Hoeffding and the all-equal-endpoint rule."
    )

    def __init__(
        self,
        *,
        name: str,
        low: float,
        high: float,
        epsilon: float,
        delta: float,
        budget: int,
        sampler,
        rng,
        checkpoints: list[int] | None = None,
        bernoulli_endpoint_rule: bool = False,
    ):
        self.name = str(name)
        self.low = float(low)
        self.high = float(high)
        self.width = validate_width(self.low, self.high)
        self.epsilon = validate_epsilon(epsilon)
        self.delta = validate_delta(delta)
        if isinstance(budget, bool) or not isinstance(budget, int) or budget < 1:
            raise ValueError(f"budget must be a positive integer, got {budget!r}")
        self.budget = budget
        if not callable(sampler):
            raise TypeError("sampler must be callable: sampler(rng) -> float in [low, high]")
        self._sampler = sampler
        self._rng = rng
        self.bernoulli_endpoint_rule = bool(bernoulli_endpoint_rule)
        self.checkpoints = self._resolve_checkpoints(checkpoints)

    def _resolve_checkpoints(self, checkpoints) -> list[int]:
        if checkpoints is None:
            # Default: dyadic checkpoints 1, 2, 4, ... capped at the budget.
            cps, n = [], 1
            while n < self.budget:
                cps.append(n)
                n *= 2
            cps.append(self.budget)
            return cps
        cps = list(checkpoints)
        if not cps:
            raise ValueError("checkpoints must be non-empty")
        if any(isinstance(c, bool) or not isinstance(c, int) or c < 1 for c in cps):
            raise ValueError("checkpoints must be positive integers")
        if any(b <= a for a, b in zip(cps, cps[1:])):
            raise ValueError("checkpoints must be strictly increasing (pre-declared)")
        if cps[-1] > self.budget:
            raise ValueError("checkpoints must not exceed the budget")
        return cps

    def _draw(self) -> float:
        x = float(self._sampler(self._rng))
        if not self.low <= x <= self.high:
            raise ValueError(
                f"sampler returned {x!r} outside declared bounds "
                f"[{self.low}, {self.high}]"
            )
        return x

    def _radius(self, n, stage, seen_low, seen_high):
        d_stage = stage_delta(self.delta, stage)
        if self.bernoulli_endpoint_rule:
            # Split the stage budget evenly between the two rules so the
            # per-stage failure probability stays <= d_stage.
            d_half = d_stage / 2.0
            radius = hoeffding_radius(n, self.width, d_half)
            rule = "hoeffding"
            if seen_low == seen_high == self.low or seen_low == seen_high == self.high:
                r_ep = bernoulli_endpoint_radius(n, self.width, d_half)
                if r_ep < radius:
                    radius, rule = r_ep, "bernoulli-endpoint"
            return radius, rule, d_stage
        return hoeffding_radius(n, self.width, d_stage), "hoeffding", d_stage

    def run(self) -> EstimationResult:
        seed = getattr(self._rng, "seed", None)

        # Degenerate (constant) variable: exact answer, zero samples needed.
        if self.width == 0.0:
            return EstimationResult(
                name=self.name,
                status=MET,
                conclusion_kind=EXACT,
                estimate=self.low,
                radius=0.0,
                epsilon=self.epsilon,
                delta=self.delta,
                n_samples=0,
                seed=seed,
                rng_draws=getattr(self._rng, "draws", None),
                stages=[],
                guarantee="constant variable (low == high): exact, no sampling required",
            )

        total, n = 0.0, 0
        seen_low, seen_high = float("inf"), float("-inf")
        records: list[StageRecord] = []
        result = None
        for stage, cp in enumerate(self.checkpoints, start=1):
            while n < cp:
                x = self._draw()
                total += x
                n += 1
                seen_low = min(seen_low, x)
                seen_high = max(seen_high, x)
            radius, rule, d_stage = self._radius(n, stage, seen_low, seen_high)
            estimate = total / n
            met = radius <= self.epsilon
            records.append(
                StageRecord(
                    stage=stage, n=n, estimate=estimate, radius=radius,
                    delta_stage=d_stage, rule=rule, met=met,
                )
            )
            if met:
                result = EstimationResult(
                    name=self.name, status=MET, conclusion_kind=EMPIRICAL,
                    estimate=estimate, radius=radius, epsilon=self.epsilon,
                    delta=self.delta, n_samples=n, seed=seed,
                    rng_draws=getattr(self._rng, "draws", None),
                    stages=records,
                    guarantee=self.GUARANTEE
                    + (" " + self.ENDPOINT_NOTE if self.bernoulli_endpoint_rule else ""),
                )
                break

        if result is None:
            # Budget exhausted: explicitly NOT met. Never report convergence.
            result = EstimationResult(
                name=self.name, status=NOT_MET, conclusion_kind=EMPIRICAL,
                estimate=total / n if n else None,
                radius=records[-1].radius if records else None,
                epsilon=self.epsilon, delta=self.delta, n_samples=n, seed=seed,
                rng_draws=getattr(self._rng, "draws", None),
                stages=records,
                guarantee=(
                    "BUDGET EXHAUSTED before the error target was certified: "
                    "the epsilon-accuracy guarantee does NOT hold for this run. "
                    "All staged evidence is preserved."
                ),
            )
        return result
