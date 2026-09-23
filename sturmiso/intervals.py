"""Exact rational intervals, possibly unbounded, with open/closed endpoints."""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


def _fmt_bound(x) -> str:
    if x is None:
        return "-inf"
    return str(x)


def _fmt_bound_upper(x) -> str:
    if x is None:
        return "+inf"
    return str(x)


@dataclass(frozen=True)
class Interval:
    """Interval with rational endpoints; None means unbounded (-inf / +inf).

    A point interval {r} is lower == upper == r with both endpoints closed.
    """

    lower: Fraction | None
    upper: Fraction | None
    lower_closed: bool = False
    upper_closed: bool = False

    def __post_init__(self):
        if self.lower is None and self.lower_closed:
            raise ValueError("unbounded lower endpoint cannot be closed")
        if self.upper is None and self.upper_closed:
            raise ValueError("unbounded upper endpoint cannot be closed")
        if self.lower is not None and self.upper is not None:
            if self.lower > self.upper:
                raise ValueError("empty interval: lower > upper")
            if self.lower == self.upper and not (
                self.lower_closed and self.upper_closed
            ):
                raise ValueError("degenerate open interval is empty")

    def is_point(self) -> bool:
        return (
            self.lower is not None
            and self.upper is not None
            and self.lower == self.upper
        )

    def contains(self, x: Fraction) -> bool:
        if self.lower is not None:
            if x < self.lower or (x == self.lower and not self.lower_closed):
                return False
        if self.upper is not None:
            if x > self.upper or (x == self.upper and not self.upper_closed):
                return False
        return True

    def contains_interval(self, other: "Interval") -> bool:
        """True iff other is a subset of self (exact endpoint logic)."""
        if self.lower is not None:
            if other.lower is None or other.lower < self.lower:
                return False
            if (
                other.lower == self.lower
                and not self.lower_closed
                and other.lower_closed
            ):
                return False
        if self.upper is not None:
            if other.upper is None or other.upper > self.upper:
                return False
            if (
                other.upper == self.upper
                and not self.upper_closed
                and other.upper_closed
            ):
                return False
        return True

    def width(self):
        if self.lower is None or self.upper is None:
            return None
        return self.upper - self.lower

    def intersect(self, other: "Interval"):
        """Exact intersection; returns None when empty."""
        # max of lowers
        if self.lower is None:
            lo, lo_closed = other.lower, other.lower_closed
        elif other.lower is None:
            lo, lo_closed = self.lower, self.lower_closed
        elif self.lower > other.lower:
            lo, lo_closed = self.lower, self.lower_closed
        elif other.lower > self.lower:
            lo, lo_closed = other.lower, other.lower_closed
        else:
            lo = self.lower
            lo_closed = self.lower_closed and other.lower_closed
        # min of uppers
        if self.upper is None:
            hi, hi_closed = other.upper, other.upper_closed
        elif other.upper is None:
            hi, hi_closed = self.upper, self.upper_closed
        elif self.upper < other.upper:
            hi, hi_closed = self.upper, self.upper_closed
        elif other.upper < self.upper:
            hi, hi_closed = other.upper, other.upper_closed
        else:
            hi = self.upper
            hi_closed = self.upper_closed and other.upper_closed
        if lo is not None and hi is not None:
            if lo > hi:
                return None
            if lo == hi and not (lo_closed and hi_closed):
                return None
        return Interval(lo, hi, lo_closed, hi_closed)

    def __str__(self) -> str:
        if self.is_point():
            return "{" + str(self.lower) + "}"
        left = "[" if self.lower_closed else "("
        right = "]" if self.upper_closed else ")"
        return f"{left}{_fmt_bound(self.lower)}, {_fmt_bound_upper(self.upper)}{right}"
