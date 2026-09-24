"""Interval-valued statistics: unknown statistics are wide intervals."""
from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True)
class Interval:
    lo: float
    hi: float

    def __post_init__(self):
        if self.lo < 0 or self.hi < self.lo:
            raise ValueError(f"bad interval [{self.lo}, {self.hi}]")

    @staticmethod
    def exact(v):
        return Interval(float(v), float(v))

    @staticmethod
    def unknown():
        return Interval(1.0, 1.0e15)

    @property
    def mid(self):
        return (self.lo + self.hi) / 2

    @property
    def width_ratio(self):
        return self.hi / max(self.lo, 1e-12)

    @property
    def is_wide(self):
        return self.width_ratio > 1e3

    def __add__(self, other):
        return Interval(self.lo + other.lo, self.hi + other.hi)

    def __mul__(self, other):
        return Interval(self.lo * other.lo, self.hi * other.hi)

    def scale(self, factor):
        return Interval(self.lo * factor, self.hi * factor)

    def imin(self, other):
        return Interval(min(self.lo, other.lo), min(self.hi, other.hi))

    def imax(self, other):
        return Interval(max(self.lo, other.lo), max(self.hi, other.hi))

    def contains(self, x, tol=1.0):
        return self.lo / tol <= x <= self.hi * tol


@dataclass(frozen=True)
class ColumnStat:
    ndv: Interval
    null_frac: Interval = Interval.exact(0.0)
    width: int = 8


@dataclass(frozen=True)
class RelationStat:
    rows: Interval
    columns: dict = field(default_factory=dict)

    @staticmethod
    def unknown(colnames=()):
        return RelationStat(
            Interval.unknown(),
            {c: ColumnStat(Interval.unknown(), Interval(0.0, 1.0)) for c in colnames},
        )

    def is_wide(self):
        return self.rows.is_wide or any(c.ndv.is_wide for c in self.columns.values())


class Catalog:
    """Versioned statistics catalog.

    Bumping the version (by setting a statistic) after a plan was created
    marks the plan's statistics as stale; the executor detects this.
    """

    def __init__(self):
        self._stats = {}
        self.version = 0

    def set(self, name, stat):
        self._stats[name] = stat
        self.version += 1

    def get(self, name):
        return self._stats.get(name, RelationStat.unknown())
