"""Reference implementation: correctness by brute force.

Keeps only the live event set and per-source watermarks. After every op it
re-sorts all events and recomputes every window from scratch, then diffs the
previous window map against the new one to derive the same retract/upsert
changelog contract as the incremental engine. Used as the differential-test
oracle; intentionally simple and O(n log n) per op.
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
    Op,
    Retract,
    Revision,
    Window,
    _member_key,
)


class ReferenceSessionWindows:
    def __init__(self, gap_ms: int, allowed_lateness_ms: int = 0):
        self.gap_ms = gap_ms
        self.allowed_lateness_ms = allowed_lateness_ms
        self._events: Dict[Tuple[str, int], Event] = {}
        self._watermarks: Dict[str, int] = {}
        self._windows: Dict[str, Window] = {}
        self._rev = 0

    def apply(self, ops: Iterable[Op]) -> BatchResult:
        revisions: List[Revision] = []
        changes: List[Change] = []
        for op in ops:
            rev = self._apply_one(op)
            revisions.append(rev)
            changes.extend(rev.changes)
        return BatchResult(revisions=tuple(revisions), changes=tuple(changes))

    def snapshot(self) -> List[Window]:
        return sorted(self._windows.values(), key=lambda w: (w.source, w.start_ms))

    # ------------------------------------------------------------------

    def _watermark(self, source: str) -> int:
        return self._watermarks.get(source, -(2**63))

    def _apply_one(self, op: Op) -> Revision:
        self._rev += 1
        if isinstance(op, Event):
            if (op.source, op.seq) in self._events:
                return Revision(self._rev, op, Disposition.DUPLICATE)
            if op.event_ms < self._watermark(op.source) - self.allowed_lateness_ms:
                return Revision(self._rev, op, Disposition.DROPPED_LATE)
            self._events[(op.source, op.seq)] = op
            disposition = Disposition.ACCEPTED
        elif isinstance(op, Retract):
            if self._events.pop((op.source, op.seq), None) is None:
                return Revision(self._rev, op, Disposition.RETRACT_UNKNOWN)
            disposition = Disposition.RETRACTED
        elif isinstance(op, AdvanceWatermark):
            if op.watermark_ms > self._watermark(op.source):
                self._watermarks[op.source] = op.watermark_ms
            disposition = Disposition.WATERMARK
        else:
            raise TypeError(f"unsupported op: {op!r}")

        reclaim_source = op.source if isinstance(op, AdvanceWatermark) else None
        open_windows, closed_windows = self._recompute(reclaim_source)
        changes: List[Change] = []
        for wid in self._windows:
            if wid not in open_windows:
                if wid in closed_windows:
                    changes.append(Change(ChangeKind.CLOSE, wid, closed_windows[wid]))
                else:
                    changes.append(Change(ChangeKind.RETRACT, wid))
        for wid, win in open_windows.items():
            if self._windows.get(wid) != win:
                changes.append(Change(ChangeKind.UPSERT, wid, win))
        self._windows = open_windows
        return Revision(self._rev, op, disposition, tuple(changes))

    def _recompute(
        self, reclaim_source: Optional[str]
    ) -> Tuple[Dict[str, Window], Dict[str, Window]]:
        """Returns (open windows, newly closed windows).

        A window is closed once ``end + gap < watermark - allowed_lateness``.
        Reclamation is lazy: it only happens when the watermark advances,
        matching the incremental engine. Closing is final — closed windows
        are dropped from the live set and their events purged, so later
        retractions of them report RETRACT_UNKNOWN.
        """
        by_source: Dict[str, List[Event]] = {}
        for ev in self._events.values():
            by_source.setdefault(ev.source, []).append(ev)

        open_windows: Dict[str, Window] = {}
        closed_windows: Dict[str, Window] = {}
        horizon = {
            s: wm - self.allowed_lateness_ms for s, wm in self._watermarks.items()
        }
        for source, events in by_source.items():
            events.sort(key=_member_key)
            groups: List[List[Event]] = [[events[0]]]
            for ev in events[1:]:
                if ev.event_ms - groups[-1][-1].event_ms <= self.gap_ms:
                    groups[-1].append(ev)
                else:
                    groups.append([ev])
            for g in groups:
                win = Window(
                    id=Window.make_id(source, g[0].event_ms),
                    source=source,
                    start_ms=g[0].event_ms,
                    end_ms=g[-1].event_ms,
                    members=tuple(g),
                )
                if source == reclaim_source and g[-1].event_ms + self.gap_ms < horizon.get(
                    source, -(2**63)
                ):
                    closed = Window(
                        id=win.id, source=win.source, start_ms=win.start_ms,
                        end_ms=win.end_ms, members=win.members, closed=True,
                    )
                    closed_windows[win.id] = closed
                    for m in g:
                        del self._events[(m.source, m.seq)]
                else:
                    open_windows[win.id] = win
        return open_windows, closed_windows
