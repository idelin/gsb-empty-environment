"""Incremental session-window tracker with a retract/upsert changelog.

Per-source indexes (see README for the full discussion):

* ``members: dict[seq -> Member]`` -- O(1) dedup / retraction lookup.
* ``windows: list[Window]`` -- sorted by ``start_ms``; bisect finds the
  <=2 windows an event can touch, or the single window owning a member.

No operation ever scans other sources or untouched windows:

* upsert:   1 dict insert + bisect over windows + splice of the <=2
            touched window entries; only touched members are re-sorted.
* retract:  1 dict delete + bisect to the owning window + re-grouping of
            that window's members only; splice of 1 -> 0/1/2 entries.
* watermark: pops the sorted prefix of finalizable windows (O(k) for k
            reclaimed windows) and drops their members from the dict.

Changelog entries are emitted as the *net* delta of each op (at most one
entry per window id), which is exactly what a from-scratch recompute
would diff to -- see ``reference.py`` and the parity tests.
"""

from __future__ import annotations

from bisect import bisect_right
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

__all__ = ["SessionTracker"]


class _Delta:
    """Accumulates before/after snapshots of touched window ids for one op.

    Produces the net change set: ids that vanished -> RETRACT, ids that
    appeared or changed -> UPSERT, unchanged ids -> nothing.
    """

    def __init__(self) -> None:
        self._before: Dict[str, Optional[Window]] = {}
        self._after: Dict[str, Optional[Window]] = {}

    def removing(self, window: Window) -> None:
        self._before.setdefault(window.window_id, window)
        self._after[window.window_id] = None

    def adding(self, window: Window) -> None:
        self._before.setdefault(window.window_id, None)
        self._after[window.window_id] = window

    def changes(self) -> Tuple[Change, ...]:
        out: List[Change] = []
        for window_id in self._before.keys() | self._after.keys():
            before = self._before.get(window_id)
            after = self._after.get(window_id)
            if after is None:
                if before is not None:
                    out.append(Change(ChangeKind.RETRACT, before))
            elif after != before:
                out.append(Change(ChangeKind.UPSERT, after))
        out.sort(key=lambda c: c.window.window_id)
        return tuple(out)


class _SourceState:
    __slots__ = ("members", "windows", "watermark")

    def __init__(self) -> None:
        self.members: Dict[int, Member] = {}
        self.windows: List[Window] = []  # sorted by start_ms (disjoint)
        self.watermark: Optional[int] = None


