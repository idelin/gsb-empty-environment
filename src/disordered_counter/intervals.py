from __future__ import annotations

import hashlib
import json
from collections.abc import Iterable

from .models import (
    CounterConfig,
    CounterAnalysisError,
    IntervalResult,
    Sample,
    as_sample,
    sample_order_key,
)


def make_interval_id(source: str, left_id: str, right_id: str) -> str:
    payload = json.dumps(
        [source, left_id, right_id],
        separators=(",", ":"),
        ensure_ascii=False,
    ).encode("utf-8")
    digest = hashlib.sha256(payload).hexdigest()[:16]
    return f"iv:{digest}"


def ordered_samples(samples: Iterable[Sample]) -> list[Sample]:
    return sorted(samples, key=sample_order_key)


def validate_sample_set(
    config: CounterConfig, samples: Iterable[Sample]
) -> list[Sample]:
    ordered = ordered_samples(samples)
    seen_ids: set[str] = set()
    by_order: set[tuple[str, float | int, int]] = set()

    for sample in ordered:
        sample.with_config(config)
        if sample.sample_id in seen_ids:
            raise CounterAnalysisError(f"duplicate sample_id {sample.sample_id!r}")
        seen_ids.add(sample.sample_id)

        order_key = (sample.source, sample.event_time, sample.seq)
        if order_key in by_order:
            raise CounterAnalysisError(
                f"duplicate ordering key (source={sample.source!r}, "
                f"event_time={sample.event_time!r}, seq={sample.seq})"
            )
        by_order.add(order_key)

    return ordered


def classify_pair(
    config: CounterConfig,
    left: Sample,
    right: Sample,
) -> IntervalResult:
    if left.source != right.source:
        raise CounterAnalysisError("cannot classify samples from different sources")

    spec = config.source(right.source)
    interval_id = make_interval_id(right.source, left.sample_id, right.sample_id)
    gap = right.event_time - left.event_time

    if right.reset:
        return IntervalResult(
            interval_id=interval_id,
            source=right.source,
            left_sample_id=left.sample_id,
            right_sample_id=right.sample_id,
            left_time=left.event_time,
            right_time=right.event_time,
            left_count=left.count,
            right_count=right.count,
            delta=None,
            status="reset",
            config_version=config.version,
        )

    if gap > spec.max_gap:
        return IntervalResult(
            interval_id=interval_id,
            source=right.source,
            left_sample_id=left.sample_id,
            right_sample_id=right.sample_id,
            left_time=left.event_time,
            right_time=right.event_time,
            left_count=left.count,
            right_count=right.count,
            delta=None,
            status="unknown_gap",
            config_version=config.version,
        )

    raw_delta = right.count - left.count
    delta = raw_delta if raw_delta >= 0 else raw_delta + spec.modulus
    return IntervalResult(
        interval_id=interval_id,
        source=right.source,
        left_sample_id=left.sample_id,
        right_sample_id=right.sample_id,
        left_time=left.event_time,
        right_time=right.event_time,
        left_count=left.count,
        right_count=right.count,
        delta=delta,
        status="definite",
        config_version=config.version,
    )


def recompute_intervals(
    config: CounterConfig,
    samples: Iterable[Sample | dict],
) -> list[IntervalResult]:
    """Reference implementation: sort every active sample and rebuild edges."""

    ordered = validate_sample_set(config, (as_sample(s) for s in samples))
    intervals: list[IntervalResult] = []
    previous_by_source: dict[str, Sample] = {}

    for sample in ordered:
        previous = previous_by_source.get(sample.source)
        if previous is not None:
            intervals.append(classify_pair(config, previous, sample))
        previous_by_source[sample.source] = sample

    return intervals
