"""Exact sets of real numbers as finite unions of open/closed rational intervals.

All coordinates are ``fractions.Fraction`` (ints are promoted).  Floats are
rejected: topology (connectedness, emptiness, isolated points) is decided by
exact rational comparison, never by epsilon tolerances.

An :class:`IntervalSet` is kept normalized: intervals are sorted, disjoint,
and non-adjacent (two intervals that touch at a point covered by at least one
of them are merged, so ``[0,1] | (1,2]`` normalizes to the single component
``[0,2]``).
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Iterable, Iterator, Optional, Tuple, Union

Number = Union[int, Fraction]

__all__ = ["Number", "to_fraction", "Interval", "ivl", "IntervalSet"]


def to_fraction(value: Number) -> Fraction:
    """Promote an int/Fraction to Fraction; reject anything inexact."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("booleans are not valid coordinates")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        raise TypeError(
            "float coordinates are forbidden; use int or Fraction for exactness"
        )
    raise TypeError(f"unsupported coordinate type: {type(value)!r}")


@dataclass(frozen=True)
class Interval:
    """A single non-empty interval.  ``None`` endpoints mean -inf / +inf."""

    lo: Optional[Fraction]
    lo_closed: bool
    hi: Optional[Fraction]
    hi_closed: bool

    def __post_init__(self) -> None:
        if self.lo is None and self.lo_closed:
            object.__setattr__(self, "lo_closed", False)
        if self.hi is None and self.hi_closed:
            object.__setattr__(self, "hi_closed", False)
        if self.lo is not None and self.hi is not None:
            if self.lo > self.hi:
                raise ValueError(f"empty interval: {self.lo} > {self.hi}")
            if self.lo == self.hi and not (self.lo_closed and self.hi_closed):
                raise ValueError(f"empty interval: open at single point {self.lo}")

    @property
    def is_point(self) -> bool:
        return (
            self.lo is not None
            and self.hi is not None
            and self.lo == self.hi
        )

    def contains(self, x: Fraction) -> bool:
        if self.lo is not None:
            if x < self.lo or (x == self.lo and not self.lo_closed):
                return False
        if self.hi is not None:
            if x > self.hi or (x == self.hi and not self.hi_closed):
                return False
        return True

    def __str__(self) -> str:
        if self.is_point:
            return f"{{{self.lo}}}"
        lo = "-inf" if self.lo is None else str(self.lo)
        hi = "+inf" if self.hi is None else str(self.hi)
        return f"{'[' if self.lo_closed else '('}{lo}, {hi}{']' if self.hi_closed else ')'}"


def ivl(
    lo: Optional[Number],
    hi: Optional[Number],
    lo_closed: bool = True,
    hi_closed: bool = True,
) -> Interval:
    """Convenience constructor promoting int/Fraction coordinates."""
    return Interval(
        None if lo is None else to_fraction(lo),
        lo_closed,
        None if hi is None else to_fraction(hi),
        hi_closed,
    )


def point(x: Number) -> Interval:
    """The degenerate closed interval {x}."""
    return ivl(x, x, True, True)


def _mk(
    lo: Optional[Fraction], lo_closed: bool,
    hi: Optional[Fraction], hi_closed: bool,
) -> Optional[Interval]:
    """Build an Interval, returning None if it would be empty."""
    if lo is not None and hi is not None:
        if lo > hi or (lo == hi and not (lo_closed and hi_closed)):
            return None
    return Interval(lo, lo_closed, hi, hi_closed)


def _lo_key(iv: Interval):
    # -inf sorts before everything; at equal lo, closed sorts before open.
    return (iv.lo is not None, iv.lo, not iv.lo_closed)


def _max_lo(a_lo, a_c, b_lo, b_c):
    if a_lo is None:
        return b_lo, b_c
    if b_lo is None:
        return a_lo, a_c
    if a_lo > b_lo:
        return a_lo, a_c
    if b_lo > a_lo:
        return b_lo, b_c
    return a_lo, a_c and b_c


def _min_hi(a_hi, a_c, b_hi, b_c):
    if a_hi is None:
        return b_hi, b_c
    if b_hi is None:
        return a_hi, a_c
    if a_hi < b_hi:
        return a_hi, a_c
    if b_hi < a_hi:
        return b_hi, b_c
    return a_hi, a_c and b_c


