"""Retroactive drift detection with exact arithmetic and revision chains.

Design
------
* Samples arrive in order and carry (source, seq, event_time, value).
* The baseline for judging a sample is the rolling mean / population
  variance of the last ``window`` *eligible* samples of the same source
  (arrival order).  Samples covered by an active anomaly segment are
  excluded from every baseline and are themselves not judged.
* Drift criterion (public, Shewhart-style): a sample drifts iff
  ``|x - mean| > k * sigma``.  All arithmetic is exact
  (``fractions.Fraction``); the comparison is evaluated in the squared
  form ``(x - mean)^2 > k^2 * var`` so no floating-point rounding can
  ever flip a borderline decision.
* Anomaly marks can be backfilled and retracted.  A change propagates
  only along real dependencies: results of the affected source are
  recomputed from the earliest covered sample forward, and propagation
  stops as soon as recomputed state converges with the stored state.
  Every changed result grows a revision chain that records the reason
  and the causing event.
* ``replay()`` recomputes every result from scratch under the current
  marks; ``verify_against_replay()`` proves the incremental revisions
  are equivalent to a full replay.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from decimal import Decimal
from enum import Enum
from fractions import Fraction
from typing import Optional, Union

Number = Union[int, str, float, Decimal, Fraction]


class Judgment(str, Enum):
    OK = "OK"
    DRIFT = "DRIFT"
    UNDECIDABLE = "UNDECIDABLE"
    EXCLUDED = "EXCLUDED"


def to_exact(value: Number) -> Fraction:
    """Convert a value to an exact Fraction.

    Strings and Decimals keep their decimal meaning ("0.1" == 1/10);
    floats are converted through their decimal string so that passing
    0.1 means 1/10, not the binary approximation.  bool is rejected.
    """
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool is not a valid measurement value")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, Decimal):
        return Fraction(value)
    if isinstance(value, float):
        return Fraction(str(value))
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"unsupported value type: {type(value)!r}")


@dataclass(frozen=True)
class Sample:
    source: str
    seq: int
    event_time: Union[int, str]
    value: Fraction

    @property
    def key(self) -> tuple[str, int]:
        return (self.source, self.seq)


@dataclass
class Segment:
    segment_id: str
    source: str
    start_seq: int
    end_seq: int
    reason: str
    active: bool = True

    def covers(self, source: str, seq: int) -> bool:
        return (
            self.active
            and self.source == source
            and self.start_seq <= seq <= self.end_seq
        )


@dataclass(frozen=True)
class Event:
    """An entry in the mark/retract evidence log."""

    event_id: int
    kind: str  # "mark" | "retract"
    segment_id: str
    source: str
    start_seq: int
    end_seq: int
    reason: str


@dataclass(frozen=True)
class Revision:
    """One version of a result.  A result's revisions form its chain."""

    version: int
    judgment: Judgment
    window_keys: tuple[tuple[str, int], ...]
    mean: Optional[Fraction]
    variance: Optional[Fraction]
    reason: str
    cause_event: Optional[int]


@dataclass
class ResultRecord:
    key: tuple[str, int]
    revisions: list[Revision] = field(default_factory=list)

    @property
    def current(self) -> Optional[Revision]:
        return self.revisions[-1] if self.revisions else None


@dataclass(frozen=True)
class _Outcome:
    judgment: Judgment
    window_keys: tuple[tuple[str, int], ...]
    mean: Optional[Fraction]
    variance: Optional[Fraction]


