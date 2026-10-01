"""Simultaneous mean estimation for several bounded variables.

The overall failure probability is controlled by a PRE-DECLARED allocation:
variable ``i`` receives ``delta_i = delta * weight_i / sum(weights)`` and
runs its own staged (peeled) confidence schedule.  The union bound over
variables and stages needs NO independence assumption, so variables may
share correlated samples (``joint_sampler`` mode) without invalidating the
guarantee.  We never multiply per-variable coverage probabilities -- that
would wrongly treat shared, correlated samples as independent.

The next variable to sample is chosen adaptively (largest current
radius/epsilon ratio).  Adaptivity is safe because every variable's checks
still happen at its own pre-declared sample sizes.
"""

from __future__ import annotations

import math
import random
from dataclasses import dataclass
from typing import Callable

from .confidence import (
    check_bounds,
    check_delta,
    check_epsilon,
    check_positive_int,
    constant_observation_radius,
    hoeffding_radius,
    stage_delta,
    trivial_radius,
)
from .estimator import ConclusionKind, Sampler, StageRecord, StagedTracker, Status

JointSampler = Callable[[random.Random], dict[str, float]]


@dataclass(frozen=True)
class VariableSpec:
    name: str
    low: float
    high: float
    epsilon: float
    sampler: Sampler | None = None
    weight: float = 1.0


@dataclass(frozen=True)
class SelectionRecord:
    """Evidence of one adaptive selection step."""

    step: int
    variable: str
    reason: str
    draws: int


@dataclass
class VariableResult:
    name: str
    estimate: float
    estimate_kind: ConclusionKind
    radius: float
    guarantee_kind: ConclusionKind
    epsilon: float
    delta: float
    n: int
    converged: bool
    target_met: bool
    evidence: tuple[StageRecord, ...]


@dataclass
class MultiResult:
    results: dict[str, VariableResult]
    status: Status
    converged: bool
    seed: int | None
    total_draws: int
    delta: float
    selection_log: tuple[SelectionRecord, ...]


