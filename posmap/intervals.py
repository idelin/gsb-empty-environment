"""Interval-relation maps: the core of the multi-layer position mapping.

A mapping between two coordinate spaces (e.g. source bytes <-> expanded
bytes, expanded bytes <-> bytecode pcs) is a *relation* stored as a set of
rectangular blocks ``(src, dst)``.  Using relations instead of single
offsets means:

* one-to-many: one source interval may appear in several blocks;
* overlapping sources: blocks may overlap on either side freely;
* generated fragments: blocks with ``src is None`` have no source at all.

Composition is block-wise relation composition: ``(a, b)`` composed with
``(b', c)`` yields ``(a, c)`` whenever ``b`` and ``b'`` intersect.  Blocks
of the right-hand map whose source side is not covered by anything on the
left become ``generated`` blocks in the composed map.  Composition is
associative (verified by finite-model property tests).
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True, order=True)
class Interval:
    """Half-open interval ``[start, end)``.

    For source text the unit is UTF-8 bytes; for the expanded buffer it is
    bytes as well; for bytecode it is instruction indices (pcs).
    """

    start: int
    end: int

    def __post_init__(self) -> None:
        if self.start < 0 or self.end < self.start:
            raise ValueError(f"bad interval [{self.start}, {self.end})")

    def intersects(self, other: "Interval") -> bool:
        return self.start < other.end and other.start < self.end

    def contains(self, pos: int) -> bool:
        return self.start <= pos < self.end

    def shift(self, delta: int) -> "Interval":
        return Interval(self.start + delta, self.end + delta)

    def union(self, other: "Interval") -> "Interval":
        return Interval(min(self.start, other.start), max(self.end, other.end))


# Lower priority wins when a diagnostic picks the "best" candidate; the
# combination itself only needs an associative, commutative operation.
TAG_PRIORITY = {"source": 0, "code": 0, "macro_body": 1, "generated": 2}


def combine_tags(a: str, b: str) -> str:
    """Combine two block tags under composition.

    ``max`` on the total order ``(priority, tag)`` is associative and
    commutative, so the tag of a composed chain never depends on how the
    composition was bracketed.
    """

    return max(a, b, key=lambda tag: (TAG_PRIORITY[tag], tag))


@dataclass(frozen=True)
class Block:
    """One rectangle of an interval relation.

    ``src is None`` marks a generated fragment: it has a destination but no
    source, and no layer below may invent one for it.
    """

    src: Interval | None
    dst: Interval
    tag: str
    meta: tuple = ()  # hashable, concatenated associatively under compose


class IntervalMap:
    """An immutable interval relation between two coordinate spaces."""

    __slots__ = ("blocks", "name")

    def __init__(self, blocks=(), name: str = ""):
        self.blocks = frozenset(blocks)
        self.name = name

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return f"IntervalMap({self.name!r}, {len(self.blocks)} blocks)"

    def forward(self, interval: Interval) -> list[Block]:
        """All blocks whose *source* side intersects ``interval``."""

        return [b for b in self.blocks if b.src is not None and b.src.intersects(interval)]

    def backward(self, interval: Interval) -> list[Block]:
        """All blocks whose *destination* side intersects ``interval``."""

        return [b for b in self.blocks if b.dst.intersects(interval)]

    def compose(self, other: "IntervalMap", name: str = "") -> "IntervalMap":
        """Relation composition: ``self`` is A->B, ``other`` is B->C.

        The result relates A to C.  One-to-many and overlapping blocks are
        preserved because every intersecting pair contributes a block.
        Blocks of ``other`` whose source side is not covered by any block
        of ``self`` become generated blocks (``src is None``): they are
        reachable in C but have no origin in A.
        """

        out: set[Block] = set()
        covered: set[Block] = set()
        for b1 in self.blocks:
            for b2 in other.blocks:
                if b2.src is not None and b1.dst.intersects(b2.src):
                    out.add(
                        Block(
                            b1.src,
                            b2.dst,
                            combine_tags(b1.tag, b2.tag),
                            b1.meta + b2.meta,
                        )
                    )
                    covered.add(b2)
        for b2 in other.blocks:
            if b2 not in covered:
                out.add(Block(None, b2.dst, "generated", b2.meta))
        return IntervalMap(out, name)
