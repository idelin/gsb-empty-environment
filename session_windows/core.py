"""Incremental session-window index with a retract/upsert changelog.

Semantics
---------
* An event is identified by ``(source, seq)`` and carries ``event_ms`` and a
  ``value``.  A retraction is a separate operation that points at the same
  ``(source, seq)`` identifier.
* For one source, two adjacent (in time) events belong to the same session
  window iff their gap is ``<= gap_ms``.
* The index never returns "the current window list" as its primary output;
  every mutation returns a *changelog* of ``Upsert`` / ``RetractWindow`` /
  ``CloseWindow`` records so a downstream consumer can maintain its own
  materialised view without ambiguity.

Window-id continuation rules
----------------------------
* A brand-new window gets a fresh, never-reused monotonically increasing id.
* Merge: when one event bridges k existing windows, the window covering the
  *earliest* timestamp keeps its id; the other k-1 ids are retracted and the
  surviving id is upserted with the merged membership.
* Split: when a retraction breaks a window into fragments, the fragment
  containing the *earliest* event keeps the original id; every other fragment
  gets a fresh id.  All fragments are emitted as upserts.
* Close: once ``watermark - allowed_lateness >= window.end_ms + gap_ms`` no
  admissible future event can still join the window, so it is emitted as
  ``CloseWindow`` (final content) and its state is reclaimed.

Lateness
--------
An event is rejected with ``Disposition.REJECTED_LATE`` when
``event_ms <= watermark - allowed_lateness_ms``.  Retractions against events
that never existed or whose state was already reclaimed return
``Disposition.RETRACTION_UNKNOWN``.  Re-delivered live events return
``Disposition.DUPLICATE`` and are ignored (idempotent).

Index structures (per source) and per-revision update cost
----------------------------------------------------------
* ``events: dict[seq -> (event_ms, value)]`` -- O(1) duplicate detection and
  retraction lookup.
* ``starts`` / ``windows``: windows kept sorted by ``start_ms`` (they are
  disjoint, so ends are sorted too).  ``bisect`` locates the affected
  neighbourhood in O(log w); because windows are separated by more than
  ``gap_ms``, at most two existing windows can intersect
  ``[event_ms - gap, event_ms + gap]``, so an insert touches O(1) windows.
* ``seq_window: dict[seq -> window_id]`` -- O(1) location of the window that
  owns a retracted event; a split only scans the members of that one window.
* Watermark advance pops a *prefix* of the sorted window list (closable
  windows are exactly the prefix whose ``end_ms + gap_ms <= horizon``), so
  reclamation costs O(#closed) with no scan of retained history.

No operation ever scans the full retained history.
"""

from __future__ import annotations

import bisect
from dataclasses import dataclass
from enum import Enum
from typing import Any, Iterable, Optional, Union

__all__ = [
    "Event",
    "RetractEvent",
    "Op",
    "Disposition",
    "WindowSnapshot",
    "Upsert",
    "RetractWindow",
    "CloseWindow",
    "Change",
    "SessionWindowIndex",
]


# ---------------------------------------------------------------------------
# Public data model
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class Event:
    """An observed activity record, uniquely identified by (source, seq)."""

    source: str
    seq: int
    event_ms: int
    value: Any = None


@dataclass(frozen=True)
class RetractEvent:
    """Withdraws the event previously identified by (source, seq)."""

    source: str
    seq: int


Op = Union[Event, RetractEvent]


class Disposition(Enum):
    """Explicit per-operation outcome."""

    APPLIED = "applied"  # event inserted
    DUPLICATE = "duplicate"  # (source, seq) already live; ignored
    REJECTED_LATE = "rejected_late"  # beyond allowed lateness; not applied
    RETRACTED = "retracted"  # retraction applied
    RETRACTION_UNKNOWN = "retraction_unknown"  # no such live event


@dataclass(frozen=True)
class WindowSnapshot:
    """Immutable view of one window.

    ``members`` is a tuple of ``(seq, event_ms, value)`` sorted by
    ``(event_ms, seq)`` so snapshots compare deterministically.
    """

    id: int
    source: str
    start_ms: int
    end_ms: int
    members: tuple


@dataclass(frozen=True)
class Upsert:
    """Insert or replace the window with this id downstream."""

    window: WindowSnapshot


@dataclass(frozen=True)
class RetractWindow:
    """Delete ``window_id`` downstream; ``last`` is its final content."""

    window_id: int
    source: str
    last: WindowSnapshot


@dataclass(frozen=True)
class CloseWindow:
    """Window is final (no admissible event can still join); state reclaimed."""

    window: WindowSnapshot


Change = Union[Upsert, RetractWindow, CloseWindow]


# ---------------------------------------------------------------------------
# Internal mutable state
# ---------------------------------------------------------------------------


class _Window:
    __slots__ = ("id", "start", "end", "members")

    def __init__(self, wid: int, start: int, end: int, members: dict):
        self.id = wid
        self.start = start
        self.end = end
        self.members = members  # seq -> (event_ms, value)


