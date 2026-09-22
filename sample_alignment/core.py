from __future__ import annotations

from bisect import bisect_left, insort
from dataclasses import dataclass, field
from enum import Enum
import heapq
from typing import Iterable, Mapping, Sequence


class EventType(str, Enum):
    SAMPLE_ADDED = "sample_added"
    SAMPLE_RETRACTED = "sample_retracted"
    ANCHOR_ADDED = "anchor_added"
    ANCHOR_RETRACTED = "anchor_retracted"
    WATERMARK = "watermark"


class ChangeReason(str, Enum):
    ANCHOR_ADDED = "anchor_added"
    SAMPLE_ADDED = "sample_added"
    SAMPLE_RETRACTED = "sample_retracted"
    ANCHOR_RETRACTED = "anchor_retracted"
    WATERMARK = "watermark"


class AlignmentOperation(str, Enum):
    UPSERT = "upsert"
    DELETE = "delete"


@dataclass(frozen=True)
class Sample:
    sample_id: str
    source: str
    timestamp: int
    seq: int

    def index_key(self) -> tuple[int, int, str]:
        return (self.timestamp, self.seq, self.sample_id)


@dataclass(frozen=True)
class SampleAdded:
    sample_id: str
    source: str
    timestamp: int
    seq: int
    kind: EventType = field(default=EventType.SAMPLE_ADDED, init=False)

    def sample(self) -> Sample:
        return Sample(self.sample_id, self.source, self.timestamp, self.seq)


@dataclass(frozen=True)
class SampleRetracted:
    sample_id: str
    kind: EventType = field(default=EventType.SAMPLE_RETRACTED, init=False)


@dataclass(frozen=True)
class AnchorAdded:
    anchor_id: str
    timestamp: int
    seq: int
    kind: EventType = field(default=EventType.ANCHOR_ADDED, init=False)


@dataclass(frozen=True)
class AnchorRetracted:
    anchor_id: str
    kind: EventType = field(default=EventType.ANCHOR_RETRACTED, init=False)


@dataclass(frozen=True)
class WatermarkAdvanced:
    timestamp: int
    kind: EventType = field(default=EventType.WATERMARK, init=False)


@dataclass(frozen=True)
class AlignmentResult:
    anchor_id: str
    anchor_seq: int
    anchor_timestamp: int
    matches: tuple[tuple[str, str | None], ...]
    missing_sources: tuple[str, ...]
    version: int
    finalized: bool

    def sample_id(self, source: str) -> str | None:
        for candidate_source, sample_id in self.matches:
            if candidate_source == source:
                return sample_id
        raise KeyError(source)

    def as_table(self) -> dict[str, str | None]:
        return dict(self.matches)


@dataclass(frozen=True)
class AlignmentEvent:
    event_seq: int
    operation: AlignmentOperation
    reason: ChangeReason
    result: AlignmentResult
    previous_version: int | None = None


@dataclass
class _Candidate:
    sample: Sample
    active_references: int
    expiry: int


@dataclass
class _AnchorState:
    anchor_id: str
    seq: int
    timestamp: int
    close_time: int
    version: int
    matches: dict[str, str | None]
    sources: Sequence[str]
    finalized: bool = False

    def result(self) -> AlignmentResult:
        return _make_result(
            self.anchor_id,
            self.seq,
            self.timestamp,
            self.matches,
            self.version,
            self.finalized,
            self.sources,
        )


def _make_result(
    anchor_id: str,
    anchor_seq: int,
    anchor_timestamp: int,
    matches: Mapping[str, str | None],
    version: int,
    finalized: bool,
    sources: Sequence[str],
) -> AlignmentResult:
    ordered_matches = tuple((source, matches.get(source)) for source in sources)
    missing = tuple(source for source in sources if matches.get(source) is None)
    return AlignmentResult(
        anchor_id,
        anchor_seq,
        anchor_timestamp,
        ordered_matches,
        missing,
        version,
        finalized,
    )


