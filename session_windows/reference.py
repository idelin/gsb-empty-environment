"""Independent oracle implementation.

Deliberately naive: it keeps every live event in a flat dict and, on every
query, re-sorts all events per source and recomputes the windows from
scratch.  It shares no code with :class:`SessionWindowIndex`, so the
property tests can cross-check the incremental index (and its changelog)
against a obviously-correct definition.

Window ids are meaningless here (``-1``); equality with the incremental
implementation is defined over ``(source, start_ms, end_ms, members)``.
"""

from __future__ import annotations

from typing import Any, Iterable, Optional, Union

from .core import Disposition, Event, Op, RetractEvent, WindowSnapshot

__all__ = ["ReferenceSessionWindows"]


class ReferenceSessionWindows:
    """Recompute-everything session windows with the same public semantics."""

    def __init__(self, gap_ms: int, allowed_lateness_ms: int = 0) -> None:
        if gap_ms < 0:
            raise ValueError("gap_ms must be >= 0")
        if allowed_lateness_ms < 0:
            raise ValueError("allowed_lateness_ms must be >= 0")
        self._gap = gap_ms
        self._lateness = allowed_lateness_ms
        self._watermark: Optional[int] = None
        self._events: dict[tuple[str, int], tuple[int, Any]] = {}

    @property
    def watermark(self) -> Optional[int]:
        return self._watermark

    def _horizon(self) -> Optional[int]:
        if self._watermark is None:
            return None
        return self._watermark - self._lateness

    def apply(self, op: Op) -> Disposition:
        if isinstance(op, Event):
            key = (op.source, op.seq)
            if key in self._events:
                return Disposition.DUPLICATE
            horizon = self._horizon()
            if horizon is not None and op.event_ms <= horizon:
                return Disposition.REJECTED_LATE
            self._events[key] = (op.event_ms, op.value)
            return Disposition.APPLIED
        if isinstance(op, RetractEvent):
            if self._events.pop((op.source, op.seq), None) is None:
                return Disposition.RETRACTION_UNKNOWN
            return Disposition.RETRACTED
        raise TypeError(f"unsupported op: {op!r}")

    def apply_all(self, ops: Iterable[Op]) -> list[tuple[Op, Disposition]]:
        return [(op, self.apply(op)) for op in ops]

    def advance_watermark(self, watermark_ms: int) -> None:
        if self._watermark is None or watermark_ms > self._watermark:
            self._watermark = watermark_ms
        horizon = self._horizon()
        assert horizon is not None
        # Reclaim events of final windows, mirroring the incremental index.
        for w in self.windows():
            if w.end_ms + self._gap <= horizon:
                for seq, _ms, _val in w.members:
                    del self._events[(w.source, seq)]

    def windows(self) -> list[WindowSnapshot]:
        by_source: dict[str, list[tuple[int, int, Any]]] = {}
        for (source, seq), (ms, val) in self._events.items():
            by_source.setdefault(source, []).append((ms, seq, val))

        out: list[WindowSnapshot] = []
        for source in sorted(by_source):
            items = sorted(by_source[source])  # by (event_ms, seq)
            group = [items[0]]
            groups = [group]
            for prev, item in zip(items, items[1:]):
                if item[0] - prev[0] > self._gap:
                    group = []
                    groups.append(group)
                group.append(item)
            for g in groups:
                members = tuple((seq, ms, val) for ms, seq, val in g)
                out.append(
                    WindowSnapshot(
                        id=-1,
                        source=source,
                        start_ms=g[0][0],
                        end_ms=g[-1][0],
                        members=members,
                    )
                )
        return out
