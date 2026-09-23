"""Intervals with rational endpoints and explicit open/closed flags.

Openness is preserved exactly through intersection and combination; no
endpoint is ever silently included or dropped.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


def _f(value):
    return None if value is None else Fraction(value)


@dataclass(frozen=True)
class Interval:
    lo: Fraction | None   # None means -infinity
    hi: Fraction | None   # None means +infinity
    lo_closed: bool = False
    hi_closed: bool = False

    def __post_init__(self):
        lo = None if self.lo is None else Fraction(self.lo)
        hi = None if self.hi is None else Fraction(self.hi)
        object.__setattr__(self, "lo", lo)
        object.__setattr__(self, "hi", hi)
        if lo is not None and hi is not None and lo > hi:
            raise ValueError(f"empty interval: {lo} > {hi}")
        if lo is not None and hi is not None and lo == hi:
            if not (self.lo_closed and self.hi_closed):
                raise ValueError("degenerate interval must be closed on both sides")

    @staticmethod
    def closed(lo, hi) -> "Interval":
        return Interval(_f(lo), _f(hi), True, True)

    @staticmethod
    def open(lo, hi) -> "Interval":
        return Interval(_f(lo), _f(hi), False, False)

    @staticmethod
    def open_closed(lo, hi) -> "Interval":
        return Interval(_f(lo), _f(hi), False, True)

    @staticmethod
    def closed_open(lo, hi) -> "Interval":
        return Interval(_f(lo), _f(hi), True, False)

    @staticmethod
    def point(value) -> "Interval":
        return Interval(_f(value), _f(value), True, True)

    @staticmethod
    def all() -> "Interval":
        return Interval(None, None, False, False)

    def is_point(self) -> bool:
        return self.lo is not None and self.lo == self.hi

    def contains(self, value) -> bool:
        value = Fraction(value)
        if self.lo is not None:
            if value < self.lo or (value == self.lo and not self.lo_closed):
                return False
        if self.hi is not None:
            if value > self.hi or (value == self.hi and not self.hi_closed):
                return False
        return True

    def intersect(self, other: "Interval") -> "Interval | None":
        """Intersection; openness of shared endpoints is preserved."""
        lo, lo_closed = self.lo, self.lo_closed
        if other.lo is not None and (lo is None or other.lo > lo):
            lo, lo_closed = other.lo, other.lo_closed
        elif other.lo is not None and other.lo == lo:
            lo_closed = lo_closed and other.lo_closed
        hi, hi_closed = self.hi, self.hi_closed
        if other.hi is not None and (hi is None or other.hi < hi):
            hi, hi_closed = other.hi, other.hi_closed
        elif other.hi is not None and other.hi == hi:
            hi_closed = hi_closed and other.hi_closed
        if lo is not None and hi is not None:
            if lo > hi or (lo == hi and not (lo_closed and hi_closed)):
                return None
        return Interval(lo, hi, lo_closed, hi_closed)

    def touches(self, other: "Interval") -> bool:
        """True if the two intervals share exactly one endpoint."""
        if self.hi is not None and other.lo is not None and self.hi == other.lo:
            return True
        if other.hi is not None and self.lo is not None and other.hi == self.lo:
            return True
        return False

    def to_human(self) -> str:
        lo_s = "-inf" if self.lo is None else str(self.lo)
        hi_s = "+inf" if self.hi is None else str(self.hi)
        left = "[" if self.lo_closed else "("
        right = "]" if self.hi_closed else ")"
        return f"{left}{lo_s}, {hi_s}{right}"

    def __repr__(self) -> str:
        return f"Interval{self.to_human()}"