class SessionTracker:
    """Session-window tracker.

    ``gap_ms``: two events of the same source belong to one session iff
    consecutive timestamps differ by at most ``gap_ms`` (inclusive).
    ``allowed_lateness_ms``: once the per-source watermark is at ``wm``,
    events with ``event_ms < wm - allowed_lateness_ms`` are rejected with
    ``Disposition.TOO_LATE``; windows with
    ``end_ms + gap_ms < wm - allowed_lateness_ms`` are finalized and their
    state is reclaimed.
    """

    def __init__(self, gap_ms: int, allowed_lateness_ms: int = 0) -> None:
        if gap_ms < 0:
            raise ValueError("gap_ms must be >= 0")
        if allowed_lateness_ms < 0:
            raise ValueError("allowed_lateness_ms must be >= 0")
        self.gap_ms = gap_ms
        self.allowed_lateness_ms = allowed_lateness_ms
        self._sources: Dict[str, _SourceState] = {}

    # ------------------------------------------------------------------ API

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

    def upsert(
        self, source: str, seq: int, event_ms: int, value: object = None
    ) -> OpResult:
        return self.upsert_event(Event(source, seq, event_ms, value))

    def upsert_event(self, event: Event) -> OpResult:
        state = self._sources.get(event.source)
        if state is None:
            state = self._sources[event.source] = _SourceState()
        if self._too_late(state, event.event_ms):
            return OpResult(Disposition.TOO_LATE)
        member = Member(event.event_ms, event.seq, event.value)
        existing = state.members.get(event.seq)
        if existing is not None:
            if existing == member:
                return OpResult(Disposition.DUPLICATE)
            delta = _Delta()
            self._remove_member(state, event.source, event.seq, delta)
            self._insert_member(state, event.source, member, delta)
            return OpResult(Disposition.UPDATED, delta.changes())
        delta = _Delta()
        self._insert_member(state, event.source, member, delta)
        return OpResult(Disposition.APPLIED, delta.changes())

    def retract(self, source: str, seq: int) -> OpResult:
        state = self._sources.get(source)
        if state is None or seq not in state.members:
            return OpResult(Disposition.NOT_FOUND)
        delta = _Delta()
        self._remove_member(state, source, seq, delta)
        return OpResult(Disposition.APPLIED, delta.changes())

    def advance_watermark(self, source: str, watermark_ms: int) -> OpResult:
        state = self._sources.get(source)
        if state is None:
            state = self._sources[source] = _SourceState()
        if state.watermark is not None and watermark_ms <= state.watermark:
            return OpResult(Disposition.WATERMARK_IGNORED)
        state.watermark = watermark_ms
        horizon = watermark_ms - self.allowed_lateness_ms
        # Windows are sorted by start; disjoint windows also have strictly
        # increasing end_ms, so finalizable windows form a prefix.
        finalized: List[Window] = []
        for window in state.windows:
            if window.end_ms + self.gap_ms < horizon:
                finalized.append(window)
            else:
                break
        if finalized:
            del state.windows[: len(finalized)]
            for window in finalized:
                for member in window.members:
                    del state.members[member.seq]
        changes = tuple(
            Change(ChangeKind.FINALIZE, w)
            for w in sorted(finalized, key=lambda w: w.window_id)
        )
        return OpResult(Disposition.WATERMARK_ADVANCED, changes)

    def active_windows(self, source: Optional[str] = None) -> Tuple[Window, ...]:
        out: List[Window] = []
        for src in sorted(self._sources):
            if source is None or src == source:
                out.extend(self._sources[src].windows)
        return tuple(out)

    def watermark(self, source: str) -> Optional[int]:
        state = self._sources.get(source)
        return state.watermark if state else None

    # -------------------------------------------------------------- internals

    def _too_late(self, state: _SourceState, event_ms: int) -> bool:
        return (
            state.watermark is not None
            and event_ms < state.watermark - self.allowed_lateness_ms
        )

    def _insert_member(
        self, state: _SourceState, source: str, member: Member, delta: _Delta
    ) -> None:
        windows = state.windows
        # Last window with start_ms <= event_ms + gap: only it and its
        # predecessor can touch the event (windows are > gap apart, so an
        # event bridges at most two of them).
        idx = (
            bisect_right(
                windows, member.event_ms + self.gap_ms, key=lambda w: w.start_ms
            )
            - 1
        )
        touched = [
            i
            for i in (idx - 1, idx)
            if 0 <= i < len(windows)
            and windows[i].start_ms - self.gap_ms
            <= member.event_ms
            <= windows[i].end_ms + self.gap_ms
        ]
        state.members[member.seq] = member
        if not touched:
            new_window = make_window(source, [member])
            ins = bisect_right(
                windows, new_window.start_ms, key=lambda w: w.start_ms
            )
            windows.insert(ins, new_window)
            delta.adding(new_window)
            return
        lo, hi = touched[0], touched[-1]
        merged = [member]
        for i in touched:
            merged.extend(windows[i].members)
        new_window = make_window(source, merged)
        for old in windows[lo : hi + 1]:
            delta.removing(old)
        windows[lo : hi + 1] = [new_window]
        delta.adding(new_window)

    def _remove_member(
        self, state: _SourceState, source: str, seq: int, delta: _Delta
    ) -> None:
        member = state.members.pop(seq)
        windows = state.windows
        idx = (
            bisect_right(windows, member.event_ms, key=lambda w: w.start_ms) - 1
        )
        old = windows[idx]
        assert old.start_ms <= member.event_ms <= old.end_ms
        delta.removing(old)
        remaining = [m for m in old.members if m.seq != seq]
        if not remaining:
            del windows[idx]
            return
        # Re-group only this window's members; a split point is any
        # consecutive pair now more than gap apart.
        parts: List[List[Member]] = [[remaining[0]]]
        for m in remaining[1:]:
            if m.event_ms - parts[-1][-1].event_ms <= self.gap_ms:
                parts[-1].append(m)
            else:
                parts.append([m])
        new_windows = [make_window(source, part) for part in parts]
        windows[idx : idx + 1] = new_windows
        for window in new_windows:
            delta.adding(window)
