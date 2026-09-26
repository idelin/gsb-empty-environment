"""Exact rational intervals and interval sets with open/closed endpoints.

Endpoint openness is first-class: it is never dropped by intersection,
union or affine transforms.  Sets are kept normalised (sorted, disjoint,
non-mergeable) so results are canonical and breakpoint-based -- a set
covering [0, 10**15] is still a handful of endpoints, never ticks.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Iterable, Iterator

from .types import exact


@dataclass(frozen=True)
class Interval:
    lo: Fraction
    hi: Fraction
    lo_open: bool = False
    hi_open: bool = True

    def __post_init__(self) -> None:
        object.__setattr__(self, "lo", exact(self.lo))
        object.__setattr__(self, "hi", exact(self.hi))
        if self.lo > self.hi:
            raise ValueError(f"interval lo > hi: {self.lo} > {self.hi}")

    @classmethod
    def point(cls, x) -> "Interval":
        return cls(exact(x), exact(x), False, False)

    @property
    def is_empty(self) -> bool:
        return self.lo == self.hi and (self.lo_open or self.hi_open)

    @property
    def is_point(self) -> bool:
        return self.lo == self.hi and not self.is_empty

    def contains(self, x) -> bool:
        x = exact(x)
        if x < self.lo or x > self.hi:
            return False
        if x == self.lo and self.lo_open:
            return False
        if x == self.hi and self.hi_open:
            return False
        return True

    def intersect(self, other: "Interval") -> "Interval | None":
        if self.lo < other.lo:
            lo, lo_open = other.lo, other.lo_open
        elif self.lo > other.lo:
            lo, lo_open = self.lo, self.lo_open
        else:
            lo, lo_open = self.lo, self.lo_open and other.lo_open
        if self.hi < other.hi:
            hi, hi_open = self.hi, self.hi_open
        elif self.hi > other.hi:
            hi, hi_open = other.hi, other.hi_open
        else:
            hi, hi_open = self.hi, self.hi_open and other.hi_open
        if lo > hi:
            return None
        iv = Interval(lo, hi, lo_open, hi_open)
        return None if iv.is_empty else iv

    def touches(self, other: "Interval") -> bool:
        """True when self.hi == other.lo and the shared point lies in either side."""
        return self.hi == other.lo and (not self.hi_open or not other.lo_open)

    def __str__(self) -> str:
        if self.is_point:
            return f"{{{self.lo}}}"
        left = "(" if self.lo_open else "["
        right = ")" if self.hi_open else "]"
        return f"{left}{self.lo}, {self.hi}{right}"

    __repr__ = __str__


def _merge_pair(a: Interval, b: Interval) -> Interval:
    """Merge overlapping or touching intervals; requires a.lo <= b.lo."""
    if b.hi < a.hi:
        return a
    if b.hi == a.hi:
        return Interval(a.lo, a.hi, a.lo_open, a.hi_open and b.hi_open)
    return Interval(a.lo, b.hi, a.lo_open, b.hi_open)


class IntervalSet:
    """A normalised (sorted, disjoint, non-mergeable) set of intervals."""

    def __init__(self, intervals: Iterable[Interval] = ()):
        ordered = sorted(
            (iv for iv in intervals if not iv.is_empty),
            key=lambda i: (i.lo, i.lo_open),
        )
        merged: list[Interval] = []
        for iv in ordered:
            if merged:
                last = merged[-1]
                if iv.lo < last.hi or last.touches(iv):
                    merged[-1] = _merge_pair(last, iv)
                    continue
            merged.append(iv)
        self._intervals = tuple(merged)

    @property
    def intervals(self) -> tuple[Interval, ...]:
        return self._intervals

    def __iter__(self) -> Iterator[Interval]:
        return iter(self._intervals)

    def __len__(self) -> int:
        return len(self._intervals)

    def __contains__(self, x) -> bool:
        return any(iv.contains(x) for iv in self._intervals)

    def __eq__(self, other) -> bool:
        if isinstance(other, IntervalSet):
            return self._intervals == other._intervals
        return NotImplemented

    def __hash__(self) -> int:
        return hash(self._intervals)

    def __str__(self) -> str:
        return " U ".join(str(iv) for iv in self._intervals) if self._intervals else "(empty)"

    __repr__ = __str__
