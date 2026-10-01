"""Staged mean estimation for one bounded random variable.

The estimator only stops when the pre-declared confidence-sequence radius
meets the error target ``epsilon``.  If the sample budget runs out first it
reports ``Status.BUDGET_EXHAUSTED`` and never claims convergence; every
stage check is kept as stopping evidence.
"""

from __future__ import annotations

import math
import random
from dataclasses import dataclass
from enum import Enum
from typing import Callable

from .confidence import (
    check_bounds,
    check_delta,
    check_epsilon,
    check_positive_int,
    constant_observation_radius,
    hoeffding_radius,
    stage_delta,
    stage_sample_sizes,
    trivial_radius,
)

Sampler = Callable[[random.Random], float]


class Status(Enum):
    CONVERGED = "converged"
    BUDGET_EXHAUSTED = "budget_exhausted"


class ConclusionKind(Enum):
    """Labels separating exact guarantees from empirical estimates."""

    EXACT = "exact"
    EMPIRICAL = "empirical"


@dataclass(frozen=True)
class StageRecord:
    """Stopping evidence for one stage check."""

    stage: int
    n: int
    mean: float
    radius: float
    stage_delta: float
    epsilon: float
    target_met: bool
    all_equal: bool
    forced: bool
    kind: ConclusionKind = ConclusionKind.EMPIRICAL  # the mean is empirical


class StagedTracker:
    """Accumulates samples and checks a pre-declared geometric stage schedule.

    A check is allowed only at the pre-declared stage sizes (or, for the
    single-variable driver, at the a-priori budget cap), so the union bound
    over stages stays valid under repeated peeking.
    """

    def __init__(
        self,
        *,
        low: float,
        high: float,
        epsilon: float,
        delta: float,
        initial_stage: int = 8,
        growth: float = 2.0,
    ) -> None:
        check_bounds(low, high)
        check_epsilon(epsilon)
        check_delta(delta)
        check_positive_int(initial_stage, "initial_stage")
        if not growth > 1.0:
            raise ValueError(f"growth must exceed 1, got {growth!r}")
        self.low = float(low)
        self.high = float(high)
        self.epsilon = float(epsilon)
        self.delta = float(delta)
        self._growth = float(growth)
        self._sizes = stage_sample_sizes(initial_stage, growth)
        self._next_size = next(self._sizes)
        self._stage = 0
        self._n = 0
        self._sum = 0.0
        self._min = math.inf
        self._max = -math.inf
        self.records: list[StageRecord] = []
        self.converged = False

    @property
    def n(self) -> int:
        return self._n

    @property
    def stage(self) -> int:
        return self._stage

    @property
    def next_stage_size(self) -> int:
        return self._next_size

    @property
    def mean(self) -> float:
        if self._n == 0:
            return (self.low + self.high) / 2.0
        if self.all_equal:
            return self._min  # exact, avoids float accumulation drift
        return self._sum / self._n

    def future_stage_sizes(self):
        """Yield ``(stage, size)`` pairs from the next unchecked stage on."""
        stage = self._stage
        size = self._next_size
        while True:
            yield stage, size
            stage += 1
            size = max(size + 1, math.ceil(size * self._growth))

    @property
    def all_equal(self) -> bool:
        return self._n > 0 and self._min == self._max

    def add(self, value: float) -> None:
        if not (self.low <= value <= self.high):
            raise ValueError(
                f"sample {value!r} outside declared bounds [{self.low}, {self.high}]"
            )
        self._n += 1
        self._sum += value
        if value < self._min:
            self._min = value
        if value > self._max:
            self._max = value

    def current_radius(self) -> float:
        """Radius a check at the current ``n`` and stage would use.

        The stage budget is split evenly between the Hoeffding bound and the
        constant-observation bound, so combining them costs one stage_delta.
        """
        if self._n == 0:
            return trivial_radius(self.low, self.high)
        half = stage_delta(self.delta, self._stage) / 2.0
        radius = hoeffding_radius(self._n, self.low, self.high, half)
        if self.all_equal:
            radius = min(
                radius, constant_observation_radius(self._n, self.low, self.high, half)
            )
        return min(radius, trivial_radius(self.low, self.high))

    def check(self, *, force: bool = False) -> StageRecord:
        """Check the stopping rule once; consumes the current stage's budget."""
        if self._n == 0:
            raise RuntimeError("cannot check with zero samples")
        if not force and self._n != self._next_size:
            raise RuntimeError(
                f"check allowed only at pre-declared n == {self._next_size}, got {self._n}"
            )
        radius = self.current_radius()
        record = StageRecord(
            stage=self._stage,
            n=self._n,
            mean=self.mean,
            radius=radius,
            stage_delta=stage_delta(self.delta, self._stage),
            epsilon=self.epsilon,
            target_met=radius <= self.epsilon,
            all_equal=self.all_equal,
            forced=force,
        )
        self.records.append(record)
        if record.target_met:
            self.converged = True
        else:
            self._stage += 1
            self._next_size = next(self._sizes)
        return record


@dataclass
class EstimateResult:
    estimate: float
    estimate_kind: ConclusionKind
    radius: float
    guarantee_kind: ConclusionKind
    epsilon: float
    delta: float
    n: int
    status: Status
    converged: bool
    target_met: bool
    seed: int | None
    evidence: tuple[StageRecord, ...]


def estimate_mean(
    *,
    sampler: Sampler,
    low: float,
    high: float,
    epsilon: float,
    delta: float,
    max_samples: int,
    rng: random.Random | None = None,
    seed: int | None = None,
    initial_stage: int = 8,
    growth: float = 2.0,
) -> EstimateResult:
    """Estimate E[X] for X in [low, high] within epsilon, or report failure.

    The random source is injected by the caller via ``rng`` (created from
    ``seed`` when omitted); both the seed and the number of samples drawn
    are recorded in the result.  Check sizes are ``min(n_k, max_samples)``,
    which is fixed a priori, so the final forced check at the budget cap is
    still covered by the stage union bound.
    """
    check_positive_int(max_samples, "max_samples")
    if rng is None:
        rng = random.Random(seed)
    tracker = StagedTracker(
        low=low,
        high=high,
        epsilon=epsilon,
        delta=delta,
        initial_stage=initial_stage,
        growth=growth,
    )
    while not tracker.converged:
        target = min(tracker.next_stage_size, max_samples)
        while tracker.n < target:
            tracker.add(sampler(rng))
        tracker.check(force=tracker.n < tracker.next_stage_size)
        if tracker.converged or tracker.n >= max_samples:
            break
    last = tracker.records[-1]
    status = Status.CONVERGED if tracker.converged else Status.BUDGET_EXHAUSTED
    return EstimateResult(
        estimate=last.mean,
        estimate_kind=ConclusionKind.EMPIRICAL,
        radius=last.radius,
        guarantee_kind=ConclusionKind.EXACT,
        epsilon=epsilon,
        delta=delta,
        n=tracker.n,
        status=status,
        converged=tracker.converged,
        target_met=tracker.converged,
        seed=seed,
        evidence=tuple(tracker.records),
    )
