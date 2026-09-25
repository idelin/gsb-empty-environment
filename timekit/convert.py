from __future__ import annotations

from dataclasses import dataclass
from typing import Literal, TypeAlias

from .model import (
    Duration,
    Instant,
    InstantInterval,
    WallClock,
    WallInterval,
    merge_intervals,
)
from .table import OffsetSegment, TransitionTable, instant_at, wall_at


Disambiguation: TypeAlias = Literal["earlier", "later", "raise", "gap"]


@dataclass(frozen=True, slots=True)
class OffsetEvidence:
    offset: Duration
    transition_id: str | None


@dataclass(frozen=True, slots=True)
class LocalInstantCandidate:
    instant: Instant
    evidence: OffsetEvidence


@dataclass(frozen=True, slots=True)
class GapEvidence:
    wall_interval: WallInterval
    transition_id: str
    transition_at: Instant
    before_offset: Duration
    after_offset: Duration


@dataclass(frozen=True, slots=True)
class LocalPointMapping:
    wall: WallClock
    candidates: tuple[LocalInstantCandidate, ...]
    selected: LocalInstantCandidate | None
    policy: Disambiguation | None
    gaps: tuple[GapEvidence, ...]
    table_version: int | str


@dataclass(frozen=True, slots=True)
class LocalIntervalCandidate:
    utc_interval: InstantInterval
    represented_wall_interval: WallInterval
    evidence: OffsetEvidence


@dataclass(frozen=True, slots=True)
class LocalIntervalMapping:
    wall_interval: WallInterval
    candidates: tuple[LocalIntervalCandidate, ...]
    utc_union: tuple[InstantInterval, ...]
    covered_wall_union: tuple[WallInterval, ...]
    missing_wall_parts: tuple[GapEvidence, ...]
    table_version: int | str


@dataclass(frozen=True, slots=True)
class WallPiece:
    wall_interval: WallInterval
    evidence: OffsetEvidence


@dataclass(frozen=True, slots=True)
class WallGap:
    wall_interval: WallInterval
    transition_id: str


@dataclass(frozen=True, slots=True)
class UtcIntervalMapping:
    utc_interval: InstantInterval
    wall_pieces: tuple[WallPiece, ...]
    wall_union: tuple[WallInterval, ...]
    skipped_wall_gaps: tuple[WallGap, ...]
    table_version: int | str


def map_instant(instant: Instant, table: TransitionTable) -> WallClock:
    return wall_at(instant, table.offset_at(instant))


def map_instant_with_evidence(
    instant: Instant, table: TransitionTable
) -> tuple[WallClock, OffsetEvidence]:
    return wall_at(instant, table.offset_at(instant)), OffsetEvidence(
        table.offset_at(instant), table.transition_id_at(instant)
    )


def map_utc_interval(
    interval: InstantInterval, table: TransitionTable
) -> UtcIntervalMapping:
    cuts = [interval.start]
    cuts.extend(
        transition.at
        for transition in table.transitions
        if interval.start < transition.at < interval.end
    )
    cuts.append(interval.end)

    pieces: list[WallPiece] = []
    skipped: list[WallGap] = []
    for piece_start, piece_end in zip(cuts, cuts[1:]):
        offset = table.offset_at(piece_start)
        pieces.append(
            WallPiece(
                WallInterval(wall_at(piece_start, offset), wall_at(piece_end, offset)),
                OffsetEvidence(offset, table.transition_id_at(piece_start)),
            )
        )

    for transition in table.transitions:
        if interval.start < transition.at < interval.end:
            previous_offset = _offset_before(table, transition.at)
            gap_start = wall_at(transition.at, previous_offset)
            gap_end = wall_at(transition.at, transition.offset_after)
            if gap_end > gap_start:
                skipped.append(
                    WallGap(WallInterval(gap_start, gap_end), transition.transition_id)
                )

    wall_intervals = [piece.wall_interval for piece in pieces]
    return UtcIntervalMapping(
        interval,
        tuple(pieces),
        tuple(merge_intervals(wall_intervals)),
        tuple(skipped),
        table.version,
    )


def local_point_candidates(
    wall: WallClock, table: TransitionTable
) -> tuple[tuple[LocalInstantCandidate, ...], tuple[GapEvidence, ...]]:
    candidates: list[LocalInstantCandidate] = []
    for segment in table.segments():
        if _segment_contains_wall_point(wall, segment):
            candidates.append(
                LocalInstantCandidate(
                    instant_at(wall, segment.offset),
                    OffsetEvidence(segment.offset, segment.transition_id),
                )
            )
    candidates.sort(key=lambda candidate: candidate.instant)
    gaps = [gap for gap in _transition_gaps(table) if gap.wall_interval.contains(wall)]
    return tuple(candidates), tuple(gaps)


def map_local_point(
    wall: WallClock,
    table: TransitionTable,
    policy: Disambiguation | None = None,
) -> LocalPointMapping:
    candidates, gaps = local_point_candidates(wall, table)
    selected: LocalInstantCandidate | None = None
    if candidates:
        if policy in (None, "earlier"):
            selected = candidates[0]
        elif policy == "later":
            selected = candidates[-1]
        elif policy == "raise":
            if len(candidates) > 1:
                raise AmbiguousLocalTime(wall, candidates)
            selected = candidates[0]
        elif policy == "gap":
            if len(candidates) > 1:
                selected = None
            else:
                selected = candidates[0]
        else:
            raise ValueError(f"unknown disambiguation policy: {policy}")
    return LocalPointMapping(wall, candidates, selected, policy, gaps, table.version)


