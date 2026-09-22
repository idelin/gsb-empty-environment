from __future__ import annotations

from bisect import bisect_left
from collections.abc import Iterable, Mapping

from .intervals import classify_pair, make_interval_id, validate_sample_set
from .models import (
    CounterConfig,
    CounterSummary,
    DELETE,
    DuplicateSampleError,
    IntervalResult,
    MissingSampleError,
    RETRACT,
    ReferenceDiff,
    Revision,
    Sample,
    SourceSpec,
    UPSERT,
    INSERT,
    as_sample,
    copy_config_sources,
)
from .reference import compare_state
from .summary import summarize


class CounterAnalyzer:
    """Incrementally maintain adjacent per-source counter intervals."""

    def __init__(
        self,
        sources: Mapping[str, SourceSpec | tuple[int, float | int]],
        config_version: str = "v1",
    ) -> None:
        self.config = CounterConfig(copy_config_sources(sources), config_version)
        self._samples: dict[str, Sample] = {}
        self._ordered: dict[str, list[Sample]] = {
            name: [] for name in self.config.sources
        }
        self._intervals: dict[str, IntervalResult] = {}
        self._revisions: list[Revision] = []

    @property
    def revisions(self) -> tuple[Revision, ...]:
        return tuple(self._revisions)

    @property
    def active_intervals(self) -> tuple[IntervalResult, ...]:
        return tuple(
            self._intervals[interval_id]
            for interval_id in sorted(
                self._intervals,
                key=lambda value: (
                    self._intervals[value].left_time,
                    self._intervals[value].source,
                    value,
                ),
            )
        )

    def add_sample(self, sample: Sample | Mapping[str, object]) -> tuple[Revision, ...]:
        return self.add_samples([sample])

    def add_samples(
        self,
        samples: Iterable[Sample | Mapping[str, object]],
    ) -> tuple[Revision, ...]:
        prepared = [as_sample(sample).with_config(self.config) for sample in samples]
        self._validate_new_samples(prepared)
        revisions: list[Revision] = []
        for sample in prepared:
            revisions.extend(self._insert_sample(sample))
        return tuple(revisions)

    def retract_sample(self, sample_id: str) -> tuple[Revision, ...]:
        if sample_id not in self._samples:
            raise MissingSampleError(f"sample {sample_id!r} is not active")

        sample = self._samples[sample_id]
        source_samples = self._ordered[sample.source]
        index = self._find_index(source_samples, sample)
        previous = source_samples[index - 1] if index > 0 else None
        following = (
            source_samples[index + 1] if index + 1 < len(source_samples) else None
        )

        revisions: list[Revision] = []
        if previous is not None:
            revisions.append(
                self._delete_edge(
                    previous,
                    sample,
                    sample,
                    RETRACT,
                )
            )
        if following is not None:
            revisions.append(
                self._delete_edge(
                    sample,
                    following,
                    sample,
                    RETRACT,
                )
            )
        if previous is not None and following is not None:
            revisions.append(
                self._upsert_edge(
                    previous,
                    following,
                    sample,
                    RETRACT,
                )
            )

        source_samples.pop(index)
        del self._samples[sample_id]
        return tuple(revisions)

    def summary(self) -> CounterSummary:
        return summarize(self.config, self._samples.values(), self._intervals.values())

    def active_samples(self) -> tuple[Sample, ...]:
        result: list[Sample] = []
        for source in self.config.sources:
            result.extend(self._ordered[source])
        return tuple(result)

    def recompute_reference(self) -> tuple[IntervalResult, ...]:
        from .intervals import recompute_intervals

        return tuple(recompute_intervals(self.config, self.active_samples()))

    def reference_diff(self) -> ReferenceDiff:
        return compare_state(
            self.config,
            self.active_samples(),
            tuple(self._revisions),
        )

    def matches_reference(self) -> bool:
        return self.reference_diff().matches

    def _validate_new_samples(self, prepared: list[Sample]) -> None:
        incoming_ids: set[str] = set()
        for sample in prepared:
            if sample.sample_id in self._samples or sample.sample_id in incoming_ids:
                raise DuplicateSampleError(f"duplicate sample_id {sample.sample_id!r}")
            incoming_ids.add(sample.sample_id)

        combined = [*self._samples.values(), *prepared]
        validate_sample_set(self.config, combined)

    def _insert_sample(self, sample: Sample) -> tuple[Revision, ...]:
        source_samples = self._ordered[sample.source]
        index = bisect_left(
            source_samples,
            (sample.event_time, sample.seq, sample.sample_id),
            key=lambda item: (item.event_time, item.seq, item.sample_id),
        )
        previous = source_samples[index - 1] if index > 0 else None
        following = source_samples[index] if index < len(source_samples) else None

        revisions: list[Revision] = []
        if previous is not None and following is not None:
            revisions.append(self._delete_edge(previous, following, sample, INSERT))
        if previous is not None:
            revisions.append(self._upsert_edge(previous, sample, sample, INSERT))
        if following is not None:
            revisions.append(self._upsert_edge(sample, following, sample, INSERT))

        source_samples.insert(index, sample)
        self._samples[sample.sample_id] = sample
        return tuple(revisions)

    @staticmethod
    def _find_index(source_samples: list[Sample], sample: Sample) -> int:
        index = bisect_left(
            source_samples,
            (sample.event_time, sample.seq, sample.sample_id),
            key=lambda item: (item.event_time, item.seq, item.sample_id),
        )
        if (
            index >= len(source_samples)
            or source_samples[index].sample_id != sample.sample_id
        ):
            raise RuntimeError(
                f"active index not found for sample {sample.sample_id!r}"
            )
        return index

    def _next_revision(
        self,
        operation: str,
        trigger: Sample,
        action: str,
        interval: IntervalResult | None,
        supersedes: tuple[str, ...],
    ) -> Revision:
        revision = Revision(
            revision_id=len(self._revisions) + 1,
            operation=operation,
            trigger_sample_id=trigger.sample_id,
            source=trigger.source,
            action=action,
            interval=interval,
            supersedes=supersedes,
            config_version=self.config.version,
        )
        self._revisions.append(revision)
        return revision

    def _upsert_edge(
        self,
        left: Sample,
        right: Sample,
        trigger: Sample,
        operation: str,
    ) -> Revision:
        interval = classify_pair(self.config, left, right)
        self._intervals[interval.interval_id] = interval
        return self._next_revision(
            operation,
            trigger,
            UPSERT,
            interval,
            (),
        )

    def _delete_edge(
        self,
        left: Sample,
        right: Sample,
        trigger: Sample,
        operation: str,
    ) -> Revision:
        interval_id = make_interval_id(right.source, left.sample_id, right.sample_id)
        old_interval = self._intervals.pop(interval_id)
        return self._next_revision(
            operation,
            trigger,
            DELETE,
            None,
            (old_interval.interval_id,),
        )
