"""Immutable value objects for the revision-safe coverage ledger."""

from __future__ import annotations

from dataclasses import dataclass, field, replace
from fractions import Fraction
from typing import Literal, Optional, Union

Number = Union[int, float]
OptionalEnd = Optional[Number]
QueryKind = Literal["union", "intersection", "at_least_k"]


def _canonical_number(value: object) -> Number:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise TypeError("time endpoints must be int or float")
    if isinstance(value, float) and value != value:
        raise ValueError("time endpoints must not be NaN")
    return value


def validate_finite_interval(start: object, end: object) -> tuple[Number, Number]:
    start_value = _canonical_number(start)
    end_value = _canonical_number(end)
    if start_value >= end_value:
        raise ValueError("interval must have positive length: start < end")
    return start_value, end_value


def normalize_end(end: object = None) -> Number:
    if end is None:
        return float("inf")
    return _canonical_number(end)


@dataclass(frozen=True)
class Report:
    """A half-open online interval ``[start, end)`` reported by one device."""

    device: str
    start: Number
    end: Number
    report_id: str

    def __post_init__(self) -> None:
        if not isinstance(self.device, str) or not self.device:
            raise ValueError("device must be a non-empty string")
        if not isinstance(self.report_id, str) or not self.report_id:
            raise ValueError("report_id must be a non-empty string")
        start, end = validate_finite_interval(self.start, self.end)
        object.__setattr__(self, "start", start)
        object.__setattr__(self, "end", end)


@dataclass(frozen=True)
class Membership:
    """Membership during ``[start, end)`` under one membership rule version."""

    device: str
    start: Number
    end: Number
    rule_version: str
    membership_id: str

    def __post_init__(self) -> None:
        if not isinstance(self.device, str) or not self.device:
            raise ValueError("device must be a non-empty string")
        if not isinstance(self.rule_version, str) or not self.rule_version:
            raise ValueError("rule_version must be a non-empty string")
        if not isinstance(self.membership_id, str) or not self.membership_id:
            raise ValueError("membership_id must be a non-empty string")
        start = _canonical_number(self.start)
        end = normalize_end(self.end)
        if start >= end:
            raise ValueError("membership interval must have positive length")
        object.__setattr__(self, "start", start)
        object.__setattr__(self, "end", end)


@dataclass(frozen=True)
class Evidence:
    start: Number
    end: Number
    length: Number
    matched: bool
    active_count: int
    member_count: int
    online_devices: tuple[str, ...]
    member_devices: tuple[str, ...]
    eligible_devices: tuple[str, ...]
    reports: tuple[tuple[str, str], ...]
    memberships: tuple[tuple[str, str, str], ...]

    def signature(self) -> tuple[object, ...]:
        return (
            self.matched,
            self.active_count,
            self.member_count,
            self.online_devices,
            self.member_devices,
            self.eligible_devices,
            self.reports,
            self.memberships,
        )


@dataclass(frozen=True)
class Observation:
    revision: int
    kind: QueryKind
    k: Optional[int]
    window_start: Number
    window_end: Number
    window_length: Number
    covered_length: Number
    coverage_rate: Fraction
    evidence: tuple[Evidence, ...]
    rule_versions: tuple[str, ...] = field(default_factory=tuple)

    @property
    def known_covered_length(self) -> Number:
        return self.covered_length

    @property
    def coverage(self) -> Fraction:
        return self.coverage_rate


@dataclass(frozen=True)
class EvidenceChange:
    before: Evidence
    after: Evidence


@dataclass(frozen=True)
class DeltaPatch:
    before: Evidence
    after: Evidence

    @property
    def change_type(self) -> str:
        if not self.before.matched and self.after.matched:
            return "added"
        if self.before.matched and not self.after.matched:
            return "removed"
        return "evidence_changed"


