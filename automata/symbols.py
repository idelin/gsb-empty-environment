"""Symbol sets as sorted disjoint integer intervals.

Edges of the automata carry a :class:`SymbolSet` (a finite union of closed
integer intervals, e.g. character classes or huge ranges).  All boolean
operations and the alphabet partitioning work on interval boundaries only,
so alphabets like ``[0, 2**32)`` are never enumerated.
"""
from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from typing import Iterable, Iterator, Optional, Sequence, Tuple, Union

IntervalLike = Union["Interval", Tuple[int, int]]


@dataclass(frozen=True, order=True)
class Interval:
    """Closed integer interval ``[lo, hi]``."""

    lo: int
    hi: int

    def __post_init__(self) -> None:
        if self.lo > self.hi:
            raise ValueError(f"empty interval [{self.lo}, {self.hi}]")

    @property
    def size(self) -> int:
        return self.hi - self.lo + 1

    def contains(self, value: int) -> bool:
        return self.lo <= value <= self.hi

    def intersects(self, other: "Interval") -> bool:
        return self.lo <= other.hi and other.lo <= self.hi

    def __repr__(self) -> str:
        return f"[{self.lo}..{self.hi}]"


def _coerce(intervals: Iterable[IntervalLike]) -> list[Interval]:
    return [iv if isinstance(iv, Interval) else Interval(*iv) for iv in intervals]


def _normalize(intervals: Iterable[IntervalLike]) -> Tuple[Interval, ...]:
    """Sort and merge overlapping or adjacent intervals."""
    merged: list[Interval] = []
    for iv in sorted(_coerce(intervals)):
        if merged and iv.lo <= merged[-1].hi + 1:
            last = merged[-1]
            if iv.hi > last.hi:
                merged[-1] = Interval(last.lo, iv.hi)
        else:
            merged.append(iv)
    return tuple(merged)


class SymbolSet:
    """Immutable finite union of disjoint, non-adjacent, sorted intervals."""

    __slots__ = ("intervals",)

    def __init__(self, intervals: Iterable[IntervalLike] = ()):
        self.intervals: Tuple[Interval, ...] = _normalize(intervals)

    # -- constructors ------------------------------------------------------
    @classmethod
    def range(cls, lo: int, hi: int) -> "SymbolSet":
        return cls([Interval(lo, hi)])

    @classmethod
    def point(cls, value: int) -> "SymbolSet":
        return cls([Interval(value, value)])

    @classmethod
    def chars(cls, text: str) -> "SymbolSet":
        """Character class from individual characters, e.g. ``chars("abc_")``."""
        return cls(Interval(ord(c), ord(c)) for c in text)

    @classmethod
    def char_range(cls, first: str, last: str) -> "SymbolSet":
        return cls.range(ord(first), ord(last))

    # -- dunder ------------------------------------------------------------
    def __iter__(self) -> Iterator[Interval]:
        return iter(self.intervals)

    def __len__(self) -> int:
        return len(self.intervals)

    def __bool__(self) -> bool:
        return bool(self.intervals)

    def __eq__(self, other: object) -> bool:
        return isinstance(other, SymbolSet) and self.intervals == other.intervals

    def __hash__(self) -> int:
        return hash(self.intervals)

    def __repr__(self) -> str:
        return "SymbolSet(" + ", ".join(repr(iv) for iv in self.intervals) + ")"

    # -- queries -----------------------------------------------------------
    def is_empty(self) -> bool:
        return not self.intervals

    @property
    def size(self) -> int:
        return sum(iv.size for iv in self.intervals)

    def contains(self, value: int) -> bool:
        los = [iv.lo for iv in self.intervals]
        i = bisect_right(los, value) - 1
        return i >= 0 and self.intervals[i].hi >= value

    def intersects_interval(self, atom: Interval) -> bool:
        return any(iv.intersects(atom) for iv in self.intervals)

    # -- boolean algebra ---------------------------------------------------
    def union(self, other: "SymbolSet") -> "SymbolSet":
        return SymbolSet(self.intervals + other.intervals)

    def intersect(self, other: "SymbolSet") -> "SymbolSet":
        out: list[Interval] = []
        a, b = self.intervals, other.intervals
        i = j = 0
        while i < len(a) and j < len(b):
            lo, hi = max(a[i].lo, b[j].lo), min(a[i].hi, b[j].hi)
            if lo <= hi:
                out.append(Interval(lo, hi))
            if a[i].hi < b[j].hi:
                i += 1
            else:
                j += 1
        return SymbolSet(out)

    def difference(self, other: "SymbolSet") -> "SymbolSet":
        out: list[Interval] = []
        for iv in self.intervals:
            cur = iv.lo
            for o in other.intervals:
                if o.hi < cur:
                    continue
                if o.lo > iv.hi:
                    break
                if o.lo > cur:
                    out.append(Interval(cur, o.lo - 1))
                cur = max(cur, o.hi + 1)
                if cur > iv.hi:
                    break
            if cur <= iv.hi:
                out.append(Interval(cur, iv.hi))
        return SymbolSet(out)

    def complement(self, universe: "SymbolSet") -> "SymbolSet":
        return universe.difference(self)


def partition(symbol_sets: Iterable[SymbolSet], universe: SymbolSet) -> Tuple[Interval, ...]:
    """Atomic partition of ``universe`` induced by the boundaries of ``symbol_sets``.

    Returns sorted disjoint intervals covering ``universe`` such that every
    input symbol set is an exact union of atoms.  Only boundary points are
    materialized, never the individual symbols.
    """
    bounds: set[int] = set()
    for u in universe.intervals:
        bounds.add(u.lo)
        bounds.add(u.hi + 1)
    for ss in symbol_sets:
        for iv in ss.intervals:
            bounds.add(iv.lo)
            bounds.add(iv.hi + 1)
    pts = sorted(bounds)
    atoms: list[Interval] = []
    for lo, hi_excl in zip(pts, pts[1:]):
        atom = Interval(lo, hi_excl - 1)
        for u in universe.intervals:
            if atom.intersects(u):
                atoms.append(Interval(max(atom.lo, u.lo), min(atom.hi, u.hi)))
    return tuple(atoms)


def refine_atoms(a: Sequence[Interval], b: Sequence[Interval]) -> Tuple[Interval, ...]:
    """Merge the boundaries of two existing partitions into a common refinement."""
    bounds: set[int] = set()
    for iv in list(a) + list(b):
        bounds.add(iv.lo)
        bounds.add(iv.hi + 1)
    pts = sorted(bounds)
    return tuple(Interval(lo, hi - 1) for lo, hi in zip(pts, pts[1:]))


def find_atom(atoms: Sequence[Interval], atom: Interval) -> Optional[Interval]:
    """Return the partition atom fully containing ``atom``, or ``None``.

    ``None`` happens when ``atom`` lies outside the alphabet covered by
    ``atoms`` (the two automata may declare different alphabet boundaries).
    """
    los = [a.lo for a in atoms]
    i = bisect_right(los, atom.lo) - 1
    if i < 0:
        return None
    cand = atoms[i]
    if cand.lo <= atom.lo and atom.hi <= cand.hi:
        return cand
    return None
