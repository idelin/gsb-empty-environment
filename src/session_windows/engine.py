"""Incremental session-window engine.

Per source, exactly two indexes are maintained:

* ``events``: ``dict[seq, Event]`` — identity index for dedup and retraction.
* ``sessions``: list of ``_Session`` sorted by ``start``, always disjoint and
  separated by more than ``gap``. Located by binary search; a mutation only
  ever touches the contiguous run of sessions overlapping
  ``[event_ms - gap, event_ms + gap]`` (insert) or the single containing
  session (retract), never the full history.

Window-id continuation rules
----------------------------
A window's id is always ``<source>@<start_ms>`` — a pure function of its
content. Per revision:

* Insert merging k >= 1 windows: the merged window is UPSERTed under its id.
  Every constituent id that differs from the merged id is RETRACTed. In the
  common case (the new event is not earlier than every constituent) the
  earliest constituent's id survives unchanged; a late event landing *before*
  all constituents moves the start, so the old earliest id is retracted and
  the window re-appears under the new start's id.
* Retract splitting a window into n >= 1 segments: every segment is UPSERTed
  under its own id; the old id is RETRACTed only if no segment keeps it
  (i.e. the removed event was the window's earliest, or n == 0).

Because ids are derived from ``(source, start_ms)``, the final set of ids and
members is a pure function of the set of live events — arrival order and
redelivery do not change it.
"""
from __future__ import annotations

from bisect import bisect_left, bisect_right
from dataclasses import dataclass, field
from heapq import merge as heap_merge
from typing import Dict, Iterable, Iterator, List, Optional, Tuple

from .model import (
    AdvanceWatermark,
    BatchResult,
    Change,
    ChangeKind,
    Disposition,
    Event,
    IndexStats,
    Op,
    Retract,
    Revision,
    Window,
    _member_key,
)


@dataclass
class _Session:
    start: int
    end: int
    members: List[Event]  # sorted by (event_ms, seq)

    @property
    def id(self) -> str:
        return _make_id(self)

    def to_window(self, closed: bool = False) -> Window:
        return Window(
            id=_make_id(self),
            source=self.members[0].source,
            start_ms=self.start,
            end_ms=self.end,
            members=tuple(self.members),
            closed=closed,
        )


def _make_id(sess: "_Session") -> str:
    return Window.make_id(sess.members[0].source, sess.start)


@dataclass
class _SourceState:
    events: Dict[int, Event] = field(default_factory=dict)
    sessions: List[_Session] = field(default_factory=list)
    watermark: int = -(2**63)