@dataclass(frozen=True)
class RevisionDelta:
    """Reversible fragment difference for one published query window."""

    old_revision: int
    new_revision: int
    kind: QueryKind
    k: Optional[int]
    window_start: Number
    window_end: Number
    covered_before: Number
    covered_after: Number
    patches: tuple[DeltaPatch, ...]

    @property
    def added(self) -> tuple[Evidence, ...]:
        return tuple(
            patch.after
            for patch in self.patches
            if patch.change_type == "added"
        )

    @property
    def removed(self) -> tuple[Evidence, ...]:
        return tuple(
            patch.before
            for patch in self.patches
            if patch.change_type == "removed"
        )

    @property
    def evidence_changed(self) -> tuple[EvidenceChange, ...]:
        return tuple(
            EvidenceChange(patch.before, patch.after)
            for patch in self.patches
            if patch.change_type == "evidence_changed"
        )

    @property
    def covered_delta(self) -> Number:
        added_length = sum((item.length for item in self.added), 0)
        removed_length = sum((item.length for item in self.removed), 0)
        return added_length - removed_length

    @property
    def window(self) -> tuple[Number, Number]:
        return self.window_start, self.window_end

    def inverse(self) -> "RevisionDelta":
        return RevisionDelta(
            old_revision=self.new_revision,
            new_revision=self.old_revision,
            kind=self.kind,
            k=self.k,
            window_start=self.window_start,
            window_end=self.window_end,
            covered_before=self.covered_after,
            covered_after=self.covered_before,
            patches=tuple(
                DeltaPatch(before=item.after, after=item.before)
                for item in self.patches
            ),
        )

    def apply(self, observation: Observation) -> Observation:
        """Apply this delta to the matching old observation."""

        if observation.revision != self.old_revision:
            raise ValueError("observation revision does not match delta")
        if (
            observation.kind != self.kind
            or observation.k != self.k
            or observation.window_start != self.window_start
            or observation.window_end != self.window_end
            or observation.covered_length != self.covered_before
        ):
            raise ValueError("observation identity does not match delta")

        ordered = sorted(
            self.patches, key=lambda item: (item.before.start, item.before.end)
        )
        if not ordered:
            return observation
        if ordered[0].before.start != self.window_start:
            raise ValueError("delta does not start at window boundary")
        if ordered[-1].before.end != self.window_end:
            raise ValueError("delta does not end at window boundary")

        cursor = self.window_start
        output: list[Evidence] = []
        for patch in ordered:
            if patch.before.start != cursor:
                raise ValueError("delta patches are not contiguous")
            containing = next(
                (
                    fragment
                    for fragment in observation.evidence
                    if fragment.start <= patch.before.start
                    and fragment.end >= patch.before.end
                ),
                None,
            )
            if containing is None:
                raise ValueError("delta references missing old evidence")
            if containing.signature() != patch.before.signature():
                raise ValueError("delta before-image does not match observation")
            output.append(patch.after)
            cursor = patch.before.end

        merged: list[Evidence] = []
        for fragment in output:
            if (
                merged
                and merged[-1].end == fragment.start
                and merged[-1].signature() == fragment.signature()
            ):
                previous = merged.pop()
                fragment = Evidence(
                    start=previous.start,
                    end=fragment.end,
                    length=fragment.end - previous.start,
                    matched=previous.matched,
                    active_count=previous.active_count,
                    member_count=previous.member_count,
                    online_devices=previous.online_devices,
                    member_devices=previous.member_devices,
                    eligible_devices=previous.eligible_devices,
                    reports=previous.reports,
                    memberships=previous.memberships,
                )
            merged.append(fragment)

        covered = sum((fragment.length for fragment in merged if fragment.matched), 0)
        if covered != self.covered_after:
            raise ValueError("delta covered length is inconsistent")
        versions = tuple(
            sorted(
                {
                    version
                    for fragment in merged
                    for _, _, version in fragment.memberships
                }
            )
        )
        return Observation(
            revision=self.new_revision,
            kind=self.kind,
            k=self.k,
            window_start=self.window_start,
            window_end=self.window_end,
            window_length=observation.window_length,
            covered_length=covered,
            coverage_rate=Fraction(covered, observation.window_length),
            evidence=tuple(merged),
            rule_versions=versions,
        )


@dataclass(frozen=True)
class RevisionImpact:
    revision: int
    event_scope: tuple[tuple[Number, Number], ...]
    revised_scope: tuple[tuple[Number, Number], ...]
    events: tuple[object, ...]
    windowed_revised_scope: dict[tuple[object, ...], tuple[tuple[Number, Number], ...]] = field(
        default_factory=dict
    )

