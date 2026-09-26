"""Blocked positional postings with skip metadata and decode accounting.

Positions of one (term-set, document, field) are stored in fixed-size
blocks. The maximum position of each block is kept as metadata that can
be consulted for free; touching the positions inside a block costs one
decode. This models real positional indexes (block-max skipping) and
lets the matcher jump over irrelevant stretches instead of materializing
a Cartesian product of position lists.
"""
from __future__ import annotations

import bisect
from typing import Iterable, Iterator, Optional

BLOCK_SIZE = 8
INF = 1 << 62


class DecodeCounter:
    """Instrumentation: how many posting blocks were decoded."""

    __slots__ = ("blocks_decoded", "positions_scanned")

    def __init__(self) -> None:
        self.blocks_decoded = 0
        self.positions_scanned = 0

    def __repr__(self) -> str:
        return (
            "DecodeCounter(blocks_decoded=%d, positions_scanned=%d)"
            % (self.blocks_decoded, self.positions_scanned)
        )


class BlockedPositions:
    """Sorted positions stored as blocks with free block-max metadata."""

    __slots__ = ("blocks", "block_max", "length")

    def __init__(self, positions: Iterable[int], block_size: int = BLOCK_SIZE):
        ordered = sorted(positions)
        self.blocks = [
            tuple(ordered[i : i + block_size])
            for i in range(0, len(ordered), block_size)
        ]
        self.block_max = tuple(b[-1] for b in self.blocks)
        self.length = len(ordered)

    def __len__(self) -> int:
        return self.length

    def range_iter(
        self, lo: int, hi: int, counter: Optional[DecodeCounter] = None
    ) -> Iterator[int]:
        """Yield positions p with lo <= p <= hi.

        Blocks whose max is below lo are skipped via metadata only
        (no decode). Blocks overlapping the range are decoded once.
        """
        if not self.blocks:
            return
        first = bisect.bisect_left(self.block_max, lo)
        for bi in range(first, len(self.blocks)):
            block = self.blocks[bi]
            if block[0] > hi:
                break
            if counter is not None:
                counter.blocks_decoded += 1
            for p in block:
                if p > hi:
                    break
                if p >= lo:
                    if counter is not None:
                        counter.positions_scanned += 1
                    yield p
