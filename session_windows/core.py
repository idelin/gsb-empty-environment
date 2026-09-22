"""Incremental session-window store.

Maintains session windows per source under out-of-order inserts and
retractions, emitting a retract/upsert change stream.  See README.md for the
window-id continuity rules and the per-revision index-update guarantees.
"""

from __future__ import annotations

import sys
from bisect import bisect_left, bisect_right, insort
from dataclasses import dataclass, field
from typing import Iterable

from .types import (
    ApplyResult,
    BatchResult,
    Change,
    CloseWindow,
    Disposition,
    Event,
    Member,
    Operation,
    Retract,
    RetractWindow,
    Upsert,
    Window,
)

__all__ = ["SessionWindowStore"]

_MAX_SEQ = sys.maxsize


@dataclass(slots=True)
class _Session:
    """Internal mutable session.  ``members`` is sorted by (event_ms, seq)."""

    id: int
    start_ms: int
    end_ms: int
    members: list[tuple[int, int]] = field(default_factory=list)


class _SourceState:
    """Per-source indexes.

    * ``events``   : seq -> (event_ms, value)            -- dedup / retraction lookup
    * ``times``    : sorted list of (event_ms, seq)      -- time index of live events
    * ``sessions`` : list of _Session sorted by start_ms; consecutive sessions
                     are always separated by strictly more than ``gap_ms``
    """

    __slots__ = ("events", "times", "sessions", "watermark_ms")

    def __init__(self) -> None:
        self.events: dict[int, tuple[int, object]] = {}
        self.times: list[tuple[int, int]] = []
        self.sessions: list[_Session] = []
        self.watermark_ms: int | None = None