class SessionWindows:
    """Incremental session-window computation with retract/upsert changelogs.

    :param gap_ms: two adjacent events of the same source belong to one
        session iff their timestamps differ by at most ``gap_ms``.
    :param allowed_lateness_ms: an event is dropped as late once
        ``event_ms < watermark - allowed_lateness_ms`` for its source.
    """

    def __init__(self, gap_ms: int, allowed_lateness_ms: int = 0):
        if gap_ms < 0:
            raise ValueError("gap_ms must be >= 0")
        if allowed_lateness_ms < 0:
            raise ValueError("allowed_lateness_ms must be >= 0")
        self.gap_ms = gap_ms
        self.allowed_lateness_ms = allowed_lateness_ms
        self._sources: Dict[str, _SourceState] = {}
        self._rev = 0

    # ------------------------------------------------------------------ API

    def feed(self, event: Event) -> Revision:
        """Apply one event; returns its revision with the changelog."""
        return self.apply([event]).revisions[0]

    def retract(self, source: str, seq: int) -> Revision:
        """Apply one retraction addressed by ``(source, seq)``."""
        return self.apply([Retract(source, seq)]).revisions[0]

    def advance_watermark(self, source: str, watermark_ms: int) -> Revision:
        """Advance a source's watermark, closing and reclaiming old windows."""
        return self.apply([AdvanceWatermark(source, watermark_ms)]).revisions[0]

    def apply(self, ops: Iterable[Op]) -> BatchResult:
        """Apply a batch of ops in order; per-op revisions plus the
        concatenated changelog are returned."""
        revisions: List[Revision] = []
        changes: List[Change] = []
        for op in ops:
            rev = self._apply_one(op)
            revisions.append(rev)
            changes.extend(rev.changes)
        return BatchResult(revisions=tuple(revisions), changes=tuple(changes))

    def snapshot(self) -> List[Window]:
        """Current open windows across all sources, sorted by (source, start)."""
        out: List[Window] = []
        for source in sorted(self._sources):
            for sess in self._sources[source].sessions:
                out.append(sess.to_window())
        return out

    # ------------------------------------------------------------- internals

    def _state(self, source: str) -> _SourceState:
        st = self._sources.get(source)
        if st is None:
            st = self._sources[source] = _SourceState()
        return st

    def _apply_one(self, op: Op) -> Revision:
        self._rev += 1
        if isinstance(op, Event):
            return self._insert(op)
        if isinstance(op, Retract):
            return self._retract(op)
        if isinstance(op, AdvanceWatermark):
            return self._advance(op)
        raise TypeError(f"unsupported op: {op!r}")

    # -- insert -------------------------------------------------------------

    def _insert(self, ev: Event) -> Revision:
        st = self._state(ev.source)
        stats = IndexStats()

        if ev.seq in st.events:
            return Revision(self._rev, ev, Disposition.DUPLICATE, (), stats)
        if ev.event_ms < st.watermark - self.allowed_lateness_ms:
            return Revision(self._rev, ev, Disposition.DROPPED_LATE, (), stats)

        gap = self.gap_ms
        sessions = st.sessions
        # Contiguous run of sessions overlapping [t-gap, t+gap]. Sessions are
        # disjoint and separated by > gap, so binary search + local walk
        # suffices; the rest of the list is never touched.
        hi = bisect_right(sessions, ev.event_ms + gap, key=lambda s: s.start)
        lo = hi
        while lo > 0 and sessions[lo - 1].end >= ev.event_ms - gap:
            lo -= 1
        run = sessions[lo:hi]

        changes: List[Change] = []
        if not run:
            sess = _Session(ev.event_ms, ev.event_ms, [ev])
            sessions.insert(lo, sess)
            changes.append(Change(ChangeKind.UPSERT, sess.id, sess.to_window()))
        else:
            stats.sessions_merged = len(run)
            members: List[Event] = list(
                heap_merge(*(s.members for s in run), (ev,), key=_member_key)
            )
            stats.members_touched = sum(len(s.members) for s in run) + 1
            merged = _Session(members[0].event_ms, members[-1].event_ms, members)
            sessions[lo:hi] = [merged]
            win = merged.to_window()
            for old in run:
                old_id = _make_id(old)
                if old_id != win.id:
                    changes.append(Change(ChangeKind.RETRACT, old_id))
            changes.append(Change(ChangeKind.UPSERT, win.id, win))

        st.events[ev.seq] = ev
        return Revision(self._rev, ev, Disposition.ACCEPTED, tuple(changes), stats)

    # -- retract ------------------------------------------------------------

    def _retract(self, op: Retract) -> Revision:
        st = self._state(op.source)
        stats = IndexStats()
        ev = st.events.pop(op.seq, None)
        if ev is None:
            return Revision(self._rev, op, Disposition.RETRACT_UNKNOWN, (), stats)

        sessions = st.sessions
        idx = bisect_right(sessions, ev.event_ms, key=lambda s: s.start) - 1
        sess = sessions[idx]
        assert sess.start <= ev.event_ms <= sess.end

        remaining = [m for m in sess.members if _member_key(m) != _member_key(ev)]
        stats.members_touched = len(sess.members)

        changes: List[Change] = []
        old_id = _make_id(sess)
        if not remaining:
            del sessions[idx]
            changes.append(Change(ChangeKind.RETRACT, old_id))
        else:
            # Re-segment only this window's members by the gap rule.
            segments: List[List[Event]] = [[remaining[0]]]
            for m in remaining[1:]:
                if m.event_ms - segments[-1][-1].event_ms <= self.gap_ms:
                    segments[-1].append(m)
                else:
                    segments.append([m])
            stats.segments_produced = len(segments)
            new_sessions = [
                _Session(seg[0].event_ms, seg[-1].event_ms, seg) for seg in segments
            ]
            sessions[idx : idx + 1] = new_sessions
            new_ids = {_make_id(ns) for ns in new_sessions}
            if old_id not in new_ids:
                changes.append(Change(ChangeKind.RETRACT, old_id))
            for ns in new_sessions:
                changes.append(Change(ChangeKind.UPSERT, _make_id(ns), ns.to_window()))

        return Revision(self._rev, op, Disposition.RETRACTED, tuple(changes), stats)

    # -- watermark ----------------------------------------------------------

    def _advance(self, op: AdvanceWatermark) -> Revision:
        st = self._state(op.source)
        stats = IndexStats()
        changes: List[Change] = []
        if op.watermark_ms > st.watermark:
            st.watermark = op.watermark_ms
        # Reclaim on every AdvanceWatermark op, even a non-advancing one: a
        # retraction may have shrunk a window's end below the horizon since
        # the last advance. Sessions are disjoint and sorted by start, so
        # their ends increase monotonically and a prefix scan suffices.
        horizon = st.watermark - self.allowed_lateness_ms
        sessions = st.sessions
        # A window is final once no future event could join it: any event
        # within gap of its end would be older than the lateness horizon.
        cut = 0
        while cut < len(sessions) and sessions[cut].end + self.gap_ms < horizon:
            cut += 1
        for sess in sessions[:cut]:
            win = sess.to_window(closed=True)
            changes.append(Change(ChangeKind.CLOSE, win.id, win))
            for m in sess.members:
                del st.events[m.seq]
            stats.sessions_reclaimed += 1
            stats.events_reclaimed += len(sess.members)
        del sessions[:cut]
        return Revision(self._rev, op, Disposition.WATERMARK, tuple(changes), stats)
