"""Reference implementation: keep every event, re-sort and recompute all
sessions of the affected source from scratch on each op, then diff the
window sets to derive the changelog.

This is intentionally simple and obviously correct (O(n log n) per op,
no incremental state); it exists as an oracle for the parity tests
against :class:`session_windows.tracker.SessionTracker`.
"""

from __future__ import annotations

from typing import Dict, Iterable, List, Optional, Tuple

from .model import (
    AdvanceWatermark,
    BatchResult,
    Change,
    ChangeKind,
    Disposition,
    Event,
    Member,
    Op,
    OpResult,
    Retract,
    Upsert,
    Window,
    make_window,
)

__all__ = ["ReferenceSessionTracker"]


class ReferenceSessionTracker:
    def __init__(self, gap_ms: int, allowed_lateness_ms: int = 0) -> None:
        if gap_ms < 0 or allowed_lateness_ms < 0:
            raise ValueError("gap_ms and allowed_lateness_ms must be >= 0")
        self.gap_ms = gap_ms
        self.allowed_lateness_ms = allowed_lateness_ms
        self._events: Dict[Tuple[str, int], Event] = {}
        self._watermarks: Dict[str, int] = {}
        self._windows: Dict[str, Dict[str, Window]] = {}

    def apply(self, op: Op) -> OpResult:
        if isinstance(op, Upsert):
            return self.upsert_event(op.event)
        if isinstance(op, Retract):
            return self.retract(op.source, op.seq)
        if isinstance(op, AdvanceWatermark):
            return self.advance_watermark(op.source, op.watermark_ms)
        raise TypeError(f"unsupported op: {op!r}")

    def apply_all(self, ops: Iterable[Op]) -> BatchResult:
        results: List[OpResult] = []
        changes: List[Change] = []
        for op in ops:
            result = self.apply(op)
            results.append(result)
            changes.extend(result.changes)
        return BatchResult(tuple(results), tuple(changes))

    def upsert_event(self, event: Event) -> OpResult:
        watermark = self._watermarks.get(event.source)
        if (
            watermark is not None
            and event.event_ms < watermark - self.allowed_lateness_ms
        ):
            return OpResult(Disposition.TOO_LATE)
        key = (event.source, event.seq)
        old = self._events.get(key)
        if old is not None:
            if old == event:
                return OpResult(Disposition.DUPLICATE)
            self._events[key] = event
            return OpResult(Disposition.UPDATED, self._recompute(event.source))
        self._events[key] = event
        return OpResult(Disposition.APPLIED, self._recompute(event.source))

    def retract(self, source: str, seq: int) -> OpResult:
        if (source, seq) not in self._events:
            return OpResult(Disposition.NOT_FOUND)
        del self._events[(source, seq)]
        return OpResult(Disposition.APPLIED, self._recompute(source))

    def advance_watermark(self, source: str, watermark_ms: int) -> OpResult:
        current = self._watermarks.get(source)
        if current is not None and watermark_ms <= current:
            return OpResult(Disposition.WATERMARK_IGNORED)
        self._watermarks[source] = watermark_ms
        horizon = watermark_ms - self.allowed_lateness_ms
        changes: List[Change] = []
        windows = self._windows.get(source, {})
        for window_id in sorted(
            wid
            for wid, w in windows.items()
            if w.end_ms + self.gap_ms < horizon
        ):
            window = windows.pop(window_id)
            changes.append(Change(ChangeKind.FINALIZE, window))
            for member in window.members:
                self._events.pop((source, member.seq), None)
        return OpResult(Disposition.WATERMARK_ADVANCED, tuple(changes))

    def active_windows(self, source: Optional[str] = None) -> Tuple[Window, ...]:
        out: List[Window] = []
        for src in sorted(self._windows):
            if source is None or src == source:
                out.extend(self._windows[src].values())
        return tuple(sorted(out, key=lambda w: (w.source, w.start_ms)))

    def _recompute(self, source: str) -> Tuple[Change, ...]:
        events = sorted(
            (e for e in self._events.values() if e.source == source),
            key=lambda e: (e.event_ms, e.seq),
        )
        groups: List[List[Member]] = []
        for event in events:
            member = Member(event.event_ms, event.seq, event.value)
            if groups and event.event_ms - groups[-1][-1].event_ms <= self.gap_ms:
                groups[-1].append(member)
            else:
                groups.append([member])
        new_windows = {
            w.window_id: w
            for w in (make_window(source, g) for g in groups)
        }
        old_windows = self._windows.get(source, {})
        changes: List[Change] = []
        for window_id in sorted(new_windows.keys() | old_windows.keys()):
            new = new_windows.get(window_id)
            old = old_windows.get(window_id)
            if new is None:
                changes.append(Change(ChangeKind.RETRACT, old))
            elif new != old:
                changes.append(Change(ChangeKind.UPSERT, new))
        self._windows[source] = new_windows
        return tuple(changes)
