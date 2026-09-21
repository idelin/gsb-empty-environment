"""Endpoint-indexed segment storage.

All report and membership boundaries share one sorted endpoint array.  A fact
change touches only segments covered by that fact; the full device histories
are never scanned.
"""

from __future__ import annotations

from bisect import bisect_left, bisect_right
from dataclasses import dataclass, field
from typing import Iterator, Optional

from .events import AddMembership, AddReport, RetractMembership, RetractReport
from .models import Membership, Number, Report

INF = float("inf")
NEG_INF = float("-inf")


@dataclass
class SegmentState:
    reports: dict[str, set[str]] = field(default_factory=dict)
    memberships: dict[str, dict[str, str]] = field(default_factory=dict)

    @classmethod
    def empty(cls) -> "SegmentState":
        return cls()

    def clone(self) -> "SegmentState":
        return SegmentState(
            reports={device: set(ids) for device, ids in self.reports.items()},
            memberships={
                device: dict(rules) for device, rules in self.memberships.items()
            },
        )

    def copy_from(self, other: "SegmentState") -> None:
        self.reports = {device: set(ids) for device, ids in other.reports.items()}
        self.memberships = {
            device: dict(rules) for device, rules in other.memberships.items()
        }

    def add_report(self, report: Report) -> None:
        self.reports.setdefault(report.device, set()).add(report.report_id)

    def remove_report(self, report: Report) -> None:
        ids = self.reports[report.device]
        ids.remove(report.report_id)
        if not ids:
            del self.reports[report.device]

    def add_membership(self, membership: Membership) -> None:
        rules = self.memberships.setdefault(membership.device, {})
        if membership.membership_id in rules:
            raise ValueError(f"duplicate membership id: {membership.membership_id}")
        rules[membership.membership_id] = membership.rule_version

    def remove_membership(self, membership: Membership) -> None:
        rules = self.memberships[membership.device]
        del rules[membership.membership_id]
        if not rules:
            del self.memberships[membership.device]

    @property
    def online_devices(self) -> set[str]:
        return set(self.reports)

    @property
    def member_devices(self) -> set[str]:
        return set(self.memberships)

    @property
    def eligible_devices(self) -> set[str]:
        return self.online_devices & self.member_devices

    def signature(self) -> tuple[object, object]:
        return (
            tuple(
                sorted(
                    (device, tuple(sorted(ids)))
                    for device, ids in self.reports.items()
                )
            ),
            tuple(
                sorted(
                    (
                        device,
                        tuple(sorted(rules.items())),
                    )
                    for device, rules in self.memberships.items()
                )
            ),
        )

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, SegmentState):
            return NotImplemented
        return self.signature() == other.signature()


@dataclass
class Slice:
    start: Number
    end: Number
    state: SegmentState


