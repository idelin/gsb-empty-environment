"""Pre-declared error spending for repeated looks at the simulation CI.

When the simulation is allowed to stop early ("adaptive sample
allocation"), each look at the data consumes part of a total error
budget. The schedule is fixed *before* any random numbers are drawn, so
the stopping rule never depends on unplanned peeking.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class LookPlan:
    """A pre-declared schedule of looks and per-look error budgets.

    looks: cumulative trial counts at which the CI may be inspected.
    errors: error budget spent at each look; sum(errors) == total_alpha.
            The CI reported at look k has confidence 1 - errors[k].
    """

    looks: tuple[int, ...]
    errors: tuple[float, ...]
    total_alpha: float

    def __post_init__(self) -> None:
        if not self.looks:
            raise ValueError("look plan must contain at least one look")
        if len(self.looks) != len(self.errors):
            raise ValueError("looks and errors must have equal length")
        if any(t <= 0 for t in self.looks):
            raise ValueError("look trial counts must be positive")
        if tuple(sorted(self.looks)) != self.looks:
            raise ValueError("looks must be non-decreasing")
        if any(e <= 0 for e in self.errors):
            raise ValueError("per-look error budgets must be positive")
        if abs(sum(self.errors) - self.total_alpha) > 1e-9:
            raise ValueError("error budgets must sum to total_alpha")


def plan_looks(max_trials: int, n_looks: int, total_alpha: float = 0.05,
               rho: float = 2.0) -> LookPlan:
    """Build a LookPlan with evenly spaced looks and a t**rho spending family.

    The error spent by cumulative information fraction t is
    total_alpha * t**rho (rho = 2 is conservative early, O'Brien-Fleming
    style); per-look budgets are the increments.
    """
    if max_trials < 1:
        raise ValueError("max_trials must be >= 1")
    if n_looks < 1:
        raise ValueError("n_looks must be >= 1")
    if not 0.0 < total_alpha < 1.0:
        raise ValueError("total_alpha must be in (0, 1)")
    n_looks = min(n_looks, max_trials)
    looks = tuple(
        max(1, round(max_trials * (k + 1) / n_looks)) for k in range(n_looks)
    )
    looks = tuple(dict.fromkeys(looks))  # de-duplicate, keep order
    spent_prev = 0.0
    errors: list[float] = []
    for k in range(len(looks)):
        t = (k + 1) / len(looks)
        spent = total_alpha * t**rho
        errors.append(spent - spent_prev)
        spent_prev = spent
    errors[-1] = total_alpha - sum(errors[:-1])  # exact budget closure
    return LookPlan(looks=looks, errors=tuple(errors), total_alpha=total_alpha)