def _normalize(intervals: Iterable[Interval]) -> Tuple[Interval, ...]:
    ordered = sorted(intervals, key=_lo_key)
    out: list[Interval] = []
    for iv in ordered:
        if not out:
            out.append(iv)
            continue
        cur = out[-1]
        touches = False
        if cur.hi is None:
            touches = True
        elif iv.lo is not None:
            if iv.lo < cur.hi:
                touches = True
            elif iv.lo == cur.hi and (cur.hi_closed or iv.lo_closed):
                touches = True
        if touches:
            hi, hi_closed = cur.hi, cur.hi_closed
            if iv.hi is None:
                hi, hi_closed = None, False
            elif cur.hi is not None:
                if iv.hi > cur.hi:
                    hi, hi_closed = iv.hi, iv.hi_closed
                elif iv.hi == cur.hi:
                    hi_closed = cur.hi_closed or iv.hi_closed
            out[-1] = Interval(cur.lo, cur.lo_closed, hi, hi_closed)
        else:
            out.append(iv)
    return tuple(out)


class IntervalSet:
    """Immutable normalized finite union of intervals."""

    __slots__ = ("_iv",)

    def __init__(self, intervals: Iterable[Interval] = ()):
        self._iv: Tuple[Interval, ...] = _normalize(tuple(intervals))

    # -- basic queries ----------------------------------------------------
    @property
    def intervals(self) -> Tuple[Interval, ...]:
        return self._iv

    def is_empty(self) -> bool:
        return not self._iv

    def is_connected(self) -> bool:
        """Exactly one component (the empty set is not connected)."""
        return len(self._iv) == 1

    def components(self) -> Tuple[Interval, ...]:
        return self._iv

    def isolated_points(self) -> Tuple[Fraction, ...]:
        return tuple(iv.lo for iv in self._iv if iv.is_point)

    def contains(self, x: Number) -> bool:
        x = to_fraction(x)
        for iv in self._iv:
            if iv.lo is not None and x < iv.lo:
                return False
            if iv.contains(x):
                return True
        return False

    # -- boolean algebra --------------------------------------------------
    def union(self, *others: "IntervalSet") -> "IntervalSet":
        ivs = list(self._iv)
        for other in others:
            ivs.extend(other._iv)
        return IntervalSet(ivs)

    def intersect(self, other: "IntervalSet") -> "IntervalSet":
        res = []
        i = j = 0
        a, b = self._iv, other._iv
        while i < len(a) and j < len(b):
            lo, lo_c = _max_lo(a[i].lo, a[i].lo_closed, b[j].lo, b[j].lo_closed)
            hi, hi_c = _min_hi(a[i].hi, a[i].hi_closed, b[j].hi, b[j].hi_closed)
            iv = _mk(lo, lo_c, hi, hi_c)
            if iv is not None:
                res.append(iv)
            if a[i].hi is None:  # a reaches +inf: b ends first
                j += 1
            elif b[j].hi is None:
                i += 1
            elif a[i].hi < b[j].hi:
                i += 1
            elif b[j].hi < a[i].hi:
                j += 1
            else:
                i += 1
                j += 1
        return IntervalSet(res)

    def complement(self) -> "IntervalSet":
        if not self._iv:
            return IntervalSet([Interval(None, False, None, False)])
        res = []
        cur_lo, cur_lo_c = None, False  # open end starting at -inf
        for iv in self._iv:
            if iv.lo is not None:
                gap = _mk(cur_lo, cur_lo_c, iv.lo, not iv.lo_closed)
                if gap is not None:
                    res.append(gap)
            cur_lo, cur_lo_c = iv.hi, not iv.hi_closed
        if cur_lo is not None:
            res.append(Interval(cur_lo, cur_lo_c, None, False))
        return IntervalSet(res)

    def subtract(self, other: "IntervalSet") -> "IntervalSet":
        return self.intersect(other.complement())

    # -- dunder -----------------------------------------------------------
    def __eq__(self, other) -> bool:
        return isinstance(other, IntervalSet) and self._iv == other._iv

    def __hash__(self) -> int:
        return hash(self._iv)

    def __iter__(self) -> Iterator[Interval]:
        return iter(self._iv)

    def __len__(self) -> int:
        return len(self._iv)

    def __repr__(self) -> str:
        if not self._iv:
            return "IntervalSet(∅)"
        return "IntervalSet(" + " | ".join(str(iv) for iv in self._iv) + ")"
