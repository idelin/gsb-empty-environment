"""Incremental counter store.

Maintains per-source ordered samples and the diffs between adjacent pairs.
Late inserts and retractions only revise the diffs adjacent to the touched
position; every change is reported as a :class:`Revision` so downstream
consumers know exactly which interval results are voided and which replace
them.
"""

from __future__ import annotations

from bisect import bisect_left
from dataclasses import dataclass, field

from .model import Diff, DiffStatus, Sample, SourceConfig, compute_diff


@dataclass(frozen=True)
class Revision:
    """The delta of diff-judgements caused by one mutation.

    ``invalidated`` lists diff results that no longer exist (downstream must
    discard them); ``emitted`` lists their replacements.
    """

    source: str
    op: str
    sample_id: str
    invalidated: tuple[Diff, ...] = ()
    emitted: tuple[Diff, ...] = ()


@dataclass(frozen=True)
class SourceTotals:
    """Aggregate over a source's live diffs.

    ``determined_total`` sums only DETERMINED and RESET deltas. Uncertain
    intervals are never silently dropped: they are listed explicitly and
    ``complete`` is False while any exist, so a partial total can never be
    mistaken for a full one.
    """

    source: str
    determined_total: int
    uncertain_intervals: tuple[tuple[str | None, str], ...]
    complete: bool
    config_version: str


@dataclass
class _SourceState:
    config: SourceConfig
    samples: dict[str, Sample] = field(default_factory=dict)
    order: list[Sample] = field(default_factory=list)  # sorted by order_key


class CounterStore:
    def __init__(self) -> None:
        self._sources: dict[str, _SourceState] = {}

    # -- configuration ----------------------------------------------------

    def configure(self, source: str, config: SourceConfig) -> Revision:
        """(Re)configure a source. Re-judges every interval on reconfigure."""
        state = self._sources.get(source)
        if state is None:
            self._sources[source] = _SourceState(config=config)
            return Revision(source, "configure", "", (), ())
        old_diffs = self._diffs_of(state)
        state.config = config
        new_diffs = self._diffs_of(state)
        return Revision(source, "reconfigure", "", tuple(old_diffs), tuple(new_diffs))

    def config(self, source: str) -> SourceConfig:
        return self._state(source).config

    # -- mutations ----------------------------------------------------------

    def insert(self, sample: Sample) -> Revision:
        """Insert a (possibly late/out-of-order) sample.

        Only the interval that gets split is invalidated; the two new
        adjacent intervals are emitted.
        """
        state = self._state(sample.source)
        if sample.sample_id in state.samples:
            raise ValueError(f"duplicate sample id: {sample.sample_id!r}")
        pos = bisect_left(state.order, sample.order_key(),
                          key=lambda s: s.order_key())
        left = state.order[pos - 1] if pos > 0 else None
        right = state.order[pos] if pos < len(state.order) else None

        invalidated: list[Diff] = []
        if left is not None and right is not None:
            invalidated.append(compute_diff(left, right, state.config))
        elif right is not None:
            invalidated.append(compute_diff(None, right, state.config))

        state.samples[sample.sample_id] = sample
        state.order.insert(pos, sample)

        emitted = [compute_diff(left, sample, state.config)]
        if right is not None:
            emitted.append(compute_diff(sample, right, state.config))
        return Revision(sample.source, "insert", sample.sample_id,
                        tuple(invalidated), tuple(emitted))

    def retract(self, source: str, sample_id: str) -> Revision:
        """Withdraw a sample; only its two adjacent intervals are revised."""
        state = self._state(source)
        sample = state.samples.get(sample_id)
        if sample is None:
            raise KeyError(f"unknown sample id: {sample_id!r}")
        pos = bisect_left(state.order, sample.order_key(),
                          key=lambda s: s.order_key())
        left = state.order[pos - 1] if pos > 0 else None
        right = state.order[pos + 1] if pos + 1 < len(state.order) else None

        invalidated = [compute_diff(left, sample, state.config)]
        if right is not None:
            invalidated.append(compute_diff(sample, right, state.config))

        del state.samples[sample_id]
        state.order.pop(pos)

        emitted: list[Diff] = []
        if right is not None:
            emitted.append(compute_diff(left, right, state.config))
        return Revision(source, "retract", sample_id,
                        tuple(invalidated), tuple(emitted))

    # -- queries ------------------------------------------------------------

    def diffs(self, source: str) -> list[Diff]:
        """All live diff judgements for a source, in stream order."""
        return self._diffs_of(self._state(source))

    def totals(self, source: str) -> SourceTotals:
        state = self._state(source)
        total = 0
        uncertain: list[tuple[str | None, str]] = []
        for diff in self._diffs_of(state):
            if diff.status in (DiffStatus.DETERMINED, DiffStatus.RESET):
                assert diff.delta is not None
                total += diff.delta
            elif diff.status is DiffStatus.UNCERTAIN:
                uncertain.append(diff.key)
        return SourceTotals(
            source=source,
            determined_total=total,
            uncertain_intervals=tuple(uncertain),
            complete=not uncertain,
            config_version=state.config.config_version,
        )

    # -- internals ----------------------------------------------------------

    def _state(self, source: str) -> _SourceState:
        state = self._sources.get(source)
        if state is None:
            raise KeyError(f"source not configured: {source!r}")
        return state

    @staticmethod
    def _diffs_of(state: _SourceState) -> list[Diff]:
        out: list[Diff] = []
        prev: Sample | None = None
        for sample in state.order:
            out.append(compute_diff(prev, sample, state.config))
            prev = sample
        return out