class _SourceState:
    __slots__ = ("starts", "windows", "by_id", "events", "seq_window")

    def __init__(self) -> None:
        self.starts: list[int] = []  # window start_ms, sorted, aligned with windows
        self.windows: list[_Window] = []  # disjoint => ends are sorted too
        self.by_id: dict[int, _Window] = {}
        self.events: dict[int, tuple[int, Any]] = {}  # seq -> (event_ms, value)
        self.seq_window: dict[int, int] = {}  # seq -> window id


# ---------------------------------------------------------------------------
# The index
# ---------------------------------------------------------------------------


class SessionWindowIndex:
    """Incremental session-window index.

    Parameters
    ----------
    gap_ms:
        Maximum inactivity gap; adjacent events of one source further apart
        than this belong to different windows.
    allowed_lateness_ms:
        How far behind the watermark an event may still be accepted.
    """

    def __init__(self, gap_ms: int, allowed_lateness_ms: int = 0) -> None:
        if gap_ms < 0:
            raise ValueError("gap_ms must be >= 0")
        if allowed_lateness_ms < 0:
            raise ValueError("allowed_lateness_ms must be >= 0")
        self._gap = gap_ms
        self._lateness = allowed_lateness_ms
        self._watermark: Optional[int] = None
        self._sources: dict[str, _SourceState] = {}
        self._next_id = 1

    # -- public API ------------------------------------------------------

    @property
    def watermark(self) -> Optional[int]:
        return self._watermark

    @property
    def gap_ms(self) -> int:
        return self._gap

    @property
    def allowed_lateness_ms(self) -> int:
        return self._lateness

    def apply(self, op: Op) -> tuple[Disposition, list[Change]]:
        """Apply one operation; return its disposition and the changelog."""
        if isinstance(op, Event):
            return self._apply_event(op)
        if isinstance(op, RetractEvent):
            return self._apply_retract(op)
        raise TypeError(f"unsupported op: {op!r}")

    def apply_all(
        self, ops: Iterable[Op]
    ) -> list[tuple[Op, Disposition, list[Change]]]:
        """Batch interface: apply operations in order, one result per op."""
        results = []
        for op in ops:
            disposition, changes = self.apply(op)
            results.append((op, disposition, changes))
        return results

    def advance_watermark(self, watermark_ms: int) -> list[Change]:
        """Advance the watermark (monotonic) and reclaim final windows.

        Returns one ``CloseWindow`` per reclaimed window.  A regressive
        watermark is ignored and yields no changes.
        """
        if self._watermark is not None and watermark_ms <= self._watermark:
            return []
        self._watermark = watermark_ms
        horizon = self._horizon()
        assert horizon is not None
        changes: list[Change] = []
        for source in sorted(self._sources):
            changes.extend(self._reclaim(self._sources[source], source, horizon))
        return changes

    def windows(self, source: Optional[str] = None) -> list[WindowSnapshot]:
        """Current live windows (all sources, or one)."""
        out: list[WindowSnapshot] = []
        names = [source] if source is not None else sorted(self._sources)
        for name in names:
            ss = self._sources.get(name)
            if ss is not None:
                out.extend(self._snapshot(w, name) for w in ss.windows)
        return out

    def stats(self) -> dict:
        """Sizes of the retained state (useful to observe reclamation)."""
        return {
            "sources": len(self._sources),
            "windows": sum(len(ss.windows) for ss in self._sources.values()),
            "events": sum(len(ss.events) for ss in self._sources.values()),
            "watermark": self._watermark,
        }

    # -- event insertion ---------------------------------------------------

    def _apply_event(self, ev: Event) -> tuple[Disposition, list[Change]]:
        ss = self._sources.setdefault(ev.source, _SourceState())
        if ev.seq in ss.events:
            return Disposition.DUPLICATE, []
        horizon = self._horizon()
        if horizon is not None and ev.event_ms <= horizon:
            return Disposition.REJECTED_LATE, []

        gap = self._gap
        lo = ev.event_ms - gap
        hi = ev.event_ms + gap

        # Windows are disjoint and separated by > gap, so at most two of them
        # can intersect [lo, hi]; bisect finds the neighbourhood directly.
        i = bisect.bisect_right(ss.starts, hi) - 1
        affected: list[int] = []
        while i >= 0 and ss.windows[i].end >= lo:
            affected.append(i)
            i -= 1
        affected.reverse()

        if not affected:
            pos = bisect.bisect_right(ss.starts, ev.event_ms)
            win = _Window(
                self._fresh_id(),
                ev.event_ms,
                ev.event_ms,
                {ev.seq: (ev.event_ms, ev.value)},
            )
            ss.starts.insert(pos, win.start)
            ss.windows.insert(pos, win)
            ss.by_id[win.id] = win
            ss.events[ev.seq] = (ev.event_ms, ev.value)
            ss.seq_window[ev.seq] = win.id
            return Disposition.APPLIED, [Upsert(self._snapshot(win, ev.source))]

        first, last = affected[0], affected[-1]
        group = ss.windows[first : last + 1]

        changes: list[Change] = []
        for old in group[1:]:
            changes.append(
                RetractWindow(old.id, ev.source, self._snapshot(old, ev.source))
            )
            del ss.by_id[old.id]

        # The earliest window of the merged group keeps its id.
        survivor = group[0]
        members: dict[int, tuple[int, Any]] = {}
        for old in group:
            members.update(old.members)
        members[ev.seq] = (ev.event_ms, ev.value)
        survivor.members = members
        survivor.start = min(ev.event_ms, survivor.start)
        survivor.end = max(ev.event_ms, group[-1].end)

        del ss.starts[first + 1 : last + 1]
        del ss.windows[first + 1 : last + 1]
        ss.starts[first] = survivor.start

        ss.events[ev.seq] = (ev.event_ms, ev.value)
        for seq in members:
            ss.seq_window[seq] = survivor.id

        changes.append(Upsert(self._snapshot(survivor, ev.source)))
        return Disposition.APPLIED, changes

    # -- event retraction --------------------------------------------------

    def _apply_retract(self, op: RetractEvent) -> tuple[Disposition, list[Change]]:
        ss = self._sources.get(op.source)
        if ss is None or op.seq not in ss.events:
            return Disposition.RETRACTION_UNKNOWN, []

        win = ss.by_id[ss.seq_window[op.seq]]
        idx = bisect.bisect_left(ss.starts, win.start)
        last_snapshot = self._snapshot(win, op.source)

        del ss.events[op.seq]
        del ss.seq_window[op.seq]
        del win.members[op.seq]

        if not win.members:
            del ss.starts[idx]
            del ss.windows[idx]
            del ss.by_id[win.id]
            return Disposition.RETRACTED, [
                RetractWindow(win.id, op.source, last_snapshot)
            ]

        items = sorted(win.members.items(), key=lambda kv: (kv[1][0], kv[0]))
        fragments = [[items[0]]]
        for prev, item in zip(items, items[1:]):
            if item[1][0] - prev[1][0] > self._gap:
                fragments.append([])
            fragments[-1].append(item)

        if len(fragments) == 1:
            # No split: the window shrinks in place and keeps its id.  Its
            # start can only grow and its end can only shrink, so the sorted
            # order of the window lists is preserved.
            win.start = items[0][1][0]
            win.end = items[-1][1][0]
            ss.starts[idx] = win.start
            return Disposition.RETRACTED, [Upsert(self._snapshot(win, op.source))]

        # Split: the earliest fragment keeps the original id, the rest get
        # fresh ids.  Fragment starts stay within (old_start, old_end], so
        # splicing them in at idx keeps both lists sorted.
        frag_windows: list[_Window] = []
        for k, frag in enumerate(fragments):
            members = {seq: ms_val for seq, ms_val in frag}
            start = frag[0][1][0]
            end = frag[-1][1][0]
            if k == 0:
                win.members = members
                win.start = start
                win.end = end
                frag_win = win
            else:
                frag_win = _Window(self._fresh_id(), start, end, members)
                ss.by_id[frag_win.id] = frag_win
            for seq in members:
                ss.seq_window[seq] = frag_win.id
            frag_windows.append(frag_win)

        ss.starts[idx : idx + 1] = [w.start for w in frag_windows]
        ss.windows[idx : idx + 1] = frag_windows
        changes = [Upsert(self._snapshot(w, op.source)) for w in frag_windows]
        return Disposition.RETRACTED, changes

    # -- watermark / reclamation --------------------------------------------

    def _reclaim(
        self, ss: _SourceState, source: str, horizon: int
    ) -> list[Change]:
        # Closable windows are exactly a prefix of the sorted window list.
        n = 0
        for w in ss.windows:
            if w.end + self._gap <= horizon:
                n += 1
            else:
                break
        if n == 0:
            return []
        changes: list[Change] = []
        for w in ss.windows[:n]:
            changes.append(CloseWindow(self._snapshot(w, source)))
            del ss.by_id[w.id]
            for seq in w.members:
                del ss.events[seq]
                del ss.seq_window[seq]
        del ss.starts[:n]
        del ss.windows[:n]
        return changes

    # -- helpers -------------------------------------------------------------

    def _horizon(self) -> Optional[int]:
        if self._watermark is None:
            return None
        return self._watermark - self._lateness

    def _fresh_id(self) -> int:
        wid = self._next_id
        self._next_id += 1
        return wid

    @staticmethod
    def _snapshot(win: _Window, source: str) -> WindowSnapshot:
        members = tuple(
            sorted(
                ((seq, ms, val) for seq, (ms, val) in win.members.items()),
                key=lambda t: (t[1], t[0]),
            )
        )
        return WindowSnapshot(
            id=win.id,
            source=source,
            start_ms=win.start,
            end_ms=win.end,
            members=members,
        )
