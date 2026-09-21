from __future__ import annotations

import heapq
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional, Tuple

RANK = {"A": 0, "C": 1, "B": 2}


@dataclass(frozen=True)
class Event:
    kind: str
    key: str
    ts: int
    source: str
    seq: int
    tie: Optional[int] = None

    @property
    def event_id(self) -> Tuple[str, int]:
        return (self.source, self.seq)

    @property
    def order_key(self) -> Tuple[int, int, str, int]:
        tie = RANK[self.kind] if self.tie is None else self.tie
        return (self.ts, tie, self.source, self.seq)


@dataclass(frozen=True)
class Output:
    status: str
    match_id: Optional[Tuple[str, Tuple[str, int], Tuple[str, int]]]
    key: str
    a_id: Tuple[str, int]
    b_id: Optional[Tuple[str, int]]
    event_time: int
    trigger_time: int
    reason: str


@dataclass
class _Match:
    b: Event
    final: bool = False


@dataclass
class _A:
    event: Event
    matches: Dict[Tuple[str, int], _Match]
    retracted: bool = False
    closed: bool = False


@dataclass
class _State:
    a_events: Dict[Tuple[str, int], _A]
    b_events: Dict[Tuple[str, int], Event] = field(default_factory=dict)
    c_events: Dict[Tuple[str, int], Event] = field(default_factory=dict)



