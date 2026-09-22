from __future__ import annotations

from collections.abc import Iterable, Sequence

from .intervals import ordered_samples
from .models import (
    CounterConfig,
    CounterSummary,
    IntervalResult,
    RunSummary,
    Sample,
    SourceSummary,
)


def summarize_runs(
    source: str,
    samples: Sequence[Sample],
    intervals: Sequence[IntervalResult],
) -> tuple[RunSummary, ...]:
    if not samples:
        return ()

    first = samples[0]
    current_run_id = (
        f"{source}:after-reset:{first.sample_id}"
        if first.reset
        else f"{source}:initial"
    )
    current_start = first.sample_id
    current_end = first.sample_id
    known_delta = 0
    definite_ids: list[str] = []
    unknown_ids: list[str] = []
    runs: list[RunSummary] = []
    has_open_run = True

    def close_run(end_id: str | None) -> None:
        nonlocal current_run_id, current_start, current_end
        nonlocal known_delta, definite_ids, unknown_ids
        nonlocal has_open_run
        if has_open_run:
            runs.append(
                RunSummary(
                    run_id=current_run_id,
                    start_sample_id=current_start,
                    end_sample_id=end_id,
                    known_delta=known_delta,
                    definite_interval_ids=tuple(definite_ids),
                    unknown_interval_ids=tuple(unknown_ids),
                    exact=not unknown_ids,
                )
            )
        has_open_run = False

    def open_run(run_id: str, start_id: str) -> None:
        nonlocal current_run_id, current_start, current_end
        nonlocal known_delta, definite_ids, unknown_ids, has_open_run
        current_run_id = run_id
        current_start = start_id
        current_end = start_id
        known_delta = 0
        definite_ids = []
        unknown_ids = []
        has_open_run = True

    sample_by_id = {sample.sample_id: sample for sample in samples}

    for interval in intervals:
        right = sample_by_id[interval.right_sample_id]

        if interval.status == "reset":
            close_run(interval.left_sample_id)
            open_run(f"{source}:after-reset:{right.sample_id}", right.sample_id)
            continue

        current_end = interval.left_sample_id
        if interval.status == "definite":
            assert interval.delta is not None
            known_delta += interval.delta
            definite_ids.append(interval.interval_id)
        else:
            unknown_ids.append(interval.interval_id)
        current_end = right.sample_id

    close_run(current_end if has_open_run else None)
    return tuple(runs)


def summarize(
    config: CounterConfig,
    samples: Iterable[Sample],
    intervals: Iterable[IntervalResult],
) -> CounterSummary:
    ordered = ordered_samples(samples)
    all_intervals = tuple(intervals)
    samples_by_source: dict[str, list[Sample]] = {}
    intervals_by_source: dict[str, list[IntervalResult]] = {}

    for sample in ordered:
        samples_by_source.setdefault(sample.source, []).append(sample)

    for interval in all_intervals:
        intervals_by_source.setdefault(interval.source, []).append(interval)

    source_summaries: list[SourceSummary] = []
    for source in config.sources:
        source_samples = samples_by_source.get(source, [])
        source_intervals = sorted(
            intervals_by_source.get(source, []),
            key=lambda interval: (interval.left_time, interval.right_time),
        )
        runs = summarize_runs(source, source_samples, source_intervals)
        definite_ids = tuple(
            interval.interval_id
            for interval in source_intervals
            if interval.status == "definite"
        )
        unknown_ids = tuple(
            interval.interval_id
            for interval in source_intervals
            if interval.status == "unknown_gap"
        )
        known_delta = sum(
            interval.delta
            for interval in source_intervals
            if interval.status == "definite" and interval.delta is not None
        )
        reset_boundaries = sum(
            1 for interval in source_intervals if interval.status == "reset"
        )
        source_summaries.append(
            SourceSummary(
                source=source,
                known_delta=known_delta,
                exact=not unknown_ids,
                reset_boundaries=reset_boundaries,
                unknown_interval_ids=unknown_ids,
                definite_interval_ids=definite_ids,
                runs=runs,
                config_version=config.version,
            )
        )

    return CounterSummary(
        known_delta=sum(source.known_delta for source in source_summaries),
        exact=all(source.exact for source in source_summaries),
        sources=tuple(source_summaries),
    )