class EndpointIndex:
    def __init__(self) -> None:
        self.points: list[Number] = [NEG_INF, INF]
        self.refs: dict[Number, int] = {}
        self.reports: dict[str, Report] = {}
        self.memberships: dict[str, Membership] = {}
        self.report_ids_by_device: dict[str, set[str]] = {}
        self.membership_ids_by_device: dict[str, set[str]] = {}
        self.states: list[SegmentState] = [SegmentState.empty()]

    def clone(self) -> "EndpointIndex":
        cloned = EndpointIndex()
        cloned.points = list(self.points)
        cloned.refs = dict(self.refs)
        cloned.reports = dict(self.reports)
        cloned.memberships = dict(self.memberships)
        cloned.report_ids_by_device = {
            device: set(ids) for device, ids in self.report_ids_by_device.items()
        }
        cloned.membership_ids_by_device = {
            device: set(ids) for device, ids in self.membership_ids_by_device.items()
        }
        cloned.states = [state.clone() for state in self.states]
        return cloned

    def _segment_index(self, point: Number) -> int:
        return bisect_right(self.points, point) - 1

    def state_at(self, point: Number) -> SegmentState:
        return self.states[self._segment_index(point)]

    def _ensure_endpoint(self, point: Number) -> None:
        if point == NEG_INF or point == INF:
            return
        current = self.refs.get(point)
        if current is not None:
            self.refs[point] = current + 1
            return
        position = bisect_left(self.points, point)
        if position < len(self.points) and self.points[position] == point:
            self.refs[point] = 1
            return
        self.points.insert(position, point)
        self.states.insert(position, self.states[position - 1].clone())
        self.refs[point] = 1

    def _segment_span(self, start: Number, end: Number) -> range:
        first = bisect_right(self.points, start) - 1
        after = bisect_left(self.points, end)
        return range(first, after)

    def add_report(self, report: Report) -> None:
        if report.report_id in self.reports:
            raise ValueError(f"duplicate report id: {report.report_id}")
        self._ensure_endpoint(report.start)
        self._ensure_endpoint(report.end)
        for index in self._segment_span(report.start, report.end):
            self.states[index].add_report(report)
        self.reports[report.report_id] = report
        self.report_ids_by_device.setdefault(report.device, set()).add(
            report.report_id
        )

    def add_membership(self, membership: Membership) -> None:
        if membership.membership_id in self.memberships:
            raise ValueError(
                f"duplicate membership id: {membership.membership_id}"
            )
        self._ensure_endpoint(membership.start)
        if membership.end != INF:
            self._ensure_endpoint(membership.end)
        for index in self._segment_span(membership.start, membership.end):
            self.states[index].add_membership(membership)
        self.memberships[membership.membership_id] = membership
        self.membership_ids_by_device.setdefault(membership.device, set()).add(
            membership.membership_id
        )

    def _release_endpoint(self, point: Number) -> None:
        if point == NEG_INF or point == INF:
            return
        count = self.refs[point] - 1
        if count:
            self.refs[point] = count
            return
        del self.refs[point]
        position = bisect_left(self.points, point)
        del self.points[position]
        left = self.states.pop(position - 1)
        right_index = position - 1
        if left != self.states[right_index]:
            raise RuntimeError("endpoint merge failed due to inconsistent segment state")


    def _remove_unreferenced_endpoint(self, point: Number) -> None:
        if point in self.refs or point in (NEG_INF, INF):
            return
        position = bisect_left(self.points, point)
        del self.points[position]
        left = self.states.pop(position - 1)
        right_index = position - 1
        if left != self.states[right_index]:
            raise RuntimeError("endpoint merge failed due to inconsistent segment state")

    def remove_report(self, report_id: str) -> Report:
        report = self.reports.pop(report_id)
        ids_for_device = self.report_ids_by_device[report.device]
        ids_for_device.remove(report_id)
        if not ids_for_device:
            del self.report_ids_by_device[report.device]
        for index in self._segment_span(report.start, report.end):
            self.states[index].remove_report(report)
        for point in sorted(set((report.start, report.end))):
            self._release_endpoint(point)
        return report

    def remove_membership(self, membership_id: str) -> Membership:
        membership = self.memberships.pop(membership_id)
        ids_for_device = self.membership_ids_by_device[membership.device]
        ids_for_device.remove(membership_id)
        if not ids_for_device:
            del self.membership_ids_by_device[membership.device]
        for index in self._segment_span(membership.start, membership.end):
            self.states[index].remove_membership(membership)
        for point in sorted(set((membership.start, membership.end))):
            self._release_endpoint(point)
        return membership

    def apply_events(self, events: list) -> None:
        """Apply one revision atomically."""

        candidates: set[Number] = set()
        pending_refs = dict(self.refs)
        for event in events:
            if isinstance(event, RetractReport):
                report = self.reports.pop(event.report_id)
                ids_for_device = self.report_ids_by_device[report.device]
                ids_for_device.remove(report.report_id)
                if not ids_for_device:
                    del self.report_ids_by_device[report.device]
                for index in self._segment_span(report.start, report.end):
                    self.states[index].remove_report(report)
                for point in (report.start, report.end):
                    if point not in (NEG_INF, INF):
                        pending_refs[point] = pending_refs.get(point, 0) - 1
                        candidates.add(point)
            elif isinstance(event, RetractMembership):
                membership = self.memberships.pop(event.membership_id)
                ids_for_device = self.membership_ids_by_device[membership.device]
                ids_for_device.remove(event.membership_id)
                if not ids_for_device:
                    del self.membership_ids_by_device[membership.device]
                for index in self._segment_span(membership.start, membership.end):
                    self.states[index].remove_membership(membership)
                for point in (membership.start, membership.end):
                    if point not in (NEG_INF, INF):
                        pending_refs[point] = pending_refs.get(point, 0) - 1
                        candidates.add(point)

        def ensure_final_endpoint(point: Number) -> None:
            if point == NEG_INF or point == INF:
                return
            final_count = pending_refs.get(point, 0) + 1
            pending_refs[point] = final_count
            if point in self.points:
                return
            position = bisect_left(self.points, point)
            self.points.insert(position, point)
            self.states.insert(position, self.states[position - 1].clone())

        for event in events:
            if isinstance(event, AddReport):
                report = event.report
                if report.report_id in self.reports:
                    raise ValueError(f"duplicate report id: {report.report_id}")
                ensure_final_endpoint(report.start)
                ensure_final_endpoint(report.end)
                for index in self._segment_span(report.start, report.end):
                    self.states[index].add_report(report)
                self.reports[report.report_id] = report
                self.report_ids_by_device.setdefault(report.device, set()).add(
                    report.report_id
                )
            elif isinstance(event, AddMembership):
                membership = event.membership
                if membership.membership_id in self.memberships:
                    raise ValueError(
                        f"duplicate membership id: {membership.membership_id}"
                    )
                ensure_final_endpoint(membership.start)
                if membership.end != INF:
                    ensure_final_endpoint(membership.end)
                for index in self._segment_span(membership.start, membership.end):
                    self.states[index].add_membership(membership)
                self.memberships[membership.membership_id] = membership
                self.membership_ids_by_device.setdefault(membership.device, set()).add(
                    membership.membership_id
                )

        self.refs = {
            point: count for point, count in pending_refs.items() if count > 0
        }
        for point in sorted(candidates):
            self._remove_unreferenced_endpoint(point)

    def has_report(self, report_id: str) -> bool:
        return report_id in self.reports

    def has_membership(self, membership_id: str) -> bool:
        return membership_id in self.memberships

    def iter_slices(
        self, start: Number, end: Optional[Number] = None
    ) -> Iterator[Slice]:
        if end is None:
            end = INF
        index = self._segment_index(start)
        while index < len(self.states):
            segment_start = max(start, self.points[index])
            segment_end = min(end, self.points[index + 1])
            if segment_start >= end:
                return
            if segment_start < segment_end:
                yield Slice(
                    segment_start,
                    segment_end,
                    self.states[index],
                )
            index += 1

    def finite_endpoint_count(self) -> int:
        return len(self.refs)

    def segment_count(self) -> int:
        return len(self.states)


def clip_ranges(
    ranges: tuple[tuple[Number, Number], ...] | list[tuple[Number, Number]],
    start: Number,
    end: Number,
) -> tuple[tuple[Number, Number], ...]:
    clipped = []
    for left, right in ranges:
        bounded_left = max(left, start)
        bounded_right = min(right, end)
        if bounded_left < bounded_right:
            clipped.append((bounded_left, bounded_right))
    return tuple(merge_ranges(clipped))


def merge_ranges(
    ranges: list[tuple[Number, Number]]
) -> list[tuple[Number, Number]]:
    if not ranges:
        return []
    ordered = sorted(ranges)
    merged: list[list[Number]] = []
    for start, end in ordered:
        if not merged or start > merged[-1][1]:
            merged.append([start, end])
        elif end > merged[-1][1]:
            merged[-1][1] = end
    return [(start, end) for start, end in merged]