class SessionWindowStore:
    """Session-window engine.

    :param gap_ms: two events of the same source belong to one session when
        adjacent event times differ by at most ``gap_ms``.
    :param allowed_lateness_ms: records with
        ``event_ms < watermark - allowed_lateness_ms`` are rejected with
        :attr:`Disposition.LATE_REJECTED`; sessions that can no longer be
        touched by any admissible record are reclaimed and reported via
        :class:`CloseWindow`.
    """

    def __init__(self, gap_ms: int, allowed_lateness_ms: int = 0) -> None:
        if gap_ms < 0:
            raise ValueError("gap_ms must be >= 0")
        if allowed_lateness_ms < 0:
            raise ValueError("allowed_lateness_ms must be >= 0")
        self._gap = gap_ms
        self._lateness = allowed_lateness_ms
        self._sources: dict[str, _SourceState] = {}
        self._next_id = 1

    # ------------------------------------------------------------------
    # public API
    # ------------------------------------------------------------------

    def apply(self, op: Operation) -> ApplyResult:
        """Apply one event or retraction."""
        if isinstance(op, Event):
            return self._insert(op)
        if isinstance(op, Retract):
            return self._retract(op)
        raise TypeError(f"unsupported operation: {op!r}")

    def apply_batch(self, ops: Iterable[Operation]) -> BatchResult:
        """Apply a batch in order; equivalent to calling :meth:`apply` per op."""
        return BatchResult(tuple(self.apply(op) for op in ops))

    def advance_watermark(self, source: str, watermark_ms: int) -> tuple[Change, ...]:
        """Advance the watermark of ``source`` and reclaim expired state.

        Returns a :class:`CloseWindow` change per reclaimed session, in
        ascending start order.  Non-monotonic watermarks are ignored.
        """
        st = self._sources.setdefault(source, _SourceState())
        if st.watermark_ms is not None and watermark_ms <= st.watermark_ms:
            return ()
        st.watermark_ms = watermark_ms
        horizon = watermark_ms - self._lateness

        changes: list[Change] = []
        cutoff: int | None = None
        # Sessions are sorted by start_ms; because they are disjoint and
        # ordered, end_ms is increasing too, so expired sessions form a prefix.
        while st.sessions and st.sessions[0].end_ms + self._gap < horizon:
            sess = st.sessions.pop(0)
            cutoff = sess.end_ms
            changes.append(CloseWindow(self._window(st, sess, source)))
            for _, seq in sess.members:
                del st.events[seq]
        if cutoff is not None:
            # Members of reclaimed sessions are exactly the prefix of the time
            # index with event_ms <= cutoff (live sessions start later).
            drop = bisect_right(st.times, (cutoff, _MAX_SEQ))
            del st.times[:drop]
        return tuple(changes)

    def snapshot(self) -> list[Window]:
        """Current live windows, sorted by (source, start_ms)."""
        out: list[Window] = []
        for source in sorted(self._sources):
            st = self._sources[source]
            for sess in st.sessions:
                out.append(self._window(st, sess, source))
        return out

    def stats(self) -> dict[str, dict[str, int]]:
        """Retained-state sizes per source (events, time-index entries, sessions)."""
        return {
            src: {
                "events": len(st.events),
                "times": len(st.times),
                "sessions": len(st.sessions),
                "watermark_ms": st.watermark_ms,
            }
            for src, st in sorted(self._sources.items())
        }

    def windows(self, source: str) -> list[Window]:
        """Current live windows of one source, sorted by start_ms."""
        st = self._sources.get(source)
        if st is None:
            return []
        return [self._window(st, s, source) for s in st.sessions]

    # ------------------------------------------------------------------
    # insert
    # ------------------------------------------------------------------

    def _insert(self, ev: Event) -> ApplyResult:
        st = self._sources.setdefault(ev.source, _SourceState())

        existing = st.events.get(ev.seq)
        if existing is not None:
            if existing == (ev.event_ms, ev.value):
                return ApplyResult(ev, Disposition.DUPLICATE)
            return ApplyResult(ev, Disposition.CONFLICT)

        if st.watermark_ms is not None:
            horizon = st.watermark_ms - self._lateness
            if ev.event_ms < horizon:
                return ApplyResult(ev, Disposition.LATE_REJECTED)

        st.events[ev.seq] = (ev.event_ms, ev.value)
        insort(st.times, (ev.event_ms, ev.seq))

        sessions = st.sessions
        t = ev.event_ms
        # First session with start_ms > t; the event can only interact with
        # the sessions immediately before and at that position.
        i = bisect_right(sessions, t, key=lambda s: s.start_ms)
        left = sessions[i - 1] if i > 0 else None
        right = sessions[i] if i < len(sessions) else None
        touch_left = left is not None and t - left.end_ms <= self._gap
        touch_right = right is not None and right.start_ms - t <= self._gap

        if touch_left and touch_right:
            # Merge: the window with the smaller start keeps its id.
            assert left is not None and right is not None
            left.members.append((t, ev.seq))
            left.members.extend(right.members)
            left.end_ms = right.end_ms
            sessions.pop(i)
            changes: tuple[Change, ...] = (
                RetractWindow(right.id, ev.source, right.start_ms, right.end_ms),
                Upsert(self._window(st, left, ev.source)),
            )
        elif touch_left:
            assert left is not None
            insort(left.members, (t, ev.seq))
            left.start_ms = min(left.start_ms, t)
            left.end_ms = max(left.end_ms, t)
            changes = (Upsert(self._window(st, left, ev.source)),)
        elif touch_right:
            assert right is not None
            insort(right.members, (t, ev.seq))
            right.start_ms = min(right.start_ms, t)
            right.end_ms = max(right.end_ms, t)
            changes = (Upsert(self._window(st, right, ev.source)),)
        else:
            sess = _Session(self._fresh_id(), t, t, [(t, ev.seq)])
            sessions.insert(i, sess)
            changes = (Upsert(self._window(st, sess, ev.source)),)
        return ApplyResult(ev, Disposition.APPLIED, changes)

    # ------------------------------------------------------------------
    # retract
    # ------------------------------------------------------------------

    def _retract(self, op: Retract) -> ApplyResult:
        st = self._sources.get(op.source)
        rec = st.events.get(op.seq) if st is not None else None
        if st is None or rec is None:
            # Unknown, already retracted, or already reclaimed by the watermark.
            return ApplyResult(op, Disposition.RETRACT_UNKNOWN)
        t, _ = rec

        del st.events[op.seq]
        del st.times[bisect_left(st.times, (t, op.seq))]

        sessions = st.sessions
        j = bisect_right(sessions, t, key=lambda s: s.start_ms) - 1
        sess = sessions[j]
        del sess.members[bisect_left(sess.members, (t, op.seq))]

        if not sess.members:
            sessions.pop(j)
            return ApplyResult(
                op,
                Disposition.RETRACTED,
                (RetractWindow(sess.id, op.source, sess.start_ms, sess.end_ms),),
            )

        # Re-segment only this session's members; all other sessions are
        # unaffected by the removal.
        segments: list[list[tuple[int, int]]] = [[sess.members[0]]]
        for m in sess.members[1:]:
            if m[0] - segments[-1][-1][0] <= self._gap:
                segments[-1].append(m)
            else:
                segments.append([m])

        if len(segments) == 1:
            sess.start_ms = sess.members[0][0]
            sess.end_ms = sess.members[-1][0]
            return ApplyResult(
                op, Disposition.RETRACTED, (Upsert(self._window(st, sess, op.source)),)
            )

        # Split: the fragment holding the original start keeps the id.
        changes: list[Change] = []
        sess.members = segments[0]
        sess.start_ms = segments[0][0][0]
        sess.end_ms = segments[0][-1][0]
        changes.append(Upsert(self._window(st, sess, op.source)))
        for k, seg in enumerate(segments[1:], start=1):
            frag = _Session(self._fresh_id(), seg[0][0], seg[-1][0], list(seg))
            sessions.insert(j + k, frag)
            changes.append(Upsert(self._window(st, frag, op.source)))
        return ApplyResult(op, Disposition.RETRACTED, tuple(changes))

    # ------------------------------------------------------------------
    # helpers
    # ------------------------------------------------------------------

    def _fresh_id(self) -> int:
        wid = self._next_id
        self._next_id += 1
        return wid

    @staticmethod
    def _window(st: _SourceState, sess: _Session, source: str) -> Window:
        members = tuple(
            Member(seq=seq, event_ms=ems, value=st.events[seq][1])
            for ems, seq in sess.members
        )
        return Window(
            id=sess.id,
            source=source,
            start_ms=sess.start_ms,
            end_ms=sess.end_ms,
            members=members,
        )
