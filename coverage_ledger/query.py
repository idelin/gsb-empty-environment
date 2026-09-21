"""Coverage queries over an endpoint index."""

from __future__ import annotations

from dataclasses import replace
from fractions import Fraction
from typing import Optional

from .index import EndpointIndex
from .models import (
    DeltaPatch,
    Evidence,
    Number,
    Observation,
    QueryKind,
    RevisionDelta,
    validate_finite_interval,
)


def normalize_query(kind: str, k: Optional[int]) -> tuple[QueryKind, Optional[int]]:
    if kind not in {"union", "intersection", "at_least_k"}:
        raise ValueError("kind must be union, intersection, or at_least_k")
    normalized_k: Optional[int]
    if kind == "at_least_k":
        if isinstance(k, bool) or not isinstance(k, int):
            raise ValueError("at_least_k requires an integer k")
        if k < 1:
            raise ValueError("at_least_k requires k >= 1")
        normalized_k = k
    else:
        normalized_k = None
    return kind, normalized_k  # type: ignore[return-value]


def evidence_for_state(start, end, state, kind, k):
    online = tuple(sorted(state.reports))
    members = tuple(sorted(state.memberships))
    eligible = tuple(device for device in online if device in state.memberships)
    member_count = len(members)
    active_count = len(eligible)

    if kind == "union":
        matched = active_count > 0
    elif kind == "intersection":
        matched = member_count > 0 and active_count == member_count
    else:
        if k is None:
            raise ValueError("at_least_k requires k")
        matched = active_count >= k

    return Evidence(
        start=start,
        end=end,
        length=end - start,
        matched=matched,
        active_count=active_count,
        member_count=member_count,
        online_devices=online,
        member_devices=members,
        eligible_devices=eligible,
        reports=tuple(
            sorted(
                (device, report_id)
                for device, ids in state.reports.items()
                for report_id in ids
            )
        ),
        memberships=tuple(
            sorted(
                (device, membership_id, rule_version)
                for device, rules in state.memberships.items()
                for membership_id, rule_version in rules.items()
            )
        ),
    )


def observe(index, *, revision, kind, window_start, window_end, k=None):
    start, end = validate_finite_interval(window_start, window_end)
    normalized_kind, normalized_k = normalize_query(kind, k)
    fragments = []
    for slice_item in index.iter_slices(start, end):
        fragments.append(
            evidence_for_state(
                slice_item.start,
                slice_item.end,
                slice_item.state,
                normalized_kind,
                normalized_k,
            )
        )

    covered = sum((fragment.length for fragment in fragments if fragment.matched), 0)
    window_length = end - start
    versions = tuple(
        sorted(
            {
                version
                for fragment in fragments
                for _, _, version in fragment.memberships
            }
        )
    )
    return Observation(
        revision=revision,
        kind=normalized_kind,
        k=normalized_k,
        window_start=start,
        window_end=end,
        window_length=window_length,
        covered_length=covered,
        coverage_rate=Fraction(covered, window_length),
        evidence=tuple(fragments),
        rule_versions=versions,
    )


def _containing(evidence, point):
    for fragment in evidence:
        if fragment.start <= point < fragment.end:
            return fragment
    raise AssertionError("evidence partition does not contain boundary point")


def observation_delta(before: Observation, after: Observation) -> RevisionDelta:
    identity = (before.kind, before.k, before.window_start, before.window_end)
    if identity != (after.kind, after.k, after.window_start, after.window_end):
        raise ValueError("observations must use the same query and window")

    boundaries = {before.window_start, before.window_end}
    boundaries.update(
        point
        for fragment in before.evidence + after.evidence
        for point in (fragment.start, fragment.end)
    )
    ordered_boundaries = sorted(boundaries)
    patches = []
    for left, right in zip(ordered_boundaries, ordered_boundaries[1:]):
        old_fragment = replace(
            _containing(before.evidence, left), start=left, end=right, length=right - left
        )
        new_fragment = replace(
            _containing(after.evidence, left), start=left, end=right, length=right - left
        )
        patches.append(DeltaPatch(old_fragment, new_fragment))

    return RevisionDelta(
        old_revision=before.revision,
        new_revision=after.revision,
        kind=before.kind,
        k=before.k,
        window_start=before.window_start,
        window_end=before.window_end,
        covered_before=before.covered_length,
        covered_after=after.covered_length,
        patches=tuple(patches),
    )