class PatternMatcher:
    def __init__(self, gap_ms: int = 5, allowed_lateness_ms: int = 0) -> None:
        if gap_ms < 0 or allowed_lateness_ms < 0:
            raise ValueError("time parameters must be non-negative")
        self.gap = gap_ms
        self.allowed_lateness = allowed_lateness_ms
        self.watermark: Optional[int] = None
        self.states: Dict[str, _State] = {}
        self.seen: Dict[Tuple[str, int], Any] = {}
        self.a_close_heap: List[Tuple[int, str, Tuple[str, int]]] = []
        self.expiry_heap: List[Tuple[int, str, str, Tuple[str, int]]] = []
        self.retraction_heap: List[Tuple[int, str, Tuple[str, int], str, int]] = []
        self.seen_heap: List[Tuple[int, Tuple[str, int]]] = []
        self.stats = {
            "provisional": 0,
            "final_match": 0,
            "no_match": 0,
            "revoked": 0,
            "late_match": 0,
            "late_no_match": 0,
            "out_of_scope": 0,
            "duplicates": 0,
        }

    def ingest(self, event: Event) -> List[Output]:
        if event.kind not in RANK:
            raise ValueError("event kind must be A, B, or C")
        identity = event.event_id
        previous = self.seen.get(identity)
        if previous is not None:
            if previous != event:
                raise ValueError("duplicate event id with different payload")
            self.stats["duplicates"] += 1
            return []
        if self.watermark is not None and event.ts < self.watermark - self.allowed_lateness:
            self.stats["out_of_scope"] += 1
            return [self._late_record(event)]
        self.seen[identity] = event
        heapq.heappush(
            self.seen_heap,
            (event.ts + self.gap + self.allowed_lateness, identity),
        )
        if event.kind == "A":
            return self._add_a(event)
        if event.kind == "B":
            return self._add_b(event)
        return self._add_c(event)

    def retract_a(
        self,
        key: str,
        target: Tuple[str, int],
        source: str,
        seq: int,
        ts: int,
    ) -> List[Output]:
        identity = (source, seq)
        previous = self.seen.get(identity)
        if previous is not None:
            if previous != ("R", key, target, source, seq, ts):
                raise ValueError("duplicate event id with different payload")
            self.stats["duplicates"] += 1
            return []
        if self.watermark is not None and ts < self.watermark - self.allowed_lateness:
            self.stats["out_of_scope"] += 1
            return [self._late_retraction(key, target, source, seq, ts)]
        self.seen[identity] = ("R", key, target, source, seq, ts)
        heapq.heappush(
            self.seen_heap,
            (ts + self.gap + self.allowed_lateness, identity),
        )
        return self._add_retraction(key, target, source, seq, ts)

    def advance_watermark(self, watermark: int) -> List[Output]:
        if self.watermark is not None and watermark < self.watermark:
            raise ValueError("watermarks must be monotonic")
        self.watermark = watermark
        outputs: List[Output] = []
        outputs.extend(self._close_a(watermark))
        outputs.extend(self._expire(watermark))
        outputs.extend(self._expire_retractions(watermark))
        outputs.extend(self._expire_seen(watermark))
        return outputs

    def _state(self, key: str) -> _State:
        state = self.states.get(key)
        if state is None:
            state = _State(a_events={})
            self.states[key] = state
        return state

    def _add_a(self, event: Event) -> List[Output]:
        state = self._state(event.key)
        retracted, source, seq = self._take_pending_retraction(event.key, event.event_id)
        record = _A(event=event, matches={}, retracted=retracted)
        state.a_events[event.event_id] = record
        end = event.ts + self.gap
        if not retracted:
            heapq.heappush(self.a_close_heap, (end, event.key, event.event_id))
        heapq.heappush(
            self.expiry_heap,
            (end + self.allowed_lateness, "A", event.key, event.event_id),
        )
        if self.watermark is not None and end < self.watermark:
            record.closed = True
        outputs: List[Output] = []
        if not retracted:
            for b_event in list(state.b_events.values()):
                if self._can_match(event, b_event, state):
                    self._create_match(record, b_event, outputs)
        return outputs

    def _add_b(self, event: Event) -> List[Output]:
        state = self._state(event.key)
        state.b_events[event.event_id] = event
        heapq.heappush(
            self.expiry_heap,
            (event.ts + self.gap + self.allowed_lateness, "B", event.key, event.event_id),
        )
        outputs: List[Output] = []
        for record in list(state.a_events.values()):
            if record.retracted or event.event_id in record.matches:
                continue
            if self._can_match(record.event, event, state):
                self._create_match(record, event, outputs)
        return outputs

    def _add_c(self, event: Event) -> List[Output]:
        state = self._state(event.key)
        state.c_events[event.event_id] = event
        heapq.heappush(
            self.expiry_heap,
            (event.ts + self.gap + self.allowed_lateness, "C", event.key, event.event_id),
        )
        outputs: List[Output] = []
        for record in list(state.a_events.values()):
            if record.retracted:
                continue
            affected = [
                match
                for match in record.matches.values()
                if self._blocks(event, record.event, match.b)
            ]
            for match in affected:
                self._revoke_match(record, match.b, event, outputs)
        return outputs

    def _add_retraction(
        self,
        key: str,
        target: Tuple[str, int],
        source: str,
        seq: int,
        ts: int,
    ) -> List[Output]:
        state = self.states.get(key)
        record = state.a_events.get(target) if state is not None else None
        if record is None:
            heapq.heappush(self.retraction_heap, (ts, key, target, source, seq))
            return []
        record.retracted = True
        outputs: List[Output] = []
        for match in list(record.matches.values()):
            self._revoke_match(record, match.b, None, outputs)
        return outputs

    def _take_pending_retraction(
        self,
        key: str,
        target: Tuple[str, int],
    ) -> Tuple[bool, Optional[str], Optional[int]]:
        kept: List[Tuple[int, str, Tuple[str, int], str, int]] = []
        found: Optional[Tuple[int, str, Tuple[str, int], str, int]] = None
        while self.retraction_heap:
            item = heapq.heappop(self.retraction_heap)
            if item[1] == key and item[2] == target and found is None:
                found = item
            else:
                kept.append(item)
        self.retraction_heap = kept
        heapq.heapify(self.retraction_heap)
        if found is None:
            return False, None, None
        return True, found[3], found[4]

    def _can_match(self, a_event: Event, b_event: Event, state: _State) -> bool:
        if not a_event.order_key < b_event.order_key:
            return False
        if b_event.order_key[0] > a_event.ts + self.gap:
            return False
        for c_event in state.c_events.values():
            if self._blocks(c_event, a_event, b_event):
                return False
        return True

    @staticmethod
    def _blocks(c_event: Event, a_event: Event, b_event: Event) -> bool:
        return a_event.order_key < c_event.order_key < b_event.order_key

    def _create_match(self, record: _A, b_event: Event, outputs: List[Output]) -> None:
        match = _Match(b=b_event)
        record.matches[b_event.event_id] = match
        status = "PROVISIONAL_MATCH"
        if record.closed:
            status = "LATE_MATCH"
            match.final = True
            self.stats["late_match"] += 1
        else:
            self.stats["provisional"] += 1
        outputs.append(self._output(status, record.event, b_event, b_event.ts, "B"))

    def _revoke_match(
        self,
        record: _A,
        b_event: Event,
        c_event: Optional[Event],
        outputs: List[Output],
    ) -> None:
        old = record.matches.pop(b_event.event_id)
        status = "REVOKED"
        trigger = c_event.ts if c_event is not None else (self.watermark or b_event.ts)
        reason = "C" if c_event is not None else "A_RETRACTED"
        if old.final:
            status = "LATE_NO_MATCH"
            self.stats["late_no_match"] += 1
        else:
            self.stats["revoked"] += 1
        outputs.append(self._output(status, record.event, b_event, trigger, reason))

    def _close_a(self, watermark: int) -> List[Output]:
        outputs: List[Output] = []
        while self.a_close_heap and self.a_close_heap[0][0] <= watermark:
            end, key, a_id = heapq.heappop(self.a_close_heap)
            state = self.states.get(key)
            record = state.a_events.get(a_id) if state is not None else None
            if record is None or record.closed or record.retracted:
                continue
            record.closed = True
            if record.matches:
                for match in list(record.matches.values()):
                    match.final = True
                    self.stats["final_match"] += 1
                    outputs.append(
                        self._output("FINAL_MATCH", record.event, match.b, end, "WATERMARK")
                    )
            else:
                self.stats["no_match"] += 1
                outputs.append(
                    self._output("NO_MATCH", record.event, record.event, end, "WATERMARK")
                )
        return outputs

    def _expire(self, watermark: int) -> List[Output]:
        while self.expiry_heap and self.expiry_heap[0][0] < watermark:
            _, kind, key, event_id = heapq.heappop(self.expiry_heap)
            state = self.states.get(key)
            if state is None:
                continue
            if kind == "A":
                state.a_events.pop(event_id, None)
            elif kind == "B":
                state.b_events.pop(event_id, None)
            else:
                state.c_events.pop(event_id, None)
            if not state.a_events and not state.b_events and not state.c_events:
                self.states.pop(key, None)
        return []

    def _expire_retractions(self, watermark: int) -> List[Output]:
        keep = [
            item
            for item in self.retraction_heap
            if item[0] + self.gap + self.allowed_lateness >= watermark
        ]
        self.retraction_heap = keep
        heapq.heapify(self.retraction_heap)
        return []

    def _expire_seen(self, watermark: int) -> List[Output]:
        while self.seen_heap and self.seen_heap[0][0] < watermark:
            _, identity = heapq.heappop(self.seen_heap)
            self.seen.pop(identity, None)
        return []

    def _output(
        self,
        status: str,
        a_event: Event,
        b_event: Event,
        trigger_time: int,
        reason: str,
    ) -> Output:
        return Output(
            status=status,
            match_id=(a_event.key, a_event.event_id, b_event.event_id),
            key=a_event.key,
            a_id=a_event.event_id,
            b_id=b_event.event_id,
            event_time=b_event.ts,
            trigger_time=trigger_time,
            reason=reason,
        )

    def _late_record(self, event: Event) -> Output:
        return Output(
            status="OUT_OF_SCOPE_LATE",
            match_id=None,
            key=event.key,
            a_id=event.event_id,
            b_id=None,
            event_time=event.ts,
            trigger_time=self.watermark if self.watermark is not None else event.ts,
            reason=event.kind,
        )

    def _late_retraction(
        self,
        key: str,
        target: Tuple[str, int],
        source: str,
        seq: int,
        ts: int,
    ) -> Output:
        return Output(
            status="OUT_OF_SCOPE_LATE",
            match_id=None,
            key=key,
            a_id=target,
            b_id=(source, seq),
            event_time=ts,
            trigger_time=self.watermark if self.watermark is not None else ts,
            reason="A_RETRACTED",
        )

    def retained_events(self) -> int:
        total = 0
        for state in self.states.values():
            total += len(state.a_events) + len(state.b_events) + len(state.c_events)
        total += len(self.retraction_heap)
        return total

    def pending_match_count(self) -> int:
        return sum(
            1
            for state in self.states.values()
            for record in state.a_events.values()
            for match in record.matches.values()
            if not match.final
        )