class AdaptiveMultiEstimator:
    """Adaptively sample several bounded variables under one error budget."""

    def __init__(
        self,
        *,
        variables: list[VariableSpec],
        delta: float,
        max_samples: int,
        rng: random.Random | None = None,
        seed: int | None = None,
        initial_stage: int = 8,
        growth: float = 2.0,
        joint_sampler: JointSampler | None = None,
    ) -> None:
        check_delta(delta)
        check_positive_int(max_samples, "max_samples")
        if not variables:
            raise ValueError("at least one variable is required")
        names = [v.name for v in variables]
        if len(set(names)) != len(names):
            raise ValueError("variable names must be unique")
        for v in variables:
            check_bounds(v.low, v.high)
            check_epsilon(v.epsilon)
            if not math.isfinite(v.weight) or v.weight <= 0.0:
                raise ValueError(f"weight for {v.name!r} must be positive, got {v.weight!r}")
            if joint_sampler is None and v.sampler is None:
                raise ValueError(
                    f"variable {v.name!r} needs a sampler (or pass a joint_sampler)"
                )
        self.variables = list(variables)
        self.joint_sampler = joint_sampler
        self.delta = float(delta)
        self.max_samples = max_samples
        self.rng = rng if rng is not None else random.Random(seed)
        self.seed = seed
        total_weight = sum(v.weight for v in variables)
        self._delta_alloc = {v.name: delta * v.weight / total_weight for v in variables}
        self._trackers = {
            v.name: StagedTracker(
                low=v.low,
                high=v.high,
                epsilon=v.epsilon,
                delta=self._delta_alloc[v.name],
                initial_stage=initial_stage,
                growth=growth,
            )
            for v in variables
        }
        self._draws = 0
        self._log: list[SelectionRecord] = []
        self._hopeless_logged: set[str] = set()

    def _hopeless(self, v: VariableSpec, remaining: int) -> bool:
        """True if variable ``v`` cannot meet its target within ``remaining``.

        Projection uses the worst-case (Hoeffding) radius at future stage
        sizes; the constant-observation acceleration is included only while
        every sample seen so far is identical.  Hopeless variables are left
        unconverged instead of starving the others of budget.
        """
        tracker = self._trackers[v.name]
        for stage, size in tracker.future_stage_sizes():
            if size - tracker.n > remaining:
                return True
            half = stage_delta(tracker.delta, stage) / 2.0
            radius = hoeffding_radius(size, tracker.low, tracker.high, half)
            if tracker.all_equal:
                radius = min(
                    radius,
                    constant_observation_radius(size, tracker.low, tracker.high, half),
                )
            if min(radius, trivial_radius(tracker.low, tracker.high)) <= tracker.epsilon:
                return False

    def _urgency(self, v: VariableSpec) -> float:
        tracker = self._trackers[v.name]
        if tracker.records:
            return tracker.records[-1].radius / v.epsilon
        return math.inf

    def run(self) -> MultiResult:
        status = Status.BUDGET_EXHAUSTED
        step = 0
        while True:
            pending = [v for v in self.variables if not self._trackers[v.name].converged]
            if not pending:
                status = Status.CONVERGED
                break
            remaining = self.max_samples - self._draws
            if remaining <= 0:
                break
            feasible = []
            for v in pending:
                fits = (
                    self._trackers[v.name].next_stage_size - self._trackers[v.name].n
                    <= remaining
                )
                if fits and self._hopeless(v, remaining):
                    # Kept as stopping evidence: the target is provably
                    # unreachable within the remaining budget.
                    if v.name not in self._hopeless_logged:
                        self._hopeless_logged.add(v.name)
                        self._log.append(
                            SelectionRecord(
                                step=step,
                                variable=v.name,
                                reason="hopeless: epsilon unreachable within "
                                "remaining budget; left unconverged",
                                draws=0,
                            )
                        )
                        step += 1
                    continue
                if fits:
                    feasible.append(v)
            if not feasible:
                break
            chosen = max(feasible, key=self._urgency)
            tracker = self._trackers[chosen.name]
            need = tracker.next_stage_size - tracker.n
            if tracker.records:
                reason = f"largest radius/epsilon ratio {self._urgency(chosen):.4g}"
            else:
                reason = "never checked; initializing"
            if self.joint_sampler is not None:
                for _ in range(need):
                    values = self.joint_sampler(self.rng)
                    self._draws += 1
                    for v in pending:
                        if v.name not in values:
                            raise ValueError(
                                f"joint_sampler did not return a value for {v.name!r}"
                            )
                        self._trackers[v.name].add(values[v.name])
                    # Shared draws advance every pending variable together;
                    # check each one exactly when it hits its own stage size.
                    for v in pending:
                        t = self._trackers[v.name]
                        if not t.converged and t.n == t.next_stage_size:
                            t.check()
            else:
                assert chosen.sampler is not None
                for _ in range(need):
                    tracker.add(chosen.sampler(self.rng))
                    self._draws += 1
                tracker.check()
            self._log.append(
                SelectionRecord(step=step, variable=chosen.name, reason=reason, draws=need)
            )
            step += 1
        results = {}
        for v in self.variables:
            t = self._trackers[v.name]
            if t.records:
                last = t.records[-1]
                estimate, radius = last.mean, last.radius
            else:
                estimate, radius = t.mean, trivial_radius(v.low, v.high)
            results[v.name] = VariableResult(
                name=v.name,
                estimate=estimate,
                estimate_kind=ConclusionKind.EMPIRICAL,
                radius=radius,
                guarantee_kind=ConclusionKind.EXACT,
                epsilon=v.epsilon,
                delta=self._delta_alloc[v.name],
                n=t.n,
                converged=t.converged,
                target_met=t.converged,
                evidence=tuple(t.records),
            )
        return MultiResult(
            results=results,
            status=status,
            converged=all(r.converged for r in results.values()),
            seed=self.seed,
            total_draws=self._draws,
            delta=self.delta,
            selection_log=tuple(self._log),
        )
