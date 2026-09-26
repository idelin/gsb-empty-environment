"""Continuous single-operation track scheduler with exact rational arithmetic.

An operation must occupy one compatible track continuously. The occupied
interval includes the front/back safety margins and is treated as half-open:
[start - safety_before, start + duration + safety_after).

Maintenance windows and existing occupancies are forbidden half-open
intervals on a track; touching boundaries is therefore not a conflict.

Statuses are kept strictly separate:
  OPTIMAL           earliest feasible start, proven minimal, within budget
  FEASIBLE          earliest feasible start found but it exceeds the budget
  BUDGET_UNDECIDED  search was limited to the budget and nothing fit inside;
                    feasibility beyond the budget was never explored
  INFEASIBLE        no feasible start in the full horizon, or no compatible
                    tracks at all
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Optional

OPTIMAL = "OPTIMAL"
FEASIBLE = "FEASIBLE"
BUDGET_UNDECIDED = "BUDGET_UNDECIDED"
INFEASIBLE = "INFEASIBLE"

KIND_MAINTENANCE = "maintenance"
KIND_OCCUPANCY = "occupancy"
KIND_HORIZON = "horizon"


def to_fraction(value) -> Fraction:
    """Parse an int, Fraction, or 'a/b' (or decimal) string into a Fraction."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("boolean is not a valid time value")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value.strip())
    raise TypeError(f"unsupported time value: {value!r}")


@dataclass(frozen=True)
class Interval:
    """Half-open interval [start, end)."""

    start: Fraction
    end: Fraction

    def __post_init__(self):
        if not self.start < self.end:
            raise ValueError(f"empty or inverted interval: [{self.start}, {self.end})")

    def overlaps(self, other: "Interval") -> bool:
        # Half-open semantics: touching endpoints do not overlap.
        return self.start < other.end and other.start < self.end


@dataclass(frozen=True)
class ForbiddenInterval:
    interval: Interval
    kind: str  # KIND_MAINTENANCE or KIND_OCCUPANCY
    label: str = ""


@dataclass(frozen=True)
class Operation:
    name: str
    duration: Fraction
    safety_before: Fraction = Fraction(0)
    safety_after: Fraction = Fraction(0)

    def __post_init__(self):
        if self.duration <= 0:
            raise ValueError("duration must be positive")
        if self.safety_before < 0 or self.safety_after < 0:
            raise ValueError("safety margins must be non-negative")

    @property
    def footprint(self) -> Fraction:
        """Total capacity consumed, safety margins included."""
        return self.safety_before + self.duration + self.safety_after


@dataclass(frozen=True)
class Track:
    track_id: str
    maintenance: tuple = ()  # tuple[ForbiddenInterval, ...]
    occupied: tuple = ()  # tuple[ForbiddenInterval, ...]

    @property
    def forbidden(self) -> list:
        return sorted(
            list(self.maintenance) + list(self.occupied),
            key=lambda f: (f.interval.start, f.interval.end, f.kind, f.label),
        )


@dataclass
class Conflict:
    """Evidence that a tentative occupied interval hit a forbidden interval."""

    track_id: str
    kind: str
    label: str
    forbidden_start: Fraction
    forbidden_end: Fraction
    attempted_occupied_start: Fraction
    attempted_occupied_end: Fraction

    def as_dict(self) -> dict:
        return {
            "track_id": self.track_id,
            "kind": self.kind,
            "label": self.label,
            "forbidden_interval": [str(self.forbidden_start), str(self.forbidden_end)],
            "attempted_occupied_interval": [
                str(self.attempted_occupied_start),
                str(self.attempted_occupied_end),
            ],
        }


@dataclass
class TrackScan:
    track_id: str
    occupied_start: Optional[Fraction]  # earliest feasible occupied start, None if none
    conflicts: list = field(default_factory=list)  # list[Conflict]
    blocker: Optional[dict] = None  # why nothing fit, when occupied_start is None


@dataclass
class ScheduleResult:
    status: str
    operation: str
    track_id: Optional[str] = None
    start: Optional[Fraction] = None
    occupied_start: Optional[Fraction] = None
    occupied_end: Optional[Fraction] = None
    budget: Optional[Fraction] = None
    budget_met: Optional[bool] = None
    reason: str = ""
    evidence: list = field(default_factory=list)  # list[dict]
    track_scans: list = field(default_factory=list)  # list[TrackScan]

    def as_dict(self) -> dict:
        def s(v):
            return None if v is None else str(v)

        return {
            "status": self.status,
            "operation": self.operation,
            "track_id": self.track_id,
            "start": s(self.start),
            "occupied_interval": (
                None
                if self.occupied_start is None
                else [s(self.occupied_start), s(self.occupied_end)]
            ),
            "budget": s(self.budget),
            "budget_met": self.budget_met,
            "reason": self.reason,
            "evidence": self.evidence,
            "track_scans": [
                {
                    "track_id": t.track_id,
                    "earliest_occupied_start": s(t.occupied_start),
                    "conflicts": [c.as_dict() for c in t.conflicts],
                    "blocker": t.blocker,
                }
                for t in self.track_scans
            ],
        }