class DriftDetector:
    def __init__(
        self,
        window: int = 8,
        k: Number = 3,
        min_baseline: int = 2,
    ) -> None:
        if window < 1:
            raise ValueError("window must be >= 1")
        if min_baseline < 1:
            raise ValueError("min_baseline must be >= 1")
        self.window = window
        self.k = to_exact(k)
        if self.k <= 0:
            raise ValueError("k must be positive")
        self.min_baseline = min_baseline
        self._samples: list[Sample] = []
        self._segments: dict[str, Segment] = {}
        self._results: dict[tuple[str, int], ResultRecord] = {}
        self._events: list[Event] = []

    # ------------------------------------------------------------------ #
    # ingestion
    # ------------------------------------------------------------------ #
    def add_sample(
        self,
        source: str,
        seq: int,
        event_time: Union[int, str],
        value: Number,
    ) -> Judgment:
        sample = Sample(source, seq, event_time, to_exact(value))
        if sample.key in self._results:
            raise ValueError(f"duplicate sample key {sample.key!r}")
        self._samples.append(sample)
        self._results[sample.key] = ResultRecord(key=sample.key)
        self._commit(len(self._samples) - 1, "initial evaluation", None)
        return self._results[sample.key].current.judgment

    # ------------------------------------------------------------------ #
    # anomaly marks
    # ------------------------------------------------------------------ #
    def mark_anomaly(
        self,
        segment_id: str,
        source: str,
        start_seq: int,
        end_seq: int,
        reason: str = "",
    ) -> Optional[Event]:
        """Mark [start_seq, end_seq] of ``source`` anomalous.

        Idempotent: re-marking an already-active segment id is a no-op
        and returns None.  Re-marking a retracted id reactivates it.
        """
        if start_seq > end_seq:
            raise ValueError("start_seq must be <= end_seq")
        existing = self._segments.get(segment_id)
        if existing is not None and existing.active:
            return None
        old_ineligible = self._ineligible_keys(source)
        if existing is not None:
            if (
                existing.source != source
                or existing.start_seq != start_seq
                or existing.end_seq != end_seq
            ):
                raise ValueError(
                    f"segment id {segment_id!r} already used with a "
                    "different range"
                )
            existing.active = True
            existing.reason = reason or existing.reason
            event = self._log("mark", existing)
            self._propagate(existing, event, old_ineligible)
            return event
        segment = Segment(segment_id, source, start_seq, end_seq, reason)
        self._segments[segment_id] = segment
        event = self._log("mark", segment)
        self._propagate(segment, event, old_ineligible)
        return event

    def retract_mark(self, segment_id: str, reason: str = "") -> Optional[Event]:
        """Retract a mark.  Unknown or already-retracted ids are no-ops."""
        segment = self._segments.get(segment_id)
        if segment is None or not segment.active:
            return None
        old_ineligible = self._ineligible_keys(segment.source)
        segment.active = False
        event = self._log("retract", segment, reason)
        self._propagate(segment, event, old_ineligible)
        return event

    # ------------------------------------------------------------------ #
    # inspection
    # ------------------------------------------------------------------ #
    def result(self, source: str, seq: int) -> ResultRecord:
        return self._results[(source, seq)]

    def judgment(self, source: str, seq: int) -> Judgment:
        return self._results[(source, seq)].current.judgment

    def revision_chain(self, source: str, seq: int) -> list[Revision]:
        return list(self._results[(source, seq)].revisions)

    @property
    def events(self) -> list[Event]:
        return list(self._events)

    def is_excluded(self, source: str, seq: int) -> bool:
        return any(s.covers(source, seq) for s in self._segments.values())

    # ------------------------------------------------------------------ #
    # evaluation
    # ------------------------------------------------------------------ #
    def _eligible(self, sample: Sample) -> bool:
        return not any(
            s.covers(sample.source, sample.seq) for s in self._segments.values()
        )

    def _log(self, kind: str, segment: Segment, reason: str = "") -> Event:
        event = Event(
            event_id=len(self._events) + 1,
            kind=kind,
            segment_id=segment.segment_id,
            source=segment.source,
            start_seq=segment.start_seq,
            end_seq=segment.end_seq,
            reason=reason or segment.reason,
        )
        self._events.append(event)
        return event

    def _window_for(self, index: int) -> list[Sample]:
        sample = self._samples[index]
        prior = [
            p
            for p in self._samples[:index]
            if p.source == sample.source and self._eligible(p)
        ]
        return prior[-self.window :]

    def _compute(self, index: int) -> _Outcome:
        sample = self._samples[index]
        if not self._eligible(sample):
            return _Outcome(Judgment.EXCLUDED, (), None, None)
        window = self._window_for(index)
        keys = tuple(p.key for p in window)
        if len(window) < self.min_baseline:
            return _Outcome(Judgment.UNDECIDABLE, keys, None, None)
        n = len(window)
        mean = sum((p.value for p in window), Fraction(0)) / n
        var = sum(((p.value - mean) ** 2 for p in window), Fraction(0)) / n
        diff = sample.value - mean
        # Exact squared form of |x - mean| > k * sigma; also correct
        # when var == 0 (any deviation drifts).
        judgment = (
            Judgment.DRIFT if diff * diff > self.k * self.k * var else Judgment.OK
        )
        return _Outcome(judgment, keys, mean, var)

    def _commit(
        self, index: int, reason: str, cause_event: Optional[int]
    ) -> bool:
        """Recompute result at ``index``; append a revision if it changed.

        Returns True when the stored state changed (or was created).
        """
        sample = self._samples[index]
        outcome = self._compute(index)
        record = self._results[sample.key]
        current = record.current
        if (
            current is not None
            and current.judgment == outcome.judgment
            and current.window_keys == outcome.window_keys
        ):
            return False
        record.revisions.append(
            Revision(
                version=len(record.revisions) + 1,
                judgment=outcome.judgment,
                window_keys=outcome.window_keys,
                mean=outcome.mean,
                variance=outcome.variance,
                reason=reason,
                cause_event=cause_event,
            )
        )
        return True

    # ------------------------------------------------------------------ #
    # incremental propagation along real dependencies
    # ------------------------------------------------------------------ #
    def _ineligible_keys(self, source: str) -> set[tuple[str, int]]:
        return {
            s.key
            for s in self._samples
            if s.source == source and not self._eligible(s)
        }

    def _propagate(
        self,
        segment: Segment,
        event: Event,
        old_ineligible: set[tuple[str, int]],
    ) -> None:
        first = next(
            (
                i
                for i, s in enumerate(self._samples)
                if s.source == segment.source
                and segment.start_seq <= s.seq <= segment.end_seq
            ),
            None,
        )
        if first is None:
            return
        reason = (
            f"{event.kind} {segment.segment_id} "
            f"{segment.source}[{segment.start_seq}..{segment.end_seq}]"
            + (f": {event.reason}" if event.reason else "")
        )
        # Only results of this source can change (baselines are
        # per-source).  Recompute from the earliest covered sample
        # forward.  The full state that determines every later result is
        # the sliding window of the last ``window`` eligible samples;
        # track it under both the old and the new eligibility and stop
        # as soon as the two converge, because the window transition is
        # deterministic given eligibility.  (A single unchanged result
        # is NOT sufficient: an EXCLUDED result records no window, so a
        # covered sample can look unchanged while the downstream window
        # still shifts.)
        old_window: list[tuple[str, int]] = []
        new_window: list[tuple[str, int]] = []
        for i, sample in enumerate(self._samples):
            if sample.source != segment.source:
                continue
            if sample.key not in old_ineligible:
                old_window.append(sample.key)
                del old_window[: -self.window]
            if self._eligible(sample):
                new_window.append(sample.key)
                del new_window[: -self.window]
            if i < first:
                continue
            self._commit(i, reason, event.event_id)
            if old_window == new_window:
                break

    # ------------------------------------------------------------------ #
    # full replay & equivalence check
    # ------------------------------------------------------------------ #
    def replay(self) -> dict[tuple[str, int], Revision]:
        """Recompute every result from scratch under current marks."""
        out: dict[tuple[str, int], Revision] = {}
        for i, sample in enumerate(self._samples):
            outcome = self._compute(i)
            out[sample.key] = Revision(
                version=1,
                judgment=outcome.judgment,
                window_keys=outcome.window_keys,
                mean=outcome.mean,
                variance=outcome.variance,
                reason="full replay",
                cause_event=None,
            )
        return out

    def verify_against_replay(self) -> list[tuple[str, int]]:
        """Return keys whose incremental state differs from full replay."""
        fresh = self.replay()
        mismatches = []
        for key, record in self._results.items():
            current = record.current
            want = fresh[key]
            if (
                current.judgment != want.judgment
                or current.window_keys != want.window_keys
                or current.mean != want.mean
                or current.variance != want.variance
            ):
                mismatches.append(key)
        return mismatches

