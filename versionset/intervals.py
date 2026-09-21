"""Intervals over the version domain with open/closed endpoints.

Endpoints may be ``None`` (unbounded).  Emptiness is decided with the
successor function, so no assumption about a dense domain is made and the
infinite domain is never enumerated.
"""

from __future__ import annotations

from dataclasses import dataclass

from .version import Version


@dataclass(frozen=True)
class Interval:
    low: Version | None
    low_closed: bool
    high: Version | None
    high_closed: bool

    # -- predicates -----------------------------------------------------
    def is_empty(self) -> bool:
        if self.low is None or self.high is None:
            return False
        lo = self.low if self.low_closed else self.low.successor()
        if self.high_closed:
            return lo > self.high
        return lo >= self.high

    def contains(self, v: Version) -> bool:
        if self.low is not None:
            if v < self.low or (v == self.low and not self.low_closed):
                return False
        if self.high is not None:
            if v > self.high or (v == self.high and not self.high_closed):
                return False
        return True

    # -- algebra ----------------------------------------------------------
    def intersect(self, other: "Interval") -> "Interval | None":
        if self.low is None or (other.low is not None and other.low > self.low):
            low, low_closed = other.low, other.low_closed
        elif other.low is None or self.low > other.low:
            low, low_closed = self.low, self.low_closed
        else:  # equal lows
            low = self.low
            low_closed = self.low_closed and other.low_closed
        if self.high is None or (other.high is not None and other.high < self.high):
            high, high_closed = other.high, other.high_closed
        elif other.high is None or self.high < other.high:
            high, high_closed = self.high, self.high_closed
        else:
            high = self.high
            high_closed = self.high_closed and other.high_closed
        iv = Interval(low, low_closed, high, high_closed)
        return None if iv.is_empty() else iv

    def touches(self, other: "Interval") -> bool:
        """True if the two can be merged into one interval."""
        if self.high is None or other.low is None:
            return True  # one of them is unbounded towards the other
        if self.high > other.low:
            return True
        if self.high == other.low:
            return self.high_closed or other.low_closed
        return False

    def merge(self, other: "Interval") -> "Interval":
        """Union of two touching intervals."""
        if self.low is None or (other.low is not None and other.low < self.low):
            low, low_closed = other.low, other.low_closed
        elif other.low is None or self.low < other.low:
            low, low_closed = self.low, self.low_closed
        else:
            low = self.low
            low_closed = self.low_closed or other.low_closed
        if self.high is None or (other.high is not None and other.high > self.high):
            high, high_closed = other.high, other.high_closed
        elif other.high is None or self.high > other.high:
            high, high_closed = self.high, self.high_closed
        else:
            high = self.high
            high_closed = self.high_closed or other.high_closed
        return Interval(low, low_closed, high, high_closed)

    def difference(self, other: "Interval") -> list["Interval"]:
        """self minus other: zero, one or two intervals."""
        inter = self.intersect(other)
        if inter is None:
            return [self]
        out = []
        if inter.low is not None:
            left = Interval(self.low, self.low_closed,
                            inter.low, not inter.low_closed)
            if not left.is_empty():
                out.append(left)
        if inter.high is not None:
            right = Interval(inter.high, not inter.high_closed,
                             self.high, self.high_closed)
            if not right.is_empty():
                out.append(right)
        return out

    def __str__(self) -> str:
        lo = "-inf" if self.low is None else str(self.low)
        hi = "+inf" if self.high is None else str(self.high)
        return f"{'[' if self.low_closed else '('}{lo}, {hi}{']' if self.high_closed else ')'}"


def normalize_intervals(intervals) -> tuple[Interval, ...]:
    """Sort, drop empties and merge touching intervals."""
    items = sorted(
        (iv for iv in intervals if not iv.is_empty()),
        key=lambda iv: (iv.low is not None, iv.low, not iv.low_closed),
    )
    merged: list[Interval] = []
    for iv in items:
        if merged and merged[-1].touches(iv):
            merged[-1] = merged[-1].merge(iv)
        else:
            merged.append(iv)
    return tuple(merged)


def subtract_intervals(intervals, cutters) -> list[Interval]:
    """Subtract a list of intervals from a list of intervals."""
    pieces = list(intervals)
    for cutter in cutters:
        nxt = []
        for piece in pieces:
            nxt.extend(piece.difference(cutter))
        pieces = nxt
    return pieces