def scan_track(
    track: Track,
    footprint: Fraction,
    horizon_start: Fraction,
    horizon_end: Optional[Fraction],
) -> TrackScan:
    """Earliest occupied-start on one track, plus the conflict evidence chain.

    Scans forbidden intervals in order; each interval that overlaps the
    tentative occupied interval is recorded as a conflict and pushes the
    candidate to that interval's end (half-open: starting exactly at the
    end of a forbidden interval is allowed).
    """
    candidate = horizon_start
    conflicts: list[Conflict] = []
    for forb in track.forbidden:
        iv = forb.interval
        if iv.end <= horizon_start:
            continue
        if candidate + footprint <= iv.start:
            break  # fits before this and every later forbidden interval
        if candidate < iv.end and iv.start < candidate + footprint:
            conflicts.append(
                Conflict(
                    track_id=track.track_id,
                    kind=forb.kind,
                    label=forb.label,
                    forbidden_start=iv.start,
                    forbidden_end=iv.end,
                    attempted_occupied_start=candidate,
                    attempted_occupied_end=candidate + footprint,
                )
            )
            if iv.end > candidate:
                candidate = iv.end
    if horizon_end is not None and candidate + footprint > horizon_end:
        blocker = {
            "kind": KIND_HORIZON,
            "earliest_occupied_start": str(candidate),
            "earliest_occupied_end": str(candidate + footprint),
            "horizon_end": str(horizon_end),
            "detail": "occupied interval would exceed the horizon end",
        }
        return TrackScan(track.track_id, None, conflicts, blocker)
    return TrackScan(track.track_id, candidate, conflicts, None)


def schedule(
    operation: Operation,
    tracks: list,
    compatible_track_ids: Optional[list] = None,
    horizon_start=Fraction(0),
    horizon_end: Optional[Fraction] = None,
    budget: Optional[Fraction] = None,
) -> ScheduleResult:
    """Schedule `operation` on the earliest available compatible track.

    tracks: list of Track. compatible_track_ids: which tracks may be used;
    None means all tracks, an empty list means immediate INFEASIBLE.
    horizon_end: hard bound for the occupied interval; None means unbounded
    (unless only a budget is given, in which case the budget bounds the
    search and a miss yields BUDGET_UNDECIDED).
    budget: latest acceptable operation start.
    """
    horizon_start = to_fraction(horizon_start)
    if horizon_end is not None:
        horizon_end = to_fraction(horizon_end)
    if budget is not None:
        budget = to_fraction(budget)

    if compatible_track_ids is None:
        candidates = list(tracks)
    else:
        by_id = {t.track_id: t for t in tracks}
        candidates = [by_id[i] for i in compatible_track_ids if i in by_id]

    if not candidates:
        return ScheduleResult(
            status=INFEASIBLE,
            operation=operation.name,
            budget=budget,
            reason="no compatible tracks",
        )

    # A budget without an explicit horizon bounds the search: a miss is then
    # undecided rather than infeasible, because slots beyond the budget were
    # never explored.
    budget_bounded_search = horizon_end is None and budget is not None
    search_end = horizon_end if horizon_end is not None else budget

    footprint = operation.footprint
    scans = [scan_track(t, footprint, horizon_start, search_end) for t in candidates]

    feasible = [s for s in scans if s.occupied_start is not None]
    if not feasible:
        evidence = []
        for s in scans:
            evidence.extend(c.as_dict() for c in s.conflicts)
            if s.blocker is not None:
                evidence.append({"track_id": s.track_id, **s.blocker})
        if budget_bounded_search:
            return ScheduleResult(
                status=BUDGET_UNDECIDED,
                operation=operation.name,
                budget=budget,
                budget_met=False,
                reason=(
                    "no feasible start within the budget; "
                    "feasibility beyond the budget was not explored"
                ),
                evidence=evidence,
                track_scans=scans,
            )
        return ScheduleResult(
            status=INFEASIBLE,
            operation=operation.name,
            budget=budget,
            reason="no feasible occupied interval within the horizon",
            evidence=evidence,
            track_scans=scans,
        )

    # Global earliest occupied start wins; ties break by track id for
    # determinism. Per-track scans are exact, so this start is provably
    # minimal across all compatible tracks.
    best = min(feasible, key=lambda s: (s.occupied_start, s.track_id))
    occupied_start = best.occupied_start
    occupied_end = occupied_start + footprint
    start = occupied_start + operation.safety_before

    evidence = [c.as_dict() for c in best.conflicts]
    for s in feasible:
        if s is best or s.occupied_start == occupied_start:
            continue
        evidence.append(
            {
                "track_id": s.track_id,
                "kind": "later_track",
                "earliest_occupied_start": str(s.occupied_start),
                "detail": "track cannot host an earlier occupied interval",
            }
        )

    budget_met = None if budget is None else start <= budget
    if budget is not None and start > budget:
        status = FEASIBLE
        reason = "earliest feasible start exceeds the budget"
    else:
        status = OPTIMAL
        reason = "earliest feasible start, proven minimal across compatible tracks"

    return ScheduleResult(
        status=status,
        operation=operation.name,
        track_id=best.track_id,
        start=start,
        occupied_start=occupied_start,
        occupied_end=occupied_end,
        budget=budget,
        budget_met=budget_met,
        reason=reason,
        evidence=evidence,
        track_scans=scans,
    )
