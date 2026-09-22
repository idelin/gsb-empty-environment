from __future__ import annotations

from collections.abc import Iterable

from .intervals import recompute_intervals
from .models import (
    CounterConfig,
    IntervalResult,
    ReferenceDiff,
    Revision,
    Sample,
    as_sample,
)


def materialize_revisions(revisions: Iterable[Revision]) -> dict[str, IntervalResult]:
    """Apply the revision log in order to reconstruct active intervals."""

    active: dict[str, IntervalResult] = {}
    for revision in revisions:
        if revision.action == "delete":
            for interval_id in revision.supersedes:
                active.pop(interval_id, None)
        else:
            if revision.interval is None:
                raise ValueError(
                    f"revision {revision.revision_id} upsert has no interval"
                )
            active[revision.interval.interval_id] = revision.interval
    return active


def compare_state(
    config: CounterConfig,
    active_samples: Iterable[Sample | dict],
    revisions: Iterable[Revision],
) -> ReferenceDiff:
    """Compare revision replay against a complete sort-and-recompute pass."""

    samples = tuple(as_sample(sample) for sample in active_samples)
    reference = {
        interval.interval_id: interval
        for interval in recompute_intervals(config, samples)
    }
    materialized = materialize_revisions(revisions)

    missing = tuple(
        interval_id for interval_id in reference if interval_id not in materialized
    )
    unexpected = tuple(
        interval_id for interval_id in materialized if interval_id not in reference
    )
    changed = tuple(
        interval_id
        for interval_id, reference_interval in reference.items()
        if (
            interval_id in materialized
            and materialized[interval_id] != reference_interval
        )
    )
    known_delta_delta = sum(
        interval.delta or 0 for interval in materialized.values()
    ) - sum(interval.delta or 0 for interval in reference.values())

    return ReferenceDiff(
        matches=not missing
        and not unexpected
        and not changed
        and known_delta_delta == 0,
        missing_interval_ids=missing,
        unexpected_interval_ids=unexpected,
        changed_interval_ids=changed,
        summary_known_delta_delta=known_delta_delta,
    )
