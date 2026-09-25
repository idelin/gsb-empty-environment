"""Closed real-interval helpers shared by the interpolation engine."""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Interval:
    """A closed interval [lo, hi]; a point interval has lo == hi."""

    lo: float
    hi: float

    def __post_init__(self) -> None:
        if self.lo > self.hi:
            raise ValueError(f"empty interval: [{self.lo!r}, {self.hi!r}]")

    @classmethod
    def point(cls, value: float) -> "Interval":
        return cls(value, value)

    @property
    def width(self) -> float:
        return self.hi - self.lo

    @property
    def is_point(self) -> bool:
        return self.lo == self.hi

    @property
    def mid(self) -> float:
        return 0.5 * (self.lo + self.hi)

    def contains(self, value: float, tol: float = 0.0) -> bool:
        return self.lo - tol <= value <= self.hi + tol

    def contains_interval(self, other: "Interval", tol: float = 0.0) -> bool:
        return self.lo - tol <= other.lo and other.hi <= self.hi + tol

    def intersects(self, other: "Interval") -> bool:
        return self.lo <= other.hi and other.lo <= self.hi

    def intersection(self, other: "Interval") -> "Interval | None":
        lo = max(self.lo, other.lo)
        hi = min(self.hi, other.hi)
        return Interval(lo, hi) if lo <= hi else None

    def hull(self, other: "Interval") -> "Interval":
        return Interval(min(self.lo, other.lo), max(self.hi, other.hi))

    def __repr__(self) -> str:
        return f"[{self.lo:.6g}, {self.hi:.6g}]"