def resolve_local_point(
    wall: WallClock,
    table: TransitionTable,
    policy: Disambiguation = "earlier",
) -> Instant:
    mapping = map_local_point(wall, table, policy)
    if mapping.selected is not None:
        return mapping.selected.instant
    if mapping.gaps:
        raise LocalTimeInGap(wall, mapping.gaps)
    if policy == "gap":
        raise AmbiguousLocalTime(wall, mapping.candidates)
    raise ValueError("local time cannot be resolved")


def map_local_interval(
    interval: WallInterval, table: TransitionTable
) -> LocalIntervalMapping:
    candidates: list[LocalIntervalCandidate] = []
    covered: list[WallInterval] = []
    for segment in table.segments():
        overlap = _project_finite_overlap(segment, interval)
        if overlap is None or overlap.empty:
            continue
        utc_interval = InstantInterval(
            instant_at(overlap.start, segment.offset),
            instant_at(overlap.end, segment.offset),
        )
        candidates.append(
            LocalIntervalCandidate(
                utc_interval,
                overlap,
                OffsetEvidence(segment.offset, segment.transition_id),
            )
        )
        covered.append(overlap)

    candidates.sort(
        key=lambda candidate: (
            candidate.utc_interval.start,
            candidate.utc_interval.end,
        )
    )
    missing = []
    for gap in _transition_gaps(table):
        overlap = _wall_overlap(gap.wall_interval, interval)
        if overlap is not None and not overlap.empty:
            missing.append(
                GapEvidence(
                    overlap,
                    gap.transition_id,
                    gap.transition_at,
                    gap.before_offset,
                    gap.after_offset,
                )
            )

    return LocalIntervalMapping(
        interval,
        tuple(candidates),
        tuple(merge_intervals([candidate.utc_interval for candidate in candidates])),
        tuple(merge_intervals(covered)),
        tuple(missing),
        table.version,
    )


class TimeConversionError(ValueError):
    pass


class LocalTimeInGap(TimeConversionError):
    def __init__(self, wall: WallClock, gaps: tuple[GapEvidence, ...]) -> None:
        super().__init__(f"{wall} does not exist in this transition table")
        self.wall = wall
        self.gaps = gaps


class AmbiguousLocalTime(TimeConversionError):
    def __init__(
        self, wall: WallClock, candidates: tuple[LocalInstantCandidate, ...]
    ) -> None:
        super().__init__(f"{wall} has {len(candidates)} UTC interpretations")
        self.wall = wall
        self.candidates = candidates


def _offset_before(table: TransitionTable, at: Instant) -> Duration:
    previous = [
        transition
        for transition in table.transitions
        if transition.at < at
    ]
    if not previous:
        return table.initial_offset
    return previous[-1].offset_after


def _transition_gaps(table: TransitionTable) -> list[GapEvidence]:
    gaps: list[GapEvidence] = []
    previous_offset = table.initial_offset
    for transition in table.transitions:
        gap_start = wall_at(transition.at, previous_offset)
        gap_end = wall_at(transition.at, transition.offset_after)
        if gap_end > gap_start:
            gaps.append(
                GapEvidence(
                    WallInterval(gap_start, gap_end),
                    transition.transition_id,
                    transition.at,
                    previous_offset,
                    transition.offset_after,
                )
            )
        previous_offset = transition.offset_after
    return gaps


def _project_segment(segment: OffsetSegment) -> WallInterval | None:
    if segment.start is None or segment.end is None:
        return None
    return WallInterval(
        wall_at(segment.start, segment.offset),
        wall_at(segment.end, segment.offset),
    )


def _project_finite_overlap(
    segment: OffsetSegment, query: WallInterval
) -> WallInterval | None:
    if segment.start is not None and segment.end is not None:
        projected = _project_segment(segment)
        assert projected is not None
        start = max(projected.start, query.start)
        end = min(projected.end, query.end)
    elif segment.start is None and segment.end is not None:
        wall_end = segment.wall_end()
        assert wall_end is not None
        start = query.start
        end = min(query.end, wall_end)
    elif segment.start is None and segment.end is None:
        start = query.start
        end = query.end
    else:
        wall_start = segment.wall_start()
        assert wall_start is not None
        start = max(query.start, wall_start)
        end = query.end
    if start < end:
        return WallInterval(start, end)
    return None


def _wall_overlap(left: WallInterval, right: WallInterval) -> WallInterval | None:
    start = max(left.start, right.start)
    end = min(left.end, right.end)
    if start < end:
        return WallInterval(start, end)
    return None


def _segment_contains_wall_point(wall: WallClock, segment: OffsetSegment) -> bool:
    projected = _project_segment(segment)
    if projected is not None:
        return projected.contains(wall)
    if segment.start is None:
        return segment.wall_end() is not None and wall < segment.wall_end()
    return segment.wall_start() is not None and wall >= segment.wall_start()
