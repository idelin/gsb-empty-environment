"""Synthetic offset-transition tables.

A :class:`TransitionTable` is an immutable, versioned snapshot: a base UTC
offset plus an ordered list of transitions.  It converts in both directions:

* instant to wall reading (always unique, carries a ``fold`` identity);
* wall clock to *all* candidate instants (0 in a spring-forward gap, 1
  normally, 2 in a fall-back overlap, and possibly more when consecutive
  backward transitions stack -- never assume at most two);
* wall interval to all candidate UTC intervals plus a report of any local
  segments that fall inside a gap and therefore do not exist.
"""

from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass

from .core import Duration, Instant, WallClock


class GapTimeError(ValueError):
    """A wall-clock time falls inside a spring-forward gap."""


class AmbiguousTimeError(ValueError):
    """A wall-clock time has more than one UTC interpretation."""


@dataclass(frozen=True)
class Transition:
    """At instant ``utc`` the offset in effect becomes ``offset_after``."""

    utc: Instant
    offset_after: Duration


@dataclass(frozen=True)
class OffsetPeriod:
    """Half-open span during which one offset applies.

    ``utc_start``/``local_start`` are ``None`` for the initial period and
    ``utc_end``/``local_end`` are ``None`` for the final period.
    """

    index: int
    utc_start: Instant | None
    utc_end: Instant | None
    offset: Duration
    local_start: WallClock | None
    local_end: WallClock | None

    def contains_utc(self, instant: Instant) -> bool:
        if self.utc_start is not None and instant < self.utc_start:
            return False
        if self.utc_end is not None and instant >= self.utc_end:
            return False
        return True

    def contains_local(self, wall: WallClock) -> bool:
        if self.local_start is not None and wall < self.local_start:
            return False
        if self.local_end is not None and wall >= self.local_end:
            return False
        return True


@dataclass(frozen=True)
class Candidate:
    """One UTC interpretation of a wall-clock time, with evidence."""

    instant: Instant
    offset: Duration
    fold: int  # 0 = earliest occurrence, 1 = next, ... (no upper bound)
    period_index: int
    evidence: str


@dataclass(frozen=True)
class WallReading:
    """Result of instant to wall conversion; ``fold`` preserves identity."""

    wall: WallClock
    offset: Duration
    fold: int
    period_index: int


@dataclass(frozen=True)
class UtcInterval:
    """Half-open interval on the UTC timeline, [start, end)."""

    start: Instant
    end: Instant


@dataclass(frozen=True)
class LocalGap:
    """Half-open wall-clock interval that does not exist locally."""

    start: WallClock
    end: WallClock


@dataclass(frozen=True)
class IntervalCandidate:
    """One interpretation of a wall interval as UTC interval(s)."""

    local_start: WallClock
    local_end: WallClock
    utc_intervals: tuple[UtcInterval, ...]
    offset: Duration
    fold: int
    period_index: int
    evidence: str


@dataclass(frozen=True)
class IntervalMapping:
    """Full result of mapping a wall-clock interval onto the UTC timeline."""

    local_start: WallClock
    local_end: WallClock
    candidates: tuple[IntervalCandidate, ...]
    missing: tuple[LocalGap, ...]

    @property
    def utc_union(self) -> tuple[UtcInterval, ...]:
        """Merged union of every candidate's UTC intervals."""
        spans = sorted(
            (iv.start.epoch_nanos, iv.end.epoch_nanos)
            for cand in self.candidates
            for iv in cand.utc_intervals
        )
        merged: list[list[int]] = []
        for start, end in spans:
            if merged and start <= merged[-1][1]:
                merged[-1][1] = max(merged[-1][1], end)
            else:
                merged.append([start, end])
        return tuple(UtcInterval(Instant(s), Instant(e)) for s, e in merged)


