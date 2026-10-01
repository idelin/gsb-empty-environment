"""Sample blocks and sampled sequences.

A :class:`SampleBlock` is one contiguous run of stored samples with a
rational period, an epoch and a data version.  A :class:`SampledSequence`
is a set of blocks that may overlap and may leave gaps; overlaps are
resolved by the (epoch, version) priority rule, never by fabrication.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .exact import as_fraction
from .intervals import Interval

__all__ = ["SampleBlock", "SampledSequence"]


@dataclass(frozen=True)
class SampleBlock:
    """Samples ``t(n) = origin + n * period`` for ``index_lo <= n < index_hi``.

    ``missing`` holds sorted, non-overlapping ``range`` objects of indices
    that were never recorded (dropped samples).  Missing indices are holes,
    not zeros.
    """

    block_id: str
    epoch: int
    version: int
    origin: Fraction
    period: Fraction
    index_lo: int
    index_hi: int
    missing: tuple = ()

    def __post_init__(self):
        object.__setattr__(self, "origin", as_fraction(self.origin))
        object.__setattr__(self, "period", as_fraction(self.period))
        if self.period <= 0:
            raise ValueError("period must be positive")
        if self.index_hi <= self.index_lo:
            raise ValueError("index_hi must be greater than index_lo")
        missing = tuple(self.missing)
        prev_stop = self.index_lo
        for r in missing:
            if r.start < prev_stop or r.stop > self.index_hi or r.start >= r.stop:
                raise ValueError(f"bad missing range {r!r} for block {self.block_id!r}")
            prev_stop = r.stop
        object.__setattr__(self, "missing", missing)

    @property
    def rate(self) -> Fraction:
        """Samples per second (exact reciprocal of the period)."""
        return 1 / self.period

    @property
    def priority(self) -> tuple:
        """Deduplication priority: newer epoch wins, then newer version."""
        return (self.epoch, self.version)

    def time_of(self, n: int) -> Fraction:
        return self.origin + n * self.period

    def contains_index(self, n: int) -> bool:
        if not self.index_lo <= n < self.index_hi:
            return False
        return not any(r.start <= n < r.stop for r in self.missing)

    def span(self) -> Interval:
        """Closed interval of actual sample instants [t(lo), t(hi - 1)]."""
        return Interval(self.time_of(self.index_lo), self.time_of(self.index_hi - 1), True, True)

    def missing_spans(self) -> list[Interval]:
        return [Interval(self.time_of(r.start), self.time_of(r.stop - 1), True, True)
                for r in self.missing]

    def claim_regions(self) -> list[Interval]:
        """Time regions this block claims for deduplication (span minus holes)."""
        regions = [self.span()]
        for hole in self.missing_spans():
            regions = [piece for region in regions for piece in region.difference(hole)]
        return regions


class SampledSequence:
    """An unordered collection of sample blocks on one local clock."""

    def __init__(self, clock_id: str, blocks=()):
        self.clock_id = clock_id
        self._blocks: list[SampleBlock] = []
        self._by_id: dict[str, SampleBlock] = {}
        for block in blocks:
            self.add_block(block)

    def add_block(self, block: SampleBlock) -> None:
        if block.block_id in self._by_id:
            raise ValueError(f"duplicate block id {block.block_id!r}")
        self._blocks.append(block)
        self._by_id[block.block_id] = block

    @property
    def blocks(self) -> tuple:
        return tuple(self._blocks)

    def get_block(self, block_id: str) -> SampleBlock:
        return self._by_id[block_id]
