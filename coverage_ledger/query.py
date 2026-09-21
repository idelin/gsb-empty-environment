"""Coverage queries over a :class:`StateView`.

A sweep line walks the union of two kinds of breakpoints inside the window:

* online interval endpoints (when a device switches up/down);
* membership rule endpoints (when the set of group members changes).

Between two consecutive breakpoints both the member set and the online set
are constant, so each elementary segment is classified once.  Devices that
report up but are not members in a segment do not count; devices that are
members but never reported anything are still eligible for the
intersection/all-online mode.

Repeated uploads are harmless: online presence is the per-device *union* of
its reported intervals, so overlap never double counts length.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Any, Dict, List, Optional, Tuple

from .errors import InvalidWindow
from .ledger import StateView
from .store import Timeline


@dataclass(frozen=True)
class Segment:
    """One constant-state elementary segment inside the window."""

    start: object
    end: object
    online_members: Tuple[str, ...]          # members currently up
    online_non_members: Tuple[str, ...]      # up devices that are not members
    members: Tuple[str, ...]                 # devices that are members here
    covered: bool                            # qualifies for the queried mode
    evidence: Tuple[str, ...] = field(default_factory=tuple)  # observation ids

    @property
    def length(self) -> object:
        return self.end - self.start


@dataclass(frozen=True)
class QueryResult:
    window: Tuple[object, object]
    mode: str
    k: Optional[int]
    known_coverage_length: object
    window_length: object
    coverage_rate: Fraction
    segments: Tuple[Segment, ...]

    @property
    def covered_segments(self) -> Tuple[Segment, ...]:
        return tuple(segment for segment in self.segments if segment.covered)

    def evidence_ids(self) -> Tuple[str, ...]:
        ids: List[str] = []
        seen = set()
        for segment in self.covered_segments:
            for evidence in segment.evidence:
                if evidence not in seen:
                    seen.add(evidence)
                    ids.append(evidence)
        return tuple(ids)

    def to_dict(self) -> Dict[str, Any]:
        return {
            "window": [_number(value) for value in self.window],
            "mode": self.mode,
            "k": self.k,
            "known_coverage_length": _number(self.known_coverage_length),
            "window_length": _number(self.window_length),
            "coverage_rate": str(self.coverage_rate),
            "covered_segments": [
                [_number(segment.start), _number(segment.end)]
                for segment in self.covered_segments
            ],
            "evidence": list(self.evidence_ids()),
        }


def _number(value: object) -> object:
    return float(value) if isinstance(value, Fraction) else value


def _check_window(window: Tuple[object, object]) -> Tuple[object, object]:
    if not isinstance(window, (tuple, list)) or len(window) != 2:
        raise InvalidWindow("window must be a (start, end) pair")
    lo, hi = tuple(window)
    if lo is None or hi is None:
        raise InvalidWindow("window endpoints must be finite")
    if hi < lo:
        raise InvalidWindow(f"window end {hi!r} precedes start {lo!r}")
    if hi == lo:
        raise InvalidWindow("zero-length window is not a valid query")
    return lo, hi


def _collect(timeline: Timeline, devices: List[str], lo: object, hi: object
             ) -> Tuple[Dict[str, List[Tuple[object, object]]], set]:
    """Per-device active (clipped) intervals and all interior breakpoints.

    Back-to-back records stay separate here; per-device presence is
    recomputed at each breakpoint so an end and a start at the same point
    never cancel.
    """
    active: Dict[str, List[Tuple[object, object]]] = {device: [] for device in devices}
    points = {lo, hi}
    for device in devices:
        for _record_id, _, start, end in timeline.covering(device, (lo, hi)):
            piece_end = hi if end is None else min(end, hi)
            active[device].append((max(start, lo), piece_end))
            if start > lo:
                points.add(start)
            if end is not None and end < hi:
                points.add(end)
    return active, points


def query(view: StateView, window: Tuple[object, object], mode: str = "union",
          k: Optional[int] = None) -> QueryResult:
    """Answer a coverage query against a pinned snapshot."""
    lo, hi = _check_window(window)
    if mode not in ("union", "intersection", "at_least_k"):
        raise InvalidWindow(f"unknown mode {mode!r}")
    if mode == "at_least_k":
        if not isinstance(k, int) or isinstance(k, bool) or k < 1:
            raise InvalidWindow("at_least_k requires a positive integer k")

    online_intervals, points = _collect(
        view.observations, view.observations.devices(), lo, hi)
    member_intervals, member_points = _collect(
        view.membership, view.membership.devices(), lo, hi)
    points |= member_points

    breakpoints = sorted(points)
    segments: List[Segment] = []
    known_coverage_length = 0
    for point, nxt in zip(breakpoints, breakpoints[1:]):
        # half-open state on [point, nxt): a record starting at point counts,
        # one ending at point does not.
        online = {device for device, ranges in online_intervals.items()
                  if any(start <= point < end for start, end in ranges)}
        members = {device for device, ranges in member_intervals.items()
                   if any(start <= point < end for start, end in ranges)}

        online_members = tuple(sorted(online & members))
        if mode == "union":
            covered = bool(online_members)
        elif mode == "intersection":
            covered = bool(members) and len(online_members) == len(members)
        else:
            covered = len(online_members) >= k  # type: ignore[operator]

        evidence: List[str] = []
        if covered:
            for device in online_members:
                evidence.extend(record[0]
                                for record in view.observations.covering(device, (point, nxt)))
            known_coverage_length += nxt - point
        segments.append(Segment(
            start=point, end=nxt,
            online_members=online_members,
            online_non_members=tuple(sorted(online - members)),
            members=tuple(sorted(members)),
            covered=covered,
            evidence=tuple(evidence),
        ))

    window_length = hi - lo
    rate = Fraction(known_coverage_length, window_length) if window_length else Fraction(0)
    return QueryResult(
        window=(lo, hi), mode=mode, k=k,
        known_coverage_length=known_coverage_length,
        window_length=window_length,
        coverage_rate=rate,
        segments=tuple(segments),
    )