class AlignmentStore:
    def __init__(
        self,
        auxiliary_sources: Sequence[str],
        tolerance: int | Mapping[str, int],
    ) -> None:
        self._sources = tuple(dict.fromkeys(auxiliary_sources))
        if not self._sources:
            raise ValueError("at least one auxiliary source is required")
        if isinstance(tolerance, Mapping):
            missing = [source for source in self._sources if source not in tolerance]
            if missing:
                raise ValueError(f"missing tolerance for sources: {', '.join(missing)}")
            tolerances = {source: int(tolerance[source]) for source in self._sources}
        else:
            value = int(tolerance)
            tolerances = {source: value for source in self._sources}
        if any(value < 0 for value in tolerances.values()):
            raise ValueError("tolerance must be non-negative")

        self._tolerances = tolerances
        self._max_tolerance = max(tolerances.values())
        self._watermark: int | None = None
        self._event_seq = 0
        self._anchors: dict[str, _AnchorState] = {}
        self._candidates: dict[str, _Candidate] = {}
        self._active_anchors: list[tuple[int, int, str]] = []
        self._candidate_index: dict[str, list[tuple[int, int, str]]] = {
            source: [] for source in self._sources
        }
        self._expiry_heap: list[tuple[int, str, int, int, str]] = []
        self._event_log: list[AlignmentEvent] = []

    @property
    def watermark(self) -> int | None:
        return self._watermark

    @property
    def active_anchor_count(self) -> int:
        return len(self._active_anchors)

    @property
    def retained_candidate_count(self) -> int:
        return len(self._candidates)

    @property
    def event_log(self) -> tuple[AlignmentEvent, ...]:
        return tuple(self._event_log)

    def current_alignment(self) -> dict[str, AlignmentResult]:
        return dict(
            sorted(
                ((anchor_id, state.result()) for anchor_id, state in self._anchors.items()),
                key=lambda item: (item[1].anchor_timestamp, item[1].anchor_seq, item[0]),
            )
        )

    def add_sample(self, sample_id: str, source: str, timestamp: int, seq: int) -> list[AlignmentEvent]:
        self._validate_sample(sample_id, source, timestamp, seq)
        sample = Sample(sample_id, source, int(timestamp), int(seq))
        tolerance = self._tolerances[source]
        expiry = sample.timestamp + tolerance

        if expiry <= self._watermark_value() and self._active_reference_count(sample, tolerance) == 0:
            return []

        candidate = _Candidate(sample, self._active_reference_count(sample, tolerance), expiry)
        self._candidates[sample.sample_id] = candidate
        insort(self._candidate_index[source], sample.index_key())
        if expiry > self._watermark_value():
            heapq.heappush(self._expiry_heap, (expiry, source, timestamp, seq, sample_id))

        events = []
        for _, _, anchor_id in self._anchors_in_window(sample.timestamp, tolerance):
            state = self._anchors[anchor_id]
            selected = self._nearest_sample_id(source, state.timestamp)
            if state.matches.get(source) != selected:
                state.matches[source] = selected
                events.append(self._upsert_event(state, ChangeReason.SAMPLE_ADDED))
        return self._publish(events)

    def retract_sample(self, sample_id: str) -> list[AlignmentEvent]:
        candidate = self._candidates.get(sample_id)
        if candidate is None:
            return []

        source = candidate.sample.source
        tolerance = self._tolerances[source]
        affected = [
            self._anchors[anchor_id]
            for _, _, anchor_id in self._anchors_in_window(candidate.sample.timestamp, tolerance)
            if self._anchors[anchor_id].matches.get(source) == sample_id
        ]
        affected.sort(key=lambda state: (state.timestamp, state.seq, state.anchor_id))

        del self._candidates[sample_id]
        index_key = candidate.sample.index_key()
        position = bisect_left(self._candidate_index[source], index_key)
        if position < len(self._candidate_index[source]) and self._candidate_index[source][position] == index_key:
            self._candidate_index[source].pop(position)

        events = []
        for state in affected:
            state.matches[source] = self._nearest_sample_id(source, state.timestamp)
            events.append(self._upsert_event(state, ChangeReason.SAMPLE_RETRACTED))
        return self._publish(events)

    def add_anchor(self, anchor_id: str, timestamp: int, seq: int) -> list[AlignmentEvent]:
        timestamp = int(timestamp)
        seq = int(seq)
        if not anchor_id:
            raise ValueError("anchor_id must not be empty")
        if seq < 0:
            raise ValueError("seq must be non-negative")
        if anchor_id in self._anchors:
            raise ValueError(f"duplicate active or finalized anchor: {anchor_id}")
        if timestamp <= self._watermark_value():
            return []

        matches: dict[str, str | None] = {}
        for source, tolerance in self._tolerances.items():
            matches[source] = self._nearest_sample_id(source, timestamp)

        state = _AnchorState(
            anchor_id,
            seq,
            timestamp,
            timestamp + self._max_tolerance,
            0,
            matches,
            self._sources,
        )
        self._anchors[anchor_id] = state
        insort(self._active_anchors, (timestamp, seq, anchor_id))
        self._increment_window_references(timestamp)
        event = self._upsert_event(state, ChangeReason.ANCHOR_ADDED)
        return self._publish([event])

    def retract_anchor(self, anchor_id: str) -> list[AlignmentEvent]:
        state = self._anchors.get(anchor_id)
        if state is None or state.finalized:
            return []
        self._remove_active_anchor(anchor_id)
        self._release_anchor_references(state)
        del self._anchors[anchor_id]
        event = self._delete_event(state, ChangeReason.ANCHOR_RETRACTED)
        return self._publish([event])

    def advance_watermark(self, timestamp: int) -> list[AlignmentEvent]:
        timestamp = int(timestamp)
        if timestamp <= self._watermark_value():
            return []
        self._watermark = timestamp

        closing = sorted(
            (
                state
                for state in self._anchors.values()
                if not state.finalized and state.close_time <= timestamp
            ),
            key=lambda state: (state.close_time, state.seq, state.anchor_id),
        )
        events = []
        for state in closing:
            state.finalized = True
            events.append(self._upsert_event(state, ChangeReason.WATERMARK))
            self._remove_active_anchor(state.anchor_id)
            self._release_anchor_references(state)

        self._collect_expired_candidates()
        return self._publish(events)

    def process_event(
        self,
        event: SampleAdded | SampleRetracted | AnchorAdded | AnchorRetracted | WatermarkAdvanced,
    ) -> list[AlignmentEvent]:
        if isinstance(event, SampleAdded):
            return self.add_sample(event.sample_id, event.source, event.timestamp, event.seq)
        if isinstance(event, SampleRetracted):
            return self.retract_sample(event.sample_id)
        if isinstance(event, AnchorAdded):
            return self.add_anchor(event.anchor_id, event.timestamp, event.seq)
        if isinstance(event, AnchorRetracted):
            return self.retract_anchor(event.anchor_id)
        if isinstance(event, WatermarkAdvanced):
            return self.advance_watermark(event.timestamp)
        raise TypeError(f"unsupported event: {type(event).__name__}")

    def process_events(
        self,
        events: Iterable[SampleAdded | SampleRetracted | AnchorAdded | AnchorRetracted | WatermarkAdvanced],
    ) -> list[AlignmentEvent]:
        emitted: list[AlignmentEvent] = []
        for event in events:
            emitted.extend(self.process_event(event))
        return emitted

    def _validate_sample(self, sample_id: str, source: str, timestamp: int, seq: int) -> None:
        if not sample_id:
            raise ValueError("sample_id must not be empty")
        if source not in self._tolerances:
            raise ValueError(f"unknown auxiliary source: {source}")
        if int(seq) < 0:
            raise ValueError("seq must be non-negative")
        if sample_id in self._candidates:
            raise ValueError(f"duplicate active sample: {sample_id}")
        int(timestamp)

    def _watermark_value(self) -> int:
        return -10**30 if self._watermark is None else self._watermark

    def _nearest_sample_id(self, source: str, anchor_timestamp: int) -> str | None:
        tolerance = self._tolerances[source]
        keys = self._candidate_index[source]
        position = bisect_left(keys, (anchor_timestamp,))
        best: tuple[int, int, int, str] | None = None

        if position < len(keys):
            timestamp, seq, sample_id = keys[position]
            distance = timestamp - anchor_timestamp
            if distance <= tolerance:
                end = bisect_left(keys, (timestamp, float("inf"), float("inf")))
                timestamp, seq, sample_id = min(keys[position:end], key=lambda key: (key[1], key[2]))
                best = (distance, timestamp, seq, sample_id)

        if position > 0:
            timestamp, _, _ = keys[position - 1]
            distance = anchor_timestamp - timestamp
            if distance <= tolerance:
                start = bisect_left(keys, (timestamp,))
                end = bisect_left(keys, (timestamp, float("inf"), float("inf")))
                timestamp, seq, sample_id = min(keys[start:end], key=lambda key: (key[1], key[2]))
                candidate = (distance, timestamp, seq, sample_id)
                if best is None or candidate < best:
                    best = candidate
        return None if best is None else best[3]

    def _candidate_keys_in_window(
        self,
        source: str,
        anchor_timestamp: int,
        tolerance: int,
    ) -> list[tuple[int, int, str]]:
        keys = self._candidate_index[source]
        start = bisect_left(keys, (anchor_timestamp - tolerance,))
        end = bisect_left(keys, (anchor_timestamp + tolerance, float("inf"), float("inf")))
        return keys[start:end]

    def _anchors_in_window(
        self,
        sample_timestamp: int,
        tolerance: int,
    ) -> list[tuple[int, int, str]]:
        start = bisect_left(self._active_anchors, (sample_timestamp - tolerance,))
        end = bisect_left(self._active_anchors, (sample_timestamp + tolerance, float("inf"), float("inf")))
        return self._active_anchors[start:end]

    def _active_reference_count(self, sample: Sample, tolerance: int) -> int:
        return len(self._anchors_in_window(sample.timestamp, tolerance))

    def _increment_window_references(self, anchor_timestamp: int) -> None:
        for source, tolerance in self._tolerances.items():
            for _, _, sample_id in self._candidate_keys_in_window(source, anchor_timestamp, tolerance):
                candidate = self._candidates.get(sample_id)
                if candidate is not None:
                    candidate.active_references += 1

    def _release_anchor_references(self, state: _AnchorState) -> None:
        for source, tolerance in self._tolerances.items():
            keys = self._candidate_keys_in_window(source, state.timestamp, tolerance)
            for timestamp, seq, sample_id in keys:
                candidate = self._candidates.get(sample_id)
                if candidate is None:
                    continue
                candidate.active_references -= 1
                if candidate.active_references == 0 and candidate.expiry <= self._watermark_value():
                    self._remove_candidate(candidate)

    def _remove_candidate(self, candidate: _Candidate) -> None:
        self._candidates.pop(candidate.sample.sample_id, None)
        index = self._candidate_index[candidate.sample.source]
        key = candidate.sample.index_key()
        position = bisect_left(index, key)
        if position < len(index) and index[position] == key:
            index.pop(position)

    def _remove_active_anchor(self, anchor_id: str) -> None:
        state = self._anchors[anchor_id]
        key = (state.timestamp, state.seq, anchor_id)
        position = bisect_left(self._active_anchors, key)
        if position < len(self._active_anchors) and self._active_anchors[position] == key:
            self._active_anchors.pop(position)

    def _collect_expired_candidates(self) -> None:
        watermark = self._watermark_value()
        while self._expiry_heap and self._expiry_heap[0][0] <= watermark:
            expiry, source, timestamp, seq, sample_id = heapq.heappop(self._expiry_heap)
            candidate = self._candidates.get(sample_id)
            if candidate is None or candidate.active_references > 0:
                continue
            if candidate.expiry != expiry or candidate.sample.index_key() != (timestamp, seq, sample_id):
                continue
            self._remove_candidate(candidate)

    def _upsert_event(self, state: _AnchorState, reason: ChangeReason) -> AlignmentEvent:
        previous_version = state.version if state.version > 0 else None
        state.version += 1
        self._event_seq += 1
        return AlignmentEvent(
            self._event_seq,
            AlignmentOperation.UPSERT,
            reason,
            state.result(),
            previous_version,
        )

    def _delete_event(self, state: _AnchorState, reason: ChangeReason) -> AlignmentEvent:
        self._event_seq += 1
        return AlignmentEvent(
            self._event_seq,
            AlignmentOperation.DELETE,
            reason,
            state.result(),
            state.version if state.version > 0 else None,
        )

    def _publish(self, events: list[AlignmentEvent]) -> list[AlignmentEvent]:
        self._event_log.extend(events)
        return events