class TransitionTable:
    """An immutable, versioned synthetic offset-transition table."""

    def __init__(
        self,
        *,
        version: str,
        base_offset: Duration,
        transitions: tuple[Transition, ...] | list[Transition] = (),
    ) -> None:
        ordered = tuple(transitions)
        for prev, nxt in zip(ordered, ordered[1:]):
            if not prev.utc < nxt.utc:
                raise ValueError("transitions must be strictly increasing in UTC")
        self.version = version
        self.base_offset = base_offset
        self.transitions = ordered
        self._periods = self._build_periods()

    def _build_periods(self) -> tuple[OffsetPeriod, ...]:
        periods: list[OffsetPeriod] = []
        offset = self.base_offset
        utc_start: Instant | None = None
        index = 0
        for transition in (*self.transitions, None):
            utc_end = transition.utc if transition is not None else None
            local_start = (
                utc_start.plus(offset).to_wall() if utc_start is not None else None
            )
            local_end = (
                utc_end.plus(offset).to_wall() if utc_end is not None else None
            )
            periods.append(
                OffsetPeriod(index, utc_start, utc_end, offset, local_start, local_end)
            )
            index += 1
            if transition is not None:
                utc_start = transition.utc
                offset = transition.offset_after
        return tuple(periods)

    @property
    def periods(self) -> tuple[OffsetPeriod, ...]:
        return self._periods

    def period_at(self, instant: Instant) -> OffsetPeriod:
        utcs = [t.utc for t in self.transitions]
        return self._periods[bisect_right(utcs, instant)]

    def instant_to_wall(self, instant: Instant) -> WallReading:
        period = self.period_at(instant)
        wall = instant.plus(period.offset).to_wall()
        candidates = self.wall_to_instants(wall)
        fold = next(c.fold for c in candidates if c.period_index == period.index)
        return WallReading(wall, period.offset, fold, period.index)

    def wall_to_instants(self, wall: WallClock) -> tuple[Candidate, ...]:
        """Return *all* UTC interpretations of ``wall``, earliest first.

        The result may be empty (gap), a singleton (normal), or have two or
        more entries (overlap).  Consecutive backward transitions can stack,
        so callers must not assume at most two candidates.
        """
        hits: list[tuple[Instant, OffsetPeriod]] = []
        for period in self._periods:
            if period.contains_local(wall):
                instant = wall.to_instant_equiv().minus(period.offset)
                hits.append((instant, period))
        hits.sort(key=lambda item: item[0].epoch_nanos)
        total = len(hits)
        return tuple(
            Candidate(
                instant=instant,
                offset=period.offset,
                fold=fold,
                period_index=period.index,
                evidence=(
                    f"offset period #{period.index} "
                    f"(UTC{period.offset.format_offset()} in effect); "
                    f"occurrence {fold + 1} of {total}"
                ),
            )
            for fold, (instant, period) in enumerate(hits)
        )

    def resolve_wall(self, wall: WallClock, policy: str = "strict") -> Candidate:
        """Resolve a wall time to one instant.

        Policies: ``earlier`` / ``later`` pick an occurrence in an overlap
        (and round-trip with :meth:`instant_to_wall` via ``fold``);
        ``strict`` raises on gaps and overlaps; ``compatible`` shifts
        forward out of a gap and picks the earlier occurrence in overlaps.
        """
        candidates = self.wall_to_instants(wall)
        if not candidates:
            if policy == "compatible":
                gap_end = self._gap_end_after(wall)
                return self.resolve_wall(gap_end, "earlier")
            raise GapTimeError(f"{wall} falls in a spring-forward gap")
        if policy in ("earlier", "compatible"):
            return candidates[0]
        if policy == "later":
            return candidates[-1]
        if len(candidates) > 1:
            raise AmbiguousTimeError(
                f"{wall} has {len(candidates)} UTC interpretations"
            )
        return candidates[0]

    def _gap_end_after(self, wall: WallClock) -> WallClock:
        """First existing wall time at or after a gap containing ``wall``."""
        for period in self._periods:
            if (
                period.local_start is not None
                and period.local_start > wall
                and period.utc_start is not None
            ):
                return period.local_start
        raise GapTimeError(f"no existing wall time at or after {wall}")

    def map_wall_interval(
        self, local_start: WallClock, local_end: WallClock
    ) -> IntervalMapping:
        """Map a half-open wall interval to candidate UTC intervals.

        Every offset period overlapping the local range contributes one
        candidate (its own UTC interval(s)); local segments that fall in a
        spring-forward gap are reported in ``missing``.
        """
        if not local_start < local_end:
            raise ValueError("local_start must be before local_end")

        segments: list[tuple[OffsetPeriod, WallClock, WallClock]] = []
        for period in self._periods:
            lo = local_start
            if period.local_start is not None and period.local_start > lo:
                lo = period.local_start
            hi = local_end
            if period.local_end is not None and period.local_end < hi:
                hi = period.local_end
            if lo < hi:
                segments.append((period, lo, hi))

        candidates: list[IntervalCandidate] = []
        for period, lo, hi in segments:
            utc_lo = lo.to_instant_equiv().minus(period.offset)
            utc_hi = hi.to_instant_equiv().minus(period.offset)
            occurrences = self.wall_to_instants(lo)
            fold = next(
                c.fold for c in occurrences if c.period_index == period.index
            )
            candidates.append(
                IntervalCandidate(
                    local_start=lo,
                    local_end=hi,
                    utc_intervals=(UtcInterval(utc_lo, utc_hi),),
                    offset=period.offset,
                    fold=fold,
                    period_index=period.index,
                    evidence=(
                        f"local [{lo}, {hi}) via offset period #{period.index} "
                        f"(UTC{period.offset.format_offset()}); "
                        f"occurrence {fold + 1} of {len(occurrences)} "
                        f"at segment start"
                    ),
                )
            )

        missing: list[LocalGap] = []
        cursor = local_start
        for _period, lo, hi in segments:
            if cursor < lo:
                missing.append(LocalGap(cursor, lo))
            if hi > cursor:
                cursor = hi
        if cursor < local_end:
            missing.append(LocalGap(cursor, local_end))

        return IntervalMapping(
            local_start=local_start,
            local_end=local_end,
            candidates=tuple(candidates),
            missing=tuple(missing),
        )
