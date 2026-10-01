"""Connected time intervals with open/closed endpoints over exact Fractions."""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .exact import as_fraction

__all__ = ["Interval"]


@dataclass(frozen=True)
class Interval:
    """A connected interval; ``None`` bounds mean unbounded (+-infinity)."""

    start: Fraction | None = None
    end: Fraction | None = None
    closed_start: bool = True
    closed_end: bool = True

    def __post_init__(self):
        if self.start is not None:
            object.__setattr__(self, "start", as_fraction(self.start))
        if self.end is not None:
            object.__setattr__(self, "end", as_fraction(self.end))

    @classmethod
    def closed(cls, start, end) -> "Interval":
        return cls(start, end, True, True)

    @classmethod
    def open(cls, start, end) -> "Interval":
        return cls(start, end, False, False)

    @classmethod
    def closed_open(cls, start, end) -> "Interval":
        return cls(start, end, True, False)

    @classmethod
    def open_closed(cls, start, end) -> "Interval":
        return cls(start, end, False, True)

    def is_empty(self) -> bool:
        if self.start is None or self.end is None:
            return False
        if self.start > self.end:
            return True
        return self.start == self.end and not (self.closed_start and self.closed_end)

    def contains(self, t) -> bool:
        t = as_fraction(t)
        if self.start is not None:
            if t < self.start or (t == self.start and not self.closed_start):
                return False
        if self.end is not None:
            if t > self.end or (t == self.end and not self.closed_end):
                return False
        return True

    def intersect(self, other: "Interval") -> "Interval":
        start, closed_start = self._max_lower(other)
        end, closed_end = self._min_upper(other)
        return Interval(start, end, closed_start, closed_end)

    def _max_lower(self, other: "Interval"):
        if self.start is None:
            return other.start, other.closed_start
        if other.start is None:
            return self.start, self.closed_start
        if self.start > other.start:
            return self.start, self.closed_start
        if other.start > self.start:
            return other.start, other.closed_start
        return self.start, self.closed_start and other.closed_start

    def _min_upper(self, other: "Interval"):
        if self.end is None:
            return other.end, other.closed_end
        if other.end is None:
            return self.end, self.closed_end
        if self.end < other.end:
            return self.end, self.closed_end
        if other.end < self.end:
            return other.end, other.closed_end
        return self.end, self.closed_end and other.closed_end

    def difference(self, other: "Interval") -> list["Interval"]:
        """Return ``self - other`` as 0..2 disjoint intervals."""
        if self.is_empty():
            return []
        if other.is_empty():
            return [self]
        pieces = []
        if other.start is not None:
            pieces.append(Interval(self.start, other.start,
                                   self.closed_start, not other.closed_start))
        if other.end is not None:
            pieces.append(Interval(other.end, self.end,
                                   not other.closed_end, self.closed_end))
        return [p for p in pieces if not p.is_empty()]

    def __repr__(self):
        left = "[" if self.closed_start else "("
        right = "]" if self.closed_end else ")"
        start = "-inf" if self.start is None else str(self.start)
        end = "+inf" if self.end is None else str(self.end)
        return f"{left}{start}, {end}{right}"
