"""Canonical sets represented as finite unions of half-open intervals.

The element type is pluggable through :class:`Domain`.  Intervals are
half-open ``[lo, hi)``.  Unbounded ends are the sentinels :data:`NEG` and
:data:`POS`.  A canonical interval set is a tuple of disjoint, non-empty,
sorted intervals in which two intervals that touch (``a.hi == b.lo``) are
merged.  On a discrete ordered domain every set of sets/intervals has such a
finite form, so the infinite version universe never has to be enumerated.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable, Iterable

Interval = tuple[Any, Any]


class _Sentinel:
    __slots__ = ("name",)

    def __init__(self, name: str):
        self.name = name

    def __repr__(self) -> str:
        return self.name


NEG = _Sentinel("NEG_INF")
POS = _Sentinel("POS_INF")


@dataclass(frozen=True)
class Domain:
    name: str
    succ: Callable[[Any], Any]

    def successor(self, value: Any) -> Any:
        return self.succ(value)


def is_neg(x: Any) -> bool:
    return x is NEG


def is_pos(x: Any) -> bool:
    return x is POS


def lower_lt(a: Any, b: Any) -> bool:
    if a is NEG:
        return b is not NEG
    if a is POS:
        return False
    if b is NEG:
        return False
    if b is POS:
        return True
    return a < b


def _nonempty(lo: Any, hi: Any) -> bool:
    if lo is NEG and hi is NEG:
        return False
    if lo is POS:
        return False
    if hi is NEG:
        return False
    if lo is NEG:
        return True
    if hi is POS:
        return True
    return lo < hi


@dataclass(frozen=True)
class IntervalSet:
    domain: Domain
    pieces: tuple[Interval, ...] = ()

    def __post_init__(self) -> None:
        cleaned = self._canonical(self.pieces)
        object.__setattr__(self, "pieces", cleaned)

    @staticmethod
    def _canonical(raw: Iterable[Interval]) -> tuple[Interval, ...]:
        ordered = sorted(
            ((lo, hi) for lo, hi in raw if _nonempty(lo, hi)),
            key=lambda iv: _sort_key(iv[0]),
        )
        merged: list[Interval] = []
        for lo, hi in ordered:
            if merged and not lower_lt(merged[-1][1], lo):
                prev_lo, prev_hi = merged[-1]
                merged[-1] = (prev_lo, hi if lower_lt(prev_hi, hi) else prev_hi)
            else:
                merged.append((lo, hi))
        return tuple(merged)

    @classmethod
    def empty(cls, domain: Domain) -> "IntervalSet":
        return cls(domain, ())

    @classmethod
    def make(cls, domain: Domain, pieces: Iterable[Interval]) -> "IntervalSet":
        return cls(domain, tuple(pieces))

    @classmethod
    def universe(cls, domain: Domain) -> "IntervalSet":
        return cls(domain, ((NEG, POS),))

    @property
    def is_empty(self) -> bool:
        return not self.pieces

    def contains(self, value: Any) -> bool:
        for lo, hi in self.pieces:
            if lower_lt(value, lo):
                return False
            if (lo is NEG or lo <= value) and lower_lt(value, hi):
                return True
        return False

    def __contains__(self, value: Any) -> bool:
        return self.contains(value)

    def _check_domain(self, other: "IntervalSet") -> None:
        if self.domain.name != other.domain.name:
            raise TypeError(f"domain mismatch: {self.domain.name} vs {other.domain.name}")

    def __or__(self, other: "IntervalSet") -> "IntervalSet":
        self._check_domain(other)
        return IntervalSet(self.domain, self.pieces + other.pieces)

    def __and__(self, other: "IntervalSet") -> "IntervalSet":
        self._check_domain(other)
        i = j = 0
        out: list[Interval] = []
        a, b = self.pieces, other.pieces
        while i < len(a) and j < len(b):
            lo, hi = a[i]
            olo, ohi = b[j]
            nlo = lo if lower_lt(olo, lo) else olo
            nhi = hi if lower_lt(hi, ohi) else ohi
            if _nonempty(nlo, nhi):
                out.append((nlo, nhi))
            if lower_lt(hi, ohi):
                i += 1
            elif lower_lt(ohi, hi):
                j += 1
            else:
                i += 1
                j += 1
        return IntervalSet(self.domain, out)

    def __sub__(self, other: "IntervalSet") -> "IntervalSet":
        self._check_domain(other)
        pieces: list[Interval] = list(self.pieces)
        for blo, bhi in other.pieces:
            nxt: list[Interval] = []
            for lo, hi in pieces:
                if not lower_lt(lo, bhi) or not lower_lt(blo, hi):
                    nxt.append((lo, hi))
                    continue
                if lower_lt(lo, blo):
                    nxt.append((lo, blo))
                if lower_lt(bhi, hi):
                    nxt.append((bhi, hi))
            pieces = nxt
        return IntervalSet(self.domain, pieces)

    def min_absent(self, anchor: Any) -> Any:
        """Smallest value >= ``anchor`` not contained in this set.

        Because the universe has a least element but no greatest one, a value
        always exists: after the last finite interval end the successor is
        absent.
        """
        if anchor is NEG or anchor is POS:
            raise ValueError("anchor must be a concrete domain value")
        cursor = anchor
        for lo, hi in self.pieces:
            if lower_lt(cursor, lo):
                return cursor
            if not lower_lt(cursor, hi):
                continue
            if hi is POS:
                raise ValueError("no absent value above the unbounded interval")
            return hi
        return cursor

    def min_included(self, anchor: Any) -> Any:
        """Smallest value >= ``anchor`` contained in this set, or ``None``."""
        if anchor is NEG or anchor is POS:
            raise ValueError("anchor must be a concrete domain value")
        for lo, hi in self.pieces:
            if lo is NEG:
                start = anchor
            else:
                start = anchor if lower_lt(lo, anchor) else lo
            if lower_lt(start, hi):
                return start
        return None

    def serialize(self) -> str:
        if not self.pieces:
            return "{}"
        parts = []
        for lo, hi in self.pieces:
            lo_s = "*" if lo is NEG else str(lo)
            hi_s = "*" if hi is POS else str(hi)
            parts.append(f"[{lo_s},{hi_s})")
        return "|".join(parts)


def _sort_key(x: Any):
    if x is NEG:
        return (0,)
    if x is POS:
        return (2,)
    return (1, x)
