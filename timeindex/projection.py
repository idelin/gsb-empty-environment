"""Forward projection: continuous time interval -> index slices + gaps.

The result of a projection is *not* promised to be a contiguous array:
it is a list of :class:`IndexSlice` objects plus a list of :class:`Gap`
objects.  Missing coverage is reported, never filled with fabricated zeros.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from math import ceil, floor

from .blocks import SampleBlock, SampledSequence
from .intervals import Interval

__all__ = [
    "NO_COVERAGE", "MISSING_SAMPLES", "NonContiguousError",
    "RoundingEvidence", "IndexSlice", "Gap", "Projection", "project",
]

NO_COVERAGE = "no_coverage"
MISSING_SAMPLES = "missing_samples"


class NonContiguousError(Exception):
    """Raised when a projection is treated as one contiguous array but is not."""


@dataclass(frozen=True)
class RoundingEvidence:
    """Exact record of how the interval endpoints were rounded onto the grid.

    ``start_position`` / ``end_position`` are the exact fractional grid
    positions of the interval bounds; the residuals are the exact seconds
    between a bound and the sample that was actually selected (>= 0).
    A zero residual means the bound landed exactly on a sample instant.
    """

    start_position: Fraction | None
    start_index: int
    start_residual: Fraction | None
    end_position: Fraction | None
    end_index: int
    end_residual: Fraction | None

    @property
    def start_hit(self) -> bool:
        return self.start_residual == 0

    @property
    def end_hit(self) -> bool:
        return self.end_residual == 0


@dataclass(frozen=True)
class IndexSlice:
    """A contiguous run of indices ``[start, stop)`` inside one block."""

    block: SampleBlock
    start: int
    stop: int
    evidence: RoundingEvidence | None = None

    def __len__(self) -> int:
        return self.stop - self.start

    def indices(self) -> range:
        return range(self.start, self.stop)

    def covered_interval(self) -> Interval:
        """Reverse map: the exact closed interval of actual sample instants."""
        return Interval(self.block.time_of(self.start),
                        self.block.time_of(self.stop - 1), True, True)

    def cell_interval(self) -> Interval:
        """Covered interval widened by half a period on each side."""
        half = self.block.period / 2
        cov = self.covered_interval()
        return Interval(cov.start - half, cov.end + half, True, True)


@dataclass(frozen=True)
class Gap:
    """A query region with no usable samples (never zero-filled)."""

    interval: Interval
    reason: str = NO_COVERAGE


class Projection:
    """Slices + gaps answering one interval query."""

    def __init__(self, slices=(), gaps=(), table_version=None,
                 query_wall=None, local_pieces=()):
        self.slices = list(slices)
        self.gaps = list(gaps)
        self.table_version = table_version
        self.query_wall = query_wall
        self.local_pieces = tuple(local_pieces)

    @property
    def is_contiguous(self) -> bool:
        return not self.gaps and len(self.slices) == 1

    def require_contiguous(self) -> IndexSlice:
        """Return the single slice, or raise instead of faking contiguity."""
        if self.gaps:
            raise NonContiguousError(
                f"projection has {len(self.gaps)} gap(s); refusing to fabricate samples")
        if len(self.slices) != 1:
            raise NonContiguousError(
                f"projection spans {len(self.slices)} slice(s); not a contiguous array")
        return self.slices[0]

    @property
    def total_samples(self) -> int:
        return sum(len(s) for s in self.slices)

    def iter_indices(self):
        for sl in self.slices:
            for n in sl.indices():
                yield (sl.block.block_id, n)

    def sample_times(self):
        for sl in self.slices:
            for n in sl.indices():
                yield sl.block.time_of(n)

    def slice_summary(self) -> list:
        return [(s.block.block_id, s.start, s.stop) for s in self.slices]

    def gap_summary(self) -> list:
        return [(g.interval, g.reason) for g in self.gaps]


def _project_block(block: SampleBlock, interval: Interval) -> IndexSlice | None:
    if interval.start is None:
        n_min = block.index_lo
        start_pos = None
    else:
        x = (interval.start - block.origin) / block.period
        start_pos = x
        n_min = ceil(x) if interval.closed_start else floor(x) + 1
    if interval.end is None:
        n_max = block.index_hi - 1
        end_pos = None
    else:
        y = (interval.end - block.origin) / block.period
        end_pos = y
        n_max = floor(y) if interval.closed_end else ceil(y) - 1
    n_min = max(n_min, block.index_lo)
    n_max = min(n_max, block.index_hi - 1)
    if n_min > n_max:
        return None
    start_res = None if interval.start is None else block.time_of(n_min) - interval.start
    end_res = None if interval.end is None else interval.end - block.time_of(n_max)
    evidence = RoundingEvidence(start_pos, n_min, start_res, end_pos, n_max, end_res)
    return IndexSlice(block, n_min, n_max + 1, evidence)


def project(sequence: SampledSequence, interval: Interval) -> Projection:
    """Project ``interval`` (local-clock time) onto ``sequence``.

    Blocks claim time regions in descending (epoch, version) priority, so
    when two blocks contain the same timestamp the sample is taken from the
    highest-priority block exactly once.  Whatever no block claims is
    reported as gaps.
    """
    remaining = [interval]
    slices: list[IndexSlice] = []
    ordered = sorted(sequence.blocks, key=lambda b: b.priority, reverse=True)
    for block in ordered:
        if not remaining:
            break
        next_remaining = []
        for rem in remaining:
            pieces = [rem]
            for region in block.claim_regions():
                claimed = []
                for piece in pieces:
                    inter = piece.intersect(region)
                    if inter.is_empty():
                        claimed.append(piece)
                        continue
                    sl = _project_block(block, inter)
                    if sl is not None:
                        slices.append(sl)
                    claimed.extend(piece.difference(inter))
                pieces = claimed
            next_remaining.extend(pieces)
        remaining = next_remaining
    gaps = _classify_gaps(sequence, remaining)
    slices.sort(key=lambda sl: (sl.block.time_of(sl.start), sl.block.block_id))
    return Projection(slices=slices, gaps=gaps, local_pieces=(interval,))


def _classify_gaps(sequence: SampledSequence, remaining: list[Interval]) -> list[Gap]:
    spans = [b.span() for b in sequence.blocks]
    gaps: list[Gap] = []
    for rem in remaining:
        if rem.is_empty():
            continue
        uncovered = [rem]
        for span in spans:
            uncovered = [p for u in uncovered for p in u.difference(span)]
        covered = [rem]
        for u in uncovered:
            covered = [p for c in covered for p in c.difference(u)]
        gaps.extend(Gap(u, NO_COVERAGE) for u in uncovered)
        gaps.extend(Gap(c, MISSING_SAMPLES) for c in covered)
    gaps.sort(key=lambda g: (g.interval.start is not None, g.interval.start))
    return gaps
