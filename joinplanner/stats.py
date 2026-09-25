"""Statistics with intervals.

Statistics may be unknown; every quantity is an Interval [lo, hi].  Unknown
statistics are represented by wide intervals, and the planner is required to
return conservative (worst-case aware) choices plus fallback alternatives.
"""
from __future__ import annotations

import math
from dataclasses import dataclass, field


@dataclass(frozen=True)
class Interval:
    lo: float
    hi: float

    def __post_init__(self):
        if self.lo > self.hi:
            raise ValueError(f"empty interval [{self.lo}, {self.hi}]")

    @classmethod
    def point(cls, v: float) -> "Interval":
        return cls(float(v), float(v))

    @classmethod
    def unknown(cls, hi: float = 1e12) -> "Interval":
        return cls(1.0, float(hi))

    def mid(self) -> float:
        return math.sqrt(self.lo * self.hi)

    def pick_rows(self, mode: str) -> float:
        # Pessimistic row count = high end (bigger intermediates).
        if mode == "opt":
            return self.lo
        if mode == "pess":
            return self.hi
        return self.mid()

    def pick_ndv(self, mode: str) -> float:
        # Pessimistic NDV = low end (fewer distinct keys => bigger join output).
        if mode == "opt":
            return self.hi
        if mode == "pess":
            return self.lo
        return self.mid()


@dataclass
class RelStats:
    rows: Interval
    ndv: dict[str, Interval] = field(default_factory=dict)

    def ndv_for(self, col: str) -> Interval:
        return self.ndv.get(col, Interval.unknown())


class StatsProvider:
    """Holds the current statistics snapshot.

    `epoch` models the statistics version: a plan built against an older
    epoch is stale.  Execution checks the epoch at materialization
    boundaries and records an explicit STALE_STATS state.
    """

    def __init__(self, stats: dict[str, RelStats] | None = None, epoch: int = 0):
        self._stats = dict(stats or {})
        self.epoch = epoch

    def get(self, rel: str) -> RelStats:
        return self._stats.get(rel, RelStats(Interval.unknown()))

    def update(self, rel: str, stats: RelStats) -> None:
        self._stats[rel] = stats

    def bump_epoch(self) -> None:
        self.epoch += 1
