"""Integer intervals and alphabet partitioning.

Edges of automata are labelled with intervals over an ordered, potentially
huge alphabet (integers, or characters via their code points).  All set
operations are performed on *partitions* of the alphabet into disjoint
cells, never by enumerating individual symbols.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Interval:
    """Closed integer interval [lo, hi]."""

    lo: int
    hi: int

    def __post_init__(self) -> None:
        if self.lo > self.hi:
            raise ValueError(f"empty interval [{self.lo}, {self.hi}]")

    def contains(self, x: int) -> bool:
        return self.lo <= x <= self.hi

    def intersects(self, other: "Interval") -> bool:
        return self.lo <= other.hi and other.lo <= self.hi

    def __str__(self) -> str:  # pragma: no cover - cosmetic
        return f"[{self.lo},{self.hi}]"


def char_interval(a: str, b: str | None = None) -> Interval:
    """Interval over single-character strings, e.g. char_interval('a','z')."""
    if len(a) != 1 or (b is not None and len(b) != 1):
        raise ValueError("char_interval expects single characters")
    b = a if b is None else b
    return Interval(ord(a), ord(b))


def encode(text: str) -> tuple[int, ...]:
    """Encode a string as a word (tuple of alphabet symbols)."""
    return tuple(ord(c) for c in text)


def decode(word: tuple[int, ...] | list[int]) -> str:
    """Best-effort decode of a word back to a string (for display)."""
    try:
        return "".join(chr(s) for s in word)
    except (ValueError, OverflowError):
        return repr(tuple(word))


def partition(intervals: list[Interval], domain: Interval | None = None) -> list[Interval]:
    """Refine ``intervals`` into a list of pairwise-disjoint cells.

    The result covers the union of ``intervals`` (or ``domain`` when given)
    and every input interval is an exact union of result cells.  Runs in
    O(n log n) on the number of interval endpoints; the alphabet itself is
    never enumerated.
    """
    ivs = list(intervals)
    if domain is not None:
        ivs.append(domain)
    if not ivs:
        return []
    points: set[int] = set()
    for iv in ivs:
        points.add(iv.lo)
        points.add(iv.hi + 1)
    bounds = sorted(points)
    cells: list[Interval] = []
    for lo, hi_excl in zip(bounds, bounds[1:]):
        cell = Interval(lo, hi_excl - 1)
        if any(cell.intersects(iv) for iv in ivs):
            cells.append(cell)
    return cells
