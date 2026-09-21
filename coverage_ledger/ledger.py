"""Append-only, revision-safe coverage ledger."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from .events import (
    AddMembership,
    AddReport,
    Event,
    RetractMembership,
    RetractReport,
    RevisionEntry,
)
from .index import INF, EndpointIndex, clip_ranges, merge_ranges
from .models import (
    Membership,
    Number,
    Observation,
    Report,
    RevisionDelta,
    RevisionImpact,
)
from .query import observation_delta, observe


def _overlaps(left: Membership, right: Membership) -> bool:
    return max(left.start, right.start) < min(left.end, right.end)


def _fact_key(fact):
    if isinstance(fact, Report):
        return ("Report", fact.device, fact.report_id)
    return ("Membership", fact.device, fact.rule_version)


def _fact_interval(fact: Report | Membership) -> tuple[Number, Number]:
    return fact.start, fact.end


@dataclass(frozen=True)
class CommitResult:
    impact: RevisionImpact
    events: tuple[Event, ...]


class CoverageLedger:
    def __init__(self) -> None:
        self.index = EndpointIndex()
        self.entries: list[RevisionEntry] = []

    @property
    def revision(self) -> int:
        return len(self.entries)

    def _normalize_events(self, events: list[Event] | tuple[Event, ...]):
        pending_reports: dict[str, Optional[Report]] = {}
        pending_memberships: dict[str, Optional[Membership]] = {}
        normalized: list[Event] = []
        event_ranges: list[tuple[Number, Number]] = []
        affected_before_reports: dict[str, Optional[Report]] = {}
        affected_before_memberships: dict[str, Optional[Membership]] = {}

        def report_for(report_id: str) -> Optional[Report]:
            if report_id in pending_reports:
                return pending_reports[report_id]
            return self.index.reports.get(report_id)

        def original_report(report_id: str) -> Optional[Report]:
            return self.index.reports.get(report_id)

        def membership_for(membership_id: str) -> Optional[Membership]:
            if membership_id in pending_memberships:
                return pending_memberships[membership_id]
            return self.index.memberships.get(membership_id)

        def original_membership(membership_id: str) -> Optional[Membership]:
            return self.index.memberships.get(membership_id)

        def overlaps_current_membership(membership: Membership) -> bool:
            for slice_item in self.index.iter_slices(
                membership.start, membership.end
            ):
                rules = slice_item.state.memberships.get(membership.device)
                if not rules:
                    continue
                for other_id in rules:
                    if other_id == membership.membership_id:
                        continue
                    if other_id in pending_memberships:
                        continue
                    return True
            for other in pending_memberships.values():
                if (
                    other is not None
                    and other.device == membership.device
                    and other.membership_id != membership.membership_id
                    and _overlaps(membership, other)
                ):
                    return True
            return False

        for event in events:
            if isinstance(event, AddReport):
                report = event.report
                existing = report_for(report.report_id)
                if existing is not None:
                    if existing == report:
                        continue
                    normalized.append(RetractReport(report.report_id))
                    event_ranges.append(_fact_interval(existing))
                else:
                    affected_before_reports.setdefault(
                        report.report_id, original_report(report.report_id)
                    )
                pending_reports[report.report_id] = report
                normalized.append(event)
                event_ranges.append(_fact_interval(report))
                if existing is not None:
                    affected_before_reports.setdefault(report.report_id, existing)

            elif isinstance(event, RetractReport):
                report_id = event.report_id
                existing = report_for(report_id)
                if existing is None:
                    raise ValueError(f"cannot retract unknown report: {report_id!r}")
                pending_reports[report_id] = None
                normalized.append(event)
                event_ranges.append(_fact_interval(existing))
                affected_before_reports.setdefault(report_id, existing)

            elif isinstance(event, AddMembership):
                membership = event.membership
                existing = membership_for(membership.membership_id)
                if existing is not None:
                    if existing == membership:
                        continue
                    if existing.device != membership.device:
                        raise ValueError(
                            f"membership id {membership.membership_id!r} cannot change device"
                        )
                    normalized.append(RetractMembership(membership.membership_id))
                    event_ranges.append(_fact_interval(existing))
                else:
                    affected_before_memberships.setdefault(
                        membership.membership_id,
                        original_membership(membership.membership_id),
                    )
                pending_memberships[membership.membership_id] = membership
                if overlaps_current_membership(membership):
                    raise ValueError(
                        "membership intervals for one device cannot overlap"
                    )
                normalized.append(event)
                event_ranges.append(_fact_interval(membership))
                if existing is not None:
                    affected_before_memberships.setdefault(
                        membership.membership_id, existing
                    )

            elif isinstance(event, RetractMembership):
                membership_id = event.membership_id
                existing = membership_for(membership_id)
                if existing is None:
                    raise ValueError(
                        f"cannot retract unknown membership: {membership_id!r}"
                    )
                pending_memberships[membership_id] = None
                normalized.append(event)
                event_ranges.append(_fact_interval(existing))
                affected_before_memberships.setdefault(membership_id, existing)
            else:
                raise TypeError(f"unsupported ledger event: {event!r}")

        return (
            normalized,
            merge_ranges(event_ranges),
            affected_before_reports,
            affected_before_memberships,
        )

    @staticmethod
    def _apply_event(index: EndpointIndex, event: Event) -> None:
        if isinstance(event, AddReport):
            index.add_report(event.report)
        elif isinstance(event, RetractReport):
            index.remove_report(event.report_id)
        elif isinstance(event, AddMembership):
            index.add_membership(event.membership)
        elif isinstance(event, RetractMembership):
            index.remove_membership(event.membership_id)
        else:
            raise TypeError(f"unsupported ledger event: {event!r}")

    @staticmethod
    def _changed_scope(
        old_reports: dict[str, Optional[Report]],
        new_reports: dict[str, Optional[Report]],
        old_memberships: dict[str, Optional[Membership]],
        new_memberships: dict[str, Optional[Membership]],
    ) -> tuple[tuple[Number, Number], ...]:
        pairs: list[tuple[Optional[object], Optional[object]]] = [
            (old_reports.get(identifier), new_reports.get(identifier))
            for identifier in set(old_reports) | set(new_reports)
        ]
        pairs.extend(
            (
                old_memberships.get(identifier),
                new_memberships.get(identifier),
            )
            for identifier in set(old_memberships) | set(new_memberships)
        )

        changed_pairs = [(old, new) for old, new in pairs if old != new]
        if not changed_pairs:
            return ()

        starts = [fact.start for old, new in changed_pairs for fact in (old, new) if fact]
        ends = [fact.end for old, new in changed_pairs for fact in (old, new) if fact]
        boundaries = sorted(set(starts + ends))
        ranges = []
        for left, right in zip(boundaries, boundaries[1:]):
            sample = left
            old_active = {
                _fact_key(old)
                for old, _ in changed_pairs
                if old is not None and old.start <= sample < old.end
            }
            new_active = {
                _fact_key(new)
                for _, new in changed_pairs
                if new is not None and new.start <= sample < new.end
            }
            if old_active != new_active:
                ranges.append((left, right))
        return tuple(merge_ranges(ranges))

    def commit(
        self,
        events: list[Event] | tuple[Event, ...],
        *,
        reason: str = "",
        query_windows: Optional[
            dict[object, tuple[str, Optional[int], Number, Number]]
        ] = None,
    ) -> Optional[CommitResult]:
        (
            normalized,
            event_scope,
            old_reports,
            old_memberships,
        ) = self._normalize_events(events)
        if not normalized:
            return None

        self.index.apply_events(normalized)

        new_reports = {
            identifier: self.index.reports.get(identifier)
            for identifier in set(old_reports)
        }
        new_memberships = {
            identifier: self.index.memberships.get(identifier)
            for identifier in set(old_memberships)
        }
        revised_scope = self._changed_scope(
            old_reports,
            new_reports,
            old_memberships,
            new_memberships,
        )
        revision = self.revision + 1
        windowed = {}
        for key, (_kind, _k, window_start, window_end) in (
            query_windows or {}
        ).items():
            windowed[key] = clip_ranges(
                revised_scope, window_start, window_end
            )
        impact = RevisionImpact(
            revision=revision,
            event_scope=tuple(event_scope),
            revised_scope=revised_scope,
            events=tuple(normalized),
            windowed_revised_scope=windowed,
        )
        self.entries.append(
            RevisionEntry(
                revision=revision,
                events=tuple(normalized),
                reason=reason,
            )
        )
        return CommitResult(impact=impact, events=tuple(normalized))

    def add_report(self, report: Report, *, reason: str = "", **kwargs):
        return self.commit([AddReport(report)], reason=reason, **kwargs)

    def retract_report(self, report_id: str, *, reason: str = "", **kwargs):
        return self.commit([RetractReport(report_id)], reason=reason, **kwargs)

    def replace_report(
        self, old_report_id: str, report: Report, *, reason: str = "", **kwargs
    ):
        return self.commit(
            [RetractReport(old_report_id), AddReport(report)],
            reason=reason,
            **kwargs,
        )

    def add_membership(self, membership: Membership, *, reason: str = "", **kwargs):
        return self.commit([AddMembership(membership)], reason=reason, **kwargs)

    def retract_membership(self, membership_id: str, *, reason: str = "", **kwargs):
        return self.commit(
            [RetractMembership(membership_id)], reason=reason, **kwargs
        )

    def close_membership(
        self,
        membership_id: str,
        end: Number,
        *,
        reason: str = "",
        **kwargs,
    ):
        existing = self.index.memberships[membership_id]
        closed = Membership(
            device=existing.device,
            start=existing.start,
            end=end,
            rule_version=existing.rule_version,
            membership_id=membership_id,
        )
        return self.commit(
            [RetractMembership(membership_id), AddMembership(closed)],
            reason=reason,
            **kwargs,
        )

    def replace_membership(
        self,
        old_membership_id: str,
        membership: Membership,
        *,
        reason: str = "",
        **kwargs,
    ):
        return self.commit(
            [
                RetractMembership(old_membership_id),
                AddMembership(membership),
            ],
            reason=reason,
            **kwargs,
        )

    def observe(
        self,
        *,
        kind: str,
        window_start: Number,
        window_end: Number,
        k: Optional[int] = None,
    ) -> Observation:
        return observe(
            self.index,
            revision=self.revision,
            kind=kind,
            window_start=window_start,
            window_end=window_end,
            k=k,
        )

    def index_at_revision(self, revision: int) -> EndpointIndex:
        if isinstance(revision, bool) or not isinstance(revision, int):
            raise TypeError("revision must be an integer")
        if revision < 0 or revision > self.revision:
            raise ValueError(f"revision must be in [0, {self.revision}]")
        rebuilt = EndpointIndex()
        for entry in self.entries[:revision]:
            for event in entry.events:
                self._apply_event(rebuilt, event)
        return rebuilt

    def observe_at_revision(
        self,
        revision: int,
        *,
        kind: str,
        window_start: Number,
        window_end: Number,
        k: Optional[int] = None,
    ) -> Observation:
        return observe(
            self.index_at_revision(revision),
            revision=revision,
            kind=kind,
            window_start=window_start,
            window_end=window_end,
            k=k,
        )

    def delta_for_observation(self, old_observation: Observation) -> RevisionDelta:
        current = self.observe_at_revision(
            self.revision,
            kind=old_observation.kind,
            window_start=old_observation.window_start,
            window_end=old_observation.window_end,
            k=old_observation.k,
        )
        return observation_delta(old_observation, current)

    def delta_between_revisions(
        self,
        old_revision: int,
        new_revision: int,
        *,
        kind: str,
        window_start: Number,
        window_end: Number,
        k: Optional[int] = None,
    ) -> RevisionDelta:
        if old_revision > new_revision:
            forward = self.delta_between_revisions(
                new_revision,
                old_revision,
                kind=kind,
                window_start=window_start,
                window_end=window_end,
                k=k,
            )
            return forward.inverse()
        before = self.observe_at_revision(
            old_revision,
            kind=kind,
            window_start=window_start,
            window_end=window_end,
            k=k,
        )
        after = self.observe_at_revision(
            new_revision,
            kind=kind,
            window_start=window_start,
            window_end=window_end,
            k=k,
        )
        return observation_delta(before, after)
