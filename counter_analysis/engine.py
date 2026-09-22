"""Incremental engine: maintains per-source order and adjacent-interval
judgements under out-of-order inserts and retractions.

Only the intervals adjacent to an inserted/removed sample are revised;
each revision reports exactly which interval results are invalidated
(`removed`) and which replace them (`added`), so downstream consumers can
retract derived state precisely instead of recomputing everything.
"""

from __future__ import annotations

from bisect import bisect_left
from dataclasses import dataclass, field

from .delta import IntervalResult, judge_pair
from .model import CounterConfig, Sample


@dataclass(frozen=True)
class SourceTotal:
    """Aggregate for one source.

    definite_sum: sum of deltas over *decidable* intervals only.
    uncertain:    ids of undecidable intervals (their true contribution is
                  min_delta + k * 2**bit_width, k >= 0 unknown).
    complete:     True iff there are no undecidable intervals. When False,
                  definite_sum is a *partial* figure and must not be
                  presented as the source's total increment.
    """

    definite_sum: int
    min_sum: int  # definite_sum + min_delta of undecidable intervals
    uncertain: tuple[tuple[str, str, str], ...]

    @property
    def complete(self) -> bool:
        return not self.uncertain


@dataclass(frozen=True)
class Revision:
    """What one insert/retract changed."""

    op: str  # "insert" | "retract"
    sample_id: str
    source: str
    removed: tuple[IntervalResult, ...]  # invalidated interval results
    added: tuple[IntervalResult, ...]  # their replacements
    definite_total_delta: int  # change of the source's definite_sum
    config_version: str


class _SourceState:
    __slots__ = ("keys", "samples", "intervals")

    def __init__(self) -> None:
        self.keys: list[tuple[float, int, str]] = []
        self.samples: dict[str, Sample] = {}
        self.intervals: dict[tuple[str, str, str], IntervalResult] = {}


class IncrementalEngine:
    def __init__(self, config: CounterConfig) -> None:
        self.config = config
        self._states: dict[str, _SourceState] = {}

    # -- queries ---------------------------------------------------------

    def intervals(self, source: str) -> dict[tuple[str, str, str], IntervalResult]:
        state = self._states.get(source)
        return dict(state.intervals) if state else {}

    def total(self, source: str) -> SourceTotal:
        state = self._states.get(source)
        if not state:
            return SourceTotal(0, 0, ())
        definite = 0
        minimum = 0
        uncertain: list[tuple[str, str, str]] = []
        for iid, res in state.intervals.items():
            if res.certain:
                definite += res.delta  # type: ignore[operator]
                minimum += res.delta  # type: ignore[operator]
            else:
                minimum += res.min_delta
                uncertain.append(iid)
        return SourceTotal(definite, minimum, tuple(sorted(uncertain)))

    def samples(self, source: str) -> list[Sample]:
        """Samples of `source` in judgement order."""
        state = self._states.get(source)
        if not state:
            return []
        return [state.samples[k[2]] for k in state.keys]

    # -- mutations ---------------------------------------------------------

    def insert(self, sample: Sample) -> Revision:
        src_cfg = self.config.source(sample.source)
        if not (0 <= sample.count < src_cfg.modulus):
            raise ValueError(
                f"count {sample.count} out of range for {src_cfg.bit_width}-bit counter"
            )
        state = self._states.setdefault(sample.source, _SourceState())
        if sample.sample_id in state.samples:
            raise ValueError(f"duplicate sample_id {sample.sample_id!r}")

        pos = bisect_left(state.keys, sample.order_key)
        left = state.samples[state.keys[pos - 1][2]] if pos > 0 else None
        right = state.samples[state.keys[pos][2]] if pos < len(state.keys) else None

        removed: list[IntervalResult] = []
        added: list[IntervalResult] = []
        if left is not None and right is not None:
            removed.append(state.intervals.pop((sample.source, left.sample_id, right.sample_id)))
        if left is not None:
            res = judge_pair(left, sample, src_cfg, self.config.version)
            state.intervals[res.interval_id] = res
            added.append(res)
        if right is not None:
            res = judge_pair(sample, right, src_cfg, self.config.version)
            state.intervals[res.interval_id] = res
            added.append(res)

        state.keys.insert(pos, sample.order_key)
        state.samples[sample.sample_id] = sample
        return self._revision("insert", sample, removed, added)

    def retract(self, sample_id: str) -> Revision:
        for source, state in self._states.items():
            sample = state.samples.get(sample_id)
            if sample is not None:
                break
        else:
            raise KeyError(f"unknown sample_id {sample_id!r}")

        src_cfg = self.config.source(source)
        pos = state.keys.index(sample.order_key)
        left = state.samples[state.keys[pos - 1][2]] if pos > 0 else None
        right = (
            state.samples[state.keys[pos + 1][2]] if pos < len(state.keys) - 1 else None
        )

        removed: list[IntervalResult] = []
        added: list[IntervalResult] = []
        if left is not None:
            removed.append(state.intervals.pop((source, left.sample_id, sample_id)))
        if right is not None:
            removed.append(state.intervals.pop((source, sample_id, right.sample_id)))
        if left is not None and right is not None:
            res = judge_pair(left, right, src_cfg, self.config.version)
            state.intervals[res.interval_id] = res
            added.append(res)

        del state.keys[pos]
        del state.samples[sample_id]
        return self._revision("retract", sample, removed, added)

    # -- internals ---------------------------------------------------------

    def _revision(
        self,
        op: str,
        sample: Sample,
        removed: list[IntervalResult],
        added: list[IntervalResult],
    ) -> Revision:
        delta = sum(r.delta for r in added if r.certain) - sum(
            r.delta for r in removed if r.certain
        )
        return Revision(
            op=op,
            sample_id=sample.sample_id,
            source=sample.source,
            removed=tuple(removed),
            added=tuple(added),
            definite_total_delta=delta,
            config_version=self.config.version,
        )
