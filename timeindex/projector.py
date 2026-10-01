"""High-level projector: wall-clock intervals -> index slices on a local clock,
plus versioned cursors that survive clock recalibration."""
from __future__ import annotations

from dataclasses import dataclass
from math import floor

from .exact import WallClockTime, as_fraction
from .intervals import Interval
from .offset import Clock
from .blocks import SampledSequence
from .projection import Gap, Projection, project

__all__ = ["Cursor", "SequenceProjector"]


@dataclass(frozen=True)
class Cursor:
    """A versioned position reference into a sampled sequence.

    Stores the offset-table version used to create it, so historical
    positions stay reproducible after later clock calibrations.
    """

    block_id: str
    epoch: int
    block_version: int
    index: int
    table_version: object


class SequenceProjector:
    """Projects wall-clock intervals onto a sampled sequence via a Clock."""

    def __init__(self, sequence: SampledSequence, clock: Clock):
        if sequence.clock_id != clock.clock_id:
            raise ValueError("sequence and clock disagree on the clock id")
        self.sequence = sequence
        self.clock = clock

    def project_wall(self, wall_interval: Interval, table_version=None) -> Projection:
        table = self.clock.table(table_version)
        slices, gaps = [], []
        local_pieces = table.forward_interval(wall_interval)
        for piece in local_pieces:
            sub = project(self.sequence, piece)
            slices.extend(sub.slices)
            gaps.extend(sub.gaps)
        return Projection(slices=slices, gaps=gaps, table_version=table.version,
                          query_wall=wall_interval, local_pieces=local_pieces)

    def reproject_after_correction(self, prev: Projection, at_wall,
                                   new_version=None) -> Projection:
        """Recompute only the slices intersecting the corrected region.

        A correction applies from wall time ``at_wall`` onward.  Slices and
        gaps entirely before it are reused as the very same objects (their
        historical version is still valid); only intersecting parts are
        recomputed with the new table version.
        """
        if prev.query_wall is None or prev.table_version is None:
            raise ValueError("projection was not produced by project_wall")
        at_wall = as_fraction(at_wall)
        if new_version is None:
            new_version = self.clock.latest_version
        old_table = self.clock.table(prev.table_version)
        at_local = old_table.forward(at_wall)
        query_end_local = (None if prev.query_wall.end is None
                           else old_table.forward(prev.query_wall.end))
        tail_local = Interval(at_local, query_end_local, True, True)
        head_local = Interval(None, at_local, True, False)

        slices, gaps = [], []
        for sl in prev.slices:
            cov = sl.covered_interval()
            if cov.intersect(tail_local).is_empty():
                slices.append(sl)  # untouched: same object, historical version
                continue
            head_part = cov.intersect(head_local)
            if not head_part.is_empty():
                slices.extend(project(self.sequence, head_part).slices)
        for gap in prev.gaps:
            if gap.interval.intersect(tail_local).is_empty():
                gaps.append(gap)
                continue
            head_part = gap.interval.intersect(head_local)
            if not head_part.is_empty():
                gaps.append(Gap(head_part, gap.reason))
        tail_query = Interval(at_wall, prev.query_wall.end, True,
                              prev.query_wall.closed_end)
        tail = self.project_wall(tail_query, new_version)
        slices.extend(tail.slices)
        gaps.extend(tail.gaps)
        return Projection(slices=slices, gaps=gaps, table_version=new_version,
                          query_wall=prev.query_wall)

    def cursor_at(self, wall_time: WallClockTime, table_version=None) -> Cursor | None:
        """Position a cursor at the latest sample at or before ``wall_time``."""
        table = self.clock.table(table_version)
        local = table.forward(wall_time.offset)
        best = None
        for blk in self.sequence.blocks:
            if not blk.span().contains(local):
                continue
            n = floor((local - blk.origin) / blk.period)
            n = min(max(n, blk.index_lo), blk.index_hi - 1)
            if not blk.contains_index(n):
                continue
            if best is None or blk.priority > best[0].priority:
                best = (blk, n)
        if best is None:
            return None
        blk, n = best
        return Cursor(blk.block_id, blk.epoch, blk.version, n, table.version)

    def resolve_cursor(self, cursor: Cursor) -> WallClockTime:
        """Map a cursor back to wall time using its *stored* table version."""
        blk = self.sequence.get_block(cursor.block_id)
        local = blk.time_of(cursor.index)
        return WallClockTime(self.clock.table(cursor.table_version).backward(local))
